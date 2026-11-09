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

package otlpexport

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/joao00001/pg-regression-radar/internal/telemetry/otlpdeploy"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

var (
	traceID = mustHex("0102030405060708090a0b0c0d0e0f10")
	spanID  = mustHex("a1a2a3a4a5a6a7a8")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func sampleRegression() v1alpha1.PerformanceRegression {
	return v1alpha1.PerformanceRegression{
		Name: "reg-1", Namespace: "shop", DeployEventID: otlpdeploy.EventID(traceID, spanID),
		QueryID: 7_000_000_000_000_001, QueryText: "SELECT * FROM orders WHERE id = $1",
		Status: v1alpha1.StatusDetected, ConfidenceScore: 0.97,
		MeanLatencyBefore: 1.5, MeanLatencyAfter: 6, LatencyChangeFactor: 4,
		ExternalCauseSuspected: true, TriggerType: v1alpha1.TriggerTypeDeploy,
		DetectedChangeAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		PlanDiffSummary:  "Seq Scan replaced Index Scan",
	}
}

// ----- test-only protobuf reader for ExportLogsServiceRequest -----

type decodedLog struct {
	timeNanos, observed uint64
	severity            uint64
	severityText, name  string
	body                string
	attrs               map[string]any
	resource            map[string]any
	traceID, spanID     []byte
	scopeName           string
}

func walk(t *testing.T, b []byte, fn func(num protowire.Number, typ protowire.Type, v []byte, scalar uint64)) {
	t.Helper()
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			t.Fatalf("bad tag: %v", protowire.ParseError(n))
		}
		b = b[n:]
		switch typ {
		case protowire.BytesType:
			v, m := protowire.ConsumeBytes(b)
			if m < 0 {
				t.Fatal("bad bytes")
			}
			fn(num, typ, v, 0)
			b = b[m:]
		case protowire.VarintType:
			v, m := protowire.ConsumeVarint(b)
			if m < 0 {
				t.Fatal("bad varint")
			}
			fn(num, typ, nil, v)
			b = b[m:]
		case protowire.Fixed64Type:
			v, m := protowire.ConsumeFixed64(b)
			if m < 0 {
				t.Fatal("bad fixed64")
			}
			fn(num, typ, nil, v)
			b = b[m:]
		default:
			t.Fatalf("unexpected wire type %v", typ)
		}
	}
}

func readAny(t *testing.T, b []byte) any {
	var out any
	walk(t, b, func(num protowire.Number, _ protowire.Type, v []byte, s uint64) {
		switch num {
		case 1:
			out = string(v)
		case 2:
			out = s != 0
		case 3:
			out = int64(s)
		case 4:
			out = math.Float64frombits(s)
		}
	})
	return out
}

func readAttrs(t *testing.T, into map[string]any, b []byte) {
	var k string
	var v any
	walk(t, b, func(num protowire.Number, _ protowire.Type, p []byte, _ uint64) {
		switch num {
		case 1:
			k = string(p)
		case 2:
			v = readAny(t, p)
		}
	})
	into[k] = v
}

func decodeProtoLogs(t *testing.T, b []byte) decodedLog {
	t.Helper()
	d := decodedLog{attrs: map[string]any{}, resource: map[string]any{}}
	walk(t, b, func(_ protowire.Number, _ protowire.Type, rl []byte, _ uint64) {
		walk(t, rl, func(num protowire.Number, _ protowire.Type, v []byte, _ uint64) {
			switch num {
			case 1: // resource
				walk(t, v, func(n protowire.Number, _ protowire.Type, kv []byte, _ uint64) {
					if n == 1 {
						readAttrs(t, d.resource, kv)
					}
				})
			case 2: // scope_logs
				walk(t, v, func(n protowire.Number, _ protowire.Type, sv []byte, _ uint64) {
					switch n {
					case 1:
						walk(t, sv, func(sn protowire.Number, _ protowire.Type, x []byte, _ uint64) {
							if sn == 1 {
								d.scopeName = string(x)
							}
						})
					case 2:
						walk(t, sv, func(ln protowire.Number, _ protowire.Type, x []byte, s uint64) {
							switch ln {
							case 1:
								d.timeNanos = s
							case 2:
								d.severity = s
							case 3:
								d.severityText = string(x)
							case 5:
								d.body, _ = readAny(t, x).(string)
							case 6:
								readAttrs(t, d.attrs, x)
							case 9:
								d.traceID = x
							case 10:
								d.spanID = x
							case 11:
								d.observed = s
							case 12:
								d.name = string(x)
							}
						})
					}
				})
			}
		})
	})
	return d
}

func TestEncodeProtobuf(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC)
	rec := buildRecord(sampleRegression(), now)
	resource := []attr{{"service.name", "pg-regression-radar"}, {"k8s.cluster.name", "prod"}}
	d := decodeProtoLogs(t, encodeProtobuf(resource, "1.2.3", rec))

	if d.name != EventName || d.severity != 13 || d.severityText != "WARN" || d.scopeName != scopeName {
		t.Errorf("header fields: %+v", d)
	}
	if d.timeNanos != uint64(sampleRegression().DetectedChangeAt.UnixNano()) || d.observed != uint64(now.UnixNano()) {
		t.Errorf("timestamps: %d %d", d.timeNanos, d.observed)
	}
	if hex.EncodeToString(d.traceID) != hex.EncodeToString(traceID) || hex.EncodeToString(d.spanID) != hex.EncodeToString(spanID) {
		t.Errorf("trace link: %x %x", d.traceID, d.spanID)
	}
	if !strings.Contains(d.body, "4.00x") || !strings.Contains(d.body, "deploy otlp-") {
		t.Errorf("body = %q", d.body)
	}
	want := map[string]any{
		"db.system.name":                               "postgresql",
		"pg_regression_radar.regression.name":          "reg-1",
		"pg_regression_radar.regression.status":        "Detected",
		"pg_regression_radar.regression.trigger_type":  "deploy",
		"pg_regression_radar.query_id":                 int64(7_000_000_000_000_001),
		"pg_regression_radar.confidence":               0.97,
		"pg_regression_radar.latency.before_ms":        1.5,
		"pg_regression_radar.latency.after_ms":         6.0,
		"pg_regression_radar.latency.change_factor":    4.0,
		"pg_regression_radar.external_cause_suspected": true,
		"pg_regression_radar.auto_abort.triggered":     false,
		"db.query.text":                                "SELECT * FROM orders WHERE id = $1",
		"pg_regression_radar.plan_diff_summary":        "Seq Scan replaced Index Scan",
	}
	for k, v := range want {
		if d.attrs[k] != v {
			t.Errorf("attr %s = %#v, want %#v", k, d.attrs[k], v)
		}
	}
	if d.resource["service.name"] != "pg-regression-radar" || d.resource["k8s.cluster.name"] != "prod" {
		t.Errorf("resource = %v", d.resource)
	}
}

func TestEncodeJSON_MatchesProtobufSemantics(t *testing.T) {
	t.Parallel()
	rec := buildRecord(sampleRegression(), time.Now())
	b, err := encodeJSON([]attr{{"service.name", "pg-regression-radar"}}, "1.2.3", rec)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ResourceLogs []struct {
			ScopeLogs []struct {
				LogRecords []struct {
					TimeUnixNano   string `json:"timeUnixNano"`
					SeverityNumber int    `json:"severityNumber"`
					EventName      string `json:"eventName"`
					TraceID        string `json:"traceId"`
					SpanID         string `json:"spanId"`
					Attributes     []struct {
						Key   string         `json:"key"`
						Value map[string]any `json:"value"`
					} `json:"attributes"`
				} `json:"logRecords"`
			} `json:"scopeLogs"`
		} `json:"resourceLogs"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	lr := got.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	if lr.EventName != EventName || lr.SeverityNumber != 13 || lr.TraceID != hex.EncodeToString(traceID) || lr.SpanID != hex.EncodeToString(spanID) {
		t.Errorf("record = %+v", lr)
	}
	if lr.TimeUnixNano != "1777636800000000000" {
		t.Errorf("timeUnixNano = %s", lr.TimeUnixNano)
	}
	for _, a := range lr.Attributes {
		if a.Key == "pg_regression_radar.query_id" && a.Value["intValue"] != "7000000000000001" {
			t.Errorf("query id must be a decimal string: %v", a.Value)
		}
	}
}

func TestBuildRecord_PeriodicAndNonOTLPDeploy(t *testing.T) {
	t.Parallel()
	res := sampleRegression()
	res.TriggerType, res.DeployEventID, res.QueryText = v1alpha1.TriggerTypePeriodic, "", ""
	rec := buildRecord(res, time.Now())
	if rec.traceID != nil || strings.Contains(rec.body, "after deploy") {
		t.Errorf("periodic record should not reference a deploy: %+v", rec)
	}
	for _, a := range rec.attrs {
		if a.key == "db.query.text" || a.key == "pg_regression_radar.deploy_event_id" {
			t.Errorf("unexpected attr %s for periodic record without text/deploy", a.key)
		}
	}

	res = sampleRegression()
	res.DeployEventID = "argocd-app-abc"
	if rec := buildRecord(res, time.Now()); rec.traceID != nil {
		t.Error("only OTLP-sourced deploy IDs may produce a trace link")
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	t.Parallel()
	ok := map[string]string{
		"http://c:4318":               "http://c:4318/v1/logs",
		"http://c:4318/":              "http://c:4318/v1/logs",
		"https://c/otel":              "https://c/otel/v1/logs",
		"https://c/otel/v1/logs":      "https://c/otel/v1/logs",
		"  https://c:4318?x=1#frag  ": "https://c:4318/v1/logs?x=1",
	}
	for in, want := range ok {
		if got, err := NormalizeEndpoint(in); err != nil || got != want {
			t.Errorf("NormalizeEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "collector:4318", "ftp://c", "http://", "http://user:pw@c:4318", "://x"} {
		if _, err := NormalizeEndpoint(bad); err == nil {
			t.Errorf("NormalizeEndpoint(%q) should fail", bad)
		}
	}
}

func TestParseHeaders(t *testing.T) {
	t.Parallel()
	got, err := ParseHeaders("Authorization=Bearer%20tok, x-team=a=b\nx-other=1")
	if err != nil {
		t.Fatal(err)
	}
	if got["Authorization"] != "Bearer tok" || got["x-team"] != "a=b" || got["x-other"] != "1" || len(got) != 3 {
		t.Errorf("headers = %v", got)
	}
	for _, bad := range []string{"novalue", "=v", "bad key=v"} {
		if _, err := ParseHeaders(bad); err == nil {
			t.Errorf("ParseHeaders(%q) should fail", bad)
		}
	}
}

// ----- delivery -----

type captured struct {
	mu   sync.Mutex
	reqs []*http.Request
	body [][]byte
}

func (c *captured) add(r *http.Request, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, r)
	c.body = append(c.body, b)
}

func (c *captured) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestExporter(t *testing.T, srvURL string, mut func(*Config)) (*Exporter, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	cfg := Config{Endpoint: srvURL, BaseBackoff: time.Millisecond, Registerer: reg, ServiceVersion: "test",
		Headers: map[string]string{"Authorization": "Bearer t"}}
	if mut != nil {
		mut(&cfg)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e, reg
}

func result(e *Exporter, r string) float64 {
	var m dto.Metric
	if err := e.counter.WithLabelValues(r).Write(&m); err != nil {
		panic(err)
	}
	return m.GetCounter().GetValue()
}

func TestExporter_DeliversProtobufWithHeadersAndGzip(t *testing.T) {
	t.Parallel()
	var cap captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.add(r, b)
	}))
	defer srv.Close()

	e, _ := newTestExporter(t, srv.URL, func(c *Config) {
		c.Gzip = true
		c.ResourceAttributes = map[string]string{"k8s.namespace.name": "shop"}
	})
	if !e.Export(sampleRegression()) {
		t.Fatal("Export reported drop")
	}
	waitFor(t, func() bool { return result(e, "success") == 1 })

	r := cap.reqs[0]
	if r.URL.Path != "/v1/logs" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-protobuf" ||
		r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Authorization") != "Bearer t" {
		t.Errorf("request = %s %s %v", r.Method, r.URL.Path, r.Header)
	}
	zr, err := gzip.NewReader(strings.NewReader(string(cap.body[0])))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	d := decodeProtoLogs(t, raw)
	if d.name != EventName || d.resource["k8s.namespace.name"] != "shop" || d.resource["service.version"] != "test" {
		t.Errorf("decoded = %+v", d)
	}
}

func TestExporter_JSONEncoding(t *testing.T) {
	t.Parallel()
	var cap captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.add(r, b)
	}))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, func(c *Config) { c.Encoding = EncodingJSON })
	e.Export(sampleRegression())
	waitFor(t, func() bool { return cap.count() == 1 })
	if cap.reqs[0].Header.Get("Content-Type") != "application/json" || !json.Valid(cap.body[0]) {
		t.Errorf("json export: ct=%s valid=%v", cap.reqs[0].Header.Get("Content-Type"), json.Valid(cap.body[0]))
	}
}

func TestExporter_RetriesTransientThenSucceeds(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, nil)
	e.Export(sampleRegression())
	waitFor(t, func() bool { return result(e, "success") == 1 })
	if calls.Load() != 3 || result(e, "failed") != 0 {
		t.Errorf("calls=%d failed=%v", calls.Load(), result(e, "failed"))
	}
}

func TestExporter_PermanentErrorsAreNotRetried(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, nil)
	e.Export(sampleRegression())
	waitFor(t, func() bool { return result(e, "failed") == 1 })
	if calls.Load() != 1 {
		t.Errorf("400 must not be retried, calls=%d", calls.Load())
	}
}

func TestExporter_GivesUpAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, func(c *Config) { c.MaxAttempts = 3 })
	e.Export(sampleRegression())
	waitFor(t, func() bool { return result(e, "failed") == 1 })
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", calls.Load())
	}
}

func TestExporter_DoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, nil)
	e.Export(sampleRegression())
	waitFor(t, func() bool { return result(e, "failed") == 1 })
	if hit.Load() {
		t.Error("redirect target was contacted")
	}
}

func TestExporter_QueueFullDropsWithoutBlocking(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, func(c *Config) { c.QueueSize = 1; c.Timeout = 5 * time.Second })

	e.Export(sampleRegression())
	waitFor(t, func() bool { return len(e.queue) == 0 }) // worker holds #1 in flight
	if !e.Export(sampleRegression()) {
		t.Fatal("second record should fit in the queue")
	}
	start := time.Now()
	if e.Export(sampleRegression()) {
		t.Fatal("third record should be dropped")
	}
	if time.Since(start) > time.Second {
		t.Error("Export blocked on a full queue")
	}
	if result(e, "dropped") != 1 {
		t.Errorf("dropped = %v", result(e, "dropped"))
	}
	close(release)
}

func TestExporter_CloseFlushesAndRejectsLater(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, nil)
	for i := 0; i < 5; i++ {
		e.Export(sampleRegression())
	}
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Errorf("flush delivered %d of 5", calls.Load())
	}
	if e.Export(sampleRegression()) || result(e, "dropped") != 1 {
		t.Error("Export after Close must drop and count")
	}
	if err := e.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestExporter_CloseDeadlineAbortsRetries(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	e, _ := newTestExporter(t, srv.URL, nil)
	e.Export(sampleRegression())
	waitFor(t, func() bool { return true })
	time.Sleep(50 * time.Millisecond) // let attempt 1 fail and the worker start its backoff

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := e.Close(ctx); err == nil {
		t.Error("Close should report the expired deadline")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("Close did not abort the retry backoff")
	}
	if result(e, "failed") != 1 {
		t.Errorf("failed = %v", result(e, "failed"))
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if d := parseRetryAfter("7", now); d != 7*time.Second {
		t.Errorf("seconds: %v", d)
	}
	if d := parseRetryAfter(now.Add(9*time.Second).Format(http.TimeFormat), now); d != 9*time.Second {
		t.Errorf("http-date: %v", d)
	}
	for _, v := range []string{"", "soon", "-3", now.Add(-time.Hour).Format(http.TimeFormat)} {
		if d := parseRetryAfter(v, now); d != 0 {
			t.Errorf("parseRetryAfter(%q) = %v", v, d)
		}
	}
}

func TestNew_Validation(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{Endpoint: ""}); err == nil {
		t.Error("empty endpoint")
	}
	if _, err := New(Config{Endpoint: "http://c", Encoding: "xml"}); err == nil {
		t.Error("bad encoding")
	}
	reg := prometheus.NewRegistry()
	e, err := New(Config{Endpoint: "http://127.0.0.1:1", Registerer: reg})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Close(context.Background()) }()
	if _, err := New(Config{Endpoint: "http://c", Registerer: reg}); err == nil {
		t.Error("double registration on the same registry should fail, not panic")
	}
}
