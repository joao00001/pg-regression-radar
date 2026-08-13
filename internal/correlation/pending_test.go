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

// TestPendingSet_RetriesUntilEnoughData is the regression test for the real
// bug this file's sibling (pending.go) fixes: running the actual operator
// binary against a real PostgreSQL instance and a real webhook POST showed
// that a single Analyse call taken immediately on deploy-event arrival
// almost always sees StatusInsufficientData (a real deploy webhook fires the
// moment the deploy completes, before enough post-deploy samples could
// possibly exist), and — since nothing retried — a real regression was
// silently never reported. This proves PendingSet actually closes that gap:
// the first Tick, with too little post-deploy data, reports nothing and
// keeps the event pending; once enough data exists, a later Tick reports it
// exactly once.
func TestPendingSet_RetriesUntilEnoughData(t *testing.T) {
	t.Parallel()

	// deployAt just 10s in the past keeps this event's 1-minute analysis
	// window (WindowMinutes: 1 below) from having elapsed yet at any point
	// during this test, so PendingSet has no reason to retire it early.
	deployAt := time.Now().UTC().Add(-10 * time.Second)
	const queryID = int64(42)
	const queryText = "SELECT pg_sleep($1) /* pgrr_pending_test */"

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{
		queryID: buildSamples(queryID, queryText, deployAt.Add(-time.Minute), time.Second, 10, 10),
	}}

	engine := correlation.New(correlation.Config{
		WindowMinutes:          1,
		MinExecutions:          5,
		LatencyChangeThreshold: 0.20,
	}, src, nil)

	pending := correlation.NewPendingSet(engine)
	pending.Add(v1alpha1.DeployEvent{ID: "dep-1", Timestamp: deployAt})

	// --- Tick 1: only 2 post-deploy samples exist so far (min is 5). ---
	src.data[queryID] = append(src.data[queryID], buildSamples(queryID, queryText, deployAt.Add(time.Second), time.Second, 2, 60)...)

	results := pending.Tick()
	if len(results) != 0 {
		t.Fatalf("tick 1: expected no results with insufficient post-deploy data, got %d: %+v", len(results), results)
	}
	if pending.Len() != 1 {
		t.Fatalf("tick 1: expected the event to remain pending (insufficient data isn't final), got Len()=%d", pending.Len())
	}

	// --- Tick 2: enough post-deploy data has now "arrived" for real. ---
	src.data[queryID] = append(src.data[queryID], buildSamples(queryID, queryText, deployAt.Add(4*time.Second), time.Second, 6, 60)...)

	results = pending.Tick()
	if len(results) != 1 {
		t.Fatalf("tick 2: expected exactly 1 newly-detected regression once enough data exists, got %d: %+v", len(results), results)
	}
	if results[0].Status != v1alpha1.StatusDetected {
		t.Errorf("tick 2: expected Status=Detected, got %s", results[0].Status)
	}
	if results[0].QueryID != queryID {
		t.Errorf("tick 2: expected query id %d, got %d", queryID, results[0].QueryID)
	}

	// --- Tick 3: same data, no new samples — must not re-report. ---
	results = pending.Tick()
	if len(results) != 0 {
		t.Fatalf("tick 3: expected no duplicate notification for an already-reported query, got %d: %+v", len(results), results)
	}
	if pending.Len() != 1 {
		t.Fatalf("tick 3: event's analysis window hasn't elapsed yet, expected it to remain pending, got Len()=%d", pending.Len())
	}
}

// TestPendingSet_RetiresAfterWindowElapses confirms the other half of the
// contract: PendingSet must not retry forever. Once a deploy event's
// post-deploy window has fully elapsed, no future sample could possibly
// change the outcome, so it must be dropped rather than retried on every
// tick indefinitely.
func TestPendingSet_RetiresAfterWindowElapses(t *testing.T) {
	t.Parallel()

	// deployAt 2 minutes in the past, with a 1-minute window, means this
	// event's analysis window (deployAt+1m = 1 minute ago) has already
	// fully elapsed by the time the first Tick runs.
	deployAt := time.Now().UTC().Add(-2 * time.Minute)
	const queryID = int64(7)
	const queryText = "SELECT 1 /* pgrr_pending_expiry_test */"

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{
		queryID: buildSamples(queryID, queryText, deployAt.Add(-time.Minute), time.Second, 10, 10),
		// Deliberately no post-deploy samples: this event can never reach
		// Detected, which is exactly the case that must not retry forever.
	}}

	engine := correlation.New(correlation.Config{
		WindowMinutes:          1,
		MinExecutions:          5,
		LatencyChangeThreshold: 0.20,
	}, src, nil)

	pending := correlation.NewPendingSet(engine)
	pending.Add(v1alpha1.DeployEvent{ID: "dep-2", Timestamp: deployAt})

	results := pending.Tick()
	if len(results) != 0 {
		t.Fatalf("expected no results for a query with no post-deploy data, got %d: %+v", len(results), results)
	}
	if pending.Len() != 0 {
		t.Fatalf("expected the event to be retired once its analysis window elapsed, got Len()=%d", pending.Len())
	}
}

func TestEngine_AnalysisWindow(t *testing.T) {
	t.Parallel()

	engine := correlation.New(correlation.Config{WindowMinutes: 15}, &fakeSampleSource{}, nil)
	if got, want := engine.AnalysisWindow(), 15*time.Minute; got != want {
		t.Errorf("AnalysisWindow() = %s, want %s", got, want)
	}
}
