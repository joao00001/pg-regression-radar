# Multi-Cluster (Fleet) Mode

*How one `cmd/manager` in a hub cluster watches Postgres/CloudNativePG clusters living in other ("spoke") Kubernetes clusters, and what's still missing to call it complete.*

## Overview

`cmd/manager` has always been able to reconcile any number of `PostgresWatch` CRs at once, each with its own `Collector`/`Engine`/`Notifier`/`ingester.Store` (see [Architecture Overview](architecture.md) and `internal/controller/registry.go`) — that part of "multi-cluster support" predates this page. What it could not do until now is reach a Postgres cluster whose CloudNativePG-generated DSN Secret lives in a *different* Kubernetes cluster than the one the manager itself runs in. This page covers `spec.remoteClusterSecretRef`, the field that closes that gap, the hub-spoke model it implements, and the security/RBAC split that comes with it.

## The hub-spoke model

- **Hub cluster**: where `cmd/manager` (the Deployment, its ServiceAccount, and its RBAC) actually runs. This is the only cluster the manager's own Kubernetes client (`r.Client` in `internal/controller/postgreswatch_controller.go`) can talk to.
- **Spoke cluster(s)**: any number of other Kubernetes clusters, each typically running its own CloudNativePG installation and Postgres cluster(s). The manager never runs anything in a spoke cluster; it only reads one Secret from it.

A `PostgresWatch` created in the hub cluster opts into fleet mode by setting two things:

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: prod-eu-west
  namespace: default
spec:
  clusterName: prod-eu-west
  dsnSecretRef:
    name: prod-eu-west-app-superuser  # a Secret name, looked up in the SPOKE cluster
    key: uri                          # CloudNativePG's generated key for the connection URI
  remoteClusterSecretRef:
    name: prod-eu-west-kubeconfig     # a Secret in the HUB cluster, in this same namespace
    key: kubeconfig
```

This is the same shape Cluster API, Argo CD's cluster Secrets, and Open Cluster Management all converge on for "let a central controller reach a remote cluster's resources": a kubeconfig, stored as a Secret in the controller's own cluster, is the sole credential needed to act as if the controller were running inside the remote cluster.

## What actually happens on reconcile

`PostgresWatchReconciler.resolveDSN` → `dsnSecretClient` (`internal/controller/postgreswatch_controller.go`) decide, on every reconcile, which Kubernetes API server to read `spec.dsnSecretRef` from:

1. **`remoteClusterSecretRef` unset (default):** read the DSN Secret with the manager's own client, exactly as before this feature existed. Zero behavior change, zero new RBAC needed.
2. **`remoteClusterSecretRef` set:**
   a. Read the kubeconfig Secret it names — **in the hub cluster**, in the `PostgresWatch`'s own namespace — using the manager's own client (the same "get;list;watch Secrets" RBAC it already needs for `dsnSecretRef` covers this; see below).
   b. Build (or reuse a cached) `client.Client` from that kubeconfig via `clientcmd.RESTConfigFromKubeConfig` + `client.New` (`internal/controller/remote_client.go`).
   c. Read `spec.dsnSecretRef` **through that remote client**, against the Secret of the same name **in a namespace of the same name** as the `PostgresWatch`'s own namespace — mirroring the convention CloudNativePG itself uses for its generated credential Secrets. There is deliberately no separate "remote namespace" field; see [Known gaps](#known-gaps-and-deliberate-scope-cuts) if that turns out to matter for your fleet.

Any failure in that chain — the kubeconfig Secret doesn't exist, its key is missing, the kubeconfig is malformed, or the remote API server is unreachable/rejects the request — is treated exactly like any other DSN resolution failure: `Reconcile` calls `markFailed`, which sets `status.phase = Failed`, `status.message` to the underlying error, and returns the error so controller-runtime's rate limiter retries with backoff. There is no special-cased fleet error path; a broken remote cluster looks, to an operator running `kubectl get postgreswatch`, exactly like a broken DSN.

## Generating and storing the kubeconfig Secret

The kubeconfig you put in the Secret should be scoped down to the bare minimum on the **spoke** side — least privilege lives entirely in the kubeconfig's own embedded credentials/RBAC, not in anything the hub cluster enforces (the hub cluster has no way to know or limit what a kubeconfig can do once it's handed to a remote API server). A reasonable way to build one:

```bash
# On/against the SPOKE cluster:
kubectl create serviceaccount pg-regression-radar-reader -n <spoke-namespace>

kubectl create role pg-regression-radar-dsn-reader \
  --verb=get --resource=secrets \
  --resource-name=<dsn-secret-name> \
  -n <spoke-namespace>

kubectl create rolebinding pg-regression-radar-dsn-reader \
  --role=pg-regression-radar-dsn-reader \
  --serviceaccount=<spoke-namespace>:pg-regression-radar-reader \
  -n <spoke-namespace>

# Mint a kubeconfig for that ServiceAccount only (exact steps depend on your
# cluster's token/cert issuance setup — a short-lived ServiceAccount token
# via `kubectl create token`, or a long-lived one via a Secret of type
# kubernetes.io/service-account-token, both work as the "users.user.token"
# field of a kubeconfig).

# On the HUB cluster:
kubectl create secret generic prod-eu-west-kubeconfig \
  --from-file=kubeconfig=./spoke-scoped.kubeconfig \
  -n default
```

The Role above grants `get` on exactly one named Secret — nothing else. It should **not** grant `list`/`watch` on all Secrets, access to any other resource type, or any verb beyond what's needed to read that one DSN Secret. This is an operational responsibility of whoever creates the kubeconfig Secret, not something `cmd/manager` or its own RBAC can enforce from the hub side.

## RBAC: two separate concerns

It's easy to conflate "the manager's own RBAC" with "what the manager can reach in a spoke cluster" — they are unrelated:

| | Grants | Enforced by |
|---|---|---|
| Hub ServiceAccount RBAC (`config/rbac/role.yaml`, `+kubebuilder:rbac` marker above `PostgresWatchReconciler.Reconcile`) | Read access to **Secrets in the hub cluster** — this already covered `dsnSecretRef` before this feature, and now also covers reading the `remoteClusterSecretRef` kubeconfig Secret. Nothing about this RBAC changes what can be reached in a spoke cluster. | The hub cluster's API server |
| The kubeconfig's own embedded credentials | Whatever the spoke cluster's API server decides that identity can do — should be scoped to `get` on exactly the one Secret `dsnSecretRef` names, per the previous section. | The **spoke** cluster's API server |

`config/rbac/role.yaml`'s existing `secrets: get;list;watch` ClusterRole rule is unchanged by this feature — it was already necessary and sufficient for `dsnSecretRef`, and the exact same rule is what makes `remoteClusterSecretRef` kubeconfig Secrets readable too, since both live in the hub cluster. If you're running a hardened setup that narrows that ClusterRole to specific Secret names (e.g. via `resourceNames`), remember to include your kubeconfig Secrets in that list alongside your DSN Secrets.

## Known gaps and deliberate scope cuts

This feature closes the "Postgres lives in a different Kubernetes cluster" gap, but intentionally does not attempt everything a mature fleet story eventually needs:

- **No kubeconfig rotation/expiration handling.** A short-lived token embedded in a kubeconfig Secret that expires will start failing DSN resolution (surfacing as `status.phase: Failed`) with no automatic re-authentication — refreshing the Secret's content is an operational responsibility, same as rotating `dsnSecretRef` itself.
- **The remote client cache never evicts entries** (`internal/controller/remote_client.go`). Each distinct kubeconfig content seen gets a cached `client.Client` (and its underlying TLS transport) that lives for the life of the manager process. For the fleet sizes this project targets (tens, not thousands, of `PostgresWatch` CRs / spoke clusters) that's an acceptable amount of memory; it would need revisiting — an LRU, a TTL, or explicit eviction on Secret deletion — at much larger scale.
- **No fixed remote-namespace override.** The DSN Secret is looked up in a namespace of the same *name* on the spoke cluster as the `PostgresWatch`'s own namespace in the hub cluster. If your fleet's naming conventions don't line up namespace-for-namespace between hub and spokes, this won't resolve correctly — a `remoteNamespace` field would be a natural follow-up if that turns out to matter in practice.
- **The CloudNativePG `Cluster` resource itself is not read remotely.** Only the generated DSN Secret is fetched from the spoke cluster; nothing here reads spoke-side `Cluster`/`Pooler`/etc. custom resources to, say, auto-discover DSN Secret names or validate the target actually exists. `clusterName` remains a free-text label, not a live cross-cluster reference.
- **Not validated against a real second Kubernetes cluster.** Testing here uses `sigs.k8s.io/controller-runtime/pkg/client/fake` for the hub-side Secret reads, and a syntactically valid kubeconfig pointing at an address nothing listens on to exercise the "unreachable remote cluster" failure path (see `internal/controller/postgreswatch_controller_test.go` and `remote_client_test.go`) — the same sandboxed-CI constraint noted elsewhere in [the roadmap](roadmap.md#known-robustness-gaps) for real-`kind`-cluster validation applies doubly here, since it would need *two* real clusters.

## See also

- [Architecture Overview](architecture.md) — how `cmd/manager` reconciles many `PostgresWatch` CRs today, hub-spoke or not.
- [Configuration Reference](configuration.md) — the full `PostgresWatch` spec field table, including `remoteClusterSecretRef`.
- [Roadmap](roadmap.md) — where fleet/multi-cluster support sits against the rest of the version roadmap.
