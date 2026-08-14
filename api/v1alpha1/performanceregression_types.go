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

// PerformanceRegressionSpec records the immutable facts about the
// deploy/query pair that was analysed. It is set once at creation time by
// PostgresWatchReconciler and is not expected to change afterwards.
type PerformanceRegressionSpec struct {
	// postgresWatchRef is the name of the PostgresWatch (in the same
	// namespace) whose Correlation Engine produced this result.
	PostgresWatchRef string `json:"postgresWatchRef"`

	// clusterName is copied from the owning PostgresWatch for convenient
	// filtering with `kubectl get performanceregressions -l ...` style
	// selectors and for display without a second lookup.
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// deployEventID identifies the deploy event that triggered this
	// analysis (see internal/ingester's DeployEvent.ID). Empty when
	// triggerType is "periodic" -- a periodic analysis pass has no deploy
	// event to reference at all.
	//
	// Was a required field before triggerType existed; relaxed to optional
	// alongside it, since every pre-existing (deploy-triggered) regression
	// already sets this and is unaffected.
	// +optional
	DeployEventID string `json:"deployEventId,omitempty"`

	// queryID is the pg_stat_statements queryid analysed.
	QueryID int64 `json:"queryId"`

	// queryText is a truncated excerpt of the analysed query, for
	// human-readable display in `kubectl describe`.
	// +optional
	QueryText string `json:"queryText,omitempty"`

	// triggerType identifies which analysis path produced this regression:
	// "deploy" (deployEventID is populated) or "periodic" (deployEventID is
	// empty -- see docs/periodic-detection.md). Empty is treated as
	// "deploy", matching every regression created before this field
	// existed.
	// +kubebuilder:validation:Enum=deploy;periodic
	// +optional
	TriggerType RegressionTriggerType `json:"triggerType,omitempty"`
}

// RegressionTriggerType mirrors pkg/apis/v1alpha1.TriggerType so the CRD
// Spec uses the exact same vocabulary as the internal DTO the Correlation
// Engine returns.
type RegressionTriggerType string

const (
	RegressionTriggerTypeDeploy   RegressionTriggerType = "deploy"
	RegressionTriggerTypePeriodic RegressionTriggerType = "periodic"
)

// RegressionStatus mirrors pkg/apis/v1alpha1.PerformanceRegressionStatus so
// the CRD Status uses the exact same vocabulary as the internal DTO the
// Correlation Engine returns.
type RegressionStatus string

const (
	RegressionStatusDetected         RegressionStatus = "Detected"
	RegressionStatusNoRegression     RegressionStatus = "NoRegression"
	RegressionStatusInsufficientData RegressionStatus = "InsufficientData"
)

// PerformanceRegressionStatus reflects the outcome of the Correlation
// Engine's analysis for this deploy/query pair.
type PerformanceRegressionStatus struct {
	// status is the analysis outcome.
	// +optional
	Status RegressionStatus `json:"status,omitempty"`

	// confidenceScore is 1 - p-value from Welch's t-test; closer to 1 means
	// the observed latency shift is extremely unlikely to be random noise.
	// +optional
	ConfidenceScore string `json:"confidenceScore,omitempty"`

	// meanLatencyBeforeMs is the mean query latency in the pre-deploy
	// window, in milliseconds.
	// +optional
	MeanLatencyBeforeMs string `json:"meanLatencyBeforeMs,omitempty"`

	// meanLatencyAfterMs is the mean query latency in the post-deploy
	// window, in milliseconds.
	// +optional
	MeanLatencyAfterMs string `json:"meanLatencyAfterMs,omitempty"`

	// latencyChangeFactor is meanLatencyAfterMs / meanLatencyBeforeMs.
	// +optional
	LatencyChangeFactor string `json:"latencyChangeFactor,omitempty"`

	// externalCauseSuspected hints that CPU/IO also shifted around the
	// same time, so the deploy may not be the sole cause.
	// +optional
	ExternalCauseSuspected bool `json:"externalCauseSuspected,omitempty"`

	// autoAbortTriggered is true if the owning PostgresWatch had
	// spec.autoAbort.enabled set and this regression's confidence cleared
	// spec.autoAbort.confidenceThreshold, so this manager attempted to
	// abort the originating Argo Rollouts canary. It does not by itself
	// mean the abort succeeded -- see autoAbortError. Always false for a
	// regression whose deploy event didn't come from an argo-rollouts
	// DeploySource, regardless of confidence, since there is nothing to
	// abort. See docs/auto-abort.md.
	// +optional
	AutoAbortTriggered bool `json:"autoAbortTriggered,omitempty"`

	// autoAbortError carries the abort attempt's error, if autoAbortTriggered
	// is true and the attempt failed (e.g. the Rollout object no longer
	// exists, or RBAC wasn't granted). Empty when autoAbortTriggered is
	// false, or when the attempt succeeded.
	// +optional
	AutoAbortError string `json:"autoAbortError,omitempty"`

	// planDiffSummary is a short, human-readable description of how this
	// query's execution plan changed around the detected change point (see
	// internal/planner.Diff and
	// docs/detection-algorithm.md#plan-diff-correlation-optional). Only
	// populated when the owning PostgresWatch has spec.capturePlans set.
	// +optional
	PlanDiffSummary string `json:"planDiffSummary,omitempty"`

	// detectedAt is when the Correlation Engine produced this result.
	// +optional
	DetectedAt *metav1.Time `json:"detectedAt,omitempty"`

	// conditions represent the current state of the PerformanceRegression
	// resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pgreg
// +kubebuilder:printcolumn:name="Watch",type=string,JSONPath=".spec.postgresWatchRef"
// +kubebuilder:printcolumn:name="Query",type=integer,JSONPath=".spec.queryId"
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=".status.status"
// +kubebuilder:printcolumn:name="Confidence",type=string,JSONPath=".status.confidenceScore"
// +kubebuilder:printcolumn:name="Change",type=string,JSONPath=".status.latencyChangeFactor"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// PerformanceRegression is created (and later updated) by
// PostgresWatchReconciler whenever the Correlation Engine flags a
// statistically significant latency regression for a deploy/query pair.
// `kubectl get performanceregressions -A` surfaces the full history.
type PerformanceRegression struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec PerformanceRegressionSpec `json:"spec,omitempty"`
	// +optional
	Status PerformanceRegressionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PerformanceRegressionList contains a list of PerformanceRegression.
type PerformanceRegressionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerformanceRegression `json:"items"`
}
