package controller

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

// testScheme builds a runtime.Scheme with our CRD types registered, as
// mgr.GetScheme() would provide in production.
func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := radarv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return s
}

func newTestReconciler(t *testing.T, objs ...client.Object) (*PostgresWatchReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresWatch{}, &radarv1alpha1.PerformanceRegression{}).
		WithObjects(objs...).
		Build()

	r := &PostgresWatchReconciler{
		Client:   c,
		Scheme:   s,
		Registry: NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return r, c
}

func samplePostgresWatch(name, namespace string) *radarv1alpha1.PostgresWatch {
	return &radarv1alpha1.PostgresWatch{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: radarv1alpha1.PostgresWatchSpec{
			ClusterName:            "test-cluster",
			DSN:                    "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable",
			ScrapeIntervalSeconds:  3600, // long enough that no scrape fires during the test
			WindowMinutes:          30,
			MinExecutions:          10,
			LatencyChangeThreshold: "0.20",
			PValueThreshold:        "0.05",
		},
	}
}

// TestReconcile_CreateStartsWorker verifies that reconciling a newly created
// PostgresWatch starts a tracked WatchRuntime and reports phase Running.
func TestReconcile_CreateStartsWorker(t *testing.T) {
	watch := samplePostgresWatch("watch-a", "default")
	r, c := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-a", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered after create")
	}
	if rt.Collector == nil || rt.Engine == nil || rt.Notifier == nil {
		t.Fatal("expected WatchRuntime to have a Collector, Engine, and Notifier")
	}
	defer rt.Cancel()

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseRunning {
		t.Fatalf("expected phase Running, got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}
}

// TestReconcile_DeleteStopsWorker verifies that once the CR is gone,
// reconciling cancels and forgets its WatchRuntime.
func TestReconcile_DeleteStopsWorker(t *testing.T) {
	watch := samplePostgresWatch("watch-b", "default")
	r, c := newTestReconciler(t, watch)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-b", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	if _, ok := r.Registry.Get(req.NamespacedName); !ok {
		t.Fatal("expected worker to be registered after create")
	}

	if err := c.Delete(context.Background(), watch); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile after delete: %v", err)
	}

	if _, ok := r.Registry.Get(req.NamespacedName); ok {
		t.Fatal("expected worker to be stopped and unregistered after delete")
	}
}

// TestReconcile_SpecChangeRestartsWorker verifies that changing the spec
// (which changes the effective config fingerprint) tears down the old
// WatchRuntime and replaces it with a new one, rather than reusing the old
// one silently.
func TestReconcile_SpecChangeRestartsWorker(t *testing.T) {
	watch := samplePostgresWatch("watch-c", "default")
	r, c := newTestReconciler(t, watch)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-c", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	first, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected worker after first reconcile")
	}

	// Reconciling again with no spec change must NOT replace the runtime.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("no-op reconcile: %v", err)
	}
	again, ok := r.Registry.Get(req.NamespacedName)
	if !ok || again != first {
		t.Fatal("expected the same WatchRuntime to survive a no-op reconcile")
	}

	// Now change the spec and expect a restart (new runtime instance).
	var current radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &current); err != nil {
		t.Fatalf("get: %v", err)
	}
	current.Spec.LatencyChangeThreshold = "0.50"
	if err := c.Update(context.Background(), &current); err != nil {
		t.Fatalf("update: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile after spec change: %v", err)
	}
	second, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a worker after spec change")
	}
	if second == first {
		t.Fatal("expected a new WatchRuntime instance after spec change, got the same one")
	}
	if second.SpecHash == first.SpecHash {
		t.Fatal("expected SpecHash to change when latencyChangeThreshold changes")
	}
	defer second.Cancel()

	// Give the superseded worker's context a moment to be observed as
	// cancelled; Cancel() itself is synchronous but the goroutines it
	// stops are not guaranteed to have exited instantaneously.
	time.Sleep(10 * time.Millisecond)
}

// TestReconcile_MissingDSNMarksFailed verifies that a PostgresWatch with
// neither dsn nor dsnSecretRef set is reported as Failed rather than
// panicking or silently doing nothing.
func TestReconcile_MissingDSNMarksFailed(t *testing.T) {
	watch := samplePostgresWatch("watch-d", "default")
	watch.Spec.DSN = ""
	r, c := newTestReconciler(t, watch)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-d", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected an error result when DSN cannot be resolved")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected phase Failed, got %q", got.Status.Phase)
	}
	if _, ok := r.Registry.Get(req.NamespacedName); ok {
		t.Fatal("expected no worker to be registered when DSN resolution fails")
	}
}
