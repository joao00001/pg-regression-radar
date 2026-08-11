package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeploySourceSpec describes where deploy events come from and which
// PostgresWatch they should be correlated against. DeploySourceReconciler
// uses this to register a webhook route backed by internal/ingester.Handler.
type DeploySourceSpec struct {
	// postgresWatchRef is the name of the PostgresWatch (in the same
	// namespace) whose Correlation Engine will analyse deploys ingested
	// from this source.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	PostgresWatchRef string `json:"postgresWatchRef"`

	// sourceType drives payload normalisation in internal/ingester.Handler.
	// +kubebuilder:validation:Enum=argocd;argo-rollouts;flux;generic
	// +kubebuilder:default=generic
	SourceType string `json:"sourceType,omitempty"`

	// appName narrows correlation to a single application; empty means all
	// apps reported by this source are considered.
	// +optional
	AppName string `json:"appName,omitempty"`
}

// DeploySourcePhase summarises whether the webhook route backing this
// DeploySource is currently being served.
type DeploySourcePhase string

const (
	// DeploySourcePhasePending means the referenced PostgresWatch has not
	// been found yet (or is not yet Running), so no route is registered.
	DeploySourcePhasePending DeploySourcePhase = "Pending"
	// DeploySourcePhaseReady means the webhook route is registered and
	// accepting deploy events.
	DeploySourcePhaseReady DeploySourcePhase = "Ready"
)

// DeploySourceStatus reflects whether the webhook route is live.
type DeploySourceStatus struct {
	// phase summarises the route registration state.
	// +optional
	Phase DeploySourcePhase `json:"phase,omitempty"`

	// observedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// webhookPath is the HTTP path this source's webhook is served on by
	// the manager's webhook listener, e.g. /webhook/<namespace>/<name>.
	// +optional
	WebhookPath string `json:"webhookPath,omitempty"`

	// message carries a human-readable explanation, most useful while
	// phase is Pending.
	// +optional
	Message string `json:"message,omitempty"`

	// conditions represent the current state of the DeploySource resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=pgds
// +kubebuilder:printcolumn:name="Watch",type=string,JSONPath=".spec.postgresWatchRef"
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=".spec.sourceType"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=".status.webhookPath"

// DeploySource registers a GitOps deploy-event webhook and binds it to a
// PostgresWatch for correlation.
type DeploySource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec DeploySourceSpec `json:"spec,omitempty"`
	// +optional
	Status DeploySourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DeploySourceList contains a list of DeploySource.
type DeploySourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeploySource `json:"items"`
}
