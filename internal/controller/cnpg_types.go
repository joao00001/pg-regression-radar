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

package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// cnpgGroupVersion identifies CloudNativePG's own CRD group/version. Its
// Cluster resource is what spec.cnpgClusterRef (see cnpg_cluster.go)
// validates against — see
// https://cloudnative-pg.io/documentation/current/cloudnative-pg.v1/.
var cnpgGroupVersion = schema.GroupVersion{Group: "postgresql.cnpg.io", Version: "v1"}

// cnpgCluster is a minimal, locally-defined shadow of CloudNativePG's own
// `Cluster` resource, declaring only the status fields spec.cnpgClusterRef
// validation actually reads. This project deliberately does not vendor
// CloudNativePG's own API module (github.com/cloudnative-pg/cloudnative-pg)
// anywhere else, and pulling in that entire module — including its own
// transitive dependency tree — just to read two status fields off a
// resource this project never creates, updates, or deletes would be a very
// heavy dependency for a narrow, read-only check. A real CloudNativePG
// `Cluster` object decodes into this subset just fine: both
// controller-runtime's scheme-based client and the one-off JSON decoding
// client-go falls back to for unregistered fields silently ignore any
// upstream field not declared here (status.instances, status.conditions'
// other entries, spec.* entirely, etc.) — nothing below needs to, or
// attempts to, round-trip a full Cluster object.
type cnpgCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status cnpgClusterStatus `json:"status,omitempty"`
}

// cnpgClusterStatus mirrors the subset of CloudNativePG's own
// `Cluster.status` this project reads: `phase` (a short, free-text
// operational phase CloudNativePG has used since before it added
// `conditions`, e.g. "Cluster in healthy state", "Failed") and
// `conditions` (standard Kubernetes conditions; CloudNativePG reports a
// `type: "Ready"` condition with `status: "True"` once the instance
// manager considers the cluster fully operational). See
// validateCNPGCluster (cnpg_cluster.go) for exactly how these two are
// combined into a single healthy/unhealthy verdict.
type cnpgClusterStatus struct {
	Phase      string             `json:"phase,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DeepCopyObject implements runtime.Object, required for cnpgCluster to be
// usable as a client.Object with any controller-runtime client (real or
// fake). Hand-written rather than controller-gen'd: cnpgCluster is an
// internal, narrowly-scoped shadow type with no CRD of its own to
// generate manifests for, so running it through the same codegen pass as
// api/v1alpha1's real CRD types would be more machinery than three fields
// warrant.
func (in *cnpgCluster) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(cnpgCluster)
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Status.Phase = in.Status.Phase
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
	return out
}

// cnpgClusterList is the List counterpart cnpgCluster's registration into
// a runtime.Scheme requires (client.List's generic machinery expects every
// registered Kind to have one), even though validateCNPGCluster only ever
// performs a single-object Get, never a List.
type cnpgClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []cnpgCluster `json:"items"`
}

func (in *cnpgClusterList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(cnpgClusterList)
	out.TypeMeta = in.TypeMeta
	in.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]cnpgCluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyObject().(*cnpgCluster).DeepCopyInto(&out.Items[i])
		}
	}
	return out
}

// DeepCopyInto is a small helper so cnpgClusterList's DeepCopyObject above
// doesn't have to hand-roll a second copy of cnpgCluster's field-by-field
// copy.
func (in *cnpgCluster) DeepCopyInto(out *cnpgCluster) {
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Status.Phase = in.Status.Phase
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
}

// AddCNPGToScheme registers cnpgCluster/cnpgClusterList into s under
// postgresql.cnpg.io/v1, Kind=Cluster — the same GVK a real CloudNativePG
// installation's CRD serves. Called for every scheme a controller-runtime
// client.Client might be built against that could plausibly need to read a
// spec.cnpgClusterRef target: the manager's own (hub) scheme
// (internal/cli/manager.go), the remote/spoke client scheme
// (remote_client.go), and test schemes (postgreswatch_controller_test.go).
func AddCNPGToScheme(s *runtime.Scheme) {
	// AddKnownTypeWithName, not the shorter AddKnownTypes, deliberately:
	// AddKnownTypes infers the Kind string from the Go type's own name via
	// reflection (schema.GroupVersionKind{..., Kind: "cnpgCluster"} for an
	// unexported cnpgCluster struct) rather than the real CloudNativePG
	// Kind ("Cluster"). A fake client in unit tests resolves GVKs straight
	// from the scheme and never notices the mismatch, but a real API
	// server's RESTMapper only knows the resource under its real Kind —
	// registering under the wrong one fails every remote Get with "no
	// matches for kind \"cnpgCluster\" in version \"postgresql.cnpg.io/v1\""
	// despite the CRD being installed and reachable.
	s.AddKnownTypeWithName(cnpgGroupVersion.WithKind("Cluster"), &cnpgCluster{})
	s.AddKnownTypeWithName(cnpgGroupVersion.WithKind("ClusterList"), &cnpgClusterList{})
	metav1.AddToGroupVersion(s, cnpgGroupVersion)
}
