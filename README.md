# pg-regression-radar — Postgres Performance Regression Detector

[![CI](https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml/badge.svg)](https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

> **pg-regression-radar** observes every query on your Postgres cluster and tells you, with
> statistical evidence, which specific Kubernetes deployment degraded performance —
> before it becomes an incident.

---

## The Problem

Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona …)
deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts).
When query latency spikes, the first question in every post-mortem is:

> **"Which deploy caused this?"**

Today that question is answered manually: open Grafana, look at
`pg_stat_statements`, try to recall when the last deploy happened, cross-reference
timestamps by eye. No open-source tool closes that loop automatically.

pg-regression-radar does.

---

## How It Works

```
┌──────────────────────┐      ┌───────────────────────┐
│  Postgres (CNPG)     │      │  ArgoCD / Argo        │
│  pg_stat_statements  │      │  Rollouts / Flux      │
│  pg_stat_monitor     │      │  (deploy events)      │
└──────────┬───────────┘      └──────────┬────────────┘
           │ scrape (30–60 s)            │ webhook
           ▼                             ▼
   ┌────────────────┐            ┌───────────────────┐
   │  Collector     │            │  Deploy Event     │
   │  (Go)          │            │  Ingester (Go)    │
   └───────┬────────┘            └─────────┬─────────┘
           │                               │
           ▼                               ▼
   ┌─────────────────────────────────────────────┐
   │   In-memory time-series store               │
   │   (Prometheus metrics exposed on /metrics)  │
   └───────────────────┬─────────────────────────┘
                        ▼
              ┌────────────────────┐
              │  Correlation       │
              │  Engine (Go)       │
              │  • E-divisive      │
              │    change-point    │
              │  • Welch's t-test  │
              └─────────┬──────────┘
                        ▼
              ┌────────────────────┐
              │  Alerting          │
              │  • Slack / webhook │
              │  • CRD status      │
              └────────────────────┘
```

1. **Collector** — scrapes `pg_stat_statements` every 60 s and keeps an
   in-memory time-series per `queryid`.  Metrics are also exposed in
   Prometheus format.
2. **Deploy Event Ingester** — receives webhooks from ArgoCD
   (`on-sync-succeeded`), Argo Rollouts, and Flux and stores normalised
   `DeployEvent` records.
3. **Correlation Engine** — for every deploy event, extracts the pre/post
   latency windows and runs:
   - **E-divisive** change-point detection to locate the regime shift.
   - **Welch's t-test** to confirm statistical significance.
   A `PerformanceRegression` is emitted if both tests agree.
4. **Alerting** — fires a Slack (or generic) webhook with the query text,
   latency before/after, change factor, and confidence score.

---

## Quick Start

### Prerequisites

- Go 1.22+
- A Postgres cluster with `pg_stat_statements` enabled
- (Optional) ArgoCD / Argo Rollouts / Flux for deploy-event webhooks

### Run the all-in-one operator

```bash
go run ./cmd/operator \
  --dsn "******localhost:5432/mydb?sslmode=disable" \
  --cluster-name my-cluster \
  --namespace production \
  --webhook-listen :8080 \
  --metrics-listen :9090 \
  --slack-url https://hooks.slack.com/services/XXX/YYY/ZZZ \
  --source-type argocd \
  --window-minutes 30 \
  --min-executions 10 \
  --latency-threshold 0.20
```

### Deploy on Kubernetes via Helm

```bash
helm install pg-regression-radar ./deploy/helm/deploylens \
  --set postgres.dsn="******cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set postgres.namespace=production \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

### Simulate a deploy event (for testing)

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "app": "my-app",
    "namespace": "production",
    "revision": "abc123def456",
    "imageTag": "my-app:v42",
    "timestamp": "2026-08-11T12:00:00Z"
  }'
```

---

## Configuration Reference

### Operator flags

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
| `--latency-threshold` | `0.20` | Min relative latency increase to flag (e.g. 0.20 = 20 %) |
| `--retention-minutes` | `180` | How long the collector keeps in-memory query samples before pruning them (see [Collector memory & queryid notes](#collector-memory--queryid-notes)) |

### Standalone collector flags

The `collector` binary (`cmd/collector`) exposes the scraper on its own, without the
correlation/alerting/webhook pieces:

| Flag | Default | Description |
|---|---|---|
| `--dsn` | *(required)* | Postgres connection string |
| `--scrape-interval` | `60s` | How often to read `pg_stat_statements` |
| `--cluster-name` | `default` | Label added to all metrics |
| `--namespace` | `default` | Kubernetes namespace label |
| `--listen` | `:9090` | Prometheus metrics listen address |
| `--retention-minutes` | `180` | How long to keep in-memory query samples before pruning them |

### Collector memory & queryid notes

The collector keeps a bounded, in-memory time series of `pg_stat_statements`
observations per `queryid`, and two properties of that design are worth knowing
about when tuning `--retention-minutes` or debugging a "missing data" report:

- **Retention, not an unbounded log.** Every scrape, samples older than
  `now - RetentionDuration` are pruned (a single reslice per queryid, since
  samples are always appended in chronological order — no O(n²) scan), and
  any queryid left with zero samples (e.g. one that fell out of
  `pg_stat_statements`'s top-500 `ORDER BY total_exec_time DESC LIMIT 500`) is
  removed from the map entirely, so both the data volume and the number of
  tracked keys stay bounded under long-running, continuous scraping. The
  default of **180 minutes** is 3x the full before+after span of the default
  30-minute analysis window (2 × 30 = 60 min), giving headroom for delayed
  deploy-event ingestion/processing and for the queryid-fingerprint fallback
  below to have data on both sides of a rotation. If you run with a larger
  `--window-minutes`, size `--retention-minutes` to stay comfortably above
  `2 * window-minutes`. Two Prometheus gauges expose current state:
  `pg_regression_radar_collector_tracked_queries` (distinct queryids retained)
  and `pg_regression_radar_collector_retained_samples_total` (total samples
  retained across all queryids).

- **`queryid` is not a stable identifier across a deploy.** Per the
  [PostgreSQL documentation](https://www.postgresql.org/docs/current/pgstatstatements.html),
  `queryid` "is not safe to assume... will be stable across major versions of
  PostgreSQL", and it also changes if a referenced object (e.g. a table
  touched by a migration) is dropped and recreated between executions — which
  can happen in the middle of the very deploy this tool is trying to analyze.
  The collector guards against this by also computing a text-normalized
  `FingerprintQuery` hash (see `internal/collector/fingerprint.go`) for every
  sample. When `SamplesInRange(queryID, from, to)` finds few samples directly
  under `queryID` in range, it additionally merges in same-range samples
  recorded under any other queryid that currently shares that fingerprint,
  so a query whose `queryid` rotated mid-window doesn't look like it has "no
  data" on one side of the rotation. This is fully internal to the collector:
  the public `SampleSource` interface consumed by `internal/correlation`
  (`SamplesInRange`/`AllQueryIDs`) is unchanged.
  **Known limitation:** `AllQueryIDs()` still lists the old and new queryids
  separately, so a rotated query may be analyzed (and, if flagged, reported)
  once per queryid, each pulling in the same merged data.

- **`pg_stat_statements` column names vary by PostgreSQL major version.**
  PostgreSQL 13 split the old `total_time`/`mean_time` columns into
  `total_plan_time`/`total_exec_time` and `mean_plan_time`/`mean_exec_time`
  (compare the [PG12](https://www.postgresql.org/docs/12/pgstatstatements.html)
  and [PG13](https://www.postgresql.org/docs/13/pgstatstatements.html) column
  references). CloudNativePG's currently supported releases only ship
  PostgreSQL 14+ (see [Supported releases](https://cloudnative-pg.io/docs/devel/supported_releases)),
  and PostgreSQL 13 itself reached community end-of-life on 2025-11-13, so a
  query hard-coded to the modern column names is already safe for every
  CloudNativePG-managed cluster in support today. The collector nonetheless
  detects `server_version_num` once at startup and falls back to the legacy
  column names below PostgreSQL 13, so it also works against older or
  self-managed Postgres instances.

---

## API Types

### DeployEvent

```json
{
  "id": "argocd-my-app-1234567890",
  "source": "prod-argocd",
  "app": "my-app",
  "cluster": "prod-cluster",
  "namespace": "production",
  "revision": "abc123def456",
  "imageTag": "my-app:v42",
  "timestamp": "2026-08-11T12:00:00Z"
}
```

### PerformanceRegression

```json
{
  "name": "argocd-my-app-1234567890-q8675309",
  "namespace": "production",
  "deployEventId": "argocd-my-app-1234567890",
  "queryId": 8675309,
  "queryText": "SELECT * FROM orders WHERE user_id = $1",
  "status": "Detected",
  "confidenceScore": 0.98,
  "meanLatencyBefore": 4.2,
  "meanLatencyAfter": 13.7,
  "latencyChangeFactor": 3.26,
  "externalCauseSuspected": false,
  "createdAt": "2026-08-11T12:35:00Z"
}
```

---

## Supported Webhook Sources

| Source | Trigger | Notes |
|---|---|---|
| **ArgoCD** | `on-sync-succeeded` notification | Configure in ArgoCD Notifications |
| **Argo Rollouts** | Rollout promotion webhook | Set `--source-type argo-rollouts` |
| **Flux** | Notification Controller event | Set `--source-type flux` |
| **Generic** | Any JSON matching `DeployEvent` schema | Useful for custom CI systems |

---

## Detection Algorithm

pg-regression-radar uses a two-stage approach (inspired by
[Hunter — DataStax Labs](https://github.com/datastax-labs/hunter)):

1. **E-divisive means** — energy-statistic based change-point detection that
   finds the most likely shift point in the query latency time series within the
   analysis window.
2. **Welch's t-test** — confirms that the means before and after the change
   point are statistically different (configurable p-value threshold, default
   p < 0.05).

Both stages must agree before a `PerformanceRegression` is emitted.  This
two-stage design prioritises **precision over recall** to avoid alert fatigue.

---

## Project Layout

```
.
├── cmd/
│   ├── collector/    # standalone scraper binary
│   ├── ingester/     # standalone webhook receiver binary
│   └── operator/     # all-in-one operator binary
├── internal/
│   ├── collector/    # pg_stat_statements scraper + Prometheus metrics
│   ├── correlation/  # E-divisive + Welch t-test engine
│   ├── ingester/     # webhook handler + in-memory event store
│   └── alerting/     # Slack / generic webhook notifier
├── pkg/apis/v1alpha1/ # CRD type definitions
└── deploy/helm/deploylens/  # Helm chart
```

---

## Roadmap

- **MVP (now):** Collector + Ingester + Correlation Engine + Slack alerting + Helm chart
- **v0.2:** Argo Rollouts and Flux source types; `auto_explain` plan diff
- **v0.3:** Kubebuilder CRDs (`PostgresWatch`, `DeploySource`, `PerformanceRegression`)
  surfaced as Kubernetes resources
- **v0.4:** GitHub/GitLab PR comment on detected regression; Grafana annotation
- **v1.0:** OLM bundle for OperatorHub.io; multi-cluster support

---

## Contributing

Pull requests are welcome. Please open an issue first for significant changes.

```bash
go test ./...       # run all tests
go vet ./...        # static analysis
go build ./...      # build all binaries
```

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
