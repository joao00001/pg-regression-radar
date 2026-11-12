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
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
)

// outcome is one scored run.
type outcome struct {
	a     attribution.Assessment
	truth sim.Truth
}

func runSeeds(build func(seed uint64) sim.World, seeds int, cfg attribution.Config) []outcome {
	out := make([]outcome, seeds)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for s := 0; s < seeds; s++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(s int) {
			defer wg.Done()
			defer func() { <-sem }()
			units, ev, d, truth := build(uint64(s + 1)).Generate()
			out[s] = outcome{a: attribution.Assess(cfg, units, ev, d), truth: truth}
		}(s)
	}
	wg.Wait()
	return out
}

// summary scores a batch of runs against ground truth.
type summary struct {
	runs                    int
	anyRegressed            int // runs with at least one unit called Regressed
	anyRegressedAB          int // ... at grade A or B
	tp, fp, positives       int
	fpAB                    int
	grades                  map[attribution.Grade]int
	health                  map[attribution.Health]int
	flags                   map[attribution.Flag]int
	impactA, impactAB       float64
	minAttr                 float64
	impactTrue, impactFound float64 // share of database time in truly regressed units, and in those found
}

func score(outs []outcome, isTrue func(o outcome, v attribution.UnitVerdict) bool) summary {
	s := summary{runs: len(outs), grades: map[attribution.Grade]int{}, health: map[attribution.Health]int{}, flags: map[attribution.Flag]int{}, minAttr: 1}
	for _, o := range outs {
		s.health[o.a.Health]++
		s.impactA += o.a.Impact.A
		s.impactAB += o.a.Impact.AB
		any, anyAB := false, false
		for _, v := range o.a.Units {
			if isTrue(o, v) {
				s.positives++
				s.impactTrue += v.ImpactShare
				if v.Kind == attribution.KindRegressed {
					s.impactFound += v.ImpactShare
				}
			}
			if v.Kind != attribution.KindRegressed {
				continue
			}
			any = true
			s.grades[v.Grade]++
			for _, f := range v.Flags {
				s.flags[f]++
			}
			ab := v.Grade == attribution.GradeA || v.Grade == attribution.GradeB
			if ab {
				anyAB = true
			}
			if isTrue(o, v) {
				s.tp++
			} else {
				s.fp++
				if ab {
					s.fpAB++
				}
			}
			if v.Attribution < s.minAttr {
				s.minAttr = v.Attribution
			}
		}
		if any {
			s.anyRegressed++
		}
		if anyAB {
			s.anyRegressedAB++
		}
	}
	return s
}

func (s summary) String() string {
	var gs, hs, fs []string
	for g, n := range s.grades {
		gs = append(gs, fmt.Sprintf("%s:%d", g, n))
	}
	for h, n := range s.health {
		hs = append(hs, fmt.Sprintf("%s:%d", h, n))
	}
	for f, n := range s.flags {
		fs = append(fs, fmt.Sprintf("%s:%d", f, n))
	}
	sort.Strings(gs)
	sort.Strings(hs)
	sort.Strings(fs)
	rec := 0.0
	if s.positives > 0 {
		rec = float64(s.tp) / float64(s.positives)
	}
	fdp := 0.0
	if s.tp+s.fp > 0 {
		fdp = float64(s.fp) / float64(s.tp+s.fp)
	}
	irec := 0.0
	if s.impactTrue > 0 {
		irec = s.impactFound / s.impactTrue
	}
	return fmt.Sprintf("runs=%d anyReg=%d (A/B %d) recall=%.2f impact-recall=%.2f fdp=%.3f fpAB=%d lowerImpact(A=%.3f AB=%.3f) grades[%s] health[%s] flags[%s]",
		s.runs, s.anyRegressed, s.anyRegressedAB, rec, irec, fdp, s.fpAB, s.impactA/float64(s.runs), s.impactAB/float64(s.runs),
		strings.Join(gs, " "), strings.Join(hs, " "), strings.Join(fs, " "))
}

func realRegression(o outcome, v attribution.UnitVerdict) bool { return o.truth.Regressed[v.Key] }

type scenario struct {
	name  string
	build func(seed uint64) sim.World
}

var scenarios = []scenario{
	{"A/A: nothing changed", func(s uint64) sim.World { return sim.World{Seed: s} }},
	{"real regression: 20% of queries 1.6x", func(s uint64) sim.World {
		return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
	}},
	{"real regression: rolling over 6 ticks", func(s uint64) sim.World {
		return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6, RampTicks: 6}}
	}},
	{"small real regression: 1.35x", func(s uint64) sim.World {
		return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.35}}
	}},
	{"below the margin: 1.1x", func(s uint64) sim.World {
		return sim.World{Seed: s, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.1}}
	}},
	{"load shock, no deploy effect", func(s uint64) sim.World { return sim.World{Seed: s, LoadShock: 0.7} }},
	{"stats refresh on one table (not reported)", func(s uint64) sim.World {
		return sim.World{Seed: s, StatsTable: 3, StatsFactor: 1.8}
	}},
	{"stats refresh on one table (reported)", func(s uint64) sim.World {
		return sim.World{Seed: s, StatsTable: 3, StatsFactor: 1.8, StatsRecorded: true}
	}},
	{"cold cache after deploy (decays in 10 ticks)", func(s uint64) sim.World {
		return sim.World{Seed: s, Warmup: 1.8, WarmupTicks: 10}
	}},
	{"10% of queries rewritten", func(s uint64) sim.World { return sim.World{Seed: s, Rewrite: 0.1, RewriteFactor: 1} }},
	{"concurrent deploy at +8 min hits other queries", func(s uint64) sim.World {
		return sim.World{Seed: s, ConcurrentAt: 8, Concurrent: sim.Effect{Fraction: 0.2, Offset: 40, Factor: 1.7}}
	}},
	{"deploy slows every query 1.5x, no control arm", func(s uint64) sim.World { return sim.World{Seed: s, Global: 1.5} }},
	{"deploy slows every query 1.5x, canary control", func(s uint64) sim.World { return sim.World{Seed: s, Global: 1.5, Canary: true} }},
	{"canary: real regression", func(s uint64) sim.World {
		return sim.World{Seed: s, Canary: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
	}},
	{"canary: load shock only", func(s uint64) sim.World { return sim.World{Seed: s, Canary: true, LoadShock: 0.7} }},
	{"first deploy: no history", func(s uint64) sim.World {
		return sim.World{Seed: s, FirstDeploy: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}
	}},
}

func TestScenarioReport(t *testing.T) {
	skipLong(t)
	for _, sc := range scenarios {
		outs := runSeeds(sc.build, 20, attribution.Config{})
		t.Logf("%-52s %s", sc.name, score(outs, realRegression))
	}
}
