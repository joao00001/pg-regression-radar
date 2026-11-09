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

// Package otlpdeploy turns OpenTelemetry trace data into DeployEvents, so a
// deploy that is already instrumented as an OTel span (a CD pipeline step,
// an ArgoCD/Flux sync span emitted by a bridge, a custom release script)
// can feed the Correlation Engine through the same ingester.Store path the
// webhook DeploySources use — no per-tool webhook template required.
//
// It understands the two OTLP/HTTP trace encodings (binary protobuf and
// JSON) at the wire level and nothing else: no gRPC, no SDK. See
// docs/otlp-deploy-events.md for the mapping rules and an end-to-end
// OpenTelemetry Collector example.
package otlpdeploy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Format is an OTLP/HTTP payload encoding.
type Format int

const (
	// FormatProtobuf is Content-Type application/x-protobuf.
	FormatProtobuf Format = iota
	// FormatJSON is Content-Type application/json.
	FormatJSON
)

// ContentType returns the canonical Content-Type for f.
func (f Format) ContentType() string {
	if f == FormatJSON {
		return "application/json"
	}
	return "application/x-protobuf"
}

// FormatFromContentType maps an HTTP Content-Type header to a Format.
// ok is false for anything that isn't an OTLP/HTTP encoding.
func FormatFromContentType(contentType string) (f Format, ok bool) {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(mt) {
	case "application/x-protobuf", "application/protobuf":
		return FormatProtobuf, true
	case "application/json":
		return FormatJSON, true
	}
	return 0, false
}

// Decode parses a decompressed ExportTraceServiceRequest body.
func Decode(body []byte, f Format) ([]Span, error) {
	if f == FormatJSON {
		return decodeJSON(body)
	}
	return decodeProto(body)
}

// Attribute-name defaults follow the OpenTelemetry semantic conventions.
const (
	defaultAppAttr       = "service.name"
	defaultNamespaceAttr = "k8s.namespace.name"
	defaultClusterAttr   = "k8s.cluster.name"
)

var (
	defaultRevisionAttrs = []string{"vcs.ref.head.revision", "service.version"}
	defaultImageTagAttrs = []string{"container.image.tags", "container.image.tag"}
)

// Matcher decides which spans are deploys and builds DeployEvents from
// them. It is immutable after NewMatcher and safe for concurrent use.
type Matcher struct {
	spanNames         map[string]struct{}
	matchAttrs        map[string]string
	includeFailed     bool
	appAttr           string
	namespaceAttr     string
	clusterAttr       string
	revisionAttrs     []string
	imageTagAttrs     []string
	useStartTimestamp bool
}

// ValidateMapping reports whether m is a usable mapping.
func ValidateMapping(m v1alpha1.OTLPSpanMapping) error {
	_, err := NewMatcher(m)
	return err
}

// NewMatcher validates m and resolves its defaults. At least one of
// SpanNames or MatchAttributes is required: with neither, every span an
// OTLP sender emits would become a deploy event.
func NewMatcher(m v1alpha1.OTLPSpanMapping) (*Matcher, error) {
	if len(m.SpanNames) == 0 && len(m.MatchAttributes) == 0 {
		return nil, errors.New("otlpdeploy: mapping needs spanNames and/or matchAttributes, otherwise every span would count as a deploy")
	}
	mt := &Matcher{
		spanNames:     make(map[string]struct{}, len(m.SpanNames)),
		matchAttrs:    make(map[string]string, len(m.MatchAttributes)),
		includeFailed: m.IncludeFailedSpans,
		appAttr:       defaultAppAttr,
		namespaceAttr: defaultNamespaceAttr,
		clusterAttr:   defaultClusterAttr,
		revisionAttrs: defaultRevisionAttrs,
		imageTagAttrs: defaultImageTagAttrs,
	}
	for _, n := range m.SpanNames {
		if n == "" {
			return nil, errors.New("otlpdeploy: spanNames must not contain an empty name")
		}
		mt.spanNames[n] = struct{}{}
	}
	for k, v := range m.MatchAttributes {
		if k == "" {
			return nil, errors.New("otlpdeploy: matchAttributes must not contain an empty key")
		}
		mt.matchAttrs[k] = v
	}
	if m.AppAttribute != "" {
		mt.appAttr = m.AppAttribute
	}
	if m.NamespaceAttribute != "" {
		mt.namespaceAttr = m.NamespaceAttribute
	}
	if m.ClusterAttribute != "" {
		mt.clusterAttr = m.ClusterAttribute
	}
	if m.RevisionAttribute != "" {
		mt.revisionAttrs = []string{m.RevisionAttribute}
	}
	if m.ImageTagAttribute != "" {
		mt.imageTagAttrs = []string{m.ImageTagAttribute}
	}
	switch m.Timestamp {
	case "", "end":
	case "start":
		mt.useStartTimestamp = true
	default:
		return nil, fmt.Errorf("otlpdeploy: timestamp must be \"end\" or \"start\", got %q", m.Timestamp)
	}
	return mt, nil
}

// lookup returns the value of key from span attributes, falling back to
// resource attributes.
func lookup(sp *Span, key string) (string, bool) {
	if v, ok := sp.Attrs[key]; ok {
		return v, true
	}
	v, ok := sp.ResourceAttrs[key]
	return v, ok
}

func lookupFirst(sp *Span, keys []string) string {
	for _, k := range keys {
		if v, ok := lookup(sp, k); ok && v != "" {
			return v
		}
	}
	return ""
}

func (m *Matcher) matches(sp *Span) bool {
	if len(m.spanNames) > 0 {
		if _, ok := m.spanNames[sp.Name]; !ok {
			return false
		}
	}
	for k, want := range m.matchAttrs {
		if got, ok := lookup(sp, k); !ok || got != want {
			return false
		}
	}
	return true
}

// Result summarises one Events call.
type Result struct {
	// Events are the DeployEvents built from matching spans.
	Events []v1alpha1.DeployEvent
	// Matched counts spans that looked like deploys and produced an event.
	Matched int
	// Rejected counts spans that matched the selector but could not become
	// an event (no application name). Surfaced to the sender through the
	// OTLP partial_success response.
	Rejected int
	// RejectReason describes the first rejection, for the response message.
	RejectReason string
}

// Events converts spans into DeployEvents. now bounds timestamps: a span
// ending in the future (sender clock skew) is clamped to now, since the
// deploy demonstrably happened no later than this request arrived.
func (m *Matcher) Events(spans []Span, now time.Time) Result {
	var res Result
	for i := range spans {
		sp := &spans[i]
		if !m.matches(sp) {
			continue
		}
		if sp.StatusCode == statusCodeError && !m.includeFailed {
			continue
		}
		app, _ := lookup(sp, m.appAttr)
		if app == "" {
			res.Rejected++
			if res.RejectReason == "" {
				res.RejectReason = fmt.Sprintf("span %q matched but has no %q attribute", sp.Name, m.appAttr)
			}
			continue
		}

		nanos := sp.EndNanos
		if m.useStartTimestamp || nanos == 0 {
			nanos = sp.StartNanos
		}
		ts := now
		if nanos != 0 {
			ts = time.Unix(0, int64(nanos)).UTC()
			if ts.After(now) {
				ts = now
			}
		}

		ns, _ := lookup(sp, m.namespaceAttr)
		cluster, _ := lookup(sp, m.clusterAttr)
		ev := v1alpha1.DeployEvent{
			App:       app,
			Cluster:   cluster,
			Namespace: ns,
			Revision:  lookupFirst(sp, m.revisionAttrs),
			ImageTag:  lookupFirst(sp, m.imageTagAttrs),
			Timestamp: ts,
		}
		ev.ID = eventIDFor(sp, ev)
		res.Events = append(res.Events, ev)
		res.Matched++
	}
	return res
}

const eventIDPrefix = "otlp-"

// EventID is the DeployEvent ID for a span: deterministic, so an OTLP
// exporter retrying a request is deduplicated by the Store's
// upsert-by-ID semantics, and reversible via ParseEventID so a
// PerformanceRegression can be linked back to the deploy span.
func EventID(traceID, spanID []byte) string {
	return eventIDPrefix + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(spanID)
}

func eventIDFor(sp *Span, ev v1alpha1.DeployEvent) string {
	if len(sp.TraceID) == 16 && len(sp.SpanID) == 8 {
		return EventID(sp.TraceID, sp.SpanID)
	}
	// A sender that omits IDs still gets a stable, retry-safe identity.
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%d", sp.Name, ev.App, ev.Namespace, ev.Revision, ev.Timestamp.UnixNano())))
	return eventIDPrefix + "h-" + hex.EncodeToString(h[:8])
}

// ParseEventID reverses EventID. ok is false for IDs that didn't come from
// an OTLP span carrying real trace/span IDs.
func ParseEventID(id string) (traceID, spanID []byte, ok bool) {
	rest, found := strings.CutPrefix(id, eventIDPrefix)
	if !found {
		return nil, nil, false
	}
	t, s, found := strings.Cut(rest, "-")
	if !found || len(t) != 32 || len(s) != 16 {
		return nil, nil, false
	}
	tb, err := hex.DecodeString(t)
	if err != nil {
		return nil, nil, false
	}
	sb, err := hex.DecodeString(s)
	if err != nil {
		return nil, nil, false
	}
	return tb, sb, true
}

// EncodeResponse builds the OTLP/HTTP ExportTraceServiceResponse body. Per
// the OTLP spec a fully accepted request yields an empty message; a
// partially rejected one carries partial_success so the sender can log it
// instead of retrying a request that will never succeed.
func EncodeResponse(f Format, rejected int, message string) []byte {
	if f == FormatJSON {
		if rejected == 0 {
			return []byte("{}")
		}
		msg, _ := json.Marshal(message)
		return []byte(fmt.Sprintf(`{"partialSuccess":{"rejectedSpans":"%d","errorMessage":%s}}`, rejected, msg))
	}
	if rejected == 0 {
		return nil
	}
	var ps []byte
	ps = protowire.AppendTag(ps, 1, protowire.VarintType)
	ps = protowire.AppendVarint(ps, uint64(rejected))
	ps = protowire.AppendTag(ps, 2, protowire.BytesType)
	ps = protowire.AppendString(ps, message)
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.BytesType)
	return protowire.AppendBytes(out, ps)
}
