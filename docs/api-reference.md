# API Reference

*The JSON shape of the two types that cross a process boundary: `DeployEvent` and `PerformanceRegression`.*

## Overview

These are the two types every webhook source normalises into and every alert is built from. Both are plain Go DTOs in `pkg/apis/v1alpha1` — see [Architecture Overview](architecture.md)'s note on the difference between these and the real Kubernetes CRDs in `api/v1alpha1`.

## `DeployEvent`

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

Produced by the Ingester from any of the four webhook sources — see [Deploy Sources & Webhooks](webhooks.md) for how each source populates `cluster`.

## `PerformanceRegression`

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
  "planDiffSummary": "root node changed from Index Scan to Seq Scan",
  "createdAt": "2026-08-11T12:35:00Z"
}
```

Produced by the Correlation Engine for every query analysed against a `DeployEvent` — see [Detection Algorithm](detection-algorithm.md) for what `status`, `confidenceScore`, and the latency fields mean, and how `Detected` is decided. `status` is one of `Detected`, `NoRegression`, or `InsufficientData`. `planDiffSummary` is only populated when plan-diff correlation is enabled (`--capture-plans` / `spec.capturePlans`) — see [Detection Algorithm: Plan-diff correlation](detection-algorithm.md#plan-diff-correlation-optional).

## Dashboard API

`internal/dashboardapi` serves a read-only HTTP API for the observability dashboard. It is enabled with `--dashboard-addr` on `cmd/operator` and `cmd/manager` (see [Configuration Reference](configuration.md)) and is a pure read layer: every field returned already exists on the `PerformanceRegression`/`PostgresWatch` CRDs (`api/v1alpha1`) or in `internal/storage`/`internal/collector`/`internal/ingester`, with no new computed metrics. Fields the current detection pipeline does not compute — t-statistic, Cohen's d, confidence intervals, e-divisive permutation counts, cache-hit ratio, rows-examined, lock-wait time, p95/p99 latency — are not part of this API.

Each route returns `501 Not Implemented` if the process serving it wasn't wired with the dependency that route needs. `/api/v1/regressions` and `/api/v1/watches` list Kubernetes objects and need a `client.Client` — only `cmd/manager` has one, so they always 501 on `cmd/operator`. `/api/v1/deploys` and `/api/v1/queries` need query-sample/deploy-event history, which only `cmd/operator` builds (via its `Collector`/deploy-event `Store`, or the postgres state backend when `--state-backend=postgres`) — they always 501 on `cmd/manager`.

### `GET /api/v1/regressions?namespace=&status=`

Lists `PerformanceRegression` objects (`api/v1alpha1/performanceregression_types.go`) via a `client.Client`, optionally filtered by namespace and `status` (`Detected`, `NoRegression`, `InsufficientData`). Each item:

```json
{
  "name": "argocd-my-app-1234567890-q8675309",
  "namespace": "production",
  "clusterName": "prod-cluster",
  "queryId": 8675309,
  "queryText": "SELECT * FROM orders WHERE user_id = $1",
  "status": "Detected",
  "triggerType": "deploy",
  "confidenceScore": "0.98",
  "meanLatencyBeforeMs": "4.2",
  "meanLatencyAfterMs": "13.7",
  "latencyChangeFactor": "3.26",
  "externalCauseSuspected": false,
  "planDiffSummary": "root node changed from Index Scan to Seq Scan",
  "autoAbortTriggered": false,
  "detectedAt": "2026-08-11T12:35:00Z"
}
```

### `GET /api/v1/deploys?since=RFC3339&until=RFC3339`

Returns `DeployEvent` (above), unmodified. Backed by `storage.EventStore.EventsInRange` when `--state-backend=postgres`, or otherwise directly by the operator's own in-process deploy-event history (`internal/ingester.Store`) — either way this works with `cmd/operator`'s default configuration, not only when postgres persistence is opted into. Defaults to the last 7 days when `since`/`until` are omitted.

### `GET /api/v1/queries?windowMinutes=60`

Returns, per tracked `queryId`, an aggregate over samples recorded in the last `windowMinutes` (default 60). Backed by `storage.SampleStore` when `--state-backend=postgres`, or otherwise directly by the operator's own in-process query-sample history (`internal/collector.Collector`) — either way this works with `cmd/operator`'s default configuration:

```json
{
  "queryId": 8675309,
  "queryText": "SELECT * FROM orders WHERE user_id = $1",
  "calls": 4213,
  "meanExecMs": 4.2,
  "sampleCount": 12
}
```

`calls` and `meanExecMs` mirror exactly what `regression_radar.query_samples` stores (`calls`, `mean_exec_time_ms`); there is no p95/p99 or cache-hit-ratio field, since that data isn't collected today.

### `GET /api/v1/watches`

Lists `PostgresWatch` objects (`api/v1alpha1/postgreswatch_types.go`) as-is, with no transformation.

## See also

- [Detection Algorithm](detection-algorithm.md) — how `PerformanceRegression` fields are computed.
- [Deploy Sources & Webhooks](webhooks.md) — how `DeployEvent` is produced from each source type.

