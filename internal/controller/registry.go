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

// Package controller contains the controller-runtime reconcilers that turn
// PostgresWatch, DeploySource, and PerformanceRegression CRs into running
// instances of the pre-existing, CRD-agnostic engine packages
// (internal/collector, internal/correlation, internal/ingester,
// internal/alerting). None of those packages are modified here; this layer
// only orchestrates them from Kubernetes state.
package controller

import (
	"context"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/types"

	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/attribution/revert"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/internal/observe"
	"github.com/joao00001/pg-regression-radar/internal/telemetry/otlpexport"
	apidto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// RolloutAborter is the narrow interface pollLoop needs to auto-abort an
// Argo Rollouts canary; *internal/actuation.ArgoRolloutsAborter satisfies
// it structurally. Declared here (the consumer), not in internal/actuation,
// so controller tests can supply a stub without importing
// k8s.io/client-go/dynamic at all.
type RolloutAborter interface {
	Abort(ctx context.Context, namespace, name string) error
}

// RolloutPauser is the second narrow interface the attribution engine needs:
// holding a canary without aborting it. *actuation.ArgoRolloutsAborter satisfies
// it structurally, next to RolloutAborter.
type RolloutPauser interface {
	Pause(ctx context.Context, namespace, name string) error
}

// ActuateRuntime is a watch's permission to act on the attribution engine's
// decisions, and the means to do it. A nil *ActuateRuntime means the engine only
// reports and serves verdicts.
type ActuateRuntime struct {
	Aborter RolloutAborter
	Pauser  RolloutPauser
	// IsRollout reports whether the deploy came from an argo-rollouts
	// DeploySource, the only kind with a pause or abort to call. Anything else
	// is left alone.
	IsRollout func(ctx context.Context, ev apidto.DeployEvent) bool
	// Namespaces limits actuation to Rollouts in these namespaces; empty means
	// any (attribution.actuate.namespaces).
	Namespaces []string
	// Paused reports whether an operator has switched actuation off with the
	// pg-regression-radar.io/actuation-paused annotation on the PostgresWatch.
	// It is read at the moment of acting, so the switch takes effect at once.
	Paused func(ctx context.Context) bool
}

// WatchRuntime bundles the running background components that back a single
// PostgresWatch CR: a Collector scraping pg_stat_statements, a Correlation
// Engine analysing deploys against the Collector's samples, a Store of
// ingested DeployEvents fed by any DeploySource CRs that reference this
// watch, and a WebhookNotifier for Slack alerts.
//
// A WatchRuntime is created by PostgresWatchReconciler when a PostgresWatch
// is created (or its spec changes) and torn down via its cancel func when
// the CR is deleted or the spec changes again.
type WatchRuntime struct {
	// Arms reads the Postgres log for attribution.arms; nil when it is not
	// configured or cannot work.
	Arms *ArmsRuntime

	// Store holds DeployEvents ingested by any DeploySource bound to this
	// watch. DeploySourceReconciler looks up the Store for its
	// postgresWatchRef and wires an ingester.Handler on top of it.
	Store *ingester.Store

	// Collector scrapes pg_stat_statements for this watch's target
	// database on its own goroutine (started by the reconciler). nil when
	// spec.sampleSource selects an alternative implementation (currently
	// only "prometheus") instead — see SampleSource below, which is
	// always set regardless of which one backs it, and CapturePlans's own
	// doc comment for why that combination is rejected before either
	// field would need to handle it.
	Collector *collector.Collector

	// SampleSource is whichever correlation.SampleSource implementation
	// actually backs Engine below — Collector when spec.sampleSource is
	// unset (the default), or the alternative implementation spec.sampleSource
	// selects otherwise. Code that only needs SampleSource's two-method
	// interface (e.g. refreshStatus's AllQueryIDs call) should use this
	// field so it keeps working regardless of which one is active; use
	// Collector directly only for collector.Collector-specific behavior
	// (LastScrapeTime, PlansAround) that has no equivalent on other
	// SampleSource implementations.
	SampleSource correlation.SampleSource

	// Engine analyses DeployEvents against SampleSource's samples.
	Engine *correlation.Engine

	// Notifier fires Slack/webhook alerts for detected regressions.
	Notifier *alerting.WebhookNotifier

	// OTLPExporter publishes detected regressions as OTLP log records when
	// spec.otlpExport is set; nil otherwise. Closed (flushed) by Cancel.
	OTLPExporter *otlpexport.Exporter

	// PromRegistry is a private Prometheus registry for this watch's
	// Collector metrics. Each watch gets its own registry (rather than
	// sharing one process-wide registry) so that stopping and restarting a
	// watch on spec change never collides with "duplicate metrics
	// collector registration" errors from a previous incarnation whose
	// metrics hadn't been unregistered yet.
	PromRegistry *prometheus.Registry

	// SpecHash fingerprints the PostgresWatchSpec this runtime was built
	// from, so the reconciler can detect spec changes cheaply.
	SpecHash string

	// breakerResetSeen is the last value of the reset-breaker annotation the
	// reconciler looked at. A value that differs from it asks for the circuit
	// breaker to be closed. It starts as the annotation's value when the
	// runtime is built, so one left over from before a restart does nothing.
	// Only the PostgresWatch reconciler reads or writes it.
	breakerResetSeen string

	// Cancel stops the Collector's scrape loop and the analysis poll loop.
	Cancel func()

	// ClusterName is copied from the spec for use when building
	// PerformanceRegression CRs without re-reading the PostgresWatch.
	ClusterName string

	// CapturePlans mirrors the owning PostgresWatch's spec.capturePlans —
	// copied here (rather than re-read from the CR on every poll
	// iteration) so pollLoop can cheaply decide whether to call
	// Collector.PlansAround for a detected regression. startWatch rejects
	// spec.capturePlans: true combined with a non-collector
	// spec.sampleSource before a WatchRuntime is ever built, so
	// CapturePlans true here is a guarantee that Collector above is
	// non-nil, not just a likelihood.
	CapturePlans bool

	// Shadow is the attribution evaluator (shadow or enforce, per the watch), set when the
	// owning PostgresWatch has spec.attribution.enabled and the watch scrapes
	// its own Collector. nil otherwise. It only reports; acting is Actuate's job.
	Shadow *shadow.Evaluator

	// ObserveNamespaces is attribution.actuate.namespaces, copied whether or not
	// actuation is enabled: the dashboard shows this watch's deploys only in these
	// namespaces. Empty means every namespace.
	ObserveNamespaces []string

	// Revert is the draft-revert proposal for this watch, set only when
	// spec.attribution.revert is enabled and valid. nil otherwise.
	Revert *RevertRuntime

	// Actuate is non-nil only when attribution.actuate.enabled is set, the watch
	// is in enforce mode, and this manager has the means to act (an Aborter
	// that can also pause). See ActuateRuntime.
	Actuate *ActuateRuntime

	// AutoAbortEnabled mirrors the owning PostgresWatch's
	// spec.autoAbort.enabled. False (the default) means pollLoop never
	// calls Aborter, regardless of whether one is configured.
	AutoAbortEnabled bool

	// AutoAbortThreshold mirrors spec.autoAbort.confidenceThreshold
	// (parsed, defaulted to 0.99). Only consulted when AutoAbortEnabled.
	AutoAbortThreshold float64

	// Aborter performs the actual abort call when pollLoop decides a
	// detected regression is confident enough. Shared across every
	// WatchRuntime (it is not Postgres-cluster-specific) — set from
	// PostgresWatchReconciler.Aborter, which is nil unless cmd/manager was
	// able to build a Kubernetes dynamic client, in which case
	// AutoAbortEnabled is simply never actionable regardless of what any
	// PostgresWatch's spec says.
	Aborter RolloutAborter

	// PeriodicTracker runs deploy-independent regression detection on its
	// own ticker (see periodicPollLoop) when PeriodicEnabled is true; nil
	// otherwise. Mirrors AutoAbortEnabled's pattern: PeriodicEnabled is the
	// single flag pollLoop's sibling goroutine checks, so a nil tracker
	// here is never dereferenced when the watch didn't opt in — see
	// docs/periodic-detection.md.
	PeriodicTracker *correlation.PeriodicTracker

	// PeriodicEnabled mirrors the owning PostgresWatch's
	// spec.periodicDetection.enabled.
	PeriodicEnabled bool

	// PeriodicIntervalMinutes mirrors spec.periodicDetection.intervalMinutes
	// (parsed, defaulted to 15). Only consulted when PeriodicEnabled.
	PeriodicIntervalMinutes int
}

// Registry tracks the live WatchRuntime for every reconciled PostgresWatch,
// keyed by namespaced name. DeploySourceReconciler reads it (read-only) to
// find the Store to feed; PostgresWatchReconciler owns writes.
type Registry struct {
	mu       sync.RWMutex
	runtimes map[types.NamespacedName]*WatchRuntime

	// Events carries what happens to deploys to the live dashboard. It is never
	// nil, and publishing to it never blocks.
	Events *observe.Hub
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{runtimes: make(map[types.NamespacedName]*WatchRuntime), Events: observe.NewHub(0)}
}

// Set stores (or replaces) the runtime for key.
func (r *Registry) Set(key types.NamespacedName, rt *WatchRuntime) {
	if rt != nil && rt.Shadow != nil {
		r.hook(rt.Shadow)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes[key] = rt
}

// hook publishes an evaluator's doings to the event hub.
func (r *Registry) hook(ev *shadow.Evaluator) {
	ev.Hooks = shadow.Hooks{
		OnRegister: func(d shadow.Deploy) {
			r.Events.Publish(observe.Event{Type: observe.EventDeployRegistered, Namespace: d.Namespace, DeployID: d.ID,
				Summary: d.App + " deployed"})
		},
		OnEvaluate: func(d shadow.Deploy, rec shadow.Record) {
			r.Events.Publish(observe.Event{Type: observe.EventEvaluation, Namespace: d.Namespace, DeployID: d.ID,
				Summary: "judged: " + string(rec.Assessment.Health) + ", grade " + string(rec.Decision.Grade)})
		},
		OnAction: func(a shadow.ActionRecord) {
			r.Events.Publish(observe.Event{Type: observe.EventAction, Namespace: a.Namespace, DeployID: a.DeployID,
				Summary: a.Action + " " + a.Result})
		},
	}
}

// Each calls fn for every watch, in no particular order, with the registry
// unlocked, so fn may take its time.
func (r *Registry) Each(fn func(key types.NamespacedName, rt *WatchRuntime)) {
	r.mu.RLock()
	keys := make([]types.NamespacedName, 0, len(r.runtimes))
	rts := make([]*WatchRuntime, 0, len(r.runtimes))
	for k, rt := range r.runtimes {
		keys = append(keys, k)
		rts = append(rts, rt)
	}
	r.mu.RUnlock()
	for i := range keys {
		if rts[i] != nil {
			fn(keys[i], rts[i])
		}
	}
}

// Get returns the runtime for key, if any.
func (r *Registry) Get(key types.NamespacedName) (*WatchRuntime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.runtimes[key]
	return rt, ok
}

// Delete removes the runtime for key without stopping it; callers must call
// rt.Cancel() themselves before (or after) deleting.
func (r *Registry) Delete(key types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runtimes, key)
}

// Len reports how many watches are currently tracked. Useful for tests and
// for a coarse liveness/debug signal.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.runtimes)
}

// Gather implements prometheus.Gatherer by fanning out to every active
// watch's private registry and merging the results. This lets the manager
// expose one aggregated /metrics endpoint for Postgres query metrics even
// though each watch's Collector is registered against its own private
// *prometheus.Registry (see WatchRuntime.PromRegistry doc comment).
func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	r.mu.RLock()
	gatherers := make(prometheus.Gatherers, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		gatherers = append(gatherers, rt.PromRegistry)
	}
	r.mu.RUnlock()
	return gatherers.Gather()
}

// Shadows returns the attribution evaluator of every watch that has one.
func (r *Registry) Shadows() []*shadow.Evaluator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*shadow.Evaluator
	for _, rt := range r.runtimes {
		if rt.Shadow != nil {
			out = append(out, rt.Shadow)
		}
	}
	return out
}

// RevertRuntime is a watch's draft-revert proposal and how it is used.
type RevertRuntime struct {
	Proposer *revert.Proposer
	// Enforce is whether the watch is in enforce mode; otherwise a proposal is
	// only logged.
	Enforce        bool
	IncludeQueries bool
}
