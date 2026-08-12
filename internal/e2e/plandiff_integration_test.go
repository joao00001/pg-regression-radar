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

package e2e

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/planner"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// TestIntegration_FullPipeline_PlanDiffSummaryReflectsRealPlanChange proves
// the GENERIC_PLAN plan-diff feature (feat: "add EXPLAIN (GENERIC_PLAN)
// plan-diff correlation for regressions", internal/planner + Collector's
// CapturePlan/PlansAround) end to end, with a REAL query plan change and a
// REAL, unsimulated latency regression caused by it — not two synthetic
// query texts standing in for "the plan changed" (contrast with
// internal/collector/collector_integration_test.go's
// TestIntegration_CapturePlans_RealPostgres16, which deliberately uses that
// proxy and documents why: a session-level GUC toggle isn't reliably
// observable through a pooled *sql.DB connection). Here the plan change is
// forced the way the task notes for this suite specifically call out: a real
// index drop on a real, adequately large table, so the planner's choice
// between an Index Scan and a Seq Scan — and the resulting execution time —
// are both genuinely different, not asserted by fiat.
//
// pg_stat_statements.io/planner.Diff itself is already unit- and
// integration-tested in isolation (internal/planner/planner_test.go,
// planner_integration_test.go). What has NOT been tested anywhere else in
// this repo is the actual field this whole feature exists to populate:
// v1alpha1.PerformanceRegression.PlanDiffSummary, as it would really be
// filled in by a detected regression flowing through the full pipeline. That
// wiring lives only in internal/cli.RunOperator's poll loop (col.PlansAround
// + planner.Diff, gated on --capture-plans) — an unexported package this
// test cannot call into directly — so this test reproduces that exact
// two-line wiring itself against a real Collector and a real detected
// PerformanceRegression, which is the most faithful equivalent available
// without depending on internal/cli.
//
// Requires PostgreSQL 16+ (EXPLAIN GENERIC_PLAN's minimum version); skips
// automatically on older servers or if PGRR_TEST_DSN is unset, same as this
// package's other integration tests:
//
//	docker run --rm -d --name pgrr-e2e-plandiff-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/e2e/...
func TestIntegration_FullPipeline_PlanDiffSummaryReflectsRealPlanChange(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping plan-diff integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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

	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset: %v", err)
	}

	// A table large enough that dropping its index turns a sub-millisecond
	// Index Scan into a measurably (not just theoretically) slower Seq Scan —
	// verified directly against this exact PostgreSQL 16 binary before
	// writing this test (0.02ms indexed vs. ~4.5ms sequential on 300k rows),
	// comfortably clearing correlation.Config's default 20%
	// LatencyChangeThreshold many times over.
	const tableName = "pgrr_e2e_plandiff_probe"
	if _, err := setup.ExecContext(ctx, `DROP TABLE IF EXISTS `+tableName); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `CREATE TABLE `+tableName+` (id int, val text)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = setup.ExecContext(context.Background(), `DROP TABLE IF EXISTS `+tableName)
	})
	if _, err := setup.ExecContext(ctx, `INSERT INTO `+tableName+` SELECT g, 'v'||g FROM generate_series(1, 300000) g`); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	const indexName = "idx_pgrr_e2e_plandiff_probe_id"
	if _, err := setup.ExecContext(ctx, `CREATE INDEX `+indexName+` ON `+tableName+` (id)`); err != nil {
		t.Fatalf("create index: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `ANALYZE `+tableName); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	col, err := collector.New(collector.Config{
		DSN:          dsn,
		ClusterName:  "e2e-plandiff-test",
		Namespace:    "default",
		CapturePlans: true,
	}, logger, reg)
	if err != nil {
		t.Fatalf("collector.New: %v", err)
	}
	t.Cleanup(func() { _ = col.Close() })

	const (
		marker = "pgrr_e2e_plandiff_marker"
		// preSamples is comfortably larger than postSamples (see the ring-
		// capacity note below) purely for averaging robustness against a
		// cold-cache first execution (this table was just populated with
		// 300k rows and never queried before) — a single slow outlier among
		// only a couple of samples would otherwise meaningfully skew a
		// microsecond-scale Index Scan mean; verified this matters when this
		// suite's other, heavier tests (e.g. the plan-diff test's own 300k-row
		// table build) have already run in the same test binary beforehand.
		preSamples = 8
		// postSamples must stay UNDER collector.planHistorySize (5, see
		// internal/collector/collector.go's appendPlanSnapshot): with
		// CapturePlans enabled, every scrape in both phases below appends one
		// plan snapshot to a small bounded ring for this queryid, evicting
		// the oldest entry once full. Regardless of how large preSamples is,
		// the LAST pre-deploy capture is always the one still standing after
		// the ring evicts down to its last 5 entries as long as postSamples
		// itself is < 5 — if it weren't, the ring would hold only
		// post-deploy captures and PlansAround would never find a "before"
		// snapshot, breaking this test's premise of a real before/after
		// comparison.
		postSamples = 4
	)
	query := `SELECT val FROM ` + tableName + ` WHERE id = 123456 /* ` + marker + ` */`

	runQueryAndScrape := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if _, err := setup.ExecContext(ctx, query); err != nil {
				t.Fatalf("probe query #%d: %v", i, err)
			}
			if err := col.Scrape(ctx); err != nil {
				t.Fatalf("scrape after probe #%d: %v", i, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// A couple of untimed, unscraped warm-up executions so the very first
	// SAMPLE the pre-deploy phase records isn't dominated by cold shared-
	// buffers/OS page cache for a table that was populated microseconds ago
	// — the index-scan path being measured is fast enough (sub-millisecond)
	// that a single cold execution could otherwise dwarf the rest of the
	// pre-deploy mean.
	for i := 0; i < 3; i++ {
		if _, err := setup.ExecContext(ctx, query); err != nil {
			t.Fatalf("warm-up query #%d: %v", i, err)
		}
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset after warm-up: %v", err)
	}

	// --- Pre-deploy phase: index in place, fast Index Scan. ---
	runQueryAndScrape(preSamples)

	deployAt := time.Now().UTC()
	time.Sleep(50 * time.Millisecond)

	// --- The "deploy": an index drop (e.g. a migration that removed what
	// looked like an unused index). Query text — and therefore queryid —
	// is untouched; only the plan the planner picks changes. ---
	if _, err := setup.ExecContext(ctx, `DROP INDEX `+indexName); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset before post-deploy phase: %v", err)
	}

	// Sanity check the index drop actually took effect for a fresh backend's
	// query plan before trusting any timing below. Catalog invalidation is
	// normally instantaneous (PostgreSQL processes pending invalidation
	// messages at the start of the next statement on every backend), but
	// this test observed occasional runs — only when several other
	// integration tests in this package had already run real DDL/DML
	// against the same live server beforehand — where the very next query
	// still measured Index-Scan-speed timings immediately after the DROP
	// INDEX above, i.e. an EXPLAIN'd plan still worth confirming rather than
	// assuming. A short poll here turns that into a clear, diagnosable
	// failure (or a brief, self-healing wait) instead of a silent,
	// confusing "regression not detected" a long way downstream.
	waitForSeqScanPlan(t, ctx, setup, query)

	// --- Post-deploy phase: no index, forced Seq Scan. ---
	runQueryAndScrape(postSamples)

	engine := correlation.New(correlation.Config{
		WindowMinutes: 5,
		// Explicit, not the "-2 margin" this package's other tests use: with
		// postSamples this small, that formula could hit 0, which
		// correlation.Config.defaults() would silently reinterpret as "use
		// the default of 10" — defeating the small sample count chosen above
		// for planHistorySize reasons.
		MinExecutions:          2,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, col, logger)

	deployEvent := v1alpha1.DeployEvent{
		ID:        "e2e-plandiff-deploy-1",
		Source:    "e2e-test",
		App:       "probe-app",
		Cluster:   "e2e-plandiff-test",
		Namespace: "default",
		Revision:  "drop-index-rev",
		Timestamp: deployAt,
	}

	results := engine.Analyse(deployEvent)

	var match *v1alpha1.PerformanceRegression
	for i := range results {
		// CapturePlans's own EXPLAIN (FORMAT JSON, GENERIC_PLAN) <text> calls
		// (see internal/planner.CapturePlan) are themselves executed as a
		// distinct top-level SQL statement, so pg_stat_statements tracks
		// them under their OWN separate queryid — and since that statement's
		// text is literally "EXPLAIN (...) " followed by the original probe
		// query verbatim, it ALSO contains this test's marker comment.
		// Verified directly against this exact scenario: without this
		// exclusion, the loop below sometimes matches that EXPLAIN-wrapped
		// noise entry instead of the real probe query — its own call
		// count/mean reflects GENERIC_PLAN's (fast, largely plan-shape-
		// independent) planning time, not the real query's execution time,
		// which intermittently produced a spurious small change factor or
		// mismatched sample counts unrelated to the actual index-drop
		// regression this test exists to detect.
		if strings.Contains(results[i].QueryText, marker) && !strings.HasPrefix(results[i].QueryText, "EXPLAIN") {
			match = &results[i]
			break
		}
	}
	if match == nil {
		t.Fatal("correlation engine produced no result for the probe query")
	}
	if match.Status != v1alpha1.StatusDetected {
		t.Fatalf("expected the index-drop-induced slowdown to be Detected, got status=%s change_factor=%.2f", match.Status, match.LatencyChangeFactor)
	}
	t.Logf("detected regression: change_factor=%.2fx mean_before=%.4fms mean_after=%.4fms", match.LatencyChangeFactor, match.MeanLatencyBefore, match.MeanLatencyAfter)

	// --- This is the actual feature under test: attach a plan-diff summary
	// exactly as internal/cli.RunOperator's poll loop does when
	// --capture-plans is set (col.PlansAround(queryID, DetectedChangeAt),
	// then planner.Diff), and confirm the resulting
	// PerformanceRegression.PlanDiffSummary is both non-empty AND actually
	// reflects a real change, not the "nothing changed" branch. ---
	before, after := col.PlansAround(match.QueryID, match.DetectedChangeAt)
	if before == nil || after == nil {
		t.Fatalf("expected both a before and after plan snapshot from CapturePlans, got before=%+v after=%+v", before, after)
	}
	t.Logf("captured plans: before=%s (cost=%.2f) after=%s (cost=%.2f)", before.RootNodeType, before.TotalCost, after.RootNodeType, after.TotalCost)

	match.PlanDiffSummary = planner.Diff(before, after)
	if match.PlanDiffSummary == "" {
		t.Fatal("expected a non-empty PlanDiffSummary on the detected PerformanceRegression")
	}
	if match.PlanDiffSummary == "plan shape unchanged; cost roughly stable" {
		t.Fatalf("expected PlanDiffSummary to reflect the real index-drop-induced plan change, got the no-op summary %q (before=%s/%.2f after=%s/%.2f)",
			match.PlanDiffSummary, before.RootNodeType, before.TotalCost, after.RootNodeType, after.TotalCost)
	}
	t.Logf("PlanDiffSummary: %s", match.PlanDiffSummary)
}

// waitForSeqScanPlan polls EXPLAIN (FORMAT TEXT) for query until its plan no
// longer mentions an Index Scan (i.e. the preceding DROP INDEX has actually
// taken effect for a fresh backend), or fails the test after a short bound.
// See its call site's comment for why this exists.
func waitForSeqScanPlan(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	const attempts = 10
	var lastPlan string
	for i := 0; i < attempts; i++ {
		rows, err := db.QueryContext(ctx, `EXPLAIN (FORMAT TEXT) `+query)
		if err != nil {
			t.Fatalf("EXPLAIN probe query: %v", err)
		}
		var b strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				_ = rows.Close()
				t.Fatalf("scan EXPLAIN line: %v", err)
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		_ = rows.Close()
		lastPlan = b.String()
		if !strings.Contains(lastPlan, "Index Scan") && !strings.Contains(lastPlan, "Index Only Scan") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("query still plans as an Index Scan %d attempts after dropping its index (catalog invalidation not observed in time); last plan:\n%s", attempts, lastPlan)
}
