# Collector Internals

*Retention behaviour, `queryid` instability, and PostgreSQL version handling — the details worth knowing when tuning `--retention-minutes` or debugging a "missing data" report.*

## Overview

The collector (`internal/collector`) keeps a bounded, in-memory time series of `pg_stat_statements` observations per `queryid`. Three properties of that design have real operational consequences, covered below: retention is bounded (not an unbounded log), `queryid` is not a stable identifier across a deploy, and `pg_stat_statements`'s column names vary by PostgreSQL major version.

## Retention, not an unbounded log

Every scrape, samples older than `now - RetentionDuration` are pruned (a single reslice per queryid, since samples are always appended in chronological order — no O(n²) scan), and any queryid left with zero samples (e.g. one that fell out of `pg_stat_statements`'s top-500 `ORDER BY total_exec_time DESC LIMIT 500`) is removed from the map entirely, so both the data volume and the number of tracked keys stay bounded under long-running, continuous scraping.

The default of **180 minutes** is 3x the full before+after span of the default 30-minute analysis window (2 x 30 = 60 min), giving headroom for delayed deploy-event ingestion/processing and for the queryid-fingerprint fallback below to have data on both sides of a rotation. If you run with a larger `--window-minutes`, size `--retention-minutes` to stay comfortably above `2 * window-minutes`.

Two Prometheus gauges expose current state:

- `pg_regression_radar_collector_tracked_queries` — distinct queryids retained.
- `pg_regression_radar_collector_retained_samples_total` — total samples retained across all queryids.

## `queryid` is not a stable identifier across a deploy

Per the [PostgreSQL documentation](https://www.postgresql.org/docs/current/pgstatstatements.html), `queryid` "is not safe to assume... will be stable across major versions of PostgreSQL", and it also changes if a referenced object (e.g. a table touched by a migration) is dropped and recreated between executions — which can happen in the middle of the very deploy this tool is trying to analyze.

The collector guards against this by also computing a text-normalized `FingerprintQuery` hash (see `internal/collector/fingerprint.go`) for every sample. When `SamplesInRange(queryID, from, to)` finds few samples directly under `queryID` in range, it additionally merges in same-range samples recorded under any other queryid that currently shares that fingerprint, so a query whose `queryid` rotated mid-window doesn't look like it has "no data" on one side of the rotation. This is fully internal to the collector: the public `SampleSource` interface consumed by `internal/correlation` (`SamplesInRange`/`AllQueryIDs`) is unchanged.

!!! warning "Known limitation"
    `AllQueryIDs()` still lists the old and new queryids separately, so a rotated query may be analyzed (and, if flagged, reported) once per queryid, each pulling in the same merged data.

## `pg_stat_statements` column names vary by PostgreSQL major version

PostgreSQL 13 split the old `total_time`/`mean_time` columns into `total_plan_time`/`total_exec_time` and `mean_plan_time`/`mean_exec_time` (compare the [PG12](https://www.postgresql.org/docs/12/pgstatstatements.html) and [PG13](https://www.postgresql.org/docs/13/pgstatstatements.html) column references).

CloudNativePG's currently supported releases only ship PostgreSQL 14+ (see [Supported releases](https://cloudnative-pg.io/docs/devel/supported_releases)), and PostgreSQL 13 itself reached community end-of-life on 2025-11-13, so a query hard-coded to the modern column names is already safe for every CloudNativePG-managed cluster in support today. The collector nonetheless detects `server_version_num` once at startup and falls back to the legacy column names below PostgreSQL 13, so it also works against older or self-managed Postgres instances.

## See also

- [Detection Algorithm](detection-algorithm.md) — how the samples described here are consumed by the Correlation Engine.
- [Configuration Reference](configuration.md) — `--retention-minutes` and related flags.
- [Persistence](persistence.md) — the optional durable copy of these same samples in Postgres.
