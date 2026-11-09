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

package controller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/internal/testlogger"
)

func newDeploySourceReconciler(t *testing.T, registry *Registry, mux *DynamicMux, objs ...client.Object) *DeploySourceReconciler {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.DeploySource{}).
		WithObjects(objs...).
		Build()

	return &DeploySourceReconciler{
		Client:   c,
		Registry: registry,
		Mux:      mux,
		Logger:   testlogger.New(t),
	}
}

// TestDeploySourceReconcile_PendingWithoutWatch verifies that a DeploySource
// referencing a PostgresWatch that isn't (yet) running stays Pending and
// registers no webhook route.
func TestDeploySourceReconcile_PendingWithoutWatch(t *testing.T) {
	src := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-a", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "missing-watch",
			SourceType:       "generic",
		},
	}
	mux := NewDynamicMux()
	r := newDeploySourceReconciler(t, NewRegistry(), mux, src)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "src-a", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if mux.Len() != 0 {
		t.Fatalf("expected no routes registered, got %d", mux.Len())
	}

	var got radarv1alpha1.DeploySource
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.DeploySourcePhasePending {
		t.Fatalf("expected phase Pending, got %q", got.Status.Phase)
	}
}

// TestDeploySourceReconcile_RegistersAndUnregistersRoute verifies the full
// lifecycle: once the referenced watch is in the Registry, reconciling
// registers a working webhook route that forwards to
// internal/ingester.Handler (unmodified); deleting the DeploySource removes
// the route.
func TestDeploySourceReconcile_RegistersAndUnregistersRoute(t *testing.T) {
	registry := NewRegistry()
	watchKey := types.NamespacedName{Name: "watch-x", Namespace: "default"}
	store := &ingester.Store{}
	registry.Set(watchKey, &WatchRuntime{Store: store})

	src := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-b", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "generic",
		},
	}
	mux := NewDynamicMux()
	r := newDeploySourceReconciler(t, registry, mux, src)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "src-b", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got radarv1alpha1.DeploySource
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.DeploySourcePhaseReady {
		t.Fatalf("expected phase Ready, got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}
	wantPath := "/webhook/default/src-b"
	if got.Status.WebhookPath != wantPath {
		t.Fatalf("expected webhook path %q, got %q", wantPath, got.Status.WebhookPath)
	}

	// Exercise the registered route end-to-end through the real,
	// unmodified ingester.Handler via the generic payload shape.
	body := bytes.NewBufferString(`{"id":"evt-1","app":"checkout","revision":"abc123"}`)
	req2 := httptest.NewRequest(http.MethodPost, wantPath, body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req2)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from ingester handler via mux, got %d: %s", rec.Code, rec.Body.String())
	}
	events := store.All()
	if len(events) != 1 || events[0].ID != "evt-1" {
		t.Fatalf("expected the deploy event to land in the watch's Store, got %+v", events)
	}

	// Deleting the DeploySource must remove the route.
	if err := r.Delete(context.Background(), &got); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile after delete: %v", err)
	}
	if mux.Len() != 0 {
		t.Fatalf("expected route to be unregistered after delete, mux still has %d routes", mux.Len())
	}
}

const otlpDeployTraceJSON = `{"resourceSpans":[{"resource":{"attributes":[` +
	`{"key":"service.name","value":{"stringValue":"checkout"}},` +
	`{"key":"k8s.namespace.name","value":{"stringValue":"shop"}},` +
	`{"key":"vcs.ref.head.revision","value":{"stringValue":"abc123"}}]},` +
	`"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef",` +
	`"name":"deploy","startTimeUnixNano":"1700000000000000000","endTimeUnixNano":"1700000001000000000","status":{"code":1}}]}]}]}`

func postOTLP(mux http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(otlpDeployTraceJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestDeploySourceReconcile_OTLPRegistersBothRoutes verifies the
// OTLP/HTTP exporter's convention (endpoint + "/v1/traces") reaches the
// handler, that spans become deploy events in the watch's Store, and that
// both routes go away together.
func TestDeploySourceReconcile_OTLPRegistersBothRoutes(t *testing.T) {
	registry := NewRegistry()
	watchKey := types.NamespacedName{Name: "watch-x", Namespace: "default"}
	store := &ingester.Store{}
	registry.Set(watchKey, &WatchRuntime{Store: store})

	src := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-otlp", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "otlp",
			OTLP:             &radarv1alpha1.OTLPDeployConfig{SpanNames: []string{"deploy"}},
		},
	}
	mux := NewDynamicMux()
	r := newDeploySourceReconciler(t, registry, mux, src)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "src-otlp", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got radarv1alpha1.DeploySource
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != radarv1alpha1.DeploySourcePhaseReady {
		t.Fatalf("phase = %q (%s)", got.Status.Phase, got.Status.Message)
	}
	if mux.Len() != 2 {
		t.Fatalf("expected 2 routes (base + /v1/traces), got %d", mux.Len())
	}
	for _, p := range []string{"/webhook/default/src-otlp", "/webhook/default/src-otlp/v1/traces"} {
		if rec := postOTLP(mux, p); rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", p, rec.Code, rec.Body.String())
		}
	}
	events := store.All()
	if len(events) != 1 {
		// The same span posted twice has the same deterministic ID.
		t.Fatalf("expected the two identical posts to dedupe to 1 event, got %d: %+v", len(events), events)
	}
	if events[0].App != "checkout" || events[0].Revision != "abc123" || events[0].Namespace != "shop" {
		t.Fatalf("unexpected event: %+v", events[0])
	}

	if err := r.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if mux.Len() != 0 {
		t.Fatalf("both routes must be unregistered, %d left", mux.Len())
	}
}

// TestDeploySourceReconcile_OTLPInvalidMappingFails verifies a mapping that
// would match every span (or nothing) is surfaced as Failed/InvalidSpec
// instead of registering a route that silently drops or floods events.
func TestDeploySourceReconcile_OTLPInvalidMappingFails(t *testing.T) {
	registry := NewRegistry()
	registry.Set(types.NamespacedName{Name: "watch-x", Namespace: "default"}, &WatchRuntime{Store: &ingester.Store{}})
	src := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-bad", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "otlp",
		},
	}
	mux := NewDynamicMux()
	r := newDeploySourceReconciler(t, registry, mux, src)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "src-bad", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile should not requeue-error on a spec problem: %v", err)
	}
	var got radarv1alpha1.DeploySource
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Phase != radarv1alpha1.DeploySourcePhaseFailed {
		t.Fatalf("phase = %q, want Failed", got.Status.Phase)
	}
	if mux.Len() != 0 {
		t.Fatalf("no route may be registered, got %d", mux.Len())
	}
}
