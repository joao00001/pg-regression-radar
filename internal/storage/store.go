// Package storage defines persistence-agnostic interfaces for the two pieces
// of state pg-regression-radar keeps at runtime:
//
//   - SampleStore: pg_stat_statements observations, mirroring what
//     internal/collector.Collector keeps in an in-memory map today.
//   - EventStore: normalised deploy events, mirroring what internal/ingester.Store
//     keeps in an in-memory slice today.
//
// Both interfaces are designed as drop-in replacements for that in-memory
// state: same responsibilities, same query shapes, just backed by something
// that survives a pod restart and can be shared across replicas. See
// internal/storage/memory for a reference in-memory implementation (useful in
// tests, or anywhere a real database is unnecessary) and
// internal/storage/postgres for the Postgres-backed implementation used in
// production.
//
// IMPORTANT — this package makes STATE durable, not the SYSTEM safe for
// multiple replicas. If you point two operator replicas at the same
// SampleStore/EventStore backend today, both will still scrape
// pg_stat_statements and evaluate correlations independently, which means
// duplicate alerts. Preventing that requires leader election (only one
// replica actively scraping/analysing at a time), which is being handled
// separately as part of the controller-runtime operator work. Nothing in
// this package should be read as "safe to run N replicas unattended" until
// that lands.
package storage

import (
	"context"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// SampleStore persists pg_stat_statements observations collected by
// internal/collector.Collector so they survive process restarts and can be
// shared across replicas.
//
// All methods take a context so implementations backed by a network
// round-trip (e.g. Postgres) can respect caller cancellation/timeouts; the
// in-memory implementation simply ignores it.
type SampleStore interface {
	// Append stores a single QuerySample observation. Implementations should
	// treat this as an insert-only append — samples are immutable observations,
	// never updated in place.
	Append(ctx context.Context, s collector.QuerySample) error

	// SamplesInRange returns every sample for queryID whose RecordedAt falls
	// within [from, to] (inclusive), ordered by RecordedAt ascending.
	SamplesInRange(ctx context.Context, queryID int64, from, to time.Time) ([]collector.QuerySample, error)

	// AllQueryIDs returns the distinct set of query IDs known to the store.
	AllQueryIDs(ctx context.Context) ([]int64, error)

	// Prune deletes samples recorded strictly before olderThan. It is safe to
	// call repeatedly and concurrently with Append/SamplesInRange — this is
	// how retention is enforced (see RunPruneLoop).
	Prune(ctx context.Context, olderThan time.Time) error
}

// EventStore persists normalised deploy events ingested by internal/ingester.
type EventStore interface {
	// Add stores ev. Webhook senders retry on timeout/5xx, so implementations
	// should treat re-adding an event with the same ID as an idempotent
	// upsert rather than erroring or creating a duplicate.
	Add(ctx context.Context, ev v1alpha1.DeployEvent) error

	// EventsInRange returns all events whose Timestamp falls within [from, to]
	// (inclusive).
	EventsInRange(ctx context.Context, from, to time.Time) ([]v1alpha1.DeployEvent, error)

	// All returns every stored event.
	All(ctx context.Context) ([]v1alpha1.DeployEvent, error)
}
