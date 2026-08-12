# Detection Algorithm

*The two-stage statistics behind every `PerformanceRegression`, and why both stages have to agree.*

## Overview

The Correlation Engine (`internal/correlation`) doesn't just compare a before/after mean — a naive comparison alone is prone to false positives whenever an unrelated latency shift happens to overlap the analysis window. This page explains the real two-stage pipeline it runs instead, inspired by [Hunter — DataStax Labs](https://github.com/datastax-labs/hunter) and the accompanying ICPE'23 paper, ["Hunter: Using Change Point Detection to Hunt for Performance Regressions"](https://arxiv.org/abs/2301.03034).

## Stage 0: cheap pre-filter

The naive mean latency over the whole pre-deploy window is compared against the naive mean over the whole post-deploy window. If the relative change is below `LatencyChangeThreshold` (default 20%), the query is rejected immediately without running the more expensive stages below.

## Stage 1: E-divisive means (locate)

For queries that pass the pre-filter, the pre- and post-deploy samples are merged into a single chronologically-ordered series covering the whole analysis window, and the E-divisive means algorithm (energy-statistic based change-point detection; Matteson & James, 2014, JASA) locates the single most significant change point in that series.

Crucially, this does **not** assume the shift happens exactly at the deploy timestamp — rolling updates, connection draining, and collector scrape lag all delay the observable effect of a deploy by anywhere from seconds to a few minutes. The located change point must fall within `ChangePointTolerance` of the deploy timestamp (default: 20% of the analysis window, floored at 2 minutes) to be considered related to that deploy at all.

If E-divisive finds no change point, or the one it finds is too far from the deploy, the query is marked `NoRegression` — even if the naive pre/post means from stage 0 looked like a regression.

## Stage 2: Welch's t-test (confirm)

The two segments actually defined by the change point E-divisive found (not the naive deploy-timestamp split) are compared with Welch's t-test to confirm the difference in means is statistically significant (configurable p-value threshold, default p < 0.05). `ConfidenceScore` is derived from this p-value.

## Both stages must agree

A `PerformanceRegression` is only marked `Detected` when the change point is found, lies near the deploy, **and** the t-test on the real segments is significant. This two-stage design prioritises **precision over recall** to avoid alert fatigue.

The change point's own timestamp is preserved on the result as `DetectedChangeAt`, so you can see exactly when the shift was located — which may differ slightly from the deploy event's timestamp, within the configured tolerance.

## Plan-diff correlation (optional)

A detected latency regression tells you *that* a query got slower and roughly *when*, but not *why*. `--capture-plans` (opt-in, default off) adds a short plan-diff hint by periodically snapshotting each tracked query's execution plan and diffing the snapshot from just before the detected change point against the most recent one.

### Why `EXPLAIN (GENERIC_PLAN)`, specifically

`pg_stat_statements` normalises query text, replacing every literal value with a `$1`/`$2`/... placeholder. A plain `EXPLAIN` cannot run against that text without real bound parameter values — which `pg_stat_statements` never records — so it fails with "there is no parameter $1". PostgreSQL 16 added `EXPLAIN`'s `GENERIC_PLAN` option specifically to solve this: it plans the query using the planner's normal type inference and default, parameter-independent selectivity estimates, without requiring any real values (see [pganalyze's writeup](https://pganalyze.com/blog/5mins-postgres-explain-generic-plan) and [Cybertec's](https://www.cybertec-postgresql.com/en/explain-generic-plan-postgresql-16/)). `internal/planner.CapturePlan` uses exactly this, requiring **PostgreSQL 16+**. CloudNativePG supports PostgreSQL 14+, so this feature is simply unavailable on some clusters — below PG16, this is logged once at Info level and the collector otherwise runs exactly as before.

### What it captures, and how much

Once per scrape cycle (not once per query — this stays bounded), the collector runs `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` for every query already in that cycle's `pg_stat_statements` read (the same top-500 set the scrape itself uses, no separate query universe) and retains the last 5 plan snapshots per `queryid` in a small in-memory ring — plans change far less often than latency samples, so this doesn't need `--retention-minutes`' time-based mechanism.

When a regression is `Detected`, the operator's poll loop looks up the plan snapshot closest before `DetectedChangeAt` and the most recent one since, and diffs them (`internal/planner.Diff`): whether the root plan node changed (e.g. "root plan node changed from Index Scan to Seq Scan") and whether the estimated cost moved by more than ~10% (e.g. "estimated cost increased 4.2x"). The result is attached to the `PerformanceRegression` as `PlanDiffSummary` and included in the Slack message when non-empty.

### Honest limitations

- **Generic plans, not real ones.** A `GENERIC_PLAN` reflects the planner's default, parameter-independent cost estimate — it can differ from the plan Postgres would actually choose for a real, skewed parameter value (e.g. a highly selective vs. a common value for the same column). Treat the diff as a hint pointing you toward `EXPLAIN ANALYZE`-ing the real query yourself, not a substitute for it.
- **No real auto_explain/pg_store_plans integration.** This deliberately does not ingest real `auto_explain` log output or integrate the `pg_store_plans` extension, both of which would give real, per-parameter plans. Either is a heavier lift (a log-shipping pipeline, or an extra Postgres extension most clusters won't have installed) left as a documented follow-up — see [Roadmap](roadmap.md).

## See also

- [Architecture Overview](architecture.md) — where the Correlation Engine sits in the overall pipeline.
- [Collector Internals](collector-internals.md) — how the samples this algorithm consumes are collected and retained.
- [Configuration Reference](configuration.md) — `--window-minutes`, `--min-executions`, `--latency-threshold`, `--changepoint-tolerance`, `--capture-plans`, and the `PostgresWatch` equivalents.
- [Testing](testing.md) — `internal/e2e`'s full-pipeline integration test proves this algorithm against real `pg_stat_statements` data, not synthetic samples.
