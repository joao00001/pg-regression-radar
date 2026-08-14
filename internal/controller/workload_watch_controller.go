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
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch

// WorkloadWatchReconciler reconciles apps/v1 Deployment and StatefulSet
// objects and, for every DeploySource with spec.sourceType "kubernetes"
// whose spec.workloadKind/spec.appName matches, synthesises a DeployEvent
// the instant that workload's rollout completes on a new revision.
//
// This closes a real adoption gap: DeploySource's other three source types
// (argocd, argo-rollouts, flux) all depend on an external GitOps tool
// sending a webhook when it deploys. A shop running plain
// `kubectl apply`/CI-driven Deployments, with no GitOps controller in front
// of them at all, previously had no way to feed this project anything.
// WorkloadWatchReconciler watches the workload directly via the Kubernetes
// API instead — no webhook, no GitOps tool, no extra credential to manage.
//
// It is a separate reconciler from DeploySourceReconciler (which still owns
// the webhook route for the other three source types) because it watches a
// completely different set of GVKs, on a completely different trigger
// (workload status updates, not DeploySource spec changes) — see
// SetupWithManager. DeploySourceReconciler itself special-cases
// sourceType "kubernetes" to skip webhook route registration entirely
// (there is nothing meaningful to POST to for this source type).
type WorkloadWatchReconciler struct {
	client.Client

	// Registry is shared with PostgresWatchReconciler and
	// DeploySourceReconciler; read-only from here.
	Registry *Registry

	Logger *slog.Logger

	// lastRevision remembers the most recently emitted revision per
	// (DeploySource, workload) pair, so a Deployment/StatefulSet that isn't
	// actually rolling out (e.g. a routine Reconcile triggered by an
	// unrelated status field update) doesn't re-emit a DeployEvent for a
	// revision already reported. Cleared for every DeploySource watching a
	// given workload when that workload is deleted, so a later recreation
	// under the same name starts fresh.
	//
	// Keyed by (DeploySource, workload) rather than just workload: two
	// different DeploySources (typically feeding two different
	// PostgresWatches) can legitimately both watch the same
	// Deployment/StatefulSet, and each needs its own independent
	// already-reported bookkeeping — see reconcileWorkload.
	mu           sync.Mutex
	lastRevision map[workloadWatchKey]string
}

// workloadWatchKey identifies one (DeploySource, workload) pairing for
// lastRevision's dedup bookkeeping. A bare workload types.NamespacedName is
// not enough on its own: it collapses two distinct DeploySources watching
// the same workload into a single dedup entry, so whichever one reconciles
// first would make the second look like an already-reported repeat instead
// of a genuinely separate deploy event for a (possibly different)
// PostgresWatch — see reconcileWorkload's fan-out over every matching
// DeploySource.
type workloadWatchKey struct {
	deploySource types.NamespacedName
	workload     types.NamespacedName
}

// SetupWithManager wires this reconciler into mgr, watching both
// Deployments and StatefulSets. controller-runtime's builder only accepts
// one primary type via For(); StatefulSet is added as a second watched
// type via Watches() with the same "enqueue the object itself" handler —
// Reconcile disambiguates which kind it's actually looking at by trying
// Get() against each type in turn (see reconcileWorkload's callers).
func (r *WorkloadWatchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.Deployment{}).
		Watches(&appsv1.StatefulSet{}, &handler.EnqueueRequestForObject{}).
		Named("workloadwatch").
		Complete(r)
}

// Reconcile implements the controller-runtime Reconciler interface.
func (r *WorkloadWatchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var dsList radarv1alpha1.DeploySourceList
	if err := r.List(ctx, &dsList, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list deploysources in %s: %w", req.Namespace, err)
	}

	var deploy appsv1.Deployment
	switch err := r.Get(ctx, req.NamespacedName, &deploy); {
	case err == nil:
		return r.reconcileWorkload(ctx, dsList.Items, "Deployment", req.NamespacedName,
			deploy.Annotations["deployment.kubernetes.io/revision"], deploymentRolloutComplete(&deploy))
	case !apierrors.IsNotFound(err):
		return ctrl.Result{}, err
	}

	var sts appsv1.StatefulSet
	switch err := r.Get(ctx, req.NamespacedName, &sts); {
	case err == nil:
		return r.reconcileWorkload(ctx, dsList.Items, "StatefulSet", req.NamespacedName,
			sts.Status.UpdateRevision, statefulSetRolloutComplete(&sts))
	case !apierrors.IsNotFound(err):
		return ctrl.Result{}, err
	}

	// Neither kind exists any more (deleted, or never existed): forget any
	// cached revision (for every DeploySource that had one for this
	// workload) so a future recreation under the same name is treated as a
	// fresh rollout rather than a no-op repeat.
	r.forgetWorkload(req.NamespacedName)
	return ctrl.Result{}, nil
}

// forgetWorkload clears every cached lastRevision entry for workload,
// regardless of which DeploySource it was recorded under. lastRevision has
// no secondary index from workload -> its DeploySource keys, but this map
// is bounded by (distinct DeploySources) x (distinct kubernetes-watched
// workloads) in the cluster -- both small, human-managed counts -- so a
// linear scan on the (rare, deletion-only) path that calls this is not a
// performance concern.
func (r *WorkloadWatchReconciler) forgetWorkload(workload types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.lastRevision {
		if k.workload == workload {
			delete(r.lastRevision, k)
		}
	}
}

// reconcileWorkload finds every DeploySource that watches the named
// workload and, for each one whose rollout has genuinely completed on a
// revision it hasn't already reported, feeds a synthesised DeployEvent into
// that DeploySource's own PostgresWatch's Store.
//
// It fans out to every match rather than stopping at the first: two
// different DeploySources (typically feeding two different PostgresWatches)
// can both legitimately watch the same Deployment/StatefulSet, and each one
// needs its own DeployEvent — see workloadWatchKey's doc comment for why an
// earlier version of this method, which used a plain "first match wins"
// break, silently dropped every match after the first.
func (r *WorkloadWatchReconciler) reconcileWorkload(ctx context.Context, sources []radarv1alpha1.DeploySource, kind string, key types.NamespacedName, revision string, rolloutComplete bool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var matches []*radarv1alpha1.DeploySource
	for i := range sources {
		ds := &sources[i]
		if ds.Spec.SourceType == "kubernetes" && ds.Spec.WorkloadKind == kind && ds.Spec.AppName == key.Name {
			matches = append(matches, ds)
		}
	}
	if len(matches) == 0 {
		// No DeploySource cares about this workload. Common case: most
		// Deployments/StatefulSets in a cluster have nothing to do with
		// pg-regression-radar at all.
		return ctrl.Result{}, nil
	}

	if !rolloutComplete || revision == "" {
		// Rollout still in progress, or this kind hasn't been assigned a
		// revision marker yet (e.g. brand new StatefulSet mid-creation).
		// Wait for the next status update to re-trigger Reconcile rather
		// than emitting a DeployEvent for a half-finished rollout. Applies
		// equally to every match — none of them have anything to report
		// yet either.
		return ctrl.Result{}, nil
	}

	// Requeue if *any* match's target PostgresWatch isn't ready yet, but
	// don't let that stop the others (whose PostgresWatch may well already
	// be ready) from being reported this pass.
	var needsRequeue bool

	for _, matched := range matches {
		dsKey := types.NamespacedName{Namespace: matched.Namespace, Name: matched.Name}
		wwKey := workloadWatchKey{deploySource: dsKey, workload: key}

		r.mu.Lock()
		if r.lastRevision == nil {
			r.lastRevision = make(map[workloadWatchKey]string)
		}
		alreadyReported := r.lastRevision[wwKey] == revision
		r.mu.Unlock()
		if alreadyReported {
			continue
		}

		watchKey := types.NamespacedName{Namespace: matched.Namespace, Name: matched.Spec.PostgresWatchRef}
		rt, ok := r.Registry.Get(watchKey)
		if !ok {
			// This match's target PostgresWatch isn't running yet. Retry
			// later rather than caching this revision as already-emitted
			// — a workload's status may not change again for a long time
			// (or ever) after a rollout completes, so there is no
			// guarantee another Reconcile would come along on its own to
			// give this a second chance.
			log.Info("workloadwatch: postgresWatchRef not ready yet, will retry",
				"deploySource", matched.Name, "workload", key.String(), "kind", kind)
			needsRequeue = true
			continue
		}

		ev := dto.DeployEvent{
			ID:        fmt.Sprintf("k8s-%s-%s-%s-%s-%s", kind, key.Namespace, key.Name, matched.Name, revision),
			Source:    matched.Name,
			App:       key.Name,
			Cluster:   rt.ClusterName,
			Namespace: key.Namespace,
			Revision:  revision,
			Timestamp: time.Now().UTC(),
		}
		rt.Store.Add(ev)

		r.mu.Lock()
		r.lastRevision[wwKey] = revision
		r.mu.Unlock()

		log.Info("workloadwatch: deploy event synthesised from native rollout",
			"kind", kind, "workload", key.String(), "revision", revision, "deploySource", matched.Name)
	}

	if needsRequeue {
		return ctrl.Result{RequeueAfter: pendingRequeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

// deploymentRolloutComplete reports whether d has finished rolling out: the
// controller has observed the latest spec generation, and every replica is
// updated, present, and available. Mirrors the check
// `kubectl rollout status deployment` performs.
func deploymentRolloutComplete(d *appsv1.Deployment) bool {
	if d.Status.ObservedGeneration < d.Generation {
		return false
	}
	var want int32 = 1
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	return d.Status.UpdatedReplicas == want &&
		d.Status.Replicas == want &&
		d.Status.AvailableReplicas == want
}

// statefulSetRolloutComplete reports whether s has finished rolling out to
// its latest revision: the controller has observed the latest spec
// generation, every replica is updated, and — the StatefulSet-specific
// signal, since it has no direct equivalent of Deployment's "available
// replicas" — currentRevision has caught up to updateRevision.
func statefulSetRolloutComplete(s *appsv1.StatefulSet) bool {
	if s.Status.ObservedGeneration < s.Generation {
		return false
	}
	var want int32 = 1
	if s.Spec.Replicas != nil {
		want = *s.Spec.Replicas
	}
	return s.Status.UpdatedReplicas == want &&
		s.Status.CurrentRevision == s.Status.UpdateRevision
}
