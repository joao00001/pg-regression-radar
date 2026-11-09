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

package ingester_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func otlpSource(mut func(*v1alpha1.DeploySource)) v1alpha1.DeploySource {
	src := v1alpha1.DeploySource{
		Name: "otel", SourceType: "otlp", ClusterName: "fallback-cluster",
		OTLP: &v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}},
	}
	if mut != nil {
		mut(&src)
	}
	return src
}

func otlpJSON(app string, names ...string) string {
	end := time.Now().Add(-time.Minute).UnixNano()
	var spans []string
	for i, n := range names {
		spans = append(spans, fmt.Sprintf(
			`{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"a1a2a3a4a5a6a7a%d","name":%q,"endTimeUnixNano":"%d","status":{"code":1},
			  "attributes":[{"key":"vcs.ref.head.revision","value":{"stringValue":"rev%d"}}]}`, i, n, end, i))
	}
	return fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":%q}}]},
	  "scopeSpans":[{"spans":[%s]}]}]}`, app, strings.Join(spans, ","))
}

func post(h http.Handler, body []byte, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestOTLP_JSONStoresMatchingSpansOnly(t *testing.T) {
	t.Parallel()
	store := ingester.NewStore()
	h := ingester.NewHandler(store, otlpSource(nil), nil)

	rr := post(h, []byte(otlpJSON("checkout", "deploy", "compile", "deploy")), map[string]string{"Content-Type": "application/json"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	if rr.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(rr.Body.String()) != "{}" {
		t.Errorf("response = %q (%s)", rr.Body, rr.Header().Get("Content-Type"))
	}
	evs := store.All()
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(evs), evs)
	}
	ev := evs[0]
	if ev.App != "checkout" || ev.Source != "otel" || ev.Cluster != "fallback-cluster" || ev.Revision == "" {
		t.Errorf("event = %+v", ev)
	}

	// A retried export must not duplicate events: IDs are derived from the span.
	post(h, []byte(otlpJSON("checkout", "deploy", "compile", "deploy")), map[string]string{"Content-Type": "application/json"})
	if n := len(store.All()); n != 2 {
		t.Errorf("retry duplicated events: %d", n)
	}
}

func TestOTLP_ProtobufGzip(t *testing.T) {
	t.Parallel()
	store := ingester.NewStore()
	h := ingester.NewHandler(store, otlpSource(nil), nil)

	str := func(num protowire.Number, s string) []byte {
		return protowire.AppendString(protowire.AppendTag(nil, num, protowire.BytesType), s)
	}
	msg := func(num protowire.Number, b ...[]byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, num, protowire.BytesType), bytes.Join(b, nil))
	}
	kv := msg(1, str(1, "service.name"), msg(2, str(1, "billing")))
	span := bytes.Join([][]byte{
		str(5, "deploy"),
		protowire.AppendFixed64(protowire.AppendTag(nil, 8, protowire.Fixed64Type), uint64(time.Now().Add(-time.Minute).UnixNano())),
	}, nil)
	req := msg(1, msg(1, kv), msg(2, msg(2, span)))

	rr := post(h, gz(t, req), map[string]string{"Content-Type": "application/x-protobuf", "Content-Encoding": "gzip"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	if rr.Header().Get("Content-Type") != "application/x-protobuf" || rr.Body.Len() != 0 {
		t.Errorf("response ct=%q len=%d", rr.Header().Get("Content-Type"), rr.Body.Len())
	}
	if evs := store.All(); len(evs) != 1 || evs[0].App != "billing" {
		t.Errorf("events = %+v", evs)
	}
}

func TestOTLP_AppNameFilterAndPartialSuccess(t *testing.T) {
	t.Parallel()
	store := ingester.NewStore()
	h := ingester.NewHandler(store, otlpSource(func(s *v1alpha1.DeploySource) { s.AppName = "checkout" }), nil)
	hdr := map[string]string{"Content-Type": "application/json"}

	if rr := post(h, []byte(otlpJSON("other", "deploy")), hdr); rr.Code != http.StatusOK || len(store.All()) != 0 {
		t.Errorf("other app: code=%d events=%d", rr.Code, len(store.All()))
	}

	// Matching span with no service.name: accepted request, reported as rejected.
	noApp := `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"a1a2a3a4a5a6a7a8","name":"deploy"}]}]}]}`
	rr := post(h, []byte(noApp), hdr)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"rejectedSpans":"1"`) {
		t.Errorf("partial success: code=%d body=%s", rr.Code, rr.Body)
	}
}

func TestOTLP_RequestValidation(t *testing.T) {
	t.Parallel()
	good := []byte(otlpJSON("a", "deploy"))
	json := map[string]string{"Content-Type": "application/json"}

	cases := []struct {
		name string
		h    http.Handler
		body []byte
		hdr  map[string]string
		want int
	}{
		{"unknown content type", ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil), good, map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"unknown encoding", ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil), good, map[string]string{"Content-Type": "application/json", "Content-Encoding": "br"}, http.StatusUnsupportedMediaType},
		{"bad gzip", ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil), good, map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"}, http.StatusBadRequest},
		{"malformed json", ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil), []byte(`{`), json, http.StatusBadRequest},
		{"malformed protobuf", ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil), []byte{0xff, 0xff}, map[string]string{"Content-Type": "application/x-protobuf"}, http.StatusBadRequest},
		{"oversized body", ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil), bytes.Repeat([]byte(" "), 9<<20), json, http.StatusRequestEntityTooLarge},
		{"missing mapping", ingester.NewHandler(ingester.NewStore(), otlpSource(func(s *v1alpha1.DeploySource) { s.OTLP = nil }), nil), good, json, http.StatusInternalServerError},
		{"empty selector", ingester.NewHandler(ingester.NewStore(), otlpSource(func(s *v1alpha1.DeploySource) { s.OTLP = &v1alpha1.OTLPSpanMapping{} }), nil), good, json, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if rr := post(tc.h, tc.body, tc.hdr); rr.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, rr.Code, tc.want, strings.TrimSpace(rr.Body.String()))
		}
	}
}

func TestOTLP_GzipBombRejected(t *testing.T) {
	t.Parallel()
	h := ingester.NewHandler(ingester.NewStore(), otlpSource(nil), nil)
	bomb := gz(t, bytes.Repeat([]byte(" "), 20<<20)) // compresses to a few KiB
	if len(bomb) > 1<<20 {
		t.Fatalf("test payload not small enough: %d", len(bomb))
	}
	rr := post(h, bomb, map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"})
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
}

func TestOTLP_AuthAndMethod(t *testing.T) {
	t.Parallel()
	store := ingester.NewStore()
	h := ingester.NewHandler(store, otlpSource(func(s *v1alpha1.DeploySource) { s.WebhookSecret = "s3cret" }), nil)
	body := []byte(otlpJSON("a", "deploy"))

	if rr := post(h, body, map[string]string{"Content-Type": "application/json"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rr.Code)
	}
	if rr := post(h, body, map[string]string{"Content-Type": "application/json", "X-Webhook-Token": "s3cret"}); rr.Code != http.StatusOK {
		t.Errorf("with token: %d", rr.Code)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/traces", io.NopCloser(strings.NewReader(""))))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rr.Code)
	}
}

func TestValidateSource(t *testing.T) {
	t.Parallel()
	if err := ingester.ValidateSource(v1alpha1.DeploySource{SourceType: "argocd"}); err != nil {
		t.Errorf("non-otlp should be valid: %v", err)
	}
	if err := ingester.ValidateSource(otlpSource(nil)); err != nil {
		t.Errorf("valid otlp: %v", err)
	}
	if err := ingester.ValidateSource(otlpSource(func(s *v1alpha1.DeploySource) { s.OTLP = nil })); err == nil {
		t.Error("otlp without mapping should be invalid")
	}
	if !ingester.ValidSourceTypes["otlp"] {
		t.Error("otlp must be a valid source type")
	}
}
