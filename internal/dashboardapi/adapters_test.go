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

package dashboardapi

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// newTestCollector builds a Collector without dialing a real database:
// sql.Open only validates the DSN string and lazily establishes connections
// on first use, so this is safe to drive purely through Backfill/
// SamplesInRange/AllQueryIDs.
func newTestCollector(t *testing.T) *collector.Collector {
	t.Helper()
	col, err := collector.New(collector.Config{DSN: "postgres://unused/unused?sslmode=disable"}, slog.Default(), prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("collector.New: %v", err)
	}
	return col
}

func TestCollectorSampleReader_ReadsCollectorState(t *testing.T) {
	col := newTestCollector(t)
	now := time.Now().UTC()
	col.Backfill([]collector.QuerySample{
		{QueryID: 42, QueryText: "SELECT 1", Calls: 5, MeanExecTimeMs: 3.5, RecordedAt: now},
	})

	reader := NewCollectorSampleReader(col)

	ids, err := reader.AllQueryIDs(context.Background())
	if err != nil {
		t.Fatalf("AllQueryIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != 42 {
		t.Fatalf("AllQueryIDs = %v, want [42]", ids)
	}

	samples, err := reader.SamplesInRange(context.Background(), 42, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("SamplesInRange: %v", err)
	}
	if len(samples) != 1 || samples[0].Calls != 5 {
		t.Fatalf("SamplesInRange = %+v, want one sample with Calls=5", samples)
	}
}

func TestIngesterEventReader_ReadsStoreState(t *testing.T) {
	store := ingester.NewStore()
	now := time.Now().UTC()
	store.Add(v1alpha1.DeployEvent{ID: "dep-1", Timestamp: now})

	reader := NewIngesterEventReader(store)

	events, err := reader.EventsInRange(context.Background(), now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("EventsInRange: %v", err)
	}
	if len(events) != 1 || events[0].ID != "dep-1" {
		t.Fatalf("EventsInRange = %+v, want one event with ID=dep-1", events)
	}
}
