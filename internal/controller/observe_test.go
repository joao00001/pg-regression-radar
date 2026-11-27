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
	"encoding/json"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/observe"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
	"github.com/joao00001/pg-regression-radar/internal/reason"
)

func replayOf(units []attribution.Unit) *replaySource {
	f := &replaySource{samples: map[int64][]pgstat.QuerySample{}}
	for i, u := range units {
		id := int64(i + 1)
		f.ids = append(f.ids, id)
		for _, o := range u.Obs {
			f.samples[id] = append(f.samples[id], pgstat.QuerySample{
				QueryID: id, QueryText: u.Text, RecordedAt: o.At,
				IntervalKnown: true, IntervalComputed: true,
				IntervalCalls: o.Calls, IntervalTotalMs: o.MeanMs * float64(o.Calls),
			})
		}
	}
	return f
}

// liveFixture builds two watches over the same simulated regression, in the
// namespace "shop": one with no allowlist, one allowed only "other".
func liveFixture(t *testing.T) (*LiveSource, shadow.Deploy, time.Time) {
	t.Helper()
	units, _, dep, _ := sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	now := dep.At.Add(30 * time.Minute)
	reg := NewRegistry()

	mk := func(enforce bool, allow []string) *WatchRuntime {
		ev := shadow.NewWithPolicy(replayOf(units), collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: enforce})
		return &WatchRuntime{Shadow: ev, ClusterName: "main", ObserveNamespaces: allow}
	}
	open, closed := mk(false, nil), mk(false, []string{"other"})
	reg.Set(types.NamespacedName{Namespace: "radar", Name: "open"}, open)
	reg.Set(types.NamespacedName{Namespace: "radar", Name: "closed"}, closed)

	d := shadow.Deploy{ID: "shop/orders/abc", Namespace: "shop", App: "orders", Revision: "abc", At: dep.At, HasRollbackTarget: true}
	dClosed := d
	dClosed.ID = "shop/ledger/zzz"
	dClosed.App = "ledger"
	for _, w := range []struct {
		rt *WatchRuntime
		d  shadow.Deploy
	}{{open, d}, {closed, dClosed}} {
		w.rt.Shadow.Register(w.d)
		w.rt.Shadow.Evaluate(now, w.d)
		w.rt.Shadow.Evaluate(now.Add(5*time.Minute), w.d)
	}
	return &LiveSource{Registry: reg, Started: dep.At.Add(-time.Hour), Now: func() time.Time { return now.Add(6 * time.Minute) }}, d, now
}

func TestLiveSourceShowsTheCaseAndItsEvidence(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()

	list, _ := src.Deploys(ctx, observe.Range{})
	if len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("deploys: %+v", list)
	}
	if list[0].Evaluations != 2 || list[0].Phase != observe.PhaseDecided || list[0].Latest == nil || list[0].Sentence == "" {
		t.Fatalf("card: %+v", list[0])
	}
	if list[0].NextEvaluation == nil {
		t.Error("an evaluation is still scheduled; the card should say when")
	}

	c, ok, err := src.Case(ctx, d.ID)
	if err != nil || !ok {
		t.Fatalf("case: %v %v", ok, err)
	}
	if len(c.History) != 2 || len(c.History[1].Units) == 0 {
		t.Fatalf("history: %d evaluations", len(c.History))
	}
	// The newest evaluation lists every tested query; an older one, only the regressed.
	for _, u := range c.History[0].Units {
		if !u.Rejected {
			t.Fatalf("older evaluations should keep only regressed queries, got %+v", u)
		}
	}
	first := c.History[1].Units[0]
	if !first.Rejected || first.GradeNote == "" {
		t.Fatalf("the regressed query should come first with a grade note: %+v", first)
	}
	// JSON cannot carry NaN or Inf: the whole case must encode.
	if _, err := json.Marshal(c); err != nil {
		t.Fatal(err)
	}

	s, ok, _ := src.Series(ctx, d.ID, first.Key)
	if !ok || len(s.Treatment) == 0 || s.DeployAt.IsZero() {
		t.Fatalf("series: %v %+v", ok, s)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := src.Series(ctx, d.ID, "no-such-query"); ok {
		t.Error("an unknown query has no series")
	}
}

func TestAHealthyDeployIsNotGivenAGrade(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 4}.Generate()
	reg := NewRegistry()
	ev := shadow.New(replayOf(units), collector.FingerprintQuery, attribution.Config{})
	reg.Set(types.NamespacedName{Namespace: "radar", Name: "w"}, &WatchRuntime{Shadow: ev})
	d := shadow.Deploy{ID: "shop/cart/1", Namespace: "shop", App: "cart", At: dep.At, HasRollbackTarget: true}
	ev.Register(d)
	ev.Evaluate(dep.At.Add(30*time.Minute), d)
	src := &LiveSource{Registry: reg, Started: dep.At, Now: func() time.Time { return dep.At.Add(31 * time.Minute) }}
	l, _ := src.Deploys(context.Background(), observe.Range{})
	if len(l) != 1 || l[0].Health != "Healthy" || l[0].Latest.Grade != "" || l[0].Latest.GradeNote == "" {
		t.Fatalf("%+v", l)
	}
}

func TestLiveSourceAppliesEachWatchsAllowlist(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()
	// "closed" may only show namespace "other": its deploy in "shop" is invisible
	// everywhere, and looks like one that does not exist.
	for _, id := range []string{"shop/ledger/zzz", "shop/nothing/0"} {
		if _, ok, _ := src.Case(ctx, id); ok {
			t.Errorf("case %s should not be found", id)
		}
		if _, ok, _ := src.Series(ctx, id, "x"); ok {
			t.Errorf("series %s should not be found", id)
		}
	}
	list, _ := src.Deploys(ctx, observe.Range{})
	for _, x := range list {
		if x.ID == "shop/ledger/zzz" {
			t.Fatal("deploy of an out-of-scope watch is listed")
		}
	}
	// The other watch is unaffected.
	if _, ok, _ := src.Case(ctx, d.ID); !ok {
		t.Fatal("the open watch's deploy should be visible")
	}
	// An action recorded for the out-of-scope deploy is not in the ledger either.
	src.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		rt.Shadow.RecordAction(shadow.ActionRecord{At: time.Now(), DeployID: "shop/ledger/zzz", Namespace: "shop", App: "ledger", Action: "abort", Result: "done"})
	})
	acts, _ := src.Actions(ctx, observe.Range{})
	// Recorded in both watches; only the open one may show it.
	if len(acts) != 1 {
		t.Fatalf("each watch's ledger is filtered by its own allowlist: got %d entries", len(acts))
	}

	o, _ := src.Overview(ctx)
	if !o.Scope.All {
		t.Error("one watch has no allowlist, so the union is every namespace")
	}
	if len(o.Watches) != 2 || o.Watches[0].Name != "closed" || o.Watches[0].Actuation.Namespaces[0] != "other" {
		t.Errorf("watches: %+v", o.Watches)
	}
	if o.History.Store != "memory" || o.History.Since == nil || !o.History.Recording {
		t.Errorf("history: %+v", o.History)
	}
}

func TestLiveSourcePhaseFollowsTheLedger(t *testing.T) {
	src, d, _ := liveFixture(t)
	ctx := context.Background()
	phase := func() observe.Phase {
		l, _ := src.Deploys(ctx, observe.Range{})
		for _, x := range l {
			if x.ID == d.ID {
				return x.Phase
			}
		}
		t.Fatal("deploy missing")
		return ""
	}
	var ev *shadow.Evaluator
	src.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		if k.Name == "open" {
			ev = rt.Shadow
		}
	})
	if phase() != observe.PhaseDecided {
		t.Fatalf("phase %s", phase())
	}
	ev.RecordAction(shadow.ActionRecord{At: time.Now(), DeployID: d.ID, Namespace: "shop", App: "orders", Action: "abort", Result: "failed"})
	if phase() != observe.PhaseDecided {
		t.Errorf("a failed action did not act: %s", phase())
	}
	ev.RecordAction(shadow.ActionRecord{At: time.Now(), DeployID: d.ID, Namespace: "shop", App: "orders", Action: "abort", Result: "done"})
	if phase() != observe.PhaseActed {
		t.Errorf("phase %s", phase())
	}
	ev.RecordAction(shadow.ActionRecord{At: time.Now(), DeployID: d.ID, Namespace: "shop", App: "orders", Action: "abort", Result: "persisted"})
	if phase() != observe.PhaseVerified {
		t.Errorf("phase %s", phase())
	}
	c, _, _ := src.Case(ctx, d.ID)
	if len(c.Actions) != 3 || c.LastAction == nil || c.LastAction.Result != "persisted" {
		t.Errorf("ledger of the case: %+v", c.Actions)
	}
}

func TestLiveSourceEventsFollowTheEvaluatorAndTheAllowlist(t *testing.T) {
	src, _, _ := liveFixture(t)
	ch, cancel := src.Subscribe()
	defer cancel()
	var open, closed *shadow.Evaluator
	src.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		if k.Name == "open" {
			open = rt.Shadow
		} else {
			closed = rt.Shadow
		}
	})
	// "open" has no allowlist, so every namespace may be shown: the event passes.
	open.Register(shadow.Deploy{ID: "shop/new/1", Namespace: "shop", App: "new", At: time.Now()})
	closed.RecordAction(shadow.ActionRecord{DeployID: "x", Namespace: "shop", App: "x", Action: "pause", Result: "done"})
	got := map[observe.EventType]int{}
	deadline := time.After(time.Second)
	for got[observe.EventDeployRegistered] < 1 || got[observe.EventAction] < 1 {
		select {
		case e := <-ch:
			got[e.Type]++
		case <-deadline:
			t.Fatalf("events seen: %v", got)
		}
	}
}

func TestEvaluatorKeepsABoundedNumberOfRecords(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 4}.Generate()
	ev := shadow.New(replayOf(units), collector.FingerprintQuery, attribution.Config{})
	ev.MaxRecords = 3
	d := shadow.Deploy{ID: "d", At: dep.At}
	for i := 0; i < 6; i++ {
		ev.Evaluate(dep.At.Add(time.Duration(30+i)*time.Minute), d)
	}
	recs := ev.Records()
	if len(recs) != 3 || !recs[2].At.Equal(dep.At.Add(35*time.Minute)) {
		t.Fatalf("records are not bounded to the newest: %d", len(recs))
	}
}

// The dashboard must say what `kubectl describe postgreswatch` says: the
// Overview carries the conditions refreshStatus computed, with the reason code
// and the docs page, and the message without the link that ends it in status.
func TestOverviewCarriesTheHealthConditions(t *testing.T) {
	src, _, _ := liveFixture(t)
	var rt *WatchRuntime
	src.Registry.Each(func(k types.NamespacedName, r *WatchRuntime) {
		if k.Name == "open" {
			rt = r
		}
	})
	// Before the first refresh there is nothing to show, and nothing breaks.
	o, err := src.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range o.Watches {
		if len(w.Conditions) != 0 {
			t.Fatalf("%s: conditions before the first refresh: %+v", w.Name, w.Conditions)
		}
	}

	watch := &radarv1alpha1.PostgresWatch{ObjectMeta: metav1.ObjectMeta{Name: "open", Namespace: "radar"}}
	rt.SetHealth(healthConditions(watch, rt, time.Now()))
	o, _ = src.Overview(context.Background())
	var got *observe.Condition
	for _, w := range o.Watches {
		if w.Name != "open" {
			continue
		}
		for i := range w.Conditions {
			if w.Conditions[i].Type == ConditionActuating {
				got = &w.Conditions[i]
			}
		}
	}
	if got == nil {
		t.Fatalf("no Actuating condition in the overview: %+v", o.Watches)
	}
	if got.Status != "False" || got.Reason != string(reason.ShadowMode) {
		t.Errorf("condition = %+v, want False/ShadowMode", got)
	}
	if got.Docs != reason.DocsURL(reason.ShadowMode) {
		t.Errorf("docs = %q", got.Docs)
	}
	if strings.Contains(got.Message, "http") {
		t.Errorf("the link belongs in docs, not in the message: %q", got.Message)
	}
}

func TestTheActionLedgerCarriesTheReasonCode(t *testing.T) {
	src, d, _ := liveFixture(t)
	var ev *shadow.Evaluator
	src.Registry.Each(func(k types.NamespacedName, rt *WatchRuntime) {
		if k.Name == "open" {
			ev = rt.Shadow
		}
	})
	ev.RecordAction(shadow.ActionRecord{At: time.Now(), DeployID: d.ID, Namespace: "shop", App: "orders",
		Action: "abort", Result: "skipped", Reason: string(reason.NamespaceNotAllowed), Detail: "namespace not in attribution.actuate.namespaces"})
	c, _, _ := src.Case(context.Background(), d.ID)
	if len(c.Actions) != 1 || c.Actions[0].Reason != string(reason.NamespaceNotAllowed) {
		t.Errorf("actions of the case: %+v", c.Actions)
	}
}
