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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresWatchSpec declares which Postgres cluster to monitor, how
// aggressively to sample it, and the thresholds that decide whether a
// latency shift counts as a regression. It is consumed by
// PostgresWatchReconciler, which translates it into a
// internal/collector.Config and internal/correlation.Config pair.
type PostgresWatchSpec struct {
	// clusterName identifies the CloudNativePG (or other) Cluster resource
	// being watched. It is attached to Prometheus metrics and to any
	// PerformanceRegression created from this watch.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClusterName string `json:"clusterName"`

	// dsn is the Postgres connection string used by the Collector.
	// For production use, prefer dsnSecretRef so the credential is not
	// stored in the CR itself; dsn takes precedence when both are set.
	// +optional
	DSN string `json:"dsn,omitempty"`

	// dsnSecretRef points at a Secret key holding the Postgres DSN, keeping
	// credentials out of the CR spec (and out of `kubectl get -o yaml`).
	// By default this Secret is read from the same Kubernetes cluster the
	// manager runs in (the "hub"). Set remoteClusterSecretRef to read it
	// from a different ("spoke") cluster instead.
	// +optional
	DSNSecretRef *SecretKeySelector `json:"dsnSecretRef,omitempty"`

	// remoteClusterSecretRef points at a Secret, in the hub cluster and in
	// this PostgresWatch's namespace, whose key holds a kubeconfig (raw
	// YAML or JSON, the same format `kubectl config view --raw` produces)
	// for a remote ("spoke") Kubernetes cluster. When set, dsnSecretRef is
	// resolved against that remote cluster instead of the hub — the
	// pattern fleet tools like Cluster API, Argo CD, and Open Cluster
	// Management use to let a central controller reach resources living in
	// a different cluster. See docs/multi-cluster.md for the full model,
	// including the least-privilege expectation on the kubeconfig itself:
	// it should grant nothing beyond reading the one Secret dsnSecretRef
	// names, in the remote cluster.
	//
	// Leave unset (the default) when the Postgres cluster being watched is
	// reachable over the network from the manager and its DSN Secret lives
	// in this same (hub) cluster — the common case when CloudNativePG runs
	// alongside the manager. This field only changes *where the DSN Secret
	// is read from*; it has no effect when dsn is set directly instead of
	// dsnSecretRef.
	// +optional
	RemoteClusterSecretRef *SecretKeySelector `json:"remoteClusterSecretRef,omitempty"`

	// remoteNamespace overrides which namespace dsnSecretRef is looked up
	// in on the remote cluster when remoteClusterSecretRef is set. Only
	// meaningful alongside remoteClusterSecretRef; ignored otherwise.
	//
	// Leave unset (the default) when the spoke cluster's namespace for
	// this workload has the same name as this PostgresWatch's own
	// namespace in the hub cluster — the convention CloudNativePG's own
	// generated credential Secrets follow, and the common case for a
	// fleet with matching hub/spoke namespace naming. Set it when your
	// fleet's naming conventions don't line up 1:1 between hub and spoke
	// (e.g. hub namespace "prod-eu-west" but the spoke cluster's own
	// CloudNativePG Cluster lives in a namespace simply called
	// "postgres"). See docs/multi-cluster.md.
	// +optional
	RemoteNamespace string `json:"remoteNamespace,omitempty"`

	// scrapeIntervalSeconds controls how often pg_stat_statements is
	// scraped. Lower values increase detection granularity at the cost of
	// DB load.
	// +optional
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=5
	ScrapeIntervalSeconds int32 `json:"scrapeIntervalSeconds,omitempty"`

	// windowMinutes is the analysis window on each side of a deploy. Wider
	// windows smooth noise but delay detection.
	// +optional
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	WindowMinutes int32 `json:"windowMinutes,omitempty"`

	// minExecutions guards against false positives on rarely-called
	// queries; both the pre- and post-deploy windows must meet this floor.
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	MinExecutions int64 `json:"minExecutions,omitempty"`

	// latencyChangeThreshold is the minimum relative mean-latency increase
	// (e.g. 0.20 == +20%) required before the statistical test even runs.
	// Raise it in high-churn environments to cut noise.
	// +optional
	// +kubebuilder:default="0.20"
	LatencyChangeThreshold string `json:"latencyChangeThreshold,omitempty"`

	// pValueThreshold is the Welch's t-test significance cutoff. Lower
	// values increase specificity at the cost of sensitivity.
	// +optional
	// +kubebuilder:default="0.05"
	PValueThreshold string `json:"pValueThreshold,omitempty"`

	// criticalQueryIDs bypass minExecutions so SLA-critical queries are
	// always evaluated even when rarely called.
	// +optional
	CriticalQueryIDs []int64 `json:"criticalQueryIDs,omitempty"`

	// slackWebhookURL is a Slack (or Slack-compatible) incoming webhook
	// used by internal/alerting to notify on detected regressions.
	//
	// Deprecated: use alerting instead (leaving format unset, or set to
	// "slack", is equivalent). Ignored entirely whenever alerting is set —
	// kept only so watches created before alerting existed keep working
	// unchanged.
	// +optional
	SlackWebhookURL string `json:"slackWebhookUrl,omitempty"`

	// alerting configures how detected regressions are reported and
	// supersedes slackWebhookUrl. Leave unset to keep using
	// slackWebhookUrl (or no alerting at all, if that's empty too) exactly
	// as before this field existed. See docs/alerting.md for the full list
	// of supported formats and the custom template's available fields.
	// +optional
	Alerting *AlertingConfig `json:"alerting,omitempty"`

	// capturePlans enables plan-diff correlation for this watch: around a
	// detected regression, the Collector captures the query's execution
	// plan from pg_store_plans (if installed) or falls back to
	// EXPLAIN (FORMAT JSON, GENERIC_PLAN) (PostgreSQL 16+), and a short
	// diff is attached to the resulting PerformanceRegression's
	// status.planDiffSummary. See internal/planner and
	// docs/detection-algorithm.md#plan-diff-correlation-optional. Mirrors
	// the standalone `operator` CLI's --capture-plans flag; disabled by
	// default because it adds a per-scrape-cycle EXPLAIN/lookup cost.
	// +optional
	CapturePlans bool `json:"capturePlans,omitempty"`

	// autoAbort optionally closes the loop between detection and
	// remediation: when set and enabled, a sufficiently confident detected
	// regression automatically aborts the Argo Rollouts canary that caused
	// it, instead of only alerting and waiting for a human to do it. See
	// docs/auto-abort.md for the full safety model. nil (the default) means
	// disabled -- this manager never touches any Rollout object unless a
	// PostgresWatch explicitly opts in.
	// +optional
	AutoAbort *AutoAbortConfig `json:"autoAbort,omitempty"`

	// periodicDetection optionally runs regression detection on a rolling
	// schedule, independent of any tracked deploy: no DeploySource or
	// DeployEvent is required for it to fire. See
	// docs/periodic-detection.md, including its false-positive caveat --
	// this is a materially different, less deploy-anchored kind of
	// detection than the rest of this project's default behaviour. nil
	// (the default) means disabled -- this watch behaves exactly as before
	// this field existed unless explicitly opted in.
	// +optional
	PeriodicDetection *PeriodicDetectionConfig `json:"periodicDetection,omitempty"`
}

// AutoAbortConfig controls automatic Argo Rollouts abortion. It only ever
// applies to a regression whose deploy event came from a DeploySource with
// spec.sourceType "argo-rollouts" -- deploys from argocd/flux/generic/
// kubernetes sources are never auto-aborted, since none of those has an
// equivalent "abort mid-rollout" primitive to call in this version. See
// docs/auto-abort.md.
type AutoAbortConfig struct {
	// enabled turns on automatic abortion for this watch. Every other field
	// below is ignored while this is false.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// confidenceThreshold is the minimum PerformanceRegression
	// confidenceScore (1 - Welch's t-test p-value) required before this
	// manager will abort a rollout automatically, on top of it already
	// having cleared pValueThreshold to be reported as Detected at all.
	// Deliberately conservative by default: auto-abort is meant to fire
	// only on the clearest possible signals, not on every detection that
	// merely clears the ordinary alerting bar.
	// +optional
	// +kubebuilder:default="0.99"
	ConfidenceThreshold string `json:"confidenceThreshold,omitempty"`
}

// AlertingConfig controls how a detected regression is reported: which
// payload layout to use, and where to send it. Introduced to replace the
// original Slack-only integration (see PostgresWatchSpec.SlackWebhookURL)
// with something that actually works with more than one on-call tool — see
// docs/alerting.md.
type AlertingConfig struct {
	// format selects the notification payload layout. Leave unset (or set
	// to "slack") for the original Slack-compatible incoming-webhook
	// payload.
	// +kubebuilder:validation:Enum=slack;teams;pagerduty;custom
	// +optional
	Format string `json:"format,omitempty"`

	// url is the webhook endpoint for the slack, teams, and custom
	// formats. Ignored for pagerduty, which always posts to PagerDuty's
	// fixed Events API v2 endpoint instead — see pagerDutyRoutingKey.
	// +optional
	URL string `json:"url,omitempty"`

	// pagerDutyRoutingKey is the PagerDuty Events API v2 integration
	// (routing) key. Required, and only used, when format is "pagerduty".
	// +optional
	PagerDutyRoutingKey string `json:"pagerDutyRoutingKey,omitempty"`

	// customTemplate is Go text/template source used to render the
	// notification body when format is "custom". Required, and only used,
	// when format is "custom" — see docs/alerting.md#custom-format for the
	// available template fields and a worked example.
	// +optional
	CustomTemplate string `json:"customTemplate,omitempty"`

	// destinationPolicy controls how the alerting destination URL is
	// validated and resolved. Three modes are supported:
	//
	// "permissive" (default): the URL is accepted as long as it passes the
	// static SSRF blocklist (no loopback, no link-local, no cloud-metadata
	// hostnames). This matches the pre-existing behaviour and is
	// backward-compatible with all existing installations.
	//
	// "allowlist": the URL's host must also appear in the
	// --alerting-allowed-destinations list configured at the operator or
	// manager level. Reconciliation fails with a clear message if the host
	// is not in the list. Use this in multi-tenant clusters where only a
	// pre-approved set of receivers should be reachable.
	//
	// "relay-only": the CRD-level URL is ignored entirely; the notifier
	// always sends to the relay URL supplied via
	// --alerting-destination-policy-relay-url. Reconciliation fails if
	// that flag is empty. Use this when you want a single, centrally
	// controlled egress point that individual watch owners cannot override.
	//
	// Leave unset to get "permissive" behaviour — no breaking change for
	// existing installations.
	// +kubebuilder:validation:Enum=permissive;allowlist;relay-only
	// +optional
	DestinationPolicy string `json:"destinationPolicy,omitempty"`
}

// PeriodicDetectionConfig controls deploy-independent regression detection:
// running the same E-divisive + Welch's t-test pipeline on a rolling
// schedule instead of only in response to a DeployEvent. See
// docs/periodic-detection.md and ADR-0001
// (docs/adr/0001-deploy-independent-regression-detection.md) for the full
// design rationale, including why the window defaults to double the
// deploy-triggered default and why suppression is a re-arm state machine
// rather than a fixed cooldown.
type PeriodicDetectionConfig struct {
	// enabled turns on periodic detection for this watch. Every other field
	// below is ignored while this is false.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// windowMinutes sizes the rolling window that gets split in half
	// (recent vs. previous) to look for a regression with no deploy event
	// to anchor to. Deliberately independent from (and, by default,
	// double) the deploy-triggered spec.windowMinutes default: periodic
	// analysis has no deploy nearby to narrow down where to look, so a
	// shorter window would be noisier.
	// +optional
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=1
	WindowMinutes int32 `json:"windowMinutes,omitempty"`

	// intervalMinutes is how often a full periodic analysis pass runs over
	// every tracked query.
	// +optional
	// +kubebuilder:default=15
	// +kubebuilder:validation:Minimum=1
	IntervalMinutes int32 `json:"intervalMinutes,omitempty"`
}

// SecretKeySelector selects a key of a Secret in the same namespace as the
// referencing PostgresWatch.
type SecretKeySelector struct {
	// name of the referenced Secret.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// key within the Secret's Data.
	// +kubebuilder:validation:Required
	Key string `json:"key"`
}

// PostgresWatchPhase summarises the lifecycle state of the background
// worker (Collector + Correlation Engine) owned by this watch.
type PostgresWatchPhase string

const (
	// PostgresWatchPhasePending means the reconciler has not yet started
	// (or has not yet succeeded in starting) the background worker.
	PostgresWatchPhasePending PostgresWatchPhase = "Pending"
	// PostgresWatchPhaseRunning means the Collector scrape loop and
	// Correlation Engine are active for this watch.
	PostgresWatchPhaseRunning PostgresWatchPhase = "Running"
	// PostgresWatchPhaseFailed means the worker could not be started
	// (e.g. bad DSN) and reconciliation will keep retrying.
	PostgresWatchPhaseFailed PostgresWatchPhase = "Failed"
)

// PostgresWatchStatus reflects the observed state of the background worker.
type PostgresWatchStatus struct {
	// phase is a high-level summary of the worker lifecycle state.
	// +optional
	Phase PostgresWatchPhase `json:"phase,omitempty"`

	// observedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// trackedQueryIDs is the number of distinct queryids the Collector has
	// observed since the worker last (re)started.
	// +optional
	TrackedQueryIDs int64 `json:"trackedQueryIds,omitempty"`

	// lastScrapeTime is the timestamp of the most recent successful
	// pg_stat_statements scrape reported by the Collector.
	// +optional
	LastScrapeTime *metav1.Time `json:"lastScrapeTime,omitempty"`

	// message carries a human-readable explanation, most useful when
	// phase is Failed.
	// +optional
	Message string `json:"message,omitempty"`

	// conditions represent the current state of the PostgresWatch resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pgw
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterName"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Tracked Queries",type=integer,JSONPath=".status.trackedQueryIds"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// PostgresWatch declares a Postgres cluster to monitor for query
// performance regressions correlated with Kubernetes deploys.
type PostgresWatch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec PostgresWatchSpec `json:"spec,omitempty"`
	// +optional
	Status PostgresWatchStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresWatchList contains a list of PostgresWatch.
type PostgresWatchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresWatch `json:"items"`
}
