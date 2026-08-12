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
	// +optional
	DSNSecretRef *SecretKeySelector `json:"dsnSecretRef,omitempty"`

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
	// +optional
	SlackWebhookURL string `json:"slackWebhookUrl,omitempty"`
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
