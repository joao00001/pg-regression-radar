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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// maxValueDepth bounds nesting of OTLP AnyValue arrays. Real
// instrumentation never goes anywhere near this; the bound exists so a
// hostile payload can't recurse the decoder into a stack overflow.
const maxValueDepth = 8

// Span is the subset of an OTLP span this package needs. Attribute values
// are flattened to strings: scalars use their natural text form and an
// array is represented by its first element (enough for the semantic
// conventions this package reads, e.g. container.image.tags).
type Span struct {
	TraceID       []byte
	SpanID        []byte
	Name          string
	StartNanos    uint64
	EndNanos      uint64
	StatusCode    int32
	Attrs         map[string]string
	ResourceAttrs map[string]string
}

// statusCodeError is opentelemetry.proto.trace.v1.Status.STATUS_CODE_ERROR.
const statusCodeError = 2

// ----- protobuf -----

// decodeProto decodes an ExportTraceServiceRequest in OTLP/HTTP binary
// protobuf encoding. It uses protowire directly instead of generated types:
// only a handful of fields matter here and the generated OTLP packages
// would drag a gRPC dependency tree into every binary of this project.
func decodeProto(b []byte) ([]Span, error) {
	var spans []Span
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if num != 1 || typ != protowire.BytesType { // resource_spans
			return nil
		}
		return decodeResourceSpans(v, &spans)
	})
	return spans, err
}

func decodeResourceSpans(b []byte, out *[]Span) error {
	resourceAttrs := map[string]string{}
	var scopeSpans [][]byte
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 1: // resource
			attrs, err := decodeResource(v)
			if err != nil {
				return err
			}
			resourceAttrs = attrs
		case 2: // scope_spans
			scopeSpans = append(scopeSpans, v)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ss := range scopeSpans {
		err := forEachField(ss, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
			if num != 2 || typ != protowire.BytesType { // spans
				return nil
			}
			sp, err := decodeSpan(v)
			if err != nil {
				return err
			}
			sp.ResourceAttrs = resourceAttrs
			*out = append(*out, sp)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func decodeResource(b []byte) (map[string]string, error) {
	attrs := map[string]string{}
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if num != 1 || typ != protowire.BytesType { // attributes
			return nil
		}
		return decodeKeyValue(v, attrs)
	})
	return attrs, err
}

func decodeSpan(b []byte) (Span, error) {
	sp := Span{Attrs: map[string]string{}}
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, scalar uint64) error {
		switch {
		case num == 1 && typ == protowire.BytesType:
			sp.TraceID = append([]byte(nil), v...)
		case num == 2 && typ == protowire.BytesType:
			sp.SpanID = append([]byte(nil), v...)
		case num == 5 && typ == protowire.BytesType:
			sp.Name = string(v)
		case num == 7 && typ == protowire.Fixed64Type:
			sp.StartNanos = scalar
		case num == 8 && typ == protowire.Fixed64Type:
			sp.EndNanos = scalar
		case num == 9 && typ == protowire.BytesType:
			return decodeKeyValue(v, sp.Attrs)
		case num == 15 && typ == protowire.BytesType: // status
			return forEachField(v, func(n protowire.Number, t protowire.Type, _ []byte, s uint64) error {
				if n == 3 && t == protowire.VarintType {
					sp.StatusCode = int32(s)
				}
				return nil
			})
		}
		return nil
	})
	return sp, err
}

func decodeKeyValue(b []byte, into map[string]string) error {
	var key string
	var value []byte
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 1:
			key = string(v)
		case 2:
			value = v
		}
		return nil
	})
	if err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	s, _, err := decodeAnyValue(value, 0)
	if err != nil {
		return err
	}
	into[key] = s
	return nil
}

// decodeAnyValue returns the flattened string form of an AnyValue, plus
// whether it carried any value at all.
func decodeAnyValue(b []byte, depth int) (string, bool, error) {
	if depth > maxValueDepth {
		return "", false, errors.New("otlpdeploy: attribute value nested too deeply")
	}
	var out string
	var set bool
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, scalar uint64) error {
		if set {
			return nil
		}
		switch {
		case num == 1 && typ == protowire.BytesType: // string_value
			out, set = string(v), true
		case num == 2 && typ == protowire.VarintType: // bool_value
			out, set = strconv.FormatBool(scalar != 0), true
		case num == 3 && typ == protowire.VarintType: // int_value
			out, set = strconv.FormatInt(int64(scalar), 10), true
		case num == 4 && typ == protowire.Fixed64Type: // double_value
			out, set = strconv.FormatFloat(math.Float64frombits(scalar), 'g', -1, 64), true
		case num == 5 && typ == protowire.BytesType: // array_value
			first, err := firstArrayElement(v, depth)
			if err != nil {
				return err
			}
			out, set = first, true
		case num == 7 && typ == protowire.BytesType: // bytes_value
			out, set = hex.EncodeToString(v), true
		}
		return nil
	})
	return out, set, err
}

func firstArrayElement(b []byte, depth int) (string, error) {
	var first string
	var found bool
	err := forEachField(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if found || num != 1 || typ != protowire.BytesType {
			return nil
		}
		s, ok, err := decodeAnyValue(v, depth+1)
		if err != nil {
			return err
		}
		first, found = s, ok
		return nil
	})
	return first, err
}

// forEachField walks the wire-format fields of one message. For
// length-delimited fields v is the payload; for fixed/varint fields scalar
// carries the decoded value. Malformed or truncated input is an error
// rather than a silent partial decode.
func forEachField(b []byte, fn func(num protowire.Number, typ protowire.Type, v []byte, scalar uint64) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("otlpdeploy: malformed protobuf: %w", protowire.ParseError(n))
		}
		b = b[n:]
		var (
			v      []byte
			scalar uint64
		)
		switch typ {
		case protowire.VarintType:
			x, m := protowire.ConsumeVarint(b)
			if m < 0 {
				return fmt.Errorf("otlpdeploy: malformed protobuf varint: %w", protowire.ParseError(m))
			}
			scalar, b = x, b[m:]
		case protowire.Fixed64Type:
			x, m := protowire.ConsumeFixed64(b)
			if m < 0 {
				return fmt.Errorf("otlpdeploy: malformed protobuf fixed64: %w", protowire.ParseError(m))
			}
			scalar, b = x, b[m:]
		case protowire.BytesType:
			x, m := protowire.ConsumeBytes(b)
			if m < 0 {
				return fmt.Errorf("otlpdeploy: malformed protobuf bytes: %w", protowire.ParseError(m))
			}
			v, b = x, b[m:]
		default:
			m := protowire.ConsumeFieldValue(num, typ, b)
			if m < 0 {
				return fmt.Errorf("otlpdeploy: malformed protobuf field: %w", protowire.ParseError(m))
			}
			b = b[m:]
		}
		if err := fn(num, typ, v, scalar); err != nil {
			return err
		}
	}
	return nil
}

// ----- JSON -----

// OTLP/JSON differs from plain protobuf-JSON in ways that matter here:
// trace/span IDs are hex (not base64), and 64-bit integers may arrive as
// either JSON strings or numbers depending on the sender. These structs
// model exactly the subset needed rather than going through protojson.

type jsonRequest struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []jsonKeyValue `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []jsonSpan `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

type jsonSpan struct {
	TraceID    string         `json:"traceId"`
	SpanID     string         `json:"spanId"`
	Name       string         `json:"name"`
	Start      flexUint64     `json:"startTimeUnixNano"`
	End        flexUint64     `json:"endTimeUnixNano"`
	Attributes []jsonKeyValue `json:"attributes"`
	Status     struct {
		Code flexStatusCode `json:"code"`
	} `json:"status"`
}

type jsonKeyValue struct {
	Key   string       `json:"key"`
	Value jsonAnyValue `json:"value"`
}

type jsonAnyValue struct {
	StringValue *string        `json:"stringValue"`
	BoolValue   *bool          `json:"boolValue"`
	IntValue    *flexInt64     `json:"intValue"`
	DoubleValue *float64       `json:"doubleValue"`
	BytesValue  *string        `json:"bytesValue"`
	ArrayValue  *jsonArrayVals `json:"arrayValue"`
}

type jsonArrayVals struct {
	Values []jsonAnyValue `json:"values"`
}

func (v jsonAnyValue) flatten(depth int) (string, error) {
	if depth > maxValueDepth {
		return "", errors.New("otlpdeploy: attribute value nested too deeply")
	}
	switch {
	case v.StringValue != nil:
		return *v.StringValue, nil
	case v.BoolValue != nil:
		return strconv.FormatBool(*v.BoolValue), nil
	case v.IntValue != nil:
		return strconv.FormatInt(int64(*v.IntValue), 10), nil
	case v.DoubleValue != nil:
		return strconv.FormatFloat(*v.DoubleValue, 'g', -1, 64), nil
	case v.BytesValue != nil:
		return *v.BytesValue, nil
	case v.ArrayValue != nil:
		if len(v.ArrayValue.Values) == 0 {
			return "", nil
		}
		return v.ArrayValue.Values[0].flatten(depth + 1)
	}
	return "", nil
}

func flattenAttrs(kvs []jsonKeyValue) (map[string]string, error) {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if kv.Key == "" {
			continue
		}
		s, err := kv.Value.flatten(0)
		if err != nil {
			return nil, err
		}
		out[kv.Key] = s
	}
	return out, nil
}

// flexUint64 accepts a JSON string ("123") or number (123).
type flexUint64 uint64

func (f *flexUint64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return fmt.Errorf("otlpdeploy: invalid unix-nano timestamp %q", s)
	}
	*f = flexUint64(n)
	return nil
}

// flexInt64 accepts a JSON string ("123") or number (123).
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("otlpdeploy: invalid int64 attribute value %q", s)
	}
	*f = flexInt64(n)
	return nil
}

// flexStatusCode accepts the numeric enum (2) or its name
// ("STATUS_CODE_ERROR"); the OTLP/JSON spec mandates integers but some
// proto3-JSON encoders emit names.
type flexStatusCode int32

func (f *flexStatusCode) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	switch s {
	case "", "null", "STATUS_CODE_UNSET":
		*f = 0
	case "STATUS_CODE_OK":
		*f = 1
	case "STATUS_CODE_ERROR":
		*f = statusCodeError
	default:
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return fmt.Errorf("otlpdeploy: invalid span status code %q", s)
		}
		*f = flexStatusCode(n)
	}
	return nil
}

func decodeJSON(b []byte) ([]Span, error) {
	var req jsonRequest
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, fmt.Errorf("otlpdeploy: decoding OTLP/JSON: %w", err)
	}
	var spans []Span
	for _, rs := range req.ResourceSpans {
		resAttrs, err := flattenAttrs(rs.Resource.Attributes)
		if err != nil {
			return nil, err
		}
		for _, ss := range rs.ScopeSpans {
			for _, js := range ss.Spans {
				attrs, err := flattenAttrs(js.Attributes)
				if err != nil {
					return nil, err
				}
				traceID, err := decodeHexID(js.TraceID, 16)
				if err != nil {
					return nil, fmt.Errorf("otlpdeploy: span %q traceId: %w", js.Name, err)
				}
				spanID, err := decodeHexID(js.SpanID, 8)
				if err != nil {
					return nil, fmt.Errorf("otlpdeploy: span %q spanId: %w", js.Name, err)
				}
				spans = append(spans, Span{
					TraceID:       traceID,
					SpanID:        spanID,
					Name:          js.Name,
					StartNanos:    uint64(js.Start),
					EndNanos:      uint64(js.End),
					StatusCode:    int32(js.Status.Code),
					Attrs:         attrs,
					ResourceAttrs: resAttrs,
				})
			}
		}
	}
	return spans, nil
}

// decodeHexID parses an OTLP/JSON hex trace or span ID. An empty ID is
// legal (some senders omit it) and yields nil.
func decodeHexID(s string, wantLen int) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("not valid hex: %w", err)
	}
	if len(b) != wantLen {
		return nil, fmt.Errorf("want %d bytes, got %d", wantLen, len(b))
	}
	return b, nil
}
