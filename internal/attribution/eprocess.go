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

import "math"

// Shift is the sufficient statistic of a Gaussian shift experiment: after n
// observations the log-likelihood of an effect θ is θ·Score − θ²·Info/2 up to a
// constant, with Score and Info computed at θ = 0. Under the model, and with
// θ = 0, Score is a martingale with predictable quadratic variation Info.
//
// For i.i.d. N(θ, σ²) observations Score is Σy/σ² and Info is n/σ². The
// Kalman-filter construction in statespace.go yields the same two numbers for a
// level shift in a local-level model with serially dependent observations, which
// is why everything below is written in terms of (Score, Info) and not of a
// sample mean.
type Shift struct {
	Score float64
	Info  float64
}

// Add folds in one innovation: nu is the prediction error under θ = 0, a the
// sensitivity of that error to θ (nu(θ) = nu − θ·a) and f its variance.
func (s *Shift) Add(a, nu, f float64) {
	if f <= 0 || isNaN(a) || isNaN(nu) || isNaN(f) {
		return
	}
	s.Score += a * nu / f
	s.Info += a * a / f
}

// Estimate is the maximum-likelihood effect Score/Info and its standard error
// 1/√Info. Both are NaN when no information has accumulated.
func (s Shift) Estimate() (theta, se float64) {
	if s.Info <= 0 {
		return nan, nan
	}
	return s.Score / s.Info, 1 / math.Sqrt(s.Info)
}

// LogE is ln of the mixture likelihood ratio against the composite null
// H0: θ ≤ delta, with the alternative θ − delta ~ N(0, tau²) truncated to
// (0, ∞). It is the one-sided mixture sequential probability ratio test of
// Johari, Pekelis and Walsh, shifted to a practical-significance margin delta.
//
// Validity. For θ ≤ delta, each factor of the likelihood ratio at an alternative
// θ' > delta has conditional expectation exp((θ'−delta)(θ−delta)·ΔInfo) ≤ 1, so
// the ratio is a nonnegative supermartingale under every null in the composite
// hypothesis, and so is its mixture over θ'. It is therefore an e-process:
// P(sup_n Λ_n ≥ 1/α) ≤ α (Ville's inequality), and Λ at any stopping time has
// expectation at most 1, which is what e-BH needs. The truncation to θ' > delta
// is what makes the one-sided composite null valid.
//
// Closed form. With a = Score − delta·Info and b = Info + 1/tau²:
//
//	Λ = 2/(tau·√b) · exp(a²/(2b)) · Φ(a/√b)
//
// computed in log space.
func (s Shift) LogE(delta, tau float64) float64 {
	if s.Info <= 0 || tau <= 0 {
		return 0
	}
	a := s.Score - delta*s.Info
	b := s.Info + 1/(tau*tau)
	sb := math.Sqrt(b)
	z := a / sb
	return math.Ln2 - math.Log(tau*sb) + z*z/2 + logNormCDF(z)
}

// LogETwoSided is the two-sided mixture likelihood ratio for H0: θ = theta0 with
// a N(0, tau²) mixing prior around theta0.
func (s Shift) LogETwoSided(theta0, tau float64) float64 {
	if s.Info <= 0 || tau <= 0 {
		return 0
	}
	a := s.Score - theta0*s.Info
	return -0.5*math.Log(1+tau*tau*s.Info) + tau*tau*a*a/(2*(1+tau*tau*s.Info))
}

// ConfidenceRadius is the half-width of the time-uniform normal-mixture
// confidence sequence for θ at level alpha: with probability at least 1−alpha,
// θ is inside Estimate ± radius at every n simultaneously (Howard et al. 2021,
// the normal-mixture boundary; Robbins 1970). It is NaN with no information.
func (s Shift) ConfidenceRadius(tau, alpha float64) float64 {
	if s.Info <= 0 || tau <= 0 || alpha <= 0 || alpha >= 1 {
		return nan
	}
	t2v := tau * tau * s.Info
	return math.Sqrt((1 + t2v) / (tau * tau * s.Info * s.Info) * math.Log((1+t2v)/(alpha*alpha)))
}

// EProcess tracks a unit's evidence over time: the latest e-value and the
// running maximum, whose reciprocal is an always-valid p-value.
type EProcess struct {
	LogE    float64 // ln of the latest e-value
	LogEMax float64 // ln of the running maximum, at least 0
}

// Update records the e-value for the current Shift.
func (p *EProcess) Update(s Shift, delta, tau float64) {
	p.LogE = s.LogE(delta, tau)
	if p.LogE > p.LogEMax {
		p.LogEMax = p.LogE
	}
}

// PValue is the always-valid p-value min(1, 1/max_k Λ_k).
func (p EProcess) PValue() float64 {
	return math.Min(1, math.Exp(-p.LogEMax))
}

// EBH applies the e-BH procedure of Wang and Ramdas (2022) at level alpha to
// the e-values, given as logs, and reports which hypotheses are rejected. It
// controls the false discovery rate at alpha under arbitrary dependence between
// the e-values: reject the k* hypotheses with the largest e-values, where k* is
// the largest k with e_(k) ≥ m/(alpha·k). m is the size of the family, which can
// exceed len(logE) when some hypotheses were left out because they were never
// candidates (they enter with e = 0 and can only lower the count).
func EBH(logE []float64, alpha float64, m int) []bool {
	if m < len(logE) {
		m = len(logE)
	}
	idx := make([]int, len(logE))
	for i := range idx {
		idx[i] = i
	}
	// descending by e-value; stable for determinism
	sortByDesc(idx, logE)
	k := 0
	for rank, i := range idx {
		kk := rank + 1
		if logE[i] >= math.Log(float64(m))-math.Log(alpha*float64(kk)) {
			k = kk
		}
	}
	rej := make([]bool, len(logE))
	for _, i := range idx[:k] {
		rej[i] = true
	}
	return rej
}

func sortByDesc(idx []int, v []float64) {
	// insertion sort keeps equal values in index order and avoids a closure
	// allocation; family sizes are in the hundreds.
	for i := 1; i < len(idx); i++ {
		j := i
		for j > 0 && v[idx[j-1]] < v[idx[j]] {
			idx[j-1], idx[j] = idx[j], idx[j-1]
			j--
		}
	}
}
