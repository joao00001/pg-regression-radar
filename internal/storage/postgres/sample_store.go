package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
)

// SampleStore is a Postgres-backed storage.SampleStore.
type SampleStore struct {
	db *sql.DB
}

// NewSampleStore wraps db (already migrated — see Open) as a SampleStore.
func NewSampleStore(db *sql.DB) *SampleStore {
	return &SampleStore{db: db}
}

// Append implements storage.SampleStore.
func (s *SampleStore) Append(ctx context.Context, sample collector.QuerySample) error {
	const q = `
		INSERT INTO ` + SchemaName + `.query_samples
			(query_id, query_text, calls, total_exec_time_ms, mean_exec_time_ms, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6)`

	_, err := s.db.ExecContext(ctx, q,
		sample.QueryID,
		sample.QueryText,
		sample.Calls,
		sample.TotalExecTimeMs,
		sample.MeanExecTimeMs,
		sample.RecordedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("storage/postgres: append sample (query_id=%d): %w", sample.QueryID, err)
	}
	return nil
}

// SamplesInRange implements storage.SampleStore.
func (s *SampleStore) SamplesInRange(ctx context.Context, queryID int64, from, to time.Time) ([]collector.QuerySample, error) {
	const q = `
		SELECT query_id, query_text, calls, total_exec_time_ms, mean_exec_time_ms, recorded_at
		FROM ` + SchemaName + `.query_samples
		WHERE query_id = $1 AND recorded_at >= $2 AND recorded_at <= $3
		ORDER BY recorded_at ASC`

	rows, err := s.db.QueryContext(ctx, q, queryID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: samples in range (query_id=%d): %w", queryID, err)
	}
	defer rows.Close()

	var out []collector.QuerySample
	for rows.Next() {
		var sample collector.QuerySample
		if err := rows.Scan(
			&sample.QueryID,
			&sample.QueryText,
			&sample.Calls,
			&sample.TotalExecTimeMs,
			&sample.MeanExecTimeMs,
			&sample.RecordedAt,
		); err != nil {
			return nil, fmt.Errorf("storage/postgres: scan sample row: %w", err)
		}
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage/postgres: iterate sample rows: %w", err)
	}
	return out, nil
}

// AllQueryIDs implements storage.SampleStore.
func (s *SampleStore) AllQueryIDs(ctx context.Context) ([]int64, error) {
	const q = `SELECT DISTINCT query_id FROM ` + SchemaName + `.query_samples ORDER BY query_id`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("storage/postgres: all query ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("storage/postgres: scan query id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage/postgres: iterate query id rows: %w", err)
	}
	return ids, nil
}

// Prune implements storage.SampleStore.
func (s *SampleStore) Prune(ctx context.Context, olderThan time.Time) error {
	const q = `DELETE FROM ` + SchemaName + `.query_samples WHERE recorded_at < $1`

	if _, err := s.db.ExecContext(ctx, q, olderThan.UTC()); err != nil {
		return fmt.Errorf("storage/postgres: prune samples: %w", err)
	}
	return nil
}
