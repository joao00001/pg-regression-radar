//go:build integration

// Integration tests against a real Postgres. They are excluded from the
// default `go test ./...` run (build tag "integration") because they need a
// live database; run them explicitly:
//
//	docker run --rm -d --name pgrr-test -p 5432:5432 -e POSTGRES_PASSWORD=test postgres:16
//	export PGRR_TEST_DSN="postgres://postgres:test@localhost:5432/postgres?sslmode=disable"
//	go test -tags=integration ./internal/storage/...
//
// If PGRR_TEST_DSN is unset, these tests skip (rather than fail) so
// `go test -tags=integration ./...` remains safe to run in CI stages that
// don't provision a database.
package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func testDB(t *testing.T) *SampleStoreAndEventStore {
	t.Helper()
	dsn := os.Getenv("PGRR_TEST_DSN")
	if dsn == "" {
		t.Skip("PGRR_TEST_DSN not set; skipping Postgres integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		// Leave the schema behind between runs (Migrate is idempotent and
		// cheap), just drop the rows this test wrote.
		_, _ = db.Exec(`TRUNCATE ` + SchemaName + `.query_samples, ` + SchemaName + `.deploy_events`)
		db.Close()
	})

	return &SampleStoreAndEventStore{
		Samples: NewSampleStore(db),
		Events:  NewEventStore(db),
	}
}

// SampleStoreAndEventStore is a tiny test fixture bundling both stores
// backed by the same *sql.DB, mirroring how cmd/operator wires them.
type SampleStoreAndEventStore struct {
	Samples *SampleStore
	Events  *EventStore
}

func TestIntegration_Migrate_IsIdempotent(t *testing.T) {
	fx := testDB(t)
	ctx := context.Background()

	// Open() above already ran Migrate once; running it again must not error.
	db := fx.Samples.db
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate call failed (should be idempotent): %v", err)
	}
}

func TestIntegration_SampleStore_RoundTrip(t *testing.T) {
	fx := testDB(t)
	ctx := context.Background()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	samples := []collector.QuerySample{
		{QueryID: 42, QueryText: "select * from orders", Calls: 10, TotalExecTimeMs: 100, MeanExecTimeMs: 10, RecordedAt: base},
		{QueryID: 42, QueryText: "select * from orders", Calls: 20, TotalExecTimeMs: 400, MeanExecTimeMs: 20, RecordedAt: base.Add(5 * time.Minute)},
		{QueryID: 42, QueryText: "select * from orders", Calls: 30, TotalExecTimeMs: 900, MeanExecTimeMs: 30, RecordedAt: base.Add(2 * time.Hour)},
		{QueryID: 7, QueryText: "select 1", Calls: 1, TotalExecTimeMs: 1, MeanExecTimeMs: 1, RecordedAt: base},
	}
	for _, s := range samples {
		if err := fx.Samples.Append(ctx, s); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	ids, err := fx.Samples.AllQueryIDs(ctx)
	if err != nil {
		t.Fatalf("AllQueryIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 distinct query ids, got %d (%v)", len(ids), ids)
	}

	inRange, err := fx.Samples.SamplesInRange(ctx, 42, base, base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("SamplesInRange: %v", err)
	}
	if len(inRange) != 2 {
		t.Fatalf("expected 2 samples in range, got %d", len(inRange))
	}
	if inRange[0].MeanExecTimeMs != 10 || inRange[1].MeanExecTimeMs != 20 {
		t.Fatalf("unexpected ordering/content: %+v", inRange)
	}
	if inRange[0].QueryText != "select * from orders" {
		t.Fatalf("query text round-trip failed: %q", inRange[0].QueryText)
	}

	if err := fx.Samples.Prune(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	remaining, err := fx.Samples.SamplesInRange(ctx, 42, base, base.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("SamplesInRange after prune: %v", err)
	}
	if len(remaining) != 1 || remaining[0].RecordedAt.Before(base.Add(time.Hour)) {
		t.Fatalf("expected only the post-cutoff sample to survive prune, got %+v", remaining)
	}
}

func TestIntegration_EventStore_RoundTrip(t *testing.T) {
	fx := testDB(t)
	ctx := context.Background()

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	ev := v1alpha1.DeployEvent{
		ID: "argocd-checkout-1", Source: "argocd", App: "checkout", Cluster: "prod",
		Namespace: "production", Revision: "abc123", ImageTag: "checkout:v9", Timestamp: base,
	}
	if err := fx.Events.Add(ctx, ev); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Re-add with a changed field: must upsert, not duplicate.
	ev.Revision = "def456"
	if err := fx.Events.Add(ctx, ev); err != nil {
		t.Fatalf("Add (upsert): %v", err)
	}

	all, err := fx.Events.All(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 event after idempotent upsert, got %d", len(all))
	}
	if all[0].Revision != "def456" {
		t.Fatalf("expected upserted revision, got %q", all[0].Revision)
	}

	inRange, err := fx.Events.EventsInRange(ctx, base.Add(-time.Minute), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("EventsInRange: %v", err)
	}
	if len(inRange) != 1 {
		t.Fatalf("expected 1 event in range, got %d", len(inRange))
	}

	outOfRange, err := fx.Events.EventsInRange(ctx, base.Add(time.Hour), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("EventsInRange (empty): %v", err)
	}
	if len(outOfRange) != 0 {
		t.Fatalf("expected 0 events outside range, got %d", len(outOfRange))
	}

	if err := fx.Events.Prune(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	remaining, err := fx.Events.All(ctx)
	if err != nil {
		t.Fatalf("All after prune: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected event pruned (recorded before cutoff), got %d remaining", len(remaining))
	}
}
