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

//go:build integration

package postgres

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/collector"
)

func TestIntegration_SampleStore_PreservesAggregatedIntervals(t *testing.T) {
	fx := testDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Minute)
	samples := []collector.QuerySample{
		{
			QueryID: 42, QueryText: "select 1", Calls: 1050,
			TotalExecTimeMs: 2100, MeanExecTimeMs: 2, RecordedAt: base,
			IntervalComputed: true,
		},
		{
			// Role A grows from 1000 to 1100 calls (2000 to 2200 ms),
			// while role B resets from 50 calls/100 ms to 2 calls/8 ms.
			// Differencing the aggregate would give 52 calls/108 ms,
			// but the independently derived intervals sum to 102/208.
			QueryID: 42, QueryText: "select 1", Calls: 1102,
			TotalExecTimeMs: 2208, MeanExecTimeMs: 2208.0 / 1102,
			RecordedAt:       base.Add(time.Minute),
			IntervalComputed: true, IntervalKnown: true,
			IntervalCalls: 102, IntervalTotalMs: 208, IntervalMeanMs: 208.0 / 102,
			ResetDetected: true,
		},
		{
			// A changed row set has no complete interval. That decision
			// must survive a restart instead of becoming a cumulative delta.
			QueryID: 42, QueryText: "select 1", Calls: 1200,
			TotalExecTimeMs: 2400, MeanExecTimeMs: 2,
			RecordedAt: base.Add(2 * time.Minute), IntervalComputed: true,
		},
	}
	for _, sample := range samples {
		if err := fx.Samples.Append(ctx, sample); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	loaded, err := fx.Samples.SamplesInRange(ctx, 42, base, base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("SamplesInRange: %v", err)
	}
	if len(loaded) != len(samples) {
		t.Fatalf("loaded %d samples, want %d", len(loaded), len(samples))
	}
	for i := range loaded {
		loaded[i].RecordedAt = loaded[i].RecordedAt.UTC()
		if !reflect.DeepEqual(loaded[i], samples[i]) {
			t.Fatalf("sample %d changed during persistence: got %+v, want %+v", i, loaded[i], samples[i])
		}
	}

	c, err := collector.New(collector.Config{}, nil, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("new collector: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.Backfill(loaded)
	restored := c.SamplesInRange(42, base, base.Add(3*time.Minute))
	if len(restored) != len(samples) {
		t.Fatalf("restored %d samples, want %d", len(restored), len(samples))
	}
	for i := range restored {
		// Backfill computes this index metadata; the timing fields must
		// remain exactly as the live collector recorded them.
		samples[i].Fingerprint = collector.FingerprintQuery(samples[i].QueryText)
		if !reflect.DeepEqual(restored[i], samples[i]) {
			t.Fatalf("sample %d changed during backfill: got %+v, want %+v", i, restored[i], samples[i])
		}
	}
}

func TestIntegration_SampleStore_LegacyInsertKeepsUnknownProvenance(t *testing.T) {
	fx := testDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	// Older binaries omit the new columns. Their rows must remain readable
	// and distinguishable from a deliberately unknown computed interval.
	_, err := fx.Samples.db.ExecContext(ctx, `INSERT INTO `+SchemaName+`.query_samples
		(query_id, query_text, calls, total_exec_time_ms, mean_exec_time_ms, recorded_at)
		VALUES (7, 'select 1', 10, 20, 2, $1)`, base)
	if err != nil {
		t.Fatalf("legacy insert: %v", err)
	}
	loaded, err := fx.Samples.SamplesInRange(ctx, 7, base, base)
	if err != nil {
		t.Fatalf("load legacy row: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d legacy samples, want 1", len(loaded))
	}
	sample := loaded[0]
	if sample.IntervalComputed || sample.IntervalKnown || sample.ResetDetected ||
		sample.IntervalCalls != 0 || sample.IntervalTotalMs != 0 || sample.IntervalMeanMs != 0 {
		t.Fatalf("legacy row unexpectedly acquired interval data: %+v", sample)
	}
}

func TestIntegration_Migrate_AddsIntervalsToLegacySamples(t *testing.T) {
	fx := testDB(t)
	ctx := context.Background()
	conn, err := fx.Samples.db.Conn(ctx)
	if err != nil {
		t.Fatalf("open migration connection: %v", err)
	}
	defer conn.Close()
	// Use a session-local table with the pre-migration shape so this test
	// does not remove columns from a database shared by integration suites.
	_, err = conn.ExecContext(ctx, `CREATE TEMP TABLE legacy_query_samples (
		query_id BIGINT NOT NULL, query_text TEXT NOT NULL, calls BIGINT NOT NULL,
		total_exec_time_ms DOUBLE PRECISION NOT NULL,
		mean_exec_time_ms DOUBLE PRECISION NOT NULL, recorded_at TIMESTAMPTZ NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), `DROP TABLE pg_temp.legacy_query_samples`) }()
	if _, err := conn.ExecContext(ctx, `INSERT INTO legacy_query_samples VALUES (42, 'select 1', 10, 20, 2, now())`); err != nil {
		t.Fatalf("insert pre-migration row: %v", err)
	}
	var migration string
	for _, stmt := range migrationStatements {
		if strings.HasPrefix(stmt, "ALTER TABLE "+SchemaName+".query_samples") {
			migration = strings.ReplaceAll(stmt, SchemaName+".query_samples", "pg_temp.legacy_query_samples")
			break
		}
	}
	if migration == "" {
		t.Fatal("missing additive query_samples interval migration")
	}
	for i := 0; i < 2; i++ {
		if _, err := conn.ExecContext(ctx, migration); err != nil {
			t.Fatalf("migration pass %d: %v", i+1, err)
		}
	}
	var calls, intervalCalls int64
	var computed, known, reset bool
	var total, intervalTotal, intervalMean float64
	err = conn.QueryRowContext(ctx, `SELECT calls, total_exec_time_ms,
		interval_computed, interval_known, interval_calls, interval_total_ms,
		interval_mean_ms, reset_detected FROM legacy_query_samples WHERE query_id = 42`).Scan(
		&calls, &total, &computed, &known, &intervalCalls, &intervalTotal, &intervalMean, &reset)
	if err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if calls != 10 || total != 20 || computed || known || reset || intervalCalls != 0 || intervalTotal != 0 || intervalMean != 0 {
		t.Fatalf("legacy row changed or acquired computed intervals: calls=%d total=%g computed=%v known=%v reset=%v interval=%d/%g/%g",
			calls, total, computed, known, reset, intervalCalls, intervalTotal, intervalMean)
	}
}
