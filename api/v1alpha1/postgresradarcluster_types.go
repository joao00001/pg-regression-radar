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

// PostgresRadarClusterSpec describes a remote ("spoke") Kubernetes cluster
// whose kubeconfig is managed by a cluster administrator. Only
// administrators should be granted create/update/delete access to this
// resource — see config/rbac for the expected ClusterRole bindings.
type PostgresRadarClusterSpec struct {
	// kubeconfigSecretRef points at a Secret, in the hub cluster, whose
	// named key holds the kubeconfig (raw YAML or JSON, identical to the
	// output of `kubectl config view --raw`) for this remote cluster.
	//
	// The Secret must carry the consent label
	// pg-regression-radar.io/allow-postgreswatch-access=true so that the
	// manager can read it; the manager refuses to load kubeconfigs from
	// unlabelled Secrets. Only static credentials (bearer token, client
	// certificate) are allowed in the kubeconfig — exec-based plugins,
	// auth-providers, and proxy-url settings are rejected to prevent
	// execution of untrusted code in the manager pod.
	//
	// +kubebuilder:validation:Required
	KubeconfigSecretRef KubeconfigSecretSelector `json:"kubeconfigSecretRef"`

	// namespace is the default namespace on the remote cluster in which
	// dsnSecretRef objects are looked up. When a PostgresWatch sets
	// remoteClusterRef but does not set remoteNamespace, this field is
	// used. When both this field and remoteNamespace are empty, the
	// watch's own namespace (the hub namespace) is used — the convention
	// CloudNativePG follows for generated-credential Secrets.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// KubeconfigSecretSelector identifies the hub-cluster Secret and key that
// contain the kubeconfig bytes for a registered remote cluster.
type KubeconfigSecretSelector struct {
	// namespace of the Secret in the hub cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key within the Secret's Data map that holds the kubeconfig bytes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// PostgresRadarClusterStatus reflects the observed state of the registered
// cluster, as last verified by the controller during reconciliation.
type PostgresRadarClusterStatus struct {
	// ready is true when the kubeconfig was last read and validated
	// successfully.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// message carries a human-readable explanation, populated when ready
	// is false.
	// +optional
	Message string `json:"message,omitempty"`

	// observedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=pgrc
// +kubebuilder:printcolumn:name="Ready",type=boolean,JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// PostgresRadarCluster represents a remote ("spoke") Kubernetes cluster
// registered by a cluster administrator. PostgresWatch resources reference
// these by name via spec.remoteClusterRef instead of pointing directly at
// arbitrary kubeconfig Secrets — administrators control which clusters are
// reachable, and watch creators can only choose from the pre-registered set.
//
// This is a cluster-scoped resource; only administrators should be allowed
// to create or modify it (see config/rbac).
type PostgresRadarCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec PostgresRadarClusterSpec `json:"spec,omitempty"`
	// +optional
	Status PostgresRadarClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PostgresRadarClusterList contains a list of PostgresRadarCluster.
type PostgresRadarClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresRadarCluster `json:"items"`
}
