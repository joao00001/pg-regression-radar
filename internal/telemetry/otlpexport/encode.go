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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/joao00001/pg-regression-radar/internal/telemetry/otlpdeploy"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

const (
	// EventName is the LogRecord event_name every exported regression carries.
	EventName = "pg_regression_radar.performance_regression"

	scopeName = "github.com/joao00001/pg-regression-radar"

	// OTLP SeverityNumber for WARN.
	severityWarn = 13
)

// attr is one OTLP KeyValue with a typed value (string, int64, float64 or bool).
type attr struct {
	key string
	val any
}

// logRecord is the encoding-independent form of one exported regression.
type logRecord struct {
	timeNanos     uint64
	observedNanos uint64
	body          string
	attrs         []attr
	traceID       []byte
	spanID        []byte
}

func nanos(t time.Time) uint64 {
	if t.IsZero() || t.UnixNano() <= 0 {
		return 0
	}
	return uint64(t.UnixNano())
}

// buildRecord maps a PerformanceRegression onto an OTLP log record. When the
// regression's deploy came from an OTLP span (otlpdeploy.EventID), the
// record carries that span's trace_id/span_id, so a backend can jump from a
// deploy trace straight to the regression it caused.
func buildRecord(res v1alpha1.PerformanceRegression, observed time.Time) logRecord {
	ts := res.DetectedChangeAt
	if ts.IsZero() {
		ts = res.CreatedAt
	}
	if ts.IsZero() {
		ts = observed
	}

	rec := logRecord{timeNanos: nanos(ts), observedNanos: nanos(observed)}

	trigger := string(res.TriggerType)
	if trigger == "" {
		trigger = string(v1alpha1.TriggerTypeDeploy)
	}
	if res.TriggerType == v1alpha1.TriggerTypePeriodic || res.DeployEventID == "" {
		rec.body = fmt.Sprintf("Query %d latency regressed %.2fx (mean %.2f ms -> %.2f ms), confidence %.2f",
			res.QueryID, res.LatencyChangeFactor, res.MeanLatencyBefore, res.MeanLatencyAfter, res.ConfidenceScore)
	} else {
		rec.body = fmt.Sprintf("Query %d latency regressed %.2fx (mean %.2f ms -> %.2f ms) after deploy %s, confidence %.2f",
			res.QueryID, res.LatencyChangeFactor, res.MeanLatencyBefore, res.MeanLatencyAfter, res.DeployEventID, res.ConfidenceScore)
	}

	a := []attr{
		{"db.system.name", "postgresql"},
		{"pg_regression_radar.regression.name", res.Name},
		{"pg_regression_radar.regression.status", string(res.Status)},
		{"pg_regression_radar.regression.trigger_type", trigger},
		{"pg_regression_radar.query_id", res.QueryID},
		{"pg_regression_radar.confidence", res.ConfidenceScore},
		{"pg_regression_radar.latency.before_ms", res.MeanLatencyBefore},
		{"pg_regression_radar.latency.after_ms", res.MeanLatencyAfter},
		{"pg_regression_radar.latency.change_factor", res.LatencyChangeFactor},
		{"pg_regression_radar.external_cause_suspected", res.ExternalCauseSuspected},
		{"pg_regression_radar.auto_abort.triggered", res.AutoAbortTriggered},
	}
	if res.QueryText != "" {
		a = append(a, attr{"db.query.text", res.QueryText})
	}
	if res.DeployEventID != "" {
		a = append(a, attr{"pg_regression_radar.deploy_event_id", res.DeployEventID})
	}
	if !res.DetectedChangeAt.IsZero() {
		a = append(a, attr{"pg_regression_radar.detected_change_at", res.DetectedChangeAt.UTC().Format(time.RFC3339Nano)})
	}
	if res.AutoAbortError != "" {
		a = append(a, attr{"pg_regression_radar.auto_abort.error", res.AutoAbortError})
	}
	if res.PlanDiffSummary != "" {
		a = append(a, attr{"pg_regression_radar.plan_diff_summary", res.PlanDiffSummary})
	}
	rec.attrs = a

	if tr, sp, ok := otlpdeploy.ParseEventID(res.DeployEventID); ok {
		rec.traceID, rec.spanID = tr, sp
	}
	return rec
}

func sortedAttrs(m map[string]string) []attr {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]attr, 0, len(keys))
	for _, k := range keys {
		out = append(out, attr{k, m[k]})
	}
	return out
}

// ----- protobuf -----

func appendBytesField(b []byte, num protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendStringField(b []byte, num protowire.Number, s string) []byte {
	return appendBytesField(b, num, []byte(s))
}

func pbAnyValue(v any) []byte {
	var b []byte
	switch x := v.(type) {
	case string:
		b = appendStringField(b, 1, x)
	case bool:
		b = protowire.AppendTag(b, 2, protowire.VarintType)
		b = protowire.AppendVarint(b, protowire.EncodeBool(x))
	case int64:
		b = protowire.AppendTag(b, 3, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(x))
	case float64:
		b = protowire.AppendTag(b, 4, protowire.Fixed64Type)
		b = protowire.AppendFixed64(b, math.Float64bits(x))
	default:
		b = appendStringField(b, 1, fmt.Sprint(x))
	}
	return b
}

func pbKeyValue(a attr) []byte {
	b := appendStringField(nil, 1, a.key)
	return appendBytesField(b, 2, pbAnyValue(a.val))
}

func pbAttrs(b []byte, num protowire.Number, attrs []attr) []byte {
	for _, a := range attrs {
		b = appendBytesField(b, num, pbKeyValue(a))
	}
	return b
}

func encodeProtobuf(resource []attr, version string, rec logRecord) []byte {
	var lr []byte
	lr = protowire.AppendTag(lr, 1, protowire.Fixed64Type)
	lr = protowire.AppendFixed64(lr, rec.timeNanos)
	lr = protowire.AppendTag(lr, 2, protowire.VarintType)
	lr = protowire.AppendVarint(lr, severityWarn)
	lr = appendStringField(lr, 3, "WARN")
	lr = appendBytesField(lr, 5, pbAnyValue(rec.body))
	lr = pbAttrs(lr, 6, rec.attrs)
	if len(rec.traceID) == 16 && len(rec.spanID) == 8 {
		lr = appendBytesField(lr, 9, rec.traceID)
		lr = appendBytesField(lr, 10, rec.spanID)
	}
	lr = protowire.AppendTag(lr, 11, protowire.Fixed64Type)
	lr = protowire.AppendFixed64(lr, rec.observedNanos)
	lr = appendStringField(lr, 12, EventName)

	scope := appendStringField(nil, 1, scopeName)
	scope = appendStringField(scope, 2, version)

	scopeLogs := appendBytesField(nil, 1, scope)
	scopeLogs = appendBytesField(scopeLogs, 2, lr)

	res := pbAttrs(nil, 1, resource)

	resourceLogs := appendBytesField(nil, 1, res)
	resourceLogs = appendBytesField(resourceLogs, 2, scopeLogs)

	return appendBytesField(nil, 1, resourceLogs) // ExportLogsServiceRequest.resource_logs
}

// ----- JSON -----

func jsonAnyValue(v any) map[string]any {
	switch x := v.(type) {
	case string:
		return map[string]any{"stringValue": x}
	case bool:
		return map[string]any{"boolValue": x}
	case int64:
		// int64 is a decimal string in proto3 JSON.
		return map[string]any{"intValue": strconv.FormatInt(x, 10)}
	case float64:
		return map[string]any{"doubleValue": x}
	default:
		return map[string]any{"stringValue": fmt.Sprint(x)}
	}
}

func jsonAttrs(attrs []attr) []map[string]any {
	out := make([]map[string]any, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, map[string]any{"key": a.key, "value": jsonAnyValue(a.val)})
	}
	return out
}

func encodeJSON(resource []attr, version string, rec logRecord) ([]byte, error) {
	lr := map[string]any{
		"timeUnixNano":         strconv.FormatUint(rec.timeNanos, 10),
		"observedTimeUnixNano": strconv.FormatUint(rec.observedNanos, 10),
		"severityNumber":       severityWarn,
		"severityText":         "WARN",
		"body":                 jsonAnyValue(rec.body),
		"attributes":           jsonAttrs(rec.attrs),
		"eventName":            EventName,
	}
	if len(rec.traceID) == 16 && len(rec.spanID) == 8 {
		lr["traceId"] = hex.EncodeToString(rec.traceID)
		lr["spanId"] = hex.EncodeToString(rec.spanID)
	}
	req := map[string]any{"resourceLogs": []any{map[string]any{
		"resource": map[string]any{"attributes": jsonAttrs(resource)},
		"scopeLogs": []any{map[string]any{
			"scope":      map[string]any{"name": scopeName, "version": version},
			"logRecords": []any{lr},
		}},
	}}}
	return json.Marshal(req)
}
