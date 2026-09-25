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
	"net/http/httptest"
	"strings"
	"testing"

	ctrl "sigs.k8s.io/controller-runtime"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

// fakePrometheusServer stands in for a real Prometheus HTTP API, returning
// an always-empty (but well-formed) response — enough for startWatch to
// successfully build a promsource.Source and for the reconcile itself to
// reach phase Running; the sample content itself is exercised by
// internal/telemetry/promsource's own tests, not duplicated here.
func fakePrometheusServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	return srv
}

func prometheusSampleSourceWatch(name, namespace, prometheusURL string) *radarv1alpha1.PostgresWatch {
	watch := samplePostgresWatch(name, namespace)
	// No DSN/DSNSecretRef needed for this sample source — see
	// usesPrometheusSampleSource's doc comment on why Reconcile skips
	// resolveDSN entirely in this mode.
	watch.Spec.DSN = ""
	watch.Spec.SampleSource = &radarv1alpha1.SampleSourceConfig{
		Type: "prometheus",
		Prometheus: &radarv1alpha1.PrometheusSampleSourceConfig{
			URL:        prometheusURL,
			MetricName: "pg_regression_radar_query_mean_exec_time_ms",
		},
	}
	return watch
}

// TestReconcile_PrometheusSampleSource_StartsWithoutCollector verifies the
// core Phase 1b wiring: a watch that opts into sampleSource.type=prometheus
// starts successfully with no DSN at all, ends up with a nil rt.Collector,
// and still gets a working rt.SampleSource/rt.Engine pair.
func TestReconcile_PrometheusSampleSource_StartsWithoutCollector(t *testing.T) {
	srv := fakePrometheusServer(t)
	watch := prometheusSampleSourceWatch("watch-prom", "default", srv.URL)
	r, c := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-prom", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered")
	}
	defer rt.Cancel()

	if rt.Collector != nil {
		t.Fatal("expected rt.Collector to be nil when sampleSource.type=prometheus")
	}
	if rt.SampleSource == nil {
		t.Fatal("expected rt.SampleSource to be set regardless of which implementation backs it")
	}
	if rt.Engine == nil || rt.Notifier == nil {
		t.Fatal("expected WatchRuntime to still have an Engine and Notifier")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseRunning {
		t.Fatalf("expected phase Running, got %q (message=%q)", got.Status.Phase, got.Status.Message)
	}
}

// TestReconcile_PrometheusSampleSource_CapturePlansRejected verifies the
// invariant WatchRuntime.CapturePlans's doc comment describes: capturePlans
// requires a direct database connection that sampleSource.type=prometheus
// deliberately doesn't have, so reconciliation must fail with a clear
// message rather than silently ignoring capturePlans or panicking later on
// a nil rt.Collector.
func TestReconcile_PrometheusSampleSource_CapturePlansRejected(t *testing.T) {
	srv := fakePrometheusServer(t)
	watch := prometheusSampleSourceWatch("watch-prom-plans", "default", srv.URL)
	watch.Spec.CapturePlans = true
	r, c := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-prom-plans", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected an error when capturePlans is combined with sampleSource.type=prometheus")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected phase Failed, got %q", got.Status.Phase)
	}
	if !strings.Contains(got.Status.Message, "capturePlans") {
		t.Fatalf("expected status message to explain the capturePlans conflict, got %q", got.Status.Message)
	}
	if _, ok := r.Registry.Get(req.NamespacedName); ok {
		t.Fatal("expected no worker to be registered when startWatch rejects the spec")
	}
}

// TestReconcile_PrometheusSampleSource_MissingPrometheusConfigRejected
// verifies startWatch's nil-guard on spec.sampleSource.prometheus: the CRD
// schema alone can't express "prometheus is required when type is
// prometheus" as a cross-field constraint, so startWatch must catch it
// itself rather than nil-pointer-dereferencing on promCfg.URL.
func TestReconcile_PrometheusSampleSource_MissingPrometheusConfigRejected(t *testing.T) {
	watch := samplePostgresWatch("watch-prom-noconfig", "default")
	watch.Spec.DSN = ""
	watch.Spec.SampleSource = &radarv1alpha1.SampleSourceConfig{Type: "prometheus"}
	r, c := newTestReconciler(t, watch)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-prom-noconfig", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("expected an error when sampleSource.prometheus is unset")
	}

	var got radarv1alpha1.PostgresWatch
	if err := c.Get(context.Background(), req.NamespacedName, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != radarv1alpha1.PostgresWatchPhaseFailed {
		t.Fatalf("expected phase Failed, got %q", got.Status.Phase)
	}
}

// TestReconcile_PrometheusSampleSource_NoDSNRequired verifies that a
// prometheus-sourced watch with neither spec.dsn nor spec.dsnSecretRef set
// does not fail the way a "collector"-sourced watch would (see
// resolveDSN's "neither spec.dsn nor spec.dsnSecretRef is set" error) --
// this is the whole point of skipping resolveDSN for this sample source.
func TestReconcile_PrometheusSampleSource_NoDSNRequired(t *testing.T) {
	srv := fakePrometheusServer(t)
	watch := prometheusSampleSourceWatch("watch-prom-nodsn", "default", srv.URL)
	// prometheusSampleSourceWatch already clears DSN; be explicit that
	// DSNSecretRef is unset too, since that's the exact combination
	// resolveDSN would otherwise reject.
	watch.Spec.DSNSecretRef = nil

	r, c := newTestReconciler(t, watch)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "watch-prom-nodsn", Namespace: "default"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	rt, ok := r.Registry.Get(req.NamespacedName)
	if !ok {
		t.Fatal("expected a WatchRuntime to be registered")
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

// TestUsesPrometheusSampleSource covers usesPrometheusSampleSource's own
// truth table directly, since several tests above depend on it choosing
// correctly.
func TestUsesPrometheusSampleSource(t *testing.T) {
	tests := []struct {
		name string
		spec radarv1alpha1.PostgresWatchSpec
		want bool
	}{
		{"nil sampleSource", radarv1alpha1.PostgresWatchSpec{}, false},
		{
			"type unset (defaults to collector)",
			radarv1alpha1.PostgresWatchSpec{SampleSource: &radarv1alpha1.SampleSourceConfig{}},
			false,
		},
		{
			"type collector explicitly",
			radarv1alpha1.PostgresWatchSpec{SampleSource: &radarv1alpha1.SampleSourceConfig{Type: "collector"}},
			false,
		},
		{
			"type prometheus",
			radarv1alpha1.PostgresWatchSpec{SampleSource: &radarv1alpha1.SampleSourceConfig{Type: "prometheus"}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usesPrometheusSampleSource(tt.spec); got != tt.want {
				t.Errorf("usesPrometheusSampleSource(%+v) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}
