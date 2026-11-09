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

package cli

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func parseDeployFlags(t *testing.T, args ...string) *otlpDeployFlags {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := registerOTLPDeployFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func TestOTLPDeployFlags_Mapping(t *testing.T) {
	t.Parallel()
	f := parseDeployFlags(t,
		"--otlp-span-name", "deploy", "--otlp-span-name", "rollout",
		"--otlp-match-attribute", "cicd.pipeline.name=release",
		"--otlp-match-attribute", "deployment.environment=prod=eu",
		"--otlp-include-failed", "--otlp-timestamp", "start", "--otlp-app-attribute", "app.name")
	m, err := f.mapping("otlp")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.SpanNames) != 2 || m.SpanNames[1] != "rollout" {
		t.Fatalf("span names = %v", m.SpanNames)
	}
	if m.MatchAttributes["cicd.pipeline.name"] != "release" || m.MatchAttributes["deployment.environment"] != "prod=eu" {
		t.Fatalf("match attributes = %v (value may itself contain '=')", m.MatchAttributes)
	}
	if !m.IncludeFailedSpans || m.Timestamp != "start" || m.AppAttribute != "app.name" {
		t.Fatalf("unexpected mapping %+v", m)
	}
}

func TestOTLPDeployFlags_MappingErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		sourceType string
		args       []string
	}{
		{"no equals", "otlp", []string{"--otlp-match-attribute", "novalue"}},
		{"empty key", "otlp", []string{"--otlp-match-attribute", "=v"}},
		{"conflicting duplicate", "otlp", []string{"--otlp-match-attribute", "a=1", "--otlp-match-attribute", "a=2"}},
		{"flags without otlp source", "argocd", []string{"--otlp-span-name", "deploy"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseDeployFlags(t, tc.args...).mapping(tc.sourceType); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if m, err := parseDeployFlags(t).mapping("argocd"); m != nil || err != nil {
		t.Fatalf("non-otlp source without flags must yield nil mapping, got %v %v", m, err)
	}
}

const cliOTLPTrace = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"api"}}]},` +
	`"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"deploy",` +
	`"startTimeUnixNano":"1700000000000000000","endTimeUnixNano":"1700000001000000000","status":{"code":1}}]}]}]}`

func TestIngesterMux_OTLPServesTracesPath(t *testing.T) {
	t.Parallel()
	store := ingester.NewStore()
	source := v1alpha1.DeploySource{
		Name: "t", SourceType: "otlp", ClusterName: "c1",
		OTLP: &v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}},
	}
	mux := newIngesterMux(store, source, "", nil)
	for _, path := range []string{"/webhook", "/webhook/v1/traces"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(cliOTLPTrace))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	evs := store.All()
	if len(evs) != 1 || evs[0].App != "api" || evs[0].Cluster != "c1" {
		t.Fatalf("events = %+v", evs)
	}

	// Other source types must not grow a /v1/traces route.
	plain := newIngesterMux(ingester.NewStore(), v1alpha1.DeploySource{SourceType: "generic"}, "", nil)
	rec := httptest.NewRecorder()
	plain.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhook/v1/traces", strings.NewReader("{}")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("generic source /webhook/v1/traces = %d, want 404", rec.Code)
	}
}

func exportFlags(t *testing.T, args ...string) *otlpExportFlags {
	t.Helper()
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := registerOTLPExportFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func TestOTLPExportFlags_Config(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")

	if _, on, err := exportFlags(t).config(nil, "", nil, nil, nil); on || err != nil {
		t.Fatalf("no endpoint must disable export without error, got on=%v err=%v", on, err)
	}

	cfg, on, err := exportFlags(t, "--otlp-endpoint", "http://localhost:4318", "--otlp-encoding", "json", "--otlp-gzip", "--otlp-timeout", "3s").
		config(nil, "", map[string]string{"k8s.cluster.name": "c"}, nil, nil)
	if err != nil || !on {
		t.Fatalf("config: on=%v err=%v", on, err)
	}
	if cfg.Endpoint != "http://localhost:4318/v1/logs" || string(cfg.Encoding) != "json" || !cfg.Gzip || cfg.Timeout != 3*time.Second {
		t.Fatalf("unexpected cfg %+v", cfg)
	}
}

func TestOTLPExportFlags_HeadersFromEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "authorization=generic")
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "authorization=logs")
	cfg, _, err := exportFlags(t, "--otlp-endpoint", "https://c.example.com").config(nil, "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Headers["authorization"] != "logs" {
		t.Fatalf("signal-specific env must win, got %v", cfg.Headers)
	}
	cfg, _, err = exportFlags(t, "--otlp-endpoint", "https://c.example.com", "--otlp-headers", "authorization=flag").config(nil, "", nil, nil, nil)
	if err != nil || cfg.Headers["authorization"] != "flag" {
		t.Fatalf("flag must win over env: %v %v", cfg.Headers, err)
	}
}

func TestOTLPExportFlags_Rejections(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "")
	cases := []struct {
		name    string
		args    []string
		allowed []string
		policy  alerting.DestinationPolicy
	}{
		{name: "bad scheme", args: []string{"--otlp-endpoint", "ftp://x"}},
		{name: "credentials in url", args: []string{"--otlp-endpoint", "https://u:p@x.example.com"}},
		{name: "bad encoding", args: []string{"--otlp-endpoint", "http://x.example.com", "--otlp-encoding", "xml"}},
		{name: "bad timeout", args: []string{"--otlp-endpoint", "http://x.example.com", "--otlp-timeout", "0s"}},
		{name: "bad headers", args: []string{"--otlp-endpoint", "http://x.example.com", "--otlp-headers", "oops"}},
		{name: "not allowlisted", args: []string{"--otlp-endpoint", "http://x.example.com"}, allowed: []string{"y.example.com"}},
		{name: "relay only", args: []string{"--otlp-endpoint", "http://x.example.com"}, policy: alerting.DestinationPolicyRelayOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := exportFlags(t, tc.args...).config(tc.allowed, tc.policy, nil, nil, nil); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
