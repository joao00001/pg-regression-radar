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
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// TestIntegration_PendingSet_RetriesUntilRealDataArrives closes a real
// coverage gap TestIntegration_FullPipeline_DetectsRealRegression (in this
// same package) does not: that test generates every post-deploy sample
// *before* ever calling Engine.Analyse, so it only ever proves the
// statistics are correct against data that already fully exists — it never
// exercises what happens when Analyse is asked about a deploy event before
// enough post-deploy data has actually arrived, which is exactly the
// real-world timing a live deploy webhook has (see
// internal/correlation.PendingSet's own doc comment for how a production
// bug hiding in exactly that gap was found: running the operator binary for
// real against a real Postgres and a real webhook, not via this test
// suite).
//
// This test drives correlation.PendingSet itself — the actual retry
// primitive internal/cli.RunOperator's and
// internal/controller.PostgresWatchReconciler's poll loops are built on —
// against a real Collector reading a real PostgreSQL's real
// pg_stat_statements, with post-deploy samples genuinely arriving after the
// first Tick, and a real HTTP server standing in for Slack. If this ever
// regresses back to "analyse once, immediately, and give up", this test
// fails on its very first assertion.
func TestIntegration_PendingSet_RetriesUntilRealDataArrives(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping end-to-end pending-retry integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()
	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements (is shared_preload_libraries set?): %v", err)
	}
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg := prometheus.NewRegistry()
	col, err := collector.New(collector.Config{DSN: dsn, ClusterName: "e2e-test", Namespace: "default"}, logger, reg)
	if err != nil {
		t.Fatalf("collector.New: %v", err)
	}
	t.Cleanup(func() { _ = col.Close() })

	const (
		marker         = "pgrr_e2e_pending_retry_query"
		queryTemplate  = "SELECT pg_sleep(%f) /* " + marker + " */"
		samplesPerRun  = 8
		baselineLatSec = 0.01
		regressedLat   = 0.08
	)

	// --- Pre-deploy phase: fast baseline, exactly like
	// TestIntegration_FullPipeline_DetectsRealRegression's regressed probe. ---
	runProbe(t, ctx, setup, col, queryTemplate, baselineLatSec, samplesPerRun)

	deployAt := time.Now().UTC()
	time.Sleep(50 * time.Millisecond) // keep pre/post timestamps unambiguous

	engine := correlation.New(correlation.Config{
		WindowMinutes:          5,
		MinExecutions:          int64(samplesPerRun) - 2, // small margin below what runProbe actually produces
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, col, logger)

	pending := correlation.NewPendingSet(engine, logger)
	deployEvent := v1alpha1.DeployEvent{
		ID:        "e2e-pending-retry-1",
		Source:    "e2e-test",
		App:       "probe-app",
		Cluster:   "e2e-test",
		Namespace: "default",
		Revision:  "abc123",
		Timestamp: deployAt,
	}
	pending.Add(deployEvent)

	// --- Tick #1: registered the instant the "webhook" arrived, with zero
	// post-deploy samples collected yet — this is the exact timing a real
	// ArgoCD/Flux/Rollouts webhook has, and the exact case the bug this test
	// guards against got wrong. Must find nothing, and must NOT retire the
	// event: InsufficientData is not final, it just means "too early". ---
	if results := pending.Tick(); len(results) != 0 {
		t.Fatalf("tick #1 (no post-deploy data has arrived yet): expected 0 results, got %d: %+v", len(results), results)
	}
	if pending.Len() != 1 {
		t.Fatalf("tick #1: event must remain pending (insufficient data isn't a final answer), got Len()=%d", pending.Len())
	}

	// --- Now real post-deploy data actually arrives, exactly like a real
	// collector's background scrape loop would produce it over real elapsed
	// time — this is the part TestIntegration_FullPipeline_DetectsRealRegression
	// does *before* ever calling Analyse; this test deliberately does it
	// *after* Tick #1 instead. ---
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset before post-deploy phase: %v", err)
	}
	runProbe(t, ctx, setup, col, queryTemplate, regressedLat, samplesPerRun)

	// --- Real alerting: PendingSet.Tick()'s output wired into a real HTTP
	// POST, exactly like internal/cli.RunOperator's poll loop does. ---
	var (
		mu          sync.Mutex
		receivedRaw []byte
	)
	slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedRaw = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer slackServer.Close()
	notifier := alerting.NewWebhookNotifier(alerting.WebhookConfig{URL: slackServer.URL, ClusterName: "e2e-test", Registerer: prometheus.NewRegistry()}, logger)

	// --- Tick #2: enough real post-deploy data now exists. ---
	results := pending.Tick()
	if len(results) != 1 {
		t.Fatalf("tick #2 (post-deploy data has now arrived): expected exactly 1 newly-detected regression, got %d: %+v", len(results), results)
	}
	if results[0].Regression.Status != v1alpha1.StatusDetected {
		t.Errorf("tick #2: expected Status=Detected, got %s", results[0].Regression.Status)
	}
	if err := notifier.Notify(ctx, results[0].Regression); err != nil {
		t.Fatalf("notifier.Notify: %v", err)
	}
	mu.Lock()
	body := receivedRaw
	mu.Unlock()
	if len(body) == 0 {
		t.Fatal("Slack server received no request body — the alert PendingSet surfaced did not actually fire over HTTP")
	}

	// --- Tick #3: identical data, no new samples — must not re-report the
	// same query a second time (that would mean every subsequent poll tick
	// for the rest of the analysis window re-fires the same Slack alert). ---
	if results := pending.Tick(); len(results) != 0 {
		t.Fatalf("tick #3 (no new data since tick #2): expected 0 results (already-notified query must not repeat), got %d: %+v", len(results), results)
	}

	// --- The event's analysis window (5 minutes) hasn't elapsed yet, so it
	// must still be tracked — PendingSet must not retire an event just
	// because it already found a regression; other queries under the same
	// deploy could still resolve later. ---
	if pending.Len() != 1 {
		t.Fatalf("tick #3: event's analysis window hasn't elapsed, expected it to remain pending, got Len()=%d", pending.Len())
	}
}
