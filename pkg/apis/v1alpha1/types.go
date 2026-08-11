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
	// MinExecutions guards against false positives on rarely-called queries.
	MinExecutions int64 `json:"minExecutions,omitempty"`
	// LatencyChangeThreshold filters out noise; raise it in high-churn environments.
	LatencyChangeThreshold float64 `json:"latencyChangeThreshold,omitempty"`
	// CriticalQueryIDs bypass MinExecutions so SLA-critical queries are always checked.
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
}

// DeployEvent is a normalised deploy event ingested from any supported source.
type DeployEvent struct {
	ID        string `json:"id"`
	// Source links back to the DeploySource for multi-source deployments.
	Source    string `json:"source"`
	App       string `json:"app"`
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Revision  string `json:"revision"`
	ImageTag  string `json:"imageTag,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// PerformanceRegressionStatus reflects the outcome of the correlation analysis.
type PerformanceRegressionStatus string

const (
	StatusDetected         PerformanceRegressionStatus = "Detected"
	StatusNoRegression     PerformanceRegressionStatus = "NoRegression"
	StatusInsufficientData PerformanceRegressionStatus = "InsufficientData"
)

// PerformanceRegression is created by the Correlation Engine when it analyses a deploy.
type PerformanceRegression struct {
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	DeployEventID string `json:"deployEventId"`
	QueryID       int64  `json:"queryId"`
	QueryText     string `json:"queryText"`
	Status        PerformanceRegressionStatus `json:"status"`
	ConfidenceScore     float64 `json:"confidenceScore"`
	MeanLatencyBefore   float64 `json:"meanLatencyBefore"`
	MeanLatencyAfter    float64 `json:"meanLatencyAfter"`
	LatencyChangeFactor float64 `json:"latencyChangeFactor"`
	// ExternalCauseSuspected hints that CPU/IO also shifted, so the deploy may not be the sole cause.
	ExternalCauseSuspected bool `json:"externalCauseSuspected,omitempty"`
	// DetectedChangeAt is the timestamp of the change point the E-divisive
	// stage actually located in the query latency series. It may differ
	// slightly from DeployEventID's deploy timestamp (within the engine's
	// configured tolerance) since rollouts, connection draining and scrape
	// lag delay the observable effect of a deploy. Zero unless Status is
	// Detected.
	DetectedChangeAt time.Time `json:"detectedChangeAt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
