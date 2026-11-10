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
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// noisyQuerySamples returns per-scrape interval samples for one query whose
// latency is lognormal around mu with coefficient of variation cv. From the
// scrape at index shiftAt on, latency is multiplied by factor (use -1 for no
// change).
func noisyQuerySamples(rng *rand.Rand, qid int64, n int, mu, cv float64, t0 time.Time, shiftAt int, factor float64) []collector.QuerySample {
	out := make([]collector.QuerySample, n)
	const calls = 200
	for i := range out {
		lat := mu * math.Exp(cv*rng.NormFloat64()-cv*cv/2)
		if shiftAt >= 0 && i >= shiftAt {
			lat *= factor
		}
		out[i] = collector.QuerySample{
			QueryID: qid, QueryText: "SELECT 1", RecordedAt: t0.Add(time.Duration(i) * time.Minute),
			IntervalKnown: true, IntervalCalls: calls, IntervalTotalMs: lat * calls, IntervalMeanMs: lat,
			Calls: int64(i+1) * calls, MeanExecTimeMs: lat,
		}
	}
	return out
}

// 300 unchanged but noisy queries and one real, large regression. Each noisy
// query has a small chance of clearing the 20% gate and p <= 0.05 by luck; across
// 300 of them some do. The Benjamini-Hochberg pass removes those and keeps the
// regression whose p-value is orders of magnitude below its threshold.
func TestAnalyse_MultipleTestingBH_RemovesChanceDetectionsKeepsRealOne(t *testing.T) {
	t.Parallel()

	const queries, window = 300, 30
	deployAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t0 := deployAt.Add(-window * time.Minute)

	run := func(mode correlation.MultipleTesting) (falsePositives int, realDetected bool) {
		rng := rand.New(rand.NewSource(11))
		data := map[int64][]collector.QuerySample{}
		for q := int64(1); q <= queries; q++ {
			shift, factor := -1, 1.0
			if q == 1 {
				shift, factor = window, 4.0
			}
			data[q] = noisyQuerySamples(rng, q, 2*window+1, 20, 0.6, t0, shift, factor)
		}
		eng := correlation.New(correlation.Config{
			WindowMinutes: window, MinSamples: 10, MinExecutions: 100,
			LatencyChangeThreshold: 0.2, PValueThreshold: 0.05, MultipleTesting: mode,
		}, &fakeSampleSource{data: data}, nil)
		for _, r := range eng.Analyse(v1alpha1.DeployEvent{ID: "d", Timestamp: deployAt}) {
			if r.Status != v1alpha1.StatusDetected {
				continue
			}
			if r.QueryID == 1 {
				realDetected = true
			} else {
				falsePositives++
			}
		}
		return falsePositives, realDetected
	}

	fpNone, realNone := run(correlation.MultipleTestingNone)
	if fpNone == 0 {
		t.Fatal("setup: expected the uncorrected pass to flag some of the unchanged queries by chance; the test no longer exercises the correction")
	}
	if !realNone {
		t.Fatal("setup: the real regression was not detected even without correction")
	}

	for _, mode := range []correlation.MultipleTesting{correlation.MultipleTestingBH, correlation.MultipleTestingBY} {
		fp, real := run(mode)
		if fp >= fpNone {
			t.Errorf("%s: %d false detections, no fewer than the %d without correction", mode, fp, fpNone)
		}
		if fp != 0 {
			t.Errorf("%s: %d unchanged queries still reported as Detected", mode, fp)
		}
		if !real {
			t.Errorf("%s: the real 4x regression was removed", mode)
		}
	}
}

// Periodic analysis goes through the same pass-level correction.
func TestAnalysePeriodic_MultipleTestingBH(t *testing.T) {
	t.Parallel()

	const queries, window = 300, 60
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t0 := now.Add(-2 * window * time.Minute)

	count := func(mode correlation.MultipleTesting) (fp int) {
		rng := rand.New(rand.NewSource(5))
		data := map[int64][]collector.QuerySample{}
		for q := int64(1); q <= queries; q++ {
			data[q] = noisyQuerySamples(rng, q, 2*window+1, 20, 0.6, t0, -1, 1)
		}
		eng := correlation.New(correlation.Config{
			MinSamples: 10, MinExecutions: 100, PeriodicWindowMinutes: window,
			LatencyChangeThreshold: 0.2, PValueThreshold: 0.05, MultipleTesting: mode,
		}, &fakeSampleSource{data: data}, nil)
		for _, r := range eng.AnalysePeriodic(now) {
			if r.Status == v1alpha1.StatusDetected {
				fp++
			}
		}
		return fp
	}

	none, bh := count(correlation.MultipleTestingNone), count(correlation.MultipleTestingBH)
	if none == 0 {
		t.Fatal("setup: expected chance detections without correction")
	}
	if bh >= none {
		t.Errorf("BH left %d detections, no fewer than the %d without correction", bh, none)
	}
}

// The default configuration must keep every p-value and every verdict exactly
// as it was before the options existed.
func TestAnalyse_DefaultsAreUnchangedByTheNewOptions(t *testing.T) {
	t.Parallel()

	const queries, window = 100, 30
	deployAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t0 := deployAt.Add(-window * time.Minute)
	build := func() map[int64][]collector.QuerySample {
		rng := rand.New(rand.NewSource(3))
		data := map[int64][]collector.QuerySample{}
		for q := int64(1); q <= queries; q++ {
			data[q] = noisyQuerySamples(rng, q, 2*window+1, 20, 0.5, t0, window, 1.3)
		}
		return data
	}
	cfg := correlation.Config{WindowMinutes: window, MinSamples: 10, MinExecutions: 100, LatencyChangeThreshold: 0.2, PValueThreshold: 0.05}
	explicit := cfg
	explicit.MultipleTesting = correlation.MultipleTestingNone
	explicit.AutocorrelationAdjust = false

	a := correlation.New(cfg, &fakeSampleSource{data: build()}, nil).Analyse(v1alpha1.DeployEvent{ID: "d", Timestamp: deployAt})
	b := correlation.New(explicit, &fakeSampleSource{data: build()}, nil).Analyse(v1alpha1.DeployEvent{ID: "d", Timestamp: deployAt})
	if len(a) != len(b) {
		t.Fatalf("result counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Status != b[i].Status || (a[i].PValue == nil) != (b[i].PValue == nil) || (a[i].PValue != nil && *a[i].PValue != *b[i].PValue) {
			t.Fatalf("query %d differs between the zero-value config and the explicit defaults", a[i].QueryID)
		}
	}
}

// AutocorrelationAdjust only ever raises p-values, so with the same data it can
// remove detections but never add one.
func TestAnalyse_AutocorrelationAdjustOnlyRemovesDetections(t *testing.T) {
	t.Parallel()

	const queries, window = 200, 30
	deployAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t0 := deployAt.Add(-window * time.Minute)
	rng := rand.New(rand.NewSource(9))
	data := map[int64][]collector.QuerySample{}
	for q := int64(1); q <= queries; q++ {
		data[q] = noisyQuerySamples(rng, q, 2*window+1, 20, 0.5, t0, window, 1.4)
	}
	base := correlation.Config{WindowMinutes: window, MinSamples: 10, MinExecutions: 100, LatencyChangeThreshold: 0.2, PValueThreshold: 0.05}
	adj := base
	adj.AutocorrelationAdjust = true

	plain := correlation.New(base, &fakeSampleSource{data: data}, nil).Analyse(v1alpha1.DeployEvent{ID: "d", Timestamp: deployAt})
	adjusted := correlation.New(adj, &fakeSampleSource{data: data}, nil).Analyse(v1alpha1.DeployEvent{ID: "d", Timestamp: deployAt})
	for i := range plain {
		if adjusted[i].Status == v1alpha1.StatusDetected && plain[i].Status != v1alpha1.StatusDetected {
			t.Fatalf("query %d: autocorrelation adjustment added a detection", plain[i].QueryID)
		}
		if plain[i].PValue != nil && adjusted[i].PValue != nil && *adjusted[i].PValue < *plain[i].PValue-1e-12 {
			t.Fatalf("query %d: adjusted p=%g below plain p=%g", plain[i].QueryID, *adjusted[i].PValue, *plain[i].PValue)
		}
	}
}
