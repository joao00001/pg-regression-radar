# Configuration Reference

*Every flag and CRD field across all four binaries, with defaults.*

## Overview

This page is the single reference for configuring pg-regression-radar, regardless of which binary or CRD you're using — see [Architecture Overview](architecture.md) for which one applies to your setup. Every table documents the default, including when the default is "*(required)*" or an empty string, since an absent default is itself information.

## Operator flags (`cmd/operator`)

| Flag | Default | Description |
|---|---|---|
| `--dsn` | *(required)* | Postgres connection string. **Use a least-privilege role** (e.g. `pg_monitor`) — see [Installation: least-privilege role](installation.md#creating-a-least-privilege-postgres-role). |
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
| `--capture-plans` | `false` | Capture periodic `EXPLAIN (GENERIC_PLAN)` plan snapshots for tracked queries and attach a plan-diff summary to detected regressions. Requires PostgreSQL 16+ (logged once and otherwise a no-op on older servers); adds one extra planner invocation per tracked query per scrape cycle — see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional) |
| `--version` | `false` | Print version, commit, and build date, then exit — see [Versioning & dry-run](#versioning-dry-run) |
| `--dry-run` | `false` | Validate `--source-type`, Postgres connectivity, and (if `--state-backend=postgres`) the state DSN, then exit without starting any server — see [Versioning & dry-run](#versioning-dry-run) |

## Standalone collector flags (`cmd/collector`)

The `collector` binary exposes the scraper on its own, without the correlation/alerting/webhook pieces:

| Flag | Default | Description |
|---|---|---|
| `--dsn` | *(required)* | Postgres connection string. **Use a least-privilege role** (e.g. `pg_monitor`) — see [Installation: least-privilege role](installation.md#creating-a-least-privilege-postgres-role). |
| `--scrape-interval` | `60s` | How often to read `pg_stat_statements` |
| `--cluster-name` | `default` | Label added to all metrics |
| `--namespace` | `default` | Kubernetes namespace label |
| `--listen` | `:9090` | Prometheus metrics listen address |
| `--retention-minutes` | `180` | How long to keep in-memory query samples before pruning them |
| `--version` | `false` | Print version, commit, and build date, then exit |
| `--dry-run` | `false` | Validate Postgres connectivity (dial + `pg_stat_statements` installed), then exit without scraping |

## Ingester flags (`cmd/ingester`)

The `ingester` binary runs the deploy-event webhook receiver on its own, without the collector/correlation/alerting pieces — useful when the Correlation Engine polls it separately or when running the operator/manager mode with an external ingester:

| Flag | Default | Description |
|---|---|---|
| `--listen` | `:8080` | HTTP listen address for the webhook endpoint |
| `--source-type` | `generic` | `argocd`, `argo-rollouts`, `flux`, `generic` |
| `--source-name` | `default` | Unique name for this `DeploySource` |
| `--postgres-watch-ref` | `` | `PostgresWatch` to associate events with |
| `--app-name` | `` (all apps) | Filter events to a specific application name |
| `--cluster-name` | `` | Cluster identity stamped on `DeployEvent`s when the webhook payload doesn't carry one (e.g. Argo Rollouts, Flux without `eventMetadata`) |
| `--version` | `false` | Print version, commit, and build date, then exit |
| `--dry-run` | `false` | Validate `--source-type` and that `--listen` is a resolvable address, then exit without starting the webhook server |

Exposed routes once running: `POST /webhook` (receives deploy events), `GET /events` (lists everything ingested so far, as JSON), `GET /healthz`.

## Manager flags (`cmd/manager`)

| Flag | Default | Description |
|---|---|---|
| `--leader-elect` | `true` | Enable leader election (multi-replica HA) |
| `--leader-election-namespace` | *(auto)* | Namespace the `coordination.k8s.io` Lease is created in |
| `--metrics-bind-address` | `0` (disabled) | controller-runtime's own reconcile/workqueue metrics |
| `--pg-metrics-bind-address` | `:9090` | Aggregated `pg_stat_statements`-derived metrics (leader only) |
| `--webhook-bind-address` | `:8080` | Deploy-event webhooks, one route per `DeploySource` CR (leader only) |
| `--health-probe-bind-address` | `:8081` | `/healthz` and `/readyz` |
| `--version` | `false` | Print version, commit, and build date, then exit |
| `--dry-run` | `false` | Validate that a Kubernetes API server config can be resolved (in-cluster or via kubeconfig), then exit without starting the manager |

## Versioning & dry-run {#versioning-dry-run}

All four binaries share two flags:

- **`--version`** prints a one-line `<binary> <version> (commit <sha>, built <date>)` string and exits immediately, before any other flag is validated. A build made without the Dockerfile's `--build-arg VERSION/COMMIT/DATE` (e.g. a local `go build`) reports `dev`/`none`/`unknown` rather than a misleading guess.
- **`--dry-run`** validates configuration and (where applicable) live connectivity — Postgres reachability and the `pg_stat_statements` extension for `operator`/`collector`, source-type/address validity for `ingester`, Kubernetes API config resolution for `manager` — then exits with status `0` on success or `1` (with a logged error) on failure, without starting any server. Useful in CI or an init container to fail fast on a bad DSN or typo'd `--source-type` before the real process starts.

## `PostgresWatch` spec fields

| Field | Default | Description |
|---|---|---|
| `clusterName` | *(required)* | Label added to metrics and to `PerformanceRegression` CRs |
| `dsn` / `dsnSecretRef` | *(one required)* | Postgres DSN, inline or via a Secret key (preferred). **Use a least-privilege role** (e.g. `pg_monitor`) — see [Installation: least-privilege role](installation.md#creating-a-least-privilege-postgres-role). |
| `remoteClusterSecretRef` | *(none)* | Secret (in the hub cluster) holding a kubeconfig for a remote cluster to resolve `dsnSecretRef` against instead of the hub — see [Multi-Cluster (Fleet) Mode](multi-cluster.md) |
| `remoteNamespace` | *(same name as the watch's own namespace)* | Namespace to look up `dsnSecretRef` in on the remote cluster; only meaningful alongside `remoteClusterSecretRef` — see [Multi-Cluster (Fleet) Mode: What actually happens on reconcile](multi-cluster.md#what-actually-happens-on-reconcile) |
| `scrapeIntervalSeconds` | `60` | How often to read `pg_stat_statements` |
| `windowMinutes` | `30` | Analysis window (minutes before/after deploy) |
| `minExecutions` | `10` | Min query executions per window |
| `latencyChangeThreshold` | `"0.20"` | Min relative latency increase to flag (e.g. `"0.20"` = 20%) |
| `pValueThreshold` | `"0.05"` | Welch's t-test significance cutoff |
| `criticalQueryIDs` | `[]` | Queries that bypass `minExecutions` |
| `slackWebhookUrl` | `""` | Slack incoming-webhook URL for this watch |
| `capturePlans` | `false` | Enable plan-diff correlation — see [Detection Algorithm: Plan-diff correlation](detection-algorithm.md#plan-diff-correlation-optional). Populates the resulting `PerformanceRegression`'s `status.planDiffSummary` |
| `autoAbort.enabled` | `false` | Automatically abort the Argo Rollouts canary behind a high-confidence detected regression instead of only alerting — see [Auto-Abort](auto-abort.md) |
| `autoAbort.confidenceThreshold` | `"0.99"` | Minimum confidence required before auto-aborting; only meaningful alongside `autoAbort.enabled` — see [Auto-Abort: Confidence threshold](auto-abort.md#confidence-threshold) |

## `DeploySource` spec fields

| Field | Default | Description |
|---|---|---|
| `postgresWatchRef` | *(required)* | Name of the `PostgresWatch` (same namespace) to correlate against |
| `sourceType` | `generic` | `argocd`, `argo-rollouts`, `flux`, `generic`, `kubernetes` — see [Deploy Sources & Webhooks: Native Kubernetes watch](webhooks.md#native-kubernetes-watch-no-webhook) for `kubernetes` |
| `appName` | `""` (all apps) | Narrow correlation to a single application. When `sourceType` is `kubernetes`, this is the Deployment/StatefulSet's name and is required |
| `workloadKind` | `""` | `Deployment` or `StatefulSet`. Only meaningful (and required) when `sourceType` is `kubernetes` |

## See also

- [Getting Started](getting-started.md) — these flags/fields used in full runnable commands.
- [Detection Algorithm](detection-algorithm.md) — what `--window-minutes`, `--min-executions`, `--latency-threshold`, and `--changepoint-tolerance` actually control.
- [Persistence](persistence.md) — the `--state-*` flags in depth.
- [Deploy Sources & Webhooks](webhooks.md) — `--source-type` and `sourceType` per webhook source.
- [Auto-Abort (Argo Rollouts)](auto-abort.md) — `autoAbort.enabled`/`autoAbort.confidenceThreshold` in depth, including the RBAC gate.
- [Multi-Cluster (Fleet) Mode](multi-cluster.md) — `remoteClusterSecretRef` in depth, including the hub-spoke RBAC split.
- [API Versioning & Compatibility](api-versioning.md) — what `v1alpha1` guarantees (and doesn't) for the CRDs documented above.
