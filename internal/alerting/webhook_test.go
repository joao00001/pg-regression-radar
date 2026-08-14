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

// Package alerting had no test file at all until this one: it was only ever
// exercised indirectly by internal/e2e's full-pipeline integration test,
// which requires PGRR_TEST_DSN and is skipped by default — so `go test
// ./...` (what CI's build-and-test job and every contributor's default
// workflow actually run) never touched this package's logic. This file
// closes that gap with real, fast, no-external-service-needed unit tests
// against an httptest.Server, covering the behaviour Notify actually
// promises: no-op below Detected, exact payload shape, non-2xx handling,
// network-error handling, and the configured timeout being enforced.
package alerting

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func sampleRegression(status v1alpha1.PerformanceRegressionStatus) v1alpha1.PerformanceRegression {
	return v1alpha1.PerformanceRegression{
		Name:                "probe-app-regression-1",
		Namespace:           "default",
		DeployEventID:       "deploy-123",
		QueryID:             987654321,
		QueryText:           "SELECT * FROM orders WHERE customer_id = $1",
		Status:              status,
		ConfidenceScore:     0.97,
		MeanLatencyBefore:   12.5,
		MeanLatencyAfter:    84.2,
		LatencyChangeFactor: 6.74,
		DetectedChangeAt:    time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
		CreatedAt:           time.Date(2026, 8, 12, 10, 5, 0, 0, time.UTC),
	}
}

// TestNotify_NoOpWhenNotDetected asserts Notify makes no HTTP request at all
// for a non-Detected result: a webhook that fired on every analysis result
// (NoRegression, InsufficientData) would spam on-call for every deploy,
// defeating the entire point of the correlation engine's discrimination.
func TestNotify_NoOpWhenNotDetected(t *testing.T) {
	for _, status := range []v1alpha1.PerformanceRegressionStatus{
		v1alpha1.StatusNoRegression,
		v1alpha1.StatusInsufficientData,
		"",
	} {
		t.Run(string(status), func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "test-cluster", Registerer: prometheus.NewRegistry()}, nil)
			if err := notifier.Notify(context.Background(), sampleRegression(status)); err != nil {
				t.Fatalf("Notify returned an error for a no-op case: %v", err)
			}
			if called {
				t.Errorf("Notify made an HTTP request for Status=%q; it must be a no-op below Detected", status)
			}
		})
	}
}

// TestNotify_SendsCorrectPayload verifies the actual HTTP request Notify
// issues for a Detected regression: method, content type, and that every
// field Notify claims to report actually appears in the JSON body sent —
// not just that some request was made.
func TestNotify_SendsCorrectPayload(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotBody        slackPayload
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("server: decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "prod-east", Registerer: prometheus.NewRegistry()}, nil)
	reg := sampleRegression(v1alpha1.StatusDetected)
	if err := notifier.Notify(context.Background(), reg); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", gotContentType)
	}
	if !strings.Contains(gotBody.Text, "prod-east") {
		t.Errorf("expected message text to reference the cluster name, got %q", gotBody.Text)
	}
	if len(gotBody.Attachments) != 1 {
		t.Fatalf("expected exactly one attachment, got %d", len(gotBody.Attachments))
	}
	attachment := gotBody.Attachments[0]
	if attachment.Color != "danger" {
		t.Errorf("expected color=danger for a plain detected regression, got %q", attachment.Color)
	}

	fields := make(map[string]string, len(attachment.Fields))
	for _, f := range attachment.Fields {
		fields[f.Title] = f.Value
	}
	wantContains := map[string]string{
		"Deploy Event":    reg.DeployEventID,
		"Query ID":        "987654321",
		"Query (excerpt)": reg.QueryText,
		"Latency Before":  "12.50",
		"Latency After":   "84.20",
		"Change Factor":   "6.74",
		"Confidence":      "97",
		"Change Point":    "2026-08-12T10:00:00Z",
	}
	for title, want := range wantContains {
		got, ok := fields[title]
		if !ok {
			t.Errorf("missing field %q in webhook payload", title)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("field %q: expected value to contain %q, got %q", title, want, got)
		}
	}
}

// TestNotify_ExternalCauseSuspected verifies the payload is visibly
// downgraded from "danger" to "warning" and gains an explanatory field when
// the regression might not be the deploy's fault — an operator paged for a
// real "danger" alert should be able to tell the two cases apart from the
// Slack message alone.
func TestNotify_ExternalCauseSuspected(t *testing.T) {
	var gotBody slackPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "test-cluster", Registerer: prometheus.NewRegistry()}, nil)
	reg := sampleRegression(v1alpha1.StatusDetected)
	reg.ExternalCauseSuspected = true
	if err := notifier.Notify(context.Background(), reg); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if len(gotBody.Attachments) != 1 {
		t.Fatalf("expected exactly one attachment, got %d", len(gotBody.Attachments))
	}
	attachment := gotBody.Attachments[0]
	if attachment.Color != "warning" {
		t.Errorf("expected color=warning when ExternalCauseSuspected, got %q", attachment.Color)
	}

	found := false
	for _, f := range attachment.Fields {
		if strings.Contains(f.Value, "External cause suspected") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a field explaining the external-cause suspicion, found none")
	}
}

// TestNotify_PlanDiffSummary_IncludedWhenPresent verifies a non-empty
// PlanDiffSummary (populated by internal/cli.RunOperator's poll loop when
// --capture-plans is enabled — see internal/planner.Diff) is surfaced as its
// own field in the outgoing Slack payload.
func TestNotify_PlanDiffSummary_IncludedWhenPresent(t *testing.T) {
	var gotBody slackPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "test-cluster", Registerer: prometheus.NewRegistry()}, nil)
	reg := sampleRegression(v1alpha1.StatusDetected)
	reg.PlanDiffSummary = "root plan node changed from Index Scan to Seq Scan; estimated cost increased 4.2x"
	if err := notifier.Notify(context.Background(), reg); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if len(gotBody.Attachments) != 1 {
		t.Fatalf("expected exactly one attachment, got %d", len(gotBody.Attachments))
	}
	found := false
	for _, f := range gotBody.Attachments[0].Fields {
		if f.Title == "Plan Diff" && f.Value == reg.PlanDiffSummary {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'Plan Diff' field containing %q, fields=%+v", reg.PlanDiffSummary, gotBody.Attachments[0].Fields)
	}
}

// TestNotify_PlanDiffSummary_OmittedWhenEmpty verifies the common case (plan
// capture disabled, or simply not yet available) doesn't add a confusing
// empty "Plan Diff" field to every alert.
func TestNotify_PlanDiffSummary_OmittedWhenEmpty(t *testing.T) {
	var gotBody slackPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "test-cluster", Registerer: prometheus.NewRegistry()}, nil)
	if err := notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected)); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	for _, f := range gotBody.Attachments[0].Fields {
		if f.Title == "Plan Diff" {
			t.Errorf("did not expect a 'Plan Diff' field when PlanDiffSummary is empty, got value %q", f.Value)
		}
	}
}

// TestNotify_NonSuccessStatusCode verifies Notify treats any >=300 response
// from the webhook endpoint as a failure rather than silently swallowing it
// — a silently-dropped alert is worse than a loud one, since nobody would
// ever know the regression detector caught something.
func TestNotify_NonSuccessStatusCode(t *testing.T) {
	for _, code := range []int{400, 404, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer server.Close()

			notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, Registerer: prometheus.NewRegistry()}, nil)
			err := notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected))
			if err == nil {
				t.Fatalf("expected an error for status code %d, got nil", code)
			}
		})
	}
}

// TestNotify_DoesNotFollowRedirects verifies Notify treats a 3xx response
// from the configured webhook as the final response (and, per
// TestNotify_NonSuccessStatusCode's >=300 rule, a failure) instead of
// transparently following the redirect target. An initially-validated
// destination that later responds with a redirect to an arbitrary,
// unvalidated location is a classic SSRF bypass — see validateWebhookURL in
// factory.go for the up-front check this complements.
func TestNotify_DoesNotFollowRedirects(t *testing.T) {
	redirectTargetCalled := false
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectTargetCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, Registerer: prometheus.NewRegistry()}, nil)
	err := notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected))
	if err == nil {
		t.Fatal("expected an error for a redirect response, got nil")
	}
	if redirectTargetCalled {
		t.Error("Notify followed the redirect to a second destination; it must not")
	}
}

// TestNotify_NetworkError verifies Notify surfaces a genuine transport
// failure (nothing listening on the target address) as an error rather than
// panicking or hanging.
func TestNotify_NetworkError(t *testing.T) {
	// Bind and immediately close a listener to obtain a port nothing is
	// listening on, instead of guessing a "probably free" port number.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	notifier := NewWebhookNotifier(WebhookConfig{URL: "http://" + addr, Registerer: prometheus.NewRegistry()}, nil)
	err = notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected))
	if err == nil {
		t.Fatal("expected an error when the webhook endpoint is unreachable, got nil")
	}
}

func TestNotify_DrainsResponseBodyBeforeClose(t *testing.T) {
	body := &trackingReadCloser{reader: strings.NewReader("ok")}
	notifier := NewWebhookNotifier(WebhookConfig{URL: "http://example.invalid", Registerer: prometheus.NewRegistry()}, nil)
	notifier.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       body,
				Header:     make(http.Header),
			}, nil
		}),
		Timeout: notifier.client.Timeout,
	}

	if err := notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected)); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if !body.sawEOF {
		t.Fatal("expected Notify to read the response body to EOF before closing it")
	}
	if !body.closed {
		t.Fatal("expected Notify to close the response body")
	}
}

// TestNotify_RespectsConfiguredTimeout verifies WebhookConfig.Timeout is
// actually wired into the HTTP client Notify uses: a handler that sleeps
// past the timeout must cause Notify to return (with an error) close to the
// configured duration, not hang until the test framework's own deadline (or
// forever, in production, blocking whatever goroutine is delivering alerts).
func TestNotify_RespectsConfiguredTimeout(t *testing.T) {
	// handlerDelay only needs to be comfortably longer than timeout below;
	// keeping both short keeps this test fast (httptest.Server.Close waits
	// for the in-flight handler to finish, so the delay is on the critical
	// path of the test regardless of the client having already timed out).
	const (
		timeout      = 50 * time.Millisecond
		handlerDelay = 300 * time.Millisecond
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(handlerDelay)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, Timeout: timeout, Registerer: prometheus.NewRegistry()}, nil)

	start := time.Now()
	err := notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	// Generous upper bound to avoid flaking on a loaded CI runner while still
	// catching a timeout that isn't wired up at all (which would take
	// handlerDelay, not timeout, if Timeout were silently ignored).
	if elapsed > handlerDelay {
		t.Errorf("Notify took %s to fail; configured Timeout=%s doesn't appear to be enforced", elapsed, timeout)
	}
}

// TestNewWebhookNotifier_DefaultsTimeout verifies the documented default
// (10s) is actually applied when WebhookConfig.Timeout is left zero, and
// that an explicit value is left untouched. This is an internal-package test
// (not alerting_test) specifically so it can inspect the unexported
// client field instead of inferring the timeout indirectly from a slow test.
func TestNewWebhookNotifier_DefaultsTimeout(t *testing.T) {
	n := NewWebhookNotifier(WebhookConfig{URL: "http://example.invalid", Registerer: prometheus.NewRegistry()}, nil)
	if n.client.Timeout != 10*time.Second {
		t.Errorf("expected default Timeout=10s, got %s", n.client.Timeout)
	}

	n2 := NewWebhookNotifier(WebhookConfig{URL: "http://example.invalid", Timeout: 3 * time.Second, Registerer: prometheus.NewRegistry()}, nil)
	if n2.client.Timeout != 3*time.Second {
		t.Errorf("expected explicit Timeout=3s to be preserved, got %s", n2.client.Timeout)
	}
}

// TestNewWebhookNotifier_NilLoggerDoesNotPanic verifies passing a nil logger
// (the zero value most call sites would pass without thinking about it)
// falls back to slog.Default() instead of nil-pointer-dereferencing the
// first time Notify logs a successful send.
func TestNewWebhookNotifier_NilLoggerDoesNotPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, Registerer: prometheus.NewRegistry()}, nil)
	if notifier.logger == nil {
		t.Fatal("expected a non-nil default logger")
	}
	if err := notifier.Notify(context.Background(), sampleRegression(v1alpha1.StatusDetected)); err != nil {
		t.Fatalf("Notify: %v", err)
	}
}

func TestNotify_RecordsSuccessMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reg := prometheus.NewRegistry()
	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "prod-east", Registerer: reg}, nil)
	regression := sampleRegression(v1alpha1.StatusDetected)
	regression.TriggerType = v1alpha1.TriggerTypePeriodic

	notifier.ObserveDetectedRegression(regression)
	if err := notifier.Notify(context.Background(), regression); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if got := counterVecValue(t, notifier.notificationsTotal, "success", "slack"); got != 1 {
		t.Fatalf("expected notifications_total{status=success,format=slack}=1, got %v", got)
	}
	if got := counterVecValue(t, notifier.notificationsTotal, "error", "slack"); got != 0 {
		t.Fatalf("expected notifications_total{status=error,format=slack}=0, got %v", got)
	}
	if got := counterVecValue(t, notifier.regressionsDetectedTotal, "periodic", "prod-east"); got != 1 {
		t.Fatalf("expected regressions_detected_total{trigger=periodic,cluster=prod-east}=1, got %v", got)
	}
}

func TestNotify_RecordsErrorMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	reg := prometheus.NewRegistry()
	notifier := NewWebhookNotifier(WebhookConfig{URL: server.URL, ClusterName: "prod-east", Registerer: reg}, nil)
	regression := sampleRegression(v1alpha1.StatusDetected)
	regression.TriggerType = v1alpha1.TriggerTypeDeploy

	notifier.ObserveDetectedRegression(regression)
	err := notifier.Notify(context.Background(), regression)
	if err == nil {
		t.Fatal("expected Notify to fail for a non-2xx response")
	}

	if got := counterVecValue(t, notifier.notificationsTotal, "error", "slack"); got != 1 {
		t.Fatalf("expected notifications_total{status=error,format=slack}=1, got %v", got)
	}
	if got := counterVecValue(t, notifier.notificationsTotal, "success", "slack"); got != 0 {
		t.Fatalf("expected notifications_total{status=success,format=slack}=0, got %v", got)
	}
	if got := counterVecValue(t, notifier.regressionsDetectedTotal, "deploy", "prod-east"); got != 1 {
		t.Fatalf("expected regressions_detected_total{trigger=deploy,cluster=prod-east}=1, got %v", got)
	}
}

func counterVecValue(t *testing.T, cv *prometheus.CounterVec, labelValues ...string) float64 {
	t.Helper()

	metric, err := cv.GetMetricWithLabelValues(labelValues...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%v): %v", labelValues, err)
	}
	m := &dto.Metric{}
	if err := metric.Write(m); err != nil {
		t.Fatalf("Write(%v): %v", labelValues, err)
	}
	return m.GetCounter().GetValue()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type trackingReadCloser struct {
	reader io.Reader
	closed bool
	sawEOF bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if err == io.EOF {
		t.sawEOF = true
	}
	return n, err
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}
