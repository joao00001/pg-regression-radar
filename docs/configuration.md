# Configuration Reference

*Every flag and CRD field across all four binaries, with defaults.*

## Overview

This page is the single reference for configuring pg-regression-radar, regardless of which binary or CRD you're using — see [Architecture Overview](architecture.md) for which one applies to your setup. Every table documents the default, including when the default is "*(required)*" or an empty string, since an absent default is itself information.

## Operator flags (`cmd/operator`)

| Flag | Default | Description |
|---|---|---|
| `--dsn` | *(required)* | Postgres connection string |
| `--cluster-name` | `default` | Label added to all metrics |
| `--namespace` | `default` | Kubernetes namespace label |
| `--scrape-interval` | `60s` | How often to read `pg_stat_statements` |
| `--webhook-listen` | `:8080` | Deploy-event webhook listen address |
| `--metrics-listen` | `:9090` | Prometheus metrics listen address |
| `--slack-url` | `` | Slack incoming-webhook URL |
| `--source-type` | `generic` | `argocd`, `argo-rollouts`, `flux`, `generic` |
| `--window-minutes` | `30` | Analysis window (minutes before/after deploy) |
| `--min-executions` | `10` | Min query executions per window |
| `--latency-threshold` | `0.20` | Min relative latency increase to flag (e.g. 0.20 = 20%) |
| `--retention-minutes` | `180` | How long the collector keeps in-memory query samples before pruning them — see [Collector Internals](collector-internals.md) |
| `--changepoint-tolerance` | `0` (auto) | Max distance between the E-divisive change point and the deploy timestamp still attributed to that deploy (0 = auto: 20% of window, floor 2m) |
| `--state-backend` | `memory` | State persistence backend: `memory` or `postgres` — see [Persistence](persistence.md) |
| `--state-dsn` | `` | Postgres DSN for the state backend when `--state-backend=postgres` (defaults to `--dsn`) |
| `--state-retention` | `168h` (7 days) | How long samples/events are kept in the postgres state backend |
| `--state-prune-interval` | `15m` | How often the retention sweep runs against the postgres state backend |

## Standalone collector flags (`cmd/collector`)

The `collector` binary exposes the scraper on its own, without the correlation/alerting/webhook pieces:

| Flag | Default | Description |
|---|---|---|
| `--dsn` | *(required)* | Postgres connection string |
| `--scrape-interval` | `60s` | How often to read `pg_stat_statements` |
| `--cluster-name` | `default` | Label added to all metrics |
| `--namespace` | `default` | Kubernetes namespace label |
| `--listen` | `:9090` | Prometheus metrics listen address |
| `--retention-minutes` | `180` | How long to keep in-memory query samples before pruning them |

## Manager flags (`cmd/manager`)

| Flag | Default | Description |
|---|---|---|
| `--leader-elect` | `true` | Enable leader election (multi-replica HA) |
| `--leader-election-namespace` | *(auto)* | Namespace the `coordination.k8s.io` Lease is created in |
| `--metrics-bind-address` | `0` (disabled) | controller-runtime's own reconcile/workqueue metrics |
| `--pg-metrics-bind-address` | `:9090` | Aggregated `pg_stat_statements`-derived metrics (leader only) |
| `--webhook-bind-address` | `:8080` | Deploy-event webhooks, one route per `DeploySource` CR (leader only) |
| `--health-probe-bind-address` | `:8081` | `/healthz` and `/readyz` |

## `PostgresWatch` spec fields

| Field | Default | Description |
|---|---|---|
| `clusterName` | *(required)* | Label added to metrics and to `PerformanceRegression` CRs |
| `dsn` / `dsnSecretRef` | *(one required)* | Postgres DSN, inline or via a Secret key (preferred) |
| `scrapeIntervalSeconds` | `60` | How often to read `pg_stat_statements` |
| `windowMinutes` | `30` | Analysis window (minutes before/after deploy) |
| `minExecutions` | `10` | Min query executions per window |
| `latencyChangeThreshold` | `"0.20"` | Min relative latency increase to flag (e.g. `"0.20"` = 20%) |
| `pValueThreshold` | `"0.05"` | Welch's t-test significance cutoff |
| `criticalQueryIDs` | `[]` | Queries that bypass `minExecutions` |
| `slackWebhookUrl` | `""` | Slack incoming-webhook URL for this watch |

## `DeploySource` spec fields

| Field | Default | Description |
|---|---|---|
| `postgresWatchRef` | *(required)* | Name of the `PostgresWatch` (same namespace) to correlate against |
| `sourceType` | `generic` | `argocd`, `argo-rollouts`, `flux`, `generic` |
| `appName` | `""` (all apps) | Narrow correlation to a single application |

## See also

- [Getting Started](getting-started.md) — these flags/fields used in full runnable commands.
- [Detection Algorithm](detection-algorithm.md) — what `--window-minutes`, `--min-executions`, `--latency-threshold`, and `--changepoint-tolerance` actually control.
- [Persistence](persistence.md) — the `--state-*` flags in depth.
- [Deploy Sources & Webhooks](webhooks.md) — `--source-type` and `sourceType` per webhook source.
