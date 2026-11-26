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

package controller

import (
	"context"
	"math"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/observe"
)

// maxUnitsShown bounds the queries listed per evaluation: the ones that matter
// come first, so what is cut is the long tail of healthy ones.
const maxUnitsShown = 200

// LiveSource is the Observe API's view of the running manager. It reads the
// attribution evaluators of every watch and translates what the engine knows
// into the API's types; it changes nothing.
//
// The allowlist (attribution.actuate.namespaces) is applied here, per watch: a
// deploy a watch is not allowed to act on is not shown, in any endpoint, and
// looks like a deploy that does not exist.
type LiveSource struct {
	Registry *Registry
	// Started is when this manager began; the in-memory history starts then.
	Started time.Time
	// Now is the clock; time.Now if nil.
	Now func() time.Time

	// History keeps what the radar concluded beyond the evaluators' bounded
	// memory (see observe_history.go). Nil serves only what the evaluators hold.
	History observe.HistoryStore
	// Retention is reported to clients; the store applies it.
	Retention time.Duration
	// StoreQueryText keeps the normalised SQL in stored cases. Off by default: a
	// store outlives the process and may be read by more people than the screen.
	StoreQueryText bool

	rec recorder
}

var _ observe.Source = (*LiveSource)(nil)

func (s *LiveSource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// view is one watch with an evaluator.
type view struct {
	key types.NamespacedName
	rt  *WatchRuntime
	ev  *shadow.Evaluator
}

func (v view) allows(ns string) bool {
	if len(v.rt.ObserveNamespaces) == 0 {
		return true
	}
	for _, n := range v.rt.ObserveNamespaces {
		if n == ns {
			return true
		}
	}
	return false
}

func (v view) mode() string {
	if v.ev.Policy.Enforcing() {
		return "enforce"
	}
	return "shadow"
}

func (s *LiveSource) views() []view {
	var out []view
	s.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		if rt.Shadow != nil {
			out = append(out, view{k, rt, rt.Shadow})
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].key.String() < out[j].key.String() })
	return out
}

// Overview implements observe.Source.
func (s *LiveSource) Overview(ctx context.Context) (observe.Overview, error) {
	now := s.now()
	o := observe.Overview{Version: observe.Version, GeneratedAt: now}
	scope := observe.Scope{}
	seen := map[string]bool{}
	s.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		w := observe.WatchStatus{Name: k.Name, Namespace: k.Namespace, Cluster: rt.ClusterName, Mode: "off"}
		w.Collector = observe.CollectorHealth{StaleSeconds: -1}
		if rt.Collector != nil {
			if t := rt.Collector.LastScrapeTime(); !t.IsZero() {
				w.Collector.LastScrape = &t
				w.Collector.StaleSeconds = int64(now.Sub(t).Seconds())
			}
		}
		if rt.Shadow != nil {
			w.Mode = "shadow"
			if rt.Shadow.Policy.Enforcing() {
				w.Mode = "enforce"
			}
			w.BreakerOpen = rt.Shadow.BreakerOpen()
			if len(rt.ObserveNamespaces) == 0 {
				scope.All = true
			}
			for _, n := range rt.ObserveNamespaces {
				if !seen[n] {
					seen[n] = true
					scope.Namespaces = append(scope.Namespaces, n)
				}
			}
		}
		w.Actuation = observe.ActuationStatus{Enabled: rt.Actuate != nil, Namespaces: append([]string(nil), rt.ObserveNamespaces...)}
		if rt.Actuate != nil && rt.Actuate.Paused != nil {
			w.Actuation.Paused = rt.Actuate.Paused(ctx)
		}
		w.Arms = armsHealth(rt.Arms, now)
		o.Watches = append(o.Watches, w)
	})
	sort.Slice(o.Watches, func(i, j int) bool {
		return o.Watches[i].Namespace+"/"+o.Watches[i].Name < o.Watches[j].Namespace+"/"+o.Watches[j].Name
	})
	if len(o.Watches) == 0 {
		scope.All = true
	}
	sort.Strings(scope.Namespaces)
	o.Scope = scope

	o.History = s.historyInfo(ctx, now)
	return o, nil
}

func armsHealth(a *ArmsRuntime, now time.Time) *observe.ArmsHealth {
	if a == nil {
		return nil
	}
	st := a.Store.Stats()
	h := &observe.ArmsHealth{Usable: a.Usable(), Records: st.Records, Dropped: st.Dropped}
	if !st.Newest.IsZero() {
		t := st.Newest
		h.Newest = &t
	}
	switch {
	case h.Usable:
	case !a.usable.Load():
		h.Why = "The server's log settings did not pass the check; see the manager log."
	case st.Newest.IsZero():
		h.Why = "No log line has arrived yet."
	default:
		h.Why = "No log line for " + now.Sub(st.Newest).Round(time.Second).String() + "; the log is not flowing."
	}
	return h
}

// index groups a watch's evaluations and ledger by deploy.
type index struct {
	records map[string][]shadow.Record
	actions map[string][]shadow.ActionRecord
}

func (v view) index() index {
	ix := index{records: map[string][]shadow.Record{}, actions: map[string][]shadow.ActionRecord{}}
	for _, r := range v.ev.Records() {
		id := r.Assessment.Deploy.ID
		ix.records[id] = append(ix.records[id], r)
	}
	for _, a := range v.ev.Actions() {
		ix.actions[a.DeployID] = append(ix.actions[a.DeployID], a)
	}
	return ix
}

func (v view) summary(d shadow.Deploy, ix index, now time.Time) observe.DeploySummary {
	recs, acts := ix.records[d.ID], ix.actions[d.ID]
	out := observe.DeploySummary{
		ID: d.ID, Namespace: d.Namespace, App: d.App, Revision: d.Revision, At: d.At,
		Phase: observe.PhaseObserving, Evaluations: len(recs),
	}
	var last time.Time
	regressed := 0
	if n := len(recs); n > 0 {
		r := recs[n-1]
		dec := decision(r)
		out.Latest, out.Health, last = &dec, string(r.Assessment.Health), r.At
		regressed = r.Assessment.Regressed
		out.Phase = observe.PhaseDecided
	}
	for _, a := range acts {
		act := action(a)
		out.LastAction = &act
		switch {
		case a.Result == "recovered" || a.Result == "persisted":
			out.Phase = observe.PhaseVerified
		case a.Result == "done" && out.Phase != observe.PhaseVerified:
			out.Phase = observe.PhaseActed
		}
	}
	for _, o := range shadowEvaluations {
		if t := d.At.Add(o); t.After(last) && t.After(now) {
			out.NextEvaluation = &t
			break
		}
	}
	out.Sentence = observe.Sentence(out, v.mode(), regressed)
	return out
}

// Deploys implements observe.Source.
func (s *LiveSource) liveDeploys(r observe.Range) []observe.DeploySummary {
	now := s.now()
	var out []observe.DeploySummary
	for _, v := range s.views() {
		ix := v.index()
		for _, d := range v.ev.Deploys() {
			if v.allows(d.Namespace) && r.Contains(d.At) {
				out = append(out, v.summary(d, ix, now))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

func (s *LiveSource) liveCase(id string) (observe.Case, bool) {
	now := s.now()
	for _, v := range s.views() {
		d, ok := v.ev.DeployByID(id)
		if !ok || !v.allows(d.Namespace) {
			continue
		}
		ix := v.index()
		c := observe.Case{DeploySummary: v.summary(d, ix, now), History: []observe.Evaluation{}, Actions: []observe.Action{}}
		recs := ix.records[id]
		for i, r := range recs {
			// The newest evaluation carries every query; older ones only the
			// regressed, to keep the case small and the history readable.
			c.History = append(c.History, evaluation(r, i == len(recs)-1))
		}
		for _, a := range ix.actions[id] {
			c.Actions = append(c.Actions, action(a))
		}
		return c, true
	}
	return observe.Case{}, false
}

func (s *LiveSource) liveSeries(id, unit string) (observe.Series, bool) {
	now := s.now()
	for _, v := range s.views() {
		d, ok := v.ev.DeployByID(id)
		if !ok || !v.allows(d.Namespace) {
			continue
		}
		u, ok := v.ev.Unit(now, d, unit)
		if !ok {
			return observe.Series{}, false
		}
		out := observe.Series{DeployID: id, Namespace: d.Namespace, Key: unit, DeployAt: d.At, Control: points(u.Control)}
		if len(u.Treatment) > 0 {
			out.Treatment, out.Whole = points(u.Treatment), points(u.Obs)
		} else {
			out.Treatment = points(u.Obs)
		}
		return out, true
	}
	return observe.Series{}, false
}

func (s *LiveSource) liveActions(r observe.Range) []observe.Action {
	var out []observe.Action
	for _, v := range s.views() {
		for _, a := range v.ev.Actions() {
			if v.allows(a.Namespace) && r.Contains(a.At) {
				out = append(out, action(a))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Subscribe implements observe.Source. An event is passed on only when some watch
// may show its namespace.
func (s *LiveSource) Subscribe() (<-chan observe.Event, func()) {
	in, cancel := s.Registry.Events.Subscribe()
	return observe.FilterEvents(in, cancel, s.mayShow)
}

// mayShow says whether any watch may show a namespace: the filter for the event
// stream, which is not tied to one watch.
func (s *LiveSource) mayShow(ns string) bool {
	vs := s.views()
	if len(vs) == 0 {
		return true
	}
	for _, v := range vs {
		if v.allows(ns) {
			return true
		}
	}
	return false
}

func decision(r shadow.Record) observe.Decision {
	d := r.Decision
	out := observe.Decision{
		At: d.At, Action: string(d.Action), Wanted: string(d.Wanted), Grade: string(d.Grade),
		GradeNote: observe.GradeNote(string(d.Grade)), Impact: finite(d.Impact), Attribution: finite(r.Assessment.Attribution),
		Streak: d.Streak, Enforced: d.Enforced, Reasons: d.Reasons,
	}
	// A grade says how well the evidence supports a regression. When nothing
	// regressed there is nothing to grade, and "grade D, not enough data" on a
	// healthy deploy would be read as a warning.
	if r.Assessment.Health != attribution.HealthRegressed {
		out.Grade = ""
		out.GradeNote = "Nothing regressed, so there is no evidence to grade."
	}
	return out
}

func action(a shadow.ActionRecord) observe.Action {
	return observe.Action{At: a.At, DeployID: a.DeployID, Namespace: a.Namespace, App: a.App, Action: a.Action, Result: a.Result, Detail: a.Detail}
}

func evaluation(r shadow.Record, all bool) observe.Evaluation {
	a := r.Assessment
	e := observe.Evaluation{
		At: r.At, Health: string(a.Health), Coverage: finite(a.Coverage),
		Impact:      observe.ImpactBy{A: finite(a.Impact.A), AB: finite(a.Impact.AB), ABC: finite(a.Impact.ABC)},
		PointImpact: finite(a.PointImpact), Attribution: finite(a.Attribution), GlobalShift: a.GlobalShift,
		Counts: observe.Counts{Tested: a.Tested, Regressed: a.Regressed, Improved: a.Improved,
			Healthy: a.Healthy, Inconclusive: a.Inconclusive, Untestable: a.Untestable},
		Decision: decision(r), CappedBy: a.CappedBy(), Units: []observe.UnitEvidence{},
	}
	units := make([]attribution.UnitVerdict, 0, len(a.Units))
	for _, u := range a.Units {
		if u.Kind == attribution.KindUntestable {
			continue
		}
		if all || u.Rejected {
			units = append(units, u)
		}
	}
	// What explains the verdict first: rejected queries, then by their weight.
	sort.SliceStable(units, func(i, j int) bool {
		if units[i].Rejected != units[j].Rejected {
			return units[i].Rejected
		}
		return units[i].ImpactShare > units[j].ImpactShare
	})
	if len(units) > maxUnitsShown {
		units = units[:maxUnitsShown]
	}
	for _, u := range units {
		e.Units = append(e.Units, unit(u))
	}
	return e
}

func unit(u attribution.UnitVerdict) observe.UnitEvidence {
	ev := observe.UnitEvidence{
		Key: u.Key, Family: u.Family, Text: u.Text,
		Kind: string(u.Kind), Grade: string(u.Grade), GradeNote: observe.GradeNote(string(u.Grade)), Method: u.Method,
		Ratio: finite(u.Ratio), RatioLow: finite(u.RatioLow), RatioHigh: finite(u.RatioHigh), PReg: finite(u.PReg),
		Rejected: u.Rejected, PreN: u.PreN, PostN: u.PostN, Donors: u.Donors, ImpactShare: finite(u.ImpactShare),
		OnsetSecs: int64(u.Onset.Seconds()), Persisting: u.PersistRegress,
	}
	for _, f := range u.Flags {
		ev.Flags = append(ev.Flags, observe.Flag(string(f)))
	}
	return ev
}

func points(in []attribution.Obs) []observe.Point {
	out := make([]observe.Point, 0, len(in))
	for _, o := range in {
		out = append(out, observe.Point{At: o.At, MeanMs: finite(o.MeanMs), N: o.Calls})
	}
	return out
}

// finite maps NaN and ±Inf, which JSON cannot carry, to 0: the engine uses them
// for "not computed", which the client reads from the grade and flags instead.
func finite(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}
