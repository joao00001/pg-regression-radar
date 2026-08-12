# Architecture Overview

*How the four core packages fit together, the two ways to run them, and the repo layout.*

## Overview

pg-regression-radar is built from four engine packages (`internal/collector`, `internal/ingester`, `internal/correlation`, `internal/alerting`) that never change depending on how the process is run. What changes is the entrypoint that wires them up: a single CLI-flag-configured binary, or a Kubernetes-CRD-driven controller. This page covers both, plus where everything lives in the repository.

## The four engine packages

1. **Collector** (`internal/collector`) — scrapes `pg_stat_statements` on an interval and keeps a bounded in-memory time series per `queryid`, with Prometheus metrics exposed. See [Collector Internals](collector-internals.md) for retention, `queryid` rotation, and version-detection details.
2. **Deploy Event Ingester** (`internal/ingester`) — receives webhooks from ArgoCD, Argo Rollouts, and Flux and stores normalised `DeployEvent` records. See [Deploy Sources & Webhooks](webhooks.md).
3. **Correlation Engine** (`internal/correlation`) — for every deploy event, extracts the pre/post latency windows and runs E-divisive change-point detection followed by Welch's t-test. See [Detection Algorithm](detection-algorithm.md).
4. **Alerting** (`internal/alerting`) — fires a Slack-compatible webhook with the query text, latency before/after, change factor, and confidence score.

## Two ways to run pg-regression-radar

Both entrypoints wire up the *same* engine packages in two different ways:

| | `cmd/operator` (standalone) | `cmd/manager` (CRD-driven) |
|---|---|---|
| Configuration | CLI flags | `PostgresWatch` / `DeploySource` Kubernetes CRs |
| Postgres clusters watched | exactly one | any number, added/removed at runtime |
| Kubernetes API access needed | none | yes (CRDs + a Secret read) |
| High availability | run 1 replica | run N replicas with **leader election**; only the leader works, a standby takes over automatically on failure |
| Regression history | Slack/webhook alert only | Slack/webhook alert **and** a `PerformanceRegression` CR (`kubectl get performanceregressions -A`) |
| Recommended for | quick trials, single-cluster setups, environments without CRD install rights | production |

Both binaries are built from this one module and neither modifies the other's code path — `cmd/manager`'s reconcilers (`internal/controller/postgreswatch_controller.go`, `internal/controller/deploysource_controller.go`) call the exact same `collector.New` / `correlation.New` / `alerting.NewWebhookNotifier` constructors `cmd/operator/main.go` calls directly, just with one instance per `PostgresWatch` CR instead of one instance total, orchestrated by [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) instead of `main()`.

!!! note
    `pkg/apis/v1alpha1` holds plain Go DTO structs used internally between packages — they are **not** Kubernetes objects. The real CRDs (`metav1.TypeMeta`/`ObjectMeta`, `runtime.Object`, OpenAPI schemas) live in `api/v1alpha1` and are only used by `cmd/manager` and `internal/controller`. This split lets the detection engine stay Kubernetes-agnostic while still getting a first-class CRD experience.

### When to use which

- Use **`cmd/operator`** if you have one Postgres cluster, don't need HA, and would rather not grant the process any Kubernetes RBAC at all — it never talks to the Kubernetes API.
- Use **`cmd/manager`** (the Helm chart's `mode: manager`) if you want to watch multiple Postgres clusters from one deployment, want `kubectl get postgreswatch,deploysource,performanceregression` visibility, or need HA (multiple replicas with automatic failover via a `coordination.k8s.io` Lease).

## Multi-cluster: two independent axes

"Multiple Postgres clusters" and "Postgres clusters living in a different Kubernetes cluster than the manager" are two separate capabilities, easy to conflate:

- **Many `PostgresWatch` CRs, one Kubernetes cluster.** `cmd/manager` has always supported this: `internal/controller/registry.go`'s `Registry` tracks one independent `Collector`/`Engine`/`Notifier`/`ingester.Store` per reconciled `PostgresWatch`, however many DSNs (reachable over the network from wherever the manager pods run) that adds up to.
- **A `PostgresWatch`'s DSN Secret living in a *different* Kubernetes cluster.** This is what `spec.remoteClusterSecretRef` adds: a hub-spoke model, matching the pattern Cluster API / Argo CD / Open Cluster Management use, where a kubeconfig Secret in the manager's own (hub) cluster lets `PostgresWatchReconciler` build a `client.Client` for a remote ("spoke") cluster and read that cluster's CloudNativePG-generated DSN Secret through it, instead of requiring an operator to manually copy the Secret into the hub. See [Multi-Cluster (Fleet) Mode](multi-cluster.md) for the full model, the RBAC split between "read a Secret in the hub" and "whatever the kubeconfig itself is allowed to do in the spoke," and the gaps deliberately left out of this first pass (kubeconfig rotation, remote client cache eviction, a fixed remote-namespace override).

`DeploySource`/the webhook Ingester need neither of the above: a `DeploySource`'s whole job is registering an HTTP route that a remote system (ArgoCD, Argo Rollouts, Flux, or a custom CI job) pushes a webhook *to* — the connection is always initiated by the remote side, never by the manager reaching out, so there is nothing for a Kubernetes client (local or remote) to do here regardless of which cluster the deploy tooling runs in. See [Deploy Sources & Webhooks](webhooks.md).

## Project layout

```
.
├── Dockerfile          # multi-stage build for all four cmd/ binaries
│                       # (docker build --target operator|manager|collector|ingester)
├── mkdocs.yml          # this docs site's config
├── docs/               # this docs site's source (see docs/TEMPLATE.md)
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
│   ├── storage/        # SampleStore/EventStore interfaces + memory & postgres backends
│   ├── controller/     # PostgresWatchReconciler + DeploySourceReconciler
│   └── e2e/            # full real-pipeline integration test
├── pkg/apis/v1alpha1/  # internal DTOs shared by the packages above
│                       # (NOT Kubernetes objects — see note above)
├── config/
│   ├── crd/bases/      # generated CRD manifests (kubectl apply -f)
│   └── rbac/           # reference ClusterRole/Role for cmd/manager
└── deploy/helm/deploylens/  # Helm chart (installs CRDs + RBAC too)
```

## See also

- [Detection Algorithm](detection-algorithm.md) — the statistics behind the Correlation Engine.
- [Collector Internals](collector-internals.md) — retention, `queryid` rotation, PostgreSQL version handling.
- [Configuration Reference](configuration.md) — every flag and CRD field for both entrypoints.
- [Getting Started](getting-started.md) — runnable commands for both entrypoints and the Helm chart.
- [Multi-Cluster (Fleet) Mode](multi-cluster.md) — the hub-spoke model behind `remoteClusterSecretRef`.
