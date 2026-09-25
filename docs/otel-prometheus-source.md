# OpenTelemetry / Prometheus Sample Source

*`internal/telemetry/promsource` — an opt-in `SampleSource` that reads query samples from Prometheus instead of scraping `pg_stat_statements` directly; for fleets that already run an OpenTelemetry Collector.*

## Overview

`internal/correlation.Engine` depends only on a two-method `SampleSource` interface (`SamplesInRange`, `AllQueryIDs` — see [Roadmap § OpenTelemetry as a pluggable analysis input](roadmap.md#opentelemetry-as-a-pluggable-analysis-input-in-detail)), not concretely on `internal/collector.Collector`. `internal/telemetry/promsource.Source` is a second implementation of that same interface, backed by PromQL queries against a Prometheus HTTP API instead of a direct database connection. This page covers what it does and, more importantly, what it currently cannot do without extra setup — that second part matters more here than for most pages.

## The real gap: per-query attribution isn't there yet, out of the box

opentelemetry-collector-contrib's `postgresqlreceiver` scrapes `pg_stat_statements` and exposes `postgresql.query.execution.time` — but as of this writing that metric is a **cumulative Sum scoped to `db.namespace` (the database) only**. It carries no `queryid` and no query text. Per-query detail (`queryid`, calls, exec time, query text) exists only on the receiver's `db.server.top_query` **log record**, and a metrics-only Prometheus exporter never sees log records.

Concretely: pointing `promsource.Config.MetricName` at `postgresql_query_execution_time_seconds_total` (the Prometheus-exported name of that metric, per the [OTel-to-Prometheus translation rules](https://opentelemetry.io/docs/specs/otel/compatibility/prometheus_and_openmetrics/)) will not work — there is no `queryid` label on it to select by, so `SamplesInRange` would only ever see one series per database, never per query.

`promsource.Source` does not hardcode that broken assumption. `Config.MetricName` and `Config.QueryIDLabel` are both required configuration, not defaults pointing at the receiver's stock metric — see the package doc comment on `internal/telemetry/promsource/promsource.go` for the full reasoning. To actually get a usable feed, an operator needs one of:

- A custom OTel Collector pipeline that promotes `db.server.top_query`'s `postgresql.queryid` (and, ideally, `db.query.text`) attributes onto a metric with per-query cardinality — for example a `transform` processor turning that log record into a metric point, exported by `prometheusexporter` as usual.
- A Prometheus recording rule computed over whatever that custom pipeline exposes, producing the mean-execution-time-in-milliseconds shape `promsource.Source` expects (see below) — keeping the PromQL complexity in Prometheus config rather than in this project's Go code.
- A future `postgresqlreceiver` release that adds per-query attribution to the metric itself, at which point `Config.MetricName`/`Config.QueryIDLabel` point at it directly with no code change here.

This is deliberately staying "bring your own per-query metric" rather than "here's a broken one-liner that looks like it works." See [Roadmap](roadmap.md#opentelemetry-as-a-pluggable-analysis-input-in-detail) for why this is still worth shipping as Phase 1a regardless: the seam is real, and the moment any of the three options above exists in a given fleet, this package is what plugs it in — no changes to `internal/correlation` or to this package itself.

## Value semantics: mean exec time in milliseconds, per point

Whatever metric is configured must report a per-scrape, per-queryid **mean** execution time in milliseconds — the same shape as `pg_stat_statements.mean_exec_time`, which is the only field `internal/correlation.Engine`'s detection math actually reads (`extractLatencies` in `internal/correlation/engine.go`). A raw cumulative counter is the wrong shape and must be turned into a mean *before* Prometheus scrapes it — for example, at the recording-rule layer:

```promql
avg by (queryid) (
  rate(some_per_query_exec_time_seconds_total[5m])
  /
  rate(some_per_query_calls_total[5m])
) * 1000
```

`promsource.Source.SamplesInRange` then reads that recording rule's raw values back with a `query_range` request and maps each returned point to one `collector.QuerySample` directly — it does not itself `rate()` or average anything, exactly mirroring how one `collector.Collector.Scrape()` call stores one raw observation per queryid.

## Configuration

| Field | Default | Notes |
|---|---|---|
| `BaseURL` | *(required)* | Prometheus HTTP API base, e.g. `http://prometheus:9090`. |
| `MetricName` | *(required)* | See above — no default is safe to assume. |
| `QueryIDLabel` | `queryid` | Label carrying the queryid as a base-10 integer string. Always parsed with `strconv.ParseInt`, never through a JSON-number/float path, so values past `2^53` (routine for `pg_stat_statements` queryids, which are `int64`) don't silently lose precision. |
| `QueryTextLabel` | `query` | Optional; set to `-` to disable. Populates `QuerySample.QueryText`/`Fingerprint` for display and for the queryid-rotation fallback described in [Collector Internals](collector-internals.md#queryid-is-not-a-stable-identifier-across-a-deploy) — a queryid-only feed still satisfies `SampleSource` without it. |
| `Step` | `15s` | `query_range` resolution; should match or exceed the underlying scrape interval. |
| `HTTPClient` / `Timeout` | 10s timeout | `HTTPClient` set explicitly takes precedence; set its own `Timeout` in that case. |

## Using it from a PostgresWatch CRD (cmd/manager)

The CRD-driven `cmd/manager` path (`PostgresWatch`) selects this source declaratively via `spec.sampleSource`, instead of any Go code:

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: widgets-prod
spec:
  clusterName: widgets-prod
  sampleSource:
    type: prometheus
    prometheus:
      url: http://prometheus.monitoring.svc:9090
      # A custom recording rule's output, not the stock
      # postgresql_query_execution_time_seconds_total metric — see
      # "The real gap" above for why.
      metricName: pg_regression_radar_query_mean_exec_time_ms
      queryIDLabel: queryid
      queryTextLabel: query
      stepSeconds: 15
  windowMinutes: 30
  minExecutions: 10
```

`dsn`/`dsnSecretRef` are not required, and not read, when `sampleSource.type` is `prometheus` — `internal/controller.PostgresWatchReconciler` skips DSN resolution entirely in that mode (see `usesPrometheusSampleSource` in `internal/controller/postgreswatch_controller.go`). `capturePlans: true` combined with `sampleSource.type: prometheus` is rejected at reconcile time with a clear error, rather than silently ignored: `EXPLAIN`-based plan-diff capture needs the direct database connection this mode deliberately doesn't have. Every other field (`windowMinutes`, `minExecutions`, `alerting`, `autoAbort`, `periodicDetection`, …) works exactly the same regardless of which `sampleSource` is active — the only things this field changes are which `correlation.SampleSource` implementation `internal/correlation.Engine` runs against and whether a direct database connection exists at all.

See [`SampleSourceConfig`](https://github.com/joao00001/pg-regression-radar/blob/main/api/v1alpha1/postgreswatch_types.go) in `api/v1alpha1/postgreswatch_types.go` for the full field reference (`type`, and `prometheus.{url,metricName,queryIDLabel,queryTextLabel,stepSeconds}`), each mirroring `promsource.Config` one-to-one.

## Usage example (direct Go construction)

Outside the CRD path — the standalone `operator` CLI, a custom binary, or a smoke test against a real Prometheus — `internal/telemetry/promsource` is used the same way any other `correlation.SampleSource` implementation is: constructed directly and passed to `correlation.New`, in place of an `*internal/collector.Collector`.

```go
package main

import (
	"log/slog"
	"os"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/telemetry/promsource"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	src, err := promsource.New(promsource.Config{
		BaseURL: "http://prometheus.monitoring.svc:9090",
		// A custom recording rule's output, not the stock
		// postgresql_query_execution_time_seconds_total metric — see
		// "The real gap" above for why.
		MetricName:     "pg_regression_radar_query_mean_exec_time_ms",
		QueryIDLabel:   "queryid",
		QueryTextLabel: "query",
		Step:           15 * time.Second,
	})
	if err != nil {
		logger.Error("configuring promsource", "error", err)
		os.Exit(1)
	}

	engine := correlation.New(correlation.Config{
		WindowMinutes: 30,
	}, src, logger)

	// engine.Analyse(...) / engine.AnalysePeriodic(...) from here on,
	// exactly as with the default internal/collector.Collector-backed
	// SampleSource — see Detection Algorithm.
	_ = engine
}
```

Calling `SamplesInRange`/`AllQueryIDs` directly (e.g. for a smoke test against a real Prometheus) looks like:

```go
now := time.Now()
ids := src.AllQueryIDs()
for _, qid := range ids {
	samples := src.SamplesInRange(qid, now.Add(-30*time.Minute), now)
	logger.Info("samples in range", "queryid", qid, "count", len(samples))
}
```

## Failure behavior

`SamplesInRange` and `AllQueryIDs` return `nil` on any error — unreachable Prometheus, a non-`success` API response, a malformed body, or the wrong `resultType` — rather than an error, matching `correlation.SampleSource`'s signature and the same "unknown queryid returns nothing" contract `internal/collector.Collector`'s own implementation follows. There is currently no metric or log distinguishing "no data in range" from "Prometheus is unreachable"; that is a natural follow-up once this source has real usage.

## See also

- [Roadmap § OpenTelemetry as a pluggable analysis input](roadmap.md#opentelemetry-as-a-pluggable-analysis-input-in-detail) — the phased plan this package is Phase 1a of.
- [Collector Internals](collector-internals.md) — the existing, default `SampleSource` implementation this package is an alternative to, including the `queryid`-fingerprint fallback this package's `QueryTextLabel` also participates in.
- [Detection Algorithm](detection-algorithm.md) — how `SamplesInRange`'s output is actually consumed.
