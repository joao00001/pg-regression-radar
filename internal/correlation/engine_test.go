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

// ----- helpers -----

// fakeSampleSource implements correlation.SampleSource backed by a static map.
type fakeSampleSource struct {
	data map[int64][]collector.QuerySample
}

func (f *fakeSampleSource) SamplesInRange(queryID int64, from, to time.Time) []collector.QuerySample {
	var out []collector.QuerySample
	for _, s := range f.data[queryID] {
		if !s.RecordedAt.Before(from) && !s.RecordedAt.After(to) {
			out = append(out, s)
		}
	}
	return out
}

func (f *fakeSampleSource) AllQueryIDs() []int64 {
	ids := make([]int64, 0, len(f.data))
	for id := range f.data {
		ids = append(ids, id)
	}
	return ids
}

// buildSamples generates n QuerySamples starting at t0, spaced by step,
// with the given mean latency and a small noise amplitude.
func buildSamples(qid int64, qtext string, t0 time.Time, step time.Duration, n int, latency float64) []collector.QuerySample {
	samples := make([]collector.QuerySample, n)
	for i := range samples {
		samples[i] = collector.QuerySample{
			QueryID:        qid,
			QueryText:      qtext,
			Calls:          int64(i + 1),
			MeanExecTimeMs: latency,
			RecordedAt:     t0.Add(step * time.Duration(i)),
		}
	}
	return samples
}

// ----- tests -----

func TestAnalyse_DetectsRegression(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	deployAt := now // deploy happened at "now"

	step := time.Minute
	n := 15 // 15 samples per window

	// Pre-deploy window: latency = 10 ms (fast)
	preSamples := buildSamples(42, "SELECT 1", deployAt.Add(-time.Duration(n)*step), step, n, 10.0)
	// Post-deploy window: latency = 50 ms (5x regression)
	postSamples := buildSamples(42, "SELECT 1", deployAt.Add(step), step, n, 50.0)

	src := &fakeSampleSource{
		data: map[int64][]collector.QuerySample{
			42: append(preSamples, postSamples...),
		},
	}

	engine := correlation.New(correlation.Config{
		WindowMinutes:          20,
		MinExecutions:          10,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, src, nil)

	ev := v1alpha1.DeployEvent{
		ID:        "deploy-1",
		App:       "my-app",
		Namespace: "production",
		Revision:  "abc123",
		Timestamp: deployAt,
	}

	results := engine.Analyse(ev)

	if len(results) == 0 {
		t.Fatal("expected at least one result, got none")
	}

	var found *v1alpha1.PerformanceRegression
	for i := range results {
		if results[i].QueryID == 42 {
			found = &results[i]
			break
		}
	}
	if found == nil {
		t.Fatal("no result for queryid 42")
	}

	if found.Status != v1alpha1.StatusDetected {
		t.Errorf("expected StatusDetected, got %s", found.Status)
	}
	if found.LatencyChangeFactor < 2.0 {
		t.Errorf("expected change factor >= 2.0, got %.2f", found.LatencyChangeFactor)
	}
	if found.ConfidenceScore <= 0 || found.ConfidenceScore > 1 {
		t.Errorf("confidence score out of [0,1]: %.4f", found.ConfidenceScore)
	}
}

func TestAnalyse_NoRegression(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	step := time.Minute
	n := 15

	// Both windows have the same latency (no regression).
	samples := append(
		buildSamples(7, "SELECT count(*) FROM t", now.Add(-time.Duration(n)*step), step, n, 5.0),
		buildSamples(7, "SELECT count(*) FROM t", now.Add(step), step, n, 5.1)...,
	)

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{7: samples}}
	engine := correlation.New(correlation.Config{
		WindowMinutes:          20,
		MinExecutions:          10,
		LatencyChangeThreshold: 0.20,
	}, src, nil)

	results := engine.Analyse(v1alpha1.DeployEvent{
		ID:        "deploy-2",
		Timestamp: now,
	})

	for _, r := range results {
		if r.QueryID == 7 && r.Status == v1alpha1.StatusDetected {
			t.Errorf("false positive: expected NoRegression but got Detected (factor=%.2f)", r.LatencyChangeFactor)
		}
	}
}

func TestAnalyse_InsufficientData(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	// Only 3 pre-deploy samples — below the minimum of 10.
	preSamples := buildSamples(99, "UPDATE t SET x=1", now.Add(-3*time.Minute), time.Minute, 3, 20.0)
	postSamples := buildSamples(99, "UPDATE t SET x=1", now.Add(time.Minute), time.Minute, 3, 100.0)

	src := &fakeSampleSource{data: map[int64][]collector.QuerySample{99: append(preSamples, postSamples...)}}
	engine := correlation.New(correlation.Config{
		WindowMinutes: 10,
		MinExecutions: 10,
	}, src, nil)

	results := engine.Analyse(v1alpha1.DeployEvent{ID: "deploy-3", Timestamp: now})

	for _, r := range results {
		if r.QueryID == 99 && r.Status != v1alpha1.StatusInsufficientData {
			t.Errorf("expected InsufficientData, got %s", r.Status)
		}
	}
}

// ----- E-divisive tests -----

func TestEDivisive_DetectsChangePoint(t *testing.T) {
	t.Parallel()

	// Construct a series with a clear step at index 20.
	series := make([]float64, 40)
	for i := 0; i < 20; i++ {
		series[i] = 10.0
	}
	for i := 20; i < 40; i++ {
		series[i] = 50.0
	}

	cps := correlation.EDivisive(series, 5, 3)
	if len(cps) == 0 {
		t.Fatal("expected at least one change point, got none")
	}

	// The detected change point should be at or very near index 20.
	found := cps[0]
	if math.Abs(float64(found.Index-20)) > 2 {
		t.Errorf("expected change point near index 20, got %d", found.Index)
	}
	if found.EStatistic <= 0 {
		t.Errorf("expected positive E-statistic, got %f", found.EStatistic)
	}
}

func TestEDivisive_NoChangePoint(t *testing.T) {
	t.Parallel()

	// Flat series — no change point expected.
	series := make([]float64, 30)
	for i := range series {
		series[i] = 42.0
	}

	cps := correlation.EDivisive(series, 5, 3)
	// A flat series may still report a change point with E-stat 0; ensure
	// that no point has a positive E-statistic.
	for _, cp := range cps {
		if cp.EStatistic > 0 {
			t.Errorf("unexpected change point with positive E-stat at index %d (E=%.4f)", cp.Index, cp.EStatistic)
		}
	}
}

func TestEDivisive_ShortSeries(t *testing.T) {
	t.Parallel()

	// Series shorter than 2*minSegLen — should return nil.
	cps := correlation.EDivisive([]float64{1, 2, 3}, 5, 3)
	if len(cps) != 0 {
		t.Errorf("expected no change points for short series, got %d", len(cps))
	}
}

// ----- statistical helper tests -----

func TestWelchTTest_SignificantDifference(t *testing.T) {
	t.Parallel()

	// Two clearly separated groups.
	a := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	b := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}

	// We exercise the function indirectly through the engine; but let's also
	// test that a regression IS detected for such extreme data.
	src := &fakeSampleSource{
		data: map[int64][]collector.QuerySample{1: makeSamplesFromLatencies(1, a, b, time.Now())},
	}
	engine := correlation.New(correlation.Config{
		WindowMinutes: 20, MinExecutions: 5, LatencyChangeThreshold: 0.20,
	}, src, nil)

	results := engine.Analyse(v1alpha1.DeployEvent{ID: "d", Timestamp: time.Now()})
	found := false
	for _, r := range results {
		if r.QueryID == 1 && r.Status == v1alpha1.StatusDetected {
			found = true
		}
	}
	if !found {
		t.Error("expected regression to be detected for clearly separated groups")
	}
}

// makeSamplesFromLatencies builds a sample slice from pre/post latency slices.
func makeSamplesFromLatencies(qid int64, pre, post []float64, deployAt time.Time) []collector.QuerySample {
	var all []collector.QuerySample
	for i, lat := range pre {
		all = append(all, collector.QuerySample{
			QueryID:        qid,
			MeanExecTimeMs: lat,
			RecordedAt:     deployAt.Add(-time.Duration(len(pre)-i) * time.Minute),
		})
	}
	for i, lat := range post {
		all = append(all, collector.QuerySample{
			QueryID:        qid,
			MeanExecTimeMs: lat,
			RecordedAt:     deployAt.Add(time.Duration(i+1) * time.Minute),
		})
	}
	return all
}

// ----- two-stage detection tests (E-divisive locates, Welch confirms) -----

// TestAnalyse_TwoStage_ToleratesDelayedRollout verifies that a regression
// which only becomes observable a few minutes after the deploy timestamp
// (as expected from a rolling update that takes time to replace every pod)
// is still attributed to the deploy, because the real change point found by
// E-divisive falls within ChangePointTolerance.
func TestAnalyse_TwoStage_ToleratesDelayedRollout(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	deployAt := now
	step := time.Minute

	preSamples := buildSamples(66, "SELECT 1", deployAt.Add(-15*step), step, 15, 10.0) // t=-15..-1, fast
	postFast := buildSamples(66, "SELECT 1", deployAt.Add(1*step), step, 2, 10.0)      // t=1,2: rollout still in progress
	postSlow := buildSamples(66, "SELECT 1", deployAt.Add(3*step), step, 13, 50.0)     // t=3..15: regressed

	postSamples := append(postFast, postSlow...)

	src := &fakeSampleSource{
		data: map[int64][]collector.QuerySample{
			66: append(append([]collector.QuerySample{}, preSamples...), postSamples...),
		},
	}

	engine := correlation.New(correlation.Config{
		WindowMinutes:          30,
		MinExecutions:          10,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
	}, src, nil)

	results := engine.Analyse(v1alpha1.DeployEvent{ID: "deploy-delayed", Timestamp: deployAt})

	var found *v1alpha1.PerformanceRegression
	for i := range results {
		if results[i].QueryID == 66 {
			found = &results[i]
		}
	}
	if found == nil {
		t.Fatal("no result for query 66")
	}
	if found.Status != v1alpha1.StatusDetected {
		t.Fatalf("expected Detected (change point 3m after deploy is within default tolerance), got %s", found.Status)
	}
	if found.DetectedChangeAt.IsZero() {
		t.Fatal("expected DetectedChangeAt to be populated for a Detected regression")
	}
	gotOffset := found.DetectedChangeAt.Sub(deployAt)
	if gotOffset < 2*time.Minute || gotOffset > 4*time.Minute {
		t.Errorf("expected DetectedChangeAt ~3m after deploy, got offset %s", gotOffset)
	}
}

// TestAnalyse_TwoStage_RejectsDistantChangePoint proves the core value of the
// two-stage pipeline: a naive pre/post-deploy mean comparison would flag this
// as a regression, but the REAL shift in the data happened 10 minutes before
// the deploy (i.e. unrelated to it). Because the E-divisive change point
// falls outside ChangePointTolerance, the engine must reject it.
func TestAnalyse_TwoStage_RejectsDistantChangePoint(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	deployAt := now
	step := time.Minute

	segA := buildSamples(88, "SELECT 1", deployAt.Add(-30*step), step, 20, 10.0) // t=-30..-11, fast (old regime)
	segB := buildSamples(88, "SELECT 1", deployAt.Add(-10*step), step, 10, 50.0) // t=-10..-1, slow (real shift, unrelated to the deploy)
	segPost := buildSamples(88, "SELECT 1", deployAt, step, 30, 50.0)            // t=0..29, slow (steady state)

	preSamples := append(segA, segB...)
	postSamples := segPost

	src := &fakeSampleSource{
		data: map[int64][]collector.QuerySample{
			88: append(append([]collector.QuerySample{}, preSamples...), postSamples...),
		},
	}

	engine := correlation.New(correlation.Config{
		WindowMinutes:          30,
		MinExecutions:          10,
		LatencyChangeThreshold: 0.20,
		PValueThreshold:        0.05,
		// ChangePointTolerance left at 0 => defaults to 20% of the 30m
		// window (6m), which is well short of the real 10m offset.
	}, src, nil)

	results := engine.Analyse(v1alpha1.DeployEvent{ID: "deploy-distant", Timestamp: deployAt})

	var found *v1alpha1.PerformanceRegression
	for i := range results {
		if results[i].QueryID == 88 {
			found = &results[i]
		}
	}
	if found == nil {
		t.Fatal("no result for query 88")
	}

	// Sanity check on the test's own premise: the naive mean comparison
	// alone really would look like a regression here.
	if found.LatencyChangeFactor < 1.2 {
		t.Fatalf("test setup invalid: naive change factor too small (%.2f) to exercise the naive-vs-real-changepoint distinction", found.LatencyChangeFactor)
	}

	if found.Status != v1alpha1.StatusNoRegression {
		t.Errorf("expected NoRegression (real change point is 10m before the deploy, outside tolerance), got %s (change_at=%s)",
			found.Status, found.DetectedChangeAt)
	}
}

// TestAnalyse_TwoStage_NoiseRejected verifies that pure noise around a
// constant mean — with NO genuine regime shift anywhere in the window — is
// rejected even when sampling variance happens to push the naive pre/post
// means apart. LatencyChangeThreshold is set near zero so the cheap
// pre-filter can't hide the point of the test: rejection must come from
// E-divisive finding no real change point (or, failing that, from Welch's
// t-test on whatever segments it does find).
func TestAnalyse_TwoStage_NoiseRejected(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	step := time.Minute
	n := 30

	rng := rand.New(rand.NewSource(42))
	noise := func(qid int64, t0 time.Time, n int) []collector.QuerySample {
		samples := make([]collector.QuerySample, n)
		for i := range samples {
			samples[i] = collector.QuerySample{
				QueryID:        qid,
				QueryText:      "SELECT * FROM noisy",
				Calls:          int64(i + 1),
				MeanExecTimeMs: 20 + rng.NormFloat64()*6, // same distribution across the whole window
				RecordedAt:     t0.Add(step * time.Duration(i)),
			}
		}
		return samples
	}

	preSamples := noise(55, now.Add(-time.Duration(n)*step), n)
	postSamples := noise(55, now.Add(step), n)

	src := &fakeSampleSource{
		data: map[int64][]collector.QuerySample{
			55: append(append([]collector.QuerySample{}, preSamples...), postSamples...),
		},
	}

	engine := correlation.New(correlation.Config{
		WindowMinutes:          20,
		MinExecutions:          10,
		LatencyChangeThreshold: 0.0001,
		PValueThreshold:        0.05,
	}, src, nil)

	results := engine.Analyse(v1alpha1.DeployEvent{ID: "deploy-noise", Timestamp: now})

	for _, r := range results {
		if r.QueryID == 55 && r.Status == v1alpha1.StatusDetected {
			t.Errorf("false positive on pure noise: got Detected (change_factor=%.2f, detected_change_at=%s)",
				r.LatencyChangeFactor, r.DetectedChangeAt)
		}
	}
}
