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

package dashboardapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/storage/memory"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	s := runtime.NewScheme()
	if err := radarv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestHandleRegressions_NoClientConfigured(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleRegressions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/regressions", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestHandleRegressions_ListsAndFilters(t *testing.T) {
	detectedAt := metav1.NewTime(time.Date(2026, 8, 11, 12, 35, 0, 0, time.UTC))
	detected := &radarv1alpha1.PerformanceRegression{
		ObjectMeta: metav1.ObjectMeta{Name: "reg-1", Namespace: "prod"},
		Spec: radarv1alpha1.PerformanceRegressionSpec{
			ClusterName: "prod-cluster",
			QueryID:     8675309,
			QueryText:   "SELECT * FROM orders WHERE user_id = $1",
			TriggerType: radarv1alpha1.RegressionTriggerTypeDeploy,
		},
		Status: radarv1alpha1.PerformanceRegressionStatus{
			Status:                 radarv1alpha1.RegressionStatusDetected,
			ConfidenceScore:        "0.98",
			MeanLatencyBeforeMs:    "4.2",
			MeanLatencyAfterMs:     "13.7",
			LatencyChangeFactor:    "3.26",
			ExternalCauseSuspected: false,
			PlanDiffSummary:        "root node changed from Index Scan to Seq Scan",
			DetectedAt:             &detectedAt,
		},
	}
	noRegression := &radarv1alpha1.PerformanceRegression{
		ObjectMeta: metav1.ObjectMeta{Name: "reg-2", Namespace: "staging"},
		Status: radarv1alpha1.PerformanceRegressionStatus{
			Status: radarv1alpha1.RegressionStatusNoRegression,
		},
	}

	h := &Handler{Client: newFakeClient(t, detected, noRegression)}

	// No filters: both come back.
	rec := httptest.NewRecorder()
	h.handleRegressions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/regressions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	all := decodeJSON[[]regressionDTO](t, rec)
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}

	// Filter by namespace + status: only reg-1 matches.
	rec = httptest.NewRecorder()
	h.handleRegressions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/regressions?namespace=prod&status=Detected", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	filtered := decodeJSON[[]regressionDTO](t, rec)
	if len(filtered) != 1 {
		t.Fatalf("len(filtered) = %d, want 1", len(filtered))
	}
	got := filtered[0]
	want := regressionDTO{
		Name:                "reg-1",
		Namespace:           "prod",
		ClusterName:         "prod-cluster",
		QueryID:             "8675309",
		QueryText:           "SELECT * FROM orders WHERE user_id = $1",
		Status:              "Detected",
		TriggerType:         "deploy",
		ConfidenceScore:     "0.98",
		MeanLatencyBeforeMs: "4.2",
		MeanLatencyAfterMs:  "13.7",
		LatencyChangeFactor: "3.26",
		PlanDiffSummary:     "root node changed from Index Scan to Seq Scan",
		DetectedAt:          "2026-08-11T12:35:00Z",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// Filter by namespace that matches nothing.
	rec = httptest.NewRecorder()
	h.handleRegressions(rec, httptest.NewRequest(http.MethodGet, "/api/v1/regressions?namespace=other", nil))
	empty := decodeJSON[[]regressionDTO](t, rec)
	if len(empty) != 0 {
		t.Fatalf("len(empty) = %d, want 0", len(empty))
	}
}

func TestHandleDeploys_NoStoreConfigured(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleDeploys(rec, httptest.NewRequest(http.MethodGet, "/api/v1/deploys", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestHandleDeploys_DefaultsAndRange(t *testing.T) {
	store := memory.NewEventStore()
	now := time.Now().UTC()
	recent := v1alpha1.DeployEvent{ID: "recent", Timestamp: now.Add(-time.Hour)}
	old := v1alpha1.DeployEvent{ID: "old", Timestamp: now.Add(-30 * 24 * time.Hour)}
	if err := store.Add(context.Background(), recent); err != nil {
		t.Fatalf("add recent: %v", err)
	}
	if err := store.Add(context.Background(), old); err != nil {
		t.Fatalf("add old: %v", err)
	}

	h := &Handler{EventStore: store}

	rec := httptest.NewRecorder()
	h.handleDeploys(rec, httptest.NewRequest(http.MethodGet, "/api/v1/deploys", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[[]v1alpha1.DeployEvent](t, rec)
	if len(got) != 1 || got[0].ID != "recent" {
		t.Fatalf("got %+v, want only 'recent' (default 7d lookback should exclude 'old')", got)
	}

	// Explicit range wide enough to include both.
	since := now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	until := now.Add(time.Hour).Format(time.RFC3339)
	rec = httptest.NewRecorder()
	h.handleDeploys(rec, httptest.NewRequest(http.MethodGet, "/api/v1/deploys?since="+since+"&until="+until, nil))
	got = decodeJSON[[]v1alpha1.DeployEvent](t, rec)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
}

func TestHandleDeploys_InvalidRFC3339(t *testing.T) {
	h := &Handler{EventStore: memory.NewEventStore()}
	rec := httptest.NewRecorder()
	h.handleDeploys(rec, httptest.NewRequest(http.MethodGet, "/api/v1/deploys?since=not-a-date", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleQueries_NoStoreConfigured(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleQueries(rec, httptest.NewRequest(http.MethodGet, "/api/v1/queries", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestHandleQueries_AggregatesSamples(t *testing.T) {
	store := memory.NewSampleStore()
	now := time.Now().UTC()
	ctx := context.Background()
	samples := []collector.QuerySample{
		{QueryID: 42, QueryText: "SELECT 1", Calls: 10, TotalExecTimeMs: 100, MeanExecTimeMs: 10, RecordedAt: now.Add(-30 * time.Minute)},
		{QueryID: 42, QueryText: "SELECT 1", Calls: 20, TotalExecTimeMs: 300, MeanExecTimeMs: 15, RecordedAt: now.Add(-10 * time.Minute)},
		// Outside the default 60-minute window.
		{QueryID: 42, QueryText: "SELECT 1", Calls: 999, TotalExecTimeMs: 999, MeanExecTimeMs: 999, RecordedAt: now.Add(-2 * time.Hour)},
	}
	for _, s := range samples {
		if err := store.Append(ctx, s); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	h := &Handler{SampleStore: store}
	rec := httptest.NewRecorder()
	h.handleQueries(rec, httptest.NewRequest(http.MethodGet, "/api/v1/queries", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[[]queryDTO](t, rec)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	want := queryDTO{QueryID: "42", QueryText: "SELECT 1", Calls: 20, MeanExecMs: 12.5, SampleCount: 2}
	if got[0] != want {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
}

func TestHandleQueries_InvalidWindowMinutes(t *testing.T) {
	h := &Handler{SampleStore: memory.NewSampleStore()}
	rec := httptest.NewRecorder()
	h.handleQueries(rec, httptest.NewRequest(http.MethodGet, "/api/v1/queries?windowMinutes=-5", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleWatches_NoClientConfigured(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.handleWatches(rec, httptest.NewRequest(http.MethodGet, "/api/v1/watches", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestHandleWatches_ListsWithoutTransformation(t *testing.T) {
	watch := &radarv1alpha1.PostgresWatch{
		ObjectMeta: metav1.ObjectMeta{Name: "watch-1", Namespace: "prod"},
		Spec: radarv1alpha1.PostgresWatchSpec{
			ClusterName: "prod-cluster",
		},
	}
	h := &Handler{Client: newFakeClient(t, watch)}
	rec := httptest.NewRecorder()
	h.handleWatches(rec, httptest.NewRequest(http.MethodGet, "/api/v1/watches", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[[]radarv1alpha1.PostgresWatch](t, rec)
	if len(got) != 1 || got[0].Name != "watch-1" || got[0].Spec.ClusterName != "prod-cluster" {
		t.Fatalf("got %+v", got)
	}
}

func TestHandleQuerySamples_NoStoreConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queries/42/samples", nil)
	req.SetPathValue("queryId", "42")
	rec := httptest.NewRecorder()
	h.handleQuerySamples(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

func TestHandleQuerySamples_InvalidQueryID(t *testing.T) {
	h := &Handler{SampleStore: memory.NewSampleStore()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queries/not-an-int/samples", nil)
	req.SetPathValue("queryId", "not-an-int")
	rec := httptest.NewRecorder()
	h.handleQuerySamples(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleQuerySamples_InvalidRFC3339(t *testing.T) {
	h := &Handler{SampleStore: memory.NewSampleStore()}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queries/42/samples?since=not-a-date", nil)
	req.SetPathValue("queryId", "42")
	rec := httptest.NewRecorder()
	h.handleQuerySamples(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleQuerySamples_ReturnsRangeFilteredSamples(t *testing.T) {
	store := memory.NewSampleStore()
	now := time.Now().UTC()
	ctx := context.Background()
	samples := []collector.QuerySample{
		{QueryID: 42, QueryText: "SELECT 1", Calls: 10, TotalExecTimeMs: 100, MeanExecTimeMs: 10, RecordedAt: now.Add(-30 * time.Minute)},
		{QueryID: 42, QueryText: "SELECT 1", Calls: 20, TotalExecTimeMs: 300, MeanExecTimeMs: 15, RecordedAt: now.Add(-10 * time.Minute)},
		// Outside the default 60-minute window, and a different queryid —
		// neither should appear in the response below.
		{QueryID: 42, QueryText: "SELECT 1", Calls: 999, TotalExecTimeMs: 999, MeanExecTimeMs: 999, RecordedAt: now.Add(-2 * time.Hour)},
		{QueryID: 7, QueryText: "SELECT 2", Calls: 1, TotalExecTimeMs: 1, MeanExecTimeMs: 1, RecordedAt: now.Add(-5 * time.Minute)},
	}
	for _, s := range samples {
		if err := store.Append(ctx, s); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	h := &Handler{SampleStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/queries/42/samples", nil)
	req.SetPathValue("queryId", "42")
	rec := httptest.NewRecorder()
	h.handleQuerySamples(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[[]sampleDTO](t, rec)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (one per in-window sample for queryid 42): %+v", len(got), got)
	}
	for _, s := range got {
		if s.Calls != 10 && s.Calls != 20 {
			t.Errorf("unexpected sample in response: %+v (want only the two in-window queryid-42 samples)", s)
		}
	}
}

func TestRoutes_QuerySamplesEndpoint_ResolvesQueryIdFromPath(t *testing.T) {
	store := memory.NewSampleStore()
	now := time.Now().UTC()
	if err := store.Append(context.Background(), collector.QuerySample{
		QueryID: 42, QueryText: "SELECT 1", Calls: 5, TotalExecTimeMs: 50, MeanExecTimeMs: 10, RecordedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	h := &Handler{SampleStore: store}
	mux := http.NewServeMux()
	h.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/queries/42/samples", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	got := decodeJSON[[]sampleDTO](t, rec)
	if len(got) != 1 || got[0].Calls != 5 {
		t.Fatalf("got %+v, want one sample with Calls=5 (routing must resolve {queryId}=42 from the path)", got)
	}

	// The bare "/api/v1/queries" route must still work — i.e. the new
	// {queryId} pattern doesn't shadow or otherwise break it.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/queries", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("aggregate /api/v1/queries status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestWithCORS_SetsHeadersOnSuccessAndError(t *testing.T) {
	h := &Handler{} // no Client configured, so the wrapped handler answers 501
	mux := http.NewServeMux()
	h.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/regressions", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want \"*\" — CORS headers must be set even on an error response", got)
	}
}

func TestWithCORS_PreflightShortCircuits(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/regressions", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (OPTIONS preflight must short-circuit before reaching the wrapped handler)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, want \"GET, OPTIONS\"", got)
	}
}

func TestRoutes_MethodNotAllowed(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/regressions", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
