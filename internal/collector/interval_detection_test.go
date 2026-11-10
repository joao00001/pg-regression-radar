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

package collector_test

import (
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// These tests drive the real Collector + correlation.Engine with the exact
// shape of data pg_stat_statements produces: cumulative calls and
// total_exec_time since the last reset. They exist because the original
// pipeline fed mean_exec_time, the all-time average, into the detector.

// feed builds cumulative pg_stat_statements rows, one per minute, for a query
// whose per-call latency is latencyAt(i) with perMinute calls each minute, on
// top of an existing history of histCalls calls at histMean ms. A negative
// index in resetAt (the zero value is "never") makes the counters restart from
// zero right before that sample, as pg_stat_statements_reset() does.
type feed struct {
	histCalls  int64
	histMean   float64
	perMinute  int64
	n          int
	start      time.Time
	resetAt    int // sample index at which counters restart; 0 = never
	latencyAt  func(i int) float64
	jitterSeed float64
}

func (f feed) rows(qid int64) []collector.QuerySample {
	calls := f.histCalls
	total := float64(f.histCalls) * f.histMean
	out := make([]collector.QuerySample, 0, f.n)
	for i := 0; i < f.n; i++ {
		if f.resetAt != 0 && i == f.resetAt {
			calls, total = 0, 0
		}
		// Deterministic +-3% jitter so each interval has realistic noise.
		jitter := 1 + 0.03*math.Sin(float64(i)*2.399+f.jitterSeed)
		calls += f.perMinute
		total += float64(f.perMinute) * f.latencyAt(i) * jitter
		out = append(out, collector.QuerySample{
			QueryID:         qid,
			QueryText:       "SELECT * FROM orders WHERE customer_id = $1",
			Calls:           calls,
			TotalExecTimeMs: total,
			MeanExecTimeMs:  total / float64(calls), // what pg_stat_statements reports
			RecordedAt:      f.start.Add(time.Duration(i) * time.Minute),
		})
	}
	return out
}

// legacySource hides the interval fields, reproducing what the detector saw
// before: only the cumulative mean_exec_time of each sample.
type legacySource struct{ inner correlation.SampleSource }

func (l legacySource) AllQueryIDs() []int64 { return l.inner.AllQueryIDs() }
func (l legacySource) SamplesInRange(q int64, from, to time.Time) []collector.QuerySample {
	s := l.inner.SamplesInRange(q, from, to)
	for i := range s {
		s[i].IntervalKnown, s[i].IntervalCalls, s[i].IntervalTotalMs, s[i].IntervalMeanMs, s[i].ResetDetected = false, 0, 0, 0, false
	}
	return s
}

func newEngine(src correlation.SampleSource) *correlation.Engine {
	return correlation.New(correlation.Config{
		WindowMinutes:          15,
		MinSamples:             10,
		MinExecutions:          10,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, src, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func seeded(t *testing.T, rows []collector.QuerySample) *collector.Collector {
	t.Helper()
	c, err := collector.New(collector.Config{DSN: "postgres://unused", ClusterName: "t"}, slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.Backfill(rows)
	return c
}

func result(t *testing.T, e *correlation.Engine, ev v1alpha1.DeployEvent, qid int64) v1alpha1.PerformanceRegression {
	t.Helper()
	for _, r := range e.Analyse(ev) {
		if r.QueryID == qid {
			return r
		}
	}
	t.Fatalf("no result for queryid %d", qid)
	return v1alpha1.PerformanceRegression{}
}

func TestDetection_SeesRegressionHiddenByCumulativeMean(t *testing.T) {
	// 1M historical calls at 10 ms, then 600 calls/min. The deploy at sample 16
	// makes the query 5x slower (10 -> 50 ms). 15 minutes at 50 ms moves the
	// all-time mean from 10.00 to ~10.4 ms: invisible to the old pipeline.
	start := time.Now().UTC().Add(-40 * time.Minute)
	deploy := start.Add(16*time.Minute - 30*time.Second) // between samples 15 and 16
	f := feed{histCalls: 1_000_000, histMean: 10, perMinute: 600, n: 32, start: start,
		latencyAt: func(i int) float64 {
			if i < 16 {
				return 10
			}
			return 50
		}}
	c := seeded(t, f.rows(1))
	ev := v1alpha1.DeployEvent{ID: "d1", Namespace: "prod", Timestamp: deploy}

	got := result(t, newEngine(c), ev, 1)
	if got.Status != v1alpha1.StatusDetected {
		t.Fatalf("interval-based detection: status = %v, want Detected (before=%.2f after=%.2f factor=%.2f)",
			got.Status, got.MeanLatencyBefore, got.MeanLatencyAfter, got.LatencyChangeFactor)
	}
	if got.LatencyChangeFactor < 4.5 || got.LatencyChangeFactor > 5.5 {
		t.Errorf("change factor = %.2f, want ~5 (50 ms vs 10 ms)", got.LatencyChangeFactor)
	}

	// Control: the same data seen only through the cumulative mean is missed.
	old := result(t, newEngine(legacySource{c}), ev, 1)
	if old.Status == v1alpha1.StatusDetected {
		t.Fatalf("control failed: cumulative-mean path unexpectedly detected the regression (factor %.2f); the test no longer proves the fix", old.LatencyChangeFactor)
	}
}

func TestDetection_ResetDoesNotCreateAFalseRegression(t *testing.T) {
	// Long history at a slow 40 ms mean; the query itself is steady at 10 ms.
	// pg_stat_statements_reset() at sample 12 drops the cumulative mean from
	// ~40 to 10 ms, a step that is NOT a regression, and also drives the
	// counters negative against the previous snapshot.
	start := time.Now().UTC().Add(-40 * time.Minute)
	deploy := start.Add(16*time.Minute - 30*time.Second) // between samples 15 and 16
	f := feed{histCalls: 1_000_000, histMean: 40, perMinute: 600, n: 32, start: start, resetAt: 12,
		latencyAt: func(int) float64 { return 10 }}
	c := seeded(t, f.rows(2))

	got := result(t, newEngine(c), v1alpha1.DeployEvent{ID: "d2", Namespace: "prod", Timestamp: deploy}, 2)
	if got.Status == v1alpha1.StatusDetected {
		t.Fatalf("a counter reset produced a false regression (factor %.2f, change at %v)", got.LatencyChangeFactor, got.DetectedChangeAt)
	}
	for _, s := range c.SamplesInRange(2, start.Add(-time.Minute), start.Add(time.Hour)) {
		if s.IntervalMeanMs < 0 || s.IntervalCalls < 0 {
			t.Fatalf("negative interval after reset: %+v", s)
		}
	}
}

func TestDetection_FindsRegressionRightAfterAReset(t *testing.T) {
	// The counters restart at sample 15, one sample before the deploy, then the
	// deploy makes the query 5x slower. The cumulative mean restarts too, so the
	// regression has to be read from the post-reset deltas.
	start := time.Now().UTC().Add(-40 * time.Minute)
	deploy := start.Add(16*time.Minute - 30*time.Second) // between samples 15 and 16
	f := feed{histCalls: 1_000_000, histMean: 10, perMinute: 600, n: 32, start: start, resetAt: 15,
		latencyAt: func(i int) float64 {
			if i < 16 {
				return 10
			}
			return 50
		}}
	c := seeded(t, f.rows(3))

	got := result(t, newEngine(c), v1alpha1.DeployEvent{ID: "d3", Namespace: "prod", Timestamp: deploy}, 3)
	if got.Status != v1alpha1.StatusDetected {
		t.Fatalf("status = %v, want Detected (factor %.2f)", got.Status, got.LatencyChangeFactor)
	}
}
