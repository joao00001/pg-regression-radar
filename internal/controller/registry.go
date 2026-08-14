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
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
)

// RolloutAborter is the narrow interface pollLoop needs to auto-abort an
// Argo Rollouts canary; *internal/actuation.ArgoRolloutsAborter satisfies
// it structurally. Declared here (the consumer), not in internal/actuation,
// so controller tests can supply a stub without importing
// k8s.io/client-go/dynamic at all.
type RolloutAborter interface {
	Abort(ctx context.Context, namespace, name string) error
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
	// Store holds DeployEvents ingested by any DeploySource bound to this
	// watch. DeploySourceReconciler looks up the Store for its
	// postgresWatchRef and wires an ingester.Handler on top of it.
	Store *ingester.Store

	// Collector scrapes pg_stat_statements for this watch's target
	// database on its own goroutine (started by the reconciler).
	Collector *collector.Collector

	// Engine analyses DeployEvents against Collector's samples.
	Engine *correlation.Engine

	// Notifier fires Slack/webhook alerts for detected regressions.
	Notifier *alerting.WebhookNotifier

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

	// Cancel stops the Collector's scrape loop and the analysis poll loop.
	Cancel func()

	// ClusterName is copied from the spec for use when building
	// PerformanceRegression CRs without re-reading the PostgresWatch.
	ClusterName string

	// CapturePlans mirrors the owning PostgresWatch's spec.capturePlans —
	// copied here (rather than re-read from the CR on every poll
	// iteration) so pollLoop can cheaply decide whether to call
	// Collector.PlansAround for a detected regression.
	CapturePlans bool

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
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{runtimes: make(map[types.NamespacedName]*WatchRuntime)}
}

// Set stores (or replaces) the runtime for key.
func (r *Registry) Set(key types.NamespacedName, rt *WatchRuntime) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes[key] = rt
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
