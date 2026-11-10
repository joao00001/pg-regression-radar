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
	"math/rand"
	"testing"
)

// ar1Series returns n values of a stationary AR(1) process with lag-1
// coefficient rho around a fixed level.
func ar1Series(rng *rand.Rand, n int, rho float64) []float64 {
	out := make([]float64, n)
	z := rng.NormFloat64()
	for i := range out {
		z = rho*z + math.Sqrt(1-rho*rho)*rng.NormFloat64()
		out[i] = 10 + z
	}
	return out
}

// rejectionRates draws trials pairs of equal-mean AR(1) segments and returns
// the share each test calls significant at 0.05. With no real difference the
// share is the test's actual false-positive rate.
func rejectionRates(rho float64, n, trials int) (welch, adjusted float64) {
	rng := rand.New(rand.NewSource(1))
	var w, a int
	for i := 0; i < trials; i++ {
		x, y := ar1Series(rng, n, rho), ar1Series(rng, n, rho)
		if _, p := welchTTest(x, y); p <= 0.05 {
			w++
		}
		if _, p := welchTTestAutocorrelated(x, y); p <= 0.05 {
			a++
		}
	}
	return float64(w) / float64(trials), float64(a) / float64(trials)
}

// Zwiers and von Storch's correction is meant to bring the false-positive
// rate of a two-sample mean test back toward its nominal 5% when consecutive
// observations are correlated. The bounds are loose on purpose: the
// correction is approximate for short series, so the test asserts "far closer
// to nominal than Welch", not "exactly 5%".
func TestWelchAutocorrelated_FalsePositiveRate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rho          float64
		welchAtLeast float64
		adjAtMost    float64
	}{
		{0, 0.04, 0.07},
		{0.5, 0.20, 0.11},
		{0.7, 0.35, 0.14},
	}
	for _, tc := range cases {
		welch, adj := rejectionRates(tc.rho, 30, 2000)
		if welch < tc.welchAtLeast {
			t.Errorf("rho=%.1f: plain Welch rejected %.3f of equal-mean pairs, want at least %.2f (the problem the adjustment addresses)", tc.rho, welch, tc.welchAtLeast)
		}
		if adj > tc.adjAtMost {
			t.Errorf("rho=%.1f: adjusted test rejected %.3f of equal-mean pairs, want at most %.2f", tc.rho, adj, tc.adjAtMost)
		}
		if adj > welch+1e-9 {
			t.Errorf("rho=%.1f: adjusted rate %.3f exceeds plain Welch's %.3f", tc.rho, adj, welch)
		}
	}
}

// The adjustment may only widen the standard error: whatever the data, its
// p-value is never smaller than plain Welch's, including for negatively
// correlated data, where the textbook formula would enlarge the sample.
func TestWelchAutocorrelated_NeverMoreLiberalThanWelch(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(42))
	for _, rho := range []float64{-0.8, -0.3, 0, 0.3, 0.6, 0.9} {
		for i := 0; i < 200; i++ {
			x, y := ar1Series(rng, 25, rho), ar1Series(rng, 40, rho)
			for j := range y {
				y[j] += 0.5 * rng.Float64()
			}
			_, pw := welchTTest(x, y)
			_, pa := welchTTestAutocorrelated(x, y)
			if pa < pw-1e-12 {
				t.Fatalf("rho=%.1f: adjusted p=%g is smaller than Welch's p=%g", rho, pa, pw)
			}
		}
	}
}

func TestWelchAutocorrelated_EdgeCases(t *testing.T) {
	t.Parallel()

	// Too short to estimate a correlation: fall back to plain Welch.
	short := []float64{1, 2}
	long := []float64{3, 4, 5, 6, 7}
	_, want := welchTTest(short, long)
	if _, got := welchTTestAutocorrelated(short, long); got != want {
		t.Errorf("short segment: p=%g, want Welch's %g", got, want)
	}

	// Constant segments: a definite shift or no difference, as in Welch.
	flatA := []float64{5, 5, 5, 5, 5}
	flatB := []float64{9, 9, 9, 9, 9}
	if _, p := welchTTestAutocorrelated(flatA, flatB); p != 0 {
		t.Errorf("constant segments with different levels: p=%g, want 0", p)
	}
	if _, p := welchTTestAutocorrelated(flatA, flatA); p != 1 {
		t.Errorf("identical constant segments: p=%g, want 1", p)
	}
}

func TestEffectiveSize(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		n, r float64
		want float64
	}{
		{"no correlation keeps n", 30, 0, 30},
		{"negative correlation never enlarges", 30, -0.5, 30},
		{"AR(1) formula at r=0.5", 30, 0.5, 10},
		{"AR(1) formula at r=0.7", 30, 0.7, 30 * 0.3 / 1.7},
		{"floor at 2", 30, 0.95, 2},
		{"r at the clamp", 30, 1, 2},
	}
	for _, tc := range cases {
		if got := effectiveSize(tc.n, tc.r); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: effectiveSize(%v, %v) = %v, want %v", tc.name, tc.n, tc.r, got, tc.want)
		}
	}
}

func TestPooledLag1(t *testing.T) {
	t.Parallel()

	// A level shift between the segments is not autocorrelation: each segment is
	// centred on its own mean.
	a := []float64{1, 3, 1, 3, 1, 3}
	b := []float64{101, 103, 101, 103, 101, 103}
	r := pooledLag1(a, mean(a), b, mean(b))
	if r > -0.7 {
		t.Errorf("alternating segments should be strongly negatively correlated, got r=%.3f", r)
	}

	ramp := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	if r := pooledLag1(ramp, mean(ramp), ramp, mean(ramp)); r < 0.3 {
		t.Errorf("trending segments should be positively correlated, got r=%.3f", r)
	}

	flat := []float64{4, 4, 4}
	if r := pooledLag1(flat, 4, flat, 4); r != 0 {
		t.Errorf("constant segments: r=%v, want 0", r)
	}
}
