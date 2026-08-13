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
	"errors"
	"io"
	"log/slog"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// stubAborter is a test double for RolloutAborter: it records the last
// call it received and returns whatever error it was configured with,
// without touching any real Kubernetes API.
type stubAborter struct {
	called    bool
	namespace string
	name      string
	err       error
}

func (s *stubAborter) Abort(_ context.Context, namespace, name string) error {
	s.called = true
	s.namespace = namespace
	s.name = name
	return s.err
}

// TestReconcile_AutoAbortPropagatesToRuntime verifies that
// spec.autoAbort reaches the WatchRuntime the same way spec.capturePlans
// does (see TestReconcile_CapturePlansPropagatesToRuntime) — enabled,
// confidenceThreshold parsed, and the reconciler's Aborter carried over —
// so pollLoop has everything it needs without re-reading the CR.
func TestReconcile_AutoAbortPropagatesToRuntime(t *testing.T) {
	watch := samplePostgresWatch("watch-autoabort-a", "default")
	watch.Spec.AutoAbort = &radarv1alpha1.AutoAbortConfig{Enabled: true, ConfidenceThreshold: "0.95"}

	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresWatch{}, &radarv1alpha1.PerformanceRegression{}).
		WithObjects(watch).
		Build()
	aborter := &stubAborter{}
	r := &PostgresWatchReconciler{
		Client:   c,
		Scheme:   s,
		Registry: NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Aborter:  aborter,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-autoabort-a", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered after create")
	}
	defer rt.Cancel()

	if !rt.AutoAbortEnabled {
		t.Fatal("expected AutoAbortEnabled to be true when spec.autoAbort.enabled is true and an Aborter is configured")
	}
	if rt.AutoAbortThreshold != 0.95 {
		t.Fatalf("expected AutoAbortThreshold 0.95, got %v", rt.AutoAbortThreshold)
	}
	if rt.Aborter != aborter {
		t.Fatal("expected WatchRuntime.Aborter to be the reconciler's configured Aborter")
	}
}

// TestReconcile_AutoAbortDisabledWithoutAborter verifies the safety
// fallback: even if a PostgresWatch asks for auto-abort, a reconciler with
// no Aborter configured (e.g. cmd/manager couldn't build a dynamic client)
// must never set AutoAbortEnabled — pollLoop must degrade to alert-only
// rather than risk a nil-pointer call.
func TestReconcile_AutoAbortDisabledWithoutAborter(t *testing.T) {
	watch := samplePostgresWatch("watch-autoabort-b", "default")
	watch.Spec.AutoAbort = &radarv1alpha1.AutoAbortConfig{Enabled: true}
	r, _ := newTestReconciler(t, watch) // no Aborter set

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-autoabort-b", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered after create")
	}
	defer rt.Cancel()

	if rt.AutoAbortEnabled {
		t.Fatal("expected AutoAbortEnabled to stay false when the reconciler has no Aborter, regardless of spec.autoAbort.enabled")
	}
}

// TestReconcile_AutoAbortDefaultsFalse verifies that leaving spec.autoAbort
// unset entirely (the overwhelmingly common case) never enables auto-abort,
// even when an Aborter happens to be configured.
func TestReconcile_AutoAbortDefaultsFalse(t *testing.T) {
	watch := samplePostgresWatch("watch-autoabort-c", "default")

	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresWatch{}, &radarv1alpha1.PerformanceRegression{}).
		WithObjects(watch).
		Build()
	r := &PostgresWatchReconciler{
		Client:   c,
		Scheme:   s,
		Registry: NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Aborter:  &stubAborter{},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-autoabort-c", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered after create")
	}
	defer rt.Cancel()

	if rt.AutoAbortEnabled {
		t.Fatal("expected AutoAbortEnabled to default to false when spec.autoAbort is unset")
	}
	if rt.AutoAbortThreshold != 0.99 {
		t.Fatalf("expected the default confidence threshold 0.99, got %v", rt.AutoAbortThreshold)
	}
}

// deploySource is a small helper to keep the maybeAutoAbort tests below
// readable.
func deploySource(namespace, name, sourceType, postgresWatchRef string) *radarv1alpha1.DeploySource {
	return &radarv1alpha1.DeploySource{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: radarv1alpha1.DeploySourceSpec{
			PostgresWatchRef: postgresWatchRef,
			SourceType:       sourceType,
		},
	}
}

// TestMaybeAutoAbort_TriggersForArgoRolloutsSource verifies the core
// decision: a detected regression whose deploy event came from a
// DeploySource with sourceType "argo-rollouts" gets an abort attempt,
// targeting the Rollout named after the deploy event's App, in its
// Namespace — and the outcome is recorded on the PerformanceRegression DTO.
func TestMaybeAutoAbort_TriggersForArgoRolloutsSource(t *testing.T) {
	src := deploySource("default", "rollouts-src", "argo-rollouts", "watch-x")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(src).Build()

	aborter := &stubAborter{}
	r := &PostgresWatchReconciler{Client: c, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rt := &WatchRuntime{Aborter: aborter}
	ev := dto.DeployEvent{Source: "rollouts-src", App: "checkout", Namespace: "default"}
	res := &dto.PerformanceRegression{ConfidenceScore: 0.995, QueryID: 1}

	r.maybeAutoAbort(context.Background(), types.NamespacedName{Namespace: "default", Name: "watch-x"}, rt, ev, res)

	if !aborter.called {
		t.Fatal("expected Abort to be called for an argo-rollouts-sourced deploy event")
	}
	if aborter.namespace != "default" || aborter.name != "checkout" {
		t.Fatalf("expected Abort(default, checkout), got Abort(%s, %s)", aborter.namespace, aborter.name)
	}
	if !res.AutoAbortTriggered {
		t.Fatal("expected AutoAbortTriggered to be true")
	}
	if res.AutoAbortError != "" {
		t.Fatalf("expected no AutoAbortError on success, got %q", res.AutoAbortError)
	}
}

// TestMaybeAutoAbort_SkipsNonArgoRolloutsSource verifies that deploys from
// argocd/flux/generic/kubernetes sources are never auto-aborted, even with
// AutoAbortEnabled and a high-confidence detection — there is no
// "abort mid-rollout" primitive to call for any of them.
func TestMaybeAutoAbort_SkipsNonArgoRolloutsSource(t *testing.T) {
	for _, sourceType := range []string{"argocd", "flux", "generic", "kubernetes"} {
		src := deploySource("default", "other-src", sourceType, "watch-x")
		s := testScheme(t)
		c := fake.NewClientBuilder().WithScheme(s).WithObjects(src).Build()

		aborter := &stubAborter{}
		r := &PostgresWatchReconciler{Client: c, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		rt := &WatchRuntime{Aborter: aborter}
		ev := dto.DeployEvent{Source: "other-src", App: "checkout", Namespace: "default"}
		res := &dto.PerformanceRegression{ConfidenceScore: 0.995, QueryID: 1}

		r.maybeAutoAbort(context.Background(), types.NamespacedName{Namespace: "default", Name: "watch-x"}, rt, ev, res)

		if aborter.called {
			t.Fatalf("sourceType %q: expected Abort not to be called", sourceType)
		}
		if res.AutoAbortTriggered {
			t.Fatalf("sourceType %q: expected AutoAbortTriggered to stay false", sourceType)
		}
	}
}

// TestMaybeAutoAbort_SkipsMissingDeploySource verifies that a deploy event
// whose DeploySource no longer exists (e.g. deleted between detection and
// this poll tick) is handled as a safe no-op, not a crash.
func TestMaybeAutoAbort_SkipsMissingDeploySource(t *testing.T) {
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build() // no DeploySource at all

	aborter := &stubAborter{}
	r := &PostgresWatchReconciler{Client: c, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rt := &WatchRuntime{Aborter: aborter}
	ev := dto.DeployEvent{Source: "does-not-exist", App: "checkout", Namespace: "default"}
	res := &dto.PerformanceRegression{ConfidenceScore: 0.995, QueryID: 1}

	r.maybeAutoAbort(context.Background(), types.NamespacedName{Namespace: "default", Name: "watch-x"}, rt, ev, res)

	if aborter.called {
		t.Fatal("expected Abort not to be called when the DeploySource is missing")
	}
	if res.AutoAbortTriggered {
		t.Fatal("expected AutoAbortTriggered to stay false when the DeploySource is missing")
	}
}

// TestMaybeAutoAbort_RecordsAbortError verifies that a failed abort attempt
// (e.g. the Rollout doesn't actually exist, or RBAC wasn't granted) is
// still recorded as triggered — the attempt was made — with the error
// captured for visibility on the resulting PerformanceRegression CR.
func TestMaybeAutoAbort_RecordsAbortError(t *testing.T) {
	src := deploySource("default", "rollouts-src", "argo-rollouts", "watch-x")
	s := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(src).Build()

	aborter := &stubAborter{err: errors.New("rollouts.argoproj.io \"checkout\" not found")}
	r := &PostgresWatchReconciler{Client: c, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rt := &WatchRuntime{Aborter: aborter}
	ev := dto.DeployEvent{Source: "rollouts-src", App: "checkout", Namespace: "default"}
	res := &dto.PerformanceRegression{ConfidenceScore: 0.995, QueryID: 1}

	r.maybeAutoAbort(context.Background(), types.NamespacedName{Namespace: "default", Name: "watch-x"}, rt, ev, res)

	if !res.AutoAbortTriggered {
		t.Fatal("expected AutoAbortTriggered to be true even though the attempt failed — the attempt was made")
	}
	if res.AutoAbortError == "" {
		t.Fatal("expected AutoAbortError to capture the failure")
	}
}
