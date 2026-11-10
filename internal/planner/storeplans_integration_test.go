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

// Integration test against a REAL pg_store_plans installation
// (https://github.com/ossc-db/pg_store_plans), not the fake *sql.DB
// planner_test.go exercises CapturePlanFromStorePlans against — unlike
// planner_integration_test.go's GENERIC_PLAN coverage (which runs against
// the plain, officially-supported postgres:16/17/18 images), pg_store_plans
// is a third-party C extension none of those images ship, so this test
// needs a server that actually has it compiled in.
//
// scripts/pg_store_plans builds exactly such a server (a real PostgreSQL
// 17, with the real upstream pg_store_plans source compiled via PGXS and
// loaded via shared_preload_libraries — see that directory's Dockerfile for
// why it isn't just `FROM postgres:16/17/18`). Run it and point this test
// at it with its own, separate env var (PGRR_STOREPLANS_TEST_DSN, not
// PGRR_TEST_DSN) so it can never be silently satisfied by a plain
// postgres:* container that doesn't have the extension at all:
//
//	docker build -t pgrr-pg-store-plans scripts/pg_store_plans
//	docker run --rm -d --name pgrr-storeplans-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test pgrr-pg-store-plans
//	export PGRR_STOREPLANS_TEST_DSN="postgres://user:password@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration -run StorePlans ./internal/planner/...
package planner_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/joao00001/pg-regression-radar/internal/planner"
)

func TestIntegration_CapturePlanFromStorePlans_RealExtension(t *testing.T) {
	dsn := os.Getenv("PGRR_STOREPLANS_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_STOREPLANS_TEST_DSN not set; skipping real pg_store_plans integration test — see this file's doc comment for how to build and start one")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Fail loudly rather than skip if PGRR_STOREPLANS_TEST_DSN is set but
	// doesn't actually point at a server with a reliable pg_store_plans —
	// unlike the "not configured at all" case above, this means the test
	// fixture is broken and silently falling back to generic_plan would
	// defeat the entire point of this test existing.
	var extversion string
	if err := db.QueryRowContext(ctx,
		`SELECT extversion FROM pg_extension WHERE extname = 'pg_store_plans'`,
	).Scan(&extversion); err != nil {
		t.Fatalf("PGRR_STOREPLANS_TEST_DSN is set but pg_store_plans is not installed there (%v) — this test requires a server built from scripts/pg_store_plans, not a plain postgres:* image", err)
	}
	t.Logf("real pg_store_plans %s detected at %s", extversion, dsn)

	if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS pgrr_storeplans_probe`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE pgrr_storeplans_probe (id INT PRIMARY KEY, val TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS pgrr_storeplans_probe`)
	})
	if _, err := db.ExecContext(ctx, `INSERT INTO pgrr_storeplans_probe SELECT g, 'v' || g FROM generate_series(1, 1000) g`); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE pgrr_storeplans_probe`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// Mirrors exactly what internal/collector does in production: a real
	// parameterized query actually executes (several times, so
	// pg_store_plans has a settled last_call row), then its queryid is
	// discovered the same way the collector discovers one — by reading it
	// back out of pg_stat_statements, not by guessing it.
	const probeQuery = `SELECT val FROM pgrr_storeplans_probe WHERE id = $1`
	for i := 0; i < 5; i++ {
		var val string
		if err := db.QueryRowContext(ctx, probeQuery, 42).Scan(&val); err != nil {
			t.Fatalf("execute probe query: %v", err)
		}
	}

	var queryID int64
	if err := db.QueryRowContext(ctx,
		`SELECT queryid FROM pg_stat_statements WHERE query = $1`, probeQuery,
	).Scan(&queryID); err != nil {
		t.Fatalf("look up queryid for probe query in pg_stat_statements: %v", err)
	}
	t.Logf("probe query's real pg_stat_statements queryid: %d", queryID)

	t.Run("CapturePlanFromStorePlans reads the real, previously-executed plan", func(t *testing.T) {
		snap, err := planner.CapturePlanFromStorePlans(ctx, db, queryID)
		if err != nil {
			t.Fatalf("CapturePlanFromStorePlans: %v", err)
		}
		if snap.Source != planner.SourceStorePlans {
			t.Errorf("Source = %q, want %q", snap.Source, planner.SourceStorePlans)
		}
		if snap.QueryID != queryID {
			t.Errorf("QueryID = %d, want %d", snap.QueryID, queryID)
		}
		if snap.RootNodeType == "" {
			t.Error("expected a non-empty RootNodeType read from a real pg_store_plans row")
		}
		if snap.TotalCost <= 0 {
			t.Errorf("expected a positive TotalCost for a real indexed lookup, got %.2f", snap.TotalCost)
		}
		if !strings.Contains(snap.PlanJSON, "Index") {
			t.Errorf("expected the real plan JSON to mention the primary key index it used, got: %s", snap.PlanJSON)
		}
		t.Logf("real pg_store_plans plan: node=%s cost=%.2f", snap.RootNodeType, snap.TotalCost)
	})

	t.Run("CapturePlan facade prefers the real plan over GENERIC_PLAN once pg_store_plans has a row", func(t *testing.T) {
		snap, err := planner.CapturePlan(ctx, db, queryID, planner.NormalizeQueryText(probeQuery))
		if err != nil {
			t.Fatalf("CapturePlan: %v", err)
		}
		if snap.Source != planner.SourceStorePlans {
			t.Errorf("Source = %q, want %q (CapturePlan should prefer the real pg_store_plans row over a GENERIC_PLAN estimate)", snap.Source, planner.SourceStorePlans)
		}
	})

	t.Run("Diff between two real pg_store_plans snapshots carries no estimate caution", func(t *testing.T) {
		before, err := planner.CapturePlanFromStorePlans(ctx, db, queryID)
		if err != nil {
			t.Fatalf("CapturePlanFromStorePlans (before): %v", err)
		}
		after, err := planner.CapturePlanFromStorePlans(ctx, db, queryID)
		if err != nil {
			t.Fatalf("CapturePlanFromStorePlans (after): %v", err)
		}
		diff := planner.Diff(before, after)
		if diff == "" {
			t.Fatal("expected a non-empty diff between two real pg_store_plans snapshots")
		}
		if strings.Contains(diff, "caution") {
			t.Errorf("Diff should not warn about estimation when both snapshots are real pg_store_plans captures, got: %s", diff)
		}
		t.Logf("diff between two real pg_store_plans captures of the same queryid: %s", diff)
	})
}
