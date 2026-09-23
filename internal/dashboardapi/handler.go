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

// Package dashboardapi is a read-only HTTP API for the pg-regression-radar
// observability dashboard. It is a pure extension: it does not touch the
// core controllers, CRDs, or statistical detection logic — it only reads
// state that already exists (via a controller-runtime client.Client and the
// internal/storage stores) and reshapes it into JSON.
//
// Every route is served on a best-effort basis: a process that hasn't been
// wired with a given dependency (for example cmd/collector, which has no
// Kubernetes client) responds with 501 Not Implemented for the routes that
// need it, rather than panicking or serving stale/fake data.
//
// This package deliberately does not invent fields that don't exist
// elsewhere in the codebase today (t-statistic, Cohen's d, confidence
// intervals, e-divisive permutation counts, cache-hit ratio, rows-examined,
// lock-wait time, p95/p99 latency). Surfacing those would require extending
// internal/correlation.Engine and the PerformanceRegression CRD first — see
// docs/detection-algorithm.md — which is out of scope here.
package dashboardapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// defaultDeploysLookback is used for GET /api/v1/deploys when neither
// ?since nor ?until is supplied.
const defaultDeploysLookback = 7 * 24 * time.Hour

// defaultQueriesWindowMinutes is used for GET /api/v1/queries when
// ?windowMinutes is not supplied.
const defaultQueriesWindowMinutes = 60

// SampleReader is the read-only slice of storage.SampleStore that GET
// /api/v1/queries needs. storage.SampleStore satisfies this automatically;
// it is declared separately so callers whose query-sample history lives
// somewhere other than a storage.SampleStore (e.g. internal/collector's own
// in-memory map — see NewCollectorSampleReader) can plug in too, without
// having to also implement Append/Prune.
type SampleReader interface {
	// SamplesInRange returns every sample for queryID whose RecordedAt
	// falls within [from, to] (inclusive).
	SamplesInRange(ctx context.Context, queryID int64, from, to time.Time) ([]collector.QuerySample, error)

	// AllQueryIDs returns the distinct set of query IDs known to the store.
	AllQueryIDs(ctx context.Context) ([]int64, error)
}

// EventReader is the read-only slice of storage.EventStore that GET
// /api/v1/deploys needs. storage.EventStore satisfies this automatically;
// it is declared separately so callers whose deploy-event history lives
// somewhere other than a storage.EventStore (e.g. internal/ingester.Store's
// own in-memory slice — see NewIngesterEventReader) can plug in too.
type EventReader interface {
	// EventsInRange returns all events whose Timestamp falls within [from,
	// to] (inclusive).
	EventsInRange(ctx context.Context, from, to time.Time) ([]v1alpha1.DeployEvent, error)
}

// Handler serves the dashboard's read-only HTTP API. Every field is
// optional: a nil dependency means the routes that need it answer 501 Not
// Implemented instead of the data they'd otherwise serve, so a single
// Handler value can be safely wired into any pg-regression-radar process
// regardless of which dependencies that process happens to construct.
type Handler struct {
	// Client lists PerformanceRegression (GET /api/v1/regressions) and
	// PostgresWatch (GET /api/v1/watches) objects. Typically
	// mgr.GetClient() from a controller-runtime Manager (cmd/manager) —
	// cmd/operator has no Kubernetes client, so these two routes always
	// 501 there.
	Client client.Client

	// EventStore backs GET /api/v1/deploys. Both storage.EventStore and
	// NewIngesterEventReader(*ingester.Store) satisfy this.
	EventStore EventReader

	// SampleStore backs GET /api/v1/queries. Both storage.SampleStore and
	// NewCollectorSampleReader(*collector.Collector) satisfy this.
	SampleStore SampleReader

	// Logger receives handler-internal error logs. A nil Logger disables
	// logging (see Handler.logger).
	Logger *slog.Logger
}

// Routes registers every dashboard API route on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/regressions", h.handleRegressions)
	mux.HandleFunc("/api/v1/deploys", h.handleDeploys)
	mux.HandleFunc("/api/v1/queries", h.handleQueries)
	mux.HandleFunc("/api/v1/watches", h.handleWatches)
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// regressionDTO is the JSON shape returned by GET /api/v1/regressions. Every
// field is copied verbatim from PerformanceRegression.Spec/.Status
// (api/v1alpha1/performanceregression_types.go) — nothing here is computed
// or invented.
type regressionDTO struct {
	Name                   string `json:"name"`
	Namespace              string `json:"namespace"`
	ClusterName            string `json:"clusterName"`
	QueryID                int64  `json:"queryId"`
	QueryText              string `json:"queryText"`
	Status                 string `json:"status"`
	TriggerType            string `json:"triggerType"`
	ConfidenceScore        string `json:"confidenceScore"`
	MeanLatencyBeforeMs    string `json:"meanLatencyBeforeMs"`
	MeanLatencyAfterMs     string `json:"meanLatencyAfterMs"`
	LatencyChangeFactor    string `json:"latencyChangeFactor"`
	ExternalCauseSuspected bool   `json:"externalCauseSuspected"`
	PlanDiffSummary        string `json:"planDiffSummary,omitempty"`
	AutoAbortTriggered     bool   `json:"autoAbortTriggered"`
	AutoAbortError         string `json:"autoAbortError,omitempty"`
	DetectedAt             string `json:"detectedAt,omitempty"`
}

func toRegressionDTO(r radarv1alpha1.PerformanceRegression) regressionDTO {
	dto := regressionDTO{
		Name:                   r.Name,
		Namespace:              r.Namespace,
		ClusterName:            r.Spec.ClusterName,
		QueryID:                r.Spec.QueryID,
		QueryText:              r.Spec.QueryText,
		Status:                 string(r.Status.Status),
		TriggerType:            string(r.Spec.TriggerType),
		ConfidenceScore:        r.Status.ConfidenceScore,
		MeanLatencyBeforeMs:    r.Status.MeanLatencyBeforeMs,
		MeanLatencyAfterMs:     r.Status.MeanLatencyAfterMs,
		LatencyChangeFactor:    r.Status.LatencyChangeFactor,
		ExternalCauseSuspected: r.Status.ExternalCauseSuspected,
		PlanDiffSummary:        r.Status.PlanDiffSummary,
		AutoAbortTriggered:     r.Status.AutoAbortTriggered,
		AutoAbortError:         r.Status.AutoAbortError,
	}
	if r.Status.DetectedAt != nil {
		dto.DetectedAt = r.Status.DetectedAt.UTC().Format(time.RFC3339)
	}
	return dto
}

// handleRegressions serves GET /api/v1/regressions?namespace=&status=.
func (h *Handler) handleRegressions(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if h.Client == nil {
		writeError(w, http.StatusNotImplemented, "kubernetes client not configured on this process")
		return
	}

	namespace := r.URL.Query().Get("namespace")
	statusFilter := r.URL.Query().Get("status")

	var list radarv1alpha1.PerformanceRegressionList
	var opts []client.ListOption
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := h.Client.List(r.Context(), &list, opts...); err != nil {
		h.logger().Error("dashboardapi: list regressions failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list regressions")
		return
	}

	out := make([]regressionDTO, 0, len(list.Items))
	for _, item := range list.Items {
		if statusFilter != "" && string(item.Status.Status) != statusFilter {
			continue
		}
		out = append(out, toRegressionDTO(item))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeploys serves GET /api/v1/deploys?since=RFC3339&until=RFC3339.
// Defaults to the last 7 days. Returns v1alpha1.DeployEvent as documented in
// docs/api-reference.md, unmodified.
func (h *Handler) handleDeploys(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if h.EventStore == nil {
		writeError(w, http.StatusNotImplemented, "event store not configured on this process")
		return
	}

	until := time.Now().UTC()
	since := until.Add(-defaultDeploysLookback)

	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since (want RFC3339): "+err.Error())
			return
		}
		since = t
	}
	if raw := r.URL.Query().Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until (want RFC3339): "+err.Error())
			return
		}
		until = t
	}

	events, err := h.EventStore.EventsInRange(r.Context(), since, until)
	if err != nil {
		h.logger().Error("dashboardapi: list deploys failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list deploys")
		return
	}
	if events == nil {
		events = []v1alpha1.DeployEvent{}
	}
	writeJSON(w, http.StatusOK, events)
}

// queryDTO is the JSON shape returned by GET /api/v1/queries. Only fields
// backed by the regression_radar.query_samples table (calls,
// total_exec_time_ms, mean_exec_time_ms) are populated — there is
// deliberately no p95/p99 or cache-hit-ratio field, since that data doesn't
// exist in storage.SampleStore today.
type queryDTO struct {
	QueryID     int64   `json:"queryId"`
	QueryText   string  `json:"queryText"`
	Calls       int64   `json:"calls"`
	MeanExecMs  float64 `json:"meanExecMs"`
	SampleCount int     `json:"sampleCount"`
}

// handleQueries serves GET /api/v1/queries?windowMinutes=60.
func (h *Handler) handleQueries(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if h.SampleStore == nil {
		writeError(w, http.StatusNotImplemented, "sample store not configured on this process")
		return
	}

	windowMinutes := defaultQueriesWindowMinutes
	if raw := r.URL.Query().Get("windowMinutes"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid windowMinutes: must be a positive integer")
			return
		}
		windowMinutes = n
	}

	ctx := r.Context()
	now := time.Now().UTC()
	from := now.Add(-time.Duration(windowMinutes) * time.Minute)

	ids, err := h.SampleStore.AllQueryIDs(ctx)
	if err != nil {
		h.logger().Error("dashboardapi: list query ids failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list queries")
		return
	}

	out := make([]queryDTO, 0, len(ids))
	for _, id := range ids {
		dto, ok, err := aggregateQuerySamples(ctx, h.SampleStore, id, from, now)
		if err != nil {
			h.logger().Error("dashboardapi: load samples failed", "query_id", id, "err", err)
			writeError(w, http.StatusInternalServerError, "failed to load query samples")
			return
		}
		if ok {
			out = append(out, dto)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// aggregateQuerySamples summarises every sample for queryID in [from, to]
// into a single queryDTO. ok is false when there were no samples in range,
// in which case queryID is omitted from the /api/v1/queries response rather
// than reported with zero/misleading values.
func aggregateQuerySamples(ctx context.Context, store SampleReader, queryID int64, from, to time.Time) (queryDTO, bool, error) {
	samples, err := store.SamplesInRange(ctx, queryID, from, to)
	if err != nil {
		return queryDTO{}, false, err
	}
	if len(samples) == 0 {
		return queryDTO{}, false, nil
	}

	dto := queryDTO{QueryID: queryID, SampleCount: len(samples)}
	var meanSum float64
	// Calls (like the rest of pg_stat_statements) is a cumulative counter
	// as of each scrape, not a per-window delta, so the most recently
	// recorded sample's Calls is the representative value for the window
	// rather than a sum across samples (which would double-count).
	var latest time.Time
	for _, s := range samples {
		meanSum += s.MeanExecTimeMs
		if s.RecordedAt.After(latest) || latest.IsZero() {
			latest = s.RecordedAt
			dto.Calls = s.Calls
			dto.QueryText = s.QueryText
		}
	}
	dto.MeanExecMs = meanSum / float64(len(samples))
	return dto, true, nil
}

// handleWatches serves GET /api/v1/watches. Returns PostgresWatchList's
// Items as-is (see api/v1alpha1/postgreswatch_types.go), with no
// transformation.
func (h *Handler) handleWatches(w http.ResponseWriter, r *http.Request) {
	if !allowGET(w, r) {
		return
	}
	if h.Client == nil {
		writeError(w, http.StatusNotImplemented, "kubernetes client not configured on this process")
		return
	}

	var list radarv1alpha1.PostgresWatchList
	if err := h.Client.List(r.Context(), &list); err != nil {
		h.logger().Error("dashboardapi: list watches failed", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list watches")
		return
	}

	items := list.Items
	if items == nil {
		items = []radarv1alpha1.PostgresWatch{}
	}
	writeJSON(w, http.StatusOK, items)
}

func allowGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "only GET is supported")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}
