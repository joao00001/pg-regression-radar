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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto_prom "github.com/prometheus/client_model/go"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/revert"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/pgstat"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

type replaySource struct {
	ids     []int64
	samples map[int64][]pgstat.QuerySample
}

func (r *replaySource) AllQueryIDs() []int64 { return r.ids }
func (r *replaySource) SamplesInRange(id int64, from, to time.Time) []pgstat.QuerySample {
	var out []pgstat.QuerySample
	for _, s := range r.samples[id] {
		if !s.RecordedAt.Before(from) && !s.RecordedAt.After(to) {
			out = append(out, s)
		}
	}
	return out
}

func TestShadowTrackerRunsDueEvaluationsOnce(t *testing.T) {
	units, _, dep, _ := sim.World{Seed: 3, Queries: 40, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	src := &replaySource{samples: map[int64][]pgstat.QuerySample{}}
	for i, u := range units {
		id := int64(i + 1)
		src.ids = append(src.ids, id)
		for _, o := range u.Obs {
			src.samples[id] = append(src.samples[id], pgstat.QuerySample{
				QueryID: id, QueryText: u.Text, RecordedAt: o.At, IntervalKnown: true, IntervalComputed: true,
				IntervalCalls: o.Calls, IntervalTotalMs: o.MeanMs * float64(o.Calls),
			})
		}
	}
	reg := prometheus.NewRegistry()
	tr := newShadowTracker(shadow.New(src, collector.FingerprintQuery, attribution.Config{}),
		slog.New(slog.NewTextHandler(io.Discard, nil)), reg, "c")
	tr.add(dto.DeployEvent{ID: dep.ID, Timestamp: dep.At})

	tr.tick(dep.At.Add(5 * time.Minute)) // nothing due yet
	if n := len(tr.ev.Records()); n != 0 {
		t.Fatalf("evaluated early: %d", n)
	}
	tr.tick(dep.At.Add(46 * time.Minute)) // all eight due
	deadline := time.Now().Add(3 * time.Minute)
	for len(tr.ev.Records()) < 8 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(tr.ev.Records()); n != 8 {
		t.Fatalf("records = %d, want 8", n)
	}
	tr.tick(dep.At.Add(2 * time.Hour)) // nothing left
	time.Sleep(100 * time.Millisecond)
	if n := len(tr.ev.Records()); n != 8 {
		t.Fatalf("records = %d after the job finished, want 8", n)
	}
	mfs, _ := reg.Gather()
	var total float64
	for _, mf := range mfs {
		if mf.GetName() == "pg_regression_radar_attribution_shadow_decisions_total" {
			for _, m := range mf.GetMetric() {
				total += counterValue(m)
			}
		}
	}
	if total != 8 {
		t.Fatalf("metric total = %v, want 8", total)
	}
}

func counterValue(m *dto_prom.Metric) float64 { return m.GetCounter().GetValue() }

// fakeGitHub answers the calls the revert proposer makes and counts the pull
// requests it is asked to create.
type fakeGitHub struct {
	mu      sync.Mutex
	calls   int
	created int
}

func (f *fakeGitHub) handler(sha string) http.Handler {
	mux := http.NewServeMux()
	count := func() { f.mu.Lock(); f.calls++; f.mu.Unlock() }
	mux.HandleFunc("/search/issues", func(w http.ResponseWriter, r *http.Request) {
		count()
		_ = json.NewEncoder(w).Encode(map[string]int{"total_count": 0})
	})
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		count()
		_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/o/r/commits/", func(w http.ResponseWriter, r *http.Request) {
		count()
		_ = json.NewEncoder(w).Encode([]any{map[string]any{"number": 7, "node_id": "N", "merged_at": "2026-01-01T00:00:00Z", "merge_commit_sha": sha, "base": map[string]string{"ref": "main"}}})
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		count()
		f.mu.Lock()
		f.created++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"revertPullRequest": map[string]any{
			"revertPullRequest": map[string]any{"number": 8, "url": "https://github.com/o/r/pull/8"}}}})
	})
	return mux
}

func revertTracker(t *testing.T, enforce bool) (*shadowTracker, *fakeGitHub, dto.DeployEvent, *prometheus.Registry) {
	t.Helper()
	const sha = "0123456789abcdef0123456789abcdef01234567"
	units, _, dep, _ := sim.World{Seed: 3, Queries: 40, Deploy: sim.Effect{Fraction: 0.2, Factor: 1.6}}.Generate()
	src := &replaySource{samples: map[int64][]pgstat.QuerySample{}}
	for i, u := range units {
		id := int64(i + 1)
		src.ids = append(src.ids, id)
		for _, o := range u.Obs {
			src.samples[id] = append(src.samples[id], pgstat.QuerySample{
				QueryID: id, QueryText: u.Text, RecordedAt: o.At, IntervalKnown: true, IntervalComputed: true,
				IntervalCalls: o.Calls, IntervalTotalMs: o.MeanMs * float64(o.Calls),
			})
		}
	}
	gh := &fakeGitHub{}
	srv := httptest.NewServer(gh.handler(sha))
	t.Cleanup(srv.Close)
	prop, err := revert.New(revert.Config{Repository: "o/r", APIURL: srv.URL}, func(context.Context) (string, error) { return "t", nil })
	if err != nil {
		t.Fatal(err)
	}
	reg := prometheus.NewRegistry()
	ev := shadow.NewWithPolicy(src, collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: enforce})
	tr := newShadowTracker(ev, slog.New(slog.NewTextHandler(io.Discard, nil)), reg, "c")
	tr.withRevert(prop, enforce, false)
	tr.add(dto.DeployEvent{ID: dep.ID, App: "api", Namespace: "prod", Revision: sha, Timestamp: dep.At})
	return tr, gh, dto.DeployEvent{Timestamp: dep.At}, reg
}

func waitRecords(t *testing.T, tr *shadowTracker, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for len(tr.ev.Records()) < n && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	// let the goroutine finish its last proposal
	time.Sleep(200 * time.Millisecond)
	if len(tr.ev.Records()) < n {
		t.Fatalf("only %d evaluations", len(tr.ev.Records()))
	}
}

func TestEnforceOpensExactlyOneDraftRevert(t *testing.T) {
	tr, gh, at, reg := revertTracker(t, true)
	tr.tick(at.Timestamp.Add(46 * time.Minute))
	waitRecords(t, tr, 8)
	if gh.created != 1 {
		t.Fatalf("opened %d revert pull requests across 8 evaluations, want exactly 1", gh.created)
	}
	mfs, _ := reg.Gather()
	var opened float64
	for _, mf := range mfs {
		if mf.GetName() != "pg_regression_radar_attribution_revert_proposals_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			switch m.GetLabel()[len(m.GetLabel())-1].GetValue() {
			case "opened":
				opened += m.GetCounter().GetValue()
			}
		}
	}
	if opened != 1 {
		t.Fatalf("metric: opened=%v, want 1", opened)
	}
}

func TestShadowNeverCallsGitHub(t *testing.T) {
	tr, gh, at, _ := revertTracker(t, false)
	tr.tick(at.Timestamp.Add(46 * time.Minute))
	waitRecords(t, tr, 8)
	if gh.calls != 0 {
		t.Fatalf("shadow mode made %d calls to GitHub", gh.calls)
	}
}
