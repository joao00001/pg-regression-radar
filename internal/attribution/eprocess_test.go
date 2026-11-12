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
	"math/rand/v2"
	"testing"
)

func rng(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }

func TestLogEMatchesNumericalIntegration(t *testing.T) {
	// Λ = ∫_{θ'>δ} exp((θ'-δ)·a − (θ'-δ)²·I/2) · 2φ((θ'-δ)/τ)/τ d(θ'-δ)
	for _, c := range []struct{ score, info, delta, tau float64 }{
		{3, 10, 0, 0.5}, {-4, 10, 0.1, 0.3}, {12, 20, 0.2, 1}, {0, 1, 0, 2}, {7, 5, 0.5, 0.25},
	} {
		s := Shift{Score: c.score, Info: c.info}
		a := c.score - c.delta*c.info
		var sum float64
		const h = 1e-4
		for u := h / 2; u < 40*c.tau; u += h {
			sum += math.Exp(u*a-u*u*c.info/2) * 2 * math.Exp(-u*u/(2*c.tau*c.tau)) / (c.tau * math.Sqrt(2*math.Pi)) * h
		}
		got := math.Exp(s.LogE(c.delta, c.tau))
		if math.Abs(got-sum)/sum > 1e-3 {
			t.Errorf("%+v: closed form %.6g, numeric %.6g", c, got, sum)
		}
	}
}

func TestLogETwoSidedMatchesNumericalIntegration(t *testing.T) {
	s := Shift{Score: 5, Info: 8}
	tau := 0.7
	var sum float64
	const h = 1e-3
	for u := -40 * tau; u < 40*tau; u += h {
		sum += math.Exp(u*s.Score-u*u*s.Info/2) * math.Exp(-u*u/(2*tau*tau)) / (tau * math.Sqrt(2*math.Pi)) * h
	}
	if got := math.Exp(s.LogETwoSided(0, tau)); math.Abs(got-sum)/sum > 1e-3 {
		t.Errorf("two-sided %.6g vs %.6g", got, sum)
	}
}

// Ville's inequality: under the null, the probability that the e-process ever
// reaches 1/alpha within the horizon is at most alpha, however often it is
// looked at. This is the property that lets the engine re-test on every scrape.
func TestNullTimeUniformErrorRate(t *testing.T) {
	const (
		alpha = 0.05
		sims  = 4000
		n     = 400
		tau   = 0.3
	)
	for _, c := range []struct {
		name         string
		theta, delta float64
	}{
		{"point null", 0, 0},
		{"composite null, strictly inside", -0.1, 0},
		{"composite null, on the boundary of a margin", 0.2, 0.2},
		{"composite null, far inside a margin", 0.05, 0.2},
	} {
		r := rng(11)
		crossed := 0
		for k := 0; k < sims; k++ {
			var p EProcess
			var s Shift
			for i := 0; i < n; i++ {
				s.Add(1, c.theta+r.NormFloat64(), 1)
				p.Update(s, c.delta, tau)
			}
			if p.PValue() <= alpha {
				crossed++
			}
		}
		rate := float64(crossed) / sims
		// 4000 sims: the standard error at 0.05 is 0.0034; allow 3 SE over alpha.
		if rate > alpha+3*math.Sqrt(alpha*(1-alpha)/sims) {
			t.Errorf("%s: type-I error %.4f exceeds alpha %.2f", c.name, rate, alpha)
		}
		t.Logf("%s: ever-crossed rate %.4f (alpha %.2f)", c.name, rate, alpha)
	}
}

func TestDetectsRealEffect(t *testing.T) {
	r := rng(5)
	const tau = 0.3
	detected, sims := 0, 500
	var meanN float64
	for k := 0; k < sims; k++ {
		var s Shift
		for i := 1; i <= 400; i++ {
			s.Add(1, 0.3+r.NormFloat64(), 1) // a true effect of 0.3 sd per observation
			if s.LogE(0, tau) >= math.Log(20) {
				detected++
				meanN += float64(i)
				break
			}
		}
	}
	if float64(detected)/float64(sims) < 0.99 {
		t.Errorf("power = %.3f", float64(detected)/float64(sims))
	}
	t.Logf("effect 0.3σ: detected %d/%d after %.1f observations on average", detected, sims, meanN/float64(detected))
}

func TestMarginMakesSmallEffectsUndetectable(t *testing.T) {
	// A true effect of 0.1 with a practical margin of 0.2 is below the margin:
	// the test of H0: θ ≤ 0.2 must not reject more often than alpha.
	r := rng(7)
	const alpha, sims, n, tau = 0.05, 2000, 400, 0.3
	crossed := 0
	for k := 0; k < sims; k++ {
		var s Shift
		var p EProcess
		for i := 0; i < n; i++ {
			s.Add(1, 0.1+r.NormFloat64()*1, 1)
			p.Update(s, 0.2, tau)
		}
		if p.PValue() <= alpha {
			crossed++
		}
	}
	if rate := float64(crossed) / sims; rate > alpha+0.015 {
		t.Errorf("rejected a sub-margin effect %.3f of the time", rate)
	}
}

func TestConfidenceSequenceCoverage(t *testing.T) {
	r := rng(21)
	const alpha, sims, n, tau = 0.05, 3000, 300, 0.3
	theta := 0.15
	miss := 0
	for k := 0; k < sims; k++ {
		var s Shift
		bad := false
		for i := 0; i < n && !bad; i++ {
			s.Add(1, theta+r.NormFloat64(), 1)
			est, _ := s.Estimate()
			if math.Abs(est-theta) > s.ConfidenceRadius(tau, alpha) {
				bad = true
			}
		}
		if bad {
			miss++
		}
	}
	rate := float64(miss) / sims
	if rate > alpha+3*math.Sqrt(alpha*(1-alpha)/sims) {
		t.Errorf("confidence sequence missed θ in %.4f of runs (alpha %.2f)", rate, alpha)
	}
	t.Logf("confidence sequence miss rate %.4f (alpha %.2f)", rate, alpha)
}

func TestEProcessPValueIsMonotone(t *testing.T) {
	var p EProcess
	prev := 1.0
	r := rng(3)
	var s Shift
	for i := 0; i < 200; i++ {
		s.Add(1, 0.2+r.NormFloat64(), 1)
		p.Update(s, 0, 0.3)
		if p.PValue() > prev+1e-15 {
			t.Fatalf("always-valid p-value rose from %g to %g", prev, p.PValue())
		}
		prev = p.PValue()
	}
	if p.LogEMax < p.LogE {
		t.Error("running max below current")
	}
}

func TestEBHKnownCases(t *testing.T) {
	ln := math.Log
	// m=4, alpha=0.1: thresholds e_(k) >= 4/(0.1k) = 40/k -> 40, 20, 13.3, 10
	got := EBH([]float64{ln(50), ln(5), ln(25), ln(1)}, 0.1, 4)
	want := []bool{true, false, true, false}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("EBH = %v, want %v", got, want)
			break
		}
	}
	// the k-th largest passing implies all larger pass: e=(30,30,30) -> k=3 needs 13.3
	got = EBH([]float64{ln(30), ln(30), ln(30)}, 0.1, 4)
	if !got[0] || !got[1] || !got[2] {
		t.Errorf("EBH = %v, want all true", got)
	}
	// a larger family than the candidates tightens the bar
	if r := EBH([]float64{ln(30)}, 0.1, 1); !r[0] {
		t.Error("single e=30 should pass at m=1")
	}
	if r := EBH([]float64{ln(30)}, 0.1, 500); r[0] {
		t.Error("single e=30 must not pass when the family has 500 hypotheses")
	}
	if len(EBH(nil, 0.1, 10)) != 0 {
		t.Error("empty input")
	}
}

// e-BH controls the FDR at alpha under arbitrary dependence. Build a family of
// 200 queries, 10% truly regressed, with the null observations strongly
// positively correlated through a shared shock, and check the realised false
// discovery proportion averaged over many families.
func TestEBHControlsFDRUnderDependence(t *testing.T) {
	const (
		alpha = 0.1
		reps  = 300
		m     = 200
		n     = 40
		tau   = 0.3
	)
	r := rng(99)
	var fdpSum, powerSum float64
	for rep := 0; rep < reps; rep++ {
		logE := make([]float64, m)
		isAlt := make([]bool, m)
		shock := make([]float64, n)
		for i := range shock {
			shock[i] = 0.8 * r.NormFloat64() // shared across the family
		}
		for q := 0; q < m; q++ {
			isAlt[q] = q < m/10
			theta := 0.0
			if isAlt[q] {
				theta = 0.8
			}
			var s Shift
			for i := 0; i < n; i++ {
				// the shared shock is common to all queries; it inflates the
				// variance of each e-process but each remains a valid
				// supermartingale only if its own variance is honest, so the unit
				// variance below includes it.
				y := theta + math.Sqrt(1-0.64)*r.NormFloat64() + shock[i]
				s.Add(1, y, 1)
			}
			logE[q] = s.LogE(0, tau)
		}
		rej := EBH(logE, alpha, m)
		var fd, d float64
		for q, ok := range rej {
			if !ok {
				continue
			}
			d++
			if !isAlt[q] {
				fd++
			}
		}
		if d > 0 {
			fdpSum += fd / d
		}
		var tp float64
		for q := 0; q < m/10; q++ {
			if rej[q] {
				tp++
			}
		}
		powerSum += tp / float64(m/10)
	}
	fdr := fdpSum / reps
	if fdr > alpha {
		t.Errorf("FDR %.4f exceeds alpha %.2f", fdr, alpha)
	}
	t.Logf("e-BH: FDR %.4f (alpha %.2f), power %.3f, under a shared shock", fdr, alpha, powerSum/reps)
}
