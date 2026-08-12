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

// Package postgres implements storage.SampleStore and storage.EventStore
// backed by a real Postgres database.
//
// # Which Postgres?
//
// The store can point at the SAME Postgres cluster the operator is already
// monitoring (reusing --dsn) or at a SEPARATE Postgres instance dedicated to
// pg-regression-radar's own state. Both are valid:
//
//   - Same cluster (the default via --state-dsn="" in cmd/operator): simplest
//     to operate — one connection string, one cluster to run. The trade-off
//     is that this tool's own write traffic (sample inserts, prune deletes)
//     now shows up in the very pg_stat_statements it is watching, and an
//     outage of the monitored cluster also takes down the tool's history.
//   - Separate instance: isolates pg-regression-radar's storage load and
//     availability from the cluster under observation, at the cost of
//     running (and paying for) a second Postgres. Point --state-dsn at it.
//
// # Schema management
//
// Everything lives in its own schema (regression_radar) so it never
// collides with application tables, and every DDL statement is
// CREATE ... IF NOT EXISTS. See the comment on Migrate for why this project
// hand-rolls idempotent DDL instead of pulling in a migration framework.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	// pq registers the "postgres" driver used by database/sql. This mirrors
	// internal/collector.Collector, which imports it the same way.
	_ "github.com/lib/pq"
)

// SchemaName is the dedicated Postgres schema all pg-regression-radar state
// lives under, so it's trivial to find ("\dn+ regression_radar") and
// trivial to drop entirely if the tool is uninstalled.
//
// Deliberately NOT prefixed "pg_": PostgreSQL reserves that literal prefix
// for its own system schemas and CREATE SCHEMA rejects it outright with
// "unacceptable schema name" (see the CREATE SCHEMA documentation) —
// discovered the hard way via a real-Postgres integration test failure,
// which is exactly the class of bug those tests exist to catch.
const SchemaName = "regression_radar"

// migrationStatements is applied, in order, every time Open (or Migrate) is
// called — including on every process restart, and even if multiple
// replicas race to run it concurrently.
//
// Migration strategy: hand-rolled idempotent DDL, not golang-migrate or
// pressly/goose.
//
// Why: those tools earn their keep once you have destructive/data-shaping
// migrations (renames, backfills, column type changes) that must run
// exactly once, in order, with a tracked version and rollback path — that's
// the right call for a system-of-record schema. This schema is neither: it
// is two append-mostly tables holding derived/observational data (query
// samples, deploy events) that can be reconstructed from source systems if
// ever wiped, evolves purely additively (new nullable column, new index),
// and ships inside a single small binary where adding a migration-runner
// dependency plus embedded .sql files plus a schema_migrations bookkeeping
// table is meaningfully more machinery than the problem warrants today.
// `CREATE TABLE/INDEX IF NOT EXISTS` plus, when a column is added later,
// `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` (supported since Postgres 9.6)
// covers every change this schema is likely to need, is trivially safe to
// re-run from N racing replicas on every boot, and needs no separate
// "run migrations" step in deploy tooling. If/when this schema needs a real
// breaking change (a rename, a backfill, a NOT NULL added to existing rows),
// that's the signal to introduce golang-migrate at that point rather than
// pre-emptively now.
var migrationStatements = []string{
	`CREATE SCHEMA IF NOT EXISTS ` + SchemaName,

	`CREATE TABLE IF NOT EXISTS ` + SchemaName + `.query_samples (
		id                  BIGSERIAL PRIMARY KEY,
		query_id            BIGINT NOT NULL,
		query_text          TEXT NOT NULL,
		calls               BIGINT NOT NULL,
		total_exec_time_ms  DOUBLE PRECISION NOT NULL,
		mean_exec_time_ms   DOUBLE PRECISION NOT NULL,
		recorded_at         TIMESTAMPTZ NOT NULL
	)`,

	// Primary access pattern: "samples for this queryid in this time window"
	// (correlation engine, per deploy, per query).
	`CREATE INDEX IF NOT EXISTS idx_query_samples_queryid_recorded_at
		ON ` + SchemaName + `.query_samples (query_id, recorded_at)`,

	// Secondary access pattern: "everything older than X" (Prune). A plain
	// per-queryid index can't serve a queryid-agnostic range delete
	// efficiently, hence the separate index.
	`CREATE INDEX IF NOT EXISTS idx_query_samples_recorded_at
		ON ` + SchemaName + `.query_samples (recorded_at)`,

	// NOTE for future extension: if/when collector.QuerySample grows a
	// Fingerprint field (tracked separately — pg_stat_statements' queryid is
	// not stable across planner/statistics changes on some Postgres
	// versions), add it here the same way any additive column ships:
	//
	//   ALTER TABLE ` + SchemaName + `.query_samples ADD COLUMN IF NOT EXISTS fingerprint TEXT
	//   CREATE INDEX IF NOT EXISTS idx_query_samples_fingerprint_recorded_at
	//       ON ` + SchemaName + `.query_samples (fingerprint, recorded_at)
	//
	// Deliberately not added yet: the field doesn't exist on QuerySample as
	// of this writing, and guessing its type/semantics ahead of the code
	// that populates it would risk a schema that has to be corrected later.

	`CREATE TABLE IF NOT EXISTS ` + SchemaName + `.deploy_events (
		id               TEXT PRIMARY KEY,
		source           TEXT NOT NULL DEFAULT '',
		app              TEXT NOT NULL DEFAULT '',
		cluster          TEXT NOT NULL DEFAULT '',
		namespace        TEXT NOT NULL DEFAULT '',
		revision         TEXT NOT NULL DEFAULT '',
		image_tag        TEXT NOT NULL DEFAULT '',
		event_timestamp  TIMESTAMPTZ NOT NULL
	)`,

	`CREATE INDEX IF NOT EXISTS idx_deploy_events_event_timestamp
		ON ` + SchemaName + `.deploy_events (event_timestamp)`,
}

// Migrate applies all schema DDL against db. It is idempotent and safe to
// call on every process startup, including concurrently from multiple
// replicas sharing the same database (each statement is independently
// IF-NOT-EXISTS; Postgres DDL is transactional and the worst case on a race
// is one replica briefly blocking on a lock, not corruption).
func Migrate(ctx context.Context, db *sql.DB) error {
	for _, stmt := range migrationStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("storage/postgres: migrate (%s): %w", firstLine(stmt), err)
		}
	}
	return nil
}

// Open opens a connection pool to dsn, verifies connectivity, applies schema
// migrations, and returns a ready-to-use *sql.DB for NewSampleStore /
// NewEventStore.
//
// The pool is sized conservatively (small number of connections): this
// database is a side-store for the operator's own state, not a
// high-throughput application workload, and — per the package doc — dsn may
// point at the very Postgres cluster being monitored, where we want to be a
// considerate, low-footprint client.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: open: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage/postgres: ping: %w", err)
	}

	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// firstLine returns the first non-empty line of a (possibly multi-line) SQL
// statement, used to keep migration error messages short and scannable.
func firstLine(sqlStmt string) string {
	for _, line := range strings.Split(sqlStmt, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return sqlStmt
}
