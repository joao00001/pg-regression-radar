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

## See also

- [Architecture Overview](architecture.md) — where the Correlation Engine sits in the overall pipeline.
- [Collector Internals](collector-internals.md) — how the samples this algorithm consumes are collected and retained.
- [Configuration Reference](configuration.md) — `--window-minutes`, `--min-executions`, `--latency-threshold`, `--changepoint-tolerance`, and the `PostgresWatch` equivalents.
- [Testing](testing.md) — `internal/e2e`'s full-pipeline integration test proves this algorithm against real `pg_stat_statements` data, not synthetic samples.
