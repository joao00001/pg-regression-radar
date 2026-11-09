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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/telemetry/otlpexport"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// repeatedFlag is a flag.Value that accumulates every occurrence.
type repeatedFlag []string

func (f *repeatedFlag) String() string { return strings.Join(*f, ",") }

func (f *repeatedFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// otlpDeployFlags are the --otlp-* flags that configure --source-type=otlp.
type otlpDeployFlags struct {
	spanNames      repeatedFlag
	matchAttrs     repeatedFlag
	includeFailed  *bool
	appAttr        *string
	namespaceAttr  *string
	clusterAttr    *string
	revisionAttr   *string
	imageTagAttr   *string
	timestampField *string
}

func registerOTLPDeployFlags(fs *flag.FlagSet) *otlpDeployFlags {
	f := &otlpDeployFlags{}
	fs.Var(&f.spanNames, "otlp-span-name", "With --source-type=otlp: span name that represents a deploy (exact match; repeatable). At least one of --otlp-span-name/--otlp-match-attribute is required.")
	fs.Var(&f.matchAttrs, "otlp-match-attribute", "With --source-type=otlp: key=value that a span (or its resource) must carry to count as a deploy (repeatable; all must match)")
	f.includeFailed = fs.Bool("otlp-include-failed", false, "With --source-type=otlp: also accept spans whose status is ERROR")
	f.appAttr = fs.String("otlp-app-attribute", "", "With --source-type=otlp: attribute holding the application name (default service.name)")
	f.namespaceAttr = fs.String("otlp-namespace-attribute", "", "With --source-type=otlp: attribute holding the Kubernetes namespace (default k8s.namespace.name)")
	f.clusterAttr = fs.String("otlp-cluster-attribute", "", "With --source-type=otlp: attribute holding the cluster (default k8s.cluster.name)")
	f.revisionAttr = fs.String("otlp-revision-attribute", "", "With --source-type=otlp: attribute holding the revision (default vcs.ref.head.revision, then service.version)")
	f.imageTagAttr = fs.String("otlp-image-tag-attribute", "", "With --source-type=otlp: attribute holding the image tag (default container.image.tags, then container.image.tag)")
	f.timestampField = fs.String("otlp-timestamp", "", "With --source-type=otlp: span timestamp used as the deploy time: end (default) or start")
	return f
}

// mapping returns the span mapping for --source-type=otlp, or nil for any
// other source type.
func (f *otlpDeployFlags) mapping(sourceType string) (*v1alpha1.OTLPSpanMapping, error) {
	if sourceType != "otlp" {
		if len(f.spanNames) > 0 || len(f.matchAttrs) > 0 {
			return nil, fmt.Errorf("--otlp-span-name/--otlp-match-attribute only apply with --source-type=otlp (got %q)", sourceType)
		}
		return nil, nil
	}
	attrs := map[string]string{}
	for _, kv := range f.matchAttrs {
		k, v, ok := strings.Cut(kv, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("--otlp-match-attribute %q: want key=value", kv)
		}
		if prev, dup := attrs[k]; dup && prev != v {
			return nil, fmt.Errorf("--otlp-match-attribute: %q given with conflicting values", k)
		}
		attrs[k] = v
	}
	if len(attrs) == 0 {
		attrs = nil
	}
	return &v1alpha1.OTLPSpanMapping{
		SpanNames:          append([]string(nil), f.spanNames...),
		MatchAttributes:    attrs,
		IncludeFailedSpans: *f.includeFailed,
		AppAttribute:       *f.appAttr,
		NamespaceAttribute: *f.namespaceAttr,
		ClusterAttribute:   *f.clusterAttr,
		RevisionAttribute:  *f.revisionAttr,
		ImageTagAttribute:  *f.imageTagAttr,
		Timestamp:          *f.timestampField,
	}, nil
}

// registerWebhookRoutes mounts handler on /webhook and, for the OTLP source,
// on /webhook/v1/traces too: an OTLP/HTTP exporter appends /v1/traces to
// the endpoint it is given, so "endpoint: http://host:8080/webhook" works
// without a custom path override.
func registerWebhookRoutes(mux *http.ServeMux, handler http.Handler, sourceType string) {
	mux.Handle("/webhook", handler)
	if sourceType == "otlp" {
		mux.Handle("/webhook/v1/traces", handler)
	}
}

// otlpExportFlags are the --otlp-endpoint family of flags that export each
// detected regression as an OTLP log record.
type otlpExportFlags struct {
	endpoint *string
	headers  *string
	encoding *string
	gzip     *bool
	timeout  *time.Duration
}

func registerOTLPExportFlags(fs *flag.FlagSet) *otlpExportFlags {
	return &otlpExportFlags{
		endpoint: fs.String("otlp-endpoint", "", "Export every detected regression as an OTLP log record to this OTLP/HTTP endpoint (e.g. http://otel-collector:4318; /v1/logs is appended). Empty disables the export — see docs/otlp-export.md"),
		headers:  fs.String("otlp-headers", "", "Headers for --otlp-endpoint as k=v,k2=v2. Falls back to OTEL_EXPORTER_OTLP_LOGS_HEADERS then OTEL_EXPORTER_OTLP_HEADERS, which keeps credentials out of the process list"),
		encoding: fs.String("otlp-encoding", "protobuf", "OTLP payload encoding for --otlp-endpoint: protobuf or json"),
		gzip:     fs.Bool("otlp-gzip", false, "Gzip-compress OTLP export request bodies"),
		timeout:  fs.Duration("otlp-timeout", 10*time.Second, "Per-attempt timeout for OTLP export requests"),
	}
}

// config validates the flags and returns the exporter configuration, or
// (zero, false, nil) when export is not enabled. allowed and policy are the
// same alerting destination controls the alert URL is held to; loopback
// endpoints are deliberately accepted when no allowlist is set because a
// collector sidecar on localhost is the common standalone deployment.
func (f *otlpExportFlags) config(allowed []string, policy alerting.DestinationPolicy, resource map[string]string, logger *slog.Logger, reg prometheus.Registerer) (otlpexport.Config, bool, error) {
	if *f.endpoint == "" {
		return otlpexport.Config{}, false, nil
	}
	if policy == alerting.DestinationPolicyRelayOnly {
		return otlpexport.Config{}, false, fmt.Errorf("--otlp-endpoint is not allowed under the %q alerting destination policy", policy)
	}
	endpoint, err := otlpexport.NormalizeEndpoint(*f.endpoint)
	if err != nil {
		return otlpexport.Config{}, false, fmt.Errorf("--otlp-endpoint: %w", err)
	}
	if len(allowed) > 0 {
		if err := alerting.ValidateOutboundURL(endpoint, allowed); err != nil {
			return otlpexport.Config{}, false, fmt.Errorf("--otlp-endpoint: %w", err)
		}
	}
	raw := *f.headers
	if raw == "" {
		raw = firstNonEmptyEnv("OTEL_EXPORTER_OTLP_LOGS_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS")
	}
	headers, err := otlpexport.ParseHeaders(raw)
	if err != nil {
		return otlpexport.Config{}, false, fmt.Errorf("--otlp-headers: %w", err)
	}
	enc := otlpexport.Encoding(*f.encoding)
	if enc != otlpexport.EncodingProtobuf && enc != otlpexport.EncodingJSON {
		return otlpexport.Config{}, false, fmt.Errorf("--otlp-encoding must be protobuf or json, got %q", *f.encoding)
	}
	if *f.timeout <= 0 {
		return otlpexport.Config{}, false, fmt.Errorf("--otlp-timeout must be positive, got %s", *f.timeout)
	}
	return otlpexport.Config{
		Endpoint:           endpoint,
		Encoding:           enc,
		Headers:            headers,
		Timeout:            *f.timeout,
		Gzip:               *f.gzip,
		ServiceVersion:     buildinfo.Version,
		ResourceAttributes: resource,
		Logger:             logger,
		Registerer:         reg,
	}, true, nil
}

func firstNonEmptyEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// otlpResourceAttributes drops unset values from the Resource attributes
// identifying the monitored cluster.
func otlpResourceAttributes(kv map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range kv {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
