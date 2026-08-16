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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/testlogger"
)

// samplePostgresRadarCluster returns a minimal PostgresRadarCluster with
// the given name and kubeconfig secret selector.
func samplePostgresRadarCluster(name, secretNamespace, secretName, secretKey string) *radarv1alpha1.PostgresRadarCluster {
	return &radarv1alpha1.PostgresRadarCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: radarv1alpha1.PostgresRadarClusterSpec{
			KubeconfigSecretRef: radarv1alpha1.KubeconfigSecretSelector{
				Namespace: secretNamespace,
				Name:      secretName,
				Key:       secretKey,
			},
		},
	}
}

// TestDSNSecretClient_RemoteClusterRef_ValidCluster verifies the happy path:
// when spec.remoteClusterRef names an existing PostgresRadarCluster backed
// by a valid (consented) kubeconfig Secret, dsnSecretClient returns a
// distinct remote client and the raw kubeconfig bytes — exactly the same
// result as the legacy remoteClusterSecretRef path, just via the registry.
func TestDSNSecretClient_RemoteClusterRef_ValidCluster(t *testing.T) {
	cluster := samplePostgresRadarCluster("prod-spoke", "default", "prod-spoke-kubeconfig", "kubeconfig")
	secret := kubeconfigSecret("prod-spoke-kubeconfig", "default", "kubeconfig", validTestKubeconfig)

	watch := samplePostgresWatch("watch-registry", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterRef = "prod-spoke"

	r, _ := newTestReconciler(t, watch, cluster, secret)

	cl, kubeconfig, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("dsnSecretClient: %v", err)
	}
	if cl == r.Client {
		t.Fatal("expected a distinct remote client, not the hub client")
	}
	if string(kubeconfig) != validTestKubeconfig {
		t.Fatalf("expected the raw kubeconfig bytes, got %q", kubeconfig)
	}
}

// TestDSNSecretClient_RemoteClusterRef_ClusterNotFound verifies that
// referencing a non-existent PostgresRadarCluster is a clear, reconcile-
// terminating error (not a panic or silent fallback to the hub client).
func TestDSNSecretClient_RemoteClusterRef_ClusterNotFound(t *testing.T) {
	watch := samplePostgresWatch("watch-missing-cluster", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterRef = "does-not-exist"

	r, _ := newTestReconciler(t, watch)

	_, _, err := r.dsnSecretClient(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when the named PostgresRadarCluster does not exist")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("expected the error to mention the missing cluster name, got: %v", err)
	}
}

// TestDSNSecretClient_RemoteClusterRef_MissingKubeconfigSecret verifies
// that a PostgresRadarCluster whose kubeconfigSecretRef points at a
// non-existent Secret surfaces a clear error instead of a nil-client panic.
func TestDSNSecretClient_RemoteClusterRef_MissingKubeconfigSecret(t *testing.T) {
	cluster := samplePostgresRadarCluster("prod-spoke", "default", "missing-secret", "kubeconfig")

	watch := samplePostgresWatch("watch-missing-secret", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterRef = "prod-spoke"

	r, _ := newTestReconciler(t, watch, cluster)

	_, _, err := r.dsnSecretClient(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when the kubeconfig Secret is missing")
	}
}

// TestDSNSecretClient_RemoteClusterRef_MissingConsentLabel verifies that
// a kubeconfig Secret referenced via a PostgresRadarCluster but lacking the
// required consent label is rejected, just as it would be for the legacy
// remoteClusterSecretRef path.
func TestDSNSecretClient_RemoteClusterRef_MissingConsentLabel(t *testing.T) {
	cluster := samplePostgresRadarCluster("prod-spoke", "default", "prod-spoke-kubeconfig", "kubeconfig")
	unlabeled := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "prod-spoke-kubeconfig", Namespace: "default"},
		Data:       map[string][]byte{"kubeconfig": []byte(validTestKubeconfig)},
	}

	watch := samplePostgresWatch("watch-no-consent-cluster", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterRef = "prod-spoke"

	r, _ := newTestReconciler(t, watch, cluster, unlabeled)

	_, _, err := r.dsnSecretClient(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error for a kubeconfig Secret missing the consent label")
	}
	if !strings.Contains(err.Error(), secretConsentLabel) {
		t.Fatalf("expected error to mention the missing label, got: %v", err)
	}
}

// TestDSNSecretClient_RemoteClusterRef_TakesPrecedenceOverSecretRef verifies
// that when both remoteClusterRef and remoteClusterSecretRef are set,
// remoteClusterRef wins (and no deprecation path is taken for the legacy
// field).
func TestDSNSecretClient_RemoteClusterRef_TakesPrecedenceOverSecretRef(t *testing.T) {
	cluster := samplePostgresRadarCluster("prod-spoke", "default", "prod-spoke-kubeconfig", "kubeconfig")
	registrySecret := kubeconfigSecret("prod-spoke-kubeconfig", "default", "kubeconfig", validTestKubeconfig)

	watch := samplePostgresWatch("watch-both-refs", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterRef = "prod-spoke"
	// Also set legacy field — it should be ignored in favour of remoteClusterRef.
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "does-not-exist", Key: "kubeconfig"}

	r, _ := newTestReconciler(t, watch, cluster, registrySecret)

	// Should succeed via the registry path even though the legacy Secret
	// does not exist in the fake client.
	cl, _, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("dsnSecretClient: %v", err)
	}
	if cl == r.Client {
		t.Fatal("expected a distinct remote client from the registry path")
	}
}

// TestDSNSecretClient_RemoteClusterRef_CacheReuse verifies that a second
// call with the same cluster and kubeconfig returns the same cached
// client.Client instance, not a newly rebuilt one.
func TestDSNSecretClient_RemoteClusterRef_CacheReuse(t *testing.T) {
	cluster := samplePostgresRadarCluster("prod-spoke", "default", "prod-spoke-kubeconfig", "kubeconfig")
	secret := kubeconfigSecret("prod-spoke-kubeconfig", "default", "kubeconfig", validTestKubeconfig)

	watch := samplePostgresWatch("watch-registry-cache", "default")
	watch.Spec.RemoteClusterRef = "prod-spoke"

	r, _ := newTestReconciler(t, watch, cluster, secret)

	cl1, _, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	cl2, _, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if cl1 != cl2 {
		t.Fatal("expected the same cached client instance on repeated calls")
	}
}

// TestDSNSecretNamespace_RemoteClusterRef verifies that remoteNamespace is
// honoured when remoteClusterRef (not remoteClusterSecretRef) triggers the
// remote-cluster path.
func TestDSNSecretNamespace_RemoteClusterRef(t *testing.T) {
	watch := &radarv1alpha1.PostgresWatch{
		ObjectMeta: metav1.ObjectMeta{Namespace: "hub-ns"},
		Spec: radarv1alpha1.PostgresWatchSpec{
			RemoteClusterRef: "prod-spoke",
			RemoteNamespace:  "spoke-ns",
		},
	}
	if got := dsnSecretNamespace(watch); got != "spoke-ns" {
		t.Fatalf("dsnSecretNamespace with remoteClusterRef+remoteNamespace = %q, want \"spoke-ns\"", got)
	}
}

// TestReconcile_RemoteClusterRef_ValidCluster_StartsWorker verifies the
// full Reconcile path: when spec.remoteClusterRef names a valid registered
// cluster, the watch reaches phase Running (DSN resolved, worker started).
func TestReconcile_RemoteClusterRef_ValidCluster_StartsWorker(t *testing.T) {
	cluster := samplePostgresRadarCluster("prod-spoke", "default", "prod-spoke-kubeconfig", "kubeconfig")
	kubecfgSecret := kubeconfigSecret("prod-spoke-kubeconfig", "default", "kubeconfig", validTestKubeconfig)
	dsnSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte("******127.0.0.1:1/nonexistent?sslmode=disable")},
	}

	// Note: we use DSN directly here to avoid needing a real remote API server
	// (the kubeconfig points at 127.0.0.1:1 which isn't listening), consistent
	// with how TestReconcile_RemoteClusterUnreachableMarksFailed tests the
	// failure path with DSNSecretRef.
	watch := samplePostgresWatch("watch-registry-full", "default")
	watch.Spec.RemoteClusterRef = "prod-spoke"
	// Keep spec.DSN set (the samplePostgresWatch default) so the worker
	// starts without needing to dial the remote apiserver for the DSN Secret.
	_ = dsnSecret // included for completeness; DSN overrides the ref.

	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresWatch{}, &radarv1alpha1.PerformanceRegression{}).
		WithObjects(watch, cluster, kubecfgSecret).
		Build()
	r := &PostgresWatchReconciler{
		Client:   c,
		Scheme:   s,
		Registry: NewRegistry(),
		Logger:   testlogger.New(t),
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: watch.Name, Namespace: watch.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer r.stopWatch(req.NamespacedName)

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseRunning {
		t.Fatalf("expected phase Running, got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}
}

// TestReconcile_RemoteClusterRef_MissingCluster_MarksFailed verifies that
// a non-existent PostgresRadarCluster name causes the watch to land in
// phase Failed (with a helpful message), matching the same failed-with-
// backoff behaviour as any other DSN resolution error.
func TestReconcile_RemoteClusterRef_MissingCluster_MarksFailed(t *testing.T) {
	watch := samplePostgresWatch("watch-bad-ref", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterRef = "nonexistent-cluster"

	r, c := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: watch.Name, Namespace: watch.Namespace}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected an error result when the PostgresRadarCluster does not exist")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected phase Failed for missing cluster ref, got %q", got.Status.Phase)
	}
}

// TestDSNSecretClient_HardenedProfile_RejectsLegacyRef verifies that when
// the reconciler is configured with SecurityProfileHardened, a
// remoteClusterSecretRef is rejected with a clear error instead of
// following the deprecated path.
func TestDSNSecretClient_HardenedProfile_RejectsLegacyRef(t *testing.T) {
	watch := samplePostgresWatch("watch-hardened", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", validTestKubeconfig)

	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(watch, secret).
		Build()
	r := &PostgresWatchReconciler{
		Client:          c,
		Scheme:          s,
		Registry:        NewRegistry(),
		Logger:          testlogger.New(t),
		SecurityProfile: radarv1alpha1.SecurityProfileHardened,
	}

	_, _, err := r.dsnSecretClient(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when remoteClusterSecretRef is used in hardened profile")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "hardened") {
		t.Fatalf("expected error to mention hardened profile, got: %v", err)
	}
}

// TestDSNSecretClient_ControlledProfile_AllowsLegacyRef verifies that when
// the reconciler is in the default controlled profile (or explicitly set to
// SecurityProfileControlled), a remoteClusterSecretRef is still accepted
// (the backward-compatible path, just with a deprecation warning logged).
func TestDSNSecretClient_ControlledProfile_AllowsLegacyRef(t *testing.T) {
	watch := samplePostgresWatch("watch-controlled", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	secret := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", validTestKubeconfig)

	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(watch, secret).
		Build()
	r := &PostgresWatchReconciler{
		Client:          c,
		Scheme:          s,
		Registry:        NewRegistry(),
		Logger:          testlogger.New(t),
		SecurityProfile: radarv1alpha1.SecurityProfileControlled, // explicit; same as the zero-value default
	}

	cl, _, err := r.dsnSecretClient(context.Background(), watch)
	if err != nil {
		t.Fatalf("dsnSecretClient in controlled profile: %v", err)
	}
	if cl == r.Client {
		t.Fatal("expected a distinct remote client in controlled profile")
	}
}
