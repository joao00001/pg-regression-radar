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

package correlation_test

import (
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// TestAnalysePeriodic_DetectsRegression proves the split-window core itself:
// with no DeployEvent at all, a query whose latency shifts partway through a
// single rolling window is reported Detected, with TriggerType=Periodic and
// no DeployEventID — there being no deploy to reference at all (see
// ADR-0001).
func TestAnalysePeriodic_DetectsRegression(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	step := time.Minute
	const windowMinutes = 10

	// Window is [now-20m, now]: older half (now-20m..now-10m) fast, newer
	// half (now-10m..now) slow.
	older := buildSamples(321, "SELECT 1", now.Add(-2*windowMinutes*step), step, windowMinutes, 10.0)
	newer := buildSamples(321, "SELECT 1", now.Add(-windowMinutes*step+step), step, windowMinutes, 50.0)

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{321: append(older, newer...)}}
	engine := correlation.New(correlation.Config{
		Namespace:              "production",
		PeriodicWindowMinutes:  windowMinutes,
		MinExecutions:          5,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, src, nil)

	results := engine.AnalysePeriodic(now)

	var found *v1alpha1.PerformanceRegression
	for i := range results {
		if results[i].QueryID == 321 {
			found = &results[i]
		}
	}
	if found == nil {
		t.Fatal("no result for query 321")
	}
	if found.Status != v1alpha1.StatusDetected {
		t.Fatalf("expected Detected, got %s", found.Status)
	}
	if found.TriggerType != v1alpha1.TriggerTypePeriodic {
		t.Errorf("expected TriggerType=periodic, got %q", found.TriggerType)
	}
	if found.Namespace != "production" {
		t.Errorf("expected Namespace=production, got %q", found.Namespace)
	}
	if found.DeployEventID != "" {
		t.Errorf("expected empty DeployEventID for a periodic regression, got %q", found.DeployEventID)
	}
	if found.LatencyChangeFactor < 2.0 {
		t.Errorf("expected change factor >= 2.0, got %.2f", found.LatencyChangeFactor)
	}
}

// TestAnalysePeriodic_NoRegression proves periodic detection doesn't false
// positive on a genuinely flat window — the same false-positive discipline
// deploy-triggered detection already has.
func TestAnalysePeriodic_NoRegression(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	step := time.Minute
	const windowMinutes = 10

	samples := buildSamples(322, "SELECT 1", now.Add(-2*windowMinutes*step), step, 2*windowMinutes, 12.0)

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{322: samples}}
	engine := correlation.New(correlation.Config{
		PeriodicWindowMinutes:  windowMinutes,
		MinExecutions:          5,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, src, nil)

	results := engine.AnalysePeriodic(now)
	for _, r := range results {
		if r.QueryID == 322 && r.Status == v1alpha1.StatusDetected {
			t.Errorf("false positive: expected NoRegression on a flat window, got Detected (factor=%.2f)", r.LatencyChangeFactor)
		}
	}
}

// TestPeriodicTracker_FireSuppressRecoverRefire is the regression test for
// the re-arm state machine ADR-0001 chose over a fixed cooldown: one alert
// per distinct episode, no re-firing on every tick a still-ongoing
// regression continues to be observed, and re-arming once the query
// recovers so a later, genuinely new episode fires again.
func TestPeriodicTracker_FireSuppressRecoverRefire(t *testing.T) {
	t.Parallel()

	const queryID = int64(555)
	const windowMinutes = 10
	step := time.Minute

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{}}
	engine := correlation.New(correlation.Config{
		PeriodicWindowMinutes:  windowMinutes,
		MinExecutions:          5,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, src, nil)
	tracker := correlation.NewPeriodicTracker(engine, nil)

	flatWindow := func(tick time.Time, latency float64) []collector.QuerySample {
		return buildSamples(queryID, "SELECT 1", tick.Add(-2*windowMinutes*step), step, 2*windowMinutes, latency)
	}
	regressedWindow := func(tick time.Time) []collector.QuerySample {
		older := buildSamples(queryID, "SELECT 1", tick.Add(-2*windowMinutes*step), step, windowMinutes, 10.0)
		newer := buildSamples(queryID, "SELECT 1", tick.Add(-windowMinutes*step+step), step, windowMinutes, 50.0)
		return append(older, newer...)
	}

	base := time.Now().UTC()

	// Tick 1: flat, healthy baseline — nothing to report, nothing suppressed.
	tick1 := base.Add(time.Duration(2*windowMinutes) * step)
	src.data[queryID] = flatWindow(tick1, 10.0)
	if got := tracker.Tick(tick1); len(got) != 0 {
		t.Fatalf("tick 1: expected no newly-detected regressions on a healthy baseline, got %d", len(got))
	}
	if tracker.Len() != 0 {
		t.Fatalf("tick 1: expected Len()=0, got %d", tracker.Len())
	}

	// Tick 2: regression appears — must fire exactly once and start
	// suppressing.
	tick2 := tick1.Add(step)
	src.data[queryID] = regressedWindow(tick2)
	got := tracker.Tick(tick2)
	if len(got) != 1 {
		t.Fatalf("tick 2: expected exactly 1 newly-detected regression, got %d: %+v", len(got), got)
	}
	if got[0].QueryID != queryID {
		t.Errorf("tick 2: expected query id %d, got %d", queryID, got[0].QueryID)
	}
	if got[0].TriggerType != v1alpha1.TriggerTypePeriodic {
		t.Errorf("tick 2: expected TriggerType=periodic, got %q", got[0].TriggerType)
	}
	if tracker.Len() != 1 {
		t.Fatalf("tick 2: expected Len()=1 (suppressed), got %d", tracker.Len())
	}

	// Tick 3: same ongoing regression — must NOT re-fire, but must remain
	// suppressed.
	tick3 := tick2.Add(step)
	src.data[queryID] = regressedWindow(tick3)
	if got := tracker.Tick(tick3); len(got) != 0 {
		t.Fatalf("tick 3: expected no re-fire for an already-reported, still-ongoing regression, got %d: %+v", len(got), got)
	}
	if tracker.Len() != 1 {
		t.Fatalf("tick 3: expected the query to remain suppressed, got Len()=%d", tracker.Len())
	}

	// Tick 4: recovered — must re-arm (Len back to 0), without itself being
	// reported as a newly-detected regression.
	tick4 := tick3.Add(step)
	src.data[queryID] = flatWindow(tick4, 10.0)
	if got := tracker.Tick(tick4); len(got) != 0 {
		t.Fatalf("tick 4: recovery itself must not be reported as a new regression, got %d: %+v", len(got), got)
	}
	if tracker.Len() != 0 {
		t.Fatalf("tick 4: expected the query to re-arm (Len()=0) once recovered, got %d", tracker.Len())
	}

	// Tick 5: a genuinely new episode — must fire again now that it's
	// re-armed.
	tick5 := tick4.Add(step)
	src.data[queryID] = regressedWindow(tick5)
	got = tracker.Tick(tick5)
	if len(got) != 1 {
		t.Fatalf("tick 5: expected the re-armed query to fire again for a new episode, got %d: %+v", len(got), got)
	}
	if tracker.Len() != 1 {
		t.Fatalf("tick 5: expected Len()=1 (suppressed again), got %d", tracker.Len())
	}
}
