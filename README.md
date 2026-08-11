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
| `--retention-minutes` | `180` | How long the collector keeps in-memory query samples before pruning them (see [Collector memory & queryid notes](#collector-memory--queryid-notes)) |
| `--changepoint-tolerance` | `0` (auto) | Max distance between the E-divisive change point and the deploy timestamp still attributed to that deploy (0 = auto: 20% of window, floor 2m) |
| `--state-backend` | `memory` | State persistence backend: `memory` or `postgres` — see [Persistence](#persistence) |
| `--state-dsn` | `` | Postgres DSN for the state backend when `--state-backend=postgres` (defaults to `--dsn`) |
| `--state-retention` | `168h` (7 days) | How long samples/events are kept in the postgres state backend |
| `--state-prune-interval` | `15m` | How often the retention sweep runs against the postgres state backend |

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

## Persistence

By default (`--state-backend=memory`, unchanged from earlier releases) all
state lives only in process memory:

- The **Collector** keeps its per-`queryid` `pg_stat_statements` samples in an
  in-memory map (`internal/collector.Collector`).
- The **Ingester** keeps deploy events in an in-memory slice
  (`internal/ingester.Store`).

That means a pod restart loses all history, and running multiple replicas
gives each one an inconsistent, independent view of the world. Setting
`--state-backend=postgres` additionally persists both to Postgres, so history
survives restarts and can be inspected/shared across replicas.

### Configuring it

```bash
operator   --dsn "host:5432/monitored_db?sslmode=disable"   --state-backend postgres
  # --state-dsn defaults to --dsn above: state lives in the SAME Postgres
  # cluster being monitored. To isolate it, point at a separate instance:
  # --state-dsn "host:5432/pgrr_state?sslmode=disable"
```

- **Same Postgres as the monitored cluster** (default, `--state-dsn` empty):
  simplest to operate — one connection string, one cluster. Trade-off: the
  tool's own write traffic shows up in the very `pg_stat_statements` it
  watches, and an outage of the monitored cluster also takes down the
  history.
- **Separate Postgres instance** (`--state-dsn` set): isolates storage load
  and availability from the cluster under observation, at the cost of running
  a second Postgres.

### Schema

Everything lives in its own schema so it never collides with application
tables and can be dropped cleanly:

```sql
CREATE SCHEMA IF NOT EXISTS pg_regression_radar;

CREATE TABLE pg_regression_radar.query_samples (
    id                  BIGSERIAL PRIMARY KEY,
    query_id            BIGINT NOT NULL,
    query_text          TEXT NOT NULL,
    calls               BIGINT NOT NULL,
    total_exec_time_ms  DOUBLE PRECISION NOT NULL,
    mean_exec_time_ms   DOUBLE PRECISION NOT NULL,
    recorded_at         TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_query_samples_queryid_recorded_at ON pg_regression_radar.query_samples (query_id, recorded_at);
CREATE INDEX idx_query_samples_recorded_at         ON pg_regression_radar.query_samples (recorded_at);

CREATE TABLE pg_regression_radar.deploy_events (
    id               TEXT PRIMARY KEY,
    source           TEXT NOT NULL DEFAULT '',
    app              TEXT NOT NULL DEFAULT '',
    cluster          TEXT NOT NULL DEFAULT '',
    namespace        TEXT NOT NULL DEFAULT '',
    revision         TEXT NOT NULL DEFAULT '',
    image_tag        TEXT NOT NULL DEFAULT '',
    event_timestamp  TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_deploy_events_event_timestamp ON pg_regression_radar.deploy_events (event_timestamp);
```

The schema is applied automatically on startup (`internal/storage/postgres`)
using hand-rolled, idempotent DDL (`CREATE ... IF NOT EXISTS`) rather than a
migration framework like `golang-migrate`/`goose` — both tables are
append-mostly, derived/observational data that evolves purely additively, so
the extra machinery of a versioned migration runner isn't earning its keep
yet. If the schema ever needs a genuinely breaking change (rename, backfill,
tightened constraint), that's the point to introduce one.

### Retention

Old rows are pruned periodically (`--state-prune-interval`, default 15 min)
using a `DELETE ... WHERE recorded_at < now() - retention` sweep, where
retention defaults to 7 days (`--state-retention`).

### Trade-offs and limitations

- **Not a replacement for leader election.** Pointing multiple operator
  replicas at the same state backend makes the *data* consistent and durable,
  but each replica still independently scrapes `pg_stat_statements` and runs
  the correlation engine — so you'd get duplicate scrapes and duplicate
  alerts. Preventing that requires only one replica being active at a time
  (leader election), which is being addressed separately via a
  controller-runtime manager's built-in leader election, not by this
  package. Don't run N unattended replicas against a shared backend until
  that lands.
- **History doesn't yet pre-load the live Collector/Ingester.** Persisted
  samples/events are a durable *copy* of what the Collector/Ingester observed
  while running; on restart, the in-memory hot path (and therefore the
  correlation engine's view) starts empty again even though the Postgres
  history is intact. Backfilling the in-memory state from the store on
  startup is a natural next step once the Collector/Ingester accept a
  pluggable storage backend directly.
- **Extra write load.** Every scrape/webhook now also does a write to
  Postgres. The connection pool used for this is intentionally small (see
  `internal/storage/postgres.Open`) to keep the footprint low, especially
  when reusing the monitored cluster's own Postgres.

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

Every `DeployEvent` carries a `cluster` field so multi-cluster setups can tell
deploys apart. Always run each ingester (or the `operator` binary) with
`--cluster-name <name>` set to the identity of the Kubernetes cluster it's
watching — this is the fallback used whenever the source payload itself
doesn't carry destination-cluster identity. Where the source tool *can*
report the target cluster in its webhook payload, the ingester prefers that
value over the flag. Per source:

- **ArgoCD** — the notification template is a Go template you control (see
  [Notifications templates](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/templates/)),
  and every template has access to the full `Application` object as `.app`,
  including `.app.spec.destination.name` / `.app.spec.destination.server` —
  the actual target cluster of the sync. Configure your
  `argocd-notifications-cm` webhook template to include it, e.g.:

  ```yaml
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: argocd-notifications-cm
  data:
    template.on-sync-succeeded-webhook: |
      webhook:
        pg-regression-radar:
          method: POST
          body: |
            {
              "app": {
                "metadata": {
                  "name": "{{.app.metadata.name}}",
                  "namespace": "{{.app.metadata.namespace}}"
                },
                "spec": {
                  "destination": {
                    "name": "{{.app.spec.destination.name}}",
                    "server": "{{.app.spec.destination.server}}"
                  }
                },
                "status": {
                  "sync": {"revision": "{{.app.status.sync.revision}}"},
                  "summary": {"images": {{toJson .app.status.summary.images}}}
                }
              }
            }
    trigger.on-sync-succeeded: |
      - when: app.status.operationState.phase in ['Succeeded']
        send: [on-sync-succeeded-webhook]
  ```

  The ingester reads `app.spec.destination.name` first and falls back to
  `app.spec.destination.server` (the API server URL) if the destination is
  addressed by server instead of by registered cluster name. If your
  ConfigMap doesn't populate `spec.destination` at all, the `--cluster-name`
  fallback is used instead.

- **Argo Rollouts** — a `Rollout` is not a cross-cluster deploy pointer the
  way an ArgoCD `Application` is: the controller and the Rollout it manages
  always live in the same cluster, so there is no destination/cluster field
  on the object to read (notification templates only expose `.rollout` and
  `.recipient`; see
  [Argo Rollouts notifications](https://argo-rollouts.readthedocs.io/en/latest/features/notifications/)).
  If a single ingester fronts Rollouts from more than one cluster and you
  need to disambiguate per event rather than per ingester instance, add a
  top-level `"cluster"` field to your `argo-rollouts-notification-configmap`
  webhook body template (the same way this project already asks you to add
  `imageTag`, which also isn't a stock Rollout field):

  ```yaml
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: argo-rollouts-notification-configmap
  data:
    template.pg-regression-radar-webhook: |
      webhook:
        pg-regression-radar:
          method: POST
          body: |
            {
              "rollout": {
                "metadata": {
                  "name": "{{.rollout.metadata.name}}",
                  "namespace": "{{.rollout.metadata.namespace}}"
                },
                "status": {"currentPodHash": "{{.rollout.status.currentPodHash}}"}
              },
              "cluster": "{{index .rollout.metadata.labels "cluster"}}"
            }
  ```

  Otherwise, just rely on `--cluster-name`.

- **Flux** — the notification-controller's `Event` payload
  ([API reference](https://fluxcd.io/flux/components/notification/events/))
  is a fixed Go struct, not a customizable template, and has no
  cluster/destination field either — Flux is installed per-cluster with no
  built-in notion of a remote target. However, the `Alert` resource lets you
  merge static key/value pairs into every event it forwards via
  `spec.eventMetadata`
  ([docs](https://fluxcd.io/flux/components/notification/alerts/#event-metadata)).
  Set a `cluster` key there and the ingester will pick it up automatically:

  ```yaml
  apiVersion: notification.toolkit.fluxcd.io/v1beta3
  kind: Alert
  metadata:
    name: pg-regression-radar
    namespace: flux-system
  spec:
    providerRef:
      name: pg-regression-radar-webhook
    eventMetadata:
      cluster: prod-cluster-1
    eventSources:
      - kind: Kustomization
        name: '*'
      - kind: HelmRelease
        name: '*'
  ```

  Without `eventMetadata.cluster`, the `--cluster-name` fallback is used.

- **Generic** — set `cluster` directly in the JSON body; it is taken as-is
  and never overwritten by the fallback.

---

## Detection Algorithm

pg-regression-radar uses a two-stage approach (inspired by
[Hunter — DataStax Labs](https://github.com/datastax-labs/hunter) and the
accompanying ICPE'23 paper, "Hunter: Using Change Point Detection to Hunt for
Performance Regressions", [arXiv:2301.03034](https://arxiv.org/abs/2301.03034)):

0. **Cheap pre-filter.** The naive mean latency over the whole pre-deploy
   window is compared against the naive mean over the whole post-deploy
   window. If the relative change is below `LatencyChangeThreshold`
   (default 20%), the query is rejected immediately without running the
   more expensive stages below.
1. **E-divisive means (stage 1 — locate).** For queries that pass the
   pre-filter, the pre- and post-deploy samples are merged into a single
   chronologically-ordered series covering the whole analysis window, and
   the E-divisive means algorithm (energy-statistic based change-point
   detection; Matteson & James, 2014, JASA) locates the single most
   significant change point in that series. Crucially, this does **not**
   assume the shift happens exactly at the deploy timestamp — rolling
   updates, connection draining and collector scrape lag all delay the
   observable effect of a deploy by anywhere from seconds to a few minutes.
   The located change point must fall within `ChangePointTolerance` of the
   deploy timestamp (default: 20% of the analysis window, floored at 2
   minutes) to be considered related to that deploy at all. If E-divisive
   finds no change point, or the one it finds is too far from the deploy,
   the query is marked `NoRegression` — even if the naive pre/post means
   from stage 0 looked like a regression.
2. **Welch's t-test (stage 2 — confirm).** The two segments actually
   defined by the change point E-divisive found (not the naive
   deploy-timestamp split) are compared with Welch's t-test to confirm the
   difference in means is statistically significant (configurable p-value
   threshold, default p < 0.05). `ConfidenceScore` is derived from this
   p-value.

A `PerformanceRegression` is only marked `Detected` when the change point is
found, lies near the deploy, **and** the t-test on the real segments is
significant — both stages must agree. This two-stage design prioritises
**precision over recall** to avoid alert fatigue: a naive before/after mean
comparison alone is prone to false positives whenever an unrelated latency
shift happens to overlap the analysis window (see `DetectedChangeAt` on the
`PerformanceRegression`, which records exactly when the shift was located).

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
│   ├── storage/        # SampleStore/EventStore interfaces + memory & postgres backends (see Persistence)
│   └── controller/     # PostgresWatchReconciler + DeploySourceReconciler:
│                       # turn CRs into running Collector/Engine instances
├── pkg/apis/v1alpha1/  # internal DTOs shared by the packages above
│                       # (NOT Kubernetes objects — see "Two ways to run")
├── api/v1alpha1/       # real CRDs (PostgresWatch, DeploySource,
│                       # PerformanceRegression) + generated deepcopy code
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
Please follow our [Code of Conduct](CODE_OF_CONDUCT.md) in all project spaces.

```bash
go test ./...       # run all tests
go vet ./...        # static analysis
go build ./...      # build all binaries
```

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
