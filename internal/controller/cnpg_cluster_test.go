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

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

// newTestCNPGCluster builds a minimal cnpgCluster object for use directly
// against the fake client (via newTestReconciler's WithObjects), exercising
// the exact same decode path a real CloudNativePG Cluster would go through.
func newTestCNPGCluster(name, namespace string, conditions []metav1.Condition, phase string) *cnpgCluster {
	return &cnpgCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Status: cnpgClusterStatus{
			Phase:      phase,
			Conditions: conditions,
		},
	}
}

// TestResolveDSN_CNPGClusterRef_Unset_SkipsValidation confirms that leaving
// spec.cnpgClusterRef unset (the default, 100%-backward-compatible case)
// never even attempts to fetch any CloudNativePG Cluster — a watch with no
// such object anywhere in its namespace must still resolve normally.
func TestResolveDSN_CNPGClusterRef_Unset_SkipsValidation(t *testing.T) {
	watch := samplePostgresWatch("watch-no-cnpg-ref", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}

	const dsn = "******127.0.0.1:5432/app"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte(dsn)},
	}
	r, _ := newTestReconciler(t, watch, secret)

	got, err := r.resolveDSN(context.Background(), watch)
	if err != nil {
		t.Fatalf("resolveDSN: %v", err)
	}
	if got != dsn {
		t.Fatalf("expected DSN %q, got %q", dsn, got)
	}
}

// TestResolveDSN_CNPGClusterRef_ReadyConditionTrue_Succeeds is the
// straightforward healthy path: a Ready=True condition must let DSN
// resolution through.
func TestResolveDSN_CNPGClusterRef_ReadyConditionTrue_Succeeds(t *testing.T) {
	watch := samplePostgresWatch("watch-cnpg-ready", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.CNPGClusterRef = "pg-cluster"

	const dsn = "******127.0.0.1:5432/app"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte(dsn)},
	}
	cluster := newTestCNPGCluster("pg-cluster", "default", []metav1.Condition{
		{Type: cnpgReadyConditionType, Status: metav1.ConditionTrue, Reason: "ClusterIsReady", Message: "all good"},
	}, "Cluster in healthy state")
	r, _ := newTestReconciler(t, watch, secret, cluster)

	got, err := r.resolveDSN(context.Background(), watch)
	if err != nil {
		t.Fatalf("resolveDSN: %v", err)
	}
	if got != dsn {
		t.Fatalf("expected DSN %q, got %q", dsn, got)
	}
}

// TestResolveDSN_CNPGClusterRef_ReadyConditionFalse_Fails verifies that an
// explicit Ready=False condition fails DSN resolution, even though the DSN
// Secret itself was perfectly readable — the whole point of this feature.
func TestResolveDSN_CNPGClusterRef_ReadyConditionFalse_Fails(t *testing.T) {
	watch := samplePostgresWatch("watch-cnpg-not-ready", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.CNPGClusterRef = "pg-cluster"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte("******127.0.0.1:5432/app")},
	}
	cluster := newTestCNPGCluster("pg-cluster", "default", []metav1.Condition{
		{Type: cnpgReadyConditionType, Status: metav1.ConditionFalse, Reason: "FailoverInProgress", Message: "primary is being promoted"},
	}, "Failing over")
	r, _ := newTestReconciler(t, watch, secret, cluster)

	_, err := r.resolveDSN(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when the cnpg cluster's Ready condition is False")
	}
	if !strings.Contains(err.Error(), "FailoverInProgress") {
		t.Fatalf("expected the error to surface the condition's reason, got: %v", err)
	}
}

// TestResolveDSN_CNPGClusterRef_Missing_Fails verifies that a
// cnpgClusterRef naming a Cluster that doesn't exist fails DSN resolution
// (rather than, say, being silently ignored).
func TestResolveDSN_CNPGClusterRef_Missing_Fails(t *testing.T) {
	watch := samplePostgresWatch("watch-cnpg-missing", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.CNPGClusterRef = "does-not-exist"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte("******127.0.0.1:5432/app")},
	}
	r, _ := newTestReconciler(t, watch, secret)

	_, err := r.resolveDSN(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when the referenced cnpg cluster does not exist")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("expected the error to name the missing cluster, got: %v", err)
	}
}

// TestResolveDSN_CNPGClusterRef_NoReadyCondition_FallsBackToPhase covers
// older CloudNativePG versions that only ever set status.phase, never a
// Ready condition: a phase not containing "fail" must be treated as
// healthy.
func TestResolveDSN_CNPGClusterRef_NoReadyCondition_FallsBackToPhase(t *testing.T) {
	watch := samplePostgresWatch("watch-cnpg-phase-fallback", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.CNPGClusterRef = "pg-cluster"

	const dsn = "******127.0.0.1:5432/app"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte(dsn)},
	}
	cluster := newTestCNPGCluster("pg-cluster", "default", nil, "Cluster in healthy state")
	r, _ := newTestReconciler(t, watch, secret, cluster)

	got, err := r.resolveDSN(context.Background(), watch)
	if err != nil {
		t.Fatalf("resolveDSN: %v", err)
	}
	if got != dsn {
		t.Fatalf("expected DSN %q, got %q", dsn, got)
	}
}

// TestResolveDSN_CNPGClusterRef_NoReadyCondition_FailedPhase_Fails is the
// negative counterpart: a phase containing "failed" (CloudNativePG's own
// wording for its one terminal failure phase) must fail resolution even
// with no Ready condition present.
func TestResolveDSN_CNPGClusterRef_NoReadyCondition_FailedPhase_Fails(t *testing.T) {
	watch := samplePostgresWatch("watch-cnpg-phase-failed", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.CNPGClusterRef = "pg-cluster"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte("******127.0.0.1:5432/app")},
	}
	cluster := newTestCNPGCluster("pg-cluster", "default", nil, "Cluster in failed state")
	r, _ := newTestReconciler(t, watch, secret, cluster)

	_, err := r.resolveDSN(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when status.phase indicates a failed state")
	}
	if !strings.Contains(err.Error(), "Cluster in failed state") {
		t.Fatalf("expected the error to surface the phase, got: %v", err)
	}
}

// TestResolveDSN_CNPGClusterRef_NoStatusAtAll_Fails covers a Cluster that
// exists but hasn't been reconciled far enough by CloudNativePG to report
// anything yet (empty phase, no conditions) — this must fail, not be
// silently treated as healthy, since "no information" is not the same as
// "known good".
func TestResolveDSN_CNPGClusterRef_NoStatusAtAll_Fails(t *testing.T) {
	watch := samplePostgresWatch("watch-cnpg-no-status", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "dsn-secret", Key: "dsn"}
	watch.Spec.CNPGClusterRef = "pg-cluster"

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dsn-secret",
			Namespace: "default",
			Labels:    map[string]string{secretConsentLabel: secretConsentValue},
		},
		Data: map[string][]byte{"dsn": []byte("******127.0.0.1:5432/app")},
	}
	cluster := newTestCNPGCluster("pg-cluster", "default", nil, "")
	r, _ := newTestReconciler(t, watch, secret, cluster)

	_, err := r.resolveDSN(context.Background(), watch)
	if err == nil {
		t.Fatal("expected an error when the cnpg cluster has no phase or Ready condition yet")
	}
}

// TestAddCNPGToScheme_RegistersRealClusterKind guards against a regression
// where AddCNPGToScheme registered cnpgCluster/cnpgClusterList under a Kind
// derived from their unexported Go type names ("cnpgCluster"/
// "cnpgClusterList") instead of CloudNativePG's real Kind ("Cluster"/
// "ClusterList"). A fake client resolves objects straight from the scheme
// and never notices that mismatch — every other test in this file would
// still pass — but a real cluster's RESTMapper only knows the resource
// under its real Kind, so every remote cnpgClusterRef lookup failed against
// an actual CloudNativePG installation with "no matches for kind
// \"cnpgCluster\" in version \"postgresql.cnpg.io/v1\"" despite the CRD
// being installed and reachable. Caught by a real two-cluster (kind) e2e
// run; this test pins the fix so it can't silently regress.
func TestAddCNPGToScheme_RegistersRealClusterKind(t *testing.T) {
	s := testScheme(t)

	gvks, _, err := s.ObjectKinds(&cnpgCluster{})
	if err != nil {
		t.Fatalf("ObjectKinds(&cnpgCluster{}): %v", err)
	}
	if len(gvks) != 1 || gvks[0].Kind != "Cluster" || gvks[0].Group != "postgresql.cnpg.io" || gvks[0].Version != "v1" {
		t.Fatalf("cnpgCluster registered under unexpected GVK(s) %v; want exactly postgresql.cnpg.io/v1, Kind=Cluster", gvks)
	}

	listGVKs, _, err := s.ObjectKinds(&cnpgClusterList{})
	if err != nil {
		t.Fatalf("ObjectKinds(&cnpgClusterList{}): %v", err)
	}
	if len(listGVKs) != 1 || listGVKs[0].Kind != "ClusterList" {
		t.Fatalf("cnpgClusterList registered under unexpected GVK(s) %v; want Kind=ClusterList", listGVKs)
	}
}
