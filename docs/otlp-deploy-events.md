# OpenTelemetry Deploy Events (OTLP)

*`sourceType: otlp` — turn the OpenTelemetry spans your pipeline already emits for a deploy into `DeployEvent`s, with no per-tool webhook template.*

## Overview

Every other [`DeploySource`](webhooks.md) type parses one tool's webhook payload (ArgoCD, Flux, Argo Rollouts, or a generic JSON body). `sourceType: otlp` instead makes the DeploySource route an **OTLP/HTTP traces receiver**. Anything that can export OpenTelemetry traces — an OpenTelemetry Collector's `otlphttp` exporter, a CI/CD pipeline instrumented with an OTel SDK, a release script — can point at it, and the spans you select become `v1alpha1.DeployEvent`s through exactly the same `ingester.Store.Add` path as every other source. Detection is unchanged: the Correlation Engine only ever sees a `DeployEvent`.

Only the OTLP/HTTP trace encodings are implemented, at the wire level (no gRPC, no OpenTelemetry SDK dependency):

| Request | Supported |
|---|---|
| `Content-Type` | `application/x-protobuf`, `application/json` (anything else → `415`) |
| `Content-Encoding` | none, `identity`, `gzip` (anything else → `415`) |
| Size limit | 8 MiB on the wire **and** after decompression (`413` otherwise), so a gzip bomb is rejected |
| Success response | `200` with an empty `ExportTraceServiceResponse`; spans that matched the mapping but are unusable are reported via OTLP `partial_success` (`rejected_spans`), so the sender logs it instead of retrying forever |
| Malformed body | `400` (never retried by compliant exporters) |

The [`X-Webhook-Token` shared secret](webhooks.md) applies exactly as for the other source types.

## Selecting which spans are deploys

An OTLP endpoint usually receives a whole trace stream, so the mapping must be explicit. A mapping with neither `spanNames` nor `matchAttributes` is rejected at configuration time (a CRD reports phase `Failed`, reason `InvalidSpec`; the CLI exits non-zero) — it would otherwise turn every span into a deploy.

| Field | Meaning |
|---|---|
| `spanNames` | Span names (exact match) that represent a deploy. Any one matching is enough. |
| `matchAttributes` | `key: value` pairs that must **all** match. Looked up on the span first, then on its resource. |
| `includeFailedSpans` | Also accept spans with status `ERROR`. Off by default — a failed deploy did not change what the database runs. |

When both are set, a span must satisfy both (name in `spanNames` **and** every `matchAttributes` pair).

## Field mapping

| `DeployEvent` field | Source (span attribute, then resource attribute) | Override |
|---|---|---|
| `app` (required) | `service.name` | `appAttribute` |
| `namespace` | `k8s.namespace.name` | `namespaceAttribute` |
| `cluster` | `k8s.cluster.name`, falling back to the handler's cluster name (`--cluster-name` / the DeploySource's watch) | `clusterAttribute` |
| `revision` | `vcs.ref.head.revision`, then `service.version` | `revisionAttribute` |
| `imageTag` | `container.image.tags` (first element), then `container.image.tag` | `imageTagAttribute` |
| `timestamp` | span **end** time (the moment the deploy finished) | `timestamp: start` |
| `id` | `otlp-<traceId>-<spanId>` | — |

Details worth knowing:

- **Retries are idempotent.** The event ID is derived from the span's trace/span IDs, so an exporter that retries a batch overwrites the same event instead of creating duplicates. A span with no IDs gets a stable hash of its name, app, namespace, revision and time.
- **Timestamps are clamped to "now"** so a sender with a skewed clock cannot create a deploy in the future.
- **A matched span without the app attribute is rejected** (counted in `partial_success`), not silently stored under an empty application name.
- The `appName` filter of the DeploySource (`--app-name` on the CLI) still applies after mapping.
- Attribute values are matched as strings; integer, double and bool attribute values are compared by their canonical string form.

### Linking a regression back to the deploy trace

Because the event ID embeds the span's trace and span IDs, a `PerformanceRegression` caused by an OTLP-sourced deploy can be exported (see [OpenTelemetry Regression Export](otlp-export.md)) as a log record carrying that same `trace_id`/`span_id`. A backend such as Grafana, Tempo or Datadog can therefore jump from the deploy trace to the regression it caused.

## Using it from a DeploySource CRD

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: DeploySource
metadata:
  name: checkout-deploys
spec:
  postgresWatchRef: checkout-prod
  sourceType: otlp
  otlp:
    spanNames: ["deploy"]
    matchAttributes:
      cicd.pipeline.name: release
    timestamp: end          # or "start"
```

The manager registers **two** routes for the DeploySource, because the OpenTelemetry `otlphttp` exporter appends `/v1/traces` to whatever endpoint it is given:

- `/webhook/<namespace>/<name>`
- `/webhook/<namespace>/<name>/v1/traces`

so the exporter's `endpoint` is simply `http://<manager>:8080/webhook/<namespace>/<name>`.

## Using it from the standalone binaries

```bash
operator --dsn "$POSTGRES_DSN" --source-type otlp \
  --otlp-span-name deploy \
  --otlp-match-attribute cicd.pipeline.name=release
# ingester accepts the same flags
```

The receiver is served on `/webhook` and `/webhook/v1/traces` of `--webhook-listen`. Further flags: `--otlp-include-failed`, `--otlp-timestamp`, and `--otlp-{app,namespace,cluster,revision,image-tag}-attribute`. `--dry-run` validates the mapping.

With the Helm chart, set `ingester.sourceType: otlp` and `ingester.otlp.*` (see `values.yaml`).

## OpenTelemetry Collector example

A Collector that receives spans from your CI/CD tooling, keeps only the deploy spans, and forwards them:

```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318

processors:
  filter/deploys:
    error_mode: ignore
    traces:
      span:
        - 'name != "deploy"'
  batch: {}

exporters:
  otlphttp/radar:
    endpoint: http://pg-regression-radar.monitoring.svc:8080/webhook
    compression: gzip            # also accepted: none
    encoding: proto              # or json
    headers:
      X-Webhook-Token: ${env:RADAR_WEBHOOK_TOKEN}

service:
  pipelines:
    traces/deploys:
      receivers: [otlp]
      processors: [filter/deploys, batch]
      exporters: [otlphttp/radar]
```

Filtering in the Collector is optional: the DeploySource's `spanNames`/`matchAttributes` already ignore non-deploy spans. It just avoids shipping the rest of the trace stream.

## Troubleshooting

- **Nothing shows up in `/events` / no regressions.** Send one request by hand and read the response. A body `{}` with HTTP 200 and no event means no span matched — check `spanNames` against the exact span name and `matchAttributes` against span **and** resource attributes.
- **`partialSuccess.rejectedSpans`** in the response: spans matched but lack the application attribute. Set `appAttribute` or add `service.name` to the resource.
- **`415`** — the sender is using gRPC or an unsupported `Content-Type`. Use the `otlphttp` exporter, not `otlp` (gRPC).
- **`401`** — missing/incorrect `X-Webhook-Token`.
- **`404` on `/v1/traces`** — the DeploySource is not `sourceType: otlp`, or (CRD mode) its PostgresWatch is not yet Running.
