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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// testScheme builds a runtime.Scheme with our CRD types registered, as
// mgr.GetScheme() would provide in production.
func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := radarv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	// corev1 is needed by any test that creates a Secret object directly
	// (dsnSecretRef / remoteClusterSecretRef tests); PostgresWatchReconciler
	// itself already depends on it transitively via r.Get(ctx, key,
	// &corev1.Secret{}) in resolveDSN/dsnSecretClient.
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 scheme: %v", err)
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

// validTestKubeconfig is a syntactically valid kubeconfig pointing at a
// loopback address nothing is listening on. It is enough for
// clientcmd.RESTConfigFromKubeConfig / client.New to succeed (neither does
// network I/O), while any actual request made with the resulting client
// fails fast with "connection refused" rather than hanging — useful for
// exercising the "remote cluster unreachable" path without a real second
// cluster.
const validTestKubeconfig = `
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: remote
contexts:
- context:
    cluster: remote
    user: remote
  name: remote
current-context: remote
users:
- name: remote
  user:
    token: fake-token-for-tests
`

func kubeconfigSecret(name, namespace, key, kubeconfig string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: []byte(kubeconfig)},
	}
}

// TestDSNSecretClient_DefaultsToHubClient verifies that a PostgresWatch
// without remoteClusterSecretRef resolves its DSN Secret via the
// reconciler's own (hub) client — the 100%-backward-compatible default
// path, unchanged from before remoteClusterSecretRef existed.
func TestDSNSecretClient_DefaultsToHubClient(t *testing.T) {
	watch := samplePostgresWatch("watch-hub", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	r, _ := newTestReconciler(t, watch)

	cl, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("dsnSecretClient: %v", err)
	}
	if cl != r.Client {
		t.Fatal("expected the hub client when remoteClusterSecretRef is unset")
	}
}

// TestDSNSecretClient_RemoteValidKubeconfig_ReturnsDistinctCachedClient
// verifies that a valid remoteClusterSecretRef produces a client.Client
// distinct from the hub client, and that the remoteClientCache reuses that
// same instance on a second call rather than rebuilding it.
func TestDSNSecretClient_RemoteValidKubeconfig_ReturnsDistinctCachedClient(t *testing.T) {
	watch := samplePostgresWatch("watch-remote", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", validTestKubeconfig)
	r, _ := newTestReconciler(t, watch, secret)

	cl1, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("dsnSecretClient (1st call): %v", err)
	}
	if cl1 == r.Client {
		t.Fatal("expected a remote client distinct from the hub client")
	}

	cl2, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("dsnSecretClient (2nd call): %v", err)
	}
	if cl1 != cl2 {
		t.Fatal("expected the cached remote client to be reused across calls")
	}
}

// TestDSNSecretClient_RemoteMissingSecret_ReturnsError verifies that a
// remoteClusterSecretRef naming a Secret that doesn't exist in the hub
// cluster surfaces a clear error instead of a nil-client panic.
func TestDSNSecretClient_RemoteMissingSecret_ReturnsError(t *testing.T) {
	watch := samplePostgresWatch("watch-remote-missing", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "does-not-exist", Key: "kubeconfig"}
	r, _ := newTestReconciler(t, watch)

	if _, err := r.dsnSecretClient(context.Background(), watch); err == nil {
		t.Fatal("expected an error when the kubeconfig secret does not exist")
	}
}

// TestDSNSecretNamespace covers dsnSecretNamespace's decision table
// directly: remoteNamespace only ever takes effect alongside a remote
// cluster, and the pre-existing same-name-on-both-sides default is
// otherwise preserved exactly as before this field existed.
func TestDSNSecretNamespace(t *testing.T) {
	remoteRef := &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	tests := []struct {
		name                   string
		namespace              string
		remoteClusterSecretRef *radarv1alpha1.SecretKeySelector
		remoteNamespace        string
		want                   string
	}{
		{
			name:      "no remote cluster, no override: watch's own namespace",
			namespace: "hub-ns",
			want:      "hub-ns",
		},
		{
			name:            "remoteNamespace set without a remote cluster: ignored",
			namespace:       "hub-ns",
			remoteNamespace: "spoke-ns",
			want:            "hub-ns",
		},
		{
			name:                   "remote cluster set, no override: same name as hub namespace",
			namespace:              "hub-ns",
			remoteClusterSecretRef: remoteRef,
			want:                   "hub-ns",
		},
		{
			name:                   "remote cluster set with override: remoteNamespace wins",
			namespace:              "hub-ns",
			remoteClusterSecretRef: remoteRef,
			remoteNamespace:        "spoke-ns",
			want:                   "spoke-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			watch := &radarv1alpha1.PostgresWatch{
				ObjectMeta: metav1.ObjectMeta{Namespace: tt.namespace},
				Spec: radarv1alpha1.PostgresWatchSpec{
					RemoteClusterSecretRef: tt.remoteClusterSecretRef,
					RemoteNamespace:        tt.remoteNamespace,
				},
			}
			if got := dsnSecretNamespace(watch); got != tt.want {
				t.Fatalf("dsnSecretNamespace() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDSNSecretClient_RemoteMissingKey_ReturnsError verifies that a
// kubeconfig Secret existing but lacking the named key is a clear error.
func TestDSNSecretClient_RemoteMissingKey_ReturnsError(t *testing.T) {
	watch := samplePostgresWatch("watch-remote-badkey", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "wrong-key", validTestKubeconfig)
	r, _ := newTestReconciler(t, watch, secret)

	if _, err := r.dsnSecretClient(context.Background(), watch); err == nil {
		t.Fatal("expected an error when the kubeconfig secret lacks the named key")
	}
}

// TestDSNSecretClient_RemoteInvalidKubeconfig_ReturnsError verifies that
// malformed kubeconfig content is rejected with an error rather than
// panicking or silently producing an unusable client.
func TestDSNSecretClient_RemoteInvalidKubeconfig_ReturnsError(t *testing.T) {
	watch := samplePostgresWatch("watch-remote-invalid", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", "this is not a valid kubeconfig")
	r, _ := newTestReconciler(t, watch, secret)

	if _, err := r.dsnSecretClient(context.Background(), watch); err == nil {
		t.Fatal("expected an error for malformed kubeconfig content")
	}
}

// TestReconcile_RemoteClusterInvalidKubeconfigMarksFailed exercises the
// full Reconcile path (not just dsnSecretClient) to verify that a bad
// remoteClusterSecretRef surfaces the same Failed-phase-with-backoff
// behaviour as any other DSN resolution error (see
// TestReconcile_MissingDSNMarksFailed), rather than a special case.
func TestReconcile_RemoteClusterInvalidKubeconfigMarksFailed(t *testing.T) {
	watch := samplePostgresWatch("watch-f", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", "not: [valid, kubeconfig")
	r, c := newTestReconciler(t, watch, secret)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-f", Namespace: "default"}}

	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected an error result when the remote kubeconfig is invalid")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected phase Failed, got %q", got.Status.Phase)
	}
	if _, ok := r.Registry.Get(req.NamespacedName); ok {
		t.Fatal("expected no worker to be registered when the remote kubeconfig is invalid")
	}
}

// TestReconcile_RemoteClusterUnreachableMarksFailed exercises a valid
// kubeconfig pointing at an address nothing is listening on, standing in
// for a genuinely unreachable remote cluster: the DSN Secret fetch should
// fail (connection refused, not a hang) and Reconcile should mark the
// PostgresWatch Failed with backoff, exactly as it does for a bad DSN.
func TestReconcile_RemoteClusterUnreachableMarksFailed(t *testing.T) {
	watch := samplePostgresWatch("watch-g", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", validTestKubeconfig)
	r, c := newTestReconciler(t, watch, secret)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-g", Namespace: "default"}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := r.Reconcile(ctx, req); err == nil {
		t.Fatal("expected an error result when the remote cluster is unreachable")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected phase Failed, got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}
	if _, ok := r.Registry.Get(req.NamespacedName); ok {
		t.Fatal("expected no worker to be registered when the remote cluster is unreachable")
	}
}

// TestReconcile_CapturePlansPropagatesToRuntime verifies that
// spec.capturePlans reaches both the Collector's Config (so it actually
// captures plan snapshots) and the WatchRuntime itself (so pollLoop knows to
// call PlansAround for a detected regression), the same way ClusterName and
// every other spec-derived setting is threaded through startWatch.
func TestReconcile_CapturePlansPropagatesToRuntime(t *testing.T) {
	watch := samplePostgresWatch("watch-h", "default")
	watch.Spec.CapturePlans = true
	r, _ := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-h", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered after create")
	}
	defer rt.Cancel()

	if !rt.CapturePlans {
		t.Fatal("expected WatchRuntime.CapturePlans to be true when spec.capturePlans is true")
	}
}

// TestReconcile_CapturePlansDefaultsFalse is the inverse of the above:
// leaving spec.capturePlans unset must not silently enable plan capture,
// since it adds a per-scrape-cycle EXPLAIN/lookup cost that should stay
// opt-in.
func TestReconcile_CapturePlansDefaultsFalse(t *testing.T) {
	watch := samplePostgresWatch("watch-i", "default")
	r, _ := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-i", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered after create")
	}
	defer rt.Cancel()

	if rt.CapturePlans {
		t.Fatal("expected WatchRuntime.CapturePlans to default to false")
	}
}

// TestApplyRegressionStatus_IncludesPlanDiffSummary verifies the internal
// DTO's PlanDiffSummary (set by pollLoop when CapturePlans is enabled) is
// copied onto the CRD's status, exactly like every other analysis field —
// this is the one field the manager path previously dropped on the floor,
// even when a caller had already computed a plan diff.
func TestApplyRegressionStatus_IncludesPlanDiffSummary(t *testing.T) {
	obj := &radarv1alpha1.PerformanceRegression{}
	res := dto.PerformanceRegression{
		Status:          dto.StatusDetected,
		QueryID:         42,
		PlanDiffSummary: "root node changed from Index Scan to Seq Scan",
		CreatedAt:       time.Now(),
	}

	applyRegressionStatus(obj, res)

	if obj.Status.PlanDiffSummary != res.PlanDiffSummary {
		t.Fatalf("expected status.planDiffSummary %q, got %q", res.PlanDiffSummary, obj.Status.PlanDiffSummary)
	}
}

// TestApplyRegressionStatus_OmitsPlanDiffSummaryWhenEmpty guards the common
// case (CapturePlans disabled): applyRegressionStatus must not invent a
// plan-diff summary out of thin air.
func TestApplyRegressionStatus_OmitsPlanDiffSummaryWhenEmpty(t *testing.T) {
	obj := &radarv1alpha1.PerformanceRegression{}
	res := dto.PerformanceRegression{
		Status:    dto.StatusDetected,
		QueryID:   42,
		CreatedAt: time.Now(),
	}

	applyRegressionStatus(obj, res)

	if obj.Status.PlanDiffSummary != "" {
		t.Fatalf("expected empty status.planDiffSummary, got %q", obj.Status.PlanDiffSummary)
	}
}
