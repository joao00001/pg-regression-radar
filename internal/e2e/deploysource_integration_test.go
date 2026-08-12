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
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// TestIntegration_FullPipeline_DetectsRegressionFromRealWebhookSources closes
// a coverage gap this repo's task notes flagged explicitly: of the four
// deploy source types internal/ingester.Handler supports (argocd,
// argo-rollouts, flux, generic), only "generic" had ever been driven through
// the REAL detection pipeline (Collector -> correlation.Engine ->
// PerformanceRegression) — see pipeline_integration_test.go, which hand-
// builds a v1alpha1.DeployEvent in Go rather than parsing one from a
// webhook. internal/ingester/ingester_test.go's TestHandler_ArgoCDWebhook /
// TestHandler_FluxWebhook / TestHandler_ArgoRolloutsWebhook_* already POST
// real tool-shaped JSON through a real http.Handler — genuine, valuable
// coverage — but stop at asserting on the resulting ingester.Store contents;
// none of them ever hand that DeployEvent to a real correlation.Engine fed
// by a real Collector, so none of them prove a regression is actually
// detected off the back of an ArgoCD/Argo Rollouts/Flux notification.
//
// This test drives, for each of those three source types, the complete
// chain: a real HTTP POST of that tool's actual webhook payload shape ->
// internal/ingester.Handler parses it into a DeployEvent using that event's
// own Timestamp (not a value this test injects) -> a real Collector scrapes
// real pg_stat_statements samples straddling that timestamp -> a real
// correlation.Engine detects the regression. "generic" already has this
// coverage via pipeline_integration_test.go and is not repeated here.
//
// Run it explicitly (skips automatically if PGRR_TEST_DSN is unset), same
// as this package's other integration tests:
//
//	docker run --rm -d --name pgrr-e2e-sources-test -p 5432:5432 \
//	  -e POSTGRES_PASSWORD=test postgres:16 \
//	  postgres -c shared_preload_libraries=pg_stat_statements
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/e2e/...
func TestIntegration_FullPipeline_DetectsRegressionFromRealWebhookSources(t *testing.T) {
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping deploy-source webhook integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	setup, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open setup connection: %v", err)
	}
	defer setup.Close()
	if _, err := setup.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		t.Fatalf("CREATE EXTENSION pg_stat_statements: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	const samplesPerPhase = 8

	cases := []struct {
		name       string
		sourceType string
		marker     string
		// buildPayload returns the raw webhook body for this tool's real
		// notification shape, matching the fields
		// internal/ingester/ingester.go's parse* functions for this source
		// type actually read (cross-checked against
		// internal/ingester/ingester_test.go's existing payload literals for
		// the same tool).
		buildPayload func(app string) []byte
		wantApp      string
	}{
		{
			name:       "argocd",
			sourceType: "argocd",
			marker:     "pgrr_e2e_argocd_marker",
			wantApp:    "e2e-argocd-app",
			buildPayload: func(app string) []byte {
				body, _ := json.Marshal(map[string]interface{}{
					"app": map[string]interface{}{
						"metadata": map[string]interface{}{
							"name":      app,
							"namespace": "production",
						},
						"spec": map[string]interface{}{
							"destination": map[string]interface{}{
								"name": "e2e-argocd-cluster",
							},
						},
						"status": map[string]interface{}{
							"sync":    map[string]interface{}{"revision": "argocd-rev-1"},
							"summary": map[string]interface{}{"images": []string{app + ":v1"}},
						},
					},
				})
				return body
			},
		},
		{
			name:       "argo-rollouts",
			sourceType: "argo-rollouts",
			marker:     "pgrr_e2e_rollouts_marker",
			wantApp:    "e2e-rollouts-app",
			buildPayload: func(app string) []byte {
				body, _ := json.Marshal(map[string]interface{}{
					"rollout": map[string]interface{}{
						"metadata": map[string]interface{}{
							"name":      app,
							"namespace": "staging",
						},
						"status": map[string]interface{}{"currentPodHash": "rollouts-hash-1"},
					},
					"imageTag": app + ":canary-v1",
					"cluster":  "e2e-rollouts-cluster",
				})
				return body
			},
		},
		{
			name:       "flux",
			sourceType: "flux",
			marker:     "pgrr_e2e_flux_marker",
			wantApp:    "e2e-flux-app",
			buildPayload: func(app string) []byte {
				body, _ := json.Marshal(map[string]interface{}{
					"involvedObject": map[string]interface{}{
						"name":      app,
						"namespace": "flux-system",
						"kind":      "Kustomization",
					},
					"severity":  "info",
					"message":   "Reconciliation finished",
					"metadata":  map[string]string{"revision": "main@sha1:flux-rev-1", "cluster": "e2e-flux-cluster"},
					"timestamp": time.Now().UTC().Format(time.RFC3339),
				})
				return body
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Not t.Parallel(): every sub-test shares the one real Postgres
			// instance's pg_stat_statements, whose state is server-wide (see
			// ci.yml's "-p 1" comment for the same reasoning across packages).
			if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
				t.Fatalf("pg_stat_statements_reset: %v", err)
			}

			reg := prometheus.NewRegistry()
			col, err := collector.New(collector.Config{DSN: dsn, ClusterName: "e2e-sources-test", Namespace: "default"}, logger, reg)
			if err != nil {
				t.Fatalf("collector.New: %v", err)
			}
			t.Cleanup(func() { _ = col.Close() })

			queryTemplate := `SELECT pg_sleep(%f) /* ` + tc.marker + ` */`

			// --- Pre-deploy phase ---
			runProbe(t, ctx, setup, col, queryTemplate, 0.01, samplesPerPhase)

			// Flux's payload carries its own "timestamp" field formatted with
			// time.RFC3339 (whole-second resolution — see parseFluxPayload in
			// internal/ingester/ingester.go), unlike ArgoCD/Argo Rollouts,
			// whose parsers stamp ev.Timestamp with time.Now() (full
			// nanosecond resolution) at parse time. Building that payload
			// immediately after the sub-second-fast pre-deploy phase above
			// risks the truncated-to-the-second deployAt landing IN THE
			// MIDDLE of (or even before) the pre-deploy phase's own
			// nanosecond-precision sample timestamps, corrupting
			// partitionAtDeployTime's pre/post split — verified directly
			// against this exact scenario (it produced StatusInsufficientData
			// because every "pre" sample got classified as "post"). Crossing
			// a full second of real time before building/sending the payload
			// guarantees the floored timestamp still lands safely after every
			// pre-deploy sample, for every source type (a no-op delay for
			// ArgoCD/Argo Rollouts, which don't have this truncation issue).
			time.Sleep(1100 * time.Millisecond)

			// --- The real webhook POST: this is the part that's new
			// coverage. A real net/http/httptest server, backed by the real
			// internal/ingester.Handler configured for this source type,
			// receiving this tool's actual notification payload shape. ---
			store := &ingester.Store{}
			source := v1alpha1.DeploySource{
				Name:        "e2e-" + tc.name + "-source",
				SourceType:  tc.sourceType,
				ClusterName: "e2e-sources-test",
			}
			handler := ingester.NewHandler(store, source, logger)
			webhookServer := httptest.NewServer(handler)
			defer webhookServer.Close()

			body := tc.buildPayload(tc.wantApp)
			resp, err := http.Post(webhookServer.URL, "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("POST webhook: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("expected webhook handler to return 204, got %d", resp.StatusCode)
			}

			events := store.All()
			if len(events) != 1 {
				t.Fatalf("expected exactly 1 DeployEvent ingested from the %s webhook, got %d", tc.sourceType, len(events))
			}
			ev := events[0]
			if ev.App != tc.wantApp {
				t.Errorf("expected DeployEvent.App=%q parsed from the real %s payload, got %q", tc.wantApp, tc.sourceType, ev.App)
			}
			if ev.Source != source.Name {
				t.Errorf("expected DeployEvent.Source=%q, got %q", source.Name, ev.Source)
			}
			if ev.Timestamp.IsZero() {
				t.Fatal("expected a non-zero DeployEvent.Timestamp from the real webhook payload")
			}
			deployAt := ev.Timestamp

			time.Sleep(50 * time.Millisecond)
			if _, err := setup.ExecContext(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
				t.Fatalf("pg_stat_statements_reset before post-deploy phase: %v", err)
			}

			// --- Post-deploy phase ---
			runProbe(t, ctx, setup, col, queryTemplate, 0.08, samplesPerPhase)

			engine := correlation.New(correlation.Config{
				WindowMinutes:          5,
				MinExecutions:          int64(samplesPerPhase) - 2,
				LatencyChangeThreshold: 0.20,
				PValueThreshold:        0.05,
			}, col, logger)

			results := engine.Analyse(ev)

			var match *v1alpha1.PerformanceRegression
			for i := range results {
				if strings.Contains(results[i].QueryText, tc.marker) {
					match = &results[i]
					break
				}
			}
			if match == nil {
				t.Fatalf("correlation engine produced no result for the probe query after a real %s webhook deploy event (deployAt=%s)", tc.sourceType, deployAt)
			}
			if match.Status != v1alpha1.StatusDetected {
				t.Errorf("expected Detected for a real %s-sourced deploy event, got status=%s change_factor=%.2f", tc.sourceType, match.Status, match.LatencyChangeFactor)
			}
			if match.DeployEventID != ev.ID {
				t.Errorf("expected the regression's DeployEventID to be the real ingested event's ID %q, got %q", ev.ID, match.DeployEventID)
			}
			t.Logf("%s: detected regression change_factor=%.2fx confidence=%.4f deploy_event_id=%s", tc.sourceType, match.LatencyChangeFactor, match.ConfidenceScore, match.DeployEventID)
		})
	}
}
