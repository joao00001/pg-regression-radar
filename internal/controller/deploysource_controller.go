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
	"context"
	"fmt"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// pendingRequeueInterval controls how often a DeploySource whose
// postgresWatchRef doesn't resolve yet (the PostgresWatch doesn't exist, or
// hasn't started its worker) is re-checked.
const pendingRequeueInterval = 10 * time.Second

// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=deploysources,verbs=get;list;watch
// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=deploysources/status,verbs=get;update;patch

// DeploySourceReconciler reconciles a DeploySource object. It looks up the
// PostgresWatch named by spec.postgresWatchRef in the shared Registry to
// find that watch's ingester.Store, wraps it with an unmodified
// internal/ingester.Handler, and (de)registers an HTTP route for it on
// Mux — controller-runtime's job here is purely to keep the set of live
// webhook routes in sync with DeploySource CRs; the actual payload parsing
// and normalisation is 100% delegated to internal/ingester.
//
// It is a separate reconciler from PostgresWatchReconciler (rather than one
// controller watching both types) because the two CRDs have independent
// lifecycles and failure domains: a DeploySource can be created before its
// PostgresWatch exists (it just stays Pending and retries), and one
// PostgresWatch can be fed by many DeploySources.
type DeploySourceReconciler struct {
	client.Client

	// Registry is shared with PostgresWatchReconciler; read-only from here.
	Registry *Registry

	// Mux is the manager's dynamic webhook mux; routes are registered and
	// unregistered here as DeploySources come and go.
	Mux *DynamicMux

	Logger *slog.Logger
}

// SetupWithManager wires this reconciler into mgr.
func (r *DeploySourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&radarv1alpha1.DeploySource{}).
		Named("deploysource").
		Complete(r)
}

// Reconcile implements the controller-runtime Reconciler interface.
func (r *DeploySourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var src radarv1alpha1.DeploySource
	if err := r.Get(ctx, req.NamespacedName, &src); err != nil {
		if apierrors.IsNotFound(err) {
			r.Mux.Unregister(webhookPath(req.NamespacedName))
			log.Info("deploysource deleted, webhook route removed")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	watchKey := types.NamespacedName{Namespace: req.Namespace, Name: src.Spec.PostgresWatchRef}
	rt, ok := r.Registry.Get(watchKey)
	if !ok {
		r.Mux.Unregister(webhookPath(req.NamespacedName))

		src.Status.Phase = radarv1alpha1.DeploySourcePhasePending
		src.Status.ObservedGeneration = src.Generation
		src.Status.WebhookPath = ""
		src.Status.Message = fmt.Sprintf("postgresWatchRef %q not found or not yet running", src.Spec.PostgresWatchRef)
		setCondition(&src.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "PostgresWatchNotReady",
			Message:            src.Status.Message,
			ObservedGeneration: src.Generation,
		})
		if err := r.Status().Update(ctx, &src); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: pendingRequeueInterval}, nil
	}

	sourceType := src.Spec.SourceType
	if sourceType == "" {
		sourceType = "generic"
	}

	if sourceType == "kubernetes" {
		// Nothing to register: this source is fed by WorkloadWatchReconciler
		// watching spec.appName's Deployment/StatefulSet directly via the
		// Kubernetes API, not by an inbound webhook. Unregister any stale
		// route left over from before sourceType was changed to
		// "kubernetes", then report Ready without a webhookPath.
		r.Mux.Unregister(webhookPath(req.NamespacedName))

		src.Status.Phase = radarv1alpha1.DeploySourcePhaseReady
		src.Status.ObservedGeneration = src.Generation
		src.Status.WebhookPath = ""
		src.Status.Message = "Watched natively via the Kubernetes API; no webhook route is registered."
		setCondition(&src.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "NativeWatchActive",
			Message:            src.Status.Message,
			ObservedGeneration: src.Generation,
		})
		if err := r.Status().Update(ctx, &src); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("deploysource watched natively, no webhook route registered", "sourceType", sourceType, "appName", src.Spec.AppName)
		return ctrl.Result{}, nil
	}

	handler := ingester.NewHandler(rt.Store, dto.DeploySource{
		Name:             req.Name,
		Namespace:        req.Namespace,
		PostgresWatchRef: src.Spec.PostgresWatchRef,
		SourceType:       sourceType,
		AppName:          src.Spec.AppName,
		WebhookSecret:    src.Spec.WebhookSecret,
	}, r.Logger)

	path := webhookPath(req.NamespacedName)
	r.Mux.Register(path, handler)

	log.Info("deploysource webhook route registered", "path", path, "sourceType", sourceType)

	src.Status.Phase = radarv1alpha1.DeploySourcePhaseReady
	src.Status.ObservedGeneration = src.Generation
	src.Status.WebhookPath = path
	src.Status.Message = ""
	setCondition(&src.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "RouteRegistered",
		Message:            "Webhook route is active.",
		ObservedGeneration: src.Generation,
	})
	if err := r.Status().Update(ctx, &src); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// webhookPath is the deterministic HTTP path a DeploySource's webhook is
// served on: namespace-and-name-scoped so two DeploySources never collide
// and the path is fully derivable from `kubectl get deploysource`.
func webhookPath(key types.NamespacedName) string {
	return "/webhook/" + key.Namespace + "/" + key.Name
}
