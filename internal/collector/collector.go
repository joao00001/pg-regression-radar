// Package collector scrapes pg_stat_statements from a Postgres cluster and
// exposes the data as Prometheus metrics and an in-memory time-series store
// used by the Correlation Engine.
package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	// Register the postgres driver.
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
)

// QuerySample holds a single observation of a query's runtime statistics as
// reported by pg_stat_statements.
type QuerySample struct {
	// QueryID is the pg_stat_statements queryid (hash of the normalised query).
	QueryID int64
	// QueryText is a truncated version of the normalised query string.
	QueryText string
	// Calls is the cumulative call count since the last pg_stat_statements reset.
	Calls int64
	// TotalExecTimeMs is the cumulative execution time in milliseconds.
	TotalExecTimeMs float64
	// MeanExecTimeMs is the mean execution time in milliseconds for this scrape.
	MeanExecTimeMs float64
	// RecordedAt is the wall-clock time at which this sample was taken.
	RecordedAt time.Time
}

// Config holds all configuration for the Collector.
type Config struct {
	// DSN is the Postgres connection string.
	DSN string
	// ScrapeInterval controls how often pg_stat_statements is scraped.
	ScrapeInterval time.Duration
	// ClusterName is used as a label on every metric / sample.
	ClusterName string
	// Namespace is used as a label on every metric / sample.
	Namespace string
	// MaxQueryTextLen limits how many bytes of query text are stored (default 200).
	MaxQueryTextLen int
}

func (c *Config) defaults() {
	if c.ScrapeInterval == 0 {
		c.ScrapeInterval = 60 * time.Second
	}
	if c.MaxQueryTextLen == 0 {
		c.MaxQueryTextLen = 200
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

	// Prometheus metrics
	scrapeTotal   prometheus.Counter
	scrapeErrors  prometheus.Counter
	meanExecTime  *prometheus.GaugeVec
	callsTotal    *prometheus.GaugeVec
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
		Name:        "deploylens_collector_scrapes_total",
		Help:        "Total number of pg_stat_statements scrapes performed.",
		ConstLabels: labels,
	})
	scrapeErrors := prometheus.NewCounter(prometheus.CounterOpts{
		Name:        "deploylens_collector_scrape_errors_total",
		Help:        "Total number of scrape errors.",
		ConstLabels: labels,
	})
	meanExecTime := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:        "deploylens_query_mean_exec_time_ms",
		Help:        "Mean query execution time in milliseconds (last scrape).",
		ConstLabels: labels,
	}, []string{"queryid"})
	callsTotal := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name:        "deploylens_query_calls_total",
		Help:        "Cumulative call count from pg_stat_statements.",
		ConstLabels: labels,
	}, []string{"queryid"})

	for _, m := range []prometheus.Collector{scrapeTotal, scrapeErrors, meanExecTime, callsTotal} {
		if err := reg.Register(m); err != nil {
			return nil, fmt.Errorf("collector: register metric: %w", err)
		}
	}

	return &Collector{
		cfg:          cfg,
		db:           db,
		logger:       logger,
		samples:      make(map[int64][]QuerySample),
		scrapeTotal:  scrapeTotal,
		scrapeErrors: scrapeErrors,
		meanExecTime: meanExecTime,
		callsTotal:   callsTotal,
	}, nil
}

// Run starts the scrape loop and blocks until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.cfg.ScrapeInterval)
	defer ticker.Stop()

	c.logger.Info("collector: starting scrape loop",
		"interval", c.cfg.ScrapeInterval,
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

// SamplesInRange returns all QuerySamples for the given queryid whose RecordedAt
// falls within [from, to].
func (c *Collector) SamplesInRange(queryID int64, from, to time.Time) []QuerySample {
	c.mu.RLock()
	defer c.mu.RUnlock()

	all := c.samples[queryID]
	var result []QuerySample
	for _, s := range all {
		if !s.RecordedAt.Before(from) && !s.RecordedAt.After(to) {
			result = append(result, s)
		}
	}
	return result
}

// AllQueryIDs returns the set of queryids seen since the collector started.
func (c *Collector) AllQueryIDs() []int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]int64, 0, len(c.samples))
	for id := range c.samples {
		ids = append(ids, id)
	}
	return ids
}

// scrape reads pg_stat_statements and stores a QuerySample for each row.
func (c *Collector) scrape(ctx context.Context) error {
	rows, err := c.db.QueryContext(ctx, queryStatStatements)
	if err != nil {
		return fmt.Errorf("query pg_stat_statements: %w", err)
	}
	defer rows.Close()

	now := time.Now().UTC()
	c.scrapeTotal.Inc()

	c.mu.Lock()
	defer c.mu.Unlock()

	for rows.Next() {
		var qid int64
		var queryText string
		var calls int64
		var totalExecTime, meanExecTime float64

		if err := rows.Scan(&qid, &queryText, &calls, &totalExecTime, &meanExecTime); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}

		// Truncate query text for storage efficiency.
		if len(queryText) > c.cfg.MaxQueryTextLen {
			queryText = queryText[:c.cfg.MaxQueryTextLen] + "…"
		}

		sample := QuerySample{
			QueryID:         qid,
			QueryText:       queryText,
			Calls:           calls,
			TotalExecTimeMs: totalExecTime,
			MeanExecTimeMs:  meanExecTime,
			RecordedAt:      now,
		}

		c.samples[qid] = append(c.samples[qid], sample)

		qidStr := fmt.Sprintf("%d", qid)
		c.meanExecTime.WithLabelValues(qidStr).Set(meanExecTime)
		c.callsTotal.WithLabelValues(qidStr).Set(float64(calls))
	}

	return rows.Err()
}

// queryStatStatements is the SQL used to read pg_stat_statements.
// It works on Postgres 13+ where mean_exec_time is available.
const queryStatStatements = `
SELECT
    queryid,
    LEFT(query, 500)        AS query,
    calls,
    total_exec_time,
    mean_exec_time
FROM pg_stat_statements
WHERE query NOT LIKE '%pg_stat_statements%'
ORDER BY total_exec_time DESC
LIMIT 500
`
