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

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// EventStore is a Postgres-backed storage.EventStore.
type EventStore struct {
	db *sql.DB
}

// NewEventStore wraps db (already migrated — see Open) as an EventStore.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db}
}

// Add implements storage.EventStore as an upsert keyed on ev.ID, so retried
// webhook deliveries (ArgoCD/Flux/Argo Rollouts all retry on non-2xx) don't
// create duplicate rows.
func (s *EventStore) Add(ctx context.Context, ev v1alpha1.DeployEvent) error {
	const q = `
		INSERT INTO ` + SchemaName + `.deploy_events
			(id, source, app, cluster, namespace, revision, image_tag, event_timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			source          = EXCLUDED.source,
			app             = EXCLUDED.app,
			cluster         = EXCLUDED.cluster,
			namespace       = EXCLUDED.namespace,
			revision        = EXCLUDED.revision,
			image_tag       = EXCLUDED.image_tag,
			event_timestamp = EXCLUDED.event_timestamp`

	_, err := s.db.ExecContext(ctx, q,
		ev.ID, ev.Source, ev.App, ev.Cluster, ev.Namespace, ev.Revision, ev.ImageTag, ev.Timestamp.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage/postgres: add event (id=%s): %w", ev.ID, err)
	}
	return nil
}

// EventsInRange implements storage.EventStore.
func (s *EventStore) EventsInRange(ctx context.Context, from, to time.Time) ([]v1alpha1.DeployEvent, error) {
	const q = `
		SELECT id, source, app, cluster, namespace, revision, image_tag, event_timestamp
		FROM ` + SchemaName + `.deploy_events
		WHERE event_timestamp >= $1 AND event_timestamp <= $2
		ORDER BY event_timestamp ASC`

	rows, err := s.db.QueryContext(ctx, q, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: events in range: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanDeployEvents(rows)
}

// All implements storage.EventStore.
func (s *EventStore) All(ctx context.Context) ([]v1alpha1.DeployEvent, error) {
	const q = `
		SELECT id, source, app, cluster, namespace, revision, image_tag, event_timestamp
		FROM ` + SchemaName + `.deploy_events
		ORDER BY event_timestamp ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: all events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanDeployEvents(rows)
}

// Prune deletes deploy events recorded strictly before olderThan.
//
// This is intentionally NOT part of the storage.EventStore interface (that
// contract is shared with a parallel workstream and must not gain methods
// unilaterally) — it's an extra capability of the concrete Postgres type,
// used by cmd/operator via storage.RunPruneLoop, which only requires
// structural conformance to storage.Pruner.
func (s *EventStore) Prune(ctx context.Context, olderThan time.Time) error {
	const q = `DELETE FROM ` + SchemaName + `.deploy_events WHERE event_timestamp < $1`

	if _, err := s.db.ExecContext(ctx, q, olderThan.UTC()); err != nil {
		return fmt.Errorf("storage/postgres: prune events: %w", err)
	}
	return nil
}

func scanDeployEvents(rows *sql.Rows) ([]v1alpha1.DeployEvent, error) {
	var out []v1alpha1.DeployEvent
	for rows.Next() {
		var ev v1alpha1.DeployEvent
		if err := rows.Scan(
			&ev.ID, &ev.Source, &ev.App, &ev.Cluster, &ev.Namespace, &ev.Revision, &ev.ImageTag, &ev.Timestamp,
		); err != nil {
			return nil, fmt.Errorf("storage/postgres: scan event row: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage/postgres: iterate event rows: %w", err)
	}
	return out, nil
}
