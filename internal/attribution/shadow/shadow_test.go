package shadow_test

import (
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
)

// fakeSource replays a simulated world as pg_stat_statements samples.
type fakeSource struct {
	ids     []int64
	samples map[int64][]pgstat.QuerySample
}

func (f *fakeSource) AllQueryIDs() []int64 { return f.ids }
func (f *fakeSource) SamplesInRange(id int64, from, to time.Time) []pgstat.QuerySample {
	var out []pgstat.QuerySample
	for _, s := range f.samples[id] {
		if !s.RecordedAt.Before(from) && !s.RecordedAt.After(to) {
			out = append(out, s)
		}
	}
	return out
}

func replay(units []attribution.Unit) *fakeSource {
	f := &fakeSource{samples: map[int64][]pgstat.QuerySample{}}
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

func TestShadowFlagsARealRegressionAndNeverActs(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	ev := shadow.New(replay(units), collector.FingerprintQuery, attribution.Config{})
	now := dep.At.Add(30 * time.Minute)
	d := shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true}
	ev.Evaluate(now, d)
	rec, ok := ev.Evaluate(now.Add(5*time.Minute), d)
	if !ok {
		t.Fatal("no record")
	}
	if rec.Assessment.Health != attribution.HealthRegressed {
		t.Fatalf("health %s", rec.Assessment.Health)
	}
	if rec.Decision.Action != attribution.ActionHold || rec.Decision.Enforced {
		t.Fatalf("decision %+v", rec.Decision)
	}
	if len(ev.Records()) != 2 {
		t.Fatalf("records %d", len(ev.Records()))
	}
}

func TestShadowQuietOnAA(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 4}.Generate()
	ev := shadow.New(replay(units), collector.FingerprintQuery, attribution.Config{})
	rec, _ := ev.Evaluate(dep.At.Add(30*time.Minute), shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true})
	if rec.Decision.Action != attribution.ActionNone {
		t.Fatalf("decision %+v", rec.Decision)
	}
}

func TestShadowNoSamples(t *testing.T) {
	ev := shadow.New(&fakeSource{}, collector.FingerprintQuery, attribution.Config{})
	if _, ok := ev.Evaluate(time.Now(), shadow.Deploy{ID: "x", At: time.Now()}); ok {
		t.Fatal("expected no record without samples")
	}
}

// Verify feeds the circuit breaker: a regression that is still there after an
// action counts as a failure, and after BreakerLimit of them the policy stops
// acting. A deploy with nothing wrong verifies cleanly and resets the count.
func TestVerifyCountsAnActionThatDidNotHelp(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{},
		attribution.PolicyConfig{Enforce: true, BreakerLimit: 2})
	d := shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true}
	now := dep.At.Add(45 * time.Minute)

	v, ok := ev.Verify(now, d)
	if !ok || v.Effective {
		t.Fatalf("a regression that is still there verified as effective: %+v ok=%v", v, ok)
	}
	if ev.BreakerOpen() {
		t.Fatal("breaker opened after one failure, limit is 2")
	}
	if v, _ = ev.Verify(now, d); v.Effective || !ev.BreakerOpen() {
		t.Fatalf("breaker = %v after two failures, want open", ev.BreakerOpen())
	}
	if len(ev.Records()) != 0 {
		t.Fatal("Verify must not record a decision")
	}
}

func TestVerifyAcceptsAHealthyDeploy(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 4}.Generate()
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{},
		attribution.PolicyConfig{Enforce: true, BreakerLimit: 2})
	d := shadow.Deploy{ID: dep.ID, At: dep.At}
	for i := 0; i < 3; i++ {
		if v, ok := ev.Verify(dep.At.Add(45*time.Minute), d); !ok || !v.Effective {
			t.Fatalf("healthy deploy did not verify: %+v ok=%v", v, ok)
		}
	}
	if ev.BreakerOpen() {
		t.Fatal("breaker opened on a healthy deploy")
	}
}

func TestActionLedgerIsBounded(t *testing.T) {
	ev := shadow.New(&fakeSource{}, collector.FingerprintQuery, attribution.Config{})
	for i := 0; i < 250; i++ {
		ev.RecordAction(shadow.ActionRecord{DeployID: "d", Action: "pause", Result: "done", Detail: string(rune('a' + i%26))})
	}
	if n := len(ev.Actions()); n != 200 {
		t.Fatalf("ledger holds %d entries, want it capped at 200", n)
	}
}
