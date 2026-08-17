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
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/internal/storage/postgres"
	"github.com/joao00001/pg-regression-radar/internal/testlogger"
	"github.com/joao00001/pg-regression-radar/internal/testutil"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// TestIntegration_Backfill_RestartRestoresStateAndCursor exercises
// feat/state-backfill end to end against a real Postgres, on both halves of
// the feature that internal/cli.RunOperator wires together (see its
// "Backfill" section): internal/collector.Collector.Backfill for query
// samples, and internal/ingester.Store.Backfill for deploy events — plus the
// cursor-advancement contract that keeps a restart from re-processing (and
// re-alerting on) deploy events a previous process lifetime already
// evaluated.
//
// The scenario: a first "process" (col1/store1 below) scrapes real
// pg_stat_statements samples and ingests a deploy event, mirroring both into
// the Postgres-backed durable stores exactly as internal/cli.RunOperator's
// bridge goroutines do. A second, entirely fresh "process" (col2/store2 —
// new Collector, new ingester.Store, zero in-memory state, standing in for a
// restarted pod) then Backfills from that same durable Postgres and must:
//
//  1. See the same query samples col1 originally scraped (proven by running
//     the real correlation.Engine against col2 and getting the identical
//     Detected regression col1's engine got, not just "some data exists").
//  2. NOT re-drain (and therefore not re-alert on) the deploy event that
//     was already fully processed before the "restart", by advancing
//     Store.DrainSince's cursor past the backfilled events — this is the
//     exact mechanism internal/cli.RunOperator's initialCursor comment
//     warns about getting wrong.
//  3. Still correctly drain a genuinely NEW deploy event that arrives after
//     the restart, proving the cursor fix doesn't just suppress everything.
//
// Run it explicitly (skips automatically if PGRR_TEST_DSN is unset), same
// as this package's other integration tests:
//
//	docker run --rm -d --name pgrr-e2e-backfill-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/e2e/...
func TestIntegration_Backfill_RestartRestoresStateAndCursor(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping backfill/restart integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()
	releasePGStatStatementsLock := testutil.AcquirePGStatStatementsTestLock(t, ctx, setup)
	defer releasePGStatStatementsLock()
	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements: %v", err)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset: %v", err)
	}

	// ---- Durable state backend: the same Postgres, a dedicated schema
	// (see internal/storage/postgres.SchemaName), exactly as --state-dsn=""
	// (reuse the monitored DSN) behaves in cmd/operator. ----
	stateDB, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("postgres.Open (state backend): %v", err)
	}
	defer func() { _ = stateDB.Close() }()
	if _, err := stateDB.ExecContext(ctx, `TRUNCATE `+postgres.SchemaName+`.query_samples, `+postgres.SchemaName+`.deploy_events`); err != nil {
		t.Fatalf("truncate state tables: %v", err)
	}
	t.Cleanup(func() {
		_, _ = stateDB.ExecContext(context.Background(), `TRUNCATE `+postgres.SchemaName+`.query_samples, `+postgres.SchemaName+`.deploy_events`)
	})
	pgSamples := postgres.NewSampleStore(stateDB)
	pgEvents := postgres.NewEventStore(stateDB)

	logger := testlogger.New(t)

	// ==================== "Process 1" (pre-restart) ====================
	reg1 := prometheus.NewRegistry()
	col1, err := collector.New(collector.Config{DSN: dsn, ClusterName: "e2e-backfill-test", Namespace: "default"}, logger, reg1)
	if err != nil {
		t.Fatalf("collector.New (process 1): %v", err)
	}
	t.Cleanup(func() { _ = col1.Close() })
	store1 := &ingester.Store{}

	const (
		marker          = "pgrr_e2e_backfill_marker"
		samplesPerPhase = 8
	)
	queryTemplate := `SELECT pg_sleep(%f) /* ` + marker + ` */`

	runProbe(t, ctx, setup, col1, queryTemplate, 0.01, samplesPerPhase)
	deployAt := time.Now().UTC()
	time.Sleep(50 * time.Millisecond)
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset before post-deploy phase: %v", err)
	}
	runProbe(t, ctx, setup, col1, queryTemplate, 0.08, samplesPerPhase)

	deployEvent1 := v1alpha1.DeployEvent{
		ID:        "e2e-backfill-deploy-1",
		Source:    "e2e-test",
		App:       "probe-app",
		Cluster:   "e2e-backfill-test",
		Namespace: "default",
		Revision:  "pre-restart-rev",
		Timestamp: deployAt,
	}
	// This is what an ingester.Handler would have done on webhook receipt,
	// and what internal/cli.RunOperator's poll loop does immediately after
	// draining it: store it in-process, and mirror it into the durable
	// EventStore.
	store1.Add(deployEvent1)
	if err := pgEvents.Add(ctx, deployEvent1); err != nil {
		t.Fatalf("pgEvents.Add: %v", err)
	}

	// Mirror every sample col1 scraped into the durable SampleStore — the
	// same bridge internal/cli.RunOperator runs on a ticker.
	since := deployAt.Add(-time.Hour)
	until := time.Now().UTC().Add(time.Minute)
	sampleCount := 0
	for _, qid := range col1.AllQueryIDs() {
		for _, s := range col1.SamplesInRange(qid, since, until) {
			if err := pgSamples.Append(ctx, s); err != nil {
				t.Fatalf("pgSamples.Append: %v", err)
			}
			sampleCount++
		}
	}
	if sampleCount == 0 {
		t.Fatal("sanity check failed: no samples were persisted to the durable SampleStore before the simulated restart")
	}

	// --- Sanity check (pre-restart baseline): the live collector really
	// does detect the regression, so a later "detected after restart too"
	// comparison is meaningful. ---
	cfg := correlation.Config{
		WindowMinutes:          5,
		MinExecutions:          int64(samplesPerPhase) - 2,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}
	engine1 := correlation.New(cfg, col1, logger)
	before := findRegressionByMarker(t, engine1.Analyse(deployEvent1), marker)
	if before.Status != v1alpha1.StatusDetected {
		t.Fatalf("pre-restart sanity check failed: expected Detected, got %s", before.Status)
	}

	// ==================== Simulated restart ====================
	// A brand new Collector and a brand new ingester.Store — zero in-memory
	// state, as if this were a freshly started process — standing in for
	// col1/store1 after a pod restart. Nothing from col1/store1 is reused
	// below except the shared durable Postgres state they wrote to above.
	reg2 := prometheus.NewRegistry()
	col2, err := collector.New(collector.Config{DSN: dsn, ClusterName: "e2e-backfill-test", Namespace: "default"}, logger, reg2)
	if err != nil {
		t.Fatalf("collector.New (process 2, post-restart): %v", err)
	}
	t.Cleanup(func() { _ = col2.Close() })
	store2 := &ingester.Store{}

	// ---- Backfill query samples (mirrors internal/cli.RunOperator's
	// Backfill section) ----
	qids, err := pgSamples.AllQueryIDs(ctx)
	if err != nil {
		t.Fatalf("pgSamples.AllQueryIDs: %v", err)
	}
	var backfilledSamples []collector.QuerySample
	for _, qid := range qids {
		s, err := pgSamples.SamplesInRange(ctx, qid, since, until)
		if err != nil {
			t.Fatalf("pgSamples.SamplesInRange(%d): %v", qid, err)
		}
		backfilledSamples = append(backfilledSamples, s...)
	}
	if len(backfilledSamples) != sampleCount {
		t.Fatalf("expected to backfill all %d persisted samples, got %d", sampleCount, len(backfilledSamples))
	}
	col2.Backfill(backfilledSamples)

	// ---- Backfill deploy events, capturing the cursor exactly as
	// internal/cli.RunOperator's initialCursor does ----
	events, err := pgEvents.EventsInRange(ctx, since, until)
	if err != nil {
		t.Fatalf("pgEvents.EventsInRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 backfilled deploy event, got %d", len(events))
	}
	initialCursor := store2.Backfill(events)

	// ==================== Assertion 1: samples came back, and
	// correlation still works correctly against the backfilled data ====================
	engine2 := correlation.New(cfg, col2, logger)
	after := findRegressionByMarker(t, engine2.Analyse(deployEvent1), marker)
	if after.Status != v1alpha1.StatusDetected {
		t.Errorf("expected the backfilled Collector to still detect the regression post-restart, got status=%s", after.Status)
	}
	// Not just "still Detected" by coincidence — the actual measured
	// latencies must match what process 1 saw, proving the real sample
	// values (not just placeholder/zeroed rows) round-tripped through
	// Postgres and back into col2's in-memory series.
	const latencyTolerance = 0.5 // ms; pg_sleep/network jitter margin
	if diff := after.MeanLatencyBefore - before.MeanLatencyBefore; diff > latencyTolerance || diff < -latencyTolerance {
		t.Errorf("post-restart MeanLatencyBefore=%.2fms does not match pre-restart %.2fms (backfilled sample values diverged)", after.MeanLatencyBefore, before.MeanLatencyBefore)
	}
	if diff := after.MeanLatencyAfter - before.MeanLatencyAfter; diff > latencyTolerance || diff < -latencyTolerance {
		t.Errorf("post-restart MeanLatencyAfter=%.2fms does not match pre-restart %.2fms (backfilled sample values diverged)", after.MeanLatencyAfter, before.MeanLatencyAfter)
	}

	// ==================== Assertion 2: the cursor mechanism prevents
	// re-processing (and re-alerting on) the already-handled deploy event
	// ==================== ==================================================
	// This is the crux of the fix: DrainSince(initialCursor) — the correct,
	// post-Backfill starting point internal/cli.RunOperator's poll loop
	// uses — must see nothing new, because deployEvent1 was already fully
	// analysed (and would have already been alerted on) in "process 1"'s
	// lifetime above.
	replayed, cursorAfterNoOp := store2.DrainSince(initialCursor)
	if len(replayed) != 0 {
		t.Fatalf("expected DrainSince(initialCursor) to replay nothing (the backfilled event was already handled pre-restart), got %d event(s): %+v", len(replayed), replayed)
	}
	if cursorAfterNoOp != initialCursor {
		t.Fatalf("expected the cursor to stay at %d when nothing new has arrived, got %d", initialCursor, cursorAfterNoOp)
	}
	// Contrast case, to make this assertion's value concrete rather than
	// vacuous: starting from cursor 0 instead of initialCursor (i.e. what a
	// restart WITHOUT this fix would do) DOES replay the backfilled event —
	// confirming this is a real behavioural difference the fix produces,
	// not a tautology of DrainSince's semantics.
	replayedFromZero, _ := store2.DrainSince(0)
	if len(replayedFromZero) != 1 || replayedFromZero[0].ID != deployEvent1.ID {
		t.Fatalf("expected DrainSince(0) to replay the single backfilled event (demonstrating what the cursor fix prevents), got %+v", replayedFromZero)
	}

	// ==================== Assertion 3: a genuinely NEW post-restart
	// deploy event is still correctly drained ====================
	deployEvent2 := v1alpha1.DeployEvent{
		ID:        "e2e-backfill-deploy-2",
		Source:    "e2e-test",
		App:       "probe-app",
		Cluster:   "e2e-backfill-test",
		Namespace: "default",
		Revision:  "post-restart-rev",
		Timestamp: time.Now().UTC(),
	}
	store2.Add(deployEvent2)
	newEvents, _ := store2.DrainSince(initialCursor)
	if len(newEvents) != 1 || newEvents[0].ID != deployEvent2.ID {
		t.Fatalf("expected DrainSince(initialCursor) to drain exactly the new post-restart event, got %+v", newEvents)
	}
}

// findRegressionByMarker returns the single PerformanceRegression in results
// whose QueryText contains marker, failing the test if there isn't exactly
// one.
func findRegressionByMarker(t *testing.T, results []v1alpha1.PerformanceRegression, marker string) v1alpha1.PerformanceRegression {
	t.Helper()
	var matches []v1alpha1.PerformanceRegression
	for i := range results {
		if strings.Contains(results[i].QueryText, marker) {
			matches = append(matches, results[i])
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one PerformanceRegression matching marker %q, got %d: %+v", marker, len(matches), matches)
	}
	return matches[0]
}
