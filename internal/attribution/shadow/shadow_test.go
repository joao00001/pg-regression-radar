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

	v, ok := ev.Verify(now, d, time.Time{})
	if !ok || v.Effective {
		t.Fatalf("a regression that is still there verified as effective: %+v ok=%v", v, ok)
	}
	if ev.BreakerOpen() {
		t.Fatal("breaker opened after one failure, limit is 2")
	}
	if v, _ = ev.Verify(now, d, time.Time{}); v.Effective || !ev.BreakerOpen() {
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
		if v, ok := ev.Verify(dep.At.Add(45*time.Minute), d, time.Time{}); !ok || !v.Effective {
			t.Fatalf("healthy deploy did not verify: %+v ok=%v", v, ok)
		}
	}
	if ev.BreakerOpen() {
		t.Fatal("breaker opened on a healthy deploy")
	}
}

// stopped returns the units of a deploy whose effect ends at when, as when a
// canary is aborted: what the world with the effect shows before it, and what the
// same world without the effect shows from then on. Both draw the same noise.
func stopped(t *testing.T, seed uint64, when time.Duration) ([]attribution.Unit, attribution.Deploy) {
	t.Helper()
	post := 60 * time.Minute
	bad, _, dep, _ := sim.World{Seed: seed, Post: post, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	ok, _, dep2, _ := sim.World{Seed: seed, Post: post}.Generate()
	if len(bad) != len(ok) || !dep.At.Equal(dep2.At) {
		t.Fatal("the two worlds do not line up")
	}
	cut := dep.At.Add(when)
	out := make([]attribution.Unit, len(bad))
	for i := range bad {
		out[i] = bad[i]
		out[i].Obs = nil
		for j, o := range bad[i].Obs {
			if j >= len(ok[i].Obs) || !ok[i].Obs[j].At.Equal(o.At) {
				t.Fatal("the two worlds do not line up")
			}
			if o.At.After(cut) {
				o = ok[i].Obs[j]
			}
			out[i].Obs = append(out[i].Obs, o)
		}
	}
	return out, dep
}

// A canary that ran slow until it was aborted, and is gone, must verify as
// recovered. Reading the whole window used to count the slow stretch against the
// abort, and two correct aborts opened the circuit breaker.
func TestVerifyIgnoresWhatTheDeployDidBeforeTheAbort(t *testing.T) {
	units, dep := stopped(t, 3, 20*time.Minute)
	d := shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true}
	now := dep.At.Add(40 * time.Minute)
	since := dep.At.Add(23 * time.Minute) // the abort at +20, and three minutes to drain
	policy := attribution.PolicyConfig{Enforce: true, BreakerLimit: 2}

	whole := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{}, policy)
	if v, ok := whole.Verify(now, d, time.Time{}); !ok || v.Effective {
		t.Fatalf("reading the whole window should still see the slow stretch (this is what the since argument is for): %+v ok=%v", v, ok)
	}

	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{}, policy)
	for i := 0; i < 3; i++ {
		v, ok := ev.Verify(now, d, since)
		if !ok || !v.Effective {
			t.Fatalf("check %d: an abort that worked verified as %+v (ok=%v)", i+1, v, ok)
		}
	}
	if ev.BreakerOpen() {
		t.Fatal("the breaker opened on aborts that worked")
	}
}

// An abort that did not take, the canary still serving, must still count: the
// regression is in the data after the abort too.
func TestVerifyStillCatchesAnAbortThatDidNotTake(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 3, Post: 60 * time.Minute, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	d := shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true}
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{},
		attribution.PolicyConfig{Enforce: true, BreakerLimit: 2})
	now := dep.At.Add(40 * time.Minute)
	since := dep.At.Add(23 * time.Minute)
	if v, ok := ev.Verify(now, d, since); !ok || v.Effective {
		t.Fatalf("an abort that did not take verified as %+v (ok=%v)", v, ok)
	}
	if v, _ := ev.Verify(now, d, since); v.Effective || !ev.BreakerOpen() {
		t.Fatalf("two aborts that did not take should open the breaker, open=%v", ev.BreakerOpen())
	}
}

// With nothing after the abort to read, the check cannot be made. That is not a
// failure of the abort and must not count towards the breaker.
func TestVerifyWithTooLittleAfterTheActionIsNotAFailure(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 3, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	d := shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true}
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{},
		attribution.PolicyConfig{Enforce: true, BreakerLimit: 1})
	now := dep.At.Add(30 * time.Minute)
	if _, ok := ev.Verify(now, d, now.Add(-time.Minute)); ok {
		t.Fatal("a check with one minute of data after the action should not be made")
	}
	if ev.BreakerOpen() {
		t.Fatal("a check that was not made counted as a failure")
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
