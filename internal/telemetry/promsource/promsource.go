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

// Package promsource implements correlation.SampleSource against a
// Prometheus HTTP API endpoint, so a fleet that already runs an
// OpenTelemetry Collector (postgresqlreceiver -> prometheusexporter) can
// feed the correlation engine without pg-regression-radar's own
// internal/collector.Collector running a second pg_stat_statements scraper
// against the same database.
//
// # Why this package cannot use "the" OTel Postgres metric out of the box
//
// As of this writing, opentelemetry-collector-contrib's postgresqlreceiver
// exposes exactly one execution-time metric with Prometheus-exportable
// semantics: postgresql.query.execution.time, a cumulative monotonic Sum
// (unit "s") whose only attribute is db.namespace (the database name) —
// see receiver/postgresqlreceiver/metadata.yaml on
// open-telemetry/opentelemetry-collector-contrib. It has no per-query
// attribution: no queryid, no query text. Per-query detail (queryid, calls,
// total/mean exec time, query text) exists only on the db.server.top_query
// *log record*, which the prometheusexporter — a metrics-only exporter —
// never sees.
//
// So a literal "point this at postgresql_query_execution_time_seconds_total"
// integration would only ever produce one time series per database, not per
// query, and could never satisfy correlation.SampleSource's per-queryid
// contract. Rather than hardcode that broken assumption, this package takes
// the PromQL metric name and the label carrying a numeric queryid as
// configuration (Config.MetricName, Config.QueryIDLabel). Operators wire it
// to a source that actually carries a queryid label — for example a
// Prometheus recording rule computed from a custom OTel pipeline that
// promotes db.server.top_query's postgresql.queryid attribute onto a
// metric (a transform/logstometrics-style processor), or any future
// postgresqlreceiver release that adds per-query attribution to the metric
// itself. See docs/collector-internals.md for a worked example.
//
// # Metric value semantics
//
// Whatever metric is configured must report a per-scrape, per-queryid
// *mean* execution time in milliseconds — the same semantics as
// pg_stat_statements.mean_exec_time, which is what
// internal/collector.Collector.ingestSample stores in
// collector.QuerySample.MeanExecTimeMs, and the only field
// correlation.Engine's detection math actually reads (see
// internal/correlation/engine.go's extractLatencies). A raw cumulative
// counter (like postgresql.query.execution.time itself) is the wrong shape
// for this — it must first be turned into a mean, upstream of Prometheus,
// via PromQL like:
//
//	avg by (queryid) (rate(<some_per_query_exec_time_seconds_total>[5m])
//	  / rate(<some_per_query_calls_total>[5m])) * 1000
//
// Each raw sample this package reads back with query_range is treated as
// one point-in-time observation, exactly like one collector.Scrape() call —
// it does not itself rate() or average anything.
package promsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
)

// defaultTimeout bounds a single HTTP round trip to the Prometheus API.
const defaultTimeout = 10 * time.Second

// defaultStep is the query_range resolution used when none is configured.
// It should match (or be coarser than) the OTel Collector's own scrape
// interval for the configured metric — a finer step than the underlying
// data's real resolution just returns repeated points.
const defaultStep = 15 * time.Second

// defaultQueryIDLabel is the label name this package looks for a numeric
// pg_stat_statements queryid under, absent an explicit Config.QueryIDLabel.
const defaultQueryIDLabel = "queryid"

// defaultQueryTextLabel is the label name this package looks for the
// query's text under, absent an explicit Config.QueryTextLabel. Optional:
// used only to populate QuerySample.QueryText/Fingerprint for display and
// for correlation.Engine's queryid-fingerprint-rotation fallback (see
// internal/correlation/engine.go's canonicalQueryID) — a queryid-only feed
// still satisfies SampleSource without it.
const defaultQueryTextLabel = "query"

// allQueryIDsLookback bounds how far back AllQueryIDs looks for series that
// are still "live" (have reported at least one sample recently). It should
// comfortably exceed the configured metric's own scrape interval so a
// query that is merely between scrapes isn't dropped, but stay well under
// correlation.Config's analysis windows so AllQueryIDs doesn't surface
// queryids that are actually gone. 10 minutes is a deliberately generous
// default for typical (15s-60s) OTel Collector scrape intervals.
const allQueryIDsLookback = 10 * time.Minute

// Config configures a Source. BaseURL and MetricName are required; every
// other field defaults as documented on its constant/field comment.
type Config struct {
	// BaseURL is the Prometheus HTTP API base, e.g. "http://prometheus:9090"
	// (no trailing slash required, and no "/api/v1/..." suffix).
	BaseURL string

	// MetricName is the Prometheus metric this Source reads samples from.
	// See the package doc comment for why there is deliberately no default
	// pointing at postgresql_query_execution_time_seconds_total.
	MetricName string

	// QueryIDLabel is the label carrying a query's pg_stat_statements
	// queryid, formatted as a base-10 (optionally negative) integer
	// string — Prometheus label values are always strings regardless of
	// the underlying value's type, and pg_stat_statements queryids are
	// int64, so this package always parses them with strconv.ParseInt
	// rather than through any float/JSON-number path (which would lose
	// precision past 2^53). Defaults to "queryid".
	QueryIDLabel string

	// QueryTextLabel is the label carrying the query's text, if the
	// configured metric has one. Optional — see the package doc comment.
	// Defaults to "query"; set to "-" to explicitly disable the lookup
	// (rather than silently using a same-named label that isn't the
	// query's text).
	QueryTextLabel string

	// Step is the query_range resolution. Defaults to 15s.
	Step time.Duration

	// HTTPClient is the client used for requests to Prometheus. Defaults
	// to a client with Timeout set to Config.Timeout (or defaultTimeout).
	HTTPClient *http.Client

	// Timeout bounds a single HTTP round trip when HTTPClient is left
	// unset. Ignored if HTTPClient is set explicitly — set its own
	// Timeout instead. Defaults to 10s.
	Timeout time.Duration
}

// Source implements correlation.SampleSource by issuing PromQL queries
// against a Prometheus HTTP API. It is stateless and safe for concurrent
// use, matching how internal/correlation.Engine calls SamplesInRange /
// AllQueryIDs concurrently across queryids within a single analysis (see
// internal/correlation/engine.go's analyseWindow).
type Source struct {
	cfg Config
}

// New validates cfg and returns a ready-to-use Source. It performs no
// network I/O — a Source with an unreachable BaseURL is only discovered as
// such the first time SamplesInRange or AllQueryIDs actually queries it,
// matching how internal/collector.Collector's DSN isn't validated until
// its first Scrape/Ping.
func New(cfg Config) (*Source, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("promsource: BaseURL is required")
	}
	if cfg.MetricName == "" {
		return nil, fmt.Errorf("promsource: MetricName is required (see package doc comment for why there is no default)")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("promsource: invalid BaseURL: %w", err)
	}
	if cfg.QueryIDLabel == "" {
		cfg.QueryIDLabel = defaultQueryIDLabel
	}
	if cfg.QueryTextLabel == "" {
		cfg.QueryTextLabel = defaultQueryTextLabel
	}
	if cfg.Step <= 0 {
		cfg.Step = defaultStep
	}
	if cfg.HTTPClient == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		cfg.HTTPClient = &http.Client{Timeout: timeout}
	}
	return &Source{cfg: cfg}, nil
}

// promResponse is the envelope every Prometheus HTTP API endpoint used here
// returns. See https://prometheus.io/docs/prometheus/latest/querying/api/.
type promResponse struct {
	Status    string   `json:"status"`
	Data      promData `json:"data"`
	ErrorType string   `json:"errorType"`
	Error     string   `json:"error"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promSeries `json:"result"`
}

// promSeries covers both the "matrix" (query_range) and "vector" (instant
// query) result shapes: Values is populated for a matrix result, Value for
// a vector result. Metric's values are always JSON strings — Prometheus
// does not distinguish numeric-looking label values from any other string,
// so a queryid label must be parsed back to int64 by the caller (see the
// package doc comment).
type promSeries struct {
	Metric map[string]string `json:"metric"`
	Value  promSample        `json:"value"`
	Values []promSample      `json:"values"`
}

// promSample is Prometheus's [timestamp, "value"] pair: the timestamp is a
// JSON number (unix seconds, fractional), the value is always a JSON
// string even though it is numeric.
type promSample struct {
	Timestamp float64
	Value     string
}

func (s *promSample) UnmarshalJSON(b []byte) error {
	var pair [2]json.RawMessage
	if err := json.Unmarshal(b, &pair); err != nil {
		return err
	}
	if err := json.Unmarshal(pair[0], &s.Timestamp); err != nil {
		return fmt.Errorf("promsource: parsing sample timestamp: %w", err)
	}
	if err := json.Unmarshal(pair[1], &s.Value); err != nil {
		return fmt.Errorf("promsource: parsing sample value: %w", err)
	}
	return nil
}

// SamplesInRange implements correlation.SampleSource. It issues a
// query_range request scoped to queryID via Config.QueryIDLabel and maps
// each returned point to one collector.QuerySample, mirroring one
// collector.Collector.Scrape() observation per point (see the package doc
// comment on value semantics).
//
// On any error (unreachable Prometheus, non-2xx response, malformed body)
// SamplesInRange returns nil rather than an error, matching
// correlation.SampleSource's signature — the same contract
// internal/collector.Collector's own in-memory implementation follows for
// an unknown queryid. A caller that needs to distinguish "no data" from
// "Prometheus is down" should watch this package's future metrics/logs
// rather than this return value; see docs/collector-internals.md.
func (s *Source) SamplesInRange(queryID int64, from, to time.Time) []collector.QuerySample {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.HTTPClient.Timeout+defaultTimeout)
	defer cancel()

	query := fmt.Sprintf("%s{%s=%q}", s.cfg.MetricName, s.cfg.QueryIDLabel, strconv.FormatInt(queryID, 10))
	series, err := s.queryRange(ctx, query, from, to)
	if err != nil || len(series) == 0 {
		return nil
	}

	var samples []collector.QuerySample
	for _, ser := range series {
		queryText := ""
		if s.cfg.QueryTextLabel != "-" {
			queryText = ser.Metric[s.cfg.QueryTextLabel]
		}
		fingerprint := ""
		if queryText != "" {
			fingerprint = collector.FingerprintQuery(queryText)
		}
		for _, pt := range ser.Values {
			meanExecMs, err := strconv.ParseFloat(pt.Value, 64)
			if err != nil || math.IsNaN(meanExecMs) || math.IsInf(meanExecMs, 0) {
				continue // Prometheus represents "no data"/staleness as NaN, not absence, so this isn't a usable observation
			}
			samples = append(samples, collector.QuerySample{
				QueryID:        queryID,
				QueryText:      queryText,
				MeanExecTimeMs: meanExecMs,
				RecordedAt:     time.Unix(0, int64(pt.Timestamp*float64(time.Second))),
				Fingerprint:    fingerprint,
			})
		}
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i].RecordedAt.Before(samples[j].RecordedAt) })
	return samples
}

// AllQueryIDs implements correlation.SampleSource. It issues an instant
// query (count by (<QueryIDLabel>) (<MetricName>)) scoped to
// allQueryIDsLookback so a queryid that stopped reporting recently is not
// returned indefinitely, matching internal/collector.Collector's own
// bound-by-RetentionDuration behavior for AllQueryIDs.
func (s *Source) AllQueryIDs() []int64 {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.HTTPClient.Timeout+defaultTimeout)
	defer cancel()

	query := fmt.Sprintf("count by (%s) (max_over_time(%s[%s]))",
		s.cfg.QueryIDLabel, s.cfg.MetricName, allQueryIDsLookback.String())
	series, err := s.queryInstant(ctx, query, time.Now())
	if err != nil {
		return nil
	}

	ids := make([]int64, 0, len(series))
	seen := make(map[int64]struct{}, len(series))
	for _, ser := range series {
		raw, ok := ser.Metric[s.cfg.QueryIDLabel]
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue // a non-numeric label value under QueryIDLabel is a misconfiguration, not a queryid
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// queryRange calls GET /api/v1/query_range. See
// https://prometheus.io/docs/prometheus/latest/querying/api/#range-queries.
func (s *Source) queryRange(ctx context.Context, query string, from, to time.Time) ([]promSeries, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("start", formatTimestamp(from))
	q.Set("end", formatTimestamp(to))
	q.Set("step", formatStep(s.cfg.Step))
	data, err := s.do(ctx, "/api/v1/query_range", q)
	if err != nil {
		return nil, err
	}
	if data.ResultType != "matrix" {
		return nil, fmt.Errorf("promsource: query_range returned resultType %q, want \"matrix\"", data.ResultType)
	}
	return data.Result, nil
}

// queryInstant calls GET /api/v1/query. See
// https://prometheus.io/docs/prometheus/latest/querying/api/#instant-queries.
func (s *Source) queryInstant(ctx context.Context, query string, at time.Time) ([]promSeries, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("time", formatTimestamp(at))
	data, err := s.do(ctx, "/api/v1/query", q)
	if err != nil {
		return nil, err
	}
	if data.ResultType != "vector" {
		return nil, fmt.Errorf("promsource: query returned resultType %q, want \"vector\"", data.ResultType)
	}
	return data.Result, nil
}

func (s *Source) do(ctx context.Context, path string, q url.Values) (promData, error) {
	reqURL := s.cfg.BaseURL + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return promData{}, fmt.Errorf("promsource: building request: %w", err)
	}

	resp, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return promData{}, fmt.Errorf("promsource: request to %s failed: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return promData{}, fmt.Errorf("promsource: reading response body: %w", err)
	}

	var parsed promResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return promData{}, fmt.Errorf("promsource: decoding response (status %d): %w", resp.StatusCode, err)
	}
	if parsed.Status != "success" {
		return promData{}, fmt.Errorf("promsource: prometheus returned status %q: %s: %s", parsed.Status, parsed.ErrorType, parsed.Error)
	}
	return parsed.Data, nil
}

// formatTimestamp renders t as Prometheus's RFC3339 timestamp format,
// which the HTTP API accepts for start/end/time (the alternative,
// unix_timestamp, loses no precision here but RFC3339 keeps requests
// readable in logs/traces of outgoing HTTP calls).
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// formatStep renders d as Prometheus's duration format (e.g. "15s"). Go's
// time.Duration.String() output (e.g. "15s", "1m0s") is accepted by
// Prometheus's duration parser for the range of values Config.Step is
// expected to hold (sub-day scrape-interval-sized steps), so no separate
// formatter is needed.
func formatStep(d time.Duration) string {
	return d.String()
}
