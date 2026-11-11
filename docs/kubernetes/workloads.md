# Pods and Deployments

*What runs the radar, how a rollout works, and why a finished rollout counts as a deploy.*

## Overview

Two things in your cluster are Deployments from the radar's point of view: the radar itself, and the application whose releases it is watching. Understanding Pods, ReplicaSets, Deployments and rollouts explains how the radar starts, how to tell it is healthy, how replicas and failover work, and exactly when a deploy event is recorded.

## Pods

A **Pod** is the smallest thing Kubernetes runs: one or more containers sharing a network address and storage. You almost never create a Pod yourself. Pods are disposable: when one dies or is replaced, its replacement has a new name and a new IP.

```bash
kubectl get pods -n <ns>
kubectl get pods -n <ns> -o wide     # adds node and pod IP
kubectl describe pod <pod> -n <ns>   # events, probes, restarts
```

The columns in `kubectl get pods` are the first diagnostic you have:

| Column | Meaning |
|---|---|
| `READY` | containers passing their readiness probe / total. `1/1` is healthy; `0/1` is running but not ready. |
| `STATUS` | `Running`, `Pending` (not placed or not started), `CrashLoopBackOff` (keeps exiting), `ImagePullBackOff` (cannot fetch the image), `Completed` (finished, for Jobs). |
| `RESTARTS` | how many times a container was restarted. A growing number is a crash loop even if STATUS says Running right now. |
| `AGE` | time since creation. Useful to tell whether a pod is the new one or the old one during a rollout. |

## Deployments and ReplicaSets

A **Deployment** declares "run N identical pods from this template, and replace them gradually when the template changes". It does this through **ReplicaSets**: each version of the pod template gets its own ReplicaSet, and a rollout is the Deployment scaling the new ReplicaSet up while scaling the old one down.

```
Deployment (replicas: 2, template v2)
├── ReplicaSet orders-api-5d8f...   (template v1)  → scaled down to 0
└── ReplicaSet orders-api-7c4b...   (template v2)  → 2 pods
```

```bash
kubectl get deployment,replicaset,pod -n <ns>
kubectl get deployment <name> -n <ns> -o yaml | less
```

A StatefulSet is the same idea for pods that need a stable identity and storage (databases are the usual case). The radar treats a StatefulSet's rollouts the same way as a Deployment's.

!!! note "In pg-regression-radar"
    `mode: manager` installs one Deployment named `<fullname>-manager`; `mode: operator` installs one named `<fullname>`. Both are ordinary Deployments: `kubectl rollout status`, `kubectl scale` and `kubectl logs deployment/<name>` all work on them. Both run as a non-root user with a read-only root filesystem by default ([Hardening](../deployment-security.md)), so there is nothing writable to "fix" inside the container.

## Rollouts

Changing a Deployment's pod template (a new image tag, a changed environment variable, a new annotation on the template) starts a **rollout**. Kubernetes creates the new ReplicaSet and shifts pods across, honouring the Deployment's strategy so that a minimum number of pods stay available.

```bash
kubectl set image deployment/orders-api api=orders-api:v2 -n <ns>   # triggers a rollout
kubectl rollout status deployment/orders-api -n <ns>                # blocks until it finishes
kubectl rollout history deployment/orders-api -n <ns>               # list revisions
kubectl rollout undo deployment/orders-api -n <ns>                  # go back one revision
kubectl rollout undo deployment/orders-api -n <ns> --to-revision=3
```

Each rollout gets a **revision number**, kept in the annotation `deployment.kubernetes.io/revision` on the Deployment. A rollback with `rollout undo` is also a new rollout: it reuses the old template but gets a new revision number.

### Why this is the radar's deploy signal

A `DeploySource` with `sourceType: kubernetes` watches your Deployment or StatefulSet directly:

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: DeploySource
metadata:
  name: orders-api
  namespace: <ns>
spec:
  postgresWatchRef: orders
  sourceType: kubernetes
  workloadKind: Deployment      # or StatefulSet
  appName: orders-api           # the workload's own name
```

The radar records a deploy event when the rollout **completes** on a new revision, the same condition `kubectl rollout status` waits for, and not when the template merely changes. The event's `revision` is the Deployment's revision annotation (or the StatefulSet's `status.updateRevision`). That has practical consequences:

- **A stuck rollout is not a deploy.** If new pods never become Ready, no event is recorded. Check with `kubectl rollout status`.
- **A rollback is a deploy.** `kubectl rollout undo` finishes a rollout onto a (new) revision, so the radar records one. That is useful: if the undo fixes a regression, the data shows it.
- **Changes that do not touch the pod template are not rollouts.** Scaling replicas up or down does not create a revision.

Details of every source type, including webhooks from ArgoCD, Flux and Argo Rollouts, are in [Deploy Sources & Webhooks](../webhooks.md#native-kubernetes-watch-no-webhook).

## Health: probes

A **liveness probe** tells Kubernetes when to restart a container; a **readiness probe** tells it when to send traffic to the pod and, during a rollout, when to consider the new pod "available". The radar's manager exposes both on its health port:

| Probe | Path | Port (default) |
|---|---|---|
| liveness | `/healthz` | `8081` |
| readiness | `/readyz` | `8081` |

A pod with `READY 0/1` and a healthy log usually means the readiness probe is failing. `kubectl describe pod` shows the probe failures under Events. A port clash on `8081` is the common cause of a manager that exits on startup; see [Getting Started](../getting-started.md#troubleshooting-common-setup-issues).

## Replicas, leader election and logs

A Deployment's `replicas` field is how many identical pods run. The radar's manager supports more than one replica for high availability, with **leader election**: all replicas run, but only the one holding a Lease object does the work, and a standby takes over automatically if the leader dies.

```bash
kubectl get deployment <fullname>-manager -n <ns>
kubectl get lease -n <ns>
kubectl get lease -n <ns> -o jsonpath='{.items[*].spec.holderIdentity}'
```

Two practical points:

- **`kubectl logs deployment/<name>` shows one pod's log**, picked for you. With several replicas, the interesting log is the **leader's**. Use the lease's `holderIdentity` to find the pod, or read each pod: `kubectl logs <pod> -n <ns>`.
- **A standby pod is not broken.** It is `Running` and `READY`, and quiet. Only the leader scrapes Postgres and writes `PerformanceRegression` objects.

See [Architecture Overview](../architecture.md#when-to-use-which) for when to run more than one replica.

## Resources: requests and limits

Each container can declare **requests** (what the scheduler reserves) and **limits** (the ceiling enforced at runtime). A container that exceeds its **memory limit** is killed (`OOMKilled`); one that exceeds its CPU limit is throttled, not killed.

```yaml
resources:
  requests: { cpu: 50m, memory: 64Mi }
  limits:   { cpu: 200m, memory: 128Mi }
```

`50m` means 50 millicores, a twentieth of a CPU. The chart exposes these through `resources` (operator mode) and `manager.resources` (manager mode), and can also render a `ResourceQuota` and a `LimitRange` for the namespace ([Hardening](../deployment-security.md#resourcequota-and-limitrange)). If a pod is rejected with `exceeded quota`, that quota is the reason.

```bash
kubectl describe pod <pod> -n <ns> | grep -A4 "Last State"      # shows OOMKilled and exit codes
kubectl top pod -n <ns>                                          # needs metrics-server
```

## See also

- [Config and Secrets](config-secrets.md): what the radar's pods read at startup.
- [Troubleshooting toolkit](troubleshooting.md): `Pending`, `CrashLoopBackOff` and `ImagePullBackOff` step by step.
- [Deploy Sources & Webhooks](../webhooks.md): every way a deploy becomes an event.
- [Hardening (Helm)](../deployment-security.md): security context, quota and limit range.
