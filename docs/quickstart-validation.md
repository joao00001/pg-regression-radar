# Quickstart Validation

*Step-by-step validation flow for external users to confirm that pg-regression-radar detects a regression and links it to a deployment event.*

## Overview

This guide is a practical checklist for validating the product's core capability in a local or test environment: detect a query-latency regression and identify the deployment that introduced it. It is written for users who only have public repo/docs access and does not require internal setup knowledge.

Use this page together with the full walkthrough in [Example regression scenario](examples/regression-scenario.md). The steps below show how to run that same validation flow in all three supported run modes: `operator`, `manager`, and Helm.

## 1) Prepare a test PostgreSQL and baseline workload

Follow [Example regression scenario](examples/regression-scenario.md#1-set-up-postgresql-and-test-data) to:

1. Start PostgreSQL with `pg_stat_statements` enabled.
2. Create test data and an indexed query path.
3. Run a short baseline workload (fast query execution).

Use placeholder credentials in docs/examples only, for example:

```text
postgres://user:pass@localhost:5432/mydb?sslmode=disable
```

## 2) Start pg-regression-radar (choose one run mode)

### Option A: operator mode

```bash
go run ./cmd/operator \
  --dsn "******localhost:5432/mydb?sslmode=disable" \
  --cluster-name demo-cluster \
  --namespace demo \
  --webhook-listen :8080 \
  --metrics-listen :9090 \
  --source-type generic \
  --window-minutes 15 \
  --min-executions 5 \
  --latency-threshold 0.20
```

### Option B: manager mode (CRD-driven)

```bash
kubectl apply -f config/crd/bases/

kubectl create namespace demo
kubectl create secret generic demo-db-dsn -n demo \
  --from-literal=dsn="******localhost:5432/mydb?sslmode=disable"

cat <<'EOF' | kubectl apply -f -
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: demo-db
  namespace: demo
spec:
  clusterName: demo-cluster
  dsnSecretRef:
    name: demo-db-dsn
    key: dsn
  windowMinutes: 15
  minExecutions: 5
  latencyChangeThreshold: "0.20"
---
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: DeploySource
metadata:
  name: demo-generic
  namespace: demo
spec:
  postgresWatchRef: demo-db
  sourceType: generic
EOF

go run ./cmd/manager \
  --webhook-bind-address=:8080 \
  --pg-metrics-bind-address=:9090
```

### Option C: Helm

```bash
helm install pg-regression-radar ./deploy/helm/deploylens \
  --set mode=operator \
  --set postgres.dsn="******localhost:5432/mydb?sslmode=disable" \
  --set postgres.clusterName=demo-cluster \
  --set ingester.sourceType=generic
```

## 3) Reproduce a deployment-associated regression

Execute the full scenario from [Example regression scenario](examples/regression-scenario.md):

1. Emit a baseline deploy event (`revision=v1`).
2. Intentionally slow the query path.
3. Emit a second deploy event (`revision=v2`).
4. Continue query traffic until analysis runs.

> Tip: for fast local demos, run with `--scrape-interval 1s` and keep each baseline/regression traffic phase long enough to cover at least 3–4 scrape windows; short single-burst traffic usually does not produce enough time-series shape for E-divisive detection.

## 4) Validate that detection worked

Use mode-appropriate checks:

- **operator:** check process logs and alert outputs.
- **manager:** check logs and created `PerformanceRegression` resources:
  ```bash
  kubectl get performanceregressions -A
  ```
- **Helm:** use the same checks as whichever chart mode you selected (`operator` or `manager`).

Expected outcome in all modes:

- At least one regression is reported for the slowed query.
- The reported deploy metadata points to the simulated deployment event (for example `revision=v2`).

## 5) What "successful detection" looks like in practice

A successful validation is not only "an alert fired." You should also see:

- **Query identity** (query text and/or query ID).
- **Measured latency change** (before vs after, with change factor/percent).
- **Deployment identity** (app/namespace/revision/image tag or equivalent fields from the deploy event).
- **Timestamp/correlation context** showing the regression tied to the specific deployment window.

## Security and production-use context

The commands on this page are intentionally minimal for testing and validation. Do **not** treat these defaults as production-ready.

For production use, at minimum:

- Protect webhook ingest with a shared secret (`--webhook-secret` or `spec.webhookSecret`) and only trusted senders.
- Use least-privilege RBAC and explicit Secret-access consent controls where applicable.
- Apply Kubernetes hardening controls (Pod Security, NetworkPolicy, resource boundaries, and egress controls).
- Restrict alert destinations and review outbound webhook policy.

For hardened production configuration, see:

- [Deployment security hardening (Helm)](deployment-security.md)
- [Security Model & Threat Model](security-model.md)

## See also

- [Getting Started](getting-started.md) — run commands for each supported mode.
- [Example regression scenario](examples/regression-scenario.md) — reproducible end-to-end validation workflow.
- [Alerting example](examples/alerting-example.md) — concrete alert delivery and interpretation walkthrough.
- [Deploy Sources & Webhooks](webhooks.md) — webhook source wiring and authentication.
