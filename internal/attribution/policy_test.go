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
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func regressed(g attribution.Grade, impact float64) attribution.Assessment {
	a := attribution.Assessment{
		Deploy:      attribution.Deploy{ID: "d1", Reversible: true, HasRollbackTarget: true},
		Health:      attribution.HealthRegressed,
		Attribution: 1,
	}
	switch g {
	case attribution.GradeA:
		a.Impact = attribution.Impact{A: impact, AB: impact, ABC: impact}
	case attribution.GradeB:
		a.Impact = attribution.Impact{AB: impact, ABC: impact}
	default:
		a.Impact = attribution.Impact{ABC: impact}
	}
	return a
}

// decideTwice feeds the same assessment on two consecutive evaluations.
func decideTwice(p *attribution.Policy, a attribution.Assessment) attribution.Decision {
	p.Decide(t0, a)
	return p.Decide(t0.Add(5*time.Minute), a)
}

func TestPolicyLadder(t *testing.T) {
	cases := []struct {
		name string
		a    attribution.Assessment
		want attribution.Action
	}{
		{"grade A rolls back", regressed(attribution.GradeA, 0.2), attribution.ActionRollback},
		{"grade B holds", regressed(attribution.GradeB, 0.2), attribution.ActionHold},
		{"grade C only alerts", regressed(attribution.GradeC, 0.2), attribution.ActionAlert},
		{"immaterial impact does nothing", regressed(attribution.GradeA, 0.01), attribution.ActionNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), c.a); got.Action != c.want {
				t.Fatalf("got %s (%v), want %s", got.Action, got.Reasons, c.want)
			}
		})
	}
}

func TestPolicyGuards(t *testing.T) {
	t.Run("first evaluation only alerts", func(t *testing.T) {
		p := attribution.NewPolicy(attribution.PolicyConfig{})
		if d := p.Decide(t0, regressed(attribution.GradeA, 0.2)); d.Action != attribution.ActionAlert || d.Wanted != attribution.ActionRollback {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("first deploy has no rollback target: hold, not rollback", func(t *testing.T) {
		a := regressed(attribution.GradeA, 0.2)
		a.Deploy.HasRollbackTarget = false
		if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Action != attribution.ActionHold {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("irreversible deploy: hold", func(t *testing.T) {
		a := regressed(attribution.GradeA, 0.2)
		a.Deploy.Reversible = false
		if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Action != attribution.ActionHold {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("a concurrent deploy may be the cause: alert", func(t *testing.T) {
		a := regressed(attribution.GradeA, 0.2)
		a.Attribution = 0.5
		if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Action != attribution.ActionAlert {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("global shift without a control arm: alert", func(t *testing.T) {
		a := regressed(attribution.GradeB, 0.2)
		a.GlobalShift = true
		if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Action != attribution.ActionAlert {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("global shift with a control arm still acts", func(t *testing.T) {
		a := regressed(attribution.GradeA, 0.2)
		a.GlobalShift = true
		if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Action != attribution.ActionRollback {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("insufficient data never blocks", func(t *testing.T) {
		a := attribution.Assessment{Deploy: attribution.Deploy{ID: "d1"}, Health: attribution.HealthInsufficient}
		if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Action != attribution.ActionNone {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("a healthy evaluation resets the streak", func(t *testing.T) {
		p := attribution.NewPolicy(attribution.PolicyConfig{})
		p.Decide(t0, regressed(attribution.GradeA, 0.2))
		p.Decide(t0.Add(time.Minute), attribution.Assessment{Deploy: attribution.Deploy{ID: "d1"}, Health: attribution.HealthHealthy})
		if d := p.Decide(t0.Add(2*time.Minute), regressed(attribution.GradeA, 0.2)); d.Action != attribution.ActionAlert {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("cooldown between actions", func(t *testing.T) {
		p := attribution.NewPolicy(attribution.PolicyConfig{})
		decideTwice(p, regressed(attribution.GradeB, 0.2))
		if d := p.Decide(t0.Add(6*time.Minute), regressed(attribution.GradeB, 0.2)); d.Action != attribution.ActionAlert {
			t.Fatalf("got %+v", d)
		}
		if d := p.Decide(t0.Add(30*time.Minute), regressed(attribution.GradeB, 0.2)); d.Action != attribution.ActionHold {
			t.Fatalf("got %+v", d)
		}
	})
}

func TestPolicyShadowAndEnforce(t *testing.T) {
	a := regressed(attribution.GradeA, 0.2)
	if d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a); d.Enforced {
		t.Fatal("shadow mode must never be enforced")
	}
	p := attribution.NewPolicy(attribution.PolicyConfig{Enforce: true})
	if d := decideTwice(p, a); !d.Enforced || d.Action != attribution.ActionRollback {
		t.Fatalf("got %+v", d)
	}
	if n := len(p.Ledger()); n != 2 {
		t.Fatalf("ledger has %d entries, want 2", n)
	}
}

func TestPolicyCircuitBreaker(t *testing.T) {
	p := attribution.NewPolicy(attribution.PolicyConfig{Enforce: true, Cooldown: time.Second})
	still := regressed(attribution.GradeA, 0.2)
	for i := 0; i < 2; i++ {
		if v := p.Verify(still); v.Effective {
			t.Fatal("a still-regressed verification must be a failure")
		}
	}
	if !p.BreakerOpen() {
		t.Fatal("breaker should be open after two failed actions")
	}
	d := decideTwice(p, regressed(attribution.GradeA, 0.2))
	if d.Action != attribution.ActionAlert || d.Enforced {
		t.Fatalf("open breaker must only alert, got %+v", d)
	}
	p.Reset()
	if p.BreakerOpen() {
		t.Fatal("reset should close the breaker")
	}
	if v := p.Verify(attribution.Assessment{Health: attribution.HealthHealthy}); !v.Effective {
		t.Fatal("a healthy verification is effective")
	}
}

// Through the simulator: whatever the world, a scenario with no real regression
// must never produce an action above Alert, and a real regression must produce
// the action its evidence grade calls for.
func TestPolicyEndToEnd(t *testing.T) {
	skipLong(t)
	type expect struct {
		name      string
		build     func(seed uint64) sim.World
		minAction attribution.Action // at least this, in the median run; empty = must stay at or below Alert
		factor    float64            // the regression factor injected
	}
	cases := []expect{
		{"A/A", func(s uint64) sim.World { return sim.World{Seed: s} }, "", 0},
		{"below margin", func(s uint64) sim.World {
			return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.1}}
		}, "", 0},
		{"load shock", func(s uint64) sim.World { return sim.World{Seed: s, LoadShock: 0.7} }, "", 0},
		{"stats refresh", func(s uint64) sim.World { return sim.World{Seed: s, StatsTable: 3, StatsFactor: 1.8} }, "", 0},
		{"cold cache", func(s uint64) sim.World { return sim.World{Seed: s, Warmup: 1.8, WarmupTicks: 10} }, "", 0},
		{"rewrites", func(s uint64) sim.World { return sim.World{Seed: s, Rewrite: 0.1, RewriteFactor: 1} }, "", 0},
		{"concurrent deploy", func(s uint64) sim.World {
			return sim.World{Seed: s, ConcurrentAt: 8, Concurrent: sim.Effect{Fraction: 0.2, Offset: 40, Factor: 1.7}}
		}, "", 0},
		{"canary load shock", func(s uint64) sim.World { return sim.World{Seed: s, Canary: true, LoadShock: 0.7} }, "", 0},
		{"first deploy", func(s uint64) sim.World {
			return sim.World{Seed: s, FirstDeploy: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
		}, "", 0},
		{"real regression", func(s uint64) sim.World {
			return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
		}, attribution.ActionHold, 1.6},
		{"canary real regression", func(s uint64) sim.World {
			return sim.World{Seed: s, Canary: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
		}, attribution.ActionRollback, 1.6},
		{"canary global slowdown", func(s uint64) sim.World { return sim.World{Seed: s, Canary: true, Global: 1.5} }, attribution.ActionRollback, 1.6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached, material := 0, 0
			for seed := uint64(1); seed <= 10; seed++ {
				units, events, dep, truth := c.build(seed).Generate()
				a := attribution.Assess(attribution.Config{}, units, events, dep)
				d := decideTwice(attribution.NewPolicy(attribution.PolicyConfig{}), a)
				if c.minAction == "" {
					if d.Action == attribution.ActionHold || d.Action == attribution.ActionRollback {
						t.Fatalf("seed %d: unjustified %s (%v)", seed, d.Action, d.Reasons)
					}
					continue
				}
				// A regression that adds little database time is below the
				// materiality threshold by design; only the material ones must
				// be acted on.
				var added float64
				for i, v := range a.Units {
					if truth.Regressed[v.Key] {
						added += v.ImpactShare * (c.factor - 1)
						_ = i
					}
				}
				if added < 0.12 {
					continue
				}
				material++
				if d.Action.Rank() >= c.minAction.Rank() {
					reached++
				} else {
					t.Errorf("seed %d: true added time %.1f%%, but the policy chose %s (%v)", seed, added*100, d.Action, d.Reasons)
				}
			}
			if c.minAction != "" && material == 0 {
				t.Fatal("no run had a material regression; the test checks nothing")
			}
			_ = reached
		})
	}
}
