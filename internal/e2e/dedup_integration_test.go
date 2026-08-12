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
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// TestIntegration_FullPipeline_DedupsQueryIDRotationAcrossDeploy proves
// fix/correlation-queryid-dedup (correlation.Engine.Analyse's fingerprint-
// keyed `seen` map, see engine.go) end to end against a real, mid-window
// pg_stat_statements queryid rotation — not a synthetic one.
//
// How a real rotation is forced: PostgreSQL's pg_stat_statements queryid is
// computed from the post-parse-analysis query tree (see
// internal/collector/fingerprint.go's doc comment for the full citation
// trail), which embeds the OIDs of every referenced catalog object. The
// pg_stat_statements documentation is explicit that only some kinds of
// referenced objects are reliable for this: "pg_stat_statements will
// consider two apparently-identical queries to be distinct if they
// reference a function that was dropped and recreated between executions,
// but conversely, if a table is dropped and recreated between executions,
// two apparently-identical queries may be considered the same"
// (https://www.postgresql.org/docs/current/pgstatstatements.html, queryid
// stability section). This test therefore forces rotation via a referenced
// FUNCTION, not a table — an earlier version of this test used a table
// drop+recreate and passed against a sandboxed PostgreSQL 16, but that was
// coincidental, undocumented behaviour: it failed for real in CI against
// PostgreSQL 18, where the table drop+recreate happened not to rotate the
// queryid, invalidating the test's premise. Function drop+recreate has been
// verified directly (empirically, not just by reading the docs) against
// real PostgreSQL 16, 17, and 18 instances before writing this version of
// the test: repeatedly executing the same query text before and after a
// DROP+CREATE FUNCTION of a function it calls — with a byte-identical
// function body, so only the function's OID changes, never its observable
// behaviour — reliably produces two distinct rows in pg_stat_statements for
// identical query text, on all three versions. This test additionally
// asserts on it explicitly (see the "confirmed real queryid rotation" check
// below) so a future PostgreSQL version that stops exhibiting this
// documented behaviour fails loudly here, rather than this test silently
// degrading into "there was only ever one queryid, so of course only one
// PerformanceRegression came out".
//
// Without engine.go's fingerprint dedup, this test's two-queryid rotation
// would make AllQueryIDs() enumerate the query twice and evaluateQuery run
// twice on the same merged sample set (see SamplesInRange's fingerprint
// fallback), emitting two PerformanceRegressions for one real regression —
// exactly the bug fix/correlation-queryid-dedup fixed.
//
// Run it explicitly (skips automatically if PGRR_TEST_DSN is unset), same
// as this package's other integration tests:
//
//	docker run --rm -d --name pgrr-e2e-dedup-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/e2e/...
func TestIntegration_FullPipeline_DedupsQueryIDRotationAcrossDeploy(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping queryid-rotation dedup integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()
	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset: %v", err)
	}

	const fnName = "pgrr_e2e_dedup_probe_fn"
	recreateProbeFn := func() {
		t.Helper()
		for _, stmt := range []string{
			`DROP FUNCTION IF EXISTS ` + fnName + `()`,
			// A trivial, byte-identical-every-time SQL function returning
			// exactly one row: pg_sleep(%f) sits in the SELECT list, so it is
			// only evaluated once per output row, same as the table this
			// replaced. Zero rows would make the probe query return
			// instantly (no sleep ever runs) without actually failing,
			// silently breaking the latency-change assertions further down.
			// IMMUTABLE + a constant body means the function's *observable*
			// behavior never changes across a drop+recreate — only its
			// catalog OID does, which is exactly what's needed to isolate
			// "did the queryid rotate because of the OID change" from "did
			// the queryid rotate because the query started doing something
			// different" (the latter is exercised separately, and
			// deliberately, by the sleepSeconds argument to runProbe below).
			`CREATE FUNCTION ` + fnName + `() RETURNS SETOF int AS $$ SELECT 1 $$ LANGUAGE sql IMMUTABLE`,
		} {
			if _, err := setup.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("probe function setup (%s): %v", stmt, err)
			}
		}
	}
	recreateProbeFn()
	t.Cleanup(func() {
		_, _ = setup.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+fnName+`()`)
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	col, err := collector.New(collector.Config{DSN: dsn, ClusterName: "e2e-dedup-test", Namespace: "default"}, logger, reg)
	if err != nil {
		t.Fatalf("collector.New: %v", err)
	}
	t.Cleanup(func() { _ = col.Close() })

	const (
		marker = "pgrr_e2e_dedup_marker"
		// Deliberately BELOW collector.fingerprintFallbackMinSamples (5, see
		// internal/collector/collector.go): each phase's samples must land
		// entirely on one side of the rotation, under a distinct queryid, so
		// each queryid's own SamplesInRange bucket has too few in-range
		// samples on its own and the fingerprint-merge fallback actually
		// engages for both the old and new queryid — exactly the
		// "queryid rotated partway through the window" case that fallback
		// exists for. Using >=5 per phase (e.g. this package's other
		// pipeline test's samplesPerPhase=8) would make each queryid's
		// direct bucket self-sufficient, the fallback would never trigger,
		// and neither queryid's evaluation would ever see the other side of
		// the deploy — silently returning StatusInsufficientData instead of
		// exercising the dedup path this test targets.
		samplesPerPhase = 4
	)
	// A single shared template: the whole point of this test is that the
	// SAME query text produces a different queryid purely because the
	// function it references was dropped and recreated, not because the
	// text itself changed (contrast with pipeline_integration_test.go's
	// runProbe doc comment, where two DIFFERENT templates are required to
	// avoid pg_stat_statements collapsing them together). The sleepSeconds
	// argument passed to runProbe below is the thing that actually varies
	// between phases (0.01 -> 0.08), completely independently of the
	// function drop+recreate.
	queryTemplate := `SELECT pg_sleep(%f) FROM ` + fnName + `() /* ` + marker + ` */ LIMIT 1`

	// --- Pre-deploy phase: fast baseline, under the pre-rotation queryid. ---
	runProbe(t, ctx, setup, col, queryTemplate, 0.01, samplesPerPhase)

	var oldQueryID int64
	if err := setup.QueryRowContext(ctx,
		`SELECT queryid FROM pg_stat_statements WHERE query LIKE '%'||$1||'%'`, marker,
	).Scan(&oldQueryID); err != nil {
		t.Fatalf("look up pre-deploy queryid: %v", err)
	}

	deployAt := time.Now().UTC()
	time.Sleep(50 * time.Millisecond)

	// --- The "deploy": a schema migration that drops and recreates the
	// referenced function (byte-identical body — see recreateProbeFn above),
	// forcing a real queryid rotation for byte-identical query text via the
	// documented, reliable mechanism (function OID change), not the
	// undocumented, unreliable one (table OID change) this test used to
	// rely on. ---
	recreateProbeFn()

	// pg_stat_statements does NOT drop a queryid's row just because the
	// function it referenced is gone: that row is otherwise immortal (barring
	// LRU eviction under pg_stat_statements.max) and keeps reporting its
	// last known (now frozen) mean/calls forever. Collector.scrape()
	// unconditionally re-ingests every row it reads on every cycle — not
	// just rows whose counters changed — so without this targeted reset the
	// post-deploy phase's scrapes would keep appending fresh-looking-but-
	// stale samples under the OLD queryid too (verified empirically: this
	// pushed the old queryid's own in-window sample count back above
	// fingerprintFallbackMinSamples, silently defeating the fallback merge
	// this test exists to exercise, and made "before"/"after" samples report
	// the same queryid despite a real rotation having occurred). Resetting
	// just the old queryid (not a full pg_stat_statements_reset(), which
	// would also erase the NEW queryid's data during the post-deploy phase)
	// retires it the same way it would eventually fall out of
	// pg_stat_statements' top-500 in a real, longer-lived deployment.
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset(0, 0, $1)`, oldQueryID); err != nil {
		t.Fatalf("pg_stat_statements_reset(old queryid=%d): %v", oldQueryID, err)
	}

	// --- Post-deploy phase: slower, under the post-rotation queryid. ---
	runProbe(t, ctx, setup, col, queryTemplate, 0.08, samplesPerPhase)

	var newQueryID int64
	if err := setup.QueryRowContext(ctx,
		`SELECT queryid FROM pg_stat_statements WHERE query LIKE '%'||$1||'%'`, marker,
	).Scan(&newQueryID); err != nil {
		t.Fatalf("look up post-deploy queryid: %v", err)
	}

	// --- Sanity check: the rotation actually happened, both at the
	// pg_stat_statements level and as observed by the Collector. Skipping
	// this and going straight to "exactly one result" would pass just as
	// well if rotation silently failed to occur (e.g. a PostgreSQL version
	// that stopped keying queryid off relation OIDs) — for the wrong
	// reason. ---
	if oldQueryID == newQueryID {
		t.Fatalf("expected the function drop+recreate to produce a different pg_stat_statements queryid for identical query text, got the same queryid=%d both times — rotation did not occur as expected, this test's premise is invalid", oldQueryID)
	}
	t.Logf("confirmed real queryid rotation at the pg_stat_statements level: old=%d new=%d", oldQueryID, newQueryID)

	windowStart := deployAt.Add(-time.Minute)
	windowEnd := time.Now().UTC().Add(time.Minute)
	before := findSample(col, marker, windowStart, deployAt)
	after := findSample(col, marker, deployAt, windowEnd)
	if before == nil || after == nil {
		t.Fatal("sanity check failed: probe samples missing on one side of the deploy timestamp")
	}
	if before.QueryID != oldQueryID {
		t.Errorf("expected the Collector's pre-deploy sample to carry the old queryid %d, got %d", oldQueryID, before.QueryID)
	}
	if after.QueryID != newQueryID {
		t.Errorf("expected the Collector's post-deploy sample to carry the new queryid %d, got %d", newQueryID, after.QueryID)
	}
	if before.QueryID == after.QueryID {
		t.Fatalf("expected the probe query's queryid to differ across the function drop+recreate as observed by the Collector; both sides report queryid=%d — rotation did not occur as expected", before.QueryID)
	}
	t.Logf("confirmed real queryid rotation: pre-deploy queryid=%d (mean %.1fms), post-deploy queryid=%d (mean %.1fms)",
		before.QueryID, before.MeanExecTimeMs, after.QueryID, after.MeanExecTimeMs)

	// --- Real correlation.Engine, fed by the real Collector. ---
	engine := correlation.New(correlation.Config{
		WindowMinutes:          5,
		MinExecutions:          int64(samplesPerPhase) - 2, // small margin below samplesPerPhase, mirrors pipeline_integration_test.go
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, col, logger)

	deployEvent := v1alpha1.DeployEvent{
		ID:        "e2e-dedup-deploy-1",
		Source:    "e2e-test",
		App:       "probe-app",
		Cluster:   "e2e-dedup-test",
		Namespace: "default",
		Revision:  "def456",
		Timestamp: deployAt,
	}

	results := engine.Analyse(deployEvent)

	var matches []v1alpha1.PerformanceRegression
	for i := range results {
		if strings.Contains(results[i].QueryText, marker) {
			matches = append(matches, results[i])
		}
	}

	if len(matches) != 1 {
		t.Fatalf("expected exactly ONE PerformanceRegression for the probe query despite its queryid rotating mid-window (fingerprint dedup), got %d: %+v", len(matches), matches)
	}

	result := matches[0]
	t.Logf("deduped result: reported queryid=%d status=%s change_factor=%.2f confidence=%.4f",
		result.QueryID, result.Status, result.LatencyChangeFactor, result.ConfidenceScore)

	if result.Status != v1alpha1.StatusDetected {
		t.Errorf("expected the deduped result to be Detected (real ~8x latency increase across the rotation), got status=%s change_factor=%.2f", result.Status, result.LatencyChangeFactor)
	}
	if result.LatencyChangeFactor < 2.0 {
		t.Errorf("expected a large latency change factor (~8x expected from 10ms->80ms), got %.2fx", result.LatencyChangeFactor)
	}
	if result.ConfidenceScore <= 0 {
		t.Errorf("expected a positive confidence score, got %.4f", result.ConfidenceScore)
	}
}
