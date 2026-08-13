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
	"context"
	"io"
	"log/slog"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
)

func newWorkloadWatchReconciler(t *testing.T, registry *Registry, objs ...client.Object) *WorkloadWatchReconciler {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
	return &WorkloadWatchReconciler{
		Client:   c,
		Registry: registry,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func completeDeployment(name, namespace string, revision string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace, Generation: 1,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": revision},
		},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(3))},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           3,
			UpdatedReplicas:    3,
			AvailableReplicas:  3,
		},
	}
}

// TestWorkloadWatchReconcile_EmitsDeployEventOnDeploymentRolloutComplete
// verifies the core native-watch path: a DeploySource with
// sourceType "kubernetes" watching a Deployment by name gets a DeployEvent
// the moment that Deployment's rollout completes, with no webhook and no
// GitOps tool involved at all.
func TestWorkloadWatchReconcile_EmitsDeployEventOnDeploymentRolloutComplete(t *testing.T) {
	registry := NewRegistry()
	watchKey := types.NamespacedName{Name: "watch-x", Namespace: "default"}
	store := &ingester.Store{}
	registry.Set(watchKey, &WatchRuntime{Store: store, ClusterName: "prod"})

	ds := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-native", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "kubernetes",
			WorkloadKind:     "Deployment",
			AppName:          "checkout",
		},
	}
	deploy := completeDeployment("checkout", "default", "5")

	r := newWorkloadWatchReconciler(t, registry, ds, deploy)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "checkout", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	events := store.All()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 deploy event, got %d: %+v", len(events), events)
	}
	ev := events[0]
	if ev.App != "checkout" || ev.Namespace != "default" || ev.Revision != "5" || ev.Source != "src-native" || ev.Cluster != "prod" {
		t.Fatalf("unexpected deploy event: %+v", ev)
	}
}

// TestWorkloadWatchReconcile_NoEventWhileRolloutInProgress verifies that a
// Deployment that hasn't finished rolling out yet (fewer updated replicas
// than desired) does not produce a DeployEvent — reporting a half-finished
// rollout as "deployed" would poison the correlation engine's pre/post
// window with data from two different revisions.
func TestWorkloadWatchReconcile_NoEventWhileRolloutInProgress(t *testing.T) {
	registry := NewRegistry()
	watchKey := types.NamespacedName{Name: "watch-x", Namespace: "default"}
	store := &ingester.Store{}
	registry.Set(watchKey, &WatchRuntime{Store: store})

	ds := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-native", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "kubernetes",
			WorkloadKind:     "Deployment",
			AppName:          "checkout",
		},
	}
	deploy := completeDeployment("checkout", "default", "5")
	deploy.Status.UpdatedReplicas = 2 // still rolling out

	r := newWorkloadWatchReconciler(t, registry, ds, deploy)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "checkout", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := len(store.All()); got != 0 {
		t.Fatalf("expected no deploy event while rollout is in progress, got %d", got)
	}
}

// TestWorkloadWatchReconcile_DoesNotDuplicateOnRepeatReconcile verifies that
// reconciling the same, already-reported revision again (e.g. triggered by
// an unrelated status field update) does not emit a second DeployEvent.
func TestWorkloadWatchReconcile_DoesNotDuplicateOnRepeatReconcile(t *testing.T) {
	registry := NewRegistry()
	watchKey := types.NamespacedName{Name: "watch-x", Namespace: "default"}
	store := &ingester.Store{}
	registry.Set(watchKey, &WatchRuntime{Store: store})

	ds := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-native", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "kubernetes",
			WorkloadKind:     "Deployment",
			AppName:          "checkout",
		},
	}
	deploy := completeDeployment("checkout", "default", "5")

	r := newWorkloadWatchReconciler(t, registry, ds, deploy)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "checkout", Namespace: "default"}}

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("reconcile #%d: %v", i, err)
		}
	}
	if got := len(store.All()); got != 1 {
		t.Fatalf("expected exactly 1 deploy event across 3 reconciles of the same revision, got %d", got)
	}
}

// TestWorkloadWatchReconcile_StatefulSet verifies the StatefulSet path uses
// status.currentRevision/updateRevision (StatefulSet has no
// deployment.kubernetes.io/revision annotation and no "available replicas"
// concept — it signals rollout completion differently from Deployment).
func TestWorkloadWatchReconcile_StatefulSet(t *testing.T) {
	registry := NewRegistry()
	watchKey := types.NamespacedName{Name: "watch-x", Namespace: "default"}
	store := &ingester.Store{}
	registry.Set(watchKey, &WatchRuntime{Store: store})

	ds := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-native", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "kubernetes",
			WorkloadKind:     "StatefulSet",
			AppName:          "primary-db",
		},
	}
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "primary-db", Namespace: "default", Generation: 1},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr.To(int32(1))},
		Status: appsv1.StatefulSetStatus{
			ObservedGeneration: 1,
			UpdatedReplicas:    1,
			CurrentRevision:    "primary-db-abc123",
			UpdateRevision:     "primary-db-abc123",
		},
	}

	r := newWorkloadWatchReconciler(t, registry, ds, sts)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "primary-db", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	events := store.All()
	if len(events) != 1 || events[0].Revision != "primary-db-abc123" {
		t.Fatalf("expected exactly 1 deploy event with the StatefulSet's updateRevision, got %+v", events)
	}
}

// TestWorkloadWatchReconcile_IgnoresUnmatchedWorkload verifies that
// reconciling a workload no DeploySource references is a safe no-op — this
// is the overwhelmingly common case in a real cluster, where most
// Deployments have nothing to do with pg-regression-radar.
func TestWorkloadWatchReconcile_IgnoresUnmatchedWorkload(t *testing.T) {
	registry := NewRegistry()
	deploy := completeDeployment("unrelated-app", "default", "1")
	r := newWorkloadWatchReconciler(t, registry, deploy)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "unrelated-app", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// TestWorkloadWatchReconcile_RequeuesWhenPostgresWatchNotReady verifies that
// a matched rollout, whose target PostgresWatch isn't running yet, is
// retried (RequeueAfter > 0) rather than silently dropped — a workload's
// status may never change again after a rollout completes, so there is no
// guarantee anything else would ever give this a second chance.
func TestWorkloadWatchReconcile_RequeuesWhenPostgresWatchNotReady(t *testing.T) {
	registry := NewRegistry() // empty: watch-x is not registered yet

	ds := &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: "src-native", Namespace: "default"},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: "watch-x",
			SourceType:       "kubernetes",
			WorkloadKind:     "Deployment",
			AppName:          "checkout",
		},
	}
	deploy := completeDeployment("checkout", "default", "5")

	r := newWorkloadWatchReconciler(t, registry, ds, deploy)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "checkout", Namespace: "default"}}

	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected a positive RequeueAfter while the target PostgresWatch isn't ready, got %v", res.RequeueAfter)
	}

	// Once the watch becomes available, the *same* revision must still be
	// reported — the earlier miss must not have been cached as "already
	// emitted".
	store := &ingester.Store{}
	registry.Set(types.NamespacedName{Name: "watch-x", Namespace: "default"}, &WatchRuntime{Store: store})
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile after watch became ready: %v", err)
	}
	if got := len(store.All()); got != 1 {
		t.Fatalf("expected the deploy event to be delivered once the watch became ready, got %d events", got)
	}
}
