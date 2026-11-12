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
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Grade is the quality of the counterfactual behind a verdict. The action
// policy reads it; nothing else in the engine does.
type Grade string

const (
	// GradeA: a concurrent control arm of the same unit, with parallel pre-period
	// trends. The strongest design: the control feels everything the treatment
	// feels except the deploy.
	GradeA Grade = "A"
	// GradeB: a synthetic control built from units the deploy did not touch,
	// validated by in-time placebos. Anything the donors share with the unit
	// (load, statistics, cache state) is removed.
	GradeB Grade = "B"
	// GradeC: the unit's own history only. It cannot tell the deploy from
	// anything else that happened at the same time.
	GradeC Grade = "C"
	// GradeD: not enough data to say anything.
	GradeD Grade = "D"
)

// better reports whether g is a stronger grade than o.
func (g Grade) better(o Grade) bool { return g < o }

// Kind is a unit's verdict.
type Kind string

const (
	KindRegressed    Kind = "Regressed"
	KindImproved     Kind = "Improved"
	KindHealthy      Kind = "Healthy"      // evidence that any slowdown is below the margin
	KindInconclusive Kind = "Inconclusive" // neither confirmed nor excluded
	KindUntestable   Kind = "Untestable"   // no usable counterfactual
)

// Flag explains why a verdict has the grade it has.
type Flag string

const (
	FlagBirth             Flag = "new-unit"             // no history: the query is new, or its text changed
	FlagLowData           Flag = "low-data"             // too few post-deploy observations so far
	FlagGlobalShift       Flag = "global-shift"         // most untouched units moved too: load or the deploy itself, undecidable without a control arm
	FlagFewDonors         Flag = "few-donors"           // not enough untouched units to build a synthetic control
	FlagPlaceboFailed     Flag = "placebo-failed"       // the counterfactual produced false effects at fake deploy times
	FlagSpacePlacebo      Flag = "space-placebo-failed" // the effect is not unusual among untouched units (Abadie et al. permutation test)
	FlagControlUnreliable Flag = "control-unreliable"   // the control arm did not track the treatment before the deploy
	FlagConfounded        Flag = "confounded"           // another event could explain the shift
	FlagWarmingUp         Flag = "warming-up"           // still inside the grace period after the deploy
	FlagTableLevel        Flag = "table-level"          // most queries on one table moved together, which points at the table, not the code
	FlagTransient         Flag = "transient"            // the shift was there but faded after the grace period (a cold cache, a rollout in progress)
	FlagPoorFit           Flag = "poor-fit"             // model left structure in the pre-period residuals
	FlagExtrapolating     Flag = "extrapolating"        // covariates moved well outside what the pre-period showed
)

// Config tunes the engine. The zero value is usable.
type Config struct {
	// PreWindow and PostWindow bound the data used on each side of the deploy.
	// Zero means 90 and 30 minutes.
	PreWindow, PostWindow time.Duration
	// LatencyChangeThreshold is the practical-significance margin: a slowdown
	// smaller than this fraction is not a regression however certain it is. It is
	// the same knob as correlation.Config.LatencyChangeThreshold. Zero means 0.20.
	LatencyChangeThreshold float64
	// Tau is the scale of the mixing prior on the log scale. Zero means 0.25.
	Tau float64
	// Alpha is the level of the confidence sequence and of the always-valid
	// p-value reported per unit. Zero means 0.05.
	Alpha float64
	// FDR is the e-BH level across units. Zero means 0.05.
	FDR float64
	// MinPre is the number of pre-deploy observations a unit needs; MinPost the
	// number of post-deploy ones. Zero means 12 and 3.
	MinPre, MinPost int
	// MinDonors is the smallest synthetic-control pool. Zero means 5.
	MinDonors int
	// MaxDonors caps the pool handed to the regression. Zero means 60.
	MaxDonors int
	// PlaceboAlpha is the level at which a fake deploy time must not produce an
	// effect for the counterfactual to be trusted. Zero means 0.01.
	PlaceboAlpha float64
	// Placebos is the number of in-time placebos tried. Zero means 4.
	Placebos int
	// GlobalShiftFraction is the share of untouched units that must have moved
	// before the shift counts as global. Zero means 0.4.
	GlobalShiftFraction float64
	// EventSlack is how far from the deploy another event counts as a
	// confounder. Zero means PostWindow.
	EventSlack time.Duration
	// Grace is how long after the deploy the shift is allowed to be transient: a
	// rolling rollout, a cold cache and draining connections move latency for a
	// while without being the steady state. A regression is only confirmed if it
	// is still there in the data after Grace (see PersistAlpha). Zero means
	// PostWindow/3.
	Grace time.Duration
	// PersistAlpha is the level of the persistence check: after Grace, the shift
	// must still exceed half the margin with e-value at least 1/PersistAlpha.
	// Zero means 0.1.
	PersistAlpha float64
}

func (c *Config) defaults() {
	if c.PreWindow == 0 {
		c.PreWindow = 90 * time.Minute
	}
	if c.PostWindow == 0 {
		c.PostWindow = 30 * time.Minute
	}
	if c.LatencyChangeThreshold == 0 {
		c.LatencyChangeThreshold = 0.20
	}
	if c.Tau == 0 {
		c.Tau = 0.25
	}
	if c.Alpha == 0 {
		c.Alpha = 0.05
	}
	if c.FDR == 0 {
		c.FDR = 0.05
	}
	if c.MinPre == 0 {
		c.MinPre = 12
	}
	if c.MinPost == 0 {
		c.MinPost = 3
	}
	if c.MinDonors == 0 {
		c.MinDonors = 5
	}
	if c.MaxDonors == 0 {
		c.MaxDonors = 60
	}
	if c.PlaceboAlpha == 0 {
		c.PlaceboAlpha = 0.01
	}
	if c.Placebos == 0 {
		c.Placebos = 4
	}
	if c.GlobalShiftFraction == 0 {
		c.GlobalShiftFraction = 0.4
	}
	if c.EventSlack == 0 {
		c.EventSlack = c.PostWindow
	}
	if c.Grace == 0 {
		c.Grace = c.PostWindow / 3
	}
	if c.PersistAlpha == 0 {
		c.PersistAlpha = 0.1
	}
}

// delta is the practical margin on the log scale.
func (c Config) delta() float64 { return math.Log1p(c.LatencyChangeThreshold) }

// UnitVerdict is the engine's finding for one unit.
type UnitVerdict struct {
	Key, Family, Text string
	Kind              Kind
	Grade             Grade
	Method            string // concurrent-control, synthetic-control or history
	Flags             []Flag
	Reasons           []string

	// Effect: the ratio of post-deploy to counterfactual mean latency, with the
	// time-uniform confidence sequence at Config.Alpha.
	Ratio, RatioLow, RatioHigh float64
	// LogEReg and LogEImp are ln of the e-values against "no regression beyond
	// the margin" and "no improvement beyond the margin", now. PReg and PImp are
	// the always-valid p-values (reciprocal of the running maximum).
	LogEReg, LogEImp float64
	PReg, PImp       float64
	// Rejected is true when the e-BH step across units rejected the null of no
	// regression for this unit. Set by the deploy-level assessment.
	Rejected, RejectedImprovement bool

	PreN, PostN, Donors int
	// ImpactShare is the unit's share of the total database time before the
	// deploy.
	ImpactShare float64
	// Onset is when the effect most likely began, relative to the deploy (0 means
	// at the deploy). Meaningful for rejected units.
	Onset time.Duration
	// Attribution is the probability, among the deploys whose windows overlap,
	// that this deploy is the one behind the shift. 1 when no other deploy is
	// close. Meaningful for rejected units.
	Attribution float64

	// Persistence of the shift after the grace period. LateKnown is false while
	// there are too few ticks after it to tell.
	LateKnown                      bool
	PersistRegress, PersistImprove bool

	path []Shift // evidence over the post-deploy ticks, for re-testing and onset
	res  StepResult
}

func (v *UnitVerdict) flag(f Flag, why string) {
	for _, x := range v.Flags {
		if x == f {
			return
		}
	}
	v.Flags = append(v.Flags, f)
	if why != "" {
		v.Reasons = append(v.Reasons, why)
	}
}

// HasFlag reports whether the verdict carries f.
func (v UnitVerdict) HasFlag(f Flag) bool {
	for _, x := range v.Flags {
		if x == f {
			return true
		}
	}
	return false
}

// run is the state shared by every unit's assessment of one deploy.
type run struct {
	cfg      Config
	g        Grid
	T        int
	units    []Unit
	ser      []Series
	ctl      []*Series
	events   []Event
	deploy   Deploy
	totalPre float64
	donors   map[int]*donorPool // per target, built lazily
	placeboZ map[int]float64    // standardised effect of untreated units, shared by every space placebo
}

// newRun bins the units onto a grid and prepares what is shared.
func newRun(cfg Config, units []Unit, events []Event, d Deploy) *run {
	cfg.defaults()
	from := d.At.Add(-cfg.PreWindow)
	to := d.At.Add(cfg.PostWindow)
	g := NewGrid(units, from, to)
	r := &run{cfg: cfg, g: g, units: units, events: events, deploy: d, donors: map[int]*donorPool{}, placeboZ: map[int]float64{}}
	r.T = g.TickAtOrAfter(d.At)
	r.ser = make([]Series, len(units))
	r.ctl = make([]*Series, len(units))
	for i, u := range units {
		r.ser[i] = g.Series(u.Obs)
		r.totalPre += r.ser[i].totalTimeMs(0, r.T)
		if len(u.Control) > 0 {
			s := g.Series(u.Control)
			r.ctl[i] = &s
		}
	}
	return r
}

// graceTicks is the grace period in ticks.
func (r *run) graceTicks() int {
	return int(math.Ceil(float64(r.cfg.Grace) / float64(r.g.Step)))
}

func (r *run) stepOpts() StepOptions {
	return StepOptions{Tau: r.cfg.Tau, Alpha: r.cfg.Alpha, MinPre: r.cfg.MinPre, Grace: r.graceTicks()}
}

// donorPool is the set of covariates for one target.
type donorPool struct {
	cols     [][]float64
	keys     []string
	global   bool
	eligible int
	shifted  int
}

// forwardFilled returns the log series with gaps filled from the last observed
// value (and the first before it), centred on the pre-deploy median so that the
// level of the donor does not matter. Covariates need to be complete.
func (r *run) forwardFilled(s Series) []float64 {
	out := make([]float64, len(s.Y))
	med := medianOf(s.Y[:r.T])
	if isNaN(med) {
		return nil
	}
	last := nan
	for t := range s.Y {
		if !isNaN(s.Y[t]) {
			last = s.Y[t] - med
		}
		out[t] = last
	}
	// leading gap: the first observed value
	first := nan
	for t := range out {
		if !isNaN(out[t]) {
			first = out[t]
			break
		}
	}
	for t := range out {
		if isNaN(out[t]) {
			out[t] = first
		} else {
			break
		}
	}
	return out
}

// shiftZ is a robust z-score of the post-deploy median against the pre-deploy
// distribution, and the shift in logs. It is only used to screen donors, never
// to judge a unit.
func (r *run) shiftZ(s Series) (z, shift float64, ok bool) {
	var pre, post []float64
	for t, v := range s.Y {
		if isNaN(v) {
			continue
		}
		if t < r.T {
			pre = append(pre, v)
		} else {
			post = append(post, v)
		}
	}
	if len(pre) < 8 || len(post) < 3 {
		return 0, 0, false
	}
	sd := madSigma(pre)
	if isNaN(sd) || sd < 1e-6 {
		sd = 1e-6
	}
	shift = medianOf(post) - medianOf(pre)
	z = shift / (sd * math.Sqrt(1/float64(len(pre))+1/float64(len(post))))
	return z, shift, true
}

// donorPoolFor builds the covariates for target i from the units the deploy did
// not touch.
func (r *run) donorPoolFor(i int) *donorPool {
	if p, ok := r.donors[i]; ok {
		return p
	}
	type cand struct {
		idx     int
		cover   int
		shifted bool
		up      bool
		down    bool
	}
	var cands []cand
	for j := range r.units {
		if j == i || r.units[j].Touched {
			continue
		}
		s := r.ser[j]
		pre := s.observed(0, r.T)
		if pre < r.cfg.MinPre || float64(pre) < 0.5*float64(r.T) || s.observed(r.T, r.g.N) < 1 {
			continue
		}
		c := cand{idx: j, cover: pre}
		if z, shift, ok := r.shiftZ(s); ok && math.Abs(z) >= 4 && math.Abs(shift) >= 0.08 {
			c.shifted = true
			c.up = shift > 0
			c.down = shift < 0
		}
		cands = append(cands, c)
	}
	p := &donorPool{eligible: len(cands)}
	var up, down int
	for _, c := range cands {
		if c.shifted {
			p.shifted++
		}
		if c.up {
			up++
		}
		if c.down {
			down++
		}
	}
	if p.eligible >= 5 {
		f := float64(max(up, down)) / float64(p.eligible)
		if f >= r.cfg.GlobalShiftFraction && max(up, down) >= 3 {
			p.global = true
		}
	}
	// strongest coverage first, deterministic
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].cover != cands[b].cover {
			return cands[a].cover > cands[b].cover
		}
		return r.units[cands[a].idx].Key < r.units[cands[b].idx].Key
	})
	for _, c := range cands {
		if c.shifted && !p.global {
			continue // plausibly affected by the deploy: not a clean control
		}
		col := r.forwardFilled(r.ser[c.idx])
		if col == nil {
			continue
		}
		p.cols = append(p.cols, col)
		p.keys = append(p.keys, r.units[c.idx].Key)
		if len(p.cols) >= r.cfg.MaxDonors {
			break
		}
	}
	// a market index over the pool: robust when the pool is small or noisy
	if len(p.cols) >= 3 {
		idx := make([]float64, r.g.N)
		for t := range idx {
			var sum float64
			for _, c := range p.cols {
				sum += c[t]
			}
			idx[t] = sum / float64(len(p.cols))
		}
		p.cols = append(p.cols, idx)
	}
	r.donors[i] = p
	return p
}

// inTimePlacebo fits the same counterfactual at fake deploy times inside the
// pre-deploy period, truncating the series so that the real deploy is never
// seen, and reports whether any fake deploy produced an effect beyond the
// margin at PlaceboAlpha, in either direction. A counterfactual that invents
// effects where there are none cannot be trusted where there might be one.
func (r *run) inTimePlacebo(y, w []float64, cov [][]float64, opt StepOptions) (failed bool, ran int) {
	L := r.cfg.PostWindow / r.g.Step
	horizon := int(L)
	if horizon < r.graceTicks()+4 {
		horizon = r.graceTicks() + 4
	}
	if horizon > 20 {
		horizon = 20
	}
	delta := r.cfg.delta()
	for k := 1; k <= r.cfg.Placebos; k++ {
		Tp := r.T - k*horizon
		if Tp < r.cfg.MinPre {
			break
		}
		end := Tp + horizon
		if end > r.T {
			end = r.T
		}
		var sub [][]float64
		for _, c := range cov {
			sub = append(sub, c[:end])
		}
		res := StepTest(y[:end], w[:end], sub, Tp, opt)
		if !res.Ok {
			continue
		}
		ran++
		var up, down EProcess
		for _, sh := range res.Path {
			up.Update(sh, delta, r.cfg.Tau)
			down.Update(Shift{Score: -sh.Score, Info: sh.Info}, delta, r.cfg.Tau)
		}
		if up.PValue() <= r.cfg.PlaceboAlpha || down.PValue() <= r.cfg.PlaceboAlpha {
			return true, ran
		}
	}
	return false, ran
}

// controlSeries is the log-ratio of treatment to control on the ticks where both
// were observed, with the harmonic combination of their weights.
func controlSeries(t, c Series) Series {
	out := Series{Y: make([]float64, len(t.Y)), W: make([]float64, len(t.Y))}
	for i := range out.Y {
		out.Y[i] = nan
		if !isNaN(t.Y[i]) && !isNaN(c.Y[i]) && t.W[i] > 0 && c.W[i] > 0 {
			out.Y[i] = t.Y[i] - c.Y[i]
			out.W[i] = 1 / (1/t.W[i] + 1/c.W[i])
		}
	}
	return out
}

// confounders returns the events that could explain a shift of unit i.
func (r *run) confounders(i int) []Event {
	var out []Event
	lo := r.deploy.At.Add(-r.cfg.EventSlack)
	hi := r.deploy.At.Add(r.cfg.PostWindow)
	for _, e := range r.events {
		if e.ID == r.deploy.ID && e.Kind == EventDeploy {
			continue
		}
		if e.At.Before(lo) || e.At.After(hi) {
			continue
		}
		if e.Table != "" {
			found := false
			for _, t := range r.units[i].Tables {
				if strings.EqualFold(t, e.Table) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// assessUnit produces the verdict for unit i, before the cross-unit step.
func (r *run) assessUnit(i int) UnitVerdict {
	u := r.units[i]
	v := UnitVerdict{
		Key: u.Key, Family: u.Family, Text: u.Text,
		Kind: KindUntestable, Grade: GradeD,
		Ratio: nan, RatioLow: nan, RatioHigh: nan,
		PReg: 1, PImp: 1, Attribution: 1,
	}
	s := r.ser[i]
	v.PreN = s.observed(0, r.T)
	v.PostN = s.observed(r.T, r.g.N)
	if r.totalPre > 0 {
		v.ImpactShare = s.totalTimeMs(0, r.T) / r.totalPre
	}
	if v.PreN < r.cfg.MinPre {
		v.flag(FlagBirth, fmt.Sprintf("only %d observations before the deploy (need %d): a new query, or one whose text changed, has no baseline to be judged against", v.PreN, r.cfg.MinPre))
		return v
	}
	if v.PostN < r.cfg.MinPost {
		v.flag(FlagLowData, fmt.Sprintf("%d observations after the deploy so far (need %d)", v.PostN, r.cfg.MinPost))
		return v
	}

	opt := r.stepOpts()
	var res StepResult
	set := false

	// A: concurrent control arm
	if c := r.ctl[i]; c != nil {
		d := controlSeries(s, *c)
		if d.observed(0, r.T) >= r.cfg.MinPre && d.observed(r.T, r.g.N) >= r.cfg.MinPost {
			rr := StepTest(d.Y, d.W, nil, r.T, opt)
			if rr.Ok {
				if failed, ran := r.inTimePlacebo(d.Y, d.W, nil, opt); !failed && ran > 0 {
					res, set = rr, true
					v.Grade, v.Method = GradeA, "concurrent-control"
				} else if failed {
					v.flag(FlagControlUnreliable, "the control arm did not track the treatment before the deploy (a fake deploy time produced an effect)")
				}
			}
		}
	}

	// B: synthetic control from untouched units
	if !set {
		pool := r.donorPoolFor(i)
		v.Donors = len(pool.keys)
		switch {
		case pool.global:
			v.flag(FlagGlobalShift, fmt.Sprintf("%d of %d untouched units shifted in the same direction: the cause is shared (load, a platform change or the deploy itself) and without a control arm it cannot be told apart", max(pool.shifted, 0), pool.eligible))
		case len(pool.keys) < r.cfg.MinDonors:
			v.flag(FlagFewDonors, fmt.Sprintf("%d untouched units to build a control from (need %d)", len(pool.keys), r.cfg.MinDonors))
		default:
			rr := StepTest(s.Y, s.W, pool.cols, r.T, opt)
			if rr.Ok {
				if failed, ran := r.inTimePlacebo(s.Y, s.W, pool.cols, opt); !failed && ran > 0 {
					res, set = rr, true
					v.Grade, v.Method = GradeB, "synthetic-control"
				} else if failed {
					v.flag(FlagPlaceboFailed, "the synthetic control produced effects at fake deploy times, so its counterfactual is not trusted")
				}
			}
		}
	}

	// C: the unit's own history
	if !set {
		rr := StepTest(s.Y, s.W, nil, r.T, opt)
		if !rr.Ok {
			v.flag(FlagLowData, rr.Reason)
			return v
		}
		res, set = rr, true
		v.Grade, v.Method = GradeC, "history"
	}

	if res.Rho1 > 0.5 {
		v.flag(FlagPoorFit, fmt.Sprintf("the model left serial correlation %.2f in the pre-deploy residuals", res.Rho1))
	}
	if ev := r.confounders(i); len(ev) > 0 && v.Grade.better(GradeC) {
		v.Grade = GradeC
		v.flag(FlagConfounded, fmt.Sprintf("%s at %s could also explain a shift", ev[0].Kind, ev[0].At.Format(time.RFC3339)))
	} else if len(ev) > 0 {
		v.flag(FlagConfounded, fmt.Sprintf("%s at %s could also explain a shift", ev[0].Kind, ev[0].At.Format(time.RFC3339)))
	}

	delta := r.cfg.delta()
	v.res = res
	v.path = res.Path
	theta, se := res.Shift.Estimate()
	rad := res.Shift.ConfidenceRadius(r.cfg.Tau, r.cfg.Alpha)
	if !isNaN(theta) {
		v.Ratio = math.Exp(theta)
		if !isNaN(rad) {
			v.RatioLow, v.RatioHigh = math.Exp(theta-rad), math.Exp(theta+rad)
		}
	}
	_ = se
	v.LogEReg = res.Shift.LogE(delta, r.cfg.Tau)
	v.LogEImp = Shift{Score: -res.Shift.Score, Info: res.Shift.Info}.LogE(delta, r.cfg.Tau)
	var up, down EProcess
	for _, sh := range res.Path {
		up.Update(sh, delta, r.cfg.Tau)
		down.Update(Shift{Score: -sh.Score, Info: sh.Info}, delta, r.cfg.Tau)
	}
	v.PReg, v.PImp = up.PValue(), down.PValue()

	// Persistence: once the grace period is over, is the shift still there?
	v.LateKnown = res.LatePostN >= r.cfg.MinPost
	if v.LateKnown {
		need := math.Log(1 / r.cfg.PersistAlpha)
		v.PersistRegress = res.Late.LogE(delta/2, r.cfg.Tau) >= need
		v.PersistImprove = Shift{Score: -res.Late.Score, Info: res.Late.Info}.LogE(delta/2, r.cfg.Tau) >= need
	}

	// default before the cross-unit step: confirmed absence of a regression
	// needs the whole confidence sequence under the margin
	switch {
	case !isNaN(rad) && theta+rad <= delta:
		v.Kind = KindHealthy
	default:
		v.Kind = KindInconclusive
	}
	return v
}
