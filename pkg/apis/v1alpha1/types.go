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

// Package v1alpha1 contains pg-regression-radar CRD type definitions.
package v1alpha1

import (
	"time"
)

// PostgresWatch declares which Postgres cluster to monitor and sensitivity settings.
type PostgresWatch struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// ClusterName identifies the CloudNativePG Cluster resource being watched.
	ClusterName string `json:"clusterName"`
	DSN         string `json:"dsn"`
	// ScrapeIntervalSeconds — lower values increase detection granularity at the cost of DB load.
	ScrapeIntervalSeconds int `json:"scrapeIntervalSeconds,omitempty"`
	// WindowMinutes — wider windows smooth noise but delay detection.
	WindowMinutes int `json:"windowMinutes,omitempty"`
	// MinSamples is the minimum number of latency observations per window.
	MinSamples int64 `json:"minSamples,omitempty"`
	// MinExecutions is the minimum number of executions per window, summed
	// over the observations.
	MinExecutions int64 `json:"minExecutions,omitempty"`
	// LatencyChangeThreshold filters out noise; raise it in high-churn environments.
	LatencyChangeThreshold float64 `json:"latencyChangeThreshold,omitempty"`
	// CriticalQueryIDs bypass MinSamples and MinExecutions so SLA-critical queries are always checked.
	CriticalQueryIDs []int64 `json:"criticalQueryIDs,omitempty"`
}

// DeploySource describes where deploy events come from and how they map to a
// PostgresWatch.
type DeploySource struct {
	Name             string `json:"name"`
	Namespace        string `json:"namespace"`
	PostgresWatchRef string `json:"postgresWatchRef"`
	// SourceType drives payload normalisation in the ingester.
	SourceType string `json:"sourceType"`
	// AppName narrows correlation to a single application; empty means all apps.
	AppName string `json:"appName,omitempty"`
	// ClusterName identifies the Kubernetes cluster this ingester instance is
	// running against, as configured by the operator running it. It is used as
	// the DeployEvent.Cluster fallback when the webhook payload itself doesn't
	// carry destination-cluster identity (e.g. Argo Rollouts, or Flux without
	// eventMetadata configured).
	ClusterName string `json:"clusterName,omitempty"`
	// WebhookSecret is the shared secret used to authenticate incoming webhook
	// requests via the X-Webhook-Token header. When non-empty, every POST to
	// the /webhook endpoint must include this value in the X-Webhook-Token
	// header; requests without it or with a wrong value are rejected with 401.
	// Leave empty to disable authentication (not recommended for
	// internet-facing deployments).
	WebhookSecret string `json:"webhookSecret,omitempty"`
	// OTLP configures how spans are recognised as deploys and mapped to
	// DeployEvent fields. Only read when SourceType is "otlp".
	OTLP *OTLPSpanMapping `json:"otlp,omitempty"`
}

// OTLPSpanMapping selects which OTLP spans represent a finished deploy and
// which span/resource attributes populate the resulting DeployEvent. At
// least one of SpanNames or MatchAttributes must be set so that an OTLP
// endpoint receiving a full trace stream never turns every span into a
// deploy event. Every attribute-name field is optional; the defaults follow
// OpenTelemetry semantic conventions and are documented in
// docs/otlp-deploy-events.md.
type OTLPSpanMapping struct {
	// SpanNames lists span names (exact match) that represent a deploy.
	SpanNames []string `json:"spanNames,omitempty"`
	// MatchAttributes lists attribute key/value pairs that must all be
	// present with exactly these values on the span, or on its resource, for
	// the span to represent a deploy. Span attributes take precedence.
	MatchAttributes map[string]string `json:"matchAttributes,omitempty"`
	// IncludeFailedSpans also accepts spans whose status is ERROR. Off by
	// default: a failed deploy did not change what the database runs.
	IncludeFailedSpans bool `json:"includeFailedSpans,omitempty"`
	// AppAttribute names the attribute holding the application name.
	// Default: service.name.
	AppAttribute string `json:"appAttribute,omitempty"`
	// NamespaceAttribute names the attribute holding the Kubernetes
	// namespace. Default: k8s.namespace.name.
	NamespaceAttribute string `json:"namespaceAttribute,omitempty"`
	// ClusterAttribute names the attribute holding the destination cluster.
	// Default: k8s.cluster.name.
	ClusterAttribute string `json:"clusterAttribute,omitempty"`
	// RevisionAttribute names the attribute holding the deployed revision.
	// Default: vcs.ref.head.revision, falling back to service.version.
	RevisionAttribute string `json:"revisionAttribute,omitempty"`
	// ImageTagAttribute names the attribute holding the image tag. Default:
	// container.image.tags (first element), falling back to
	// container.image.tag.
	ImageTagAttribute string `json:"imageTagAttribute,omitempty"`
	// Timestamp selects which span timestamp becomes DeployEvent.Timestamp:
	// "end" (default, the moment the deploy finished) or "start".
	Timestamp string `json:"timestamp,omitempty"`
}

// DeployEvent is a normalised deploy event ingested from any supported source.
type DeployEvent struct {
	ID string `json:"id"`
	// Source links back to the DeploySource for multi-source deployments.
	Source    string    `json:"source"`
	App       string    `json:"app"`
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace"`
	Revision  string    `json:"revision"`
	ImageTag  string    `json:"imageTag,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// PerformanceRegressionStatus reflects the outcome of the correlation analysis.
type PerformanceRegressionStatus string

const (
	StatusDetected         PerformanceRegressionStatus = "Detected"
	StatusNoRegression     PerformanceRegressionStatus = "NoRegression"
	StatusInsufficientData PerformanceRegressionStatus = "InsufficientData"
)

// TriggerType identifies which analysis path produced a PerformanceRegression.
type TriggerType string

const (
	// TriggerTypeDeploy means correlation.Engine.Analyse ran because a
	// tracked DeployEvent arrived; DeployEventID identifies which one.
	TriggerTypeDeploy TriggerType = "deploy"
	// TriggerTypePeriodic means correlation.Engine.AnalysePeriodic ran on
	// its own rolling schedule, with no DeployEvent involved at all —
	// DeployEventID is empty for these. See docs/periodic-detection.md.
	TriggerTypePeriodic TriggerType = "periodic"
)

// PerformanceRegression is created by the Correlation Engine when it analyses a deploy.
type PerformanceRegression struct {
	Name          string                      `json:"name"`
	Namespace     string                      `json:"namespace"`
	DeployEventID string                      `json:"deployEventId"`
	QueryID       int64                       `json:"queryId"`
	QueryText     string                      `json:"queryText"`
	Status        PerformanceRegressionStatus `json:"status"`
	// ConfidenceScore is 1 - PValue. It is a significance score, not the
	// probability that the regression is real or that the deploy caused it;
	// see docs/detection-algorithm.md. Zero when the significance test did not run.
	ConfidenceScore float64 `json:"confidenceScore"`
	// PValue is the two-sided Welch's t-test p-value comparing the latency
	// segments on either side of the located change point. It is nil when the
	// test did not run (insufficient data, or no change point found), which is
	// different from a p-value of zero. Because the change point is chosen from
	// the same data the test then uses, it is optimistic: see
	// docs/detection-algorithm.md.
	PValue              *float64 `json:"pValue,omitempty"`
	MeanLatencyBefore   float64  `json:"meanLatencyBefore"`
	MeanLatencyAfter    float64  `json:"meanLatencyAfter"`
	LatencyChangeFactor float64  `json:"latencyChangeFactor"`
	// ExternalCauseSuspected hints that CPU/IO also shifted, so the deploy may not be the sole cause.
	ExternalCauseSuspected bool `json:"externalCauseSuspected,omitempty"`
	// AutoAbortTriggered is true if the owning PostgresWatch had auto-abort
	// enabled and this regression's confidence cleared its threshold, so an
	// abort of the originating Argo Rollouts canary was attempted. Does not
	// by itself mean the abort succeeded; see AutoAbortError.
	AutoAbortTriggered bool `json:"autoAbortTriggered,omitempty"`
	// AutoAbortError carries the abort attempt's error, if any. Empty when
	// AutoAbortTriggered is false or the attempt succeeded.
	AutoAbortError string `json:"autoAbortError,omitempty"`
	// TriggerType identifies which analysis path produced this regression:
	// TriggerTypeDeploy (DeployEventID is populated) or TriggerTypePeriodic
	// (DeployEventID is empty — no deploy was involved). Empty only for
	// values constructed before this field existed; treat that the same as
	// TriggerTypeDeploy, since deploy-triggered analysis is what this
	// project originally, and still by default, does.
	TriggerType TriggerType `json:"triggerType,omitempty"`
	// DetectedChangeAt is the timestamp of the change point the E-divisive
	// stage actually located in the query latency series. It may differ
	// slightly from DeployEventID's deploy timestamp (within the engine's
	// configured tolerance) since rollouts, connection draining and scrape
	// lag delay the observable effect of a deploy. Zero unless Status is
	// Detected.
	DetectedChangeAt time.Time `json:"detectedChangeAt,omitempty"`
	// PlanDiffSummary is a short, human-readable description of how this
	// query's EXPLAIN (GENERIC_PLAN) plan changed around DetectedChangeAt
	// (see internal/planner.Diff and docs/detection-algorithm.md's
	// "Plan-diff correlation" section). Only populated when the operator
	// enabled --capture-plans and the server is PostgreSQL 16+; empty
	// otherwise, including for every Status other than Detected.
	PlanDiffSummary string    `json:"planDiffSummary,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}
