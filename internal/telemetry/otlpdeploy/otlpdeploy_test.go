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

package otlpdeploy

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// ----- test-only protobuf encoder (mirrors opentelemetry-proto field numbers) -----

func pbBytes(num protowire.Number, b []byte) []byte {
	out := protowire.AppendTag(nil, num, protowire.BytesType)
	return protowire.AppendBytes(out, b)
}

func pbString(num protowire.Number, s string) []byte { return pbBytes(num, []byte(s)) }

func pbVarint(num protowire.Number, v uint64) []byte {
	out := protowire.AppendTag(nil, num, protowire.VarintType)
	return protowire.AppendVarint(out, v)
}

func pbFixed64(num protowire.Number, v uint64) []byte {
	out := protowire.AppendTag(nil, num, protowire.Fixed64Type)
	return protowire.AppendFixed64(out, v)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func kvString(k, v string) []byte {
	return cat(pbString(1, k), pbBytes(2, pbString(1, v)))
}

func kvInt(k string, v int64) []byte {
	return cat(pbString(1, k), pbBytes(2, pbVarint(3, uint64(v))))
}

func kvArray(k string, vals ...string) []byte {
	var arr []byte
	for _, v := range vals {
		arr = append(arr, pbBytes(1, pbString(1, v))...)
	}
	return cat(pbString(1, k), pbBytes(2, pbBytes(5, arr)))
}

type testSpan struct {
	traceID, spanID []byte
	name            string
	start, end      uint64
	status          int
	attrs           [][]byte
}

func (s testSpan) proto() []byte {
	parts := [][]byte{
		pbBytes(1, s.traceID), pbBytes(2, s.spanID), pbString(5, s.name),
		pbFixed64(7, s.start), pbFixed64(8, s.end),
	}
	for _, a := range s.attrs {
		parts = append(parts, pbBytes(9, a))
	}
	if s.status != 0 {
		parts = append(parts, pbBytes(15, pbVarint(3, uint64(s.status))))
	}
	return cat(parts...)
}

func protoRequest(resourceAttrs [][]byte, spans ...testSpan) []byte {
	var res []byte
	for _, a := range resourceAttrs {
		res = append(res, pbBytes(1, a)...)
	}
	var ss []byte
	for _, s := range spans {
		ss = append(ss, pbBytes(2, s.proto())...)
	}
	rs := cat(pbBytes(1, res), pbBytes(2, ss))
	return pbBytes(1, rs)
}

var (
	traceA = mustHex("0102030405060708090a0b0c0d0e0f10")
	spanA  = mustHex("a1a2a3a4a5a6a7a8")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDecode_ProtoAndJSONAgree(t *testing.T) {
	t.Parallel()

	endNanos := uint64(time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC).UnixNano())
	pb := protoRequest(
		[][]byte{kvString("service.name", "checkout"), kvString("k8s.cluster.name", "prod-1")},
		testSpan{
			traceID: traceA, spanID: spanA, name: "deploy", start: endNanos - 1e9, end: endNanos, status: 1,
			attrs: [][]byte{kvString("vcs.ref.head.revision", "abc123"), kvArray("container.image.tags", "v9", "v8"), kvInt("retries", 3)},
		},
	)
	js := fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[
	  {"key":"service.name","value":{"stringValue":"checkout"}},
	  {"key":"k8s.cluster.name","value":{"stringValue":"prod-1"}}]},
	 "scopeSpans":[{"spans":[{"traceId":"%s","spanId":"%s","name":"deploy",
	  "startTimeUnixNano":"%d","endTimeUnixNano":%d,"status":{"code":"STATUS_CODE_OK"},
	  "attributes":[{"key":"vcs.ref.head.revision","value":{"stringValue":"abc123"}},
	   {"key":"container.image.tags","value":{"arrayValue":{"values":[{"stringValue":"v9"},{"stringValue":"v8"}]}}},
	   {"key":"retries","value":{"intValue":"3"}}]}]}]}]}`,
		hex.EncodeToString(traceA), hex.EncodeToString(spanA), endNanos-1e9, endNanos)

	fromPB, err := Decode(pb, FormatProtobuf)
	if err != nil {
		t.Fatalf("protobuf decode: %v", err)
	}
	fromJSON, err := Decode([]byte(js), FormatJSON)
	if err != nil {
		t.Fatalf("json decode: %v", err)
	}
	if len(fromPB) != 1 || len(fromJSON) != 1 {
		t.Fatalf("want 1 span each, got %d and %d", len(fromPB), len(fromJSON))
	}
	for name, sp := range map[string]Span{"protobuf": fromPB[0], "json": fromJSON[0]} {
		if sp.Name != "deploy" || sp.StartNanos != endNanos-1e9 || sp.EndNanos != endNanos || sp.StatusCode != 1 {
			t.Errorf("%s: unexpected span scalars: %+v", name, sp)
		}
		if !bytes.Equal(sp.TraceID, traceA) || !bytes.Equal(sp.SpanID, spanA) {
			t.Errorf("%s: ids differ: %x %x", name, sp.TraceID, sp.SpanID)
		}
		if sp.Attrs["container.image.tags"] != "v9" || sp.Attrs["retries"] != "3" || sp.Attrs["vcs.ref.head.revision"] != "abc123" {
			t.Errorf("%s: attrs = %v", name, sp.Attrs)
		}
		if sp.ResourceAttrs["service.name"] != "checkout" || sp.ResourceAttrs["k8s.cluster.name"] != "prod-1" {
			t.Errorf("%s: resource attrs = %v", name, sp.ResourceAttrs)
		}
	}
}

func TestDecode_Malformed(t *testing.T) {
	t.Parallel()

	valid := protoRequest(nil, testSpan{traceID: traceA, spanID: spanA, name: "deploy", end: 1})

	if _, err := Decode(valid[:len(valid)-3], FormatProtobuf); err == nil {
		t.Error("truncated protobuf should fail, not silently partial-decode")
	}
	if _, err := Decode([]byte{0xff, 0xff, 0xff}, FormatProtobuf); err == nil {
		t.Error("garbage protobuf should fail")
	}
	if _, err := Decode([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"zz"}]}]}]}`), FormatJSON); err == nil {
		t.Error("non-hex traceId should fail")
	}
	if _, err := Decode([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"spanId":"abcd"}]}]}]}`), FormatJSON); err == nil {
		t.Error("wrong-length spanId should fail")
	}
	if _, err := Decode([]byte(`not json`), FormatJSON); err == nil {
		t.Error("invalid JSON should fail")
	}

	// Unknown fields and wire types are skipped, not errors.
	withUnknown := cat(valid, pbString(99, "future-field"), pbFixed64(98, 7), pbVarint(97, 1))
	if spans, err := Decode(withUnknown, FormatProtobuf); err != nil || len(spans) != 1 {
		t.Errorf("unknown top-level fields: spans=%d err=%v", len(spans), err)
	}
}

func TestDecode_DeeplyNestedValueRejected(t *testing.T) {
	t.Parallel()

	v := pbString(1, "x")
	for i := 0; i < maxValueDepth+3; i++ {
		v = pbBytes(5, pbBytes(1, v)) // array_value{values[0]=v}
	}
	kv := cat(pbString(1, "deep"), pbBytes(2, v))
	req := protoRequest(nil, testSpan{traceID: traceA, spanID: spanA, name: "d", end: 1, attrs: [][]byte{kv}})
	if _, err := Decode(req, FormatProtobuf); err == nil {
		t.Fatal("deeply nested attribute value should be rejected")
	}
}

func TestNewMatcher_Validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		m    v1alpha1.OTLPSpanMapping
		ok   bool
	}{
		{"empty selector", v1alpha1.OTLPSpanMapping{}, false},
		{"span names", v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}}, true},
		{"attrs only", v1alpha1.OTLPSpanMapping{MatchAttributes: map[string]string{"a": "b"}}, true},
		{"blank span name", v1alpha1.OTLPSpanMapping{SpanNames: []string{""}}, false},
		{"blank attr key", v1alpha1.OTLPSpanMapping{MatchAttributes: map[string]string{"": "b"}}, false},
		{"bad timestamp", v1alpha1.OTLPSpanMapping{SpanNames: []string{"d"}, Timestamp: "middle"}, false},
		{"start timestamp", v1alpha1.OTLPSpanMapping{SpanNames: []string{"d"}, Timestamp: "start"}, true},
	}
	for _, tc := range cases {
		_, err := NewMatcher(tc.m)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, wantOK=%v", tc.name, err, tc.ok)
		}
	}
}

func TestMatcher_Events(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	end := now.Add(-2 * time.Minute)
	start := end.Add(-30 * time.Second)

	base := Span{
		TraceID: traceA, SpanID: spanA, Name: "deploy",
		StartNanos: uint64(start.UnixNano()), EndNanos: uint64(end.UnixNano()),
		Attrs:         map[string]string{"service.version": "1.2.3", "container.image.tag": "img:1.2.3"},
		ResourceAttrs: map[string]string{"service.name": "api", "k8s.namespace.name": "shop", "k8s.cluster.name": "c1"},
	}
	with := func(mut func(*Span)) Span {
		sp := base
		sp.Attrs = map[string]string{}
		for k, v := range base.Attrs {
			sp.Attrs[k] = v
		}
		mut(&sp)
		return sp
	}

	t.Run("defaults map semconv attributes", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}})
		res := m.Events([]Span{base}, now)
		if res.Matched != 1 || res.Rejected != 0 || len(res.Events) != 1 {
			t.Fatalf("result = %+v", res)
		}
		ev := res.Events[0]
		want := v1alpha1.DeployEvent{
			ID: EventID(traceA, spanA), App: "api", Namespace: "shop", Cluster: "c1",
			Revision: "1.2.3", ImageTag: "img:1.2.3", Timestamp: end,
		}
		if ev != want {
			t.Errorf("event = %+v\nwant   %+v", ev, want)
		}
	})

	t.Run("revision attribute prefers vcs over service.version", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}})
		sp := with(func(s *Span) { s.Attrs["vcs.ref.head.revision"] = "deadbeef" })
		if got := m.Events([]Span{sp}, now).Events[0].Revision; got != "deadbeef" {
			t.Errorf("revision = %q", got)
		}
	})

	t.Run("explicit attribute names override defaults", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{
			SpanNames: []string{"deploy"}, AppAttribute: "app", RevisionAttribute: "rev",
			NamespaceAttribute: "ns", ClusterAttribute: "cl", ImageTagAttribute: "img",
		})
		sp := with(func(s *Span) {
			s.Attrs["app"], s.Attrs["rev"], s.Attrs["ns"], s.Attrs["cl"], s.Attrs["img"] = "a", "r", "n", "c", "i"
		})
		ev := m.Events([]Span{sp}, now).Events[0]
		if ev.App != "a" || ev.Revision != "r" || ev.Namespace != "n" || ev.Cluster != "c" || ev.ImageTag != "i" {
			t.Errorf("event = %+v", ev)
		}
	})

	t.Run("selector: names and attributes both must match", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{
			SpanNames:       []string{"deploy"},
			MatchAttributes: map[string]string{"deployment.environment.name": "prod"},
		})
		other := with(func(s *Span) { s.Name = "compile" })
		wrongEnv := with(func(s *Span) { s.Attrs["deployment.environment.name"] = "staging" })
		prod := with(func(s *Span) { s.Attrs["deployment.environment.name"] = "prod" })
		prodResource := with(func(s *Span) {
			s.ResourceAttrs = map[string]string{"service.name": "api", "deployment.environment.name": "prod"}
		})
		res := m.Events([]Span{other, wrongEnv, prod, prodResource}, now)
		if len(res.Events) != 2 || res.Rejected != 0 {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("failed spans skipped unless opted in", func(t *testing.T) {
		failed := with(func(s *Span) { s.StatusCode = statusCodeError })
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}})
		if res := m.Events([]Span{failed}, now); len(res.Events) != 0 || res.Rejected != 0 {
			t.Errorf("failed span should be ignored silently: %+v", res)
		}
		m, _ = NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}, IncludeFailedSpans: true})
		if res := m.Events([]Span{failed}, now); len(res.Events) != 1 {
			t.Errorf("includeFailedSpans should accept it: %+v", res)
		}
	})

	t.Run("matching span without app is rejected with a reason", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}})
		noApp := with(func(s *Span) { s.ResourceAttrs = nil })
		res := m.Events([]Span{noApp, base}, now)
		if res.Rejected != 1 || len(res.Events) != 1 || !strings.Contains(res.RejectReason, "service.name") {
			t.Errorf("result = %+v", res)
		}
	})

	t.Run("timestamp: start mode, missing end, future clamp, missing both", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}, Timestamp: "start"})
		if got := m.Events([]Span{base}, now).Events[0].Timestamp; !got.Equal(start) {
			t.Errorf("start mode ts = %v", got)
		}
		m, _ = NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}})
		noEnd := with(func(s *Span) { s.EndNanos = 0 })
		if got := m.Events([]Span{noEnd}, now).Events[0].Timestamp; !got.Equal(start) {
			t.Errorf("missing end should fall back to start, got %v", got)
		}
		future := with(func(s *Span) { s.EndNanos = uint64(now.Add(time.Hour).UnixNano()) })
		if got := m.Events([]Span{future}, now).Events[0].Timestamp; !got.Equal(now) {
			t.Errorf("future end should clamp to now, got %v", got)
		}
		none := with(func(s *Span) { s.EndNanos, s.StartNanos = 0, 0 })
		if got := m.Events([]Span{none}, now).Events[0].Timestamp; !got.Equal(now) {
			t.Errorf("no timestamps should use now, got %v", got)
		}
	})

	t.Run("ids are deterministic, and stable even without trace ids", func(t *testing.T) {
		m, _ := NewMatcher(v1alpha1.OTLPSpanMapping{SpanNames: []string{"deploy"}})
		noIDs := with(func(s *Span) { s.TraceID, s.SpanID = nil, nil })
		a := m.Events([]Span{noIDs}, now).Events[0].ID
		b := m.Events([]Span{noIDs}, now.Add(time.Hour)).Events[0].ID
		if a != b || !strings.HasPrefix(a, "otlp-h-") {
			t.Errorf("fallback ids = %q, %q", a, b)
		}
	})
}

func TestEventIDRoundTrip(t *testing.T) {
	t.Parallel()

	id := EventID(traceA, spanA)
	tr, sp, ok := ParseEventID(id)
	if !ok || !bytes.Equal(tr, traceA) || !bytes.Equal(sp, spanA) {
		t.Fatalf("round trip failed: %q -> %x %x %v", id, tr, sp, ok)
	}
	for _, bad := range []string{"", "argocd-app-rev", "otlp-h-0011223344556677", "otlp-zz-yy", "otlp-" + strings.Repeat("0", 32) + "-xyz"} {
		if _, _, ok := ParseEventID(bad); ok {
			t.Errorf("ParseEventID(%q) should fail", bad)
		}
	}
}

func TestFormatFromContentType(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		f  Format
		ok bool
	}{
		"application/x-protobuf":                {FormatProtobuf, true},
		"application/x-protobuf; charset=utf-8": {FormatProtobuf, true},
		"Application/JSON":                      {FormatJSON, true},
		"application/json; charset=utf-8":       {FormatJSON, true},
		"text/plain":                            {0, false},
		"":                                      {0, false},
	}
	for ct, want := range cases {
		f, ok := FormatFromContentType(ct)
		if ok != want.ok || (ok && f != want.f) {
			t.Errorf("FormatFromContentType(%q) = %v,%v", ct, f, ok)
		}
	}
}

func TestEncodeResponse(t *testing.T) {
	t.Parallel()

	if got := EncodeResponse(FormatJSON, 0, ""); string(got) != "{}" {
		t.Errorf("json ok = %q", got)
	}
	if got := EncodeResponse(FormatProtobuf, 0, ""); len(got) != 0 {
		t.Errorf("protobuf ok should be empty, got %x", got)
	}
	got := EncodeResponse(FormatJSON, 2, `bad "span"`)
	if string(got) != `{"partialSuccess":{"rejectedSpans":"2","errorMessage":"bad \"span\""}}` {
		t.Errorf("json partial = %s", got)
	}
	pb := EncodeResponse(FormatProtobuf, 2, "oops")
	var rejected uint64
	var msg string
	err := forEachField(pb, func(num protowire.Number, _ protowire.Type, v []byte, _ uint64) error {
		if num != 1 {
			return nil
		}
		return forEachField(v, func(n protowire.Number, _ protowire.Type, v []byte, s uint64) error {
			switch n {
			case 1:
				rejected = s
			case 2:
				msg = string(v)
			}
			return nil
		})
	})
	if err != nil || rejected != 2 || msg != "oops" {
		t.Errorf("protobuf partial: rejected=%d msg=%q err=%v", rejected, msg, err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(protoRequest(nil, testSpan{traceID: traceA, spanID: spanA, name: "deploy", end: 1}), false)
	f.Add([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"name":"x"}]}]}]}`), true)
	f.Fuzz(func(t *testing.T, b []byte, asJSON bool) {
		format := FormatProtobuf
		if asJSON {
			format = FormatJSON
		}
		_, _ = Decode(b, format) // must never panic
	})
}
