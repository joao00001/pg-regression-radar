# Deploy sources

*A deploy happened and the radar did not judge it: follow the event from the sender to the engine and find where it stopped.*

## Overview

The radar judges a deploy only after it has been told about it. The event travels in four steps, and it can stop at any of them. Each step leaves a trace, so the question "where did it stop" has an answer.

```text
sender ──▶ webhook route ──▶ event store of the watch ──▶ correlation and attribution
(Argo, CI)   DeploySource      PostgresWatch                "deploy registered"
```

A native source (`sourceType: kubernetes`) replaces the first two steps with the manager watching the workload itself.

## A deploy never shows up

Work down this list. Stop at the first step that fails.

### 1. Is the DeploySource ready?

```bash
kubectl get pgds -n <ns>
kubectl describe pgds <name> -n <ns>
```

| `Phase` | `Path` | What it means | Go to |
|---|---|---|---|
| `Ready` | `/webhook/<ns>/<name>` | the route is live | step 2 |
| `Ready` | empty, source type `kubernetes` | watched natively, no route by design | [Native watch](#a-native-source-reports-nothing) |
| `Pending` | empty | the PostgresWatch it points to does not exist or is not running | [`PostgresWatchNotReady`](reasons.md#postgreswatchnotready) |
| `Failed` | empty | the spec is invalid | [`InvalidSpec`](reasons.md#invalidspec) |

### 2. Did the request reach the manager?

Send a test event by hand and read the answer. **Only the leader listens on the webhook port**, so go to the leader and not to a Service that may pick a standby:

```bash
LEADER=$(kubectl get lease -n <ns> pg-regression-radar-manager.radar.pgregressionradar.io \
  -o jsonpath='{.spec.holderIdentity}' | cut -d_ -f1)
kubectl -n <ns> port-forward pod/$LEADER 8080:8080 &
curl -i -X POST localhost:8080/webhook/<ns>/<name> \
  -H 'Content-Type: application/json' \
  -d '{"app":"<app>","namespace":"<ns>","revision":"test-1","timestamp":"2026-11-27T12:00:00Z"}'
```

Use a throwaway `revision`: the test is a real event and will be judged like any other. The status says where it stopped:

| Answer | Meaning | Fix |
|---|---|---|
| `204 No Content` | accepted, or accepted and dropped: see step 3 | |
| `401 Unauthorized` | the DeploySource has a `webhookSecret` and the request has no, or a wrong, `X-Webhook-Token` | send the header; see [Webhook authentication](../webhooks.md#webhook-authentication) |
| `400 Bad Request` | the body is not the format of the source type | compare with [Deploy sources](../webhooks.md#supported-sources) |
| `404 Not Found` | no route at that path: wrong namespace or name, or the source is not `Ready` | `kubectl get pgds` and use its `Path` |
| `503 Service Unavailable` with `Retry-After: 10` | the PostgresWatch behind the source is not running (restarting or being deleted) | wait; senders that retry deliver the event once it is back |
| `500` from an `otlp` source | its span mapping is unusable | the log says why: `ingester: otlp source is misconfigured` |
| connection refused | nothing listens: you reached a standby, or the port is wrong | use the leader; check `--webhook-bind-address` |

!!! warning "A Service can send an event to a standby"
    The webhook, the metrics and the verdict listeners run on the leader only. A Service that selects every manager pod sends a share of requests to standbys, which refuse the connection. Senders that retry get through on a later try; one that does not is lost. If events disappear only some of the time, this is the first thing to check: send the same request a few times through the Service and count the refusals.

### 3. Was it stored?

```bash
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --since=1h | grep 'deploy event'
```

- `ingester: deploy event stored` (with `id`, `app`, `revision`): it arrived and was kept. Continue to step 4.
- `ingester: deploy event ignored, its app is not the one this source is configured for` (debug level; run with `--log-level=debug`): the event's `app` is not the DeploySource's `spec.appName`. The sender still got `204`. Fix `appName`, or the `app` the sender reports.
- `ingester: rejected webhook request with invalid or missing token`: step 2, `401`.
- `ingester: deploy event not stored, the watch this source feeds is not running`: step 2, `503`.
- No line at all: the request did not reach this manager. Check the sender: its URL, its retries and its own delivery log.

### 4. Was it registered with the engine?

When the PostgresWatch has `attribution.enabled`, a stored event is followed by:

```text
attribution: deploy registered, it will be judged from 10 to 45 minutes after it
```

with `firstEvaluation` and `lastEvaluation` bounding when the verdict arrives. See [`DeployRegistered`](reasons.md#deployregistered). Nothing before the first evaluation is a decision, so a deploy that is five minutes old has no verdict yet.

### 5. Why does the engine not act on it?

That is a different problem: see [Enforce mode](enforce.md#the-engine-only-alerts-and-does-not-act).

## A native source reports nothing

A `kubernetes` source reports a deploy when the Deployment or StatefulSet named by `appName` **finishes** rolling out to a new revision, the same signal as `kubectl rollout status`. A half-finished rollout is never reported. Check:

- `appName` is the name of the workload itself, and the workload is in the same namespace as the DeploySource.
- `workloadKind` is right (`Deployment` or `StatefulSet`).
- The rollout completed. `kubectl rollout status deploy/<name> -n <ns>`.
- The manager may read Deployments and StatefulSets. Its default ClusterRole does; a custom one may not.
- Look for `workloadwatch: deploy event synthesised from native rollout` in the log. Seeing it means the radar got the deploy; the next place to look is step 4. If the watch was not ready at that moment you see `workloadwatch: postgresWatchRef not ready yet`, and the event is retried: [`PostgresWatchNotReady`](reasons.md#postgreswatchnotready).

## An Argo Rollouts deploy is judged but never paused

Pausing and aborting only apply to a deploy that an **`argo-rollouts`** DeploySource reported, because the radar needs the Rollout to patch. A deploy from `generic`, `argocd`, `flux`, `otlp` or a native source is judged and logged but never acted on; the log says [`NotArgoRollout`](reasons.md#notargorollout). See [Enforce mode](enforce.md).

## See also

- [Deploy sources and webhooks](../webhooks.md): every source type, its payload and its authentication.
- [Reason codes](reasons.md#deploysource)
- [Where to look](where-to-look.md)
