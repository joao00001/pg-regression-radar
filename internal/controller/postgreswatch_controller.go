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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/internal/planner"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// statusRefreshInterval is how often a PostgresWatch whose spec has not
// changed is re-reconciled purely to refresh status.lastScrapeTime and
// status.trackedQueryIds.
const statusRefreshInterval = 30 * time.Second

// pollInterval is how often a running watch's Store is drained for new
// DeployEvents to analyse. It mirrors cmd/operator/main.go's polling
// cadence: a channel-based push would require locking changes across
// packages we are not allowed to modify, so polling keeps the coupling
// minimal.
const pollInterval = 5 * time.Second

// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=postgreswatches,verbs=get;list;watch
// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=postgreswatches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=performanceregressions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=radar.pgregressionradar.io,resources=performanceregressions/status,verbs=get;update;patch
//
// This Secret RBAC covers both spec.dsnSecretRef (read directly, hub
// cluster) and spec.remoteClusterSecretRef (the kubeconfig Secret used to
// reach a remote cluster's DSN Secret instead — see dsnSecretClient below
// and docs/multi-cluster.md). It does NOT grant any access to remote
// clusters themselves: that access comes entirely from whatever RBAC is
// embedded in the kubeconfig a remoteClusterSecretRef Secret points at,
// which is operator-managed and out of this manager's control by design.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// PostgresWatchReconciler reconciles a PostgresWatch object: it ensures a
// background WatchRuntime (Collector + Correlation Engine + Notifier) is
// running whenever a PostgresWatch exists, restarts it on spec changes, and
// stops it on deletion. It never touches the internals of
// internal/collector, internal/correlation, or internal/alerting — only
// their exported constructors and methods.
//
// Design reference: this follows the "controller manages background work
// per CR" pattern described in the kubebuilder book's CronJob tutorial
// (https://book.kubebuilder.io/cronjob-tutorial/controller-implementation),
// adapted for a long-lived goroutine instead of a one-shot Job: the
// reconciler is the single owner of a keyed map of running workers
// (Registry), starting one on Create, stopping it on Delete, and
// restarting it on Update when the spec's fingerprint changes.
type PostgresWatchReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Registry tracks the live WatchRuntime per PostgresWatch so
	// DeploySourceReconciler can find the right Store to feed.
	Registry *Registry

	// Logger feeds the reused engine packages, which take *slog.Logger
	// rather than controller-runtime's logr.Logger. Reconcile-level logs
	// still go through logf.FromContext(ctx), the controller-runtime
	// convention; Logger is only for the background workers this
	// reconciler starts.
	Logger *slog.Logger

	// remoteClients caches controller-runtime clients built from
	// remoteClusterSecretRef kubeconfigs, keyed by kubeconfig content (see
	// remote_client.go). It is initialised lazily via remoteClientsOnce so
	// PostgresWatchReconciler{} zero-value construction (as used throughout
	// postgreswatch_controller_test.go) keeps working without callers
	// having to know about it.
	remoteClientsOnce sync.Once
	remoteClients     *remoteClientCache
}

// getRemoteClients returns the reconciler's remote client cache,
// initialising it on first use. Safe for concurrent use across Reconcile
// invocations.
func (r *PostgresWatchReconciler) getRemoteClients() *remoteClientCache {
	r.remoteClientsOnce.Do(func() {
		r.remoteClients = newRemoteClientCache()
	})
	return r.remoteClients
}

// Reconcile implements the controller-runtime Reconciler interface.
func (r *PostgresWatchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var watch radarv1alpha1.PostgresWatch
	if err := r.Get(ctx, req.NamespacedName, &watch); err != nil {
		if apierrors.IsNotFound(err) {
			// The CR is gone: stop its worker, if any, and forget it.
			r.stopWatch(req.NamespacedName)
			log.Info("postgreswatch deleted, worker stopped")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	dsn, err := r.resolveDSN(ctx, &watch)
	if err != nil {
		log.Error(err, "unable to resolve DSN")
		return r.markFailed(ctx, &watch, err)
	}

	specHash := hashPostgresWatchSpec(watch.Spec, dsn)

	if rt, ok := r.Registry.Get(req.NamespacedName); ok && rt.SpecHash == specHash {
		// Already running with this exact effective config: just refresh
		// status and requeue later instead of restarting the worker.
		return ctrl.Result{RequeueAfter: statusRefreshInterval}, r.refreshStatus(ctx, &watch, rt)
	}

	// First reconcile for this watch, or the spec changed: stop any
	// previous incarnation before starting the new one so we never leak a
	// goroutine or double-register Prometheus metrics.
	r.stopWatch(req.NamespacedName)

	rt, err := r.startWatch(req.NamespacedName, watch.DeepCopy(), dsn, specHash)
	if err != nil {
		log.Error(err, "unable to start watch worker")
		return r.markFailed(ctx, &watch, err)
	}
	r.Registry.Set(req.NamespacedName, rt)

	log.Info("postgreswatch worker started", "cluster", watch.Spec.ClusterName)

	watch.Status.Phase = radarv1alpha1.PostgresWatchPhaseRunning
	watch.Status.ObservedGeneration = watch.Generation
	watch.Status.Message = ""
	setCondition(&watch.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "WorkerStarted",
		Message:            "Collector and Correlation Engine are running.",
		ObservedGeneration: watch.Generation,
	})
	if err := r.Status().Update(ctx, &watch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: statusRefreshInterval}, nil
}

// markFailed records a Failed phase on the PostgresWatch status and
// requeues with backoff (by returning the error) so controller-runtime's
// rate limiter spaces out retries.
func (r *PostgresWatchReconciler) markFailed(ctx context.Context, watch *radarv1alpha1.PostgresWatch, cause error) (ctrl.Result, error) {
	watch.Status.Phase = radarv1alpha1.PostgresWatchPhaseFailed
	watch.Status.Message = cause.Error()
	setCondition(&watch.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "WorkerStartFailed",
		Message:            cause.Error(),
		ObservedGeneration: watch.Generation,
	})
	if err := r.Status().Update(ctx, watch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, cause
}

// refreshStatus updates observability fields on an already-running watch
// without restarting anything.
func (r *PostgresWatchReconciler) refreshStatus(ctx context.Context, watch *radarv1alpha1.PostgresWatch, rt *WatchRuntime) error {
	ids := rt.Collector.AllQueryIDs()
	watch.Status.Phase = radarv1alpha1.PostgresWatchPhaseRunning
	watch.Status.ObservedGeneration = watch.Generation
	watch.Status.TrackedQueryIDs = int64(len(ids))
	if last := rt.Collector.LastScrapeTime(); !last.IsZero() {
		mt := metav1.NewTime(last)
		watch.Status.LastScrapeTime = &mt
	}
	return r.Status().Update(ctx, watch)
}

// resolveDSN returns the Postgres DSN to use: spec.dsn takes precedence,
// otherwise the key named by spec.dsnSecretRef is read from a Secret — in
// the hub cluster (this manager's own API server), in the watch's own
// namespace, by default; or in a remote ("spoke") cluster when
// spec.remoteClusterSecretRef names a kubeconfig Secret, in which case the
// namespace comes from dsnSecretNamespace (spec.remoteNamespace when set,
// the watch's own namespace otherwise). See dsnSecretClient for how the
// client routing decision is made, and docs/multi-cluster.md for the
// hub-spoke model.
func (r *PostgresWatchReconciler) resolveDSN(ctx context.Context, watch *radarv1alpha1.PostgresWatch) (string, error) {
	if watch.Spec.DSN != "" {
		return watch.Spec.DSN, nil
	}
	if watch.Spec.DSNSecretRef == nil {
		return "", fmt.Errorf("postgreswatch %s/%s: neither spec.dsn nor spec.dsnSecretRef is set", watch.Namespace, watch.Name)
	}

	secretClient, remoteKubeconfig, err := r.dsnSecretClient(ctx, watch)
	if err != nil {
		return "", err
	}

	var secret corev1.Secret
	key := types.NamespacedName{Namespace: dsnSecretNamespace(watch), Name: watch.Spec.DSNSecretRef.Name}
	if err := secretClient.Get(ctx, key, &secret); err != nil {
		if remoteKubeconfig != nil {
			// The cached remote client we just used failed a real request
			// against the spoke cluster — most plausibly an expired or
			// revoked credential embedded in the kubeconfig, though a
			// transient network failure looks identical from here. Evict
			// it immediately rather than letting this watch's next
			// reconcile (or any sibling PostgresWatch pointed at the same
			// remote cluster) keep reusing a client already known to be
			// broken until the next scheduled TTL sweep — see
			// remoteClientCache.evict for exactly what this does and does
			// not fix.
			r.getRemoteClients().evict(remoteKubeconfig)
		}
		return "", fmt.Errorf("fetch dsn secret %s: %w", key, err)
	}
	val, ok := secret.Data[watch.Spec.DSNSecretRef.Key]
	if !ok {
		return "", fmt.Errorf("secret %s has no key %q", key, watch.Spec.DSNSecretRef.Key)
	}
	return string(val), nil
}

// dsnSecretNamespace returns the namespace dsnSecretRef should be looked up
// in: spec.remoteNamespace when a remote cluster is in play and it's set,
// the watch's own namespace otherwise (the pre-existing "same name on both
// sides" convention, still the default because it matches how
// CloudNativePG's own generated credential Secrets are named). A
// remoteNamespace set without remoteClusterSecretRef has no effect — it
// only means anything once there's a remote cluster to look in.
func dsnSecretNamespace(watch *radarv1alpha1.PostgresWatch) string {
	if watch.Spec.RemoteClusterSecretRef != nil && watch.Spec.RemoteNamespace != "" {
		return watch.Spec.RemoteNamespace
	}
	return watch.Namespace
}

// dsnSecretClient returns the client.Client to use when reading
// watch.Spec.DSNSecretRef: the reconciler's own (hub) client by default, or
// a client built from a remote cluster's kubeconfig when
// watch.Spec.RemoteClusterSecretRef is set — in which case the raw
// kubeconfig bytes are also returned (nil in the hub-client case) so
// resolveDSN can evict this exact cache entry if the client goes on to fail
// a real request.
//
// The kubeconfig Secret itself is always read via the hub client — it must
// live in the hub cluster, in the watch's namespace, precisely so the
// manager's existing "get;list;watch Secrets" RBAC (see the kubebuilder
// marker above Reconcile) is sufficient to reach it. The DSN Secret it then
// unlocks access to is looked up by dsnSecretNamespace — the *same*
// namespace name on the remote cluster by default (mirroring the
// convention CloudNativePG itself uses for generated-credential Secrets),
// or spec.remoteNamespace when the fleet's hub/spoke naming doesn't line up
// 1:1 (see docs/multi-cluster.md).
func (r *PostgresWatchReconciler) dsnSecretClient(ctx context.Context, watch *radarv1alpha1.PostgresWatch) (client.Client, []byte, error) {
	if watch.Spec.RemoteClusterSecretRef == nil {
		return r.Client, nil, nil
	}

	var kubeconfigSecret corev1.Secret
	key := types.NamespacedName{Namespace: watch.Namespace, Name: watch.Spec.RemoteClusterSecretRef.Name}
	if err := r.Get(ctx, key, &kubeconfigSecret); err != nil {
		return nil, nil, fmt.Errorf("fetch remote cluster kubeconfig secret %s: %w", key, err)
	}
	kubeconfig, ok := kubeconfigSecret.Data[watch.Spec.RemoteClusterSecretRef.Key]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s has no key %q", key, watch.Spec.RemoteClusterSecretRef.Key)
	}

	remoteClient, err := r.getRemoteClients().get(kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("build client for remote cluster from secret %s: %w", key, err)
	}
	return remoteClient, kubeconfig, nil
}

// startWatch builds a fresh WatchRuntime and starts its background
// goroutines. The runtime's context is intentionally independent from ctx
// (the short-lived Reconcile context) — its lifetime is controlled solely
// by Registry and stopWatch.
func (r *PostgresWatchReconciler) startWatch(key types.NamespacedName, watch *radarv1alpha1.PostgresWatch, dsn, specHash string) (*WatchRuntime, error) {
	scrapeInterval := time.Duration(watch.Spec.ScrapeIntervalSeconds) * time.Second
	if scrapeInterval <= 0 {
		scrapeInterval = 60 * time.Second
	}

	promReg := prometheus.NewRegistry()
	col, err := collector.New(collector.Config{
		DSN:            dsn,
		ScrapeInterval: scrapeInterval,
		ClusterName:    watch.Spec.ClusterName,
		Namespace:      watch.Namespace,
		CapturePlans:   watch.Spec.CapturePlans,
	}, r.Logger, promReg)
	if err != nil {
		return nil, fmt.Errorf("create collector: %w", err)
	}

	windowMinutes := int(watch.Spec.WindowMinutes)
	if windowMinutes <= 0 {
		windowMinutes = 30
	}
	minExecutions := watch.Spec.MinExecutions
	if minExecutions <= 0 {
		minExecutions = 10
	}
	latencyThreshold := parseFloatOr(watch.Spec.LatencyChangeThreshold, 0.20)
	pValueThreshold := parseFloatOr(watch.Spec.PValueThreshold, 0.05)

	engine := correlation.New(correlation.Config{
		WindowMinutes:          windowMinutes,
		MinExecutions:          minExecutions,
		LatencyChangeThreshold: latencyThreshold,
		PValueThreshold:        pValueThreshold,
	}, col, r.Logger)

	notifier := alerting.NewWebhookNotifier(alerting.WebhookConfig{
		URL:         watch.Spec.SlackWebhookURL,
		ClusterName: watch.Spec.ClusterName,
	}, r.Logger)

	workerCtx, cancel := context.WithCancel(context.Background())

	rt := &WatchRuntime{
		Store:        &ingester.Store{},
		Collector:    col,
		Engine:       engine,
		Notifier:     notifier,
		PromRegistry: promReg,
		SpecHash:     specHash,
		ClusterName:  watch.Spec.ClusterName,
		CapturePlans: watch.Spec.CapturePlans,
		Cancel:       cancel,
	}

	go func() {
		if err := col.Run(workerCtx); err != nil && workerCtx.Err() == nil {
			r.Logger.Error("postgreswatch: collector exited unexpectedly", "watch", key.String(), "err", err)
		}
	}()
	go r.pollLoop(workerCtx, key, rt)

	return rt, nil
}

// stopWatch cancels and forgets the runtime for key, if any is tracked.
func (r *PostgresWatchReconciler) stopWatch(key types.NamespacedName) {
	if rt, ok := r.Registry.Get(key); ok {
		rt.Cancel()
		r.Registry.Delete(key)
	}
}

// pollLoop drains rt.Store for DeployEvents not yet analysed, runs them
// through rt.Engine, fires alerts for detected regressions via rt.Notifier,
// and persists a PerformanceRegression CR for each one so
// `kubectl get performanceregressions` reflects reality. It mirrors the
// polling loop in cmd/operator/main.go.
func (r *PostgresWatchReconciler) pollLoop(ctx context.Context, key types.NamespacedName, rt *WatchRuntime) {
	var cursor int
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, newCursor := rt.Store.DrainSince(cursor)
			cursor = newCursor
			for _, ev := range events {

				results := rt.Engine.Analyse(ev)
				for _, res := range results {
					if res.Status != dto.StatusDetected {
						continue
					}

					// Attach a plan-diff summary before notifying/persisting,
					// mirroring internal/cli/operator.go's poll loop, so the
					// CRD-driven (cmd/manager) and standalone CLI paths give
					// the same plan-diff-correlation behavior when enabled.
					// PlansAround is nil-safe and returns (nil, nil) when
					// CapturePlans was never enabled on the Collector, so this
					// gate is an optimization (skip the lookup entirely), not
					// a correctness requirement.
					if rt.CapturePlans {
						before, after := rt.Collector.PlansAround(res.QueryID, res.DetectedChangeAt)
						res.PlanDiffSummary = planner.Diff(before, after)
					}

					notifyCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					if err := rt.Notifier.Notify(notifyCtx, res); err != nil {
						r.Logger.Error("postgreswatch: alert notify failed", "watch", key.String(), "regression", res.Name, "err", err)
					}
					cancel()

					recCtx, recCancel := context.WithTimeout(context.Background(), 15*time.Second)
					if err := r.recordRegression(recCtx, key, res); err != nil {
						r.Logger.Error("postgreswatch: failed to persist PerformanceRegression CR", "watch", key.String(), "regression", res.Name, "err", err)
					}
					recCancel()
				}
			}
		}
	}
}

// recordRegression creates (or updates, on retry) a PerformanceRegression
// CR for a detected regression, owned by the PostgresWatch so it is
// garbage-collected if the watch is deleted.
func (r *PostgresWatchReconciler) recordRegression(ctx context.Context, watchKey types.NamespacedName, res dto.PerformanceRegression) error {
	var owner radarv1alpha1.PostgresWatch
	hasOwner := r.Get(ctx, watchKey, &owner) == nil

	name := regressionResourceName(res.DeployEventID, res.QueryID)
	nsName := types.NamespacedName{Namespace: watchKey.Namespace, Name: name}

	var existing radarv1alpha1.PerformanceRegression
	err := r.Get(ctx, nsName, &existing)
	switch {
	case apierrors.IsNotFound(err):
		obj := &radarv1alpha1.PerformanceRegression{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: watchKey.Namespace,
				Labels: map[string]string{
					"radar.pgregressionradar.io/postgres-watch": watchKey.Name,
				},
			},
			Spec: radarv1alpha1.PerformanceRegressionSpec{
				PostgresWatchRef: watchKey.Name,
				DeployEventID:    res.DeployEventID,
				QueryID:          res.QueryID,
				QueryText:        res.QueryText,
			},
		}
		if hasOwner {
			obj.Spec.ClusterName = owner.Spec.ClusterName
			if err := controllerutil.SetControllerReference(&owner, obj, r.Scheme); err != nil {
				return fmt.Errorf("set owner reference: %w", err)
			}
		}
		if err := r.Create(ctx, obj); err != nil {
			return fmt.Errorf("create performanceregression: %w", err)
		}
		applyRegressionStatus(obj, res)
		if err := r.Status().Update(ctx, obj); err != nil {
			return fmt.Errorf("update performanceregression status: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get performanceregression: %w", err)
	default:
		applyRegressionStatus(&existing, res)
		return r.Status().Update(ctx, &existing)
	}
}

func applyRegressionStatus(obj *radarv1alpha1.PerformanceRegression, res dto.PerformanceRegression) {
	now := metav1.NewTime(res.CreatedAt)
	obj.Status = radarv1alpha1.PerformanceRegressionStatus{
		Status:                 radarv1alpha1.RegressionStatus(res.Status),
		ConfidenceScore:        strconv.FormatFloat(res.ConfidenceScore, 'f', 4, 64),
		MeanLatencyBeforeMs:    strconv.FormatFloat(res.MeanLatencyBefore, 'f', 4, 64),
		MeanLatencyAfterMs:     strconv.FormatFloat(res.MeanLatencyAfter, 'f', 4, 64),
		LatencyChangeFactor:    strconv.FormatFloat(res.LatencyChangeFactor, 'f', 4, 64),
		ExternalCauseSuspected: res.ExternalCauseSuspected,
		PlanDiffSummary:        res.PlanDiffSummary,
		DetectedAt:             &now,
		Conditions:             obj.Status.Conditions,
	}
	setCondition(&obj.Status.Conditions, metav1.Condition{
		Type:    "Detected",
		Status:  metav1.ConditionTrue,
		Reason:  "RegressionDetected",
		Message: fmt.Sprintf("query %d latency changed by %.2fx (confidence %.0f%%)", res.QueryID, res.LatencyChangeFactor, res.ConfidenceScore*100),
	})
}

// SetupWithManager wires this reconciler into mgr.
func (r *PostgresWatchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Registers the remote client cache's periodic eviction sweep (see
	// remote_client.go's Start) as a manager-owned background task, with
	// the same start/stop lifecycle and leader-election gating every other
	// piece of this reconciler's work already has. Calling getRemoteClients
	// here (rather than waiting for the first remoteClusterSecretRef use)
	// means the eviction loop is always running, regardless of whether any
	// PostgresWatch ever actually uses a remote cluster.
	if err := mgr.Add(r.getRemoteClients()); err != nil {
		return fmt.Errorf("register remote client cache eviction loop: %w", err)
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&radarv1alpha1.PostgresWatch{}).
		Named("postgreswatch").
		Complete(r)
}

// ----- helpers -----

// hashPostgresWatchSpec fingerprints the parts of a PostgresWatch that
// affect the running worker's configuration, including the resolved DSN
// (so rotating a referenced Secret's value also triggers a restart on the
// next reconcile, even though DSNSecretRef itself didn't change).
func hashPostgresWatchSpec(spec radarv1alpha1.PostgresWatchSpec, resolvedDSN string) string {
	spec.DSN = resolvedDSN
	spec.DSNSecretRef = nil
	spec.RemoteClusterSecretRef = nil
	b, _ := json.Marshal(spec)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func parseFloatOr(s string, fallback float64) float64 {
	if s == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fallback
	}
	return v
}

var invalidNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

// regressionResourceName derives a valid, deterministic Kubernetes object
// name from a deploy event ID and query ID so re-analysing the same
// (event, query) pair is idempotent (Create-or-Update rather than
// duplicating objects).
func regressionResourceName(deployEventID string, queryID int64) string {
	base := strings.ToLower(deployEventID)
	base = invalidNameChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "event"
	}

	suffix := fmt.Sprintf("-q%d", queryID)
	maxBaseLen := 253 - len(suffix)
	if len(base) > maxBaseLen {
		base = base[:maxBaseLen]
	}
	return strings.Trim(base, "-") + suffix
}

// setCondition upserts cond into conditions by Type, stamping
// LastTransitionTime when the status actually changes.
func setCondition(conditions *[]metav1.Condition, cond metav1.Condition) {
	now := metav1.Now()
	for i := range *conditions {
		if (*conditions)[i].Type == cond.Type {
			if (*conditions)[i].Status != cond.Status {
				cond.LastTransitionTime = now
			} else {
				cond.LastTransitionTime = (*conditions)[i].LastTransitionTime
			}
			(*conditions)[i] = cond
			return
		}
	}
	if cond.LastTransitionTime.IsZero() {
		cond.LastTransitionTime = now
	}
	*conditions = append(*conditions, cond)
}
