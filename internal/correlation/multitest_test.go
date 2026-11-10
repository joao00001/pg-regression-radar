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
	"math"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func TestParseMultipleTesting(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]MultipleTesting{
		"":     MultipleTestingNone,
		"none": MultipleTestingNone,
		"bh":   MultipleTestingBH,
		"BH":   MultipleTestingBH,
		" by ": MultipleTestingBY,
		"None": MultipleTestingNone,
	} {
		got, err := ParseMultipleTesting(in)
		if err != nil || got != want {
			t.Errorf("ParseMultipleTesting(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"bonferroni", "fdr", "0.05"} {
		if _, err := ParseMultipleTesting(bad); err == nil {
			t.Errorf("ParseMultipleTesting(%q) accepted an unknown mode", bad)
		}
	}
}

// The worked example from Benjamini and Hochberg (1995), section 4: fifteen
// p-values from a study of short-term and long-term effects, rejected at
// q = 0.05. The first four are rejected; Bonferroni would keep only three.
func TestBenjaminiHochberg_PaperExample(t *testing.T) {
	t.Parallel()

	p := []float64{0.0001, 0.0004, 0.0019, 0.0095, 0.0201, 0.0278, 0.0298, 0.0344, 0.0459, 0.3240, 0.4262, 0.5719, 0.6528, 0.7590, 1.000}
	got := benjaminiHochberg(p, 0.05, len(p))
	for i, rej := range got {
		if want := i < 4; rej != want {
			t.Errorf("p[%d]=%g: rejected=%v, want %v", i, p[i], rej, want)
		}
	}
}

func TestBenjaminiHochberg_Properties(t *testing.T) {
	t.Parallel()

	// Order of the input does not matter.
	p := []float64{0.8, 0.001, 0.04, 0.0002, 0.6}
	got := benjaminiHochberg(p, 0.05, 5)
	for i, want := range []bool{false, true, false, true, false} {
		if got[i] != want {
			t.Errorf("shuffled input, index %d: rejected=%v, want %v", i, got[i], want)
		}
	}

	// A step-up procedure rejects everything at or below the last passing rank,
	// even a p-value that fails its own rank's threshold.
	p = []float64{0.021, 0.022, 0.0001}
	got = benjaminiHochberg(p, 0.05, 3)
	for i, want := range []bool{true, true, true} {
		if got[i] != want {
			t.Errorf("step-up, index %d: rejected=%v, want %v", i, got[i], want)
		}
	}

	// Hypotheses counted in m but left out of p (known non-rejections) make the
	// thresholds stricter.
	if !benjaminiHochberg([]float64{0.004}, 0.05, 10)[0] {
		t.Error("p=0.004 with m=10 should pass (threshold 0.005)")
	}
	if benjaminiHochberg([]float64{0.004}, 0.05, 20)[0] {
		t.Error("p=0.004 with m=20 should fail (threshold 0.0025)")
	}

	if got := benjaminiHochberg(nil, 0.05, 1); len(got) != 0 {
		t.Errorf("empty input returned %v", got)
	}
}

func TestHarmonic(t *testing.T) {
	t.Parallel()

	if got := harmonic(1); got != 1 {
		t.Errorf("harmonic(1) = %v", got)
	}
	if got := harmonic(4); math.Abs(got-(1+0.5+1.0/3+0.25)) > 1e-12 {
		t.Errorf("harmonic(4) = %v", got)
	}
	// It grows like ln(m) + the Euler-Mascheroni constant.
	if got, want := harmonic(500), math.Log(500)+0.5772156649; math.Abs(got-want) > 0.002 {
		t.Errorf("harmonic(500) = %v, want about %v", got, want)
	}
}

// pass builds the results of one analysis pass: detected p-values first, then
// notDetected queries that failed a gate, then insufficient queries with too
// little data to judge.
func pass(detected []float64, notDetected, insufficient int) []v1alpha1.PerformanceRegression {
	var out []v1alpha1.PerformanceRegression
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	id := int64(0)
	for _, p := range detected {
		id++
		p := p
		out = append(out, v1alpha1.PerformanceRegression{QueryID: id, Status: v1alpha1.StatusDetected,
			PValue: &p, ConfidenceScore: 1 - p, DetectedChangeAt: at})
	}
	for i := 0; i < notDetected; i++ {
		id++
		out = append(out, v1alpha1.PerformanceRegression{QueryID: id, Status: v1alpha1.StatusNoRegression})
	}
	for i := 0; i < insufficient; i++ {
		id++
		out = append(out, v1alpha1.PerformanceRegression{QueryID: id, Status: v1alpha1.StatusInsufficientData})
	}
	return out
}

func engineWith(mode MultipleTesting) *Engine {
	return New(Config{MultipleTesting: mode, PValueThreshold: 0.05}, nil, nil)
}

func TestControlFalseDiscoveries_DemotesWhatDoesNotSurvive(t *testing.T) {
	t.Parallel()

	// 92 queries judged: two detections, 90 that failed a gate. 8 queries had
	// too little data and are not part of the family.
	results := pass([]float64{1e-9, 0.04}, 90, 8)
	got := engineWith(MultipleTestingBH).controlFalseDiscoveries(results)

	if got[0].Status != v1alpha1.StatusDetected || got[0].ConfidenceScore == 0 || got[0].DetectedChangeAt.IsZero() {
		t.Errorf("strong detection was altered: %+v", got[0])
	}
	// p=0.04 passes the unadjusted 0.05 but not 0.05*2/92.
	if got[1].Status != v1alpha1.StatusNoRegression {
		t.Errorf("borderline detection kept its status %q", got[1].Status)
	}
	if got[1].ConfidenceScore != 0 || !got[1].DetectedChangeAt.IsZero() {
		t.Errorf("demoted detection still carries detection fields: %+v", got[1])
	}
	if got[1].PValue == nil || *got[1].PValue != 0.04 {
		t.Error("the raw p-value must stay on a demoted result so it can still be inspected")
	}
	for _, r := range got[2:] {
		if r.Status == v1alpha1.StatusDetected {
			t.Fatalf("query %d became Detected; correction may only remove detections", r.QueryID)
		}
	}
	if n := len(got); n != 100 {
		t.Errorf("result count changed to %d", n)
	}
}

func TestControlFalseDiscoveries_ModeNoneIsANoOp(t *testing.T) {
	t.Parallel()

	for _, mode := range []MultipleTesting{"", MultipleTestingNone, "bogus"} {
		results := pass([]float64{0.04, 0.049}, 200, 0)
		got := engineWith(mode).controlFalseDiscoveries(results)
		for _, r := range got[:2] {
			if r.Status != v1alpha1.StatusDetected {
				t.Errorf("mode %q demoted query %d", mode, r.QueryID)
			}
		}
	}
}

func TestControlFalseDiscoveries_BYIsStricterThanBH(t *testing.T) {
	t.Parallel()

	// m=92, p=0.0004: BH threshold 0.05/92 = 0.00054 passes, BY's is about 5x lower.
	bh := engineWith(MultipleTestingBH).controlFalseDiscoveries(pass([]float64{0.0004}, 91, 0))
	by := engineWith(MultipleTestingBY).controlFalseDiscoveries(pass([]float64{0.0004}, 91, 0))
	if bh[0].Status != v1alpha1.StatusDetected {
		t.Error("BH should keep p=0.0004 among 92 queries")
	}
	if by[0].Status != v1alpha1.StatusNoRegression {
		t.Error("BY should demote p=0.0004 among 92 queries")
	}
}

func TestControlFalseDiscoveries_InsufficientDataIsNotPartOfTheFamily(t *testing.T) {
	t.Parallel()

	// 21 judged queries, 500 with too little data: m is 21 (BH threshold 0.0024),
	// not 521 (0.0001), so p=0.0004 survives.
	got := engineWith(MultipleTestingBH).controlFalseDiscoveries(pass([]float64{0.0004}, 20, 500))
	if got[0].Status != v1alpha1.StatusDetected {
		t.Error("queries with insufficient data must not count toward m")
	}
	// And with all 521 judged it does not.
	got = engineWith(MultipleTestingBH).controlFalseDiscoveries(pass([]float64{0.0004}, 520, 0))
	if got[0].Status != v1alpha1.StatusNoRegression {
		t.Error("with 521 judged queries p=0.0004 should not survive")
	}
}

func TestControlFalseDiscoveries_NothingDetected(t *testing.T) {
	t.Parallel()

	results := pass(nil, 50, 5)
	got := engineWith(MultipleTestingBH).controlFalseDiscoveries(results)
	if len(got) != 55 {
		t.Fatalf("got %d results", len(got))
	}
	for _, r := range got {
		if r.Status == v1alpha1.StatusDetected {
			t.Fatal("a detection appeared from nowhere")
		}
	}
}
