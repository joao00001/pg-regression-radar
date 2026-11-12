# Collector Internals

*Row identity, retention behaviour, `queryid` instability, and PostgreSQL version handling — the details worth knowing when tuning `--retention-minutes` or debugging a "missing data" report.*

## Overview

The collector (`internal/collector`) keeps a bounded, in-memory time series of `pg_stat_statements` observations per `queryid`. Before combining rows into that series, it calculates intervals independently for each statistics entry. Retention is bounded, `queryid` is not stable across every deploy, and column availability varies by PostgreSQL major version.

## Row identity before aggregation

`pg_stat_statements` keeps separate cumulative counters for `(dbid, userid, queryid, toplevel)`. Two roles running the same query can therefore return two rows with the same `queryid`. The collector subtracts each row's own previous counters first, then sums interval calls and execution time into one sample per `queryid` per scrape. Its interval mean is summed execution time divided by summed calls; it is not an unweighted average of the rows' means. A reset of one role does not restart another role's counters.

The first scrape, a changed set of rows, or an expired predecessor leaves that query's interval unknown. Such samples are excluded from interval analysis even when every interval in a window is unknown. Missing rows are forgotten after a successful scrape, so a returning row establishes a new baseline. A failed or incomplete scrape does not advance any row's predecessor.

The SQL still selects the **500 rows** with the greatest cumulative execution time, not 500 distinct query IDs. Several roles can occupy slots for one query; aggregation can therefore yield fewer than 500 samples. Changes at that selection boundary can leave an interval unknown. The row-predecessor cache holds only the last successful scrape and is bounded by the same row limit.

## Retention, not an unbounded log

Every scrape, samples older than `now - RetentionDuration` are pruned (a single reslice per queryid, since samples are always appended in chronological order — no O(n²) scan), and any queryid left with zero samples (e.g. one that fell out of `pg_stat_statements`'s top-500 `ORDER BY total_exec_time DESC LIMIT 500`) is removed from the map entirely, so both the data volume and the number of tracked keys stay bounded under long-running, continuous scraping.

The default of **180 minutes** is 3x the full before+after span of the default 30-minute analysis window (2 x 30 = 60 min), giving headroom for delayed deploy-event ingestion/processing and for the queryid-fingerprint fallback below to have data on both sides of a rotation. If you run with a larger `--window-minutes`, size `--retention-minutes` to stay comfortably above `2 * window-minutes`.

Two Prometheus gauges expose current state:

- `pg_regression_radar_collector_tracked_queries` — distinct queryids retained.
- `pg_regression_radar_collector_retained_samples_total` — total samples retained across all queryids.
- `pg_regression_radar_collector_explain_statements_skipped_total` — `pg_stat_statements` rows skipped because their query text is itself an `EXPLAIN` statement, so it's never retained as a sample or handed to plan capture. See [Detection Algorithm § Excluding capture's own footprint from tracking](detection-algorithm.md#excluding-captures-own-footprint-from-tracking) for why these rows exist at all and why they're excluded.

## `queryid` is not a stable identifier across a deploy

Per the [PostgreSQL documentation](https://www.postgresql.org/docs/current/pgstatstatements.html), `queryid` "is not safe to assume... will be stable across major versions of PostgreSQL", and it also changes if a referenced object (e.g. a table touched by a migration) is dropped and recreated between executions — which can happen in the middle of the very deploy this tool is trying to analyze.

The collector guards against this by also computing a text-normalized `FingerprintQuery` hash (see `internal/collector/fingerprint.go`) for every sample. When `SamplesInRange(queryID, from, to)` finds few samples directly under `queryID` in range, it additionally merges in same-range samples recorded under any other queryid that currently shares that fingerprint, so a query whose `queryid` rotated mid-window doesn't look like it has "no data" on one side of the rotation. This is fully internal to the collector: the public `SampleSource` interface consumed by `internal/correlation` (`SamplesInRange`/`AllQueryIDs`) is unchanged.

Because the collector truncates `QueryText` to `--max-query-text-len` before fingerprinting, extremely small values for that flag increase the chance that distinct queries share the same truncated prefix and therefore the same fallback fingerprint. The default (`200`) leaves plenty of shape information intact; values below roughly 20 characters are where collision risk becomes materially easier to trigger in practice.

!!! note "Deduplicated at the correlation-engine layer"
    `AllQueryIDs()` still lists the old and new queryids separately, so a rotated query is still analyzed once per queryid, each pulling in the same merged data — but `internal/correlation.Engine.Analyse` now recognizes this by fingerprint and reports it at most once, under whichever queryid most recently received a sample (the "live" identity). See [Detection Algorithm](detection-algorithm.md#deduplicating-a-queryid-rotation) for how.

## `pg_stat_statements` column names vary by PostgreSQL major version

PostgreSQL 13 split the old `total_time`/`mean_time` columns into `total_plan_time`/`total_exec_time` and `mean_plan_time`/`mean_exec_time` (compare the [PG12](https://www.postgresql.org/docs/12/pgstatstatements.html) and [PG13](https://www.postgresql.org/docs/13/pgstatstatements.html) column references).

CloudNativePG's currently supported releases only ship PostgreSQL 14+ (see [Supported releases](https://cloudnative-pg.io/docs/devel/supported_releases)), and PostgreSQL 13 itself reached community end-of-life on 2025-11-13, so a query hard-coded to the modern column names is already safe for every CloudNativePG-managed cluster in support today. The collector nonetheless detects `server_version_num` once at startup and falls back to the legacy column names below PostgreSQL 13, so it also works against older or self-managed Postgres instances.

## Counters, resets and interval latency

Version detection enables `toplevel` on PostgreSQL 14+ and `stats_since` on PostgreSQL 17+. Before PostgreSQL 14, row identity uses the three available fields and a constant `toplevel=true`. If version lookup fails, the scrape fails and retries lookup on the next attempt rather than silently combining top-level and nested entries.

`calls` and `total_exec_time` accumulate until the entry is reset or evicted, so the collector stores each scrape's cumulative values and derives the latency of the calls made since the previous scrape. On PostgreSQL 17+ it also reads `stats_since` to recognise a reset that the counters alone would not reveal. See [Detection Algorithm](detection-algorithm.md#what-a-latency-sample-is) for the formula, the reset rules and their limits.

## See also

- [Detection Algorithm](detection-algorithm.md) — how the samples described here are consumed by the Correlation Engine.
- [Configuration Reference](configuration.md) — `--retention-minutes` and related flags.
- [Persistence](persistence.md) — the optional durable copy of these same samples in Postgres.
- [OpenTelemetry / Prometheus Sample Source](otel-prometheus-source.md) — an alternative `SampleSource` implementation, for fleets that already run an OTel Collector.
