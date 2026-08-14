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

// PeriodicTracker wraps Engine.AnalysePeriodic with re-arm suppression: a
// deploy-triggered regression has a natural end to its lifecycle (the
// deploy's post-deploy window elapses — see PendingSet), but a periodic
// analysis pass runs forever on a plain ticker, with no such boundary. Without
// suppression, a query that stays regressed would be reported as "newly
// detected" again on every single tick for as long as the condition persists.
//
// PeriodicTracker instead tracks, per query, whether it is currently
// "in regression": the first tick that finds it Detected reports it and
// marks it suppressed; every later tick that still finds it Detected is
// treated as the same, already-reported episode and is not reported again;
// the first tick that finds it anything other than Detected (NoRegression or
// InsufficientData) is treated as recovery, clearing the suppression so a
// later, genuinely new episode can be reported again.
//
// Unlike PendingSet, this state is not scoped to a single event's lifetime —
// it lives for as long as the PeriodicTracker itself does, which in practice
// means for the life of the owning WatchRuntime/CLI process. It resets on
// restart, same as the rest of this project's in-memory-by-default runtime
// state (Collector, PendingSet) — a query already mid-episode at restart
// time will simply be reported again on the next tick that still finds it
// Detected, rather than staying silently suppressed forever.
type PeriodicTracker struct {
	engine       *Engine
	logger       *slog.Logger
	inRegression map[int64]bool
}

// NewPeriodicTracker creates a PeriodicTracker backed by engine. A nil
// logger falls back to slog.Default(), matching Engine's and PendingSet's
// own New/NewPendingSet.
func NewPeriodicTracker(engine *Engine, logger *slog.Logger) *PeriodicTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &PeriodicTracker{
		engine:       engine,
		logger:       logger,
		inRegression: make(map[int64]bool),
	}
}

// Tick runs one AnalysePeriodic pass anchored at now and returns only the
// regressions that just transitioned from "not alerting" to "alerting" for
// their query — genuinely new episodes, not still-ongoing ones already
// reported on an earlier Tick. Callers should notify/persist directly on
// Tick's return value, the same way they do with PendingSet.Tick's.
func (p *PeriodicTracker) Tick(now time.Time) []v1alpha1.PerformanceRegression {
	var newlyDetected []v1alpha1.PerformanceRegression

	for _, r := range p.engine.AnalysePeriodic(now) {
		if r.Status == v1alpha1.StatusDetected {
			if !p.inRegression[r.QueryID] {
				p.inRegression[r.QueryID] = true
				newlyDetected = append(newlyDetected, r)
				p.logger.Info("correlation: periodic regression newly detected, suppressing until recovery",
					"query_id", r.QueryID,
					"confidence", r.ConfidenceScore)
			}
			// Already in regression: same ongoing episode, already reported
			// on an earlier tick — suppressed, not re-added to newlyDetected.
			continue
		}

		// NoRegression or InsufficientData this tick: if this query was
		// suppressed, it has now recovered — re-arm it so a later, genuinely
		// new episode can be reported again. InsufficientData counts as
		// recovery here (not "still regressed, just unmeasured") because,
		// unlike deploy-triggered analysis, a periodic window has no fixed
		// deadline past which stale data stops mattering — it simply slides
		// forward every tick, so InsufficientData here means the window
		// genuinely doesn't have enough samples right now, not that a
		// verdict is still pending.
		if p.inRegression[r.QueryID] {
			p.logger.Info("correlation: periodic regression recovered, re-arming",
				"query_id", r.QueryID,
				"status", r.Status)
			delete(p.inRegression, r.QueryID)
		}
	}

	return newlyDetected
}

// Len reports how many queries are currently suppressed (in an ongoing,
// already-reported regression episode) — exposed for tests and metrics, not
// needed for normal operation.
func (p *PeriodicTracker) Len() int {
	return len(p.inRegression)
}
