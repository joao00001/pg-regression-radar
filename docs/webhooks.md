# Deploy Sources & Webhooks

*How to wire ArgoCD, Argo Rollouts, Flux, or a custom system into the Deploy Event Ingester, including cluster attribution per source.*

## Overview

The Ingester (`internal/ingester`) normalises webhook payloads from four source types into a single `DeployEvent` — see [API Reference](api-reference.md) for that shape. Every `DeployEvent` carries a `cluster` field so multi-cluster setups can tell deploys apart; this page covers how each source populates it.

## Supported sources

| Source | Trigger | Notes |
|---|---|---|
| **ArgoCD** | `on-sync-succeeded` notification | Configure in ArgoCD Notifications |
| **Argo Rollouts** | Rollout promotion webhook | Set `--source-type argo-rollouts` |
| **Flux** | Notification Controller event | Set `--source-type flux` |
| **Generic** | Any JSON matching `DeployEvent` schema | Useful for custom CI systems |

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

## See also

- [API Reference](api-reference.md) — the `DeployEvent` JSON shape every source normalises into.
- [Configuration Reference](configuration.md) — `--source-type` / `sourceType`.
- [Getting Started](getting-started.md) — simulating a deploy event with `curl` against the generic source.
