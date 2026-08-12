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
  "createdAt": "2026-08-11T12:35:00Z"
}
```

Produced by the Correlation Engine for every query analysed against a `DeployEvent` — see [Detection Algorithm](detection-algorithm.md) for what `status`, `confidenceScore`, and the latency fields mean, and how `Detected` is decided. `status` is one of `Detected`, `NoRegression`, or `InsufficientData`.

## See also

- [Detection Algorithm](detection-algorithm.md) — how `PerformanceRegression` fields are computed.
- [Deploy Sources & Webhooks](webhooks.md) — how `DeployEvent` is produced from each source type.
