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
	"time"
)

// Obs is one scrape interval of one unit: the mean latency of the calls made in
// that interval and how many there were. Calls is the weight of the observation
// (a mean of n calls has variance proportional to 1/n); zero means unknown, as
// for a source that only reports a mean, and is treated as a typical weight.
type Obs struct {
	At     time.Time
	MeanMs float64
	Calls  int64
}

// Unit is one thing whose latency is tracked: a query, identified by a stable
// fingerprint rather than by pg_stat_statements' queryid.
type Unit struct {
	Key    string // stable identity (the fingerprint)
	Family string // coarse structural key: statement kind and the tables it touches
	Text   string
	Tables []string
	Obs    []Obs
	// Control is the same unit observed on the arm the deploy did not reach
	// (the stable replicas during a canary), when the source can tell the arms
	// apart. It makes a concurrent control possible, grade A.
	Control []Obs
	// Touched is true when the deploy declares it changes this unit (a migration
	// on one of its tables, a rewritten query). A touched unit is never used as
	// a donor.
	Touched  bool
	QueryIDs []int64
}

// EventKind classifies something that can move latency besides a deploy.
type EventKind string

const (
	EventDeploy   EventKind = "deploy"
	EventAnalyze  EventKind = "analyze"
	EventVacuum   EventKind = "vacuum"
	EventFailover EventKind = "failover"
	EventConfig   EventKind = "config"
	EventOther    EventKind = "other"
)

// Event is a dated occurrence that could explain a latency shift. Table scopes
// it to units reading that table; empty means it affects everything.
type Event struct {
	ID    string
	Kind  EventKind
	At    time.Time
	Table string
}

// Deploy is the intervention being judged.
type Deploy struct {
	ID string
	At time.Time
	// Reversible is false when the deploy carries something rolling back cannot
	// undo (a destructive migration, a data backfill).
	Reversible bool
	// HasRollbackTarget is false for a first deploy, where there is nothing to
	// roll back to.
	HasRollbackTarget bool
	// Tables are the relations the deploy declares it changes (migrations,
	// rewritten queries). Units reading them are the likely victims; the
	// attribution step weights them up, and they are never donors.
	Tables []string
}

// Grid is the common tick axis: every unit is binned onto it so that series
// can be compared tick by tick.
type Grid struct {
	T0   time.Time
	Step time.Duration
	N    int
}

// NewGrid spans [from, to] with a step equal to the median gap between the
// observations of all units, which is the scrape interval.
func NewGrid(units []Unit, from, to time.Time) Grid {
	var gaps []float64
	for _, u := range units {
		for _, list := range [][]Obs{u.Obs, u.Control} {
			for i := 1; i < len(list); i++ {
				if d := list[i].At.Sub(list[i-1].At); d > 0 {
					gaps = append(gaps, float64(d))
				}
			}
		}
	}
	step := time.Minute
	if m := medianOf(gaps); !isNaN(m) && m >= float64(time.Second) {
		step = time.Duration(m)
	}
	n := int(to.Sub(from)/step) + 1
	if n < 1 {
		n = 1
	}
	return Grid{T0: from, Step: step, N: n}
}

// Tick maps a time to its tick, rounding to the nearest. It can be outside
// [0, N).
func (g Grid) Tick(t time.Time) int {
	return int(math.Round(float64(t.Sub(g.T0)) / float64(g.Step)))
}

// TickAtOrAfter is the first tick whose time is not before t.
func (g Grid) TickAtOrAfter(t time.Time) int {
	return int(math.Ceil(float64(t.Sub(g.T0)) / float64(g.Step)))
}

// Series is a unit on the grid: ln of the interval mean latency and the call
// count behind it, NaN and 0 where the unit did not run.
type Series struct {
	Y, W []float64
}

// Series bins obs onto the grid. Observations that land on the same tick are
// combined as a call-weighted mean. Observations with no calls or a
// non-positive mean carry no latency information and are dropped.
func (g Grid) Series(obs []Obs) Series {
	s := Series{Y: make([]float64, g.N), W: make([]float64, g.N)}
	sumMs := make([]float64, g.N)
	for i := range s.Y {
		s.Y[i] = nan
	}
	for _, o := range obs {
		t := g.Tick(o.At)
		if t < 0 || t >= g.N || o.MeanMs <= 0 || isNaN(o.MeanMs) || math.IsInf(o.MeanMs, 0) {
			continue
		}
		c := float64(o.Calls)
		if o.Calls <= 0 {
			c = 1 // unknown weight: a single typical observation
		}
		sumMs[t] += o.MeanMs * c
		s.W[t] += c
	}
	for t := range s.Y {
		if s.W[t] > 0 {
			s.Y[t] = math.Log(sumMs[t] / s.W[t])
		}
	}
	return s
}

// observed counts non-NaN ticks in [lo, hi).
func (s Series) observed(lo, hi int) int {
	if lo < 0 {
		lo = 0
	}
	if hi > len(s.Y) {
		hi = len(s.Y)
	}
	n := 0
	for t := lo; t < hi; t++ {
		if !isNaN(s.Y[t]) {
			n++
		}
	}
	return n
}

// totalTimeMs is the time spent in the unit over [lo, hi): Σ calls × mean.
func (s Series) totalTimeMs(lo, hi int) float64 {
	if lo < 0 {
		lo = 0
	}
	if hi > len(s.Y) {
		hi = len(s.Y)
	}
	var sum float64
	for t := lo; t < hi; t++ {
		if !isNaN(s.Y[t]) {
			sum += s.W[t] * math.Exp(s.Y[t])
		}
	}
	return sum
}

// sortedUnits returns the indexes of units ordered by key, for deterministic
// iteration.
func sortedUnits(units []Unit) []int {
	idx := make([]int, len(units))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return units[idx[a]].Key < units[idx[b]].Key })
	return idx
}
