package controller

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

// TestEnvtest_PostgresWatchLifecycle exercises PostgresWatchReconciler
// against a real (locally spun up) Kubernetes API server + etcd using the
// generated CRD manifests in config/crd/bases, instead of the in-memory
// fake client the rest of this package's tests use. Unlike the fake
// client, this round-trips through real CRD structural-schema validation,
// defaulting, and the status subresource.
//
// This is best-effort, secondary coverage: the fake-client tests in
// postgreswatch_controller_test.go and deploysource_controller_test.go are
// this package's primary, always-on test suite and don't need any external
// binaries. This test requires the envtest control-plane binaries (etcd,
// kube-apiserver, kubectl); if they're not available it skips itself.
//
// To run it locally:
//
//	go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
//	export KUBEBUILDER_ASSETS="$(setup-envtest use 1.31.0 -p path)"
//	go test ./internal/controller/... -run TestEnvtest -v
//
// setup-envtest downloads prebuilt binaries from a Google Cloud Storage
// bucket; if that bucket isn't reachable from your network, this test (and
// only this test) is unavailable, but go build/vet/test for the rest of
// the module are unaffected.
func TestEnvtest_PostgresWatchLifecycle(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("skipping envtest: KUBEBUILDER_ASSETS not set; see this test's doc comment for setup instructions")
	}

	s := testScheme(t)

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		Scheme:                s,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Skipf("skipping envtest: could not start test environment: %v", err)
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			t.Logf("warning: envtest teardown failed: %v", err)
		}
	}()

	k8sClient, err := client.New(cfg, client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}

	r := &PostgresWatchReconciler{
		Client:   k8sClient,
		Scheme:   s,
		Registry: NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	watch := samplePostgresWatch("envtest-watch", "default")
	if err := k8sClient.Create(ctx, watch); err != nil {
		t.Fatalf("create postgreswatch against real apiserver: %v", err)
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: watch.Name, Namespace: watch.Namespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer r.stopWatch(req.NamespacedName)

	var got radarv1alpha1.PostgresWatch
	if err := k8sClient.Get(ctx, req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseRunning {
		t.Fatalf("expected phase Running from real apiserver round-trip, got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}

	if err := k8sClient.Delete(ctx, &got); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile after delete: %v", err)
	}
	if _, ok := r.Registry.Get(req.NamespacedName); ok {
		t.Fatal("expected worker to be stopped after real apiserver deletion")
	}
}
