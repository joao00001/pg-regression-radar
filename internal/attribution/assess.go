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
	"strings"
	"time"
)

// Health is the engine's reading of a whole deploy.
type Health string

const (
	// HealthRegressed: at least one unit regressed beyond the margin with the
	// false-discovery rate controlled.
	HealthRegressed Health = "Regressed"
	// HealthHealthy: enough of the database's time was testable, nothing
	// regressed, and the confidence sequences exclude a slowdown beyond the
	// margin for most of it.
	HealthHealthy Health = "Healthy"
	// HealthInconclusive: nothing regressed, but the evidence does not exclude
	// it.
	HealthInconclusive Health = "Inconclusive"
	// HealthInsufficient: too little of the workload could be tested.
	HealthInsufficient Health = "InsufficientData"
)

// Impact is a lower bound on how much of the database's total time the
// regression adds, as a fraction of its pre-deploy total, counting only units
// whose evidence is at least the given grade. It uses the lower end of each
// unit's confidence sequence, so it is a figure the data supports, not a hope.
type Impact struct {
	A, AB, ABC float64
}

// Assessment is the engine's full finding for one deploy.
type Assessment struct {
	Deploy Deploy
	Config Config
	Units  []UnitVerdict

	Health Health
	// Coverage is the share of the pre-deploy database time that sits in units
	// the engine could test.
	Coverage float64
	// Impact is the supported lower bound of the added database time, by the
	// weakest grade admitted.
	Impact Impact
	// PointImpact is the same with point estimates: what the regression most
	// likely costs.
	PointImpact float64
	// Attribution is the lowest attribution probability among the rejected
	// units that carry impact: how sure the engine is that this deploy, and not a
	// concurrent one, is the cause.
	Attribution float64
	// GlobalShift is true when the shift is shared by most untouched units.
	GlobalShift bool

	Tested, Regressed, Improved, Healthy, Inconclusive, Untestable int
}

// Assess judges one deploy against the observed units.
//
// units carry their observations over the window; events are everything else
// that could move latency (other deploys, ANALYZE, failovers). The deploy
// timestamp defines the intervention tick.
func Assess(cfg Config, units []Unit, events []Event, d Deploy) Assessment {
	r := newRun(cfg, units, events, d)
	a := Assessment{Deploy: d, Config: r.cfg, Attribution: 1}
	a.Units = make([]UnitVerdict, len(units))
	for _, i := range sortedUnits(units) {
		a.Units[i] = r.assessUnit(i)
	}

	// e-BH across the testable units, separately for each direction. The family
	// is every unit that could be tested, whether or not it moved: a unit that
	// did not move is a hypothesis that was tested and not rejected.
	var testable []int
	for i, v := range a.Units {
		if v.Grade != GradeD && v.Kind != KindUntestable {
			testable = append(testable, i)
		}
	}
	a.Tested = len(testable)
	if len(testable) > 0 {
		logReg := make([]float64, len(testable))
		logImp := make([]float64, len(testable))
		for k, i := range testable {
			logReg[k], logImp[k] = a.Units[i].LogEReg, a.Units[i].LogEImp
		}
		rejReg := EBH(logReg, r.cfg.FDR, len(testable))
		rejImp := EBH(logImp, r.cfg.FDR, len(testable))
		for k, i := range testable {
			v := &a.Units[i]
			switch {
			case rejReg[k] && v.PersistRegress:
				v.Rejected, v.Kind = true, KindRegressed
			case rejReg[k]:
				v.noPersist("regression")
			case rejImp[k] && v.PersistImprove:
				v.RejectedImprovement, v.Kind = true, KindImproved
			case rejImp[k]:
				v.noPersist("improvement")
			}
		}
	}

	r.tableLevel(&a)

	// For each rejected regression: the permutation check among donors, when the
	// pool is large enough to support it, then the onset and the attribution
	// among overlapping deploys.
	for i := range a.Units {
		v := &a.Units[i]
		if !v.Rejected {
			continue
		}
		if v.Grade == GradeB {
			r.spacePlacebo(i, v)
		}
		if r.hasOverlap() {
			v.Onset = r.onset(i, v)
		}
		v.Attribution = r.attribution(i, v)
	}

	// Coverage, counts and impact.
	var covered float64
	for i := range a.Units {
		v := a.Units[i]
		if v.Grade != GradeD && v.Kind != KindUntestable {
			covered += v.ImpactShare
		}
		switch v.Kind {
		case KindRegressed:
			a.Regressed++
		case KindImproved:
			a.Improved++
		case KindHealthy:
			a.Healthy++
		case KindInconclusive:
			a.Inconclusive++
		default:
			a.Untestable++
		}
		if v.Rejected {
			low := math.Max(0, v.RatioLow-1) * v.ImpactShare
			pt := math.Max(0, v.Ratio-1) * v.ImpactShare
			a.PointImpact += pt
			if !isNaN(low) {
				if v.Grade == GradeA {
					a.Impact.A += low
				}
				if v.Grade == GradeA || v.Grade == GradeB {
					a.Impact.AB += low
				}
				a.Impact.ABC += low
			}
			if v.Attribution < a.Attribution && low > 0 {
				a.Attribution = v.Attribution
			}
			if v.HasFlag(FlagGlobalShift) {
				a.GlobalShift = true
			}
		}
	}
	a.Coverage = covered
	if r.totalPre <= 0 {
		a.Coverage = 0
	}
	switch {
	case a.Regressed > 0:
		a.Health = HealthRegressed
	case a.Coverage < 0.5 || a.Tested == 0:
		a.Health = HealthInsufficient
	default:
		var healthyShare float64
		for _, v := range a.Units {
			if v.Kind == KindHealthy || v.Kind == KindImproved {
				healthyShare += v.ImpactShare
			}
		}
		if healthyShare >= 0.5 {
			a.Health = HealthHealthy
		} else {
			a.Health = HealthInconclusive
		}
	}
	return a
}

// noPersist records that the evidence rejected the null but the shift is not
// confirmed to persist past the grace period: either it faded (a transient) or
// it is too early to tell. Either way the unit is not called.
func (v *UnitVerdict) noPersist(what string) {
	if v.LateKnown {
		v.flag(FlagTransient, "the "+what+" faded after the grace period, so it looks like a warm-up or a rollout in progress, not a steady change")
	} else {
		v.flag(FlagWarmingUp, "the "+what+" cannot be confirmed to persist yet: too few observations after the grace period")
	}
}

// tableLevel flags rejected units whose table moved as a whole. A deploy changes
// code, and code changes move particular queries; fresh statistics, bloat, a
// dropped index or a lock move every query that reads the table. When most of the
// testable queries on a table regressed together and the deploy did not declare
// that table, the table is the likelier cause, and nothing about the data can
// separate the two, so a synthetic-control verdict is held at grade C. Declaring the tables the
// deploy touches (Deploy.Tables, or Unit.Touched) lifts the cap.
func (r *run) tableLevel(a *Assessment) {
	declared := map[string]bool{}
	for _, t := range r.deploy.Tables {
		declared[strings.ToLower(t)] = true
	}
	byTable := map[string][]int{}
	for i, v := range a.Units {
		if v.Grade == GradeD || v.Kind == KindUntestable {
			continue
		}
		for _, t := range r.units[i].Tables {
			byTable[t] = append(byTable[t], i)
		}
	}
	moved := map[string]bool{}
	for t, idx := range byTable {
		if len(idx) < 4 || declared[strings.ToLower(t)] {
			continue
		}
		// count queries whose point estimate is past the margin, not only the ones
		// the test could reject: a low-volume query on the same table moves just
		// as much, it only has too much noise to prove it.
		n := 0
		for _, i := range idx {
			if v := a.Units[i]; !isNaN(v.Ratio) && v.Ratio >= 1+r.cfg.LatencyChangeThreshold {
				n++
			}
		}
		if float64(n)/float64(len(idx)) >= 0.7 {
			moved[t] = true
		}
	}
	if len(moved) == 0 {
		return
	}
	for i := range a.Units {
		v := &a.Units[i]
		// A concurrent control arm already excludes anything that hits the table
		// regardless of the deploy, so only synthetic-control verdicts are capped.
		if !v.Rejected || r.units[i].Touched || v.Grade != GradeB {
			continue
		}
		for _, t := range r.units[i].Tables {
			if moved[t] {
				v.Grade = GradeC
				v.flag(FlagTableLevel, "most queries on table "+t+" moved together and the deploy does not declare it: statistics, bloat, an index or a lock are likelier than the code")
				break
			}
		}
	}
}

// spacePlacebo is the permutation test of Abadie, Diamond and Hainmueller
// (2010): apply the same procedure, at the same deploy time, to units that were
// not treated, and ask how unusual the target's effect is among them. If the
// untouched units show effects as large, the target's effect is not evidence of
// anything the deploy did. It is a design-based check on the model-based test.
//
// With J placebos the smallest attainable p-value is 1/(J+1), so a pool under
// 19 cannot reject at 0.05 and the check is skipped rather than failed.
func (r *run) spacePlacebo(i int, v *UnitVerdict) {
	pool := r.donorPoolFor(i)
	var js []int
	keyIdx := map[string]int{}
	for j, u := range r.units {
		keyIdx[u.Key] = j
	}
	for _, k := range pool.keys {
		if j, ok := keyIdx[k]; ok && j != i {
			js = append(js, j)
		}
	}
	sort.Ints(js)
	if len(js) > 30 {
		// spread the placebos across the sorted pool instead of taking a prefix
		step := float64(len(js)) / 30
		sel := make([]int, 0, 30)
		for k := 0; k < 30; k++ {
			sel = append(sel, js[int(float64(k)*step)])
		}
		js = sel
	}
	if len(js) < 19 {
		return
	}
	zi := math.Abs(v.res.Theta / v.res.SE)
	more, ran := 0, 0
	for _, j := range js {
		z, ok := r.donorZ(j)
		if !ok {
			continue
		}
		ran++
		if z >= zi {
			more++
		}
	}
	if ran < 19 {
		return
	}
	if p := float64(1+more) / float64(1+ran); p > 0.10 {
		v.Grade = GradeC
		v.flag(FlagSpacePlacebo, "untouched units show effects as large as this one at the same deploy time, so the effect is not unusual enough to credit to the deploy")
	}
}

// donorZ is the absolute standardised effect |θ̂/SE| of an untreated unit at the
// deploy time, computed with its own synthetic control. Cached: the same donors
// serve as placebos for every rejected unit.
func (r *run) donorZ(j int) (float64, bool) {
	if z, ok := r.placeboZ[j]; ok {
		return z, !isNaN(z)
	}
	z := nan
	if dp := r.donorPoolFor(j); len(dp.keys) >= r.cfg.MinDonors {
		res := StepTest(r.ser[j].Y, r.ser[j].W, dp.cols, r.T, r.stepOpts())
		if res.Ok && !isNaN(res.Theta) && res.SE > 0 {
			z = math.Abs(res.Theta / res.SE)
		}
	}
	r.placeboZ[j] = z
	return z, !isNaN(z)
}

// hasOverlap reports whether another deploy falls inside the confounding window
// of this one, which is the only case in which the onset matters.
func (r *run) hasOverlap() bool {
	for _, e := range r.events {
		if e.Kind == EventDeploy && e.ID != r.deploy.ID &&
			!e.At.Before(r.deploy.At.Add(-r.cfg.EventSlack)) && !e.At.After(r.deploy.At.Add(r.cfg.PostWindow)) {
			return true
		}
	}
	return false
}

// onset estimates when the effect began by profiling the step likelihood over
// candidate start ticks around the deploy and taking the one with the largest
// likelihood-ratio statistic Score²/Info. It is a point estimate, used to
// weigh concurrent deploys against each other, not a confidence statement.
func (r *run) onset(i int, v *UnitVerdict) time.Duration {
	k := int(math.Ceil(float64(r.cfg.PostWindow) / float64(r.g.Step) / 3))
	if k < 2 {
		k = 2
	}
	s := r.ser[i]
	var cov [][]float64
	if v.Method == "synthetic-control" {
		cov = r.donorPoolFor(i).cols
	}
	y, w := s.Y, s.W
	if v.Method == "concurrent-control" && r.ctl[i] != nil {
		d := controlSeries(s, *r.ctl[i])
		y, w = d.Y, d.W
	}
	best, bestT := math.Inf(-1), r.T
	for t := r.T - k; t <= r.T+k; t++ {
		if t < r.cfg.MinPre || t >= len(y) {
			continue
		}
		res := StepTest(y, w, cov, t, r.stepOpts())
		if !res.Ok || res.Shift.Info <= 0 {
			continue
		}
		if stat := res.Shift.Score * res.Shift.Score / res.Shift.Info; res.Theta > 0 && stat > best {
			best, bestT = stat, t
		}
	}
	return time.Duration(bestT-r.T) * r.g.Step
}

// attribution weighs this deploy against the other deploys whose windows
// overlap, in the manner of Gandalf (Li et al., NSDI 2020): the probability that
// a shift beginning at the estimated onset belongs to a deploy falls with the
// distance between the onset and the deploy's own time (a Gaussian kernel whose
// width is the rollout's slack), and rises for deploys that declare they touch
// the unit's tables. The result is the share of the total weight that this
// deploy holds, 1 when it stands alone.
func (r *run) attribution(i int, v *UnitVerdict) float64 {
	onset := r.deploy.At.Add(v.Onset)
	width := math.Max(float64(2*r.g.Step), float64(r.cfg.PostWindow)/5)
	weight := func(at time.Time, scope bool) float64 {
		d := float64(onset.Sub(at))
		w := math.Exp(-d * d / (2 * width * width))
		if scope {
			w *= 3
		}
		return w
	}
	touches := func(tables []string) bool {
		for _, t := range tables {
			for _, ut := range r.units[i].Tables {
				if strings.EqualFold(t, ut) {
					return true
				}
			}
		}
		return false
	}
	mine := weight(r.deploy.At, r.units[i].Touched || touches(r.deploy.Tables))
	var others float64
	for _, e := range r.events {
		if e.Kind != EventDeploy || e.ID == r.deploy.ID {
			continue
		}
		if e.At.Before(r.deploy.At.Add(-r.cfg.EventSlack)) || e.At.After(r.deploy.At.Add(r.cfg.PostWindow)) {
			continue
		}
		others += weight(e.At, e.Table != "" && touches([]string{e.Table}))
	}
	if mine+others == 0 {
		return 0
	}
	return mine / (mine + others)
}
