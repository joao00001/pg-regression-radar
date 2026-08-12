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

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// newTestCollector builds a Collector without dialing a real database.
// sql.Open only validates the DSN string and lazily establishes connections
// on first use, so it is safe to construct a Collector this way and drive it
// purely through ingestSample/pruneAndUpdateMetrics/SamplesInRange/
// AllQueryIDs, none of which touch c.db.
func newTestCollector(t *testing.T, cfg Config) *Collector {
	t.Helper()
	if cfg.DSN == "" {
		cfg.DSN = "postgres://unused/unused?sslmode=disable"
	}
	reg := prometheus.NewRegistry()
	col, err := New(cfg, slog.Default(), reg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = col.db.Close()
	})
	return col
}

// ----- FingerprintQuery -----

func TestFingerprintQuery_NormalizesLiteralsSpacingAndCase(t *testing.T) {
	a := FingerprintQuery("SELECT * FROM users WHERE id = 42 AND name = 'Alice'")
	b := FingerprintQuery("select   *\nfrom users\nwhere id=99   and name='Bob'")

	if a != b {
		t.Fatalf("expected same fingerprint for differently-cased/spaced/literal queries, got %q vs %q", a, b)
	}
}

func TestFingerprintQuery_IgnoresSQLComments(t *testing.T) {
	a := FingerprintQuery("SELECT id FROM orders WHERE status = 'shipped'")
	b := FingerprintQuery("SELECT id FROM orders -- only shipped orders\nWHERE status = 'pending'")
	c := FingerprintQuery("SELECT id FROM orders /* block\ncomment */ WHERE status = 'pending'")

	if b != c {
		t.Fatalf("line comment and block comment variants should fingerprint the same: %q vs %q", b, c)
	}
	if a != b {
		t.Fatalf("comments should be stripped before hashing: %q vs %q", a, b)
	}
}

func TestFingerprintQuery_HandlesEscapedAndDoubledQuotesInLiterals(t *testing.T) {
	// Standard SQL doubled-quote escaping: 'it''s' is a single string literal.
	// A naive scanner that stops at the first quote would see this as two
	// short literals plus stray SQL ("s here") and diverge from the
	// same-shape query below.
	withDoubledQuote := FingerprintQuery("SELECT * FROM notes WHERE body = 'it''s here'")
	otherLiteral := FingerprintQuery("SELECT * FROM notes WHERE body = 'anything else entirely'")

	if withDoubledQuote != otherLiteral {
		t.Fatalf("expected literal contents (including doubled quotes) to be irrelevant to the fingerprint: %q vs %q", withDoubledQuote, otherLiteral)
	}

	// A comment marker embedded inside a string literal must not be treated
	// as starting a real comment (which would otherwise swallow the rest of
	// the query).
	withDashesInLiteral := FingerprintQuery("SELECT * FROM notes WHERE body = 'contains -- not a comment' AND id = 1")
	plainEquivalent := FingerprintQuery("SELECT * FROM notes WHERE body = 'xyz' AND id = 2")
	if withDashesInLiteral != plainEquivalent {
		t.Fatalf("a '--' inside a string literal must not be treated as a line comment: %q vs %q", withDashesInLiteral, plainEquivalent)
	}
}

func TestFingerprintQuery_StructurallyDifferentQueriesDiffer(t *testing.T) {
	queries := []string{
		"SELECT * FROM users WHERE id = 1",
		"SELECT * FROM accounts WHERE id = 1",
		"UPDATE users SET name = 'x' WHERE id = 1",
		"SELECT id, name FROM users WHERE id = 1",
		"SELECT * FROM users WHERE id = 1 AND active = true",
	}

	seen := make(map[string]string)
	for _, q := range queries {
		fp := FingerprintQuery(q)
		if prev, ok := seen[fp]; ok {
			t.Fatalf("expected distinct fingerprints, but %q and %q both hashed to %q", prev, q, fp)
		}
		seen[fp] = q
	}
}

func TestFingerprintQuery_CollapsesInListsOfDifferentLengths(t *testing.T) {
	a := FingerprintQuery("SELECT * FROM t WHERE id IN (1, 2, 3)")
	b := FingerprintQuery("SELECT * FROM t WHERE id IN (1, 2, 3, 4, 5, 6, 7)")

	if a != b {
		t.Fatalf("expected IN-lists of different lengths to fingerprint the same, got %q vs %q", a, b)
	}
}

// ----- retention / pruning -----

func TestCollector_PrunesSamplesOlderThanRetention(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	old := base.Add(-2 * time.Hour)
	recent := base.Add(-10 * time.Minute)

	col.ingestSample(old, 1, "SELECT 1", 1, 1, 1)
	col.ingestSample(recent, 1, "SELECT 1", 2, 2, 2)
	col.pruneAndUpdateMetrics(base)

	got := col.SamplesInRange(1, base.Add(-3*time.Hour), base)
	if len(got) != 1 {
		t.Fatalf("expected only the recent sample to survive pruning, got %d samples: %+v", len(got), got)
	}
	if got[0].RecordedAt != recent {
		t.Fatalf("expected surviving sample to be the recent one, got %v", got[0].RecordedAt)
	}
}

func TestCollector_PruneRemovesFullyEmptyQueryIDKeys(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	old := base.Add(-2 * time.Hour)

	col.ingestSample(old, 42, "SELECT 1", 1, 1, 1)
	col.pruneAndUpdateMetrics(base)

	ids := col.AllQueryIDs()
	for _, id := range ids {
		if id == 42 {
			t.Fatalf("expected queryid 42 to be fully evicted once all its samples aged out, but AllQueryIDs still reports it: %v", ids)
		}
	}

	col.mu.RLock()
	_, stillIndexed := col.queryFingerprint[42]
	col.mu.RUnlock()
	if stillIndexed {
		t.Fatalf("expected fingerprint bookkeeping for queryid 42 to be cleaned up alongside its samples")
	}
}

func TestCollector_MemoryGrowthBoundedUnderContinuousScraping(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: 5 * time.Minute})

	start := time.Now().UTC()
	// Simulate ~1 scrape/second for a long time; without retention this
	// would leave one ever-growing slice per queryid.
	const scrapes = 2000
	const queryIDs = 20
	for i := 0; i < scrapes; i++ {
		now := start.Add(time.Duration(i) * time.Second)
		for q := int64(0); q < queryIDs; q++ {
			col.ingestSample(now, q, fmt.Sprintf("SELECT * FROM t%d WHERE id = %d", q, i), int64(i), float64(i), float64(i))
		}
		col.pruneAndUpdateMetrics(now)
	}

	col.mu.RLock()
	defer col.mu.RUnlock()

	if len(col.samples) > queryIDs {
		t.Fatalf("expected at most %d tracked queryids, got %d", queryIDs, len(col.samples))
	}
	// Retention is 5 minutes at ~1 sample/sec/queryid, so each slice should
	// hold roughly 300 samples, not the ~2000 that continuous unbounded
	// accumulation over "scrapes" iterations would produce.
	for qid, samples := range col.samples {
		if len(samples) > 400 {
			t.Fatalf("queryid %d retained %d samples, expected pruning to bound it well under the %d total scrapes performed", qid, len(samples), scrapes)
		}
	}
}

// ----- fingerprint-based queryid-rotation fallback -----

func TestCollector_SamplesInRange_ReconnectsAcrossQueryIDRotation(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	const queryText = "SELECT * FROM orders WHERE customer_id = 7"

	// Old queryid accumulates a healthy history before a mid-window rotation
	// (e.g. a deploy dropped/recreated a referenced table, which the
	// PostgreSQL docs call out as a documented cause of queryid changing for
	// an unchanged query).
	for i := 0; i < 10; i++ {
		col.ingestSample(base.Add(time.Duration(i)*time.Minute), 100, queryText, int64(i), float64(i), float64(i))
	}
	// Deploy happens here; pg_stat_statements starts a new queryid for the
	// very same statement text.
	for i := 10; i < 14; i++ {
		col.ingestSample(base.Add(time.Duration(i)*time.Minute), 200, queryText, int64(i), float64(i), float64(i))
	}
	col.pruneAndUpdateMetrics(base.Add(20 * time.Minute))

	from := base
	to := base.Add(20 * time.Minute)

	// Querying the *new* queryid alone would, without the fallback, only see
	// the 4 post-rotation samples; the fingerprint fallback should also pull
	// in the pre-rotation samples recorded under the old queryid.
	got := col.SamplesInRange(200, from, to)
	if len(got) != 14 {
		t.Fatalf("expected fingerprint fallback to merge old+new queryid samples (14 total), got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].RecordedAt.Before(got[i-1].RecordedAt) {
			t.Fatalf("expected merged samples sorted chronologically, got out-of-order at index %d: %+v", i, got)
		}
	}

	// Querying the *old* queryid directly already has 10 samples in range,
	// clearing fingerprintFallbackMinSamples on its own, so no merge is
	// attempted for it - the fallback is deliberately one-sided here,
	// engaging only for the sparse bucket that actually needs it.
	got = col.SamplesInRange(100, from, to)
	if len(got) != 10 {
		t.Fatalf("expected old queryid's own sufficient direct data with no fallback merge, got %d", len(got))
	}
}

func TestCollector_SamplesInRange_NoFallbackWhenDirectDataIsSufficient(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	// Two genuinely different queries that happen to share nothing but an
	// artificially forced fingerprint collision would be a problem; here we
	// simply verify that when a queryid already has ample direct data, an
	// unrelated queryid's samples (different fingerprint) are never merged
	// in, confirming the fallback path isn't taken indiscriminately.
	for i := 0; i < 10; i++ {
		col.ingestSample(base.Add(time.Duration(i)*time.Minute), 1, "SELECT * FROM a WHERE x = 1", int64(i), float64(i), float64(i))
	}
	for i := 0; i < 10; i++ {
		col.ingestSample(base.Add(time.Duration(i)*time.Minute), 2, "SELECT * FROM b WHERE y = 2", int64(i), float64(i), float64(i))
	}
	col.pruneAndUpdateMetrics(base.Add(time.Hour))

	got := col.SamplesInRange(1, base, base.Add(time.Hour))
	if len(got) != 10 {
		t.Fatalf("expected only queryid 1's own 10 samples, got %d", len(got))
	}
	for _, s := range got {
		if s.QueryID != 1 {
			t.Fatalf("unexpected sample from unrelated queryid %d leaked into result", s.QueryID)
		}
	}
}

func TestCollector_SamplesInRange_NoFallbackWithoutSharedFingerprint(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	col.ingestSample(base, 1, "SELECT * FROM only_here", 1, 1, 1)

	got := col.SamplesInRange(1, base.Add(-time.Minute), base.Add(time.Minute))
	if len(got) != 1 {
		t.Fatalf("expected the single direct sample with no fallback siblings, got %d", len(got))
	}

	// A queryid never seen at all should simply return nothing, not panic.
	got = col.SamplesInRange(999, base.Add(-time.Minute), base.Add(time.Minute))
	if len(got) != 0 {
		t.Fatalf("expected no samples for an unknown queryid, got %d", len(got))
	}
}

// ----- Backfill -----

func TestCollector_Backfill_SeedsSamplesAndRespectsRetention(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	old := base.Add(-2 * time.Hour) // outside RetentionDuration relative to "now"
	recent := base.Add(-10 * time.Minute)

	col.Backfill([]QuerySample{
		{QueryID: 1, QueryText: "SELECT 1", Calls: 1, MeanExecTimeMs: 5, RecordedAt: old},
		{QueryID: 1, QueryText: "SELECT 1", Calls: 2, MeanExecTimeMs: 6, RecordedAt: recent},
	})

	// The old sample should already be pruned since Backfill runs pruneLocked
	// against the current time, exactly like a live scrape would.
	got := col.SamplesInRange(1, base.Add(-3*time.Hour), base)
	if len(got) != 1 {
		t.Fatalf("expected only the in-retention sample to survive Backfill's pruning, got %d: %+v", len(got), got)
	}
	if got[0].RecordedAt != recent {
		t.Fatalf("expected surviving sample to be the recent one, got %v", got[0].RecordedAt)
	}
}

func TestCollector_Backfill_ComputesMissingFingerprint(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	now := time.Now().UTC()
	// storage.SampleStore's schema has no fingerprint column (see
	// docs/persistence.md), so samples loaded from it arrive with an empty
	// Fingerprint — Backfill must compute it, the same way ingestSample does
	// for a live scrape, so the queryid-rotation fallback still works for
	// backfilled history.
	col.Backfill([]QuerySample{
		{QueryID: 1, QueryText: "SELECT * FROM t WHERE id = 1", Calls: 1, MeanExecTimeMs: 5, RecordedAt: now},
	})

	col.mu.RLock()
	fp, ok := col.queryFingerprint[1]
	col.mu.RUnlock()
	if !ok || fp == "" {
		t.Fatal("expected Backfill to compute and index a fingerprint for a sample with no Fingerprint set")
	}
	want := FingerprintQuery("SELECT * FROM t WHERE id = 1")
	if fp != want {
		t.Fatalf("expected fingerprint %q, got %q", want, fp)
	}
}

func TestCollector_Backfill_SortsOutOfOrderSamples(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	base := time.Now().UTC()
	// Deliberately out of chronological order — storage.SampleStore makes no
	// ordering promise across a bulk load built by iterating several
	// queryids and concatenating.
	col.Backfill([]QuerySample{
		{QueryID: 1, QueryText: "SELECT 1", RecordedAt: base.Add(-1 * time.Minute)},
		{QueryID: 1, QueryText: "SELECT 1", RecordedAt: base.Add(-10 * time.Minute)},
		{QueryID: 1, QueryText: "SELECT 1", RecordedAt: base.Add(-5 * time.Minute)},
	})

	got := col.SamplesInRange(1, base.Add(-time.Hour), base)
	for i := 1; i < len(got); i++ {
		if got[i].RecordedAt.Before(got[i-1].RecordedAt) {
			t.Fatalf("expected Backfill to leave samples chronologically sorted, got out-of-order at index %d: %+v", i, got)
		}
	}
}

func TestCollector_Backfill_DoesNotTouchLiveGauges(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})

	now := time.Now().UTC()
	col.Backfill([]QuerySample{
		{QueryID: 1, QueryText: "SELECT 1", Calls: 99, MeanExecTimeMs: 123.45, RecordedAt: now},
	})

	// Backfilled data must not leak into the live-scrape gauges: those are
	// meant to reflect the most recent LIVE scrape, and a metrics scrape that
	// races the first real Collector.Scrape() should see the zero-value
	// default, not a stale historical number that could be mistaken for
	// current server state.
	m := &dto.Metric{}
	metric, err := col.meanExecTime.GetMetricWithLabelValues("1")
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	if err := metric.Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := m.GetGauge().GetValue(); got != 0 {
		t.Fatalf("expected meanExecTime gauge to remain at its zero-value default after Backfill (no live scrape yet), got %v", got)
	}
}

func TestCollector_Backfill_EmptyIsNoop(t *testing.T) {
	col := newTestCollector(t, Config{RetentionDuration: time.Hour})
	col.Backfill(nil)

	if ids := col.AllQueryIDs(); len(ids) != 0 {
		t.Fatalf("expected no tracked queryids after backfilling an empty slice, got %v", ids)
	}
}

// ----- config defaults -----

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{}
	cfg.defaults()

	if cfg.RetentionDuration != defaultRetentionDuration {
		t.Fatalf("expected default retention of %v, got %v", defaultRetentionDuration, cfg.RetentionDuration)
	}
	if cfg.ScrapeInterval != 60*time.Second {
		t.Fatalf("expected default scrape interval of 60s, got %v", cfg.ScrapeInterval)
	}
}

func TestConfig_RetentionDurationOverride(t *testing.T) {
	cfg := Config{RetentionDuration: 5 * time.Minute}
	cfg.defaults()

	if cfg.RetentionDuration != 5*time.Minute {
		t.Fatalf("expected explicit RetentionDuration to be preserved, got %v", cfg.RetentionDuration)
	}
}

// ----- Ping -----

// TestPing_UnreachableHost_ReturnsError exercises the failure path without
// a real Postgres server: port 1 on localhost refuses the connection
// immediately (nothing can bind to it unprivileged), so this fails fast
// instead of needing the "integration" build tag's real database. The
// success path (a real Ping against a real Postgres with pg_stat_statements
// actually installed) is internal/collector's integration test's job — see
// collector_integration_test.go's TestIntegration_Ping_RealPostgres.
func TestPing_UnreachableHost_ReturnsError(t *testing.T) {
	col := newTestCollector(t, Config{DSN: "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable"})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := col.Ping(ctx); err == nil {
		t.Fatal("expected Ping to fail against an unreachable host, got nil")
	}
}
