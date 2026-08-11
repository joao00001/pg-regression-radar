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
