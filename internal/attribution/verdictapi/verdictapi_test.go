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

package verdictapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
	"github.com/joao00001/pg-regression-radar/internal/attribution/verdictapi"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

type src struct {
	ids     []int64
	samples map[int64][]pgstat.QuerySample
}

func (s *src) AllQueryIDs() []int64 { return s.ids }
func (s *src) SamplesInRange(id int64, from, to time.Time) []pgstat.QuerySample {
	var out []pgstat.QuerySample
	for _, x := range s.samples[id] {
		if !x.RecordedAt.Before(from) && !x.RecordedAt.After(to) {
			out = append(out, x)
		}
	}
	return out
}

func replay(units []attribution.Unit) *src {
	s := &src{samples: map[int64][]pgstat.QuerySample{}}
	for i, u := range units {
		id := int64(i + 1)
		s.ids = append(s.ids, id)
		for _, o := range u.Obs {
			s.samples[id] = append(s.samples[id], pgstat.QuerySample{
				QueryID: id, QueryText: u.Text, RecordedAt: o.At, IntervalKnown: true, IntervalComputed: true,
				IntervalCalls: o.Calls, IntervalTotalMs: o.MeanMs * float64(o.Calls),
			})
		}
	}
	return s
}

func get(t *testing.T, h *verdictapi.Handler, url string) (int, verdictapi.Response) {
	t.Helper()
	mux := http.NewServeMux()
	h.Routes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var r verdictapi.Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, r
}

// judged builds an evaluator for a simulated world, registers the deploy and
// evaluates it twice (the policy needs two agreeing evaluations to act).
func judged(t *testing.T, w sim.World, enforce bool) (*shadow.Evaluator, shadow.Deploy) {
	t.Helper()
	if w.Queries == 0 {
		w.Queries = 60
	}
	units, _, dep, _ := w.Generate()
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: enforce})
	d := shadow.Deploy{ID: dep.ID, Namespace: "prod", App: "api", Revision: "r2", At: dep.At, HasRollbackTarget: true}
	ev.Register(d)
	ev.Evaluate(dep.At.Add(30*time.Minute), d)
	ev.Evaluate(dep.At.Add(35*time.Minute), d)
	return ev, d
}

func handler(evs ...*shadow.Evaluator) *verdictapi.Handler {
	return &verdictapi.Handler{Evaluators: func() []*shadow.Evaluator { return evs }}
}

func TestDecisionVerdictMapping(t *testing.T) {
	cases := []struct {
		d    attribution.Decision
		want attribution.Verdict
	}{
		{attribution.Decision{Action: attribution.ActionRollback, Enforced: true}, attribution.VerdictFail},
		{attribution.Decision{Action: attribution.ActionHold, Enforced: true}, attribution.VerdictInconclusive},
		{attribution.Decision{Action: attribution.ActionAlert, Enforced: true}, attribution.VerdictPass},
		{attribution.Decision{Action: attribution.ActionNone, Enforced: true}, attribution.VerdictPass},
		{attribution.Decision{Action: attribution.ActionRollback, Enforced: false}, attribution.VerdictPass},
		{attribution.Decision{Action: attribution.ActionHold, Enforced: false}, attribution.VerdictPass},
	}
	for _, c := range cases {
		if got := c.d.Verdict(); got != c.want {
			t.Errorf("%+v: got %s, want %s", c.d, got, c.want)
		}
	}
}

func TestVerdictRequiresNamespaceAndApp(t *testing.T) {
	if code, _ := get(t, handler(), "/api/v1/attribution/verdict?app=api"); code != http.StatusBadRequest {
		t.Fatalf("code %d", code)
	}
}

func TestUnknownDeployNeverBlocks(t *testing.T) {
	code, r := get(t, handler(), "/api/v1/attribution/verdict?namespace=prod&app=nope")
	if code != 200 || r.Verdict != attribution.VerdictPass || r.Known {
		t.Fatalf("%d %+v", code, r)
	}
}

func TestPendingEvaluationNeverBlocks(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 3, Queries: 60}.Generate()
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: true})
	ev.Register(shadow.Deploy{ID: dep.ID, Namespace: "prod", App: "api", Revision: "r2", At: dep.At})
	_, r := get(t, handler(ev), "/api/v1/attribution/verdict?namespace=prod&app=api")
	if r.Verdict != attribution.VerdictPass || !r.Pending || !r.Known {
		t.Fatalf("%+v", r)
	}
}

func TestShadowModeServesPassButReportsWhatItWouldDo(t *testing.T) {
	ev, _ := judged(t, sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}, false)
	_, r := get(t, handler(ev), "/api/v1/attribution/verdict?namespace=prod&app=api")
	if r.Verdict != attribution.VerdictPass || r.Enforced {
		t.Fatalf("shadow mode must serve pass: %+v", r)
	}
	if r.WouldBe != attribution.VerdictInconclusive {
		t.Fatalf("wouldBe %s, want inconclusive: %+v", r.WouldBe, r)
	}
}

func TestEnforceServesInconclusiveForGradeB(t *testing.T) {
	ev, _ := judged(t, sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}, true)
	_, r := get(t, handler(ev), "/api/v1/attribution/verdict?namespace=prod&app=api&revision=r2")
	if r.Verdict != attribution.VerdictInconclusive || !r.Enforced || r.Grade != attribution.GradeB {
		t.Fatalf("%+v", r)
	}
}

// pg_stat_statements samples carry no arm label, so through the collector path
// even a canary's data reaches grade B at best and the verdict is "inconclusive"
// (a pause), never "fail". Grade A, and with it "fail", needs a sample source
// that labels arms; the Decision-to-Verdict mapping for it is covered above.
func TestSamplesWithoutArmLabelsNeverAbort(t *testing.T) {
	ev, _ := judged(t, sim.World{Seed: 2, Queries: 120, Canary: true, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}, true)
	_, r := get(t, handler(ev), "/api/v1/attribution/verdict?namespace=prod&app=api")
	if r.Verdict != attribution.VerdictInconclusive || r.Grade != attribution.GradeB {
		t.Fatalf("%+v", r)
	}
}

func TestEnforceNeverBlocksAHealthyDeployOrALoadShock(t *testing.T) {
	for name, w := range map[string]sim.World{
		"A/A":        {Seed: 4},
		"load shock": {Seed: 4, LoadShock: 0.7},
		"stats":      {Seed: 4, StatsTable: 3, StatsFactor: 1.8},
		"cold cache": {Seed: 4, Warmup: 1.8, WarmupTicks: 10},
	} {
		ev, _ := judged(t, w, true)
		_, r := get(t, handler(ev), "/api/v1/attribution/verdict?namespace=prod&app=api")
		if r.Verdict != attribution.VerdictPass {
			t.Errorf("%s: served %s (%v)", name, r.Verdict, r.Reasons)
		}
	}
}

func TestRevisionSelectsTheRelease(t *testing.T) {
	ev, _ := judged(t, sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}, true)
	_, r := get(t, handler(ev), "/api/v1/attribution/verdict?namespace=prod&app=api&revision=other")
	if r.Known {
		t.Fatalf("an unrelated revision must be unknown: %+v", r)
	}
}
