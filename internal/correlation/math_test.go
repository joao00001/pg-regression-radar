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

// package correlation (not correlation_test): tDistPValue, incompleteBeta,
// betaCF, and logBeta are unexported. They're welchTTest's p-value
// machinery — every other test in this package only exercises them
// indirectly through Analyse/AnalysePeriodic's end-to-end behavior, which
// proves the detector works but never pins the numbers this file's
// functions actually produce against an independent source of truth. This
// file does that directly: every expected value below was computed with
// Python's scipy (scipy.stats.t.cdf, scipy.special.betaln,
// scipy.special.betainc — the standard reference implementations for
// exactly these quantities), not derived from this package's own code.
package correlation

import (
	"math"
	"testing"
	"time"
)

func TestLogBeta(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b, want float64
	}{
		{1, 1, 0.000000},
		{2, 3, -2.484907},
		{0.5, 0.5, 1.144730},
		{5, 5, -6.445720},
		{2.5, 7.2, -4.889554},
	}
	for _, c := range cases {
		got := logBeta(c.a, c.b)
		if math.Abs(got-c.want) > 1e-5 {
			t.Errorf("logBeta(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestIncompleteBeta(t *testing.T) {
	t.Parallel()
	cases := []struct {
		x, a, b, want float64
	}{
		{0.5, 2, 2, 0.500000},
		{0.3, 2, 3, 0.348300},
		{0.9, 5, 2, 0.885735},
		{0.2, 1, 1, 0.200000},
		{0.5, 0.5, 0.5, 0.500000},
		{0.05, 5, 3, 0.000006},
	}
	for _, c := range cases {
		got := incompleteBeta(c.x, c.a, c.b)
		if math.Abs(got-c.want) > 1e-4 {
			t.Errorf("incompleteBeta(%v, %v, %v) = %v, want %v", c.x, c.a, c.b, got, c.want)
		}
	}
}

// TestIncompleteBeta_Boundaries locks in the fast-path/edge-case behavior
// documented directly above incompleteBeta's symmetry branch: x outside
// [0,1] is meaningless for a CDF-derived quantity and returns 0 rather than
// extrapolating, and the exact endpoints 0/1 skip the continued-fraction
// evaluation entirely.
func TestIncompleteBeta_Boundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		x, a, b float64
		want    float64
	}{
		{"x=0", 0, 2, 3, 0},
		{"x=1", 1, 2, 3, 1},
		{"x<0", -0.5, 2, 3, 0},
		{"x>1", 1.5, 2, 3, 0},
	}
	for _, c := range cases {
		if got := incompleteBeta(c.x, c.a, c.b); got != c.want {
			t.Errorf("%s: incompleteBeta(%v, %v, %v) = %v, want %v", c.name, c.x, c.a, c.b, got, c.want)
		}
	}
}

// TestBetaCF_MatchesIncompleteBetaContract pins betaCF's continued-fraction
// evaluation against the exact relationship incompleteBeta's own
// implementation relies on (front * betaCF == incompleteBeta, for x below
// the symmetry threshold) — betaCF has no independent meaning outside that
// contract, so this is the correct way to give it its own direct test
// rather than only exercising it transitively.
func TestBetaCF_MatchesIncompleteBetaContract(t *testing.T) {
	t.Parallel()
	cases := []struct{ x, a, b float64 }{
		{0.3, 2, 3},
		{0.05, 5, 3},
		{0.2, 1, 1},
	}
	for _, c := range cases {
		lbeta := logBeta(c.a, c.b)
		front := math.Exp(math.Log(c.x)*c.a+math.Log(1-c.x)*c.b-lbeta) / c.a
		cf := betaCF(c.x, c.a, c.b)
		got := front * cf
		want := incompleteBeta(c.x, c.a, c.b)
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("front*betaCF(%v,%v,%v) = %v, want %v (== incompleteBeta)", c.x, c.a, c.b, got, want)
		}
	}
}

func TestTDistPValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		absT, df, want float64
	}{
		{0.0, 10, 1.000000},
		{1.0, 10, 0.340893},
		{2.228, 10, 0.050012},
		{3.169, 10, 0.010005},
		{2.571, 5, 0.049975},
		{12.706, 1, 0.050001},
		{1.98, 120, 0.049992},
		{2.0, 20, 0.059266},
		{3.0, 15, 0.008973},
		{0.5, 50, 0.619269},
	}
	for _, c := range cases {
		got := tDistPValue(c.absT, c.df)
		if math.Abs(got-c.want) > 1e-4 {
			t.Errorf("tDistPValue(%v, %v) = %v, want %v", c.absT, c.df, got, c.want)
		}
	}
}

// TestTDistPValue_NonPositiveDF guards the df<=0 fast path: degrees of
// freedom can't be zero or negative for a real sample (variance's own
// denominator is n-1), but welchTTest's Welch-Satterthwaite formula is a
// ratio of sums of squares that could in principle produce a degenerate
// value from malformed input — returning p=1 (no evidence of a difference)
// is the safe default rather than propagating NaN/Inf into a reported
// confidence score.
func TestTDistPValue_NonPositiveDF(t *testing.T) {
	t.Parallel()
	for _, df := range []float64{0, -1, -100} {
		if got := tDistPValue(1.5, df); got != 1 {
			t.Errorf("tDistPValue(1.5, %v) = %v, want 1", df, got)
		}
	}
}

func TestPeriodicWindow(t *testing.T) {
	t.Parallel()
	// PeriodicWindow only ever reads e.cfg, never e.src, so a nil
	// SampleSource is safe here — no fake needed for this trivial getter.
	e := New(Config{PeriodicWindowMinutes: 45}, nil, nil)
	if got, want := e.PeriodicWindow(), 45*time.Minute; got != want {
		t.Errorf("PeriodicWindow() = %s, want %s", got, want)
	}
}
