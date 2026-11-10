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

import "math"

// welchTTestAutocorrelated is Welch's t-test with the standard errors
// widened for serial correlation in the two segments.
//
// Consecutive scrapes of one query are not independent: a cache warming up, a
// slow batch job or a noisy neighbour moves the latency of many scrapes in the
// same direction. Welch's test treats every scrape as a fresh observation, so
// it overstates the evidence and its p-value is too small. Here each segment's
// n observations are replaced by an effective sample size
//
//	n_e = n (1 - r) / (1 + r)
//
// the AR(1) form of the correction in Zwiers and von Storch (1995), where r is
// the lag-1 autocorrelation of both segments pooled (their Eq. 14), computed
// around each segment's own mean so the level shift being tested does not
// count as correlation. Following their Eq. 10, n_e is held between 2 and n,
// and a negative r is not used to enlarge the sample, so the adjustment can
// only make the test more conservative.
//
// The correction is approximate. With short segments r is estimated with a
// downward bias and n_e is itself estimated, so the test stays somewhat liberal
// when n_e is small (below about 30); it narrows the gap rather than closing it.
func welchTTestAutocorrelated(a, b []float64) (tStat, pValue float64) {
	n1, n2 := float64(len(a)), float64(len(b))
	if n1 < 3 || n2 < 3 {
		return welchTTest(a, b)
	}
	m1, m2 := mean(a), mean(b)
	v1, v2 := variance(a), variance(b)

	r := pooledLag1(a, m1, b, m2)
	e1, e2 := effectiveSize(n1, r), effectiveSize(n2, r)

	w1, w2 := v1/e1, v2/e2
	se := math.Sqrt(w1 + w2)
	if se == 0 {
		if m1 != m2 {
			return math.Inf(1), 0
		}
		return 0, 1
	}
	tStat = (m1 - m2) / se
	df := (w1 + w2) * (w1 + w2) / (w1*w1/(e1-1) + w2*w2/(e2-1))
	return tStat, tDistPValue(math.Abs(tStat), df)
}

// pooledLag1 is the lag-1 autocorrelation of two segments pooled, each taken
// around its own mean. It is 0 when both segments are constant.
func pooledLag1(a []float64, ma float64, b []float64, mb float64) float64 {
	var num, den float64
	for _, seg := range [...]struct {
		x []float64
		m float64
	}{{a, ma}, {b, mb}} {
		for t, v := range seg.x {
			d := v - seg.m
			den += d * d
			if t > 0 {
				num += d * (seg.x[t-1] - seg.m)
			}
		}
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// effectiveSize is n(1-r)/(1+r) held to [2, n]; any r at or below 0 leaves n.
func effectiveSize(n, r float64) float64 {
	if r <= 0 {
		return n
	}
	if r > 0.999 {
		r = 0.999
	}
	return math.Min(n, math.Max(2, n*(1-r)/(1+r)))
}
