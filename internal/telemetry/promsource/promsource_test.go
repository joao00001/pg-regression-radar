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

package promsource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// fakePrometheus is a minimal stand-in for a Prometheus HTTP API server:
// it inspects the requested path and query params and returns a scripted
// JSON body, so tests exercise this package's request-building and
// response-parsing without a real Prometheus.
type fakePrometheus struct {
	t *testing.T

	// wantPath, if set, asserts the exact request path.
	wantPath string
	// gotQuery captures the last request's query params for assertions.
	gotQuery url.Values
	// body is the raw JSON body to respond with.
	body string
	// status is the HTTP status to respond with. Defaults to 200.
	status int
}

func (f *fakePrometheus) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if f.wantPath != "" && r.URL.Path != f.wantPath {
			f.t.Errorf("request path = %q, want %q", r.URL.Path, f.wantPath)
		}
		f.gotQuery = r.URL.Query()
		if f.status != 0 {
			w.WriteHeader(f.status)
		}
		_, _ = w.Write([]byte(f.body))
	}
}

func newTestSource(t *testing.T, srv *httptest.Server, cfg Config) *Source {
	t.Helper()
	cfg.BaseURL = srv.URL
	if cfg.MetricName == "" {
		cfg.MetricName = "postgresql_query_mean_exec_time_ms"
	}
	src, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return src
}

func TestNew_validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"missing BaseURL", Config{MetricName: "m"}, true},
		{"missing MetricName", Config{BaseURL: "http://x"}, true},
		{"invalid BaseURL", Config{BaseURL: "://bad", MetricName: "m"}, true},
		{"valid minimal", Config{BaseURL: "http://x", MetricName: "m"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%+v) error = %v, wantErr %v", tt.cfg, err, tt.wantErr)
			}
		})
	}
}

func TestNew_defaults(t *testing.T) {
	src, err := New(Config{BaseURL: "http://x", MetricName: "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if src.cfg.QueryIDLabel != defaultQueryIDLabel {
		t.Errorf("QueryIDLabel default = %q, want %q", src.cfg.QueryIDLabel, defaultQueryIDLabel)
	}
	if src.cfg.QueryTextLabel != defaultQueryTextLabel {
		t.Errorf("QueryTextLabel default = %q, want %q", src.cfg.QueryTextLabel, defaultQueryTextLabel)
	}
	if src.cfg.Step != defaultStep {
		t.Errorf("Step default = %v, want %v", src.cfg.Step, defaultStep)
	}
	if src.cfg.HTTPClient == nil || src.cfg.HTTPClient.Timeout != defaultTimeout {
		t.Errorf("HTTPClient default timeout = %v, want %v", src.cfg.HTTPClient, defaultTimeout)
	}
}

func TestSamplesInRange_parsesMatrix(t *testing.T) {
	fake := &fakePrometheus{t: t, wantPath: "/api/v1/query_range", status: 200, body: `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"queryid": "123", "query": "SELECT 1"},
					"values": [
						[1700000000, "12.5"],
						[1700000015, "13.75"]
					]
				}
			]
		}
	}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	from := time.Unix(1700000000, 0)
	to := from.Add(time.Minute)
	samples := src.SamplesInRange(123, from, to)

	if len(samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(samples))
	}
	if samples[0].QueryID != 123 {
		t.Errorf("QueryID = %d, want 123", samples[0].QueryID)
	}
	if samples[0].MeanExecTimeMs != 12.5 {
		t.Errorf("samples[0].MeanExecTimeMs = %v, want 12.5", samples[0].MeanExecTimeMs)
	}
	if samples[1].MeanExecTimeMs != 13.75 {
		t.Errorf("samples[1].MeanExecTimeMs = %v, want 13.75", samples[1].MeanExecTimeMs)
	}
	if samples[0].QueryText != "SELECT 1" {
		t.Errorf("QueryText = %q, want %q", samples[0].QueryText, "SELECT 1")
	}
	if samples[0].Fingerprint == "" {
		t.Errorf("Fingerprint = %q, want non-empty when QueryText is set", samples[0].Fingerprint)
	}
	if !samples[0].RecordedAt.Before(samples[1].RecordedAt) {
		t.Errorf("samples not sorted by RecordedAt: %v, %v", samples[0].RecordedAt, samples[1].RecordedAt)
	}

	// Assert the request was scoped to the requested queryID via a label matcher.
	if q := fake.gotQuery.Get("query"); q != `postgresql_query_mean_exec_time_ms{queryid="123"}` {
		t.Errorf("query = %q", q)
	}
}

func TestSamplesInRange_queryTextLabelDisabled(t *testing.T) {
	fake := &fakePrometheus{t: t, status: 200, body: `{
		"status": "success",
		"data": {"resultType": "matrix", "result": [
			{"metric": {"queryid": "5"}, "values": [[1700000000, "1"]]}
		]}
	}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{QueryTextLabel: "-"})
	samples := src.SamplesInRange(5, time.Unix(1700000000, 0), time.Unix(1700000060, 0))
	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1", len(samples))
	}
	if samples[0].QueryText != "" || samples[0].Fingerprint != "" {
		t.Errorf("QueryText/Fingerprint should stay empty when QueryTextLabel is disabled, got %q / %q", samples[0].QueryText, samples[0].Fingerprint)
	}
}

func TestSamplesInRange_skipsNonNumericValues(t *testing.T) {
	fake := &fakePrometheus{t: t, status: 200, body: `{
		"status": "success",
		"data": {"resultType": "matrix", "result": [
			{"metric": {"queryid": "5"}, "values": [[1700000000, "NaN"], [1700000015, "7.0"]]}
		]}
	}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	samples := src.SamplesInRange(5, time.Unix(1700000000, 0), time.Unix(1700000060, 0))
	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1 (NaN sample skipped)", len(samples))
	}
	if samples[0].MeanExecTimeMs != 7.0 {
		t.Errorf("MeanExecTimeMs = %v, want 7.0", samples[0].MeanExecTimeMs)
	}
}

func TestSamplesInRange_emptyResultReturnsNil(t *testing.T) {
	fake := &fakePrometheus{t: t, status: 200, body: `{"status":"success","data":{"resultType":"matrix","result":[]}}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	if samples := src.SamplesInRange(5, time.Now(), time.Now()); samples != nil {
		t.Errorf("samples = %v, want nil", samples)
	}
}

func TestSamplesInRange_errorResponseReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"internal","error":"boom"}`))
	}))
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	if samples := src.SamplesInRange(5, time.Now(), time.Now()); samples != nil {
		t.Errorf("samples = %v, want nil on error", samples)
	}
}

func TestSamplesInRange_unreachableServerReturnsNil(t *testing.T) {
	src, err := New(Config{BaseURL: "http://127.0.0.1:1", MetricName: "m", Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if samples := src.SamplesInRange(5, time.Now(), time.Now()); samples != nil {
		t.Errorf("samples = %v, want nil when Prometheus is unreachable", samples)
	}
}

func TestSamplesInRange_wrongResultTypeReturnsNil(t *testing.T) {
	fake := &fakePrometheus{t: t, status: 200, body: `{"status":"success","data":{"resultType":"vector","result":[]}}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	if samples := src.SamplesInRange(5, time.Now(), time.Now()); samples != nil {
		t.Errorf("samples = %v, want nil when resultType is not matrix", samples)
	}
}

func TestAllQueryIDs_parsesVector(t *testing.T) {
	fake := &fakePrometheus{t: t, wantPath: "/api/v1/query", status: 200, body: `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{"metric": {"queryid": "111"}, "value": [1700000000, "3"]},
				{"metric": {"queryid": "222"}, "value": [1700000000, "1"]},
				{"metric": {"queryid": "111"}, "value": [1700000000, "3"]}
			]
		}
	}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	ids := src.AllQueryIDs()

	if len(ids) != 2 {
		t.Fatalf("len(ids) = %d, want 2 (dedup'd), got %v", len(ids), ids)
	}
	if ids[0] != 111 || ids[1] != 222 {
		t.Errorf("ids = %v, want sorted [111 222]", ids)
	}
}

func TestAllQueryIDs_skipsUnparseableLabels(t *testing.T) {
	fake := &fakePrometheus{t: t, status: 200, body: `{
		"status": "success",
		"data": {"resultType": "vector", "result": [
			{"metric": {"queryid": "not-a-number"}, "value": [1700000000, "1"]},
			{"metric": {"queryid": "42"}, "value": [1700000000, "1"]}
		]}
	}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	ids := src.AllQueryIDs()
	if len(ids) != 1 || ids[0] != 42 {
		t.Errorf("ids = %v, want [42]", ids)
	}
}

func TestAllQueryIDs_int64PrecisionPreserved(t *testing.T) {
	// A queryid near/exceeding 2^53 would silently lose precision if this
	// package ever round-tripped it through a JSON number / float64
	// instead of parsing the label string directly with strconv.ParseInt.
	const bigID = int64(9223372036854775000) // close to math.MaxInt64, not representable exactly as float64
	fake := &fakePrometheus{t: t, status: 200, body: `{
		"status": "success",
		"data": {"resultType": "vector", "result": [
			{"metric": {"queryid": "9223372036854775000"}, "value": [1700000000, "1"]}
		]}
	}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{})
	ids := src.AllQueryIDs()
	if len(ids) != 1 || ids[0] != bigID {
		t.Errorf("ids = %v, want [%d]", ids, bigID)
	}
}

func TestQueryEncoding_usesRFC3339AndDurationStep(t *testing.T) {
	fake := &fakePrometheus{t: t, status: 200, body: `{"status":"success","data":{"resultType":"matrix","result":[]}}`}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	src := newTestSource(t, srv, Config{Step: 30 * time.Second})
	from := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	to := from.Add(time.Minute)
	src.SamplesInRange(1, from, to)

	if got := fake.gotQuery.Get("start"); got != "2026-01-02T03:04:05Z" {
		t.Errorf("start = %q, want RFC3339", got)
	}
	if got := fake.gotQuery.Get("end"); got != "2026-01-02T03:05:05Z" {
		t.Errorf("end = %q, want RFC3339", got)
	}
	if got := fake.gotQuery.Get("step"); got != "30s" {
		t.Errorf("step = %q, want %q", got, "30s")
	}
}

func TestPromSampleUnmarshal(t *testing.T) {
	var s promSample
	if err := json.Unmarshal([]byte(`[1700000000.5, "42.25"]`), &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.Timestamp != 1700000000.5 {
		t.Errorf("Timestamp = %v, want 1700000000.5", s.Timestamp)
	}
	if s.Value != "42.25" {
		t.Errorf("Value = %q, want %q", s.Value, "42.25")
	}
}
