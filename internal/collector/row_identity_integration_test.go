// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration

package collector

import (
	"context"
	"database/sql"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/testutil"
)

// Exercise the real scrape SQL and driver scan with two independent roles,
// including a reset of only one role's counters. Aggregating cumulative values
// before subtracting would count the other role's old calls again after reset.
func TestIntegration_Scrape_TwoRolesKeepIndependentIntervals(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping Collector integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Close()
	release := testutil.AcquirePGStatStatementsTestLock(t, ctx, setup)
	defer release()
	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"pgrr_identity_a", "pgrr_identity_b"} {
		if _, err := setup.ExecContext(ctx, `CREATE ROLE `+role+` NOLOGIN`); err != nil {
			t.Fatal(err)
		}
		defer func(role string) {
			if _, err := setup.ExecContext(context.Background(), `DROP ROLE `+role); err != nil {
				t.Errorf("drop test role: %v", err)
			}
		}(role)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatal(err)
	}
	const marker = "pgrr_identity_probe"
	execute := func(role string, n int) {
		t.Helper()
		conn, err := setup.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, `SET ROLE `+role); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := conn.ExecContext(context.Background(), `RESET ROLE`); err != nil {
				t.Errorf("reset test role: %v", err)
			}
		}()
		for i := 0; i < n; i++ {
			if _, err := conn.ExecContext(ctx, `SELECT pg_sleep(0.002) /* `+marker+` */`); err != nil {
				t.Fatal(err)
			}
		}
	}
	c := newTestCollector(t, Config{DSN: dsn, ClusterName: "row-identity-test"})
	defer c.db.Close()
	start := time.Now().Add(-time.Second)
	probe := func(wantSamples int) QuerySample {
		t.Helper()
		if err := c.scrape(ctx); err != nil {
			t.Fatalf("scrape: %v", err)
		}
		for _, qid := range c.AllQueryIDs() {
			samples := c.SamplesInRange(qid, start, time.Now().Add(time.Second))
			if len(samples) == 0 || !strings.Contains(samples[len(samples)-1].QueryText, marker) {
				continue
			}
			if len(samples) != wantSamples {
				t.Fatalf("got %d samples after %d scrapes; want one aggregate per queryid per scrape", len(samples), wantSamples)
			}
			return samples[len(samples)-1]
		}
		t.Fatal("probe query not found")
		return QuerySample{}
	}
	execute("pgrr_identity_a", 5)
	execute("pgrr_identity_b", 2)
	var roles, queryIDs int
	if err := setup.QueryRowContext(ctx, `SELECT count(DISTINCT userid), count(DISTINCT queryid)
		FROM pg_stat_statements WHERE query LIKE $1`, "%"+marker+"%").Scan(&roles, &queryIDs); err != nil {
		t.Fatal(err)
	}
	if roles != 2 || queryIDs != 1 {
		t.Fatalf("fixture must have two roles sharing one queryid; got roles=%d queryids=%d", roles, queryIDs)
	}
	first := probe(1)
	if first.Calls != 7 || first.IntervalKnown || first.ResetDetected {
		t.Fatalf("unexpected initial aggregate: %+v", first)
	}
	execute("pgrr_identity_a", 3)
	execute("pgrr_identity_b", 1)
	second := probe(2)
	if second.Calls != 11 || !second.IntervalKnown || second.IntervalCalls != 4 || second.ResetDetected {
		t.Fatalf("independent role increments must total four calls without reset: %+v", second)
	}
	wantTotal := second.TotalExecTimeMs - first.TotalExecTimeMs
	if math.Abs(second.IntervalTotalMs-wantTotal) > 1e-6 || math.Abs(second.IntervalMeanMs-wantTotal/4) > 1e-6 {
		t.Fatalf("unexpected interval timing: %+v; want total=%v", second, wantTotal)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset(
		(SELECT oid FROM pg_roles WHERE rolname = 'pgrr_identity_a'), 0, 0)`); err != nil {
		t.Fatal(err)
	}
	execute("pgrr_identity_a", 2)
	execute("pgrr_identity_b", 1)
	third := probe(3)
	if third.Calls != 6 || !third.IntervalKnown || third.IntervalCalls != 3 || !third.ResetDetected {
		t.Fatalf("only the reset role restarts from zero; want three interval calls: %+v", third)
	}
	if third.IntervalTotalMs <= 0 || math.Abs(third.IntervalMeanMs-third.IntervalTotalMs/3) > 1e-6 {
		t.Fatalf("reset interval mean must use summed interval totals: %+v", third)
	}
}
