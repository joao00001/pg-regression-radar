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

package collector

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
)

func approxEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDeriveInterval(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	since := t0.Add(-24 * time.Hour)
	prev := QuerySample{Calls: 1000, TotalExecTimeMs: 10000, StatsSince: since}

	tests := []struct {
		name      string
		cur       QuerySample
		wantReset bool
		wantCalls int64
		wantTotal float64
		wantMean  float64
	}{
		{
			name:      "delta of the counters, not the cumulative mean",
			cur:       QuerySample{Calls: 1100, TotalExecTimeMs: 10000 + 100*50, StatsSince: since},
			wantCalls: 100, wantTotal: 5000, wantMean: 50,
		},
		{
			name:      "no calls in the interval",
			cur:       QuerySample{Calls: 1000, TotalExecTimeMs: 10000, StatsSince: since},
			wantCalls: 0, wantTotal: 0, wantMean: 0,
		},
		{
			name:      "calls went down: counters were reset",
			cur:       QuerySample{Calls: 40, TotalExecTimeMs: 400, StatsSince: since},
			wantReset: true, wantCalls: 40, wantTotal: 400, wantMean: 10,
		},
		{
			name:      "total went down with calls still growing: reset",
			cur:       QuerySample{Calls: 1050, TotalExecTimeMs: 300, StatsSince: since},
			wantReset: true, wantCalls: 1050, wantTotal: 300, wantMean: 300.0 / 1050.0,
		},
		{
			name:      "stats_since changed although counters climbed past the old values",
			cur:       QuerySample{Calls: 5000, TotalExecTimeMs: 90000, StatsSince: t0},
			wantReset: true, wantCalls: 5000, wantTotal: 90000, wantMean: 18,
		},
		{
			name:      "stats_since unknown on one side is not a reset signal",
			cur:       QuerySample{Calls: 1100, TotalExecTimeMs: 10500},
			wantCalls: 100, wantTotal: 500, wantMean: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveInterval(prev, tt.cur)
			if !got.IntervalKnown {
				t.Fatal("IntervalKnown = false, want true")
			}
			if got.ResetDetected != tt.wantReset {
				t.Errorf("ResetDetected = %v, want %v", got.ResetDetected, tt.wantReset)
			}
			if got.IntervalCalls != tt.wantCalls || !approxEqual(got.IntervalTotalMs, tt.wantTotal) || !approxEqual(got.IntervalMeanMs, tt.wantMean) {
				t.Errorf("interval = (%d calls, %.3f ms, mean %.3f), want (%d, %.3f, %.3f)",
					got.IntervalCalls, got.IntervalTotalMs, got.IntervalMeanMs, tt.wantCalls, tt.wantTotal, tt.wantMean)
			}
			if got.IntervalMeanMs < 0 || got.IntervalCalls < 0 {
				t.Errorf("negative interval: %+v", got)
			}
		})
	}
}

func TestIngestSample_DerivesIntervalFromPreviousSnapshot(t *testing.T) {
	col := newTestCollector(t, Config{})
	base := time.Now().UTC().Add(-10 * time.Minute)

	col.ingestSample(base, 7, "SELECT 1", 1_000_000, 10_000_000, 10) // history: 1M calls at 10 ms
	col.ingestSample(base.Add(time.Minute), 7, "SELECT 1", 1_000_100, 10_005_000, 10.005)

	got := col.SamplesInRange(7, base.Add(-time.Second), base.Add(time.Hour))
	if len(got) != 2 {
		t.Fatalf("got %d samples, want 2", len(got))
	}
	if got[0].IntervalKnown {
		t.Error("first sample of a queryid must not claim a known interval: there is nothing to subtract")
	}
	if !got[1].IntervalKnown || got[1].IntervalCalls != 100 || !approxEqual(got[1].IntervalMeanMs, 50) {
		t.Errorf("second sample interval = %+v, want 100 calls at mean 50 ms", got[1])
	}
	// The cumulative mean is kept as reported, and is exactly what hides the 5x slowdown.
	if !approxEqual(got[1].MeanExecTimeMs, 10.005) {
		t.Errorf("MeanExecTimeMs = %v, want it untouched (10.005)", got[1].MeanExecTimeMs)
	}
}

func TestIngestSample_CountsDetectedResets(t *testing.T) {
	col := newTestCollector(t, Config{})
	base := time.Now().UTC().Add(-10 * time.Minute)

	col.ingestSample(base, 1, "SELECT 1", 500, 5000, 10)
	col.ingestSample(base.Add(time.Minute), 1, "SELECT 1", 20, 200, 10) // pg_stat_statements_reset()

	got := col.SamplesInRange(1, base.Add(-time.Second), base.Add(time.Hour))
	if len(got) != 2 || !got[1].ResetDetected || got[1].IntervalCalls != 20 {
		t.Fatalf("expected the second sample to flag a reset with 20 calls since it, got %+v", got)
	}
}

func TestBackfill_RebuildsIntervalsFromCumulativeRows(t *testing.T) {
	col := newTestCollector(t, Config{})
	base := time.Now().UTC().Add(-30 * time.Minute)

	// Deliberately out of order: Backfill sorts before deriving.
	col.Backfill([]QuerySample{
		{QueryID: 3, QueryText: "SELECT 3", Calls: 300, TotalExecTimeMs: 3000, RecordedAt: base.Add(2 * time.Minute)},
		{QueryID: 3, QueryText: "SELECT 3", Calls: 100, TotalExecTimeMs: 1000, RecordedAt: base},
		{QueryID: 3, QueryText: "SELECT 3", Calls: 200, TotalExecTimeMs: 2000, RecordedAt: base.Add(time.Minute)},
		{QueryID: 3, QueryText: "SELECT 3", Calls: 10, TotalExecTimeMs: 400, RecordedAt: base.Add(3 * time.Minute)}, // reset
	})

	got := col.SamplesInRange(3, base.Add(-time.Second), base.Add(time.Hour))
	if len(got) != 4 {
		t.Fatalf("got %d samples, want 4", len(got))
	}
	if got[0].IntervalKnown {
		t.Error("oldest sample has no predecessor and must stay IntervalKnown=false")
	}
	for i := 1; i <= 2; i++ {
		if !got[i].IntervalKnown || got[i].IntervalCalls != 100 || !approxEqual(got[i].IntervalMeanMs, 10) {
			t.Errorf("sample %d interval = %+v, want 100 calls at 10 ms", i, got[i])
		}
	}
	if !got[3].ResetDetected || got[3].IntervalCalls != 10 || !approxEqual(got[3].IntervalMeanMs, 40) {
		t.Errorf("sample 3 = %+v, want a reset with 10 calls at 40 ms", got[3])
	}
}

func TestQueryStatStatements_SelectsStatsSinceOnlyOnPG17Plus(t *testing.T) {
	for _, tt := range []struct {
		hasSince bool
	}{{false}, {true}} {
		t.Run(fmt.Sprintf("hasStatsSince=%t", tt.hasSince), func(t *testing.T) {
			col := newTestCollector(t, Config{})
			col.hasStatsSince = tt.hasSince
			got := col.queryStatStatements()
			has := regexp.MustCompile(`(?m)^\s*stats_since\s*$`).MatchString(got)
			if has != tt.hasSince {
				t.Fatalf("stats_since selected = %v, want %v:\n%s", has, tt.hasSince, got)
			}
			if !strings.Contains(got, "AS mean_exec_time") {
				t.Fatalf("query lost its mean_exec_time column:\n%s", got)
			}
		})
	}
}
