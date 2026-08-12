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
	// analysis (see internal/ingester's DeployEvent.ID).
	DeployEventID string `json:"deployEventId"`

	// queryID is the pg_stat_statements queryid analysed.
	QueryID int64 `json:"queryId"`

	// queryText is a truncated excerpt of the analysed query, for
	// human-readable display in `kubectl describe`.
	// +optional
	QueryText string `json:"queryText,omitempty"`
}

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
