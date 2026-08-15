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
