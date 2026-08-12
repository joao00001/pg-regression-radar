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

// Integration test against a real PostgreSQL server. Unlike the rest of this
// repo's integration tests (which work against any supported PostgreSQL
// major version), this one specifically requires PostgreSQL 16+, since
// EXPLAIN (GENERIC_PLAN) is the exact feature under test — see planner.go's
// CapturePlan doc comment for why. Run it explicitly (skips automatically if
// PGRR_TEST_DSN is unset):
//
//	docker run --rm -d --name pgrr-planner-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/planner/...
package planner_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/joao00001/pg-regression-radar/internal/planner"
)

func TestIntegration_CapturePlan_RealPostgres16(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping planner integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	var versionNum int
	if err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&versionNum); err != nil {
		t.Fatalf("determine server_version_num: %v", err)
	}
	if versionNum/10000 < 16 {
		t.Skipf("this test requires PostgreSQL 16+ (EXPLAIN GENERIC_PLAN); server reports major version %d", versionNum/10000)
	}

	t.Run("trivial query", func(t *testing.T) {
		snap, err := planner.CapturePlan(ctx, db, 1, "SELECT 1")
		if err != nil {
			t.Fatalf("CapturePlan: %v", err)
		}
		if snap.RootNodeType == "" {
			t.Error("expected a non-empty RootNodeType for a trivial SELECT")
		}
		if snap.QueryID != 1 {
			t.Errorf("expected QueryID to be preserved as 1, got %d", snap.QueryID)
		}
		if snap.PlanJSON == "" {
			t.Error("expected PlanJSON to be populated")
		}
		t.Logf("SELECT 1 plan: node=%s cost=%.2f", snap.RootNodeType, snap.TotalCost)
	})

	t.Run("query against a real indexed table", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS pgrr_planner_test_probe`); err != nil {
			t.Fatalf("drop table: %v", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TABLE pgrr_planner_test_probe (id INT PRIMARY KEY, val TEXT)`); err != nil {
			t.Fatalf("create table: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), `DROP TABLE IF EXISTS pgrr_planner_test_probe`)
		})
		if _, err := db.ExecContext(ctx, `INSERT INTO pgrr_planner_test_probe SELECT g, 'v' || g FROM generate_series(1, 1000) g`); err != nil {
			t.Fatalf("seed table: %v", err)
		}
		if _, err := db.ExecContext(ctx, `ANALYZE pgrr_planner_test_probe`); err != nil {
			t.Fatalf("analyze: %v", err)
		}

		// This mirrors exactly what pg_stat_statements would hand back for
		// `SELECT val FROM pgrr_planner_test_probe WHERE id = 42` — already
		// normalized to a "$1" placeholder, which is precisely what
		// GENERIC_PLAN exists to handle.
		snap, err := planner.CapturePlan(ctx, db, 2, "SELECT val FROM pgrr_planner_test_probe WHERE id = $1")
		if err != nil {
			t.Fatalf("CapturePlan: %v", err)
		}
		if snap.RootNodeType == "" {
			t.Error("expected a non-empty RootNodeType")
		}
		if snap.TotalCost <= 0 {
			t.Errorf("expected a positive TotalCost for a real table scan/lookup, got %.2f", snap.TotalCost)
		}
		t.Logf("indexed lookup plan: node=%s cost=%.2f", snap.RootNodeType, snap.TotalCost)

		// A genuinely different plan (sequential scan of the same data via a
		// non-indexed predicate) as a proxy for "plans really do differ" —
		// confirms Diff would have something real to say between two
		// snapshots of the same query whose plan shape changed, without
		// depending on planner cost-estimate specifics that could shift
		// between PostgreSQL point releases.
		snapSeq, err := planner.CapturePlan(ctx, db, 3, "SELECT val FROM pgrr_planner_test_probe WHERE val = $1")
		if err != nil {
			t.Fatalf("CapturePlan (seq scan variant): %v", err)
		}
		diff := planner.Diff(snap, snapSeq)
		if diff == "" {
			t.Fatal("expected a non-empty diff between an indexed-lookup plan and an unindexed-predicate plan")
		}
		t.Logf("diff between indexed lookup and unindexed predicate: %s", diff)
	})
}
