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

// Package e2e exercises the full, real detection pipeline end to end:
// Collector scraping a live PostgreSQL's real pg_stat_statements,
// correlation.Engine analysing the real samples it collected, and
// alerting.WebhookNotifier firing a real HTTP POST for a detected
// regression — with no mocks, no fakes, and no synthetic in-memory sample
// data anywhere in the chain.
//
// This exists because none of this project's other tests prove the pipeline
// actually catches a regression:
//   - internal/correlation's tests feed the engine hand-built QuerySample
//     slices, so they validate the statistics but never touch a real
//     Collector or a real Postgres.
//   - internal/collector's integration test proves scrape() reads
//     pg_stat_statements correctly, but stops there — it never feeds those
//     samples into correlation.Engine.
//   - internal/controller's tests (fake client and envtest) deliberately use
//     an unreachable DSN (see samplePostgresWatch in
//     postgreswatch_controller_test.go) so no scrape ever fires; they prove
//     the Kubernetes reconciliation state machine, not detection.
//
// Run it explicitly (skips automatically if PGRR_TEST_DSN is unset):
//
//	docker run --rm -d --name pgrr-e2e-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/e2e/...
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// runProbe executes n copies of queryTemplate (a fmt template with a single
// %f verb for sleepSeconds, e.g. "SELECT pg_sleep(%f) /* marker */") against
// db, scraping the real Collector once after each execution so each scrape
// captures pg_stat_statements' running mean shortly after a single new data
// point was added. It sleeps briefly between iterations so RecordedAt
// timestamps are distinct and the loop doesn't hammer the database in a
// tight spin.
//
// queryTemplate — not just a marker string interpolated into a single shared
// template — is the whole point: see regressedQueryTemplate and
// controlQueryTemplate below for why the two probes need genuinely different
// query *shapes*, not just different SQL comments.
//
// pg_stat_statements.mean_exec_time is a lifetime running average since the
// last reset, not a per-scrape-interval average — so this resets the
// extension's stats immediately before each phase (see the two call sites
// below) to get a clean, un-blended mean for that phase's sleepSeconds
// instead of a mean dragged toward whatever the *other* phase's calls did.
func runProbe(t *testing.T, ctx context.Context, db *sql.DB, c *collector.Collector, queryTemplate string, sleepSeconds float64, n int) {
	t.Helper()
	query := fmt.Sprintf(queryTemplate, sleepSeconds)
	for i := 0; i < n; i++ {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("probe query #%d (query=%s): %v", i, query, err)
		}
		if err := c.Scrape(ctx); err != nil {
			t.Fatalf("scrape after probe #%d (query=%s): %v", i, query, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// findSample returns the first QuerySample across all tracked queries whose
// text contains marker within [from, to], or nil.
func findSample(c *collector.Collector, marker string, from, to time.Time) *collector.QuerySample {
	for _, qid := range c.AllQueryIDs() {
		for _, s := range c.SamplesInRange(qid, from, to) {
			if strings.Contains(s.QueryText, marker) {
				s := s
				return &s
			}
		}
	}
	return nil
}

func TestIntegration_FullPipeline_DetectsRealRegression(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping end-to-end pipeline integration test")
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
		regressedMarker = "pgrr_e2e_regressed_query"
		controlMarker   = "pgrr_e2e_control_query"
		samplesPerPhase = 8

		// regressedQueryTemplate and controlQueryTemplate MUST differ
		// structurally, not just by SQL comment. pg_stat_statements computes
		// queryid by jumbling the query's PARSE TREE: SQL comments are
		// discarded by the lexer before parsing (so they never influence
		// queryid at all), and literal constant VALUES are deliberately
		// normalized away too (that's the whole point of the feature — group
		// queries that only differ in the arguments passed). So
		// "SELECT pg_sleep($1) /* pgrr_e2e_regressed_query */" and
		// "SELECT pg_sleep($1) /* pgrr_e2e_control_query */" jumble to the
		// EXACT SAME queryid and silently collapse into a single
		// pg_stat_statements entry — verified directly against a real
		// PostgreSQL 16 instance. That entry keeps whichever comment was
		// captured on its first execution (here, always the regressed
		// probe's, since it always runs first below), so the control probe's
		// samples become permanently unfindable and
		// TestIntegration_FullPipeline_DetectsRealRegression fails
		// deterministically with "correlation engine produced no result for
		// the control probe query" — not a timing flake.
		//
		// The "+ 0.0" below is a structural no-op (adds a real addition node
		// to the parse tree) that's enough to force a distinct queryid while
		// leaving the actual sleep duration — and therefore this test's
		// latency measurements — unchanged.
		regressedQueryTemplate = `SELECT pg_sleep(%f) /* ` + regressedMarker + ` */`
		controlQueryTemplate   = `SELECT pg_sleep(%f + 0.0) /* ` + controlMarker + ` */`
	)

	// --- Pre-deploy phase: both queries are equally fast. ---
	runProbe(t, ctx, setup, col, regressedQueryTemplate, 0.01, samplesPerPhase)
	runProbe(t, ctx, setup, col, controlQueryTemplate, 0.01, samplesPerPhase)

	deployAt := time.Now().UTC()
	time.Sleep(50 * time.Millisecond) // keep pre/post timestamps unambiguous

	// --- Post-deploy phase: only the "regressed" query gets slower. The
	// control query keeps running at the same latency as before, so a
	// correct pipeline must flag one and clear the other — not just flag
	// everything indiscriminately after any deploy. ---
	if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		t.Fatalf("pg_stat_statements_reset before post-deploy phase: %v", err)
	}
	runProbe(t, ctx, setup, col, regressedQueryTemplate, 0.08, samplesPerPhase)
	runProbe(t, ctx, setup, col, controlQueryTemplate, 0.01, samplesPerPhase)

	windowStart := deployAt.Add(-time.Minute)
	windowEnd := time.Now().UTC().Add(time.Minute)
	regressedBefore := findSample(col, regressedMarker, windowStart, deployAt)
	regressedAfter := findSample(col, regressedMarker, deployAt, windowEnd)
	if regressedBefore == nil || regressedAfter == nil {
		t.Fatal("sanity check failed: regressed-query samples missing on one side of the deploy timestamp")
	}
	t.Logf("regressed query: mean %.1fms before -> %.1fms after (raw scrape samples, pre-correlation)",
		regressedBefore.MeanExecTimeMs, regressedAfter.MeanExecTimeMs)

	// --- Real correlation.Engine, fed by the real Collector (satisfies
	// correlation.SampleSource directly — no adapter, no mock). ---
	engine := correlation.New(correlation.Config{
		WindowMinutes:          5,
		MinExecutions:          int64(samplesPerPhase) - 2, // small margin below what runProbe actually produced
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, col, logger)

	deployEvent := v1alpha1.DeployEvent{
		ID:        "e2e-deploy-1",
		Source:    "e2e-test",
		App:       "probe-app",
		Cluster:   "e2e-test",
		Namespace: "default",
		Revision:  "abc123",
		Timestamp: deployAt,
	}

	results := engine.Analyse(deployEvent)

	var regressedResult, controlResult *v1alpha1.PerformanceRegression
	for i := range results {
		switch {
		case strings.Contains(results[i].QueryText, regressedMarker):
			regressedResult = &results[i]
		case strings.Contains(results[i].QueryText, controlMarker):
			controlResult = &results[i]
		}
	}
	if regressedResult == nil {
		t.Fatal("correlation engine produced no result for the regressed probe query")
	}
	if controlResult == nil {
		t.Fatal("correlation engine produced no result for the control probe query")
	}

	if regressedResult.Status != v1alpha1.StatusDetected {
		t.Errorf("regressed query: expected Status=Detected, got %s (change_factor=%.2f, confidence=%.2f)",
			regressedResult.Status, regressedResult.LatencyChangeFactor, regressedResult.ConfidenceScore)
	}
	if regressedResult.LatencyChangeFactor < 2.0 {
		t.Errorf("regressed query: expected a large latency change factor (~8x expected from 10ms->80ms), got %.2fx", regressedResult.LatencyChangeFactor)
	}
	if regressedResult.ConfidenceScore <= 0 {
		t.Errorf("regressed query: expected a positive confidence score, got %.4f", regressedResult.ConfidenceScore)
	}

	// The control query must NOT be flagged. This is the check that keeps
	// this test honest: a pipeline that flags every query after every
	// deploy would pass the assertions above for the wrong reason.
	if controlResult.Status == v1alpha1.StatusDetected {
		t.Errorf("control query (latency unchanged) was flagged as Detected — pipeline is not discriminating regressions from noise (change_factor=%.2f)",
			controlResult.LatencyChangeFactor)
	}

	// --- Real alerting: an actual HTTP POST to an actual server, not just
	// an in-memory struct claiming Status == Detected. ---
	var (
		mu          sync.Mutex
		receivedRaw []byte
	)
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedRaw = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	notifier := alerting.NewWebhookNotifier(alerting.WebhookConfig{URL: webhookServer.URL, ClusterName: "e2e-test", Registerer: prometheus.NewRegistry()}, logger)
	if err := notifier.Notify(ctx, *regressedResult); err != nil {
		t.Fatalf("notifier.Notify: %v", err)
	}

	mu.Lock()
	body := receivedRaw
	mu.Unlock()
	if len(body) == 0 {
		t.Fatal("webhook server received no request body — alert did not actually fire over HTTP")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("webhook payload is not valid JSON: %v (body=%s)", err, body)
	}
	if !strings.Contains(string(body), regressedResult.DeployEventID) {
		t.Errorf("webhook payload does not reference the deploy event id %q; body=%s", regressedResult.DeployEventID, body)
	}
}
