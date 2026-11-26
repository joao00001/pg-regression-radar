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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/observe"
)

// historySchemaVersion is written with every row, so a later release that changes
// the shape of a stored case can read, migrate or skip the older ones.
const historySchemaVersion = 1

var historyStatements = []string{
	`CREATE SCHEMA IF NOT EXISTS ` + SchemaName,

	// One row per deploy the radar judged: the summary for lists, the whole case
	// (evaluations and ledger) for the detail. Query text is not stored unless the
	// manager was told it may be shown.
	`CREATE TABLE IF NOT EXISTS ` + SchemaName + `.attribution_deploys (
		id             TEXT PRIMARY KEY,
		namespace      TEXT NOT NULL,
		app            TEXT NOT NULL,
		deployed_at    TIMESTAMPTZ NOT NULL,
		updated_at     TIMESTAMPTZ NOT NULL,
		schema_version INT NOT NULL,
		summary        JSONB NOT NULL,
		body           JSONB NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_attribution_deploys_deployed_at
		ON ` + SchemaName + `.attribution_deploys (deployed_at)`,

	// The points the engine compared, downsampled, one row per query of a deploy:
	// a chart of a past deploy needs neither the collector window nor the log store.
	`CREATE TABLE IF NOT EXISTS ` + SchemaName + `.attribution_series (
		deploy_id      TEXT NOT NULL REFERENCES ` + SchemaName + `.attribution_deploys (id) ON DELETE CASCADE,
		unit           TEXT NOT NULL,
		schema_version INT NOT NULL,
		body           JSONB NOT NULL,
		PRIMARY KEY (deploy_id, unit)
	)`,

	// The ledger, for the range queries that do not start from a deploy.
	`CREATE TABLE IF NOT EXISTS ` + SchemaName + `.attribution_actions (
		deploy_id      TEXT NOT NULL REFERENCES ` + SchemaName + `.attribution_deploys (id) ON DELETE CASCADE,
		at             TIMESTAMPTZ NOT NULL,
		namespace      TEXT NOT NULL,
		action         TEXT NOT NULL,
		result         TEXT NOT NULL,
		schema_version INT NOT NULL,
		body           JSONB NOT NULL,
		PRIMARY KEY (deploy_id, at, action, result)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_attribution_actions_at
		ON ` + SchemaName + `.attribution_actions (at)`,
}

// History is observe.HistoryStore over Postgres, in the same regression_radar
// schema as the rest of the operator's state. It also satisfies storage.Pruner,
// so the existing prune loop applies the retention.
type History struct{ db *sql.DB }

var _ observe.HistoryStore = (*History)(nil)

// OpenHistory connects, creates the history tables if they are missing and
// returns the store. Like the rest of the schema, the DDL is idempotent and safe
// to run from several replicas at once.
func OpenHistory(ctx context.Context, dsn string) (*History, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		// The driver's error can quote the DSN; say only what failed.
		return nil, errors.New("storage/postgres: history: cannot open the connection (check the DSN's syntax)")
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("storage/postgres: history: cannot reach the database: %w", scrub(err))
	}
	h := &History{db: db}
	if err := h.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return h, nil
}

// NewHistory wraps an open database; for tests and callers that manage the pool.
func NewHistory(db *sql.DB) *History { return &History{db: db} }

// Close releases the connection pool.
func (h *History) Close() error { return h.db.Close() }

func (h *History) migrate(ctx context.Context) error {
	for _, q := range historyStatements {
		if _, err := h.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("storage/postgres: history migration: %w", scrub(err))
		}
	}
	return nil
}

// scrub keeps a connection string out of an error that may carry one.
func scrub(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(err.Error())
}

// Save implements observe.HistoryStore.
func (h *History) Save(ctx context.Context, c observe.Case) error {
	summary, err := json.Marshal(c.DeploySummary)
	if err != nil {
		return err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+SchemaName+`.attribution_deploys
		(id, namespace, app, deployed_at, updated_at, schema_version, summary, body)
		VALUES ($1,$2,$3,$4,now(),$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET updated_at = now(), summary = EXCLUDED.summary, body = EXCLUDED.body,
			schema_version = EXCLUDED.schema_version`,
		c.ID, c.Namespace, c.App, c.At.UTC(), historySchemaVersion, summary, body); err != nil {
		return err
	}
	for _, a := range c.Actions {
		ab, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+SchemaName+`.attribution_actions
			(deploy_id, at, namespace, action, result, schema_version, body) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT DO NOTHING`,
			c.ID, a.At.UTC(), a.Namespace, a.Action, a.Result, historySchemaVersion, ab); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveSeries implements observe.HistoryStore. The deploy must have been saved
// first; a series for an unknown deploy is refused by the foreign key.
func (h *History) SaveSeries(ctx context.Context, s observe.Series) error {
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = h.db.ExecContext(ctx, `INSERT INTO `+SchemaName+`.attribution_series (deploy_id, unit, schema_version, body)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (deploy_id, unit) DO UPDATE SET body = EXCLUDED.body, schema_version = EXCLUDED.schema_version`,
		s.DeployID, s.Key, historySchemaVersion, body)
	return err
}

// bounds turns a Range into SQL bounds; an open end becomes an infinite one.
func bounds(r observe.Range) (from, to any) {
	from, to = "-infinity", "infinity"
	if !r.From.IsZero() {
		from = r.From.UTC()
	}
	if !r.To.IsZero() {
		to = r.To.UTC()
	}
	return from, to
}

// Deploys implements observe.HistoryStore.
func (h *History) Deploys(ctx context.Context, r observe.Range) ([]observe.DeploySummary, error) {
	from, to := bounds(r)
	rows, err := h.db.QueryContext(ctx, `SELECT summary FROM `+SchemaName+`.attribution_deploys
		WHERE deployed_at >= $1::timestamptz AND deployed_at <= $2::timestamptz ORDER BY deployed_at DESC LIMIT 2000`, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []observe.DeploySummary
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var d observe.DeploySummary
		if err := json.Unmarshal(raw, &d); err != nil {
			continue // a row this release cannot read is skipped, not fatal
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Case implements observe.HistoryStore.
func (h *History) Case(ctx context.Context, id string) (observe.Case, bool, error) {
	var raw []byte
	err := h.db.QueryRowContext(ctx, `SELECT body FROM `+SchemaName+`.attribution_deploys WHERE id = $1`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return observe.Case{}, false, nil
	}
	if err != nil {
		return observe.Case{}, false, err
	}
	var c observe.Case
	if err := json.Unmarshal(raw, &c); err != nil {
		return observe.Case{}, false, fmt.Errorf("storage/postgres: history: stored case %q is unreadable: %w", id, err)
	}
	return c, true, nil
}

// Series implements observe.HistoryStore.
func (h *History) Series(ctx context.Context, id, unit string) (observe.Series, bool, error) {
	var raw []byte
	err := h.db.QueryRowContext(ctx, `SELECT body FROM `+SchemaName+`.attribution_series WHERE deploy_id = $1 AND unit = $2`, id, unit).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return observe.Series{}, false, nil
	}
	if err != nil {
		return observe.Series{}, false, err
	}
	var s observe.Series
	if err := json.Unmarshal(raw, &s); err != nil {
		return observe.Series{}, false, fmt.Errorf("storage/postgres: history: stored series is unreadable: %w", err)
	}
	return s, true, nil
}

// Actions implements observe.HistoryStore.
func (h *History) Actions(ctx context.Context, r observe.Range) ([]observe.Action, error) {
	from, to := bounds(r)
	rows, err := h.db.QueryContext(ctx, `SELECT body FROM `+SchemaName+`.attribution_actions
		WHERE at >= $1::timestamptz AND at <= $2::timestamptz ORDER BY at DESC LIMIT 5000`, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []observe.Action
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var a observe.Action
		if err := json.Unmarshal(raw, &a); err == nil {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

// Info implements observe.HistoryStore. Retention is the manager's setting; the
// store does not know it, so the caller fills it in.
func (h *History) Info(ctx context.Context) (observe.StoreInfo, error) {
	var since sql.NullTime
	if err := h.db.QueryRowContext(ctx, `SELECT min(deployed_at) FROM `+SchemaName+`.attribution_deploys`).Scan(&since); err != nil {
		return observe.StoreInfo{}, err
	}
	info := observe.StoreInfo{Kind: "postgres"}
	if since.Valid {
		t := since.Time.UTC()
		info.Since = &t
	}
	return info, nil
}

// Prune implements storage.Pruner: it deletes deploys, with their series and
// ledger, that were deployed before olderThan.
func (h *History) Prune(ctx context.Context, olderThan time.Time) error {
	_, err := h.db.ExecContext(ctx, `DELETE FROM `+SchemaName+`.attribution_deploys WHERE deployed_at < $1`, olderThan.UTC())
	return err
}
