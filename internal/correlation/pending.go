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

package correlation

import (
	"log/slog"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// PendingSet fixes a real gap in how Analyse used to be called: both
// internal/cli.RunOperator's poll loop and
// internal/controller.PostgresWatchReconciler's pollLoop used to call
// Analyse(ev) exactly once, within a few seconds of a deploy event arriving,
// and discard the result whether or not it was Detected. In realistic
// production timing — a real ArgoCD/Flux/Rollouts webhook fires the moment a
// deploy completes, and the default --scrape-interval/--min-executions need
// several minutes to accumulate enough post-deploy samples — that one early
// attempt almost always sees StatusInsufficientData, and since nothing ever
// retried, a real regression could go completely unreported. This was caught
// by actually running the operator binary against a real PostgreSQL instance
// and a real webhook POST, not by the existing test suite: the e2e
// "full pipeline" test calls Engine.Analyse directly after already
// generating all of the post-deploy sample data, which never exercises the
// real poll loop's "analyse once, immediately" timing at all.
//
// PendingSet keeps a deploy event under active re-analysis until its
// post-deploy window has fully elapsed (Engine.AnalysisWindow after
// ev.Timestamp) — the point past which no further real sample could ever
// arrive for it — retrying on every Tick in between. A query is only ever
// reported once it first reaches StatusDetected, and only once per event
// even if later ticks (redundantly) find it Detected again, so a caller can
// notify/persist directly on Tick's return value without its own dedup
// bookkeeping.
type PendingSet struct {
	engine  *Engine
	logger  *slog.Logger
	pending []*pendingEvent
}

type pendingEvent struct {
	ev       v1alpha1.DeployEvent
	deadline time.Time
	notified map[int64]struct{}
}

// TickResult pairs a newly-detected regression with the deploy event that
// produced it. Tick used to return just the []v1alpha1.PerformanceRegression
// slice, but callers that want to act on *which deploy* regressed — not
// just which query — need more than PerformanceRegression carries: it has
// no App/Namespace/Source field, only a DeployEventID string. The
// motivating caller is internal/controller.PostgresWatchReconciler's
// pollLoop, which needs Event.Source (the originating DeploySource's name)
// and Event.App/Event.Namespace to decide whether, and what, to
// auto-abort — see internal/actuation.
type TickResult struct {
	Event      v1alpha1.DeployEvent
	Regression v1alpha1.PerformanceRegression
}

// NewPendingSet creates a PendingSet backed by engine. engine's own
// AnalysisWindow determines how long a deploy event stays under active
// retry. logger receives this PendingSet's own lifecycle events —
// registration and retirement of a deploy event — at Info level, so an
// operator watching logs can always answer "what ultimately happened with
// deploy X" without having to infer it from the absence of a
// "regression detected" line; a nil logger falls back to slog.Default(),
// matching Engine's own New.
func NewPendingSet(engine *Engine, logger *slog.Logger) *PendingSet {
	if logger == nil {
		logger = slog.Default()
	}
	return &PendingSet{engine: engine, logger: logger}
}

// Add registers a newly-arrived deploy event for (repeated) analysis. It is
// safe to call this for every event DrainSince returns, in arrival order.
func (p *PendingSet) Add(ev v1alpha1.DeployEvent) {
	deadline := ev.Timestamp.Add(p.engine.AnalysisWindow())
	p.pending = append(p.pending, &pendingEvent{
		ev:       ev,
		deadline: deadline,
		notified: make(map[int64]struct{}),
	})
	p.logger.Info("correlation: deploy event registered for retry",
		"event_id", ev.ID,
		"deploy_at", ev.Timestamp,
		"retry_until", deadline)
}

// Tick re-analyses every still-pending event and returns every regression
// that has newly reached StatusDetected since the last Tick, paired with
// its originating deploy event (see TickResult) — the set a caller should
// notify/persist (and, if configured, consider auto-aborting) right now.
// Events whose post-deploy window has fully elapsed as of this call are
// retired afterward (win or lose):
// past that point Analyse would see the exact same data on every future
// call, so continuing to retry would be pure waste, not a missed chance at
// a different outcome.
func (p *PendingSet) Tick() []TickResult {
	now := time.Now().UTC()
	var detected []TickResult
	live := p.pending[:0]

	for _, pe := range p.pending {
		for _, r := range p.engine.Analyse(pe.ev) {
			if r.Status != v1alpha1.StatusDetected {
				continue
			}
			if _, already := pe.notified[r.QueryID]; already {
				continue
			}
			pe.notified[r.QueryID] = struct{}{}
			detected = append(detected, TickResult{Event: pe.ev, Regression: r})
		}

		if now.Before(pe.deadline) {
			live = append(live, pe)
			continue
		}
		// Past its deadline: pe is retired here and dropped from the next
		// Tick's pending list. Any query that reached Detected already made
		// it into `detected` above (on this call or an earlier one);
		// anything still NoRegression/InsufficientData at the deadline
		// stays that way forever, since no more post-deploy data will ever
		// arrive for it. Logging this explicitly closes the same
		// observability gap Add's registration log opens: without it, a
		// deploy event that never regressed would simply stop appearing in
		// the logs with no line marking that it was, in fact, fully
		// evaluated rather than lost to a crash or a silent bug.
		p.logger.Info("correlation: deploy event's analysis window elapsed",
			"event_id", pe.ev.ID,
			"queries_detected", len(pe.notified))
	}

	p.pending = live
	return detected
}

// Len reports how many deploy events are still under active retry —
// exposed for tests and metrics, not needed for normal operation.
func (p *PendingSet) Len() int {
	return len(p.pending)
}
