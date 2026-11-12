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
	"sort"
)

// normCDF is the standard normal cumulative distribution function.
func normCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

// logNormCDF is ln Φ(x), accurate in the far lower tail where Φ underflows.
func logNormCDF(x float64) float64 {
	if x > -8 {
		return math.Log(normCDF(x))
	}
	// Asymptotic series for the Mills ratio: Φ(x) ≈ φ(x)/|x| · (1 − 1/x² + 3/x⁴ − 15/x⁶).
	x2 := x * x
	series := 1 - 1/x2 + 3/(x2*x2) - 15/(x2*x2*x2)
	return -x2/2 - math.Log(-x) - 0.5*math.Log(2*math.Pi) + math.Log(series)
}

// normQuantile is the inverse of normCDF (Acklam's rational approximation
// refined by one Halley step; absolute error below 1e-12 on (1e-12, 1-1e-12)).
func normQuantile(p float64) float64 {
	switch {
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	}
	a := [...]float64{-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02, 1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00}
	b := [...]float64{-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02, 6.680131188771972e+01, -1.328068155288572e+01}
	c := [...]float64{-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00, -2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00}
	d := [...]float64{7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00, 3.754408661907416e+00}
	const plow = 0.02425
	var x float64
	switch {
	case p < plow:
		q := math.Sqrt(-2 * math.Log(p))
		x = (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= 1-plow:
		q := p - 0.5
		r := q * q
		x = (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q / (((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		x = -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) / ((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
	e := normCDF(x) - p
	u := e * math.Sqrt(2*math.Pi) * math.Exp(x*x/2)
	return x - u/(1+x*u/2)
}

func isNaN(x float64) bool { return x != x }

var nan = math.NaN()

// meanOf is the mean of the non-NaN values, and how many there were.
func meanOf(v []float64) (float64, int) {
	var s float64
	n := 0
	for _, x := range v {
		if !isNaN(x) {
			s += x
			n++
		}
	}
	if n == 0 {
		return nan, 0
	}
	return s / float64(n), n
}

// medianOf is the median of the non-NaN values (NaN when there are none).
func medianOf(v []float64) float64 {
	c := make([]float64, 0, len(v))
	for _, x := range v {
		if !isNaN(x) {
			c = append(c, x)
		}
	}
	if len(c) == 0 {
		return nan
	}
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

// madSigma is the median absolute deviation scaled to be consistent with the
// standard deviation of a normal sample.
func madSigma(v []float64) float64 {
	m := medianOf(v)
	if isNaN(m) {
		return nan
	}
	dev := make([]float64, 0, len(v))
	for _, x := range v {
		if !isNaN(x) {
			dev = append(dev, math.Abs(x-m))
		}
	}
	return 1.4826 * medianOf(dev)
}

// lag1 is the lag-1 autocorrelation of v around its mean, over consecutive
// non-NaN pairs. It is 0 when there are fewer than 3 pairs.
func lag1(v []float64) float64 {
	m, n := meanOf(v)
	if n < 4 {
		return 0
	}
	var num, den float64
	for i, x := range v {
		if isNaN(x) {
			continue
		}
		d := x - m
		den += d * d
		if i > 0 && !isNaN(v[i-1]) {
			num += d * (v[i-1] - m)
		}
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// solveSPD solves A x = b for a symmetric positive-definite A by Cholesky
// factorisation. A is overwritten. It reports false when A is not positive
// definite.
func solveSPD(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			s := a[i][j]
			for k := 0; k < j; k++ {
				s -= a[i][k] * a[j][k]
			}
			if i == j {
				if s <= 1e-14 {
					return nil, false
				}
				a[i][i] = math.Sqrt(s)
			} else {
				a[i][j] = s / a[j][j]
			}
		}
	}
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for k := 0; k < i; k++ {
			s -= a[i][k] * y[k]
		}
		y[i] = s / a[i][i]
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := y[i]
		for k := i + 1; k < n; k++ {
			s -= a[k][i] * x[k]
		}
		x[i] = s / a[i][i]
	}
	return x, true
}

// goldenMin minimises f over [lo, hi] by golden-section search.
func goldenMin(f func(float64) float64, lo, hi float64, iters int) float64 {
	const phi = 0.6180339887498949
	a, b := lo, hi
	c := b - phi*(b-a)
	d := a + phi*(b-a)
	fc, fd := f(c), f(d)
	for i := 0; i < iters; i++ {
		if fc < fd {
			b, d, fd = d, c, fc
			c = b - phi*(b-a)
			fc = f(c)
		} else {
			a, c, fc = c, d, fd
			d = a + phi*(b-a)
			fd = f(d)
		}
	}
	return (a + b) / 2
}
