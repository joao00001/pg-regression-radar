# Objects and namespaces

*Objects, namespaces and labels, and the `kubectl` commands you will use every day.*

## Overview

Kubernetes has one idea underneath everything: you declare the state you want as an **object**, and controllers continuously work to make reality match it. A Deployment, a Secret, a Service and the radar's own `PostgresWatch` are all objects, and they all follow the same shape. Once that shape is familiar, so is every command in the rest of this section.

## The shape of an object

Every object has the same four top-level fields:

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1   # which API group and version defines this kind
kind: PostgresWatch                                # the type of object
metadata:                                          # identity: name, namespace, labels, annotations
  name: orders
  namespace: pgrr-lab
spec:                                              # what YOU want (desired state)
  clusterName: orders
  dsnSecretRef:
    name: postgres-credentials
    key: dsn
status:                                            # what IS (observed state), written by a controller
  phase: Running
```

Two rules matter more than any other:

- **You write `spec`; a controller writes `status`.** Do not edit `status` by hand. When you want to know whether the radar accepted your `PostgresWatch`, you read its `status`.
- **`apiVersion` + `kind` + `metadata.name` + `metadata.namespace` identify the object.** Applying the same YAML twice updates the object instead of creating a second one.

!!! note "In pg-regression-radar"
    The things you write are `PostgresWatch` (what database to watch and how sensitively) and `DeploySource` (where deploy events come from). The things the radar writes are `status` on both of those, and whole `PerformanceRegression` objects, one per detection. See [CRDs and operators](crds-operators.md).

## Namespaces

A **namespace** is a named partition of a cluster. Most objects live in exactly one namespace, and two objects of the same kind can share a name only if they are in different namespaces. Namespaces are how teams, environments and tools share a cluster without colliding.

```bash
kubectl get namespaces
kubectl create namespace pgrr-lab
kubectl get pods -n pgrr-lab        # one namespace
kubectl get pods -A                 # all namespaces
```

Some objects are **cluster-scoped** and belong to no namespace: nodes, CustomResourceDefinitions, ClusterRoles, and the radar's `PostgresRadarCluster`. You find out which is which with:

```bash
kubectl api-resources --namespaced=true
kubectl api-resources --namespaced=false
```

### Why namespaces matter for the radar

References between the radar's objects are **by name, within a namespace**. That is a rule you will trip on once if you do not know it:

| Reference | Where it is looked up |
|---|---|
| `DeploySource.spec.postgresWatchRef` | the `PostgresWatch` of that name **in the same namespace** as the `DeploySource` |
| `PostgresWatch.spec.dsnSecretRef` | the Secret of that name **in the same namespace** as the `PostgresWatch` (unless you use a [remote cluster](../multi-cluster.md)) |
| `DeploySource` with `sourceType: kubernetes`, `appName` | the Deployment or StatefulSet of that name **in the same namespace** as the `DeploySource` |

So the radar and the app you are watching can live in different namespaces, but the `PostgresWatch`, its `DeploySource` and the Secret holding the DSN are created together in one namespace, and a `kubernetes` deploy source watches workloads in that same namespace.

!!! tip "Set the namespace once"
    Typing `-n <ns>` on every command gets old. Pin it for your current context instead: `kubectl config set-context --current --namespace=<ns>`. Check what you are pinned to with `kubectl config view --minify | grep namespace`.

## Labels, selectors and annotations

**Labels** are key/value pairs on an object's metadata that Kubernetes uses to *select* things. **Annotations** are key/value pairs that carry information but are never used for selection.

```yaml
metadata:
  labels:
    app.kubernetes.io/name: orders-api
    app.kubernetes.io/component: api
  annotations:
    deployment.kubernetes.io/revision: "2"
```

A **selector** matches labels. A Service sends traffic to the pods whose labels match its selector; a Deployment manages the pods whose labels match its selector. That is the only glue between those objects, so a typo in a label is a silent failure: the Service exists and routes to nothing.

```bash
kubectl get pods -n <ns> --show-labels
kubectl get pods -n <ns> -l app.kubernetes.io/component=manager
kubectl label secret my-dsn -n <ns> some-key=some-value
kubectl label secret my-dsn -n <ns> some-key-          # trailing dash removes it
```

### Two metadata fields the radar relies on

- **The consent label.** `pg-regression-radar.io/allow-postgreswatch-access: "true"` on a Secret says "the owner of this Secret agrees that a `PostgresWatch` may read it". The radar refuses to use a Secret without it. The full reasoning is in [RBAC and access](rbac.md#the-consent-label); the how-to is in [Config and Secrets](config-secrets.md).
- **The revision annotation.** For a Deployment, `deployment.kubernetes.io/revision` is a counter Kubernetes bumps on every rollout. A `DeploySource` with `sourceType: kubernetes` uses it as the deploy's `revision`. See [Pods and Deployments](workloads.md#rollouts).

The Helm chart also puts the conventional `app.kubernetes.io/name`, `app.kubernetes.io/instance` and `app.kubernetes.io/component` labels on everything it creates, which is why `-l app.kubernetes.io/component=manager` selects the manager's objects.

## The `kubectl` commands you will use

| You want to | Command |
|---|---|
| List objects of a kind | `kubectl get <kind> -n <ns>` |
| Watch a list change live | `kubectl get <kind> -n <ns> -w` |
| See one object in full, as stored | `kubectl get <kind> <name> -n <ns> -o yaml` |
| See an object plus its recent **events** | `kubectl describe <kind> <name> -n <ns>` |
| Pull one field out | `kubectl get <kind> <name> -n <ns> -o jsonpath='{.status.phase}'` |
| Learn what fields a kind accepts | `kubectl explain postgreswatch.spec` |
| Create or update from a file | `kubectl apply -f file.yaml` |
| Delete | `kubectl delete -f file.yaml` or `kubectl delete <kind> <name> -n <ns>` |
| Read a pod's logs | `kubectl logs <pod> -n <ns>` (see [Troubleshooting toolkit](troubleshooting.md)) |

Three of these deserve a second look:

- **`describe` vs `get -o yaml`.** `get -o yaml` is the raw object; `describe` is a human summary that also lists **Events**, the short log Kubernetes keeps about what happened to the object. When something is stuck, `describe` is usually the first thing to run.
- **`explain` works for the radar's kinds too**, because their schemas are published in the cluster: `kubectl explain postgreswatch.spec.alerting` shows every field and its description without leaving the terminal.
- **`-o jsonpath`** is how you script against status, for example waiting for a watch to be ready: `kubectl get postgreswatch orders -n <ns> -o jsonpath='{.status.phase}'`.

### `apply`, not `create`

`kubectl create -f file.yaml` fails if the object exists. `kubectl apply -f file.yaml` creates it if it does not and updates it if it does, which is what lets you edit a manifest and re-apply it. Every manifest in this documentation is meant for `apply`.

```bash
kubectl apply -f my-watch.yaml
# edit my-watch.yaml
kubectl apply -f my-watch.yaml     # updates the same PostgresWatch in place
```

`kubectl apply` also accepts a URL or a directory, and a file with several objects separated by `---`, which is how the [lab](../lab.md) installs its whole stack in one command.

## Order matters for custom resources

Objects of a custom kind (like `PostgresWatch`) can only be created **after** their definition exists in the cluster. That is why the lab waits for the CRDs between steps:

```bash
kubectl wait --for=condition=Established --timeout=60s \
  crd/postgreswatches.radar.pgregressionradar.io
```

If you ever see `no matches for kind "PostgresWatch" in version "radar.pgregressionradar.io/v1alpha1"`, the definition is not installed yet. [CRDs and operators](crds-operators.md) explains why.

## See also

- [Pods and Deployments](workloads.md): the objects that actually run the radar and your app.
- [CRDs and operators](crds-operators.md): how `PostgresWatch` and friends come to exist.
- [Troubleshooting toolkit](troubleshooting.md): `describe`, events and logs in practice.
- [Configuration Reference](../configuration.md): every field of `PostgresWatch` and `DeploySource`.
