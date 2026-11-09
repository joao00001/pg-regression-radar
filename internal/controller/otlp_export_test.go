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
	"sigs.k8s.io/controller-runtime/pkg/client"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/alerting"
)

func otlpExportWatch(name, endpoint string) *radarv1alpha1.PostgresWatch {
	w := samplePostgresWatch(name, "default")
	w.Spec.OTLPExport = &radarv1alpha1.OTLPExportConfig{Endpoint: endpoint}
	return w
}

func headersSecret(consent bool, data string) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "otlp-headers", Namespace: "default"},
		Data:       map[string][]byte{"headers": []byte(data)},
	}
	if consent {
		s.Labels = map[string]string{secretConsentLabel: secretConsentValue}
	}
	return s
}

func reconcileWatch(t *testing.T, r *PostgresWatchReconciler, name string) (ctrl.Request, error) {
	t.Helper()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: "default"}}
	_, err := r.Reconcile(context.Background(), req)
	return req, err
}

func getWatch(t *testing.T, r *PostgresWatchReconciler, req ctrl.Request) radarv1alpha1.PostgresWatch {
	t.Helper()
	var got radarv1alpha1.PostgresWatch
	if err := r.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	return got
}

func TestReconcile_OTLPExport_BuildsExporter(t *testing.T) {
	watch := otlpExportWatch("w-otlp", "https://collector.example.com:4318")
	watch.Spec.OTLPExport.HeadersSecretRef = &radarv1alpha1.SecretKeySelector{Name: "otlp-headers", Key: "headers"}
	r, _ := newTestReconciler(t, watch, headersSecret(true, "authorization=Bearer abc"))

	req, err := reconcileWatch(t, r, "w-otlp")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime")
	}
	defer rt.Cancel()
	if rt.OTLPExporter == nil {
		t.Fatal("expected an OTLP exporter")
	}
	if got, want := rt.OTLPExporter.URL(), "https://collector.example.com:4318/v1/logs"; got != want {
		t.Fatalf("exporter URL = %q, want %q", got, want)
	}
}

func TestReconcile_NoOTLPExport_NoExporter(t *testing.T) {
	r, _ := newTestReconciler(t, samplePostgresWatch("w-plain", "default"))
	req, err := reconcileWatch(t, r, "w-plain")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	rt, _ := r.Registry.Get(req.NamespacedName)
	defer rt.Cancel()
	if rt.OTLPExporter != nil {
		t.Fatal("exporter must be nil without spec.otlpExport")
	}
}

func TestReconcile_OTLPExport_Rejections(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		mutate   func(*PostgresWatchReconciler, *radarv1alpha1.PostgresWatch)
		objs     []*corev1.Secret
		wantMsg  string
	}{
		{name: "loopback", endpoint: "http://127.0.0.1:4318", wantMsg: "rejected"},
		{name: "metadata host", endpoint: "http://metadata.google.internal/", wantMsg: "rejected"},
		{name: "bad scheme", endpoint: "ftp://collector.example.com", wantMsg: "endpoint"},
		{name: "userinfo", endpoint: "https://user:pw@collector.example.com", wantMsg: "endpoint"},
		{
			name:     "not on allowlist",
			endpoint: "https://collector.example.com",
			mutate: func(r *PostgresWatchReconciler, _ *radarv1alpha1.PostgresWatch) {
				r.AllowedAlertDestinations = []string{"other.example.com"}
			},
			wantMsg: "allowlist",
		},
		{
			name:     "relay only",
			endpoint: "https://collector.example.com",
			mutate: func(r *PostgresWatchReconciler, _ *radarv1alpha1.PostgresWatch) {
				r.AlertingDestinationPolicy = alerting.DestinationPolicyRelayOnly
			},
			wantMsg: "relay",
		},
		{
			name:     "headers secret without consent",
			endpoint: "https://collector.example.com",
			mutate: func(_ *PostgresWatchReconciler, w *radarv1alpha1.PostgresWatch) {
				w.Spec.OTLPExport.HeadersSecretRef = &radarv1alpha1.SecretKeySelector{Name: "otlp-headers", Key: "headers"}
			},
			objs:    []*corev1.Secret{headersSecret(false, "a=b")},
			wantMsg: secretConsentLabel,
		},
		{
			name:     "headers secret missing key",
			endpoint: "https://collector.example.com",
			mutate: func(_ *PostgresWatchReconciler, w *radarv1alpha1.PostgresWatch) {
				w.Spec.OTLPExport.HeadersSecretRef = &radarv1alpha1.SecretKeySelector{Name: "otlp-headers", Key: "nope"}
			},
			objs:    []*corev1.Secret{headersSecret(true, "a=b")},
			wantMsg: `no key "nope"`,
		},
		{
			name:     "headers secret malformed",
			endpoint: "https://collector.example.com",
			mutate: func(_ *PostgresWatchReconciler, w *radarv1alpha1.PostgresWatch) {
				w.Spec.OTLPExport.HeadersSecretRef = &radarv1alpha1.SecretKeySelector{Name: "otlp-headers", Key: "headers"}
			},
			objs:    []*corev1.Secret{headersSecret(true, "no-equals-sign")},
			wantMsg: "headersSecretRef",
		},
		{
			name:     "headers secret absent",
			endpoint: "https://collector.example.com",
			mutate: func(_ *PostgresWatchReconciler, w *radarv1alpha1.PostgresWatch) {
				w.Spec.OTLPExport.HeadersSecretRef = &radarv1alpha1.SecretKeySelector{Name: "otlp-headers", Key: "headers"}
			},
			wantMsg: "otlp-headers",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			watch := otlpExportWatch("w-bad", tc.endpoint)
			objs := []client.Object{watch}
			for _, s := range tc.objs {
				objs = append(objs, s)
			}
			r, _ := newTestReconciler(t, objs...)
			if tc.mutate != nil {
				tc.mutate(r, watch)
				if err := r.Update(context.Background(), watch); err != nil {
					t.Fatalf("update: %v", err)
				}
			}
			req, err := reconcileWatch(t, r, "w-bad")
			if err == nil {
				t.Fatal("expected reconcile to fail")
			}
			got := getWatch(t, r, req)
			if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
				t.Fatalf("phase = %q, want Failed", got.Status.Phase)
			}
			if !strings.Contains(got.Status.Message, tc.wantMsg) {
				t.Fatalf("message %q does not contain %q", got.Status.Message, tc.wantMsg)
			}
			if _, ok := r.Registry.Get(req.NamespacedName); ok {
				t.Fatal("no worker may be registered for a rejected spec")
			}
		})
	}
}

func TestReconcile_PrometheusBearerToken(t *testing.T) {
	srv := fakePrometheusServer(t)
	secretRef := &radarv1alpha1.SecretKeySelector{Name: "prom-token", Key: "token"}
	mk := func(consent bool, value string) *corev1.Secret {
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "prom-token", Namespace: "default"},
			Data:       map[string][]byte{"token": []byte(value)},
		}
		if consent {
			s.Labels = map[string]string{secretConsentLabel: secretConsentValue}
		}
		return s
	}

	t.Run("consented secret starts and rotation restarts", func(t *testing.T) {
		watch := prometheusSampleSourceWatch("w-tok", "default", srv.URL)
		watch.Spec.SampleSource.Prometheus.BearerTokenSecretRef = secretRef
		r, c := newTestReconciler(t, watch, mk(true, "one"))
		req, err := reconcileWatch(t, r, "w-tok")
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		first, _ := r.Registry.Get(req.NamespacedName)
		defer first.Cancel()

		if _, err := reconcileWatch(t, r, "w-tok"); err != nil {
			t.Fatal(err)
		}
		same, _ := r.Registry.Get(req.NamespacedName)
		if same != first {
			t.Fatal("an unchanged token must not restart the worker")
		}

		var s corev1.Secret
		if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "prom-token"}, &s); err != nil {
			t.Fatal(err)
		}
		s.Data["token"] = []byte("two")
		if err := c.Update(context.Background(), &s); err != nil {
			t.Fatal(err)
		}
		if _, err := reconcileWatch(t, r, "w-tok"); err != nil {
			t.Fatal(err)
		}
		second, _ := r.Registry.Get(req.NamespacedName)
		defer second.Cancel()
		if second == first {
			t.Fatal("rotating the token Secret must restart the worker")
		}
		if second.SpecHash == first.SpecHash {
			t.Fatal("spec hash must change when the token changes")
		}
	})

	t.Run("secret without consent label is refused", func(t *testing.T) {
		watch := prometheusSampleSourceWatch("w-tok2", "default", srv.URL)
		watch.Spec.SampleSource.Prometheus.BearerTokenSecretRef = secretRef
		r, _ := newTestReconciler(t, watch, mk(false, "one"))
		req, err := reconcileWatch(t, r, "w-tok2")
		if err == nil {
			t.Fatal("expected failure without the consent label")
		}
		if got := getWatch(t, r, req); got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
			t.Fatalf("phase = %q", got.Status.Phase)
		}
	})
}

func TestHashPostgresWatchSpec_SecretsOnlyAffectHashWhenSet(t *testing.T) {
	spec := samplePostgresWatch("h", "default").Spec
	base := hashPostgresWatchSpec(spec, "dsn")
	if got := hashPostgresWatchSpec(spec, "dsn", "", ""); got != base {
		t.Fatal("empty secrets must keep the pre-existing hash (no restart on upgrade)")
	}
	a := hashPostgresWatchSpec(spec, "dsn", "tok", "")
	b := hashPostgresWatchSpec(spec, "dsn", "", "tok")
	if a == base || b == base || a == b {
		t.Fatal("each secret slot must influence the hash independently")
	}
}
