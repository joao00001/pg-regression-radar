# Deploy Sources & Webhooks

*How to wire ArgoCD, Argo Rollouts, Flux, or a custom system into the Deploy Event Ingester, including cluster attribution per source.*

## Overview

The Ingester (`internal/ingester`) normalises webhook payloads from four source types into a single `DeployEvent` — see [API Reference](api-reference.md) for that shape. Every `DeployEvent` carries a `cluster` field so multi-cluster setups can tell deploys apart; this page covers how each source populates it.

A fifth source, **Kubernetes-native watch**, doesn't send a webhook at all — see [below](#native-kubernetes-watch-no-webhook) — and is only available in `manager` mode (CRD-driven), not via the standalone `operator`/`ingester` binaries' `--source-type` flag.

## Supported sources

| Source | Trigger | Notes |
|---|---|---|
| **ArgoCD** | `on-sync-succeeded` notification | Configure in ArgoCD Notifications |
| **Argo Rollouts** | Rollout promotion webhook | Set `--source-type argo-rollouts` |
| **Flux** | Notification Controller event | Set `--source-type flux` |
| **Generic** | Any JSON matching `DeployEvent` schema | Useful for custom CI systems |
| **Kubernetes-native** | Deployment/StatefulSet rollout completes | `manager` mode only; `sourceType: kubernetes` on a `DeploySource` CR — no webhook, no GitOps tool required |

## Cluster attribution

Always run each ingester (or the `operator` binary) with `--cluster-name <name>` set to the identity of the Kubernetes cluster it's watching — this is the fallback used whenever the source payload itself doesn't carry destination-cluster identity. Where the source tool *can* report the target cluster in its webhook payload, the ingester prefers that value over the flag.

### ArgoCD

The notification template is a Go template you control (see [Notifications templates](https://argo-cd.readthedocs.io/en/stable/operator-manual/notifications/templates/)), and every template has access to the full `Application` object as `.app`, including `.app.spec.destination.name` / `.app.spec.destination.server` — the actual target cluster of the sync. Configure your `argocd-notifications-cm` webhook template to include it:

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

The ingester reads `app.spec.destination.name` first and falls back to `app.spec.destination.server` (the API server URL) if the destination is addressed by server instead of by registered cluster name. If your ConfigMap doesn't populate `spec.destination` at all, the `--cluster-name` fallback is used instead.

### Argo Rollouts

A `Rollout` is not a cross-cluster deploy pointer the way an ArgoCD `Application` is: the controller and the Rollout it manages always live in the same cluster, so there is no destination/cluster field on the object to read (notification templates only expose `.rollout` and `.recipient`; see [Argo Rollouts notifications](https://argo-rollouts.readthedocs.io/en/latest/features/notifications/)).

If a single ingester fronts Rollouts from more than one cluster and you need to disambiguate per event rather than per ingester instance, add a top-level `"cluster"` field to your `argo-rollouts-notification-configmap` webhook body template (the same way this project already asks you to add `imageTag`, which also isn't a stock Rollout field):

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

### Flux

The notification-controller's `Event` payload ([API reference](https://fluxcd.io/flux/components/notification/events/)) is a fixed Go struct, not a customizable template, and has no cluster/destination field either — Flux is installed per-cluster with no built-in notion of a remote target. However, the `Alert` resource lets you merge static key/value pairs into every event it forwards via `spec.eventMetadata` ([docs](https://fluxcd.io/flux/components/notification/alerts/#event-metadata)). Set a `cluster` key there and the ingester will pick it up automatically:

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

### Generic

Set `cluster` directly in the JSON body; it is taken as-is and never overwritten by the fallback. This is also the source type used by the [manual e2e workflow](testing.md#manual-e2e-real-container) precisely because it accepts an explicit `timestamp` field, avoiding a race against the ingester's poll loop.

### Native Kubernetes watch (no webhook)

Every other source in this table exists to receive a webhook from something that already knows a deploy happened. If nothing in your cluster sends that webhook — no ArgoCD, no Argo Rollouts, no Flux, just plain `kubectl apply` or a CI pipeline rolling a `Deployment`/`StatefulSet` forward directly — there was previously no way to feed this project anything at all.

`sourceType: kubernetes` closes that gap by watching the workload itself instead of waiting for a notification about it. Set it on a `DeploySource` CR (manager mode only):

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: DeploySource
metadata:
  name: checkout-native
  namespace: default
spec:
  postgresWatchRef: prod-db
  sourceType: kubernetes
  workloadKind: Deployment   # or StatefulSet
  appName: checkout          # the Deployment/StatefulSet's own name
```

`WorkloadWatchReconciler` watches `apps/v1` `Deployment` and `StatefulSet` objects cluster-wide and, whenever the one named `appName` (in the same namespace as this `DeploySource`) finishes rolling out to a new revision, synthesises a `DeployEvent` exactly as if a webhook had arrived — `revision` is the Deployment's `deployment.kubernetes.io/revision` annotation, or the StatefulSet's `status.updateRevision`. "Finishes rolling out" means the full `kubectl rollout status` completion signal, not just "the spec changed" — a half-finished rollout never gets reported, since that would mix two revisions' data in the correlation engine's before/after windows.

Because there's no webhook payload to read a `cluster` field from, the event's `cluster` always comes from the owning `PostgresWatch`'s `spec.clusterName`.

This mode needs read-only RBAC on `apps/v1` `deployments`/`statefulsets`, already included in the manager's default `ClusterRole` (see [Configuration Reference](configuration.md)).

## Webhook authentication

By default the `/webhook` endpoint accepts any POST request that reaches port 8080, which means any process that can route to that port can inject deploy events. To prevent event spoofing — spam alerts, or masking a real regression under noise — configure a shared secret.

### How it works

Set the `--webhook-secret` flag (operator/ingester binary) or `spec.webhookSecret` (DeploySource CRD). The ingester then requires every POST to `/webhook` to include the secret verbatim in the `X-Webhook-Token` header. Requests without the header, or with a wrong value, are rejected with `401 Unauthorized`. The comparison is constant-time to prevent timing-based secret inference.

### Operator / standalone ingester

Pass the secret via an environment variable to avoid exposure in process listings:

```bash
export WEBHOOK_SECRET="$(openssl rand -hex 32)"
operator --dsn=... --webhook-secret="$WEBHOOK_SECRET"
```

Or with the standalone ingester binary:

```bash
ingester --source-type=argocd --webhook-secret="$WEBHOOK_SECRET"
```

### Helm chart (mode: operator)

Set `ingester.webhookSecret` in your `values.yaml` override (or via `--set`). The chart stores the value in the existing `<release>-secret` Kubernetes Secret and injects it into the pod as the `WEBHOOK_SECRET` environment variable:

```yaml
ingester:
  sourceType: argocd
  webhookSecret: "your-secret-here"   # generate with: openssl rand -hex 32
```

In production, avoid committing the secret to version control. Instead, pre-create the Kubernetes Secret and reference it directly, or use an external secrets operator to sync it.

### Configuring ArgoCD / Argo Rollouts / Flux to send the header

Each GitOps tool lets you add custom HTTP headers to its webhook notifications:

**ArgoCD** (`argocd-notifications-cm`):

```yaml
service.webhook.pg-regression-radar: |
  url: http://pg-regression-radar:8080/webhook
  headers:
    - name: X-Webhook-Token
      value: $webhook-secret   # reference a secret key
```

**Argo Rollouts** (`argo-rollouts-notification-configmap`):

```yaml
service.webhook.pg-regression-radar: |
  url: http://pg-regression-radar:8080/webhook
  headers:
    - name: X-Webhook-Token
      value: $webhook-secret
```

**Flux** (`Provider` resource):

```yaml
apiVersion: notification.toolkit.fluxcd.io/v1beta3
kind: Provider
metadata:
  name: pg-regression-radar-webhook
  namespace: flux-system
spec:
  type: generic
  address: http://pg-regression-radar:8080/webhook
  headers:
    - name: X-Webhook-Token
      value: your-secret-here   # replace with your actual secret
```

Flux ≥ 2.4 supports the `headers` field on the generic `Provider`, which lets you send a custom header verbatim. For earlier versions that only support `secretRef` (which sends `Authorization: token <value>` instead), use the `generic-hmac` provider type or upgrade Flux.

### Verifying the token with curl

```bash
curl -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -H "X-Webhook-Token: your-secret-here" \
  -d '{"app":"my-app","revision":"abc123","timestamp":"2024-01-15T10:00:00Z"}'
```

A request without the header returns `401 Unauthorized`; with the correct token it returns `204 No Content`.

## See also

- [API Reference](api-reference.md) — the `DeployEvent` JSON shape every source normalises into.
- [Configuration Reference](configuration.md) — `--source-type` / `sourceType`.
- [Getting Started](getting-started.md) — simulating a deploy event with `curl` against the generic source.
