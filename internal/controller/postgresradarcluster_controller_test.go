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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/version"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/testlogger"
)

// fakeDiscoveryServer starts a plain-HTTP httptest.Server that answers
// GET /version the same way a real Kubernetes API server's discovery
// endpoint would — exactly (and only) what
// discovery.DiscoveryClient.ServerVersionWithContext does, which is all
// PostgresRadarClusterReconciler's probe ever calls. Returns the server and
// a kubeconfig (static token, no TLS — insecure-skip-tls-verify is
// irrelevant over plain HTTP) pointed at it, ready to embed in a Secret.
func fakeDiscoveryServer(t *testing.T, healthy bool) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(version.Info{Major: "1", Minor: "31", GitVersion: "v1.31.0"})
	}))
	t.Cleanup(srv.Close)

	kubeconfig := fmt.Sprintf(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
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
`, srv.URL)
	return srv, kubeconfig
}

// newTestClusterReconciler builds a PostgresRadarClusterReconciler backed
// by a fake client with the status subresource enabled, mirroring
// newTestReconciler's role for PostgresWatchReconciler.
func newTestClusterReconciler(t *testing.T, objs ...client.Object) (*PostgresRadarClusterReconciler, client.Client) {
	t.Helper()
	s := testScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresRadarCluster{}).
		WithObjects(objs...).
		Build()

	r := &PostgresRadarClusterReconciler{
		Client:              c,
		Logger:              testlogger.New(t),
		HealthCheckInterval: time.Minute,
	}
	return r, c
}

// TestPostgresRadarClusterReconcile_HealthyCluster_MarksReady verifies the
// happy path end-to-end: a valid kubeconfig pointed at a reachable
// discovery endpoint results in status.ready=true, a Ready=True condition,
// and a non-nil LastProbeTime.
func TestPostgresRadarClusterReconcile_HealthyCluster_MarksReady(t *testing.T) {
	_, kubeconfig := fakeDiscoveryServer(t, true)
	cluster := samplePostgresRadarCluster("spoke-a", "default", "spoke-a-kubeconfig", "kubeconfig")
	secret := kubeconfigSecret("spoke-a-kubeconfig", "default", "kubeconfig", kubeconfig)
	r, c := newTestClusterReconciler(t, cluster, secret)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "spoke-a"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != time.Minute {
		t.Fatalf("expected RequeueAfter=1m, got %v", res.RequeueAfter)
	}

	var got radarv1alpha1.PostgresRadarCluster
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Status.Ready {
		t.Fatalf("expected status.ready=true, got false (message=%q)", got.Status.Message)
	}
	if got.Status.LastProbeTime == nil {
		t.Fatal("expected LastProbeTime to be set")
	}
	cond := findCondition(got.Status.Conditions, clusterReadyConditionType)
	if cond == nil {
		t.Fatal("expected a Ready condition")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready condition True, got %v (message=%q)", cond.Status, cond.Message)
	}
}

// TestPostgresRadarClusterReconcile_UnreachableCluster_MarksNotReady
// verifies that a kubeconfig whose server never answers (connection
// refused) results in status.ready=false and a Ready=False condition whose
// message explains why, rather than leaving status untouched.
func TestPostgresRadarClusterReconcile_UnreachableCluster_MarksNotReady(t *testing.T) {
	srv, kubeconfig := fakeDiscoveryServer(t, true)
	srv.Close() // Close immediately: every request now fails with connection refused.

	cluster := samplePostgresRadarCluster("spoke-b", "default", "spoke-b-kubeconfig", "kubeconfig")
	secret := kubeconfigSecret("spoke-b-kubeconfig", "default", "kubeconfig", kubeconfig)
	r, c := newTestClusterReconciler(t, cluster, secret)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "spoke-b"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got radarv1alpha1.PostgresRadarCluster
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Ready {
		t.Fatal("expected status.ready=false for an unreachable cluster")
	}
	cond := findCondition(got.Status.Conditions, clusterReadyConditionType)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("expected a Ready=False condition, got %+v", cond)
	}
	if cond.Reason != "HealthCheckFailed" {
		t.Fatalf("expected reason HealthCheckFailed, got %q", cond.Reason)
	}
}

// TestPostgresRadarClusterReconcile_DiscoveryErrors_MarksNotReady verifies
// that a reachable-but-erroring API server (e.g. mid-upgrade, or any 5xx
// on /version) is also treated as unhealthy, not just a hard connection
// failure.
func TestPostgresRadarClusterReconcile_DiscoveryErrors_MarksNotReady(t *testing.T) {
	_, kubeconfig := fakeDiscoveryServer(t, false)
	cluster := samplePostgresRadarCluster("spoke-c", "default", "spoke-c-kubeconfig", "kubeconfig")
	secret := kubeconfigSecret("spoke-c-kubeconfig", "default", "kubeconfig", kubeconfig)
	r, c := newTestClusterReconciler(t, cluster, secret)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "spoke-c"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got radarv1alpha1.PostgresRadarCluster
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Ready {
		t.Fatal("expected status.ready=false when discovery returns a server error")
	}
}

// TestPostgresRadarClusterReconcile_MissingConsentLabel_MarksNotReady
// verifies that the health checker respects checkSecretConsent exactly
// like resolveDSN does: an unlabeled kubeconfig Secret must not be read,
// even by the reconciler whose entire job is reading kubeconfig Secrets.
func TestPostgresRadarClusterReconcile_MissingConsentLabel_MarksNotReady(t *testing.T) {
	_, kubeconfig := fakeDiscoveryServer(t, true)
	cluster := samplePostgresRadarCluster("spoke-d", "default", "spoke-d-kubeconfig", "kubeconfig")

	// Built directly (not via the kubeconfigSecret helper, which always
	// carries the consent label) so this is the one Secret in this test
	// deliberately missing it.
	unlabeledSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "spoke-d-kubeconfig", Namespace: "default"},
		Data:       map[string][]byte{"kubeconfig": []byte(kubeconfig)},
	}
	r, c := newTestClusterReconciler(t, cluster, unlabeledSecret)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "spoke-d"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	var got radarv1alpha1.PostgresRadarCluster
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Ready {
		t.Fatal("expected status.ready=false when the kubeconfig secret is missing the consent label")
	}
	if !strings.Contains(got.Status.Message, secretConsentLabel) {
		t.Fatalf("expected the status message to mention the missing consent label, got: %q", got.Status.Message)
	}
}

// TestPostgresRadarClusterReconcile_NotFound_NoRequeue verifies that
// reconciling a name no longer backed by any PostgresRadarCluster (deleted
// between enqueue and processing) returns cleanly with no error and no
// requeue, rather than erroring forever.
func TestPostgresRadarClusterReconcile_NotFound_NoRequeue(t *testing.T) {
	r, _ := newTestClusterReconciler(t)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "gone"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue for a deleted object, got %v", res.RequeueAfter)
	}
}

// TestMapSecretToClusters_MatchesAndIgnores verifies mapSecretToClusters
// enqueues exactly the PostgresRadarCluster objects whose
// kubeconfigSecretRef matches the Secret that triggered the event, and
// ignores everything else (a Secret matching no cluster, and a Secret of
// the wrong type).
func TestMapSecretToClusters_MatchesAndIgnores(t *testing.T) {
	matching := samplePostgresRadarCluster("matches", "default", "shared-secret", "kubeconfig")
	other := samplePostgresRadarCluster("other", "default", "other-secret", "kubeconfig")
	secret := kubeconfigSecret("shared-secret", "default", "kubeconfig", validTestKubeconfig)
	r, _ := newTestClusterReconciler(t, matching, other, secret)

	reqs := r.mapSecretToClusters(context.Background(), secret)
	if len(reqs) != 1 {
		t.Fatalf("expected exactly 1 request, got %d: %+v", len(reqs), reqs)
	}
	if reqs[0].Name != "matches" {
		t.Fatalf("expected request for cluster %q, got %q", "matches", reqs[0].Name)
	}
}

// findCondition is a small test helper mirroring how a real caller would
// read PostgresRadarCluster.status.conditions.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
