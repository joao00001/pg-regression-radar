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
	pending []*pendingEvent
}

type pendingEvent struct {
	ev       v1alpha1.DeployEvent
	deadline time.Time
	notified map[int64]struct{}
}

// NewPendingSet creates a PendingSet backed by engine. engine's own
// AnalysisWindow determines how long a deploy event stays under active
// retry.
func NewPendingSet(engine *Engine) *PendingSet {
	return &PendingSet{engine: engine}
}

// Add registers a newly-arrived deploy event for (repeated) analysis. It is
// safe to call this for every event DrainSince returns, in arrival order.
func (p *PendingSet) Add(ev v1alpha1.DeployEvent) {
	p.pending = append(p.pending, &pendingEvent{
		ev:       ev,
		deadline: ev.Timestamp.Add(p.engine.AnalysisWindow()),
		notified: make(map[int64]struct{}),
	})
}

// Tick re-analyses every still-pending event and returns every regression
// that has newly reached StatusDetected since the last Tick — the set a
// caller should notify/persist right now. Events whose post-deploy window
// has fully elapsed as of this call are retired afterward (win or lose):
// past that point Analyse would see the exact same data on every future
// call, so continuing to retry would be pure waste, not a missed chance at
// a different outcome.
func (p *PendingSet) Tick() []v1alpha1.PerformanceRegression {
	now := time.Now().UTC()
	var detected []v1alpha1.PerformanceRegression
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
			detected = append(detected, r)
		}

		if now.Before(pe.deadline) {
			live = append(live, pe)
		}
		// Past its deadline: pe is dropped from the next Tick's pending
		// list. Any query that reached Detected already made it into
		// `detected` above (on this call or an earlier one); anything still
		// NoRegression/InsufficientData at the deadline stays that way
		// forever, since no more post-deploy data will ever arrive for it.
	}

	p.pending = live
	return detected
}

// Len reports how many deploy events are still under active retry —
// exposed for tests and metrics, not needed for normal operation.
func (p *PendingSet) Len() int {
	return len(p.pending)
}
