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

// world is a synthetic log-latency series generator used across the tests.
type world struct {
	pre, post int
	sigma     float64 // observation sd of ln(mean latency) at the median call count
	drift     float64 // sd of the random-walk level innovation
	beta      float64 // coefficient on the shared shock
	ar        float64 // AR(1) coefficient of the shared shock
	shockSD   float64
	missing   float64 // probability a tick has no observation
	theta     float64 // true step effect on the log scale
	hetero    bool    // vary calls per tick
}

type sample struct {
	y, w []float64
	x    []float64 // the shared shock, an exogenous covariate
	T    int
}

func (c world) draw(r interface {
	NormFloat64() float64
	Float64() float64
}) sample {
	n := c.pre + c.post
	s := sample{y: make([]float64, n), w: make([]float64, n), x: make([]float64, n), T: c.pre}
	level, shock := math.Log(8), 0.0
	for t := 0; t < n; t++ {
		level += c.drift * r.NormFloat64()
		shock = c.ar*shock + c.shockSD*math.Sqrt(1-c.ar*c.ar)*r.NormFloat64()
		s.x[t] = shock
		calls := 400.0
		if c.hetero {
			calls = 50 + 800*r.Float64()
		}
		s.w[t] = calls
		if r.Float64() < c.missing {
			s.y[t], s.w[t] = nan, 0
			continue
		}
		v := level + c.beta*shock + c.sigma*math.Sqrt(400/calls)*r.NormFloat64()
		if t >= c.pre {
			v += c.theta
		}
		s.y[t] = v
	}
	return s
}

func (s sample) cov(use bool) [][]float64 {
	if !use {
		return nil
	}
	return [][]float64{s.x}
}

// everCrossed reports whether the e-process reached 1/alpha at any post tick.
func everCrossed(res StepResult, delta, tau, alpha float64) bool {
	if !res.Ok {
		return false
	}
	var p EProcess
	for _, sh := range res.Path {
		p.Update(sh, delta, tau)
	}
	return p.PValue() <= alpha
}

func rate(sims int, f func(i int) bool) float64 {
	k := 0
	for i := 0; i < sims; i++ {
		if f(i) {
			k++
		}
	}
	return float64(k) / float64(sims)
}

func TestStepTestRecoversTheEffect(t *testing.T) {
	r := rng(1)
	w := world{pre: 80, post: 40, sigma: 0.08, drift: 0.005, theta: math.Log(1.5)}
	var sum, sumSE float64
	const sims = 200
	for i := 0; i < sims; i++ {
		res := StepTest(w.draw(r).yw())
		sum += res.Theta
		sumSE += res.SE
	}
	got := sum / sims
	if math.Abs(got-w.theta) > 0.03 {
		t.Errorf("mean estimate %.4f, true %.4f", got, w.theta)
	}
	t.Logf("true %.4f, mean estimate %.4f, mean SE %.4f", w.theta, got, sumSE/sims)
}

func (s sample) yw() ([]float64, []float64, [][]float64, int, StepOptions) {
	return s.y, s.w, nil, s.T, StepOptions{}
}

// With no effect, the chance that the e-process ever reaches 1/alpha during
// the post-period, looked at on every tick, stays at or under alpha across
// level drift, missing ticks and varying call counts. This is the guarantee the
// action policy relies on.
func TestNullErrorRateAcrossRegimes(t *testing.T) {
	const alpha, sims = 0.05, 1500
	for _, c := range []struct {
		name string
		w    world
	}{
		{"steady", world{pre: 80, post: 40, sigma: 0.1, drift: 0}},
		{"slow drift", world{pre: 80, post: 40, sigma: 0.1, drift: 0.01}},
		{"fast drift", world{pre: 80, post: 40, sigma: 0.1, drift: 0.05}},
		{"missing ticks and varying calls", world{pre: 80, post: 40, sigma: 0.1, drift: 0.01, missing: 0.25, hetero: true}},
		{"short history", world{pre: 25, post: 40, sigma: 0.1, drift: 0.01}},
	} {
		r := rng(31)
		got := rate(sims, func(int) bool {
			s := c.w.draw(r)
			return everCrossed(StepTest(s.y, s.w, nil, s.T, StepOptions{}), 0, 0.25, alpha)
		})
		if got > alpha+3*math.Sqrt(alpha*(1-alpha)/sims) {
			t.Errorf("%s: type-I error %.4f exceeds %.2f", c.name, got, alpha)
		}
		t.Logf("%s: type-I error %.4f", c.name, got)
	}
}

func TestPowerAgainstARealRegression(t *testing.T) {
	const alpha, sims = 0.05, 600
	for _, c := range []struct {
		name  string
		w     world
		floor float64
	}{
		{"50% slower, steady", world{pre: 80, post: 40, sigma: 0.1, drift: 0.005, theta: math.Log(1.5)}, 0.98},
		{"25% slower, steady", world{pre: 80, post: 40, sigma: 0.1, drift: 0.005, theta: math.Log(1.25)}, 0.8},
		{"50% slower, missing ticks", world{pre: 80, post: 40, sigma: 0.1, drift: 0.005, missing: 0.25, hetero: true, theta: math.Log(1.5)}, 0.95},
	} {
		r := rng(77)
		got := rate(sims, func(int) bool {
			s := c.w.draw(r)
			return everCrossed(StepTest(s.y, s.w, nil, s.T, StepOptions{}), 0, 0.25, alpha)
		})
		if got < c.floor {
			t.Errorf("%s: power %.3f below %.2f", c.name, got, c.floor)
		}
		t.Logf("%s: power %.3f", c.name, got)
	}
}

// The reason the covariates exist: a shock shared by the query and its
// siblings (load, fresh statistics, cache state) shifts the series with no
// deploy involved. Without covariates that is a false alarm; with them, the
// shock is part of the counterfactual and the alarm rate returns to alpha.
func TestSharedShockIsExplainedAwayByCovariates(t *testing.T) {
	const alpha, sims = 0.05, 1000
	// a shock that steps up exactly at the deploy tick, in the shared covariate
	build := func(r interface {
		NormFloat64() float64
		Float64() float64
	}) sample {
		w := world{pre: 80, post: 40, sigma: 0.08, drift: 0.005, beta: 1, ar: 0.5, shockSD: 0.15}
		s := w.draw(r)
		for t := s.T; t < len(s.y); t++ {
			s.x[t] += 0.4 // a load shift at the same moment, which the query feels through β
			if !isNaN(s.y[t]) {
				s.y[t] += 0.4
			}
		}
		return s
	}
	r := rng(5)
	without := rate(sims, func(int) bool {
		s := build(r)
		return everCrossed(StepTest(s.y, s.w, nil, s.T, StepOptions{}), 0, 0.25, alpha)
	})
	r = rng(5)
	with := rate(sims, func(int) bool {
		s := build(r)
		return everCrossed(StepTest(s.y, s.w, s.cov(true), s.T, StepOptions{}), 0, 0.25, alpha)
	})
	t.Logf("false-alarm rate without covariates %.3f, with %.3f", without, with)
	if without < 0.5 {
		t.Errorf("the scenario should fool the history-only test (got %.3f)", without)
	}
	if with > alpha+3*math.Sqrt(alpha*(1-alpha)/sims) {
		t.Errorf("covariates did not remove the shared shock: %.3f", with)
	}
}

func TestCovariatesDoNotHideARealRegression(t *testing.T) {
	const alpha, sims = 0.05, 600
	w := world{pre: 80, post: 40, sigma: 0.08, drift: 0.005, beta: 1, ar: 0.5, shockSD: 0.15, theta: math.Log(1.4)}
	r := rng(8)
	got := rate(sims, func(int) bool {
		s := w.draw(r)
		return everCrossed(StepTest(s.y, s.w, s.cov(true), s.T, StepOptions{}), 0, 0.25, alpha)
	})
	if got < 0.95 {
		t.Errorf("power with covariates %.3f", got)
	}
	t.Logf("power with covariates %.3f", got)
}

func TestStepTestRefusesWhatItCannotFit(t *testing.T) {
	r := rng(2)
	s := world{pre: 6, post: 20, sigma: 0.1}.draw(r)
	if res := StepTest(s.y, s.w, nil, s.T, StepOptions{}); res.Ok || res.Reason == "" {
		t.Errorf("fit on 6 points: %+v", res)
	}
	s = world{pre: 40, post: 20, sigma: 0.1}.draw(r)
	if res := StepTest(s.y, s.w, nil, 0, StepOptions{}); res.Ok {
		t.Error("T = 0 should be refused")
	}
	if res := StepTest(s.y, s.w, nil, len(s.y), StepOptions{}); res.Ok {
		t.Error("T = n should be refused")
	}
	for i := s.T; i < len(s.y); i++ {
		s.y[i], s.w[i] = nan, 0
	}
	if res := StepTest(s.y, s.w, nil, s.T, StepOptions{}); res.Ok {
		t.Error("no post observations should be refused")
	}
	if res := StepTest(s.y, s.w[:5], nil, s.T, StepOptions{}); res.Ok {
		t.Error("mismatched lengths should be refused")
	}
}

func TestPathIsCumulativeAndMonotoneInInformation(t *testing.T) {
	r := rng(4)
	s := world{pre: 60, post: 30, sigma: 0.1, drift: 0.01, theta: 0.3, missing: 0.2}.draw(r)
	res := StepTest(s.y, s.w, nil, s.T, StepOptions{})
	if !res.Ok || len(res.Path) != 30 {
		t.Fatalf("res = %+v, path %d", res.Ok, len(res.Path))
	}
	for i := 1; i < len(res.Path); i++ {
		if res.Path[i].Info < res.Path[i-1].Info-1e-12 {
			t.Fatalf("information fell at %d", i)
		}
	}
	if res.Path[len(res.Path)-1] != res.Shift {
		t.Error("final path entry differs from the result")
	}
}
