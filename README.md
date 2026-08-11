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

## Two ways to run pg-regression-radar

pg-regression-radar ships two entrypoints that wire up the *same* engine
packages (`internal/collector`, `internal/correlation`, `internal/ingester`,
`internal/alerting`) in two different ways:

| | `cmd/operator` (**standalone**) | `cmd/manager` (**CRD-driven**) |
|---|---|---|
| Configuration | CLI flags | `PostgresWatch` / `DeploySource` Kubernetes CRs |
| Postgres clusters watched | exactly one | any number, added/removed at runtime |
| Kubernetes API access needed | none | yes (CRDs + a Secret read) |
| High availability | run 1 replica | run N replicas with **leader election**; only the leader works, a standby takes over automatically on failure |
| Regression history | Slack/webhook alert only | Slack/webhook alert **and** a `PerformanceRegression` CR (`kubectl get performanceregressions -A`) |
| Recommended for | quick trials, single-cluster setups, environments without CRD install rights | production |

Both binaries are built from this one module and neither modifies the
other's code path — `cmd/manager`'s reconcilers
(`internal/controller/postgreswatch_controller.go`,
`internal/controller/deploysource_controller.go`) call the exact same
`collector.New` / `correlation.New` / `alerting.NewWebhookNotifier`
constructors `cmd/operator/main.go` calls directly, just with one instance
per `PostgresWatch` CR instead of one instance total, orchestrated by
[controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)
instead of `main()`.

> **Note:** `pkg/apis/v1alpha1` holds plain Go DTO structs used internally
> between packages — they are **not** Kubernetes objects. The real CRDs
> (`metav1.TypeMeta`/`ObjectMeta`, `runtime.Object`, OpenAPI schemas) live in
> `api/v1alpha1` and are only used by `cmd/manager` and
> `internal/controller`. This split lets the detection engine stay
> Kubernetes-agnostic while still getting a first-class CRD experience.

### When to use which

- Use **`cmd/operator`** if you have one Postgres cluster, don't need HA,
  and would rather not grant the process any Kubernetes RBAC at all — it
  never talks to the Kubernetes API.
- Use **`cmd/manager`** (the Helm chart's `mode: manager`) if you want to
  watch multiple Postgres clusters from one deployment, want
  `kubectl get postgreswatch,deploysource,performanceregression` visibility,
  or need HA (multiple replicas with automatic failover via a
  `coordination.k8s.io` Lease — see `--leader-elect` below).

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

### Run the manager (CRD-driven, HA)

```bash
# 1. Install the CRDs (also done automatically by the Helm chart below).
kubectl apply -f config/crd/bases/

# 2. Create a Secret holding the DSN, a PostgresWatch, and a DeploySource.
kubectl create secret generic my-cluster-dsn \
  --from-literal=dsn="******cnpg-cluster-rw.production:5432/mydb?sslmode=disable"

cat <<'EOF' | kubectl apply -f -
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: my-cluster
  namespace: production
spec:
  clusterName: cnpg-cluster
  dsnSecretRef:
    name: my-cluster-dsn
    key: dsn
  windowMinutes: 30
  minExecutions: 10
  latencyChangeThreshold: "0.20"
  slackWebhookUrl: "https://hooks.slack.com/services/XXX/YYY/ZZZ"
---
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: DeploySource
metadata:
  name: my-cluster-argocd
  namespace: production
spec:
  postgresWatchRef: my-cluster
  sourceType: argocd
EOF

# 3. Run the manager (in-cluster it reads a ServiceAccount token
#    automatically; --kubeconfig is only for out-of-cluster/local runs).
go run ./cmd/manager \
  --leader-elect=true \
  --leader-election-namespace=production \
  --webhook-bind-address=:8080 \
  --pg-metrics-bind-address=:9090

# 4. Watch it come up and, later, watch regressions land as CRs.
kubectl get postgreswatch,deploysource -n production
kubectl get performanceregressions -A
```

### Deploy on Kubernetes via Helm

```bash
helm install pg-regression-radar ./deploy/helm/deploylens \
  --set postgres.dsn="******cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set postgres.namespace=production \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

The chart's `mode` value picks the entrypoint (`operator`, the default
above, or `manager`). In `manager` mode it also installs the CRDs
(`deploy/helm/deploylens/crds/`), the RBAC the manager needs
(`templates/manager-rbac.yaml`), and — unless
`manager.createDefaultWatch=false` — a `PostgresWatch` + `DeploySource`
pair from the same `postgres.*` / `analysis.*` / `alerting.*` values, so the
two modes are drop-in equivalents for a single-cluster setup:

```bash
helm install pg-regression-radar ./deploy/helm/deploylens \
  --set mode=manager \
  --set manager.replicaCount=2 \
  --set postgres.dsn="******cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

> Helm only installs charts' `crds/` directory on `helm install`, never on
> `helm upgrade` (this is a deliberate Helm safety behaviour, not a bug in
> this chart) — see the
> [Helm docs on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/).
> To pick up CRD schema changes after upgrading the chart, apply
> `deploy/helm/deploylens/crds/*.yaml` (or `config/crd/bases/*.yaml`, the
> same files) with `kubectl apply -f` directly.

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

### Manager flags

| Flag | Default | Description |
|---|---|---|
| `--leader-elect` | `true` | Enable leader election (multi-replica HA) |
| `--leader-election-namespace` | *(auto)* | Namespace the `coordination.k8s.io` Lease is created in |
| `--metrics-bind-address` | `0` (disabled) | controller-runtime's own reconcile/workqueue metrics |
| `--pg-metrics-bind-address` | `:9090` | Aggregated `pg_stat_statements`-derived metrics (leader only) |
| `--webhook-bind-address` | `:8080` | Deploy-event webhooks, one route per `DeploySource` CR (leader only) |
| `--health-probe-bind-address` | `:8081` | `/healthz` and `/readyz` |

### PostgresWatch spec fields

| Field | Default | Description |
|---|---|---|
| `clusterName` | *(required)* | Label added to metrics and to `PerformanceRegression` CRs |
| `dsn` / `dsnSecretRef` | *(one required)* | Postgres DSN, inline or via a Secret key (preferred) |
| `scrapeIntervalSeconds` | `60` | How often to read `pg_stat_statements` |
| `windowMinutes` | `30` | Analysis window (minutes before/after deploy) |
| `minExecutions` | `10` | Min query executions per window |
| `latencyChangeThreshold` | `"0.20"` | Min relative latency increase to flag (e.g. `"0.20"` = 20 %) |
| `pValueThreshold` | `"0.05"` | Welch's t-test significance cutoff |
| `criticalQueryIDs` | `[]` | Queries that bypass `minExecutions` |
| `slackWebhookUrl` | `""` | Slack incoming-webhook URL for this watch |

### DeploySource spec fields

| Field | Default | Description |
|---|---|---|
| `postgresWatchRef` | *(required)* | Name of the `PostgresWatch` (same namespace) to correlate against |
| `sourceType` | `generic` | `argocd`, `argo-rollouts`, `flux`, `generic` |
| `appName` | `""` (all apps) | Narrow correlation to a single application |

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
├── api/v1alpha1/       # real CRDs (PostgresWatch, DeploySource,
│                       # PerformanceRegression) + generated deepcopy code
├── cmd/
│   ├── collector/      # standalone scraper binary
│   ├── ingester/       # standalone webhook receiver binary
│   ├── operator/       # all-in-one, CLI-flag-configured binary
│   └── manager/        # CRD-driven, controller-runtime binary (HA)
├── internal/
│   ├── collector/      # pg_stat_statements scraper + Prometheus metrics
│   ├── correlation/    # E-divisive + Welch t-test engine
│   ├── ingester/       # webhook handler + in-memory event store
│   ├── alerting/       # Slack / generic webhook notifier
│   └── controller/     # PostgresWatchReconciler + DeploySourceReconciler:
│                       # turn CRs into running Collector/Engine instances
├── pkg/apis/v1alpha1/  # internal DTOs shared by the packages above
│                       # (NOT Kubernetes objects — see "Two ways to run")
├── config/
│   ├── crd/bases/      # generated CRD manifests (kubectl apply -f)
│   └── rbac/           # reference ClusterRole/Role for cmd/manager
└── deploy/helm/deploylens/  # Helm chart (installs CRDs + RBAC too)
```

---

## Roadmap

- **MVP (now):** Collector + Ingester + Correlation Engine + Slack alerting + Helm chart
- **v0.2:** Argo Rollouts and Flux source types; `auto_explain` plan diff
- **v0.3 (done):** Real CRDs (`PostgresWatch`, `DeploySource`, `PerformanceRegression`)
  reconciled by `cmd/manager` via controller-runtime, with leader-election HA —
  see "Two ways to run pg-regression-radar" above
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
