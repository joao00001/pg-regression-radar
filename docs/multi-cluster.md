# Multi-Cluster (Fleet) Mode

*How one `cmd/manager` in a hub cluster watches Postgres/CloudNativePG clusters living in other ("spoke") Kubernetes clusters.*

## Overview

`cmd/manager` can reconcile any number of `PostgresWatch` CRs at once, each with its own `Collector`/`Engine`/`Notifier`/`ingester.Store` (see [Architecture Overview](architecture.md) and `internal/controller/registry.go`). When a Postgres cluster's DSN Secret lives in a *different* Kubernetes cluster than the manager, the fleet mode described on this page applies.

Two approaches are supported:

| Approach | Field | When to use |
|---|---|---|
| **Cluster registry** (recommended) | `spec.remoteClusterRef` | An administrator pre-registers the cluster; watch creators pick from the approved list. |
| **Direct Secret reference** (deprecated) | `spec.remoteClusterSecretRef` | Backward compatibility — still works in `controlled` security-profile mode, rejected in `hardened` mode. |

## Cluster registry

### Registering a cluster

An administrator creates a `PostgresRadarCluster` resource (cluster-scoped) for each remote cluster. Only users with create/update/delete access to `PostgresRadarCluster` can add or change cluster registrations — watch creators have no say in which clusters are reachable.

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresRadarCluster
metadata:
  name: prod-eu-west
spec:
  kubeconfigSecretRef:
    namespace: pg-radar-system   # namespace of the kubeconfig Secret in the HUB cluster
    name: prod-eu-west-kubeconfig
    key: kubeconfig
  namespace: postgres            # optional: default namespace on the remote cluster for DSN Secrets
```

The kubeconfig Secret must carry the [consent label](#secret-consent-label) and must contain only static credentials (bearer token or client certificate). See [Kubeconfig restrictions](#kubeconfig-restrictions) for what is rejected and why.

```bash
kubectl create secret generic prod-eu-west-kubeconfig \
  --from-file=kubeconfig=./spoke-scoped.kubeconfig \
  -n pg-radar-system

kubectl label secret prod-eu-west-kubeconfig \
  pg-regression-radar.io/allow-postgreswatch-access=true \
  -n pg-radar-system
```

### Referencing a registered cluster

A `PostgresWatch` references a registered cluster by name via `spec.remoteClusterRef`:

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: prod-eu-west
  namespace: default
spec:
  clusterName: prod-eu-west
  remoteClusterRef: prod-eu-west     # must match a PostgresRadarCluster name
  dsnSecretRef:
    name: prod-eu-west-app-superuser # Secret name on the SPOKE cluster
    key: uri
```

If the named `PostgresRadarCluster` does not exist, reconciliation fails with `status.phase: Failed` and a clear error message, exactly like any other DSN resolution failure.

### Security profiles {#security-profiles}

The manager and operator support a `--security-profile` flag that controls whether the deprecated `remoteClusterSecretRef` path is still accepted:

- **`controlled`** (default): both `remoteClusterRef` and `remoteClusterSecretRef` work. A deprecation warning is logged whenever the old field is used. This is the backward-compatible default for existing installations.
- **`hardened`**: `remoteClusterSecretRef` is rejected outright with a clear error. Only the cluster registry path (`remoteClusterRef`) is allowed.

Use `hardened` in new installations and in clusters where you want to enforce that only pre-registered clusters are reachable.

### RBAC for cluster administrators

Only bind `create`/`update`/`delete` on `postgresradarclusters` to administrators — watch creators need no write access to this resource. The Helm chart creates a `ClusterRole` for this purpose:

```bash
kubectl create clusterrolebinding pg-radar-cluster-admins \
  --clusterrole=<release>-cluster-registry-admin \
  --group=cluster-admins
```

The manager's own `ServiceAccount` only needs `get;list;watch` on `postgresradarclusters` (already included in the generated `ClusterRole`).

---

## The hub-spoke model

- **Hub cluster**: where `cmd/manager` (the Deployment, its ServiceAccount, and its RBAC) actually runs. This is the only cluster the manager's own Kubernetes client (`r.Client` in `internal/controller/postgreswatch_controller.go`) can talk to.
- **Spoke cluster(s)**: any number of other Kubernetes clusters, each typically running its own CloudNativePG installation and Postgres cluster(s). The manager never runs anything in a spoke cluster; it only reads one Secret from it.

## What actually happens on reconcile

`PostgresWatchReconciler.resolveDSN` → `dsnSecretClient` (`internal/controller/postgreswatch_controller.go`) decide, on every reconcile, which Kubernetes API server to read `spec.dsnSecretRef` from:

1. **`remoteClusterRef` set (recommended):** look up the named `PostgresRadarCluster`, read its kubeconfig Secret (from the hub cluster, in whatever namespace the `PostgresRadarCluster.spec.kubeconfigSecretRef.namespace` specifies), build (or reuse a cached) `client.Client`, and read `spec.dsnSecretRef` through that remote client. The `PostgresRadarCluster` must exist; if it doesn't, `Reconcile` marks the watch `Failed` immediately.
2. **`remoteClusterSecretRef` set (deprecated):** read the kubeconfig Secret it names — **in the hub cluster**, in the `PostgresWatch`'s own namespace — then follow the same build/cache/read path as above. Accepted in `controlled` profile (default), rejected in `hardened` profile.
3. **Neither set (default):** read the DSN Secret with the manager's own (hub) client, exactly as before fleet mode existed. Zero behavior change, zero new RBAC needed.

In all cases, `spec.remoteNamespace` (if set alongside a remote cluster path) overrides which namespace on the remote cluster is used to look up the DSN Secret — see the `remoteNamespace` field in the [Configuration Reference](configuration.md).

Any failure in that chain — the `PostgresRadarCluster` doesn't exist, its kubeconfig Secret is missing, the kubeconfig is malformed, or the remote API server is unreachable — is treated exactly like any other DSN resolution failure: `Reconcile` calls `markFailed`, setting `status.phase: Failed`, `status.message` to the error, and returning it so controller-runtime's rate limiter retries with backoff.

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

# Required — see "Secret consent label" below: without this label the
# manager refuses to read this Secret at all, regardless of RBAC.
kubectl label secret prod-eu-west-kubeconfig \
  pg-regression-radar.io/allow-postgreswatch-access=true \
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

## Secret consent label

That cluster-wide `secrets: get;list;watch` grant means anyone who can create or edit a `PostgresWatch` in a namespace — a much weaker permission than being able to read Secrets in that namespace directly — could otherwise name *any* Secret there in `dsnSecretRef` or `remoteClusterSecretRef` and have the manager read it on their behalf: a classic confused-deputy path, since it's the manager's own broader Secret-read privilege being exercised at the referencer's request, not the Secret owner's.

`internal/controller/secret_consent.go` closes this: before either Secret's contents are used, the manager checks it carries the label

```
pg-regression-radar.io/allow-postgreswatch-access: "true"
```

If the label is missing, `resolveDSN`/`dsnSecretClient` return an error (surfaced the same way any other DSN resolution failure is — `status.phase: Failed`, `status.message` set) instead of using the Secret's contents. This applies to **both** `dsnSecretRef` (the DSN Secret itself) and `remoteClusterSecretRef` (the kubeconfig Secret) — whoever owns a Secret must explicitly opt it in before any `PostgresWatch` can reference it, in the hub cluster or, via a remote kubeconfig, in a spoke cluster's own Secrets:

```bash
kubectl label secret prod-eu-west-app-superuser \
  pg-regression-radar.io/allow-postgreswatch-access=true \
  -n <namespace>
```

This is a namespace-local decision the Secret's own owner makes (labeling a Secret requires the same `patch`/`update` permission on that Secret that creating it did), independent of whatever RBAC governs who can create `PostgresWatch` CRs — it doesn't replace narrowing that RBAC or the ClusterRole above where you can, it closes the gap for installations where you can't (or haven't yet).

For **multi-tenant clusters**, treat that label as a privileged capability: add an admission policy so only approved identities can set it. A ready-to-adapt Kyverno example (edit its allowed-identity list for your environment) lives at [`docs/policies/kyverno-consent-label-policy.yaml`](policies/kyverno-consent-label-policy.yaml).

## Kubeconfig restrictions

Because a `remoteClusterSecretRef` kubeconfig comes from a Secret a `PostgresWatch`'s owner controls — untrusted input from the manager's perspective, even after the consent label above — `buildRemoteClient` (`internal/controller/remote_client.go`) rejects any kubeconfig that would hand the manager process more capability than "talk to a remote API server with a static credential":

| Rejected | Why |
|---|---|
| `users[].exec` (exec-based credential plugins) | client-go would execute it as a local process inside the manager's own pod every time the resulting client authenticates — a kubeconfig-controlled arbitrary command, not just a credential. |
| `users[].auth-provider` (deprecated auth-provider plugins) | Same "plugin decides how to authenticate" shape as `exec`; some implementations shell out too. |
| `clusters[].proxy-url` | Would route the manager's API traffic for that cluster through a proxy endpoint the kubeconfig's author chooses, not the cluster operator — a network egress redirection risk. |

Static-credential kubeconfigs — bearer token, client-certificate, or username/password `users[]` entries, with no `proxy-url` — are unaffected; those are exactly the shapes the [previous section](#generating-and-storing-the-kubeconfig-secret)'s `kubectl create token`-based example produces. A rejected kubeconfig surfaces the same way any other DSN resolution failure does (`status.phase: Failed`), with an error naming the specific user/cluster entry and restriction that was violated.

## Known gaps and deliberate scope cuts

This feature closes the "Postgres lives in a different Kubernetes cluster" gap, but intentionally does not attempt everything a mature fleet story eventually needs:

- **No kubeconfig rotation/expiration handling beyond evict-on-failure.** A short-lived static token embedded in a kubeconfig Secret that genuinely expires still fails DSN resolution the same way (surfacing as `status.phase: Failed`) — no code running in the hub cluster has the authority to mint a fresh credential for a spoke cluster it doesn't control, so refreshing the Secret's content remains an operational responsibility, same as rotating `dsnSecretRef` itself. What *is* handled: the moment a cached remote client fails a real request (`resolveDSN`), its entry is evicted from `remoteClientCache` immediately (`remoteClientCache.evict`, `internal/controller/remote_client.go`), rather than waiting for a future reconcile to keep reusing a client already known to be broken. This means (a) a transient failure gets a genuinely fresh connection/transport on the very next attempt instead of reusing one that just failed, and (b) once an external rotator *does* rewrite the kubeconfig Secret with fresh credentials, the next reconcile picks that new content up immediately with no orphaned old entry left sitting in the cache. This is now the *only* supported way to keep credentials current — [exec-based kubeconfig auth plugins](#kubeconfig-restrictions), which would otherwise get transparent, automatic refresh from client-go itself with no evict/re-fetch cycle needed at all, are rejected outright, precisely because that same "runs a local process to get fresh credentials" mechanism is what makes them unsafe to accept from a tenant-controlled Secret.
- **The remote client cache's time-based eviction has no failure-aware equivalent.** Entries unused for over an hour are swept out (`internal/controller/remote_client.go`'s `Start`, registered as a manager-owned background task in `SetupWithManager`, running every 10 minutes) — a kubeconfig still referenced by a live `PostgresWatch` gets its cache entry refreshed at least every 30 seconds (the reconciler's status-refresh cadence), so this only ever reclaims clients for kubeconfigs that were rotated away from or whose `PostgresWatch`/Secret was deleted, never one still genuinely in use. What's still out of scope: eviction isn't triggered directly by a Secret's deletion (it just stops being asked for and ages out on the next sweep), and there's no proactive health check of a cached client's remote reachability — that still only surfaces as a failed request.
- **The CloudNativePG `Cluster` resource itself is not read remotely.** Only the generated DSN Secret is fetched from the spoke cluster; nothing here reads spoke-side `Cluster`/`Pooler`/etc. custom resources to, say, auto-discover DSN Secret names or validate the target actually exists. `clusterName` remains a free-text label, not a live cross-cluster reference.
- **Not validated against a real second Kubernetes cluster.** Testing here uses `sigs.k8s.io/controller-runtime/pkg/client/fake` for the hub-side Secret reads, and a syntactically valid kubeconfig pointing at an address nothing listens on to exercise the "unreachable remote cluster" failure path (see `internal/controller/postgreswatch_controller_test.go` and `remote_client_test.go`) — the same sandboxed-CI constraint noted elsewhere in [the roadmap](roadmap.md#known-robustness-gaps) for real-`kind`-cluster validation applies doubly here, since it would need *two* real clusters.

## Migration from `remoteClusterSecretRef` to `remoteClusterRef` {#migration}

The `remoteClusterSecretRef` field on `PostgresWatch` is deprecated. It continues to work in `controlled` security-profile mode (the default), but will be rejected in `hardened` mode. Migrating is a two-step process:

**Step 1 — Register the cluster as a `PostgresRadarCluster`.**

Move the kubeconfig Secret to the administrator-managed namespace (e.g. `pg-radar-system`) if it isn't there already, and create the `PostgresRadarCluster`:

```bash
# Move the kubeconfig Secret if needed (or create a new one in the admin namespace)
kubectl create secret generic prod-eu-west-kubeconfig \
  --from-file=kubeconfig=./spoke-scoped.kubeconfig \
  -n pg-radar-system

kubectl label secret prod-eu-west-kubeconfig \
  pg-regression-radar.io/allow-postgreswatch-access=true \
  -n pg-radar-system

# Create the PostgresRadarCluster
kubectl apply -f - <<EOF
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresRadarCluster
metadata:
  name: prod-eu-west
spec:
  kubeconfigSecretRef:
    namespace: pg-radar-system
    name: prod-eu-west-kubeconfig
    key: kubeconfig
EOF
```

**Step 2 — Update each `PostgresWatch` to use `remoteClusterRef`.**

```bash
kubectl patch postgreswatch prod-eu-west \
  --type=merge \
  -p '{"spec":{"remoteClusterRef":"prod-eu-west","remoteClusterSecretRef":null}}'
```

Or apply a new manifest replacing `remoteClusterSecretRef` with `remoteClusterRef`:

```yaml
spec:
  remoteClusterRef: prod-eu-west    # replaces remoteClusterSecretRef
  dsnSecretRef:
    name: prod-eu-west-app-superuser
    key: uri
```

After migration, the old kubeconfig Secret in the watch's namespace can be removed (once no other watch references it).

## See also

- [Architecture Overview](architecture.md) — how `cmd/manager` reconciles many `PostgresWatch` CRs today, hub-spoke or not.
- [Configuration Reference](configuration.md) — the full `PostgresWatch` spec field table, including `remoteClusterRef` and the deprecated `remoteClusterSecretRef`.
- [Roadmap](roadmap.md) — where fleet/multi-cluster support sits against the rest of the version roadmap.

