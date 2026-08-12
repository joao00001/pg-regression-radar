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

// Integration test against a real PostgreSQL server with pg_stat_statements
// actually loaded and populated. Every other test in this package feeds the
// Collector synthetic QuerySamples directly (via ingestSample) and never
// touches a real database connection or the real queryStatStatements() SQL,
// so they can't catch a mistake in that SQL itself: a wrong column name, a
// version-detection bug in resolveColumns, or a real driver/scan mismatch.
// This test closes that gap by running the Collector's actual scrape() path
// against a live server.
//
// It lives in `package collector` (not collector_test) specifically so it
// can call the unexported scrape method directly, taking one deterministic
// sample instead of racing a background ticker (see Run's doc comment).
//
// Run it explicitly (skips automatically if PGRR_TEST_DSN is unset, so
// `go test -tags=integration ./...` stays safe without a database):
//
//	docker run --rm -d --name pgrr-collector-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/collector/...
//
// shared_preload_libraries can only be set at server start (ALTER SYSTEM +
// reload is not enough for this particular extension), which is why the
// docker run command above passes it as a postgres(1) flag rather than
// relying on CREATE EXTENSION alone.
package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/planner"
)

func TestIntegration_Scrape_RealPostgres(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping Collector integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()

	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements (is shared_preload_libraries set? see this file's doc comment): %v", err)
	}
	// Reset so this run's assertions aren't polluted by another test (or a
	// previous run against the same long-lived container) sharing calls
	// against the same normalized query text.
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset: %v", err)
	}

	// A query pg_stat_statements will never have seen before in this
	// container, with a deliberate, measurable latency floor (~40ms) and a
	// distinctive marker so we can find it by substring after scraping.
	//
	// The marker MUST be a SQL comment, not a string literal:
	// pg_stat_statements normalizes every constant (numeric AND string) to
	// a $N placeholder in the query text it stores, so a literal marker
	// would be invisible by the time it reaches Collector.SamplesInRange.
	// Comments, unlike literals, survive normalization verbatim — the same
	// property tools like sqlcommenter/marginalia rely on to tag queries
	// for pg_stat_statements. N identical executions (comment and all)
	// collapse into a single queryid with Calls == N.
	const marker = "pgrr_collector_integration_probe"
	const execCount = 5
	for i := 0; i < execCount; i++ {
		if _, err := setup.ExecContext(ctx, fmt.Sprintf(`SELECT pg_sleep(0.04) /* %s */`, marker)); err != nil {
			t.Fatalf("generate probe query #%d: %v", i, err)
		}
	}

	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	c, err := New(Config{
		DSN:         dsn,
		ClusterName: "collector-integration-test",
		Namespace:   "default",
	}, logger, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.db.Close() })

	// Drive the real scrape path once, deterministically, instead of
	// starting Run's background ticker and sleeping.
	if err := c.scrape(ctx); err != nil {
		t.Fatalf("scrape: %v", err)
	}

	var found *QuerySample
	for _, qid := range c.AllQueryIDs() {
		samples := c.SamplesInRange(qid, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
		for i := range samples {
			if strings.Contains(samples[i].QueryText, marker) {
				found = &samples[i]
				break
			}
		}
		if found != nil {
			break
		}
	}

	if found == nil {
		t.Fatal("probe query not found among scraped samples; check queryStatStatements()'s column names/filter against this PostgreSQL version")
	}
	if found.Calls != execCount {
		t.Errorf("expected Calls=%d for the probe query, got %d", execCount, found.Calls)
	}
	// pg_sleep(0.04) floors the real execution time at 40ms; a generous
	// lower bound (25ms) avoids flaking on a loaded CI runner while still
	// catching a genuinely broken timing column (e.g. reading calls instead
	// of mean_exec_time, which would report ~0 or ~5).
	if found.MeanExecTimeMs < 25 {
		t.Errorf("expected MeanExecTimeMs >= 25 (pg_sleep(0.04) floor), got %.2f — likely reading the wrong pg_stat_statements column", found.MeanExecTimeMs)
	}
	if found.Fingerprint == "" {
		t.Error("expected a non-empty Fingerprint on the scraped sample")
	}
}

// TestIntegration_Ping_RealPostgres proves Ping's two real checks (network
// reachability and the pg_stat_statements extension actually being
// installed) against a live server, complementing
// TestPing_UnreachableHost_ReturnsError's fast, database-free failure case
// in collector_test.go. This is exactly the check cmd/operator's and
// cmd/collector's --dry-run flag runs before starting anything for real.
func TestIntegration_Ping_RealPostgres(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping Collector integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()
	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements: %v", err)
	}

	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c, err := New(Config{DSN: dsn, ClusterName: "collector-ping-test", Namespace: "default"}, logger, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.db.Close() })

	if err := c.Ping(ctx); err != nil {
		t.Errorf("Ping against a real, correctly configured Postgres should succeed, got: %v", err)
	}
}

// TestIntegration_CapturePlans_RealPostgres16 proves the Feature 3 wiring
// end to end against a real server: capturePlans (driven here directly,
// rather than via a full scrape cycle, since we want deterministic control
// over exactly which two query texts get captured) populates the plan-
// history ring, PlansAround retrieves both sides, and planner.Diff produces
// a real, non-empty, human-readable summary — proving this isn't just
// "compiles and unit tests pass" but genuinely produces useful output
// against live EXPLAIN (GENERIC_PLAN) results.
//
// The "before"/"after" plans come from two structurally different query
// texts captured under one synthetic queryid, rather than the same query
// text before/after an index drop: forcing a real plan flip via GUC toggles
// (enable_indexscan/enable_seqscan) isn't reliably observable against a
// pooled *sql.DB connection (each query may land on a different backend, so
// a session-level SET doesn't reliably apply to the following query) — see
// this repo's task notes for the explicit "structurally different queries as
// a proxy" allowance.
func TestIntegration_CapturePlans_RealPostgres16(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping Collector CapturePlans integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()

	var versionNum int
	if err := setup.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&versionNum); err != nil {
		t.Fatalf("determine server_version_num: %v", err)
	}
	if versionNum/10000 < 16 {
		t.Skipf("this test requires PostgreSQL 16+ (EXPLAIN GENERIC_PLAN); server reports major version %d", versionNum/10000)
	}

	if _, err := setup.ExecContext(ctx, `DROP TABLE IF EXISTS pgrr_capture_plans_probe`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `CREATE TABLE pgrr_capture_plans_probe (id INT PRIMARY KEY, val TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = setup.ExecContext(context.Background(), `DROP TABLE IF EXISTS pgrr_capture_plans_probe`)
	})
	if _, err := setup.ExecContext(ctx, `INSERT INTO pgrr_capture_plans_probe SELECT g, 'v' || g FROM generate_series(1, 2000) g`); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `ANALYZE pgrr_capture_plans_probe`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	reg := prometheus.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c, err := New(Config{DSN: dsn, ClusterName: "capture-plans-test", Namespace: "default", CapturePlans: true}, logger, reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.db.Close() })

	const syntheticQID = int64(999001)

	// "Before": an indexed primary-key lookup — the planner should pick an
	// Index Scan (or Index Only Scan).
	c.capturePlans(ctx, []trackedQuery{
		{queryID: syntheticQID, queryText: "SELECT val FROM pgrr_capture_plans_probe WHERE id = $1"},
	})
	time.Sleep(20 * time.Millisecond)
	// "After": an unindexed-predicate query over the same table — the
	// planner has no index to use and must fall back to a Seq Scan.
	c.capturePlans(ctx, []trackedQuery{
		{queryID: syntheticQID, queryText: "SELECT val FROM pgrr_capture_plans_probe WHERE val = $1"},
	})

	before, after := c.PlansAround(syntheticQID, time.Now())
	if before == nil || after == nil {
		t.Fatalf("expected both a before and after plan snapshot to be captured, got before=%+v after=%+v", before, after)
	}
	t.Logf("before: node=%s cost=%.2f | after: node=%s cost=%.2f", before.RootNodeType, before.TotalCost, after.RootNodeType, after.TotalCost)

	summary := planner.Diff(before, after)
	if summary == "" {
		t.Fatal("expected a non-empty PlanDiffSummary for an indexed lookup vs. an unindexed-predicate scan")
	}
	t.Logf("PlanDiffSummary: %s", summary)
}
