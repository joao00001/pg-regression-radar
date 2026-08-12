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

// Package collector scrapes pg_stat_statements and maintains a per-queryid
// in-memory time-series consumed by the correlation engine.
package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	// pq registers the "postgres" driver used by database/sql.
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
)

// QuerySample is one observation of a query's runtime statistics from pg_stat_statements.
type QuerySample struct {
	QueryID         int64
	QueryText       string
	Calls           int64
	TotalExecTimeMs float64
	MeanExecTimeMs  float64
	RecordedAt      time.Time

	// Fingerprint is the normalized-text hash produced by FingerprintQuery
	// for QueryText. It is additive metadata: existing consumers that only
	// read the original fields are unaffected. The Collector uses it
	// internally (see SamplesInRange) to reconnect a query across a queryid
	// change; it is exposed here too so future consumers can group samples
	// by query shape without recomputing the hash.
	Fingerprint string
}

// defaultRetentionDuration bounds how long a sample is kept after being
// scraped. The correlation engine's default analysis window is 30 minutes
// on *each side* of a deploy event (see correlation.Config.WindowMinutes),
// i.e. a full before+after span of up to ~60 minutes per analysis. We keep
// 3x that full span - 180 minutes - so that: (a) a deploy event that is
// ingested or processed with some delay (the operator polls its event store
// every 5s, but a webhook sender or the ingester's own queue could lag
// further) still has its complete pre/post window available when analysis
// finally runs, and (b) queryid-fingerprint fallback (see SamplesInRange)
// has enough historical depth on both the old and new queryid to reconnect
// a query whose queryid changed mid-window. This is a default, not a
// guarantee for every deployment: operators correlating against unusually
// large WindowMinutes values should size --retention-minutes accordingly
// (retention should stay >= 2*WindowMinutes, ideally with headroom).
const defaultRetentionDuration = 180 * time.Minute

// Compaction thresholds for pruneLocked. A pruned slice is reallocated (its
// backing array copied down to exactly the retained length) only once the
// wasted capacity becomes large, so that routine pruning stays a cheap O(1)
// reslice and we don't pay a copy on every scrape for slices that are barely
// over retention.
const (
	compactionMinCapacity = 64
	compactionSlackFactor = 2
)

// fingerprintFallbackMinSamples is the direct-bucket sample count below which
// SamplesInRange also considers samples recorded under other queryids that
// share the same fingerprint. See SamplesInRange for the full rationale. Set
// well below correlation's default MinExecutions (10): a direct bucket with
// fewer than this many points in range is exactly the "queryid rotated
// partway through this window" signature (one side of the rotation has zero
// or a handful of samples), whereas a bucket that already cleared this bar
// has enough of its own data that merging in another queryid's samples would
// mostly just add collision risk for no real benefit.
const fingerprintFallbackMinSamples = 5

// Config holds all configuration for the Collector.
type Config struct {
	DSN            string
	ScrapeInterval time.Duration
	// ClusterName and Namespace are attached as Prometheus label values so
	// multiple clusters can share the same metrics endpoint.
	ClusterName string
	Namespace   string
	// MaxQueryTextLen caps storage; long queries add no diagnostic value.
	MaxQueryTextLen int
	// RetentionDuration bounds how long a QuerySample is kept in memory
	// after being scraped; samples older than now-RetentionDuration are
	// pruned once per scrape cycle (see pruneLocked). Without this, a
	// collector process running for weeks accumulates an ever-growing
	// slice per queryid, and queryids that fall out of pg_stat_statements'
	// top-500 (see queryStatStatements's ORDER BY ... LIMIT 500) are never
	// revisited, so their slices would otherwise never shrink. Defaults to
	// defaultRetentionDuration; see its doc comment for the sizing rationale.
	RetentionDuration time.Duration
}

func (c *Config) defaults() {
	if c.ScrapeInterval == 0 {
		c.ScrapeInterval = 60 * time.Second
	}
	if c.MaxQueryTextLen == 0 {
		c.MaxQueryTextLen = 200
	}
	if c.RetentionDuration == 0 {
		c.RetentionDuration = defaultRetentionDuration
	}
}

// Collector scrapes pg_stat_statements and maintains an in-memory ring of
// QuerySamples per queryid.
type Collector struct {
	cfg    Config
	db     *sql.DB
	logger *slog.Logger

	mu      sync.RWMutex
	samples map[int64][]QuerySample // keyed by queryid

	// queryFingerprint and fingerprintIndex together let SamplesInRange
	// reconnect a query across a queryid change without changing its public
	// signature. queryFingerprint records the most recently observed
	// fingerprint for each queryid; fingerprintIndex is the reverse mapping,
	// fingerprint -> set of queryids currently sharing it. Both are kept in
	// sync with `samples` (updated on ingest in scrape, cleaned up on
	// eviction in pruneLocked) so neither leaks entries for queryids that no
	// longer have any retained samples.
	queryFingerprint map[int64]string
	fingerprintIndex map[string]map[int64]struct{}

	// versionOnce/legacyTimingColumns implement the pg_stat_statements
	// column-name detection described on queryStatStatementsFor.
	versionOnce         sync.Once
	legacyTimingColumns bool

	scrapeTotal     prometheus.Counter
	scrapeErrors    prometheus.Counter
	meanExecTime    *prometheus.GaugeVec
	callsTotal      *prometheus.GaugeVec
	trackedQueries  prometheus.Gauge
	retainedSamples prometheus.Gauge

	// lastScrapeTime holds the UTC time of the most recent successful scrape
	// as an atomic.Value (stores time.Time) so that LastScrapeTime() can
	// read it without acquiring any mutex.
	lastScrapeTime atomic.Value
}

// New creates a new Collector and registers its Prometheus metrics with reg.
// Pass prometheus.DefaultRegisterer when running standalone.
func New(cfg Config, logger *slog.Logger, reg prometheus.Registerer) (*Collector, error) {
	cfg.defaults()

	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("collector: open db: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	labels := prometheus.Labels{
		"cluster":   cfg.ClusterName,
		"namespace": cfg.Namespace,
	}

	scrapeTotal := prometheus.NewCounter(prometheus.CounterOpts{
		Name:        "pg_regression_radar_collector_scrapes_total",
		Help:        "Total number of pg_stat_statements scrapes performed.",
		ConstLabels: labels,
	})
	scrapeErrors := prometheus.NewCounter(prometheus.CounterOpts{
		Name:        "pg_regression_radar_collector_scrape_errors_total",
		Help:        "Total number of scrape errors.",
		ConstLabels: labels,
	})
	meanExecTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:        "pg_regression_radar_query_mean_exec_time_ms",
		Help:        "Mean query execution time in milliseconds (last scrape).",
		ConstLabels: labels,
	}, []string{"queryid"})
	callsTotal := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:        "pg_regression_radar_query_calls_total",
		Help:        "Cumulative call count from pg_stat_statements.",
		ConstLabels: labels,
	}, []string{"queryid"})
	trackedQueries := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "pg_regression_radar_collector_tracked_queries",
		Help:        "Number of distinct queryids currently retained in memory by the collector.",
		ConstLabels: labels,
	})
	retainedSamples := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "pg_regression_radar_collector_retained_samples_total",
		Help:        "Total number of QuerySamples currently retained in memory across all queryids.",
		ConstLabels: labels,
	})

	for _, m := range []prometheus.Collector{scrapeTotal, scrapeErrors, meanExecTime, callsTotal, trackedQueries, retainedSamples} {
		if err := reg.Register(m); err != nil {
			return nil, fmt.Errorf("collector: register metric: %w", err)
		}
	}

	return &Collector{
		cfg:              cfg,
		db:               db,
		logger:           logger,
		samples:          make(map[int64][]QuerySample),
		queryFingerprint: make(map[int64]string),
		fingerprintIndex: make(map[string]map[int64]struct{}),
		scrapeTotal:      scrapeTotal,
		scrapeErrors:     scrapeErrors,
		meanExecTime:     meanExecTime,
		callsTotal:       callsTotal,
		trackedQueries:   trackedQueries,
		retainedSamples:  retainedSamples,
	}, nil
}

// Scrape performs a single, synchronous pg_stat_statements read-and-ingest
// cycle — the same logic Run drives on a ticker, exposed for callers that
// need one deterministic collection pass outside of Run's loop. The
// motivating case is integration/e2e tests that must control exactly when a
// scrape happens relative to test setup (e.g. internal/e2e's full-pipeline
// test), rather than racing a background ticker; an on-demand "collect now"
// admin/debug action is an equally reasonable use.
func (c *Collector) Scrape(ctx context.Context) error {
	return c.scrape(ctx)
}

// Close releases the Collector's database connection. Run already closes it
// when ctx is cancelled, so this is only needed by callers — tests, tools —
// that construct a Collector and drive Scrape directly without calling Run.
func (c *Collector) Close() error {
	return c.db.Close()
}

// Ping verifies the Collector can actually reach Postgres and that
// pg_stat_statements is installed in the target database, without
// performing a scrape or touching any in-memory state.
//
// This exists because sql.Open (used in New) never dials the network --
// it only validates the DSN's syntax -- so a Collector can construct
// successfully against a completely unreachable host, or a real but
// misconfigured database that simply never had `CREATE EXTENSION
// pg_stat_statements` run against it, and neither problem surfaces until
// the first scheduled scrape fails. Ping is what cmd/operator's and
// cmd/collector's --dry-run flag calls to catch both cases immediately,
// but it's equally usable as a startup or readiness check by any caller
// that wants one.
func (c *Collector) Ping(ctx context.Context) error {
	if err := c.db.PingContext(ctx); err != nil {
		return fmt.Errorf("collector: ping: %w", err)
	}

	var installed bool
	const q = `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`
	if err := c.db.QueryRowContext(ctx, q).Scan(&installed); err != nil {
		return fmt.Errorf("collector: check pg_stat_statements extension: %w", err)
	}
	if !installed {
		return fmt.Errorf("collector: pg_stat_statements extension is not installed in this database (run: CREATE EXTENSION pg_stat_statements)")
	}
	return nil
}

// Run starts the scrape loop and blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.ScrapeInterval)
	defer ticker.Stop()

	c.logger.Info("collector: starting scrape loop",
		"interval", c.cfg.ScrapeInterval,
		"retention", c.cfg.RetentionDuration,
		"cluster", c.cfg.ClusterName)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("collector: stopping")
			return c.db.Close()
		case <-ticker.C:
			if err := c.scrape(ctx); err != nil {
				c.logger.Error("collector: scrape failed", "err", err)
				c.scrapeErrors.Inc()
			}
		}
	}
}

// Backfill seeds the collector's in-memory sample history from previously
// persisted data (see internal/storage/postgres), so a freshly restarted
// process doesn't start with a cold in-memory view even though its durable
// history is intact — see docs/persistence.md. Call this once, after New and
// before Run, with samples loaded from the configured SampleStore covering
// at least the last RetentionDuration.
//
// Unlike scrape's ingestSample, Backfill does NOT update the live Prometheus
// gauges (meanExecTime/callsTotal): those reflect the most recent LIVE
// scrape, and overwriting them with historical values here would misrepresent
// current server state to anything scraping /metrics before the first real
// scrape completes.
func (c *Collector) Backfill(samples []QuerySample) {
	if len(samples) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range samples {
		if s.Fingerprint == "" {
			s.Fingerprint = FingerprintQuery(s.QueryText)
		}
		c.samples[s.QueryID] = append(c.samples[s.QueryID], s)
		c.indexFingerprintLocked(s.QueryID, s.Fingerprint)
	}
	// Loaded data may not arrive pre-sorted per queryid (storage.SampleStore
	// makes no such promise across a bulk load built by iterating several
	// queryids and concatenating); pruneLocked's binary search and
	// SamplesInRange's merge both assume chronological order per queryid.
	for qid := range c.samples {
		s := c.samples[qid]
		sort.Slice(s, func(i, j int) bool { return s[i].RecordedAt.Before(s[j].RecordedAt) })
	}
	now := time.Now().UTC()
	c.pruneLocked(now)
	c.updateRetentionMetricsLocked()
}

// SamplesInRange returns QuerySamples for queryID whose RecordedAt falls
// within [from, to].
//
// pg_stat_statements' queryid is explicitly documented as unstable across
// PostgreSQL major versions and can also change if a referenced object is
// dropped and recreated between executions (see FingerprintQuery's doc
// comment for citations). If a CloudNativePG rolling deploy ships a schema
// migration in the middle of an analysis window, the "same" query can
// therefore appear to have few or no samples under a given queryid for part
// of that window, purely because pg_stat_statements started a new entry.
//
// To compensate without changing this method's signature (this interface is
// also consumed by internal/correlation.SampleSource and must stay stable),
// SamplesInRange falls back to a fingerprint-based union: when the direct
// per-queryid result has fewer than fingerprintFallbackMinSamples samples in
// range, it additionally pulls in same-range samples recorded under any
// other queryid that currently shares this queryid's text fingerprint, and
// returns the merged set sorted by RecordedAt. When the direct bucket
// already has enough samples, no fallback is attempted, which limits the
// impact of the (rare) case where two textually-different queries hash to
// the same fingerprint.
//
// Known limitation: AllQueryIDs() still enumerates old and new queryids
// separately, so a query whose queryid rotated mid-window may be analyzed
// (and, if flagged, reported) once under each queryid, both pulling in the
// same merged sample set. Deduplicating at the correlation-engine layer is
// out of scope here to avoid touching internal/correlation's interface.
func (c *Collector) SamplesInRange(queryID int64, from, to time.Time) []QuerySample {
	c.mu.RLock()
	defer c.mu.RUnlock()

	direct := filterRange(c.samples[queryID], from, to)
	if len(direct) >= fingerprintFallbackMinSamples {
		return direct
	}

	fp, ok := c.queryFingerprint[queryID]
	if !ok {
		return direct
	}
	siblings := c.fingerprintIndex[fp]
	if len(siblings) <= 1 {
		return direct
	}

	merged := append([]QuerySample(nil), direct...)
	for sibling := range siblings {
		if sibling == queryID {
			continue
		}
		merged = append(merged, filterRange(c.samples[sibling], from, to)...)
	}
	if len(merged) <= len(direct) {
		return direct
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].RecordedAt.Before(merged[j].RecordedAt)
	})
	return merged
}

// filterRange returns the subset of all whose RecordedAt falls in [from, to].
func filterRange(all []QuerySample, from, to time.Time) []QuerySample {
	var result []QuerySample
	for _, s := range all {
		if !s.RecordedAt.Before(from) && !s.RecordedAt.After(to) {
			result = append(result, s)
		}
	}
	return result
}

// AllQueryIDs returns the set of queryids with at least one retained sample.
// Once a queryid's samples have all aged out past RetentionDuration, it is
// removed from the underlying map (see pruneLocked) and no longer appears
// here.
func (c *Collector) AllQueryIDs() []int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]int64, 0, len(c.samples))
	for id := range c.samples {
		ids = append(ids, id)
	}
	return ids
}

// LastScrapeTime returns the UTC timestamp of the most recent successful
// scrape, or the zero time.Time if no scrape has completed yet. It reads
// an atomically stored value and incurs no lock contention or allocation.
func (c *Collector) LastScrapeTime() time.Time {
	if v := c.lastScrapeTime.Load(); v != nil {
		return v.(time.Time)
	}
	return time.Time{}
}

// scrape reads pg_stat_statements and stores a QuerySample for each row.
func (c *Collector) scrape(ctx context.Context) error {
	c.resolveColumns(ctx)

	rows, err := c.db.QueryContext(ctx, c.queryStatStatements())
	if err != nil {
		return fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	c.scrapeTotal.Inc()

	for rows.Next() {
		var qid int64
		var queryText string
		var calls int64
		var totalExecTime, meanExecTime float64

		if err := rows.Scan(&qid, &queryText, &calls, &totalExecTime, &meanExecTime); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}

		c.ingestSample(now, qid, queryText, calls, totalExecTime, meanExecTime)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	c.lastScrapeTime.Store(now)
	c.pruneAndUpdateMetrics(now)
	return nil
}

// ingestSample records one observation for qid at time now. It is factored
// out of scrape's row loop so that both the real scrape path and tests can
// drive the collector's storage/fingerprint bookkeeping one sample at a time
// without needing a live pg_stat_statements connection.
func (c *Collector) ingestSample(now time.Time, qid int64, queryText string, calls int64, totalExecTime, meanExecTime float64) {
	// Truncate to stay within the configured budget; tail bytes are diagnostically redundant.
	if len(queryText) > c.cfg.MaxQueryTextLen {
		queryText = queryText[:c.cfg.MaxQueryTextLen] + "…"
	}

	fingerprint := FingerprintQuery(queryText)

	sample := QuerySample{
		QueryID:         qid,
		QueryText:       queryText,
		Calls:           calls,
		TotalExecTimeMs: totalExecTime,
		MeanExecTimeMs:  meanExecTime,
		RecordedAt:      now,
		Fingerprint:     fingerprint,
	}

	c.mu.Lock()
	c.samples[qid] = append(c.samples[qid], sample)
	c.indexFingerprintLocked(qid, fingerprint)
	c.mu.Unlock()

	qidStr := fmt.Sprintf("%d", qid)
	c.meanExecTime.WithLabelValues(qidStr).Set(meanExecTime)
	c.callsTotal.WithLabelValues(qidStr).Set(float64(calls))
}

// pruneAndUpdateMetrics evicts samples older than now-RetentionDuration and
// refreshes the tracked-queries/retained-samples gauges. Split out from
// scrape so tests can simulate the periodic-cleanup half of a scrape cycle
// (point 6/7 of the retention design) independently of ingesting new rows.
func (c *Collector) pruneAndUpdateMetrics(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pruneLocked(now)
	c.updateRetentionMetricsLocked()
}

// indexFingerprintLocked records fingerprint as qid's current fingerprint,
// updating the reverse index and evicting qid from its previous fingerprint's
// set if the fingerprint changed (e.g. the underlying query text was
// re-normalized to a different placeholder pattern - rare, but keeps the
// index from accumulating stale entries). Caller must hold c.mu for writing.
func (c *Collector) indexFingerprintLocked(qid int64, fingerprint string) {
	if oldFP, ok := c.queryFingerprint[qid]; ok && oldFP != fingerprint {
		c.removeFromFingerprintIndexLocked(oldFP, qid)
	}
	c.queryFingerprint[qid] = fingerprint

	set, ok := c.fingerprintIndex[fingerprint]
	if !ok {
		set = make(map[int64]struct{})
		c.fingerprintIndex[fingerprint] = set
	}
	set[qid] = struct{}{}
}

func (c *Collector) removeFromFingerprintIndexLocked(fingerprint string, qid int64) {
	set, ok := c.fingerprintIndex[fingerprint]
	if !ok {
		return
	}
	delete(set, qid)
	if len(set) == 0 {
		delete(c.fingerprintIndex, fingerprint)
	}
}

// forgetQueryLocked removes all bookkeeping for a queryid that no longer has
// any retained samples (samples map entry already deleted by the caller).
// Caller must hold c.mu for writing.
func (c *Collector) forgetQueryLocked(qid int64) {
	fp, ok := c.queryFingerprint[qid]
	if !ok {
		return
	}
	delete(c.queryFingerprint, qid)
	c.removeFromFingerprintIndexLocked(fp, qid)
}

// pruneLocked drops samples older than now-RetentionDuration from every
// queryid's slice, and removes queryids left with no samples at all. Samples
// are always appended in chronological order (see scrape), so the aged-out
// prefix of each slice can be located with a single binary search and
// dropped via one reslice - O(log n) search + O(1) reslice per queryid, with
// no per-sample work, which is what keeps this safe to run every scrape
// cycle even under sustained load (avoiding the O(n^2) cost of repeatedly
// popping one stale sample at a time).
//
// Caller must hold c.mu for writing.
func (c *Collector) pruneLocked(now time.Time) {
	cutoff := now.Add(-c.cfg.RetentionDuration)

	for qid, samples := range c.samples {
		idx := sort.Search(len(samples), func(i int) bool {
			return !samples[i].RecordedAt.Before(cutoff)
		})

		if idx == 0 {
			continue // nothing aged out
		}
		if idx >= len(samples) {
			// Every retained sample aged out. Drop the key entirely so the
			// map (and the fingerprint index) doesn't grow without bound in
			// *number of keys* even if each individual slice stays small -
			// this is what reclaims queryids that fell out of
			// pg_stat_statements' top-500 and stopped being scraped.
			delete(c.samples, qid)
			c.forgetQueryLocked(qid)
			continue
		}

		remaining := samples[idx:]
		// The reslice above still references the original backing array, so
		// the pruned-off prefix isn't collected until something reallocates.
		// Compact only once the waste is large enough to matter, to avoid
		// paying a copy on every scrape for slices that are barely over
		// retention.
		if cap(remaining) > compactionMinCapacity && cap(remaining) > compactionSlackFactor*len(remaining) {
			compacted := make([]QuerySample, len(remaining))
			copy(compacted, remaining)
			remaining = compacted
		}
		c.samples[qid] = remaining
	}
}

// updateRetentionMetricsLocked refreshes the tracked-queries/retained-samples
// gauges from current map state. Caller must hold c.mu (read or write).
func (c *Collector) updateRetentionMetricsLocked() {
	total := 0
	for _, s := range c.samples {
		total += len(s)
	}
	c.trackedQueries.Set(float64(len(c.samples)))
	c.retainedSamples.Set(float64(total))
}

// resolveColumns detects, once, whether the target server's pg_stat_statements
// uses the pre-13 "total_time"/"mean_time" columns or the 13+
// "total_exec_time"/"mean_exec_time" columns, and caches the result for
// queryStatStatements to use.
//
// Column names differ across PostgreSQL major versions: PostgreSQL 13 split
// the pre-13 total_time/mean_time columns into separate planning
// (total_plan_time/mean_plan_time) and execution (total_exec_time/
// mean_exec_time) statistics - see the pg_stat_statements column reference
// for PostgreSQL 13 (https://www.postgresql.org/docs/13/pgstatstatements.html)
// versus PostgreSQL 12 (https://www.postgresql.org/docs/12/pgstatstatements.html),
// where the older combined columns are still named total_time/mean_time.
//
// This collector targets CloudNativePG clusters primarily: CloudNativePG's
// currently supported minor releases (1.29.x and 1.30.x as of 2026) only
// ship PostgreSQL 14-18 (https://cloudnative-pg.io/docs/devel/supported_releases,
// "Supported PostgreSQL versions"), and PostgreSQL 13 itself reached
// community end-of-life on 2025-11-13. A query hard-coded to the 13+ column
// names is therefore already safe for every currently-supported
// CloudNativePG cluster. We still detect the server version at runtime and
// fall back to the legacy column names below PG13, both for defense in depth
// (older CloudNativePG installations may still run out-of-support Postgres
// images) and so this collector can be pointed at a non-CloudNativePG,
// self-managed Postgres instance running an older major version.
func (c *Collector) resolveColumns(ctx context.Context) {
	c.versionOnce.Do(func() {
		var versionNum int
		// server_version_num is formatted MMmmpp (e.g. 140005 = 14.5) for
		// PostgreSQL 10+, and Mmmpp for older releases (e.g. 90603 = 9.6.3);
		// dividing by 10000 yields the major version number in both schemes,
		// which is all the precision needed for the >=13 threshold below.
		err := c.db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&versionNum)
		if err != nil {
			c.logger.Warn("collector: could not determine server_version_num; assuming PostgreSQL 13+ pg_stat_statements column names",
				"err", err)
			return
		}
		major := versionNum / 10000
		if major < 13 {
			c.legacyTimingColumns = true
			c.logger.Info("collector: detected PostgreSQL older than 13; using legacy pg_stat_statements column names (total_time/mean_time)",
				"server_version_num", versionNum)
		}
	})
}

// queryStatStatements returns the SQL used to read pg_stat_statements, using
// column names appropriate for the detected server version (see
// resolveColumns). Both branches alias their timing columns to
// total_exec_time/mean_exec_time so scrape's Scan targets don't need to vary.
func (c *Collector) queryStatStatements() string {
	execCol, meanCol := "total_exec_time", "mean_exec_time"
	if c.legacyTimingColumns {
		execCol, meanCol = "total_time", "mean_time"
	}
	return fmt.Sprintf(`
SELECT
    queryid,
    LEFT(query, 500)          AS query,
    calls,
    %s                        AS total_exec_time,
    %s                        AS mean_exec_time
FROM pg_stat_statements
WHERE query NOT LIKE '%%pg_stat_statements%%'
ORDER BY %s DESC
LIMIT 500
`, execCol, meanCol, execCol)
}
