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

package shadow_test

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/adapter"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/pglog"
)

func median(x []float64) float64 {
	sort.Float64s(x)
	return x[len(x)/2]
}

// logWorld writes the log a real canary would leave: every minute, each stable
// pod and the canary pod execute each query, the server logs a fraction of the
// executions, and the canary alone runs the regression. Durations are lognormal.
// pgss-side data comes from the sim world, without the regression (the whole of
// the traffic is mostly stable pods).
func logWorld(units []attribution.Unit, dep attribution.Deploy, affected func(i int) bool, factor, canaryShare, rate float64, stablePods int, seed int64) *pglog.Store {
	rng := rand.New(rand.NewSource(seed))
	st := pglog.NewStore(time.Minute, 6*time.Hour, 0)
	st.SetSampling(pglog.Sampling{Rate: rate, AlwaysAtOrAboveMs: -1})
	for i, u := range units {
		var lat, calls []float64
		for _, o := range u.Obs {
			lat = append(lat, o.MeanMs)
			calls = append(calls, float64(o.Calls))
		}
		L, C := median(lat), median(calls)
		first, last := u.Obs[0].At, u.Obs[len(u.Obs)-1].At
		for at := first; !at.After(last); at = at.Add(time.Minute) {
			post := at.After(dep.At)
			pods := make([]string, 0, stablePods+1)
			share := make([]float64, 0, stablePods+1)
			for p := 0; p < stablePods; p++ {
				pods = append(pods, fmt.Sprintf("web-old-%c", 'a'+p))
				if post {
					share = append(share, (1-canaryShare)/float64(stablePods))
				} else {
					share = append(share, 1/float64(stablePods))
				}
			}
			if post {
				pods = append(pods, "web-new-z")
				share = append(share, canaryShare)
			}
			for pi, pod := range pods {
				mu := math.Log(L) - 0.125 // sigma 0.5
				if strings.HasPrefix(pod, "web-new") && affected(i) {
					mu += math.Log(factor)
				}
				n := poisson(rng, C*share[pi]*rate)
				for k := 0; k < n; k++ {
					d := math.Exp(mu + 0.5*rng.NormFloat64())
					st.Add(pglog.Record{At: at.Add(-30 * time.Second), App: pod, QueryID: int64(i + 1), DurationMs: d})
				}
			}
		}
	}
	return st
}

func poisson(rng *rand.Rand, lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 30 {
		return int(math.Max(0, math.Round(lambda+math.Sqrt(lambda)*rng.NormFloat64())))
	}
	l, k, p := math.Exp(-lambda), 0, 1.0
	for p > l {
		k++
		p *= rng.Float64()
	}
	return k - 1
}

func armSpec(d shadow.Deploy) (adapter.ArmSpec, bool) {
	return adapter.ArmSpec{
		Treatment: func(a string) bool { return strings.HasPrefix(a, "web-new-") },
		Control:   func(a string) bool { return strings.HasPrefix(a, "web-old-") },
	}, true
}

func runCanaryRate(t *testing.T, seed uint64, factor, rate float64) (shadow.Record, bool) {
	t.Helper()
	w := sim.World{Seed: seed}
	if factor != 1 {
		w.Deploy = sim.Effect{Fraction: 0.2, Factor: factor}
	}
	units, _, dep, _ := w.Generate()
	// The queries the deploy slowed are the ones whose own history steps up at it.
	hit := map[int]bool{}
	for i, u := range units {
		var pre, post []float64
		for _, o := range u.Obs {
			if o.At.After(dep.At.Add(10 * time.Minute)) {
				post = append(post, o.MeanMs)
			} else if o.At.Before(dep.At) {
				pre = append(pre, o.MeanMs)
			}
		}
		if factor != 1 && len(pre) > 5 && len(post) > 5 && median(post)/median(pre) > 1+(factor-1)/2 {
			hit[i] = true
		}
	}
	affected := func(i int) bool { return hit[i] }
	logs := logWorld(units, dep, affected, factor, 0.2, rate, 4, int64(seed))
	ev := shadow.NewWithPolicy(replay(units), collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: true})
	ev.Arms, ev.ArmsFor = logs, armSpec
	d := shadow.Deploy{ID: dep.ID, At: dep.At, HasRollbackTarget: true}
	now := dep.At.Add(30 * time.Minute)
	ev.Evaluate(now, d)
	return ev.Evaluate(now.Add(5*time.Minute), d)
}

// The power of a canary check is set by how many executions the log holds for
// the canary arm: traffic x canary share x log_statement_sample_rate. These tests
// fix a rate at which the world gives enough samples; the table in
// docs/attribution-engine.md says what to expect at others.
const testRate = 0.5

func TestCanaryFromLogsIsConvictedAtGradeA(t *testing.T) {
	if testing.Short() {
		t.Skip("simulation")
	}
	var rolled int
	const runs = 5
	for s := uint64(1); s <= runs; s++ {
		rec, ok := runCanaryRate(t, s, 1.6, testRate)
		if !ok {
			t.Fatal("no record")
		}
		if rec.Decision.Action == attribution.ActionRollback && rec.Decision.Grade == attribution.GradeA {
			rolled++
		} else {
			t.Logf("seed %d: %s grade %s health %s reasons %v", s, rec.Decision.Action, rec.Decision.Grade, rec.Assessment.Health, rec.Decision.Reasons)
		}
	}
	if rolled < 3 {
		t.Fatalf("convicted %d of %d real canary regressions at grade A", rolled, runs)
	}
}

func TestCanaryFromLogsAAIsLeftAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("simulation")
	}
	for s := uint64(1); s <= 4; s++ {
		for _, rate := range []float64{0.05, testRate} {
			rec, ok := runCanaryRate(t, s, 1, rate)
			if !ok {
				continue
			}
			if rec.Decision.Action == attribution.ActionRollback || rec.Decision.Action == attribution.ActionHold {
				t.Fatalf("seed %d rate %.2f: acted on an A/A canary: %s (%v)", s, rate, rec.Decision.Action, rec.Decision.Reasons)
			}
		}
	}
}
