# Troubleshooting toolkit

*A fixed order of checks, the commands for each, and a symptom-to-cause table for the radar.*

## Overview

Almost every problem with the radar is one of five things: a pod that is not running, a custom resource that is not accepted, a reference that does not resolve, a permission that is missing, or a connection that is blocked. Kubernetes tells you which, if you look in the right places in the right order. This page is that order, followed by the radar-specific symptoms.

## The order of checks

Work down this list and stop at the first thing that looks wrong. Each step costs one command.

1. **Is the radar running?** `kubectl get pods -n <ns>`
2. **Did it accept my configuration?** `kubectl get pgw,pgds -n <ns>`: look at `Phase`.
3. **What does it say?** `kubectl logs ...`
4. **What does Kubernetes say about it?** `kubectl describe` and events.
5. **Is a permission missing?** `kubectl auth can-i ... --as=...`
6. **Is the network blocking it?** `kubectl get endpoints`, `port-forward`, NetworkPolicy.

### 1. Is the radar running?

```bash
kubectl get pods -n <ns>
```

| STATUS | Meaning | Next step |
|---|---|---|
| `Running`, `READY 1/1` | healthy | go to step 2 |
| `Running`, `READY 0/1` | the readiness probe is failing | `kubectl describe pod`, then logs |
| `Pending` | no node could take it, or a quota blocked it | `kubectl describe pod`: read **Events** at the bottom |
| `ImagePullBackOff` / `ErrImagePull` | the node cannot fetch the image | wrong tag, a private registry, or no internet. `describe` shows the exact error. |
| `CrashLoopBackOff` | the container starts and exits repeatedly | `kubectl logs <pod> --previous` |
| `OOMKilled` (in `describe`, under Last State) | it exceeded its memory limit | raise `resources.limits.memory` (`manager.resources` in manager mode) |
| `Completed` | a one-off pod finished | normal for Jobs, not for the radar |

```bash
# The Events section at the bottom is usually the answer.
kubectl describe pod <pod> -n <ns>
kubectl get events -n <ns> --sort-by=.lastTimestamp
```

### 2. Did it accept my configuration?

```bash
kubectl get postgreswatch,deploysource -n <ns>
kubectl get postgreswatch <name> -n <ns> -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
kubectl describe postgreswatch <name> -n <ns>
```

| `Phase` | Meaning |
|---|---|
| `Running` (watch), `Ready` (source) | working |
| `Pending` | the controller has not got to it yet, or (for a `DeploySource`) its `PostgresWatch` does not exist in **this namespace** yet |
| `Failed` | the controller tried and could not. **`status.message` is the reason.** |
| empty | no controller has touched it: the manager is not running, or the CRD was applied but no manager is installed |

`status.message` is usually specific: a Secret without the consent label, a Secret key that does not exist, a DSN that cannot connect, a remote cluster that is unreachable.

### 3. Logs

```bash
kubectl logs -n <ns> deployment/<fullname>-manager               # one pod, picked for you
kubectl logs -n <ns> <pod> --tail=100
kubectl logs -n <ns> <pod> --since=15m
kubectl logs -n <ns> <pod> -f                                     # follow
kubectl logs -n <ns> <pod> --previous       # the container that just crashed
# Every replica at once, each line prefixed with its pod.
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --all-containers --prefix
```

Remember that with several manager replicas only the **leader** works ([Pods and Deployments](workloads.md#replicas-leader-election-and-logs)). The `-l ... --prefix` form reads all replicas at once, which finds the leader's log without guessing.

The radar's images are built on a distroless base, so there is **no shell inside them**: `kubectl exec -it <pod> -- sh` will fail. That is expected. Everything you need is in the logs, the CR statuses and the metrics endpoint.

!!! tip "Most useful log lines are errors from the reconcilers"
    Look for `unable to resolve DSN`, `unable to start watch worker` and `forbidden`. Detection itself logs at a quieter level; a failing plan capture, for instance, is logged at `DEBUG`.

### 4. Events and `describe`

Events are Kubernetes' short-lived log of what happened to an object: scheduled, pulled, started, killed, failed a probe, rejected by a quota. They expire after about an hour, so look soon.

```bash
kubectl describe deployment <fullname>-manager -n <ns>
# Why a rollout is stuck: pod creation errors show up on the ReplicaSet.
kubectl describe replicaset -n <ns> -l app.kubernetes.io/component=manager
kubectl get events -n <ns> --field-selector type=Warning
```

If a Deployment says `0/2 ready` and has no pods at all, look at its **ReplicaSet's** events: that is where `exceeded quota` and `violates PodSecurity` messages appear, because the ReplicaSet creates the pods.

### 5. Permissions

```bash
kubectl auth can-i list deployments.apps -A --as=system:serviceaccount:<ns>:<serviceaccount>
kubectl logs -n <ns> deployment/<fullname>-manager | grep -i forbidden
```

A `forbidden` message names the identity, verb and resource exactly. See [RBAC and access](rbac.md#testing-permissions-kubectl-auth-can-i).

### 6. Network

```bash
# <none> means the Service selects no ready pod.
kubectl get endpoints <fullname>-manager -n <ns>
kubectl get networkpolicy -n <ns>                       # a policy can silently drop traffic
kubectl port-forward -n <ns> service/<fullname>-manager 8080:8080
```

If `port-forward` works but your sender cannot reach the Service, the problem is between the sender and the cluster, not the radar: a NetworkPolicy, a wrong DNS name, or a wrong port. See [Services and networking](networking.md).

## Symptoms specific to the radar

| Symptom | Likely cause | Check or fix |
|---|---|---|
| `no matches for kind "PostgresWatch"` on apply | CRDs not installed yet, or not yet `Established` | `kubectl get crd \| grep radar`; apply CRDs, then `kubectl wait --for=condition=Established crd/postgreswatches.radar.pgregressionradar.io` |
| `PostgresWatch` `Failed`, message about the label | the DSN Secret lacks the consent label | `kubectl label secret <name> -n <ns> pg-regression-radar.io/allow-postgreswatch-access=true` |
| `PostgresWatch` `Failed`, message about a key or Secret not found | `dsnSecretRef` points at the wrong name or key, or the Secret is in another namespace | `kubectl get secret <name> -n <ns> -o jsonpath='{.data}'` and compare with `name` and `key` |
| `PostgresWatch` `Failed`, DSN connection errors | wrong host, password, SSL mode, or a NetworkPolicy blocking egress to Postgres | check the DSN from a throwaway pod: `kubectl run psql --rm -it --image=postgres:16 -n <ns> -- psql "<dsn>"` |
| `DeploySource` stuck `Pending` | no `PostgresWatch` with that `postgresWatchRef` **in the same namespace** | `kubectl get postgreswatch -n <ns>` |
| No `PerformanceRegression` appears | not enough baseline data, or no deploy event recorded | see [below](#nothing-is-detected) |
| Webhook returns `404` | wrong path | use `kubectl get deploysource <name> -n <ns> -o jsonpath='{.status.webhookPath}'` |
| Webhook returns `401` | a webhook secret is set | send `X-Webhook-Token` ([Config and Secrets](config-secrets.md#webhook-authentication)) |
| Webhook times out | Service selects no pod, or a NetworkPolicy blocks ingress | `kubectl get endpoints`, `kubectl get networkpolicy` |
| Manager exits at startup with `address already in use` | the probe port `8081` is taken | change `--health-probe-bind-address` / `manager.ports.healthProbe` |
| Alerts are not delivered | destination policy, egress policy, wrong URL | [Alerting](../alerting.md#destination-policies); check egress to the alert host |
| After `helm upgrade`, new CRD fields are rejected or ignored | Helm does not update CRDs | `kubectl apply -f deploy/helm/deploylens/crds/` |
| Pod rejected: `exceeded quota` | a `ResourceQuota` in the namespace | `kubectl describe resourcequota -n <ns>` |
| Pod rejected: `violates PodSecurity` | a namespace policy conflicts with an overridden security context | [RBAC and access](rbac.md#pod-security) |

### Nothing is detected

"No regression" is often correct, and sometimes means the radar never had what it needs. Work through:

1. **Is the watch running?** `kubectl get pgw -n <ns>` shows `Running` and a non-zero tracked-queries count. Zero means `pg_stat_statements` is empty or not enabled.
2. **Is there a deploy event?** For `sourceType: kubernetes`, a rollout must have **completed** on a new revision: `kubectl rollout status deployment/<app> -n <ns>`. For webhooks, check the sender's logs and the `/events` listing.
3. **Is there enough history before the deploy?** The radar needs several scrapes of the query running normally before a change means anything. In the [lab](../lab.md) that is about three minutes; in production it is bounded by `windowMinutes` and `minSamples`.
4. **Is the query running enough?** `minExecutions` filters out queries that run too rarely.
5. **Is the change big enough?** `latencyChangeThreshold` is the minimum relative change (`"0.20"` is 20%).

A `PerformanceRegression` with `status: InsufficientData` means the radar considered the deploy and did not have enough samples to decide, which is different from `NoRegression`. See [Detection Algorithm](../detection-algorithm.md) for the thresholds.

## Collecting a report

When you ask for help, this is what makes the answer fast:

```bash
kubectl get pods,deployment,service -n <ns> -o wide
kubectl get postgreswatch,deploysource,performanceregression -n <ns>
kubectl describe postgreswatch <name> -n <ns>
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --tail=200 --prefix
kubectl get events -n <ns> --sort-by=.lastTimestamp
helm get values <release> -n <ns>          # redact the DSN and any secrets first
```

!!! warning "Redact before you paste"
    `helm get values`, `kubectl get secret -o yaml` and a `PostgresWatch` with an inline DSN all contain credentials. Remove them before sharing output.

## See also

- [Pods and Deployments](workloads.md): pod states, rollouts and probes.
- [RBAC and access](rbac.md): `forbidden` errors and the consent label.
- [Services and networking](networking.md): endpoints, port-forward, NetworkPolicy.
- [Getting Started](../getting-started.md#troubleshooting-common-setup-issues): the setup-time problems.
- [Quickstart Validation](../quickstart-validation.md): a checklist that proves detection works end to end.
