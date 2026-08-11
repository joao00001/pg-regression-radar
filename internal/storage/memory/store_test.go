package memory

import (
	"context"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/storage"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Compile-time interface assertions — these are the load-bearing contract
// checks: a parallel agent depends on this exact shape.
var (
	_ storage.SampleStore = (*SampleStore)(nil)
	_ storage.EventStore  = (*EventStore)(nil)
)

func TestSampleStore_AppendAndRange(t *testing.T) {
	ctx := context.Background()
	s := NewSampleStore()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	samples := []collector.QuerySample{
		{QueryID: 1, QueryText: "select 1", Calls: 1, MeanExecTimeMs: 1.0, RecordedAt: base},
		{QueryID: 1, QueryText: "select 1", Calls: 2, MeanExecTimeMs: 2.0, RecordedAt: base.Add(1 * time.Minute)},
		{QueryID: 1, QueryText: "select 1", Calls: 3, MeanExecTimeMs: 3.0, RecordedAt: base.Add(10 * time.Minute)},
		{QueryID: 2, QueryText: "select 2", Calls: 1, MeanExecTimeMs: 5.0, RecordedAt: base},
	}
	for _, s2 := range samples {
		if err := s.Append(ctx, s2); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	ids, err := s.AllQueryIDs(ctx)
	if err != nil {
		t.Fatalf("AllQueryIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 query ids, got %d (%v)", len(ids), ids)
	}

	got, err := s.SamplesInRange(ctx, 1, base, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("SamplesInRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 samples in range, got %d", len(got))
	}
	if got[0].RecordedAt.After(got[1].RecordedAt) {
		t.Fatalf("expected ascending order by RecordedAt")
	}

	// Query id with no samples in range returns empty, not an error.
	none, err := s.SamplesInRange(ctx, 99, base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("SamplesInRange: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 samples for unknown query id, got %d", len(none))
	}
}

func TestSampleStore_Prune(t *testing.T) {
	ctx := context.Background()
	s := NewSampleStore()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	must(s.Append(ctx, collector.QuerySample{QueryID: 1, RecordedAt: base}))
	must(s.Append(ctx, collector.QuerySample{QueryID: 1, RecordedAt: base.Add(time.Hour)}))
	must(s.Append(ctx, collector.QuerySample{QueryID: 2, RecordedAt: base}))

	if err := s.Prune(ctx, base.Add(30*time.Minute)); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	ids, _ := s.AllQueryIDs(ctx)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("expected only query id 1 to survive prune (fully-pruned ids should be dropped), got %v", ids)
	}

	remaining, _ := s.SamplesInRange(ctx, 1, base, base.Add(2*time.Hour))
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining sample for query 1, got %d", len(remaining))
	}
}

func TestEventStore_AddIsIdempotentUpsert(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	ev := v1alpha1.DeployEvent{ID: "ev-1", App: "checkout", Timestamp: base}
	if err := s.Add(ctx, ev); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Re-adding with the same ID but a different field should update in
	// place, not create a duplicate — matching Postgres's ON CONFLICT upsert.
	ev.Revision = "rev-2"
	if err := s.Add(ctx, ev); err != nil {
		t.Fatalf("Add (update): %v", err)
	}

	all, err := s.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 event after idempotent re-add, got %d", len(all))
	}
	if all[0].Revision != "rev-2" {
		t.Fatalf("expected updated revision, got %q", all[0].Revision)
	}
}

func TestEventStore_EventsInRange(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	events := []v1alpha1.DeployEvent{
		{ID: "a", Timestamp: base},
		{ID: "b", Timestamp: base.Add(10 * time.Minute)},
		{ID: "c", Timestamp: base.Add(time.Hour)},
	}
	for _, ev := range events {
		if err := s.Add(ctx, ev); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	got, err := s.EventsInRange(ctx, base, base.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("EventsInRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events in range, got %d", len(got))
	}
}
