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

package attribution

import (
	"math"
	"testing"
)

func TestNormQuantileRoundTrip(t *testing.T) {
	for _, p := range []float64{1e-10, 1e-6, 0.001, 0.025, 0.1, 0.3, 0.5, 0.7, 0.9, 0.975, 0.999, 1 - 1e-8} {
		x := normQuantile(p)
		if got := normCDF(x); math.Abs(got-p) > 1e-12*math.Max(1, 1/p)*p+1e-14 {
			t.Errorf("normCDF(normQuantile(%g)) = %.15g", p, got)
		}
	}
	if math.Abs(normQuantile(0.975)-1.959963984540054) > 1e-9 {
		t.Errorf("z(0.975) = %.12f", normQuantile(0.975))
	}
}

func TestLogNormCDFContinuousAcrossSeries(t *testing.T) {
	// Below -8 the asymptotic series takes over. It must match the exact value
	// where the exact value is still representable, and keep decreasing beyond.
	for _, x := range []float64{-8, -9, -12, -20} {
		if exact := math.Log(normCDF(x)); math.Abs(logNormCDF(x)-exact) > 1e-6*math.Abs(exact) {
			t.Errorf("lnΦ(%g) = %.9f, exact %.9f", x, logNormCDF(x), exact)
		}
	}
	prev := logNormCDF(-8)
	for x := -9.0; x >= -40; x -= 1 {
		cur := logNormCDF(x)
		if cur >= prev {
			t.Fatalf("not decreasing at %g: %g >= %g", x, cur, prev)
		}
		prev = cur
	}
	// Known: ln Φ(-10) = -53.23128515...
	if math.Abs(logNormCDF(-10)+53.231285151) > 1e-6 {
		t.Errorf("lnΦ(-10) = %.9f", logNormCDF(-10))
	}
}

func TestSolveSPD(t *testing.T) {
	a := [][]float64{{4, 1, 0}, {1, 3, 1}, {0, 1, 2}}
	b := []float64{1, 2, 3}
	x, ok := solveSPD(a, b)
	if !ok {
		t.Fatal("not SPD")
	}
	want := []float64{0.1, 0.6, 1.2}
	// verify A x = b with the original matrix
	orig := [][]float64{{4, 1, 0}, {1, 3, 1}, {0, 1, 2}}
	for i := range orig {
		var s float64
		for j := range orig[i] {
			s += orig[i][j] * x[j]
		}
		if math.Abs(s-[]float64{1, 2, 3}[i]) > 1e-12 {
			t.Errorf("row %d: %g", i, s)
		}
	}
	_ = want
	if _, ok := solveSPD([][]float64{{1, 2}, {2, 1}}, []float64{1, 1}); ok {
		t.Error("indefinite matrix reported as SPD")
	}
}

func TestMedianMADAndLag1(t *testing.T) {
	if m := medianOf([]float64{3, nan, 1, 2}); m != 2 {
		t.Errorf("median = %g", m)
	}
	if !isNaN(medianOf(nil)) {
		t.Error("median of nothing should be NaN")
	}
	if s := madSigma([]float64{1, 2, 3, 4, 100}); math.Abs(s-1.4826) > 1e-9 {
		t.Errorf("mad = %g", s)
	}
	// strongly alternating series has negative lag-1; a ramp has positive
	if r := lag1([]float64{1, -1, 1, -1, 1, -1, 1, -1}); r > -0.5 {
		t.Errorf("alternating lag1 = %g", r)
	}
	if r := lag1([]float64{1, 2, 3, 4, 5, 6, 7, 8}); r < 0.5 {
		t.Errorf("ramp lag1 = %g", r)
	}
}

func TestGoldenMin(t *testing.T) {
	x := goldenMin(func(x float64) float64 { return (x - 1.25) * (x - 1.25) }, -5, 5, 80)
	if math.Abs(x-1.25) > 1e-6 {
		t.Errorf("min at %g", x)
	}
}
