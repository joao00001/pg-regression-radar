# Getting Started

*Prerequisites and the fastest path to a running pg-regression-radar, in any of its three supported forms.*

## Overview

pg-regression-radar ships two runnable entrypoints (`cmd/operator` and `cmd/manager`) plus a Helm chart that wraps both. This page covers the prerequisites and the minimal command to get each one running; see [Architecture Overview](architecture.md) for which one to pick, and [Configuration Reference](configuration.md) for every flag and CRD field used below.

## Prerequisites

- Go 1.22+ (only needed to build from source; the [Dockerfile](https://github.com/joao00001/pg-regression-radar/blob/main/Dockerfile) needs no local Go install)
- A Postgres cluster with `pg_stat_statements` enabled
- (Optional) ArgoCD, Argo Rollouts, or Flux for real deploy-event webhooks — see [Deploy Sources & Webhooks](webhooks.md)

## Run the all-in-one operator

```bash
go run ./cmd/operator \
  --dsn "postgres://user:pass@localhost:5432/mydb?sslmode=disable" \
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

## Run the manager (CRD-driven, HA)

```bash
# 1. Install the CRDs (also done automatically by the Helm chart below).
kubectl apply -f config/crd/bases/

# 2. Create a Secret holding the DSN, a PostgresWatch, and a DeploySource.
kubectl create secret generic my-cluster-dsn \
  --from-literal=dsn="postgres://user:pass@cnpg-cluster-rw.production:5432/mydb?sslmode=disable"

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

## Deploy on Kubernetes via Helm

```bash
helm install pg-regression-radar ./deploy/helm/deploylens \
  --set postgres.dsn="postgres://user:pass@cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set postgres.namespace=production \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

The chart's `mode` value picks the entrypoint (`operator`, the default above, or `manager`). In `manager` mode it also installs the CRDs (`deploy/helm/deploylens/crds/`), the RBAC the manager needs, and — unless `manager.createDefaultWatch=false` — a `PostgresWatch` + `DeploySource` pair from the same `postgres.*`/`analysis.*`/`alerting.*` values:

```bash
helm install pg-regression-radar ./deploy/helm/deploylens \
  --set mode=manager \
  --set manager.replicaCount=2 \
  --set postgres.dsn="postgres://user:pass@cnpg-cluster-rw.production:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=cnpg-cluster \
  --set alerting.slackWebhookUrl=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

!!! note
    Helm only installs a chart's `crds/` directory on `helm install`, never on `helm upgrade` — this is a deliberate Helm safety behaviour, not a bug in this chart (see the [Helm docs on CRDs](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/)). To pick up CRD schema changes after upgrading the chart, apply `deploy/helm/deploylens/crds/*.yaml` (or `config/crd/bases/*.yaml`, the same files) with `kubectl apply -f` directly.

## Simulate a deploy event (for testing)

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

That payload matches the `generic` source type (the default), which accepts a `DeployEvent` shape directly — see [API Reference](api-reference.md).

## See also

- [Architecture Overview](architecture.md) — which entrypoint to pick, and why.
- [Configuration Reference](configuration.md) — every flag and CRD field used above.
- [Deploy Sources & Webhooks](webhooks.md) — wiring up real ArgoCD/Rollouts/Flux webhooks instead of the simulated one above.
