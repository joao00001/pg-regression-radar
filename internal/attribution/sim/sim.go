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

// Package sim generates synthetic databases for testing the attribution engine:
// a workload of queries with shared load, level drift and call-count noise, a
// deploy, and the confounders that make attribution hard (a load shock, a
// statistics refresh on one table, a warm-up transient, a rewritten query, a
// concurrent deploy, a deploy that slows everything, a canary control arm).
//
// Every scenario carries its ground truth, so the engine can be scored once, on
// precision and recall of the verdicts, instead of being tested one false-
// positive class at a time. The same generator drives the calibration runs
// (A/A: no deploy effect at all; placebo: a fake deploy time) that check the
// stated error rates hold.
package sim

import (
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
)

// Effect is a multiplicative change in latency applied to a set of queries from
// a moment on.
type Effect struct {
	// Fraction of the queries affected, taken from the start of the list
	// (Offset skips that many first).
	Fraction float64
	Offset   int
	// Factor is the latency multiplier (1.5 = 50% slower). Zero means no effect.
	Factor float64
	// AfterMin is minutes after the deploy at which it takes hold.
	AfterMin float64
	// RampTicks spreads the change over this many ticks (rolling deploy).
	RampTicks int
}

// World describes one synthetic database and deploy.
type World struct {
	Seed    uint64
	Queries int // default 120
	Tables  int // default 12
	Step    time.Duration
	Pre     time.Duration
	Post    time.Duration

	// Workload noise.
	LoadSD    float64 // sd of the shared log-load process, default 0.15
	LoadAR    float64 // its AR(1) coefficient, default 0.9
	DriftSD   float64 // per-query random-walk sd of the log level, default 0.004
	CV        float64 // coefficient of variation of a single call, default 1
	RateScale float64 // typical calls per tick for the median query, default 150
	Missing   float64 // chance a query is absent on a tick, default 0.05

	// The deploy.
	Deploy Effect

	// Confounders.
	LoadShock      float64 // step in the shared log-load at the deploy time
	LoadShockAfter float64 // minutes after the deploy; default 0
	StatsTable     int     // table whose queries change plan at the deploy time
	StatsFactor    float64 // latency multiplier from that, 0 = none
	StatsRecorded  bool    // whether the ANALYZE is known to the engine as an event
	Warmup         float64 // extra latency multiplier decaying away right after the deploy
	WarmupTicks    int
	Rewrite        float64 // fraction of queries whose text changes at the deploy
	RewriteFactor  float64 // latency multiplier of the rewritten text; 0 = same
	Concurrent     Effect  // a second deploy
	ConcurrentAt   float64 // minutes after the first deploy; 0 = none
	Global         float64 // latency multiplier on every query caused by the deploy
	Canary         bool    // observe a control arm (canary against stable)
	FirstDeploy    bool    // no history: only a few ticks before the deploy
	Irreversible   bool
	Tables0        []string // tables the deploy declares touching
}

// Truth is what really happened.
type Truth struct {
	// Regressed marks units whose latency the deploy really changed by more than
	// the margin (Factor ≥ 1.2).
	Regressed map[string]bool
	// Confounded marks units shifted by something other than the deploy.
	Confounded map[string]bool
}

func (w *World) defaults() {
	if w.Queries == 0 {
		w.Queries = 120
	}
	if w.Tables == 0 {
		w.Tables = 12
	}
	if w.Step == 0 {
		w.Step = time.Minute
	}
	if w.Pre == 0 {
		w.Pre = 90 * time.Minute
	}
	if w.Post == 0 {
		w.Post = 30 * time.Minute
	}
	if w.LoadSD == 0 {
		w.LoadSD = 0.15
	}
	if w.LoadAR == 0 {
		w.LoadAR = 0.9
	}
	if w.DriftSD == 0 {
		w.DriftSD = 0.004
	}
	if w.CV == 0 {
		w.CV = 1
	}
	if w.RateScale == 0 {
		w.RateScale = 150
	}
	if w.Missing == 0 {
		w.Missing = 0.05
	}
}

// DeployTime is a fixed instant so that scenarios are reproducible.
var DeployTime = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

type query struct {
	key       string
	table     int
	text      string
	baseMs    float64
	rate      float64
	elastic   float64 // sensitivity of latency to shared load
	affected  bool
	conc      bool
	stats     bool
	rewritten bool
}

// Generate draws the workload and returns the units, events, the deploy and the
// ground truth.
func (w World) Generate() ([]attribution.Unit, []attribution.Event, attribution.Deploy, Truth) {
	w.defaults()
	r := rand.New(rand.NewPCG(w.Seed, w.Seed*0x9e3779b97f4a7c15+1)) // #nosec G404 -- deterministic simulator seed, not security-relevant
	nPre := int(w.Pre / w.Step)
	nPost := int(w.Post / w.Step)
	n := nPre + nPost
	start := DeployTime.Add(-w.Pre)

	qs := make([]query, w.Queries)
	for i := range qs {
		t := i % w.Tables
		qs[i] = query{
			key:     fmt.Sprintf("q%03d", i),
			table:   t,
			text:    fmt.Sprintf("select * from t%d where c%d = ?", t, i),
			baseMs:  math.Exp(0.5 + 1.8*r.Float64()*2), // ~1.6 ms to ~600 ms
			rate:    w.RateScale * math.Exp(1.2*r.NormFloat64()),
			elastic: 0.2 + 0.8*r.Float64(),
		}
		if qs[i].rate < 4 {
			qs[i].rate = 4
		}
	}
	// A rotation of which queries are affected so that scenarios do not always hit
	// the same ones: Offset and Fraction select a contiguous block.
	mark := func(e Effect, set func(*query)) {
		if e.Factor == 0 {
			return
		}
		k := int(math.Round(e.Fraction * float64(len(qs))))
		for i := e.Offset; i < e.Offset+k && i < len(qs); i++ {
			set(&qs[i])
		}
	}
	mark(w.Deploy, func(q *query) { q.affected = true })
	mark(w.Concurrent, func(q *query) { q.conc = true })
	if w.StatsFactor > 0 {
		for i := range qs {
			if qs[i].table == w.StatsTable {
				qs[i].stats = true
			}
		}
	}
	if w.Rewrite > 0 {
		k := int(math.Round(w.Rewrite * float64(len(qs))))
		for i := len(qs) - 1; i >= len(qs)-k && i >= 0; i-- {
			qs[i].rewritten = true
		}
	}

	// Shared load, log scale.
	load := make([]float64, n)
	for t := 1; t < n; t++ {
		load[t] = w.LoadAR*load[t-1] + w.LoadSD*math.Sqrt(1-w.LoadAR*w.LoadAR)*r.NormFloat64()
	}
	shockTick := nPre + int(w.LoadShockAfter*float64(time.Minute)/float64(w.Step))
	for t := shockTick; t < n && w.LoadShock != 0; t++ {
		load[t] += w.LoadShock
	}

	ramp := func(e Effect, t int) float64 { // 0..1 progress of an effect at tick t
		at := nPre + int(e.AfterMin*float64(time.Minute)/float64(w.Step))
		if t < at {
			return 0
		}
		if e.RampTicks <= 1 {
			return 1
		}
		return math.Min(1, float64(t-at+1)/float64(e.RampTicks))
	}

	truth := Truth{Regressed: map[string]bool{}, Confounded: map[string]bool{}}
	obs := make([][]attribution.Obs, len(qs))
	ctl := make([][]attribution.Obs, len(qs))
	oldObs := make([][]attribution.Obs, len(qs)) // rewritten queries: pre-deploy under the old text
	level := make([]float64, len(qs))

	first := 0
	if w.FirstDeploy {
		first = nPre - 4
	}

	for t := first; t < n; t++ {
		at := start.Add(time.Duration(t) * w.Step)
		for i := range qs {
			q := &qs[i]
			level[i] += w.DriftSD * r.NormFloat64()
			sample := func(effects float64, lvl float64, share float64) (float64, int64, bool) {
				if r.Float64() < w.Missing {
					return 0, 0, false
				}
				calls := q.rate * share * math.Exp(load[t])
				c := poisson(r, calls)
				if c < 1 {
					return 0, 0, false
				}
				logMean := math.Log(q.baseMs) + q.elastic*load[t] + lvl + effects +
					w.CV/math.Sqrt(float64(c))*r.NormFloat64()
				return math.Exp(logMean), int64(c), true
			}

			// What the deploy and the confounders do to this query at this tick.
			var common float64 // felt by treatment and control alike
			if q.stats && t >= nPre {
				common += math.Log(w.StatsFactor)
			}
			if w.Warmup > 0 && t >= nPre && t < nPre+w.WarmupTicks {
				common += math.Log(w.Warmup) * (1 - float64(t-nPre)/float64(w.WarmupTicks))
			}
			if q.conc && w.Concurrent.Factor > 0 {
				common += math.Log(w.Concurrent.Factor) * ramp(Effect{AfterMin: w.ConcurrentAt, RampTicks: w.Concurrent.RampTicks}, t)
			}
			var treat float64 // only the arm that received the deploy
			if q.affected && w.Deploy.Factor > 0 {
				treat += math.Log(w.Deploy.Factor) * ramp(w.Deploy, t)
			}
			if w.Global > 0 && t >= nPre {
				treat += math.Log(w.Global)
			}
			share := 1.0
			if w.Canary {
				share = 0.5 // traffic splits between the arms
			}
			if q.rewritten && t >= nPre {
				if w.RewriteFactor > 0 {
					treat += math.Log(w.RewriteFactor)
				}
			}

			if m, c, ok := sample(common+treat, level[i], share); ok {
				o := attribution.Obs{At: at, MeanMs: m, Calls: c}
				if q.rewritten && t >= nPre {
					obs[i] = append(obs[i], o)
				} else if q.rewritten {
					oldObs[i] = append(oldObs[i], o)
				} else {
					obs[i] = append(obs[i], o)
				}
			}
			if w.Canary {
				if m, c, ok := sample(common, level[i], share); ok {
					ctl[i] = append(ctl[i], attribution.Obs{At: at, MeanMs: m, Calls: c})
				}
			}
		}
	}

	var units []attribution.Unit
	for i := range qs {
		q := qs[i]
		kind := fmt.Sprintf("select:t%d", q.table)
		u := attribution.Unit{Key: q.key, Family: kind, Text: q.text, Tables: []string{fmt.Sprintf("t%d", q.table)}, Obs: obs[i], Control: ctl[i]}
		if q.rewritten {
			// the old text lives on as its own unit with only pre-deploy data...
			old := u
			old.Obs = oldObs[i]
			old.Control = nil
			units = append(units, old)
			// ...and the new text is born at the deploy.
			u.Key = q.key + "x"
			u.Text = fmt.Sprintf("select * from t%d where c%d = ? and 1 = ?", q.table, i)
			u.Control = nil
		}
		for _, tb := range w.Tables0 {
			for _, ut := range u.Tables {
				if tb == ut && q.affected {
					u.Touched = true
				}
			}
		}
		units = append(units, u)
		if (q.affected && w.Deploy.Factor >= 1.2) || w.Global >= 1.2 || (q.rewritten && w.RewriteFactor >= 1.2) {
			truth.Regressed[q.key] = true
		}
		if q.stats || q.conc {
			truth.Confounded[q.key] = true
		}
	}

	var events []attribution.Event
	if w.StatsRecorded && w.StatsFactor > 0 {
		events = append(events, attribution.Event{ID: "analyze", Kind: attribution.EventAnalyze, At: DeployTime.Add(time.Minute), Table: fmt.Sprintf("t%d", w.StatsTable)})
	}
	if w.ConcurrentAt > 0 {
		events = append(events, attribution.Event{ID: "deploy-2", Kind: attribution.EventDeploy, At: DeployTime.Add(time.Duration(w.ConcurrentAt * float64(time.Minute)))})
	}
	d := attribution.Deploy{ID: "deploy-1", At: DeployTime, Reversible: !w.Irreversible, HasRollbackTarget: !w.FirstDeploy, Tables: w.Tables0}
	return units, events, d, truth
}

// poisson draws from a Poisson distribution; for large means it uses the normal
// approximation, which is what matters at the call counts of a scrape interval.
func poisson(r *rand.Rand, mean float64) int {
	if mean <= 0 {
		return 0
	}
	if mean > 40 {
		v := mean + math.Sqrt(mean)*r.NormFloat64()
		if v < 0 {
			return 0
		}
		return int(math.Round(v))
	}
	l, k, p := math.Exp(-mean), 0, 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}
