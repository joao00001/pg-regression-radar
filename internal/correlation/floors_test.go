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

// intervalFeed builds n one-minute observations starting at t0 where each
// scrape interval saw callsPerInterval executions at the given mean latency
// (with a little alternating noise so the variance is not zero).
func intervalFeed(qid int64, t0 time.Time, n int, callsPerInterval int64, latency float64) []collector.QuerySample {
	out := make([]collector.QuerySample, n)
	for i := range out {
		l := latency * (1 + 0.02*float64(i%3-1))
		out[i] = collector.QuerySample{
			QueryID:         qid,
			QueryText:       "SELECT 1",
			RecordedAt:      t0.Add(time.Duration(i) * time.Minute),
			IntervalKnown:   true,
			IntervalCalls:   callsPerInterval,
			IntervalTotalMs: l * float64(callsPerInterval),
			IntervalMeanMs:  l,
		}
	}
	return out
}

func analyseFloors(t *testing.T, cfg correlation.Config, calls int64, samples int) v1alpha1.PerformanceRegression {
	t.Helper()
	deploy := time.Now().UTC().Add(-30 * time.Minute)
	pre := intervalFeed(7, deploy.Add(-time.Duration(samples)*time.Minute), samples, calls, 10)
	post := intervalFeed(7, deploy.Add(30*time.Second), samples, calls, 50)
	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{7: append(pre, post...)}}
	cfg.WindowMinutes = 20
	cfg.LatencyChangeThreshold = 0.20
	cfg.PValueThreshold = 0.05
	for _, r := range correlation.New(cfg, src, nil).Analyse(v1alpha1.DeployEvent{ID: "d", Namespace: "n", Timestamp: deploy}) {
		if r.QueryID == 7 {
			return r
		}
	}
	t.Fatal("no result for query 7")
	return v1alpha1.PerformanceRegression{}
}

func TestFloors_ExecutionsAreCountedNotSamples(t *testing.T) {
	t.Parallel()
	// 15 observations per window, but only 2 calls behind each: 30 executions.
	// Plenty of samples, far too few executions to trust either verdict.
	r := analyseFloors(t, correlation.Config{MinSamples: 10, MinExecutions: 100}, 2, 15)
	if r.Status != v1alpha1.StatusInsufficientData {
		t.Fatalf("status = %v, want InsufficientData: 30 executions per window is below --min-executions=100 however many samples there are", r.Status)
	}
	// Same data, floor lowered below the 30 executions actually present.
	r = analyseFloors(t, correlation.Config{MinSamples: 10, MinExecutions: 20}, 2, 15)
	if r.Status != v1alpha1.StatusDetected {
		t.Fatalf("status = %v, want Detected once the execution floor is met", r.Status)
	}
}

func TestFloors_SamplesAreCountedNotExecutions(t *testing.T) {
	t.Parallel()
	// Three observations per window with 10,000 calls each: huge execution
	// counts, too few observations for a change-point search or a t-test.
	r := analyseFloors(t, correlation.Config{MinSamples: 10, MinExecutions: 100}, 10_000, 3)
	if r.Status != v1alpha1.StatusInsufficientData {
		t.Fatalf("status = %v, want InsufficientData: 3 samples per window is below --min-samples=10 however many executions stand behind them", r.Status)
	}
}

func TestFloors_DefaultsAreSamplesTenExecutionsHundred(t *testing.T) {
	t.Parallel()
	if correlation.DefaultMinSamples != 10 || correlation.DefaultMinExecutions != 100 {
		t.Fatalf("defaults = (%d, %d), want (10, 100); update docs/configuration.md and the CRD defaults together", correlation.DefaultMinSamples, correlation.DefaultMinExecutions)
	}
	// Zero values pick the defaults: 15 observations x 2 calls is below 100.
	r := analyseFloors(t, correlation.Config{}, 2, 15)
	if r.Status != v1alpha1.StatusInsufficientData {
		t.Fatalf("status = %v, want InsufficientData under the default execution floor", r.Status)
	}
}

func TestFloors_SourcesWithoutCallCountsAreGatedBySamplesOnly(t *testing.T) {
	t.Parallel()
	// Legacy-shaped samples (mean only, no IntervalCalls), as the
	// OTel/Prometheus source produces. Executions are unknown, so the
	// default 100-execution floor must not turn every such query into
	// InsufficientData.
	deploy := time.Now().UTC().Add(-30 * time.Minute)
	pre := buildSamples(9, "SELECT 1", deploy.Add(-15*time.Minute), time.Minute, 14, 10)
	post := buildSamples(9, "SELECT 1", deploy.Add(30*time.Second), time.Minute, 14, 50)
	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{9: append(pre, post...)}}
	e := correlation.New(correlation.Config{WindowMinutes: 20}, src, nil)
	for _, r := range e.Analyse(v1alpha1.DeployEvent{ID: "d", Namespace: "n", Timestamp: deploy}) {
		if r.QueryID == 9 && r.Status == v1alpha1.StatusInsufficientData {
			t.Fatal("a source without call counts was rejected by the execution floor")
		}
	}
}
