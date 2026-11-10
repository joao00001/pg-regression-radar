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

package playground

import (
	"encoding/json"
	"math/rand/v2"
	"testing"
	"time"
)

const (
	scrape  = 15 * time.Second
	qid     = "-2050033811876822511"
	qtext   = "SELECT * FROM orders WHERE customer_id = $1"
	minute  = int64(60_000)
	startMs = int64(1_700_000_000_000)
)

type scenario struct {
	// priorCalls is the executions already counted when the first scrape runs.
	priorCalls int64
	// callsPerScrape is the traffic, in executions per scrape interval.
	callsPerScrape int64
	beforeMs       float64
	afterMs        float64
	// resetAfterDeploy, when positive, resets the counters this many scrapes
	// after the deploy (as pg_stat_statements_reset() would).
	resetAfterDeploy int
	// minExecutions overrides the engine's execution floor when positive.
	minExecutions int64
}

// build returns 60 minutes of scrapes, the deploy landing at minute 30.
func build(sc scenario, seed uint64) Request {
	rng := rand.New(rand.NewPCG(seed, 1))
	deploy := startMs + 30*minute
	n := int(60 * time.Minute / scrape)
	calls, total := sc.priorCalls, float64(sc.priorCalls)*sc.beforeMs
	since := startMs
	var snaps []Snapshot
	for i := 0; i <= n; i++ {
		at := startMs + int64(i)*scrape.Milliseconds()
		if i > 0 {
			mean := sc.beforeMs
			if at >= deploy {
				mean = sc.afterMs
			}
			if sc.resetAfterDeploy > 0 && at == deploy+int64(sc.resetAfterDeploy)*scrape.Milliseconds() {
				calls, total, since = 0, 0, at
			}
			for k := int64(0); k < sc.callsPerScrape; k++ {
				calls++
				total += mean * (1 + 0.25*rng.NormFloat64())
			}
		}
		snaps = append(snaps, Snapshot{At: at, Rows: []Row{{
			QueryID: qid, Query: qtext, Calls: calls, TotalExecTime: total, StatsSince: since,
		}}})
	}
	return Request{Snapshots: snaps, DeployAt: deploy, Config: Config{MinExecutions: sc.minExecutions}}
}

func TestRunScenarios(t *testing.T) {
	tests := []struct {
		name string
		sc   scenario
		want string
	}{
		{"regression is detected", scenario{callsPerScrape: 200, beforeMs: 10, afterMs: 20}, "Detected"},
		{"steady state is not a regression", scenario{callsPerScrape: 200, beforeMs: 10, afterMs: 10}, "NoRegression"},
		{"regression hidden behind a long history is still detected", scenario{priorCalls: 50_000_000, callsPerScrape: 200, beforeMs: 10, afterMs: 20}, "Detected"},
		{"counter reset without a regression is not a regression", scenario{callsPerScrape: 200, beforeMs: 10, afterMs: 10, resetAfterDeploy: 20}, "NoRegression"},
		{"fewer executions than the floor is insufficient data", scenario{callsPerScrape: 1, beforeMs: 10, afterMs: 20, minExecutions: 500}, "InsufficientData"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := Run(build(tt.sc, 7))
			if resp.Error != "" {
				t.Fatalf("error: %s", resp.Error)
			}
			if len(resp.Results) != 1 {
				t.Fatalf("results = %d, want 1: %+v", len(resp.Results), resp.Results)
			}
			got := resp.Results[0]
			if got.Status != tt.want {
				t.Fatalf("status = %s, want %s (%+v)", got.Status, tt.want, got)
			}
			if got.QueryID != qid {
				t.Errorf("queryid = %s, want %s", got.QueryID, qid)
			}
			if tt.want == "Detected" {
				if got.LatencyChangeFactor < 1.7 || got.LatencyChangeFactor > 2.3 {
					t.Errorf("factor = %.2f, want about 2", got.LatencyChangeFactor)
				}
				if got.DetectedChangeAt == 0 {
					t.Error("DetectedChangeAt is zero for a detected regression")
				}
			}
		})
	}
}

func TestSeriesExposesTheCumulativeMeanTrap(t *testing.T) {
	// With a long history the cumulative mean barely moves, while the
	// interval mean doubles: this is the contrast the playground charts.
	resp := Run(build(scenario{priorCalls: 50_000_000, callsPerScrape: 200, beforeMs: 10, afterMs: 20}, 3))
	pts := resp.Series[0].Points
	last := pts[len(pts)-1]
	if last.CumulativeMeanMs > 10.1 {
		t.Errorf("cumulative mean = %.3f, want it diluted to about 10", last.CumulativeMeanMs)
	}
	if last.IntervalMeanMs < 18 {
		t.Errorf("interval mean = %.3f, want about 20", last.IntervalMeanMs)
	}
	if pts[0].IntervalKnown {
		t.Error("first scrape must not claim a known interval")
	}
}

func TestResetIsReportedInSeries(t *testing.T) {
	resp := Run(build(scenario{callsPerScrape: 200, beforeMs: 10, afterMs: 10, resetAfterDeploy: 20}, 5))
	n := 0
	for _, p := range resp.Series[0].Points {
		if p.Reset {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("resets = %d, want 1", n)
	}
}

func TestRunJSON(t *testing.T) {
	in, err := json.Marshal(build(scenario{callsPerScrape: 200, beforeMs: 10, afterMs: 20}, 7))
	if err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.Unmarshal([]byte(RunJSON(string(in))), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" || len(resp.Results) != 1 || resp.Results[0].Status != "Detected" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestRunErrors(t *testing.T) {
	if r := Run(Request{}); r.Error == "" {
		t.Error("empty request must report an error")
	}
	bad := Request{Snapshots: []Snapshot{{At: 1, Rows: []Row{{QueryID: "abc"}}}}}
	if r := Run(bad); r.Error == "" {
		t.Error("non-numeric queryid must report an error")
	}
	if r := RunJSON("{not json"); r == "" {
		t.Error("RunJSON must always return JSON")
	}
}
