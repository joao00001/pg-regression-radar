# Nothing is detected

*The radar runs without errors and never reports a regression: how to tell "nothing regressed" from "it cannot see".*

## Overview

Silence has two very different causes. Either nothing got slower, or the radar is not in a position to tell. Check in this order: is it reading the database, does it hold enough history, and is the deploy the radar judges one it knows about.

## 1. Is it reading the database?

```bash
kubectl get pgw <name> -n <ns>
kubectl get pgw <name> -n <ns> -o json | jq '.status | {phase, trackedQueryIds, lastScrapeTime, conditions: [.conditions[] | {type, status, reason}]}'
```

| You see | It means | Go to |
|---|---|---|
| `Phase: Failed` | the watch's worker never started | [`DSNUnresolved`](reasons.md#dsnunresolved), [`SecretsUnresolved`](reasons.md#secretsunresolved), [`WorkerStartFailed`](reasons.md#workerstartfailed) |
| `Collecting` is `False` with `NoScrapeYet` | no scrape has completed since the worker started | [`NoScrapeYet`](reasons.md#noscrapeyet) |
| `Collecting` is `False` with `ScrapeStale` | scrapes stopped or are failing | [`ScrapeStale`](reasons.md#scrapestale) |
| `Collecting` is `True` and `Tracked Queries` is `0` | it connects but sees no statements | [Setup and the database](setup.md) (extension, permissions) |
| `Collecting` is `True` and queries are tracked | it is reading; continue below | |

The same facts are metrics: `pg_regression_radar_collector_scrapes_total` should rise once per interval, `..._collector_scrape_errors_total` should not, and `..._collector_tracked_queries` should be above zero. See [Metrics](metrics.md#collector).

For the one-binary operator there are no conditions; read its log for `collector: scrape failed` and run it with `--dry-run`.

## 2. Does it have enough to compare?

If the radar runs without errors but never reports a regression, check these in order:

1. **The counters were reset.** After `pg_stat_statements_reset()` or a server restart, the radar logs `collector: pg_stat_statements counters restarted for query; using activity since the reset`. It then needs new samples before it can compare windows.
2. **Not enough data yet.** A window needs `minSamples` scrape intervals with activity and `minExecutions` calls. See [Detection algorithm](../detection-algorithm.md).
3. **The query is outside the top 500.** Each scrape reads the 500 busiest statements.
4. **Its own queries are excluded.** Statements containing `pg_stat_statements` are filtered out on purpose.

In Kubernetes, `PerformanceRegression` objects report `Detected`, `NoRegression` or `InsufficientData`; the last one means case 2.

To see why a query was not reported, run the manager at `--log-level=debug` for one deploy and read the `correlation:` lines for its query: `correlation: insufficient data`, `correlation: no change point found`, `correlation: t-test result` (with the `p_value`) and `correlation: detection did not survive multiple-testing` each say which test stopped it. See the [log catalog](log-catalog.md#correlation-engine).

## 3. Does it know about the deploy?

A deploy-triggered check needs a deploy event. If regressions are found only by the periodic check, or never, the deploy may not be reaching the radar: see [A deploy never shows up](deploy-sources.md#a-deploy-never-shows-up).

A deploy that arrives before the radar holds enough history is not lost: it is retried until its analysis window closes. `pg_regression_radar_pending_deploy_events` counts them, and `correlation: deploy event registered for retry` and `correlation: deploy event's analysis window elapsed` bracket the wait.

## 4. It detected something and nothing arrived

A regression that passed every test is written as a `PerformanceRegression`, counted in `pg_regression_radar_regressions_detected_total`, and sent to the alert destination. If the object exists and the message did not arrive, see [Alerts and the dashboard](alerts-and-dashboard.md).

## See also

- [Where to look](where-to-look.md)
- [Detection algorithm](../detection-algorithm.md) and [Collector internals](../collector-internals.md)
