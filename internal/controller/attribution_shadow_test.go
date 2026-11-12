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
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto_prom "github.com/prometheus/client_model/go"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
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

	tr.tick(dep.At.Add(10 * time.Minute)) // nothing due yet
	if n := len(tr.ev.Records()); n != 0 {
		t.Fatalf("evaluated early: %d", n)
	}
	tr.tick(dep.At.Add(41 * time.Minute)) // all three due
	deadline := time.Now().Add(3 * time.Minute)
	for len(tr.ev.Records()) < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if n := len(tr.ev.Records()); n != 3 {
		t.Fatalf("records = %d, want 3", n)
	}
	tr.tick(dep.At.Add(2 * time.Hour)) // nothing left
	time.Sleep(100 * time.Millisecond)
	if n := len(tr.ev.Records()); n != 3 {
		t.Fatalf("records = %d after the job finished, want 3", n)
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
	if total != 3 {
		t.Fatalf("metric total = %v, want 3", total)
	}
}

func counterValue(m *dto_prom.Metric) float64 { return m.GetCounter().GetValue() }
