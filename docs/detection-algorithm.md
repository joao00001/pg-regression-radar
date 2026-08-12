# Detection Algorithm

*The two-stage statistics behind every `PerformanceRegression`, why both stages have to agree, and how a regression gets a plan-diff attached.*

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

A `PerformanceRegression` says *that* a query got slower and roughly *when*, but not *why* on its own. `--capture-plans` (opt-in, default off) adds a short plan-diff hint by periodically capturing an execution plan for each tracked queryid and diffing the snapshot from just before the detected change point against the most recent one (`internal/planner.Diff`).

### Two sources, in order of preference

There are two ways to get that plan, and they are not equally trustworthy:

| Source | What it captures | Reliability |
|---|---|---|
| `pg_store_plans` (`planner.CapturePlanFromStorePlans`) | The **real** plan PostgreSQL actually used for a past execution of that queryid, read from the [`pg_store_plans`](https://github.com/ossc-db/pg_store_plans) contrib-style extension's `pg_store_plans` view — the same idea as `pg_stat_statements`, but for plans. | High — this is what really ran. |
| `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` (`planner.CaptureGenericPlan`) | An **estimated** plan the planner produces right now, without executing the query, for the normalized query text `pg_stat_statements` recorded. `pg_stat_statements` normalises query text, replacing every literal value with a `$1`/`$2`/... placeholder — a plain `EXPLAIN` cannot run against that text without real bound parameter values, so it fails with "there is no parameter $1". PostgreSQL 16 added `GENERIC_PLAN` specifically to solve this (see [pganalyze's writeup](https://pganalyze.com/blog/5mins-postgres-explain-generic-plan) and [Cybertec's](https://www.cybertec-postgresql.com/en/explain-generic-plan-postgresql-16/)). Requires **PostgreSQL 16+**. | Lower — table statistics, index availability, and even the query's own parameter values may have changed since the original slow execution, so the estimate is not guaranteed to match the plan that actually caused the regression. |

`internal/planner.CapturePlan(ctx, db, queryID, queryText)` is the facade the collector calls: it tries `pg_store_plans` first and falls back to `GENERIC_PLAN` on any failure (extension missing, untrusted, no row yet, or an unexpected database error), returning an error only when **neither** source produces a plan — CloudNativePG supports PostgreSQL 14+, and below PG16 without a usable `pg_store_plans` install, that error is `ErrUnsupportedVersion`, logged once at Info level, and the collector otherwise runs exactly as before. Every `PlanSnapshot` carries a `Source` field (`"pg_store_plans"` or `"generic_plan"`) precisely so a caller — or a Slack alert rendering `Diff`'s output — can flag when a diff is being shown on an estimate rather than a real execution; `Diff` itself appends a caution line whenever either side of the comparison is `"generic_plan"`.

### Why `pg_store_plans` isn't always trusted just because it's installed

`pg_store_plans`'s `queryid` column is, in current releases, the same core-generated query identifier `pg_stat_statements` uses — the upstream docs state this plainly and even show `JOIN pg_store_plans USING (queryid)` against `pg_stat_statements` as the intended usage. That compatibility is *not* unconditional, though, so `internal/planner` (`detectStorePlans` in `storeplans.go`) checks three things before trusting a `pg_store_plans` queryid enough to correlate it with a `pg_stat_statements` queryid from the collector:

1. **Extension version >= 1.6.** Per the [pg_store_plans 1.6 release notes](https://github.com/ossc-db/pg_store_plans/releases/tag/1.6), that release "removed the column `queryid_stat_statements` and now the column `queryid` shows the core generated query id." Before 1.6, `pg_store_plans` computed its own `queryid` with its own algorithm and needed the separate `queryid_stat_statements` column specifically to cross-reference `pg_stat_statements` — meaning a pre-1.6 `queryid` is not safe to join directly. (The upstream HTML docs still contain a sentence claiming `queryid` "is calculated ... in a different algorithm" than `pg_stat_statements`, which reads as leftover pre-1.6 prose that contradicts the same page's own column reference table; we treat the release notes and column table as authoritative and the stray sentence as a documentation bug, but fail closed on old versions regardless.)
2. **Server is PostgreSQL 14+.** Upstream's docs explicitly scope the queryid-join guarantee to "PostgreSQL 14 or later" — consistent with 14 being the first version with the core `compute_query_id` GUC that `pg_store_plans` >= 1.6 relies on.
3. **`compute_query_id` is not `off`.** Upstream: "`pg_store_plans` requires the GUC variable `compute_query_id` to be 'on' or 'auto'. If it is set to [off], `pg_store_plans` is silently disabled." A disabled extension can still be present in `pg_extension` and even still hold stale rows, so this is checked explicitly rather than inferred from installation alone.

If any of these don't hold, `CapturePlanFromStorePlans` returns `ErrQueryIDUnreliable` (with a specific reason) instead of a snapshot, and `CapturePlan` falls back to `GENERIC_PLAN`. This is a defensive, documented choice rather than a verified guarantee for every possible `pg_store_plans` deployment — it has not been validated against a real `pg_store_plans` installation (see [Testing](testing.md) and the caveat in this project's own commit history for `internal/planner`); the version/GUC gating above is the concrete, citable mitigation for the compatibility risk a third-party review raised, not a substitute for validating against the real extension.

### What it captures, and how much

Once per scrape cycle (not once per query — this stays bounded), the collector runs `CapturePlan` for every query already in that cycle's `pg_stat_statements` read (the same top-500 set the scrape itself uses, no separate query universe) and retains the last 5 plan snapshots per `queryid` in a small in-memory ring — plans change far less often than latency samples, so this doesn't need `--retention-minutes`' time-based mechanism.

When a regression is `Detected`, the operator's poll loop looks up the plan snapshot closest before `DetectedChangeAt` and the most recent one since, and diffs them: whether the root plan node changed, whether the estimated cost moved meaningfully, and — per the caution line above — whether either side is only an estimate. The result is attached to the `PerformanceRegression` as `PlanDiffSummary` and included in the Slack message when non-empty.

### Honest limitations

- **`GENERIC_PLAN`, when that's the source actually used, is an estimate, not a real one.** It reflects the planner's default, parameter-independent cost estimate — it can differ from the plan Postgres would actually choose for a real, skewed parameter value (e.g. a highly selective vs. a common value for the same column). Treat a `"generic_plan"`-sourced diff as a hint pointing you toward `EXPLAIN ANALYZE`-ing the real query yourself, not a substitute for it. This caveat does not apply when `Source` is `"pg_store_plans"`, since that plan is what actually ran.
- **No real `auto_explain` integration.** This deliberately does not ingest real `auto_explain` log output, which would give a third, independent way to recover a real per-parameter plan. That remains a heavier lift (a log-shipping pipeline) left as a documented follow-up — see [Roadmap](roadmap.md).

## Deduplicating a queryid rotation

`pg_stat_statements`' `queryid` is not guaranteed stable across a deploy (see [Collector Internals](collector-internals.md#queryid-is-not-a-stable-identifier-across-a-deploy)), so `AllQueryIDs()` can enumerate both the pre-rotation and post-rotation queryid for what is really a single query. Once the collector's fingerprint-merge fallback has kicked in for either side, `SamplesInRange` for both queryids returns the identical merged sample set — so evaluating both, unguarded, would run the two-stage pipeline above twice on the same data and emit two `PerformanceRegression`s for one real regression.

`Engine.Analyse` prevents this: before evaluating a queryid, it looks at the `Fingerprint` of the most recently recorded sample in its (possibly merged) sample set. The first queryid — in sorted, deterministic order — to surface a given fingerprint is evaluated; every later queryid sharing that fingerprint is skipped, since it would only re-evaluate the same merged data. The one `PerformanceRegression` that *is* produced is reported under whichever queryid most recently received a sample (not necessarily the queryid that happened to win the dedup check), so it points at the "live" identity still receiving traffic in `pg_stat_statements` rather than one that may have already aged out.

This guarantees at most one `PerformanceRegression` per distinct query per `Analyse` call, regardless of how many queryids `pg_stat_statements` currently associates with it.

## See also

- [Architecture Overview](architecture.md) — where the Correlation Engine sits in the overall pipeline.
- [Collector Internals](collector-internals.md) — how the samples this algorithm consumes are collected and retained.
- [Configuration Reference](configuration.md) — `--window-minutes`, `--min-executions`, `--latency-threshold`, `--changepoint-tolerance`, `--capture-plans`, and the `PostgresWatch` equivalents.
- [Testing](testing.md) — `internal/e2e`'s full-pipeline integration test proves this algorithm against real `pg_stat_statements` data, not synthetic samples.
