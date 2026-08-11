// Package v1alpha1 contains the real Kubernetes Custom Resource Definitions
// for pg-regression-radar: PostgresWatch, DeploySource, and
// PerformanceRegression. These are genuine runtime.Object-implementing API
// types managed by controller-runtime, distinct from the plain DTO structs
// in pkg/apis/v1alpha1 that internal/collector, internal/correlation,
// internal/ingester, and internal/alerting use as their in-process data
// model. The controllers in internal/controller translate between the two:
// CR Spec -> DTO -> engine call -> result -> CR Status.
//
// +kubebuilder:object:generate=true
// +groupName=radar.pgregressionradar.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "radar.pgregressionradar.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
		metav1.AddToGroupVersion(s, GroupVersion)
		s.AddKnownTypes(GroupVersion,
			&PostgresWatch{}, &PostgresWatchList{},
			&DeploySource{}, &DeploySourceList{},
			&PerformanceRegression{}, &PerformanceRegressionList{},
		)
		return nil
	})

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
