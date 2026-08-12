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
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

// TestEnvtest_PostgresWatch_RemoteClusterSecretRef_FetchesDSNFromRemoteAPIServer
// is the "fleet" feature's one genuinely end-to-end test: it proves that
// when spec.remoteClusterSecretRef is set, Reconcile really performs two
// distinct network round trips against two distinct API servers — the hub
// client for the kubeconfig Secret, and a client built from that kubeconfig
// (via buildRemoteClient, see remote_client.go) for the DSN Secret — rather
// than accidentally reading both Secrets from the same place.
//
// The rest of this package's remote-cluster coverage
// (postgreswatch_controller_test.go's TestDSNSecretClient_* and
// TestReconcile_RemoteCluster*Failed tests) only exercises the *failure*
// paths (malformed kubeconfig, an address nothing listens on) using a fake
// hub client and a syntactically-valid-but-unreachable kubeconfig — none of
// them ever complete a real Secret fetch, so none would catch a bug like
// dsnSecretClient silently falling back to r.Client instead of the built
// remote client.
//
// Getting a real *second* Kubernetes API server without a real second
// cluster (out of scope for this sandbox — see this repo's task notes) is
// exactly what this test's structure provides for free: the hub side is the
// same in-memory sigs.k8s.io/controller-runtime/pkg/client/fake client this
// package's other tests already use (it holds the PostgresWatch CR and the
// kubeconfig Secret), while the "remote" side is a genuinely separate,
// really running kube-apiserver process from controller-runtime's envtest
// package (the same infrastructure suite_test.go's
// TestEnvtest_PostgresWatchLifecycle already uses to test the hub path) —
// serialized into a real kubeconfig and handed to the reconciler exactly as
// an operator's Secret would. A hub-side bug that reached into the fake
// client for the DSN Secret instead of dialing the envtest server would
// fail this test with "secrets \"remote-dsn\" not found", since that object
// only exists on the envtest side.
//
// Skips automatically (like TestEnvtest_PostgresWatchLifecycle) if
// KUBEBUILDER_ASSETS is unset or the envtest binaries aren't available.
func TestEnvtest_PostgresWatch_RemoteClusterSecretRef_FetchesDSNFromRemoteAPIServer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("skipping envtest: KUBEBUILDER_ASSETS not set; see suite_test.go's TestEnvtest_PostgresWatchLifecycle doc comment for setup instructions")
	}

	s := testScheme(t)

	// ---- "Remote" (spoke) cluster: a real, separately running kube-apiserver ----
	remoteEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		Scheme:                s,
	}
	remoteCfg, err := remoteEnv.Start()
	if err != nil {
		t.Skipf("skipping envtest: could not start remote test environment: %v", err)
	}
	defer func() {
		if err := remoteEnv.Stop(); err != nil {
			t.Logf("warning: remote envtest teardown failed: %v", err)
		}
	}()

	remoteAdmin, err := client.New(remoteCfg, client.Options{Scheme: s})
	if err != nil {
		t.Fatalf("build remote admin client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const remoteDSN = "postgres://remote-user:remote-pass@remote-postgres.example:5432/app?sslmode=disable"
	remoteDSNSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "remote-dsn", Namespace: "default"},
		Data:       map[string][]byte{"dsn": []byte(remoteDSN)},
	}
	if err := remoteAdmin.Create(ctx, remoteDSNSecret); err != nil {
		t.Fatalf("create DSN secret on remote apiserver: %v", err)
	}

	kubeconfig := kubeconfigFromRESTConfig(t, remoteCfg, "remote")

	// ---- Hub cluster: the in-memory fake client, holding only the
	// PostgresWatch CR and the kubeconfig Secret — deliberately NOT the DSN
	// Secret, so a successful reconcile can only mean the DSN really came
	// from the remote apiserver above. ----
	watch := samplePostgresWatch("watch-remote-real", "default")
	watch.Spec.DSN = ""
	watch.Spec.DSNSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-dsn", Key: "dsn"}
	watch.Spec.RemoteClusterSecretRef = &radarv1alpha1.SecretKeySelector{Name: "remote-kubeconfig", Key: "kubeconfig"}

	kubeconfigSec := kubeconfigSecret("remote-kubeconfig", "default", "kubeconfig", string(kubeconfig))

	hub := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&radarv1alpha1.PostgresWatch{}, &radarv1alpha1.PerformanceRegression{}).
		WithObjects(watch, kubeconfigSec).
		Build()

	r := &PostgresWatchReconciler{
		Client:   hub,
		Scheme:   s,
		Registry: NewRegistry(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: watch.Name, Namespace: watch.Namespace}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	defer r.stopWatch(req.NamespacedName)

	var got radarv1alpha1.PostgresWatch
	if err := hub.Get(ctx, req.NamespacedName, &got); err != nil {
		t.Fatalf("get from hub: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseRunning {
		t.Fatalf("expected phase Running (DSN resolved from the real remote apiserver), got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}

	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered")
	}
	if rt.Collector == nil {
		t.Fatal("expected the WatchRuntime to have a Collector built from the remotely-resolved DSN")
	}

	// hashPostgresWatchSpec folds the resolved DSN into SpecHash; recomputing
	// it here with the known-good remoteDSN and comparing against what the
	// running worker actually recorded confirms the exact DSN value that
	// flowed through resolveDSN/startWatch really is the one that lives only
	// on the remote apiserver, not some other value (e.g. an empty string
	// that happened to also produce a "successful" collector.New call).
	wantHash := hashPostgresWatchSpec(watch.Spec, remoteDSN)
	if rt.SpecHash != wantHash {
		t.Fatalf("SpecHash %s does not match the hash computed from the real remote DSN (%s); DSN resolution likely used the wrong value", rt.SpecHash, wantHash)
	}
}

// kubeconfigFromRESTConfig serializes cfg (as returned by
// envtest.Environment.Start(), which authenticates with a client
// certificate rather than a bearer token) into a kubeconfig YAML document —
// the same shape an operator would store in a remoteClusterSecretRef
// Secret's data — that clientcmd.RESTConfigFromKubeConfig (called by this
// package's buildRemoteClient) can parse back into an equivalent,
// independently-dialable *rest.Config.
func kubeconfigFromRESTConfig(t *testing.T, cfg *rest.Config, name string) []byte {
	t.Helper()

	kc := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			name: {
				Server:                   cfg.Host,
				CertificateAuthorityData: cfg.CAData,
				InsecureSkipTLSVerify:    cfg.Insecure,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			name: {Cluster: name, AuthInfo: name},
		},
		CurrentContext: name,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			name: {
				ClientCertificateData: cfg.CertData,
				ClientKeyData:         cfg.KeyData,
				Token:                 cfg.BearerToken,
			},
		},
	}

	raw, err := clientcmd.Write(kc)
	if err != nil {
		t.Fatalf("serialize kubeconfig: %v", err)
	}
	return raw
}
