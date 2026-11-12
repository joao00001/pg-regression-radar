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

package attribution_test

import (
	"math/rand"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
)

// The assertions below turn the scenario report into pass/fail properties. They
// use more seeds than the report and fail loudly if a calibration claim breaks.

// skipLong skips the multi-minute statistical tests unless PGRR_LONG is set. CI
// runs the unit tests with -race and coverage on two cores, where these take
// tens of minutes; the quick calibration test below covers the same claims at a
// size that fits. Run the full set with: PGRR_LONG=1 go test ./internal/attribution/
func skipLong(t *testing.T) {
	t.Helper()
	if os.Getenv("PGRR_LONG") == "" {
		t.Skip("long statistical test; set PGRR_LONG=1 to run")
	}
}

// A small version of the calibration claims that always runs: a few runs of
// small workloads, enough to catch a broken engine, not to estimate rates.
func TestQuickCalibration(t *testing.T) {
	cfg := attribution.Config{}
	small := func(w sim.World) sim.World { w.Queries = 40; return w }
	null := score(runSeeds(func(s uint64) sim.World { return small(sim.World{Seed: s}) }, 5, cfg), realRegression)
	if null.fpAB != 0 || null.anyRegressed > 2 {
		t.Fatalf("no change: %d runs flagged, %d at A/B", null.anyRegressed, null.fpAB)
	}
	shock := score(runSeeds(func(s uint64) sim.World { return small(sim.World{Seed: s, LoadShock: 0.7}) }, 4, cfg), realRegression)
	if shock.fpAB != 0 {
		t.Fatalf("load shock convicted at A/B: %d", shock.fpAB)
	}
	canary := score(runSeeds(func(s uint64) sim.World {
		return small(sim.World{Seed: s, Canary: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}})
	}, 4, cfg), realRegression)
	if canary.impactTrue == 0 || canary.impactFound/canary.impactTrue < 0.7 || canary.fp != 0 {
		t.Fatalf("canary: found %.2f of %.2f, %d false positives", canary.impactFound, canary.impactTrue, canary.fp)
	}
}

// No change at all: the share of runs that call any regression must stay at or
// below the nominal level (with slack for 60 runs), and no run may reach grade A
// or B.
func TestTypeIErrorUnderNoChange(t *testing.T) {
	skipLong(t)
	const runs = 60
	outs := runSeeds(func(s uint64) sim.World { return sim.World{Seed: 1000 + s} }, runs, attribution.Config{})
	s := score(outs, realRegression)
	if s.anyRegressed > 6 { // alpha = 0.05 → expect ≤ 3; 6 is a ~2-sigma bound
		t.Fatalf("A/A: %d of %d runs called a regression (alpha 0.05)", s.anyRegressed, runs)
	}
	if s.fpAB != 0 {
		t.Fatalf("A/A: %d false positives at grade A/B", s.fpAB)
	}
}

// Whatever else happens to the database, a deploy that changed nothing must not
// be convicted at grade A or B. Grade C may fire (it only ever alerts).
func TestNoConvictionAtABWhenDeployDidNothing(t *testing.T) {
	skipLong(t)
	worlds := map[string]func(uint64) sim.World{
		"load shock":    func(s uint64) sim.World { return sim.World{Seed: s, LoadShock: 0.7} },
		"stats refresh": func(s uint64) sim.World { return sim.World{Seed: s, StatsTable: 3, StatsFactor: 1.8} },
		"stats reported": func(s uint64) sim.World {
			return sim.World{Seed: s, StatsTable: 3, StatsFactor: 1.8, StatsRecorded: true}
		},
		"cold cache":        func(s uint64) sim.World { return sim.World{Seed: s, Warmup: 1.8, WarmupTicks: 10} },
		"rewritten queries": func(s uint64) sim.World { return sim.World{Seed: s, Rewrite: 0.1, RewriteFactor: 1} },
		"concurrent deploy": func(s uint64) sim.World {
			return sim.World{Seed: s, ConcurrentAt: 8, Concurrent: sim.Effect{Fraction: 0.2, Offset: 40, Factor: 1.7}}
		},
		"canary load shock": func(s uint64) sim.World { return sim.World{Seed: s, Canary: true, LoadShock: 0.7} },
		"below the margin": func(s uint64) sim.World {
			return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.1}}
		},
	}
	for name, build := range worlds {
		t.Run(name, func(t *testing.T) {
			s := score(runSeeds(build, 40, attribution.Config{}), realRegression)
			if s.fpAB != 0 {
				t.Fatalf("%d grade A/B false positives over 40 runs", s.fpAB)
			}
		})
	}
}

// A transient (a warm-up that decays) is not a regression: at most the nominal
// share of runs may flag a single query (grade C, alert only), never at A or B.
func TestTransientIsNeverRegressed(t *testing.T) {
	skipLong(t)
	s := score(runSeeds(func(u uint64) sim.World { return sim.World{Seed: u, Warmup: 1.8, WarmupTicks: 10} }, 40, attribution.Config{}), realRegression)
	if s.anyRegressed > 4 { // alpha 0.05 over 40 runs: expect ≤ 2
		t.Fatalf("%d of 40 warm-up runs called a regression", s.anyRegressed)
	}
	if s.fpAB != 0 {
		t.Fatalf("warm-up convicted at grade A/B in %d cases", s.fpAB)
	}
}

// A first deploy has no history: the engine must say it cannot tell, never
// convict, and the policy must not block.
func TestFirstDeployNeverConvicts(t *testing.T) {
	skipLong(t)
	outs := runSeeds(func(u uint64) sim.World {
		return sim.World{Seed: u, FirstDeploy: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
	}, 20, attribution.Config{})
	for i, o := range outs {
		if o.a.Health != attribution.HealthInsufficient {
			t.Fatalf("run %d: health %s, want InsufficientData", i, o.a.Health)
		}
		if d := attribution.NewPolicy(attribution.PolicyConfig{}).Decide(time.Now(), o.a); d.Action != attribution.ActionNone {
			t.Fatalf("run %d: policy chose %s on insufficient data", i, d.Action)
		}
	}
}

// Power: with a control arm the engine must find the regression that matters.
func TestPowerOnMaterialRegressions(t *testing.T) {
	skipLong(t)
	canary := score(runSeeds(func(u uint64) sim.World {
		return sim.World{Seed: u, Canary: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
	}, 30, attribution.Config{}), realRegression)
	if r := canary.impactFound / canary.impactTrue; r < 0.9 {
		t.Fatalf("canary impact-recall %.2f < 0.90", r)
	}
	if canary.fp != 0 {
		t.Fatalf("canary: %d false positives", canary.fp)
	}
	synth := score(runSeeds(func(u uint64) sim.World {
		return sim.World{Seed: u, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
	}, 30, attribution.Config{}), realRegression)
	if r := synth.impactFound / synth.impactTrue; r < 0.8 {
		t.Fatalf("synthetic-control impact-recall %.2f < 0.80", r)
	}
	if synth.fp != 0 {
		t.Fatalf("synthetic control: %d false positives", synth.fp)
	}
}

// Same input, same output: the engine has no hidden randomness or map-order
// dependence.
func TestDeterminism(t *testing.T) {
	units, ev, d, _ := sim.World{Seed: 7, Queries: 40, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	a := attribution.Assess(attribution.Config{}, units, ev, d)
	for i := 0; i < 2; i++ {
		b := attribution.Assess(attribution.Config{}, units, ev, d)
		if a.Health != b.Health || a.Impact != b.Impact || a.Attribution != b.Attribution {
			t.Fatalf("run %d differs: %+v vs %+v", i, a.Impact, b.Impact)
		}
		for j := range a.Units {
			if a.Units[j].Kind != b.Units[j].Kind || a.Units[j].LogEReg != b.Units[j].LogEReg {
				t.Fatalf("unit %d differs", j)
			}
		}
	}
}

// The order in which units are listed must not change any verdict.
func TestUnitOrderInvariance(t *testing.T) {
	units, ev, d, _ := sim.World{Seed: 9, Queries: 40, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	a := attribution.Assess(attribution.Config{}, units, ev, d)
	want := map[string]attribution.UnitVerdict{}
	for _, v := range a.Units {
		want[v.Key] = v
	}
	shuffled := append([]attribution.Unit(nil), units...)
	rand.New(rand.NewSource(1)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	b := attribution.Assess(attribution.Config{}, shuffled, ev, d)
	if a.Health != b.Health || !floatClose(a.Impact.A, b.Impact.A) || !floatClose(a.Impact.AB, b.Impact.AB) || !floatClose(a.Impact.ABC, b.Impact.ABC) {
		t.Fatalf("assessment changed with unit order: %+v vs %+v", a.Impact, b.Impact)
	}
	for _, v := range b.Units {
		w := want[v.Key]
		if v.Kind != w.Kind || v.Grade != w.Grade || !floatClose(v.LogEReg, w.LogEReg) {
			t.Fatalf("unit %s changed with order: %v/%v vs %v/%v", v.Key, v.Kind, v.Grade, w.Kind, w.Grade)
		}
	}
}

// Measuring in other units (seconds instead of milliseconds) must not change
// the verdicts: the engine works on ratios.
func TestScaleInvariance(t *testing.T) {
	units, ev, d, _ := sim.World{Seed: 11, Queries: 40, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	a := attribution.Assess(attribution.Config{}, units, ev, d)
	scaled := make([]attribution.Unit, len(units))
	for i, u := range units {
		u.Obs = append([]attribution.Obs(nil), u.Obs...)
		for j := range u.Obs {
			u.Obs[j].MeanMs *= 1000
		}
		u.Control = append([]attribution.Obs(nil), u.Control...)
		for j := range u.Control {
			u.Control[j].MeanMs *= 1000
		}
		scaled[i] = u
	}
	b := attribution.Assess(attribution.Config{}, scaled, ev, d)
	if a.Health != b.Health || a.Regressed != b.Regressed {
		t.Fatalf("scale changed the result: %s/%d vs %s/%d", a.Health, a.Regressed, b.Health, b.Regressed)
	}
}

// Missing and degenerate data must never panic and never convict.
func TestDegenerateInputs(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d := attribution.Deploy{ID: "d", At: at, Reversible: true, HasRollbackTarget: true}
	cases := map[string][]attribution.Unit{
		"no units":           nil,
		"empty unit":         {{Key: "a"}},
		"single observation": {{Key: "a", Obs: []attribution.Obs{{At: at, MeanMs: 1, Calls: 1}}}},
		"zero latency":       {{Key: "a", Obs: []attribution.Obs{{At: at.Add(-time.Minute), MeanMs: 0, Calls: 5}, {At: at.Add(time.Minute), MeanMs: 0, Calls: 5}}}},
		"non-finite":         {{Key: "a", Obs: []attribution.Obs{{At: at.Add(-time.Minute), MeanMs: nan(), Calls: 5}, {At: at.Add(time.Minute), MeanMs: 3, Calls: 5}}}},
	}
	for name, units := range cases {
		t.Run(name, func(t *testing.T) {
			a := attribution.Assess(attribution.Config{}, units, nil, d)
			if a.Health == attribution.HealthRegressed {
				t.Fatalf("degenerate input convicted: %+v", a)
			}
		})
	}
}

// Policy decisions must be a pure function of the assessment sequence.
func TestPolicyDeterministic(t *testing.T) {
	units, ev, d, _ := sim.World{Seed: 5, Queries: 40, Canary: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	a := attribution.Assess(attribution.Config{}, units, ev, d)
	run := func() []attribution.Decision {
		p := attribution.NewPolicy(attribution.PolicyConfig{})
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 4; i++ {
			p.Decide(now.Add(time.Duration(i)*5*time.Minute), a)
		}
		return p.Ledger()
	}
	if !reflect.DeepEqual(run(), run()) {
		t.Fatal("policy is not deterministic")
	}
}

func floatClose(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1e-9*(1+abs(a)+abs(b))
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func nan() float64 { z := 0.0; return z / z }
