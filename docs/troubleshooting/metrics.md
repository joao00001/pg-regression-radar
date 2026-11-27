# Metrics

*Every Prometheus metric the radar exports, with its type, its labels and what a value tells you.*

## Overview

Metrics answer "how much, and since when", where a log line answers "what happened". They are the signal to alert on, and the one to graph over days. A test compares this page with the source on every build, so a metric that is exported and not listed here, or listed and not exported, or whose type or labels differ, fails it.

All names start with `pg_regression_radar_`. Most carry a constant `cluster` label with the name of the watched cluster, which is not repeated in the rows. The `Labels` column lists the labels that vary.

## Where to read them

- **Postgres and attribution metrics.** The manager serves them per watch on `--pg-metrics-bind-address` (default `:9090`), at `/metrics`. Only the leader serves them.
- **Controller-runtime metrics** (reconcile counts and durations, work queue depth, client request rates) are served on `--metrics-bind-address`, which is `0` (off) by default. They are not listed here because they are not the radar's.
- **Standalone collector and all-in-one operator.** `--listen` on the `collector` command, and the operator's metrics address, serve the collector metrics.

```bash
kubectl -n <ns> port-forward deploy/<manager> 9090:9090
curl -s localhost:9090/metrics | grep '^pg_regression_radar_'
```

## Collector

| Metric | Type | Labels | What it tells you |
|---|---|---|---|
| `pg_regression_radar_collector_scrapes_total` | counter | | Scrapes of `pg_stat_statements` performed. It should rise once per scrape interval; flat means the collector is not scraping (see [`ScrapeStale`](reasons.md#scrapestale)). |
| `pg_regression_radar_collector_scrape_errors_total` | counter | | Scrapes that failed. Compare its rate with the scrapes counter; a rising rate means the log has `collector: scrape failed` lines. |
| `pg_regression_radar_collector_tracked_queries` | gauge | | Distinct query ids held in memory. Zero after the first scrapes means nothing is being collected. |
| `pg_regression_radar_collector_retained_samples_total` | gauge | | Samples currently held in memory across all queries. It grows until retention is reached, then stays flat. |
| `pg_regression_radar_collector_stats_resets_detected_total` | counter | | Times a query's counters went backwards (a reset, an eviction or a crash). Frequent resets mean something is calling `pg_stat_statements_reset()` or evicting entries. |
| `pg_regression_radar_collector_explain_statements_skipped_total` | counter | | Rows skipped because their text is itself an `EXPLAIN`. A sustained rate points at something other than the radar issuing raw `EXPLAIN`s against the database. |
| `pg_regression_radar_query_mean_exec_time_ms` | gauge | `queryid` | Mean execution time of a query over its whole lifetime in `pg_stat_statements`, at the last scrape. |
| `pg_regression_radar_query_interval_mean_exec_time_ms` | gauge | `queryid` | Mean execution time of the calls made since the previous scrape. This is the series the radar judges; it is absent when the interval is unknown or had no calls. |
| `pg_regression_radar_query_calls_total` | gauge | `queryid` | Cumulative calls from `pg_stat_statements`. It is a gauge because it is copied from Postgres, and goes down after a reset. |

## Attribution engine

| Metric | Type | Labels | What it tells you |
|---|---|---|---|
| `pg_regression_radar_attribution_shadow_decisions_total` | counter | `action` `grade` | Decisions the engine reached in shadow mode, which were never executed. `action` is what it would have done. |
| `pg_regression_radar_attribution_actions_total` | counter | `action` `result` | Pauses and aborts of Argo Rollouts, by `action` (`pause`, `abort`) and `result` (`done`, `failed`, `skipped`). `skipped` is a decision the engine did not carry out (namespace not allowed, kill switch, not a Rollout); `failed` matches an [`ActuationFailed`](reasons.md#actuationfailed) log line. |
| `pg_regression_radar_attribution_verifications_total` | counter | `result` | Checks made after an abort: `recovered` or `persisted`. Persisted ones count toward the circuit breaker. |
| `pg_regression_radar_attribution_breaker_open` | gauge | | `1` while the circuit breaker is open and the engine only alerts ([`BreakerOpen`](reasons.md#breakeropen)). A good alert: it needs a person. |
| `pg_regression_radar_attribution_revert_proposals_total` | counter | `result` | Draft revert pull requests, by `result`: `opened`, `already-proposed`, `skipped` (a guard declined) or `failed` (the GitHub call failed). |

## Arms: the Postgres log behind grade A

| Metric | Type | Labels | What it tells you |
|---|---|---|---|
| `pg_regression_radar_attribution_arms_usable` | gauge | | `1` when the Postgres log is flowing and its settings allow grade A evidence. `0` is the same condition as `ArmsLog` being `False`; see [`ArmsSettingsRejected`](reasons.md#armssettingsrejected), [`ArmsNoLogYet`](reasons.md#armsnologyet) and [`ArmsLogStale`](reasons.md#armslogstale). |
| `pg_regression_radar_attribution_arms_records_total` | counter | | Statement executions read from the Postgres log. Flat while `arms_usable` is `1` means the workload is idle. |
| `pg_regression_radar_attribution_arms_dropped_total` | counter | | Executions dropped because the log had too many distinct `application_name` and query pairs. Rising means arm evidence is being thinned. |

## Detection and alerting

| Metric | Type | Labels | What it tells you |
|---|---|---|---|
| `pg_regression_radar_regressions_detected_total` | counter | `trigger` `cluster` | Regressions that passed every test, by `trigger` (`deploy` or `periodic`) and the watched `cluster`. |
| `pg_regression_radar_pending_deploy_events` | gauge | | Deploy events still being retried until their analysis window closes. A value that stays high means deploys keep arriving before the radar has enough history. |
| `pg_regression_radar_periodic_regressions_in_progress` | gauge | | Queries currently suppressed under an already-reported periodic regression. |
| `pg_regression_radar_notifications_total` | counter | `status` `format` | Alert delivery attempts, by final `status` (`success` or `error`) and payload `format`. A rising `error` count matches [`AlertNotifyFailed`](reasons.md#alertnotifyfailed). |

## Exporters and sources

| Metric | Type | Labels | What it tells you |
|---|---|---|---|
| `pg_regression_radar_otlp_export_total` | counter | `result` | Regressions handed to the OTLP log exporter, by `result` (`success`, `failed`, `dropped`). |
| `pg_regression_radar_promsource_query_errors_total` | counter | `operation` | Failed queries against the Prometheus sample source, by `operation` (`samples_in_range`, `all_query_ids`). Any rise means the engine may see no samples ([`NoSamples`](reasons.md#nosamples)). |

## Useful alerts

```yaml
- alert: PgrrBreakerOpen
  expr: pg_regression_radar_attribution_breaker_open == 1
  for: 5m
- alert: PgrrNotScraping
  expr: rate(pg_regression_radar_collector_scrapes_total[10m]) == 0
  for: 10m
- alert: PgrrArmsUnusable
  expr: pg_regression_radar_attribution_arms_usable == 0
  for: 15m
- alert: PgrrAlertsNotDelivered
  expr: increase(pg_regression_radar_notifications_total{status="error"}[1h]) > 0
```

## See also

- [Where to look](where-to-look.md): which signal answers which question.
- [Log catalog](log-catalog.md) and [Reason codes](reasons.md).
