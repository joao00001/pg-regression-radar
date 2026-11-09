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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
)

// DefaultClusterHealthCheckInterval is used whenever
// PostgresRadarClusterReconciler.HealthCheckInterval is left at its zero
// value — see internal/cli/manager.go's --cluster-health-check-interval
// flag, which is the only production caller expected to ever leave it
// unset (tests always set it explicitly to keep themselves fast).
const DefaultClusterHealthCheckInterval = 5 * time.Minute

// clusterHealthCheckTimeout bounds how long a single health check's
// discovery call is allowed to block Reconcile for. A remote API server
// that is reachable but slow (e.g. mid-upgrade, or behind a congested
// network path) should eventually be reported unhealthy rather than
// wedging this reconciler's workqueue indefinitely.
const clusterHealthCheckTimeout = 15 * time.Second

// clusterReadyConditionType is the Condition.Type PostgresRadarCluster's
// own health check reports under — distinct from, but modelled after,
// cnpgReadyConditionType in cnpg_cluster.go.
const clusterReadyConditionType = "Ready"

// PostgresRadarClusterReconciler periodically verifies that a registered
// remote cluster's kubeconfig (PostgresRadarClusterSpec.KubeconfigSecretRef)
// still parses, still passes validateKubeconfigAuth, and still authenticates
// against a real, reachable API server — closing the gap documented in
// docs/roadmap.md's former "Multi-cluster support, in detail" entry: before
// this reconciler existed, a rotated or revoked kubeconfig was only ever
// discovered reactively, the next time some PostgresWatch's resolveDSN
// happened to need that cluster, and never surfaced anywhere an
// administrator watching PostgresRadarCluster.status would see it.
//
// The health check itself is a single discovery.ServerVersion() call: this
// is deliberately the least-privileged possible probe, since every
// Kubernetes cluster's default RBAC bindings expose the version/discovery
// endpoints to any authenticated identity (system:discovery ->
// system:authenticated) — the same credential a least-privilege kubeconfig
// built strictly per docs/multi-cluster.md#remote-cluster-rbac (whose only
// grant is "get" on one specific DSN Secret) must still satisfy. A check
// requiring any broader RBAC would force every existing least-privilege
// installation to grant more access than its workload actually needs, just
// to keep registering a clean bill of health.
//
// This intentionally does NOT reuse PostgresWatchReconciler's
// remoteClientCache: that cache is keyed and sized around the request-path
// assumption that many reconciles share one hot client, whereas this
// reconciler runs on a multi-minute interval per cluster — building a
// fresh REST config/discovery client each time is negligible overhead and
// avoids exporting remoteClientCache's unexported internals across a
// reconciler boundary for a caller that would barely use the cache's
// actual value proposition (TTL'd reuse).
type PostgresRadarClusterReconciler struct {
	client.Client
	Logger *slog.Logger

	// HealthCheckInterval is how often a healthy cluster is re-probed
	// (also the interval used to schedule the very first probe after a
	// PostgresRadarCluster is created). Defaults to
	// DefaultClusterHealthCheckInterval when zero. An unhealthy result
	// reuses the same interval rather than any tighter backoff — this
	// mirrors PostgresWatchReconciler's statusRefreshInterval philosophy
	// of a steady cadence over exponential backoff, since the failure mode
	// being watched for (an expired/rotated credential) does not resolve
	// itself by retrying sooner.
	HealthCheckInterval time.Duration
}

// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=postgresradarclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=postgresradarclusters/status,verbs=get;update;patch

// Reconcile implements the health check described on
// PostgresRadarClusterReconciler. It always returns a nil error alongside
// RequeueAfter: r.interval() — a steady probe cadence regardless of
// outcome — so a persistently broken kubeconfig produces one WARN-level
// condition transition (not a log line per failed attempt) and otherwise
// quiet, periodic re-checks, exactly like any other steady-state polling
// loop in this codebase (see pollInterval/statusRefreshInterval).
func (r *PostgresRadarClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	interval := r.interval()

	var cluster radarv1alpha1.PostgresRadarCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetch postgresradarcluster %s: %w", req.Name, err)
	}

	probeErr := r.probe(ctx, &cluster)
	r.recordProbeResult(&cluster, probeErr)

	if err := r.Status().Update(ctx, &cluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("update postgresradarcluster %s status: %w", req.Name, err)
	}

	if probeErr != nil && r.Logger != nil {
		r.Logger.Warn("postgresradarcluster health check failed",
			"cluster", cluster.Name, "err", probeErr)
	}

	return ctrl.Result{RequeueAfter: interval}, nil
}

// probe performs the actual kubeconfig-Secret-fetch + REST-config-build +
// discovery.ServerVersion() sequence and returns a single error describing
// the first thing that went wrong, or nil when the cluster is healthy.
func (r *PostgresRadarClusterReconciler) probe(ctx context.Context, cluster *radarv1alpha1.PostgresRadarCluster) error {
	sel := cluster.Spec.KubeconfigSecretRef
	secretKey := types.NamespacedName{Namespace: sel.Namespace, Name: sel.Name}
	var secret corev1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		return fmt.Errorf("fetch kubeconfig secret %s: %w", secretKey, err)
	}
	if err := checkSecretConsent(&secret, "registered cluster kubeconfig secret"); err != nil {
		return err
	}
	kubeconfig, ok := secret.Data[sel.Key]
	if !ok {
		return fmt.Errorf("kubeconfig secret %s has no key %q", secretKey, sel.Key)
	}

	restConfig, err := buildRemoteRESTConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("build rest config from kubeconfig secret %s: %w", secretKey, err)
	}

	disco, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("build discovery client for cluster %q: %w", cluster.Name, err)
	}

	probeCtx, cancel := context.WithTimeout(ctx, clusterHealthCheckTimeout)
	defer cancel()
	if _, err := disco.ServerVersionWithContext(probeCtx); err != nil {
		return fmt.Errorf("probe cluster %q (discovery ServerVersion): %w", cluster.Name, err)
	}
	return nil
}

// recordProbeResult stamps cluster.Status with the outcome of a just-run
// probe: the Ready condition (and, in sync with it, the legacy plain
// Ready/Message fields kept for anything that reads those directly instead
// of walking Conditions), LastProbeTime, and ObservedGeneration.
func (r *PostgresRadarClusterReconciler) recordProbeResult(cluster *radarv1alpha1.PostgresRadarCluster, probeErr error) {
	now := metav1.Now()
	cond := metav1.Condition{
		Type:               clusterReadyConditionType,
		ObservedGeneration: cluster.Generation,
	}
	if probeErr == nil {
		cond.Status = metav1.ConditionTrue
		cond.Reason = "HealthCheckSucceeded"
		cond.Message = "kubeconfig is valid and the remote API server responded to a discovery probe"
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = "HealthCheckFailed"
		cond.Message = probeErr.Error()
	}

	setCondition(&cluster.Status.Conditions, cond)
	cluster.Status.Ready = probeErr == nil
	cluster.Status.Message = cond.Message
	cluster.Status.ObservedGeneration = cluster.Generation
	cluster.Status.LastProbeTime = &now
}

// interval returns HealthCheckInterval, falling back to
// DefaultClusterHealthCheckInterval when unset.
func (r *PostgresRadarClusterReconciler) interval() time.Duration {
	if r.HealthCheckInterval > 0 {
		return r.HealthCheckInterval
	}
	return DefaultClusterHealthCheckInterval
}

// SetupWithManager wires this reconciler to watch both PostgresRadarCluster
// objects directly and, via mapSecretToClusters, the kubeconfig Secrets
// they reference — so rotating a kubeconfig triggers an immediate
// re-validation instead of waiting up to a full HealthCheckInterval for the
// change to be noticed.
func (r *PostgresRadarClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&radarv1alpha1.PostgresRadarCluster{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToClusters)).
		Named("postgresradarcluster").
		Complete(r)
}

// mapSecretToClusters lists every PostgresRadarCluster and enqueues the
// ones whose spec.kubeconfigSecretRef matches the Secret that triggered
// this event. A full List on every Secret write anywhere in the cluster is
// acceptable here: PostgresRadarCluster is a cluster-administrator-managed,
// low-cardinality resource (one entry per registered spoke cluster, not
// per-tenant), so this is not a hot path the way, say, a per-PostgresWatch
// Secret watch would be.
func (r *PostgresRadarClusterReconciler) mapSecretToClusters(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	var clusters radarv1alpha1.PostgresRadarClusterList
	if err := r.List(ctx, &clusters); err != nil {
		if r.Logger != nil {
			r.Logger.Error("list postgresradarclusters for secret-triggered health check", "err", err)
		}
		return nil
	}

	var requests []reconcile.Request
	for _, cluster := range clusters.Items {
		sel := cluster.Spec.KubeconfigSecretRef
		if sel.Namespace == secret.Namespace && sel.Name == secret.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cluster.Name},
			})
		}
	}
	return requests
}
