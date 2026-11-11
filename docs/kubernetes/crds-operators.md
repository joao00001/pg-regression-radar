# CRDs and operators

*How `PostgresWatch` and the other radar kinds exist, what the controller does, and the install rules.*

## Overview

Kubernetes ships with kinds like Pod and Deployment. A **CustomResourceDefinition (CRD)** teaches the API server a new kind, and a **controller** (often called an **operator** when it manages an application) is a program that watches objects of that kind and acts on them. The radar's `manager` mode is exactly this: four CRDs plus one controller deployment. Knowing how the two halves relate answers most install and upgrade questions before they come up.

## CRD, custom resource, controller

Three separate things, easy to conflate:

| Thing | What it is | Example |
|---|---|---|
| **CRD** | the *definition* of a kind: its name, fields and validation. Installed once per cluster. | `postgreswatches.radar.pgregressionradar.io` |
| **Custom resource (CR)** | an *instance* of that kind, written by you | a `PostgresWatch` named `orders` |
| **Controller** | a running program that watches CRs and makes reality match them | the radar's manager Deployment |

Without the CRD, the API server rejects your YAML. Without the controller, your CRs are accepted and stored, and nothing happens: they just sit there with no `status`. A `PostgresWatch` with an empty `Phase` column usually means the controller is not running.

## The reconcile loop

A controller does one thing, repeatedly:

1. **Observe** the desired state (the CR's `spec`) and the actual state.
2. **Act** to close the gap.
3. **Report** the outcome in the CR's `status`.

The controller does not run once per `kubectl apply`; it runs whenever the CR changes, whenever something it depends on changes (the DSN Secret, the Deployment it watches) and periodically. This is why Kubernetes setups are described as *eventually consistent*: you edit the spec, and `status` catches up a moment later.

```bash
kubectl apply -f my-watch.yaml
kubectl get postgreswatch orders -n <ns> -w        # watch the phase move from Pending to Running
```

!!! note "In pg-regression-radar"
    The manager runs one reconcile loop per kind. For each `PostgresWatch` it starts a scrape and detection worker; for each `DeploySource` it registers a route or a workload watch; for each detection it **creates** a `PerformanceRegression`. If the manager restarts, it re-reads the CRs and starts everything again, which is why your configuration lives in objects and not in the pod.

## The four radar kinds

| Kind | Short name | Scope | You write it? | What it is |
|---|---|---|---|---|
| `PostgresWatch` | `pgw` | namespaced | yes | one Postgres cluster to watch, with detection and alerting settings |
| `DeploySource` | `pgds` | namespaced | yes | where deploy events come from, tied to one `PostgresWatch` |
| `PerformanceRegression` | `pgreg` | namespaced | no (the radar creates it) | one detection result, with the query, latency change and plan diff |
| `PostgresRadarCluster` | `pgrc` | **cluster** | admins | a registered remote cluster, for [multi-cluster](../multi-cluster.md) mode |

All four belong to the API group `radar.pgregressionradar.io`, version `v1alpha1`. The `alpha` in the version means the schema can still change between releases; see [API Versioning](../api-versioning.md) for what is promised.

Short names make the daily commands shorter:

```bash
kubectl get pgw,pgds -n <ns>           # PostgresWatch and DeploySource
kubectl get pgreg -n <ns>              # PerformanceRegression
kubectl get pgreg -A                   # across every namespace
kubectl get pgrc                       # PostgresRadarCluster (cluster-scoped, no -n)
```

### Reading the columns

The CRDs define the columns `kubectl get` shows, so the table is a status summary on its own:

| Kind | Columns | What to look for |
|---|---|---|
| `PostgresWatch` | Cluster, Phase, tracked queries, Age | `Phase` should be `Running`. `Pending` or `Failed` has a reason in `status.message`. |
| `DeploySource` | Watch, Source, Phase, Webhook path, Age | `Phase` is `Ready` when the route (or watch) is active; `Pending` until its `PostgresWatch` exists. |
| `PerformanceRegression` | Watch, Query, Status, Confidence, P value, Latency change, Age | `Status` is `Detected`, `NoRegression` or `InsufficientData`. |

For the full picture of one object, `kubectl describe` (events and conditions) and `-o yaml` (every field):

```bash
kubectl describe postgreswatch orders -n <ns>
kubectl get pgreg -n <ns> -o yaml | less
```

## Discovering the schema

You do not need the docs open to write a CR. The schema is in the cluster:

```bash
kubectl explain postgreswatch                         # top-level fields
kubectl explain postgreswatch.spec                    # everything under spec
kubectl explain postgreswatch.spec.alerting           # a nested block
kubectl explain deploysource.spec.sourceType
```

The reference tables for every field are in the [Configuration Reference](../configuration.md); `explain` is the quick check while you edit.

## `status`, phases and conditions

CRs separate `spec` and `status` through the **status subresource**: the controller updates `status` with its own permissions, and an edit to `spec` does not wipe it. Two patterns you will see:

- **`status.phase`** (and `status.message`): a short state and, on failure, the reason. Read it as the first thing:
  ```bash
  kubectl get postgreswatch orders -n <ns> -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
  ```
- **`status.conditions`**: a list of `type`/`status`/`reason`/`message` entries, the standard Kubernetes way to report several independent facts. `kubectl describe` prints them in a readable table.

## Installing and upgrading the definitions

The CRDs must exist **before** any CR is created, and they must be **Established** (accepted by the API server) a moment after applying. Two rules cover nearly every problem:

**1. Wait for `Established` before creating CRs.** Applying CRDs and CRs in the same command can fail with `no matches for kind`, because the API server needs a moment to start serving the new kind:

```bash
kubectl apply -f crds/
kubectl wait --for=condition=Established --timeout=60s \
  crd/postgreswatches.radar.pgregressionradar.io \
  crd/deploysources.radar.pgregressionradar.io \
  crd/performanceregressions.radar.pgregressionradar.io
kubectl apply -f my-watch.yaml
```

**2. Helm installs CRDs on `install` only, never on `upgrade`.** This is deliberate Helm behaviour: CRDs are cluster-wide and removing or changing them can destroy data, so Helm does not touch them after the first install. To pick up a schema change from a newer chart, apply the files yourself:

```bash
kubectl apply -f deploy/helm/deploylens/crds/
```

!!! warning "Deleting a CRD deletes every object of that kind"
    `kubectl delete crd postgreswatches.radar.pgregressionradar.io` removes **all** `PostgresWatch` objects in every namespace, instantly. `helm uninstall` leaves CRDs in place for exactly this reason. Remove them only on purpose, and export what you want to keep first: `kubectl get pgreg -A -o yaml > regressions-backup.yaml`.

### Finalizers, in one paragraph

Some objects will not disappear on `kubectl delete`: they sit in `Terminating`. That is usually a **finalizer** (an entry in `metadata.finalizers`) telling Kubernetes "wait until a controller has cleaned up". If the controller is gone, the object waits forever. Bring the controller back, and the deletion completes. Removing finalizers by hand is a last resort that skips the cleanup.

## Leader election and the Lease

When you run several manager replicas, they coordinate through a `Lease` object (API group `coordination.k8s.io`) in the manager's namespace. Whoever holds the lease is the leader and does all the work; the rest stand by. If the leader stops renewing, another replica takes the lease. You can see it:

```bash
kubectl get lease -n <ns>
```

More on what this means for logs in [Pods and Deployments](workloads.md#replicas-leader-election-and-logs).

## See also

- [Objects and namespaces](objects.md): spec, status and namespaces, which everything above builds on.
- [RBAC and access](rbac.md): the permissions the controller needs to read and write these kinds.
- [Configuration Reference](../configuration.md): every field of every kind.
- [API Reference](../api-reference.md) and [API Versioning](../api-versioning.md): the schema and its stability.
- [Architecture Overview](../architecture.md): why `manager` mode is built this way.
