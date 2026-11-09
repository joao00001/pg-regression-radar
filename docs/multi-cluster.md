# Multi-Cluster (Fleet) Mode

*How one `cmd/manager` in a hub cluster watches Postgres/CloudNativePG clusters living in other ("spoke") Kubernetes clusters.*

## Overview

`cmd/manager` can reconcile any number of `PostgresWatch` CRs at once, each with its own `Collector`/`Engine`/`Notifier`/`ingester.Store` (see [Architecture Overview](architecture.md) and `internal/controller/registry.go`). When a Postgres cluster's DSN Secret lives in a *different* Kubernetes cluster than the manager, the fleet mode described on this page applies.

Two approaches are supported:

| Approach | Field | When to use |
|---|---|---|
| **Cluster registry** (recommended) | `spec.remoteClusterRef` | An administrator pre-registers the cluster; watch creators pick from the approved list. |
| **Direct Secret reference** (deprecated) | `spec.remoteClusterSecretRef` | Backward compatibility — still works in `controlled` security-profile mode, rejected in `hardened` mode. |

On top of resolving a DSN across clusters, fleet mode also covers two related concerns, each with its own section below: validating that a referenced CloudNativePG `Cluster` actually exists and is healthy ([`spec.cnpgClusterRef`](#remote-cloudnativepg-cluster-validation)), and continuously verifying that a registered cluster's kubeconfig is still good ([cluster health checks](#cluster-health-checks)) — independent of whether any `PostgresWatch` currently references that cluster.

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

## Generating and storing the kubeconfig Secret {#remote-cluster-rbac}

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

# Only needed when the PostgresWatch also sets spec.cnpgClusterRef — see
# "Remote CloudNativePG Cluster validation" below. Grants get on exactly
# one named Cluster object, nothing else (no access to the Cluster's own
# spec beyond what `get` on this one object implies, and no access to any
# other CloudNativePG resource type).
kubectl create role pg-regression-radar-cluster-reader \
  --verb=get --resource=clusters.postgresql.cnpg.io \
  --resource-name=<cnpg-cluster-name> \
  -n <spoke-namespace>

kubectl create rolebinding pg-regression-radar-cluster-reader \
  --role=pg-regression-radar-cluster-reader \
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

The Roles above each grant `get` on exactly one named object — the DSN Secret, and (only if `cnpgClusterRef` is set) the CloudNativePG `Cluster` — nothing else. Neither should grant `list`/`watch` on all objects of that type, access to any other resource type, or any verb beyond what's needed to read those two specific objects. This is an operational responsibility of whoever creates the kubeconfig Secret, not something `cmd/manager` or its own RBAC can enforce from the hub side.

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

Static-credential kubeconfigs — bearer token, client-certificate, or username/password `users[]` entries, with no `proxy-url` — are unaffected; those are exactly the shapes the [previous section](#remote-cluster-rbac)'s `kubectl create token`-based example produces. A rejected kubeconfig surfaces the same way any other DSN resolution failure does (`status.phase: Failed`), with an error naming the specific user/cluster entry and restriction that was violated.

## Remote CloudNativePG Cluster validation

`PostgresWatch.spec.cnpgClusterRef` names a real CloudNativePG `Cluster` object (`postgresql.cnpg.io/v1`) that must exist and be healthy before the watch is allowed to start — a cross-cluster complement to `dsnSecretRef`, since `clusterName` by itself is only ever a free-text label on metrics/`PerformanceRegression` CRs, not a live reference to anything.

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: prod-eu-west
  namespace: default
spec:
  clusterName: prod-eu-west
  remoteClusterRef: prod-eu-west
  cnpgClusterRef: prod-eu-west-pg    # the CloudNativePG Cluster's own name
  dsnSecretRef:
    name: prod-eu-west-app-superuser
    key: uri
```

`validateCNPGCluster` (`internal/controller/cnpg_cluster.go`) reads the named `Cluster` through the exact same client `dsnSecretRef` was just resolved through — the hub's own client when `remoteClusterRef`/`remoteClusterSecretRef` are unset, or the cached remote client for the spoke cluster otherwise — and in the exact same namespace `dsnSecretRef` was read from (`spec.remoteNamespace`, or the watch's own namespace by default). Nothing here reads the Cluster from a different cluster or namespace than the DSN itself came from.

"Healthy" is decided in two steps, matching how `kubectl get cluster` itself reports status:

1. If `status.conditions` contains a condition with `type: Ready`, its `status` is authoritative: `True` means healthy, anything else means not — the error message includes that condition's `reason`/`message` verbatim, so it reads the same as `kubectl describe cluster` would show.
2. Otherwise (an older CloudNativePG release that predates the `Ready` condition, or a cluster that hasn't reported status yet at all), fall back to `status.phase`: empty means not yet reconciled far enough to report anything (treated as unhealthy — the cluster "may still be initialising"), a phase containing "fail" (case-insensitive — CloudNativePG's own failed-state phase string is `"Cluster in failed state"`) is unhealthy, and any other non-empty phase (including CloudNativePG's several transient "setting up.../switching.../waiting for..." phases) is treated as healthy. This fallback is deliberately permissive: the point is catching a genuinely broken or missing target, not gating on every transient reconciliation phase of a cluster that's simply still starting up.

`cnpgClusterRef` requires no additional hub-side RBAC: `PostgresWatchReconciler` reads `Cluster` objects through whichever client it already built to resolve `dsnSecretRef`, the same way it reads Secrets. What it *does* require, when used alongside `remoteClusterRef`/`remoteClusterSecretRef`, is an additional grant in the kubeconfig's own embedded RBAC — `get` on the one named `Cluster` object, in addition to `get` on the DSN Secret — see [Remote cluster RBAC](#remote-cluster-rbac) above for the exact recipe. No consent label is required for the `Cluster` object itself (unlike `dsnSecretRef`/kubeconfig Secrets): it carries no credentials, and the confused-deputy concern the consent label exists for doesn't apply to a read-only health check of an object the kubeconfig's own RBAC already had to explicitly grant access to.

A missing `Cluster`, one without CloudNativePG CRDs installed at all, or one reporting unhealthy, all surface exactly like any other DSN resolution failure: `status.phase: Failed` with `status.message` naming the Cluster, its namespace, and precisely what was wrong.

## Cluster health checks {#cluster-health-checks}

Before `PostgresRadarClusterReconciler` existed, a registered cluster's kubeconfig going bad — rotated away from, revoked, or simply pointed at an address that stopped answering — was only ever discovered *reactively*, the next time some `PostgresWatch`'s `resolveDSN` happened to need that cluster, and never surfaced anywhere an administrator watching `PostgresRadarCluster.status` directly would see it independent of any particular watch.

`PostgresRadarClusterReconciler` (`internal/controller/postgresradarcluster_controller.go`) closes that gap by periodically re-probing every registered `PostgresRadarCluster` on its own schedule, regardless of whether any `PostgresWatch` currently references it:

- The probe itself is a single `discovery.ServerVersion()` call — deliberately the least-privileged check possible, since every Kubernetes cluster's default RBAC bindings expose the version/discovery endpoints to any authenticated identity (`system:discovery` bound to `system:authenticated`). A least-privilege kubeconfig built strictly per [Remote cluster RBAC](#remote-cluster-rbac) above (whose only grants are `get` on one Secret and, optionally, one `Cluster`) still satisfies this check — the health check never requires broader RBAC than the workload itself already needs.
- It runs on `--cluster-health-check-interval` (default `5m` — see [Configuration Reference](configuration.md#manager-flags-cmdmanager)), both for the very first probe after a `PostgresRadarCluster` is created and for every subsequent re-probe of an already-healthy cluster. An unhealthy result is re-probed on the same steady interval rather than any tighter backoff: the failure mode being watched for (an expired or rotated credential) doesn't resolve itself by retrying sooner.
- It builds its own fresh client per probe rather than reusing `PostgresWatchReconciler`'s `remoteClientCache` — that cache is sized and keyed around many reconciles sharing one hot client on a sub-minute cadence, whereas this reconciler runs per cluster on a multi-minute interval, so the cache's actual value proposition (TTL'd reuse) barely applies here.

The result is written to `PostgresRadarCluster.status`:

```yaml
status:
  ready: true
  lastProbeTime: "2026-01-15T10:32:07Z"
  conditions:
  - type: Ready
    status: "True"
    reason: HealthCheckSucceeded
    lastTransitionTime: "2026-01-15T10:32:07Z"
    message: "cluster responded to a discovery.ServerVersion() probe"
```

`status.ready` and `status.conditions` are kept in sync (`status: "True"` on the `Ready` condition implies `ready: true`) purely so anything that already reads the plain boolean doesn't need to walk `conditions` — `kubectl get postgresradarcluster` prints `status.ready` as its `Ready` column for exactly this reason. `status.lastProbeTime` is set on every probe regardless of outcome, so a `PostgresRadarCluster` that has simply never been reconciled yet (`lastProbeTime` absent) stays distinguishable from one that was just probed and found unhealthy (`lastProbeTime` recent, `ready: false`).

A failing probe does not itself evict anything from `remoteClientCache` or otherwise affect any in-flight `PostgresWatch` reconcile — it is a read-only, side-effect-free signal for observability and alerting (e.g. `kubectl get postgresradarcluster -o jsonpath='{.items[?(@.status.ready==false)].metadata.name}'` in a cron-based check, or a Prometheus alert over `kube_customresource_*` metrics if you export controller-runtime's own metrics alongside this project's), independent of and complementary to the evict-on-failure behavior described [below](#operational-considerations), which remains the mechanism that actually protects a live `PostgresWatch` reconcile from reusing a known-broken client.

## Operational considerations {#operational-considerations}

These are permanent, by-design characteristics of this feature, not open work tracked on the [Roadmap](roadmap.md) — understanding them matters for running fleet mode safely in production:

- **Kubeconfig rotation/expiration is detected, not auto-refreshed.** [Cluster health checks](#cluster-health-checks) above proactively surface a rotated, revoked, or expired kubeconfig on `PostgresRadarCluster.status` on a steady interval, independent of whether any `PostgresWatch` happens to be using it — but no code running in the hub cluster has the authority to mint a *fresh* credential for a spoke cluster it doesn't control, so actually refreshing the kubeconfig Secret's content remains an operational responsibility, same as rotating `dsnSecretRef` itself. The request-path behavior this used to be the only word on is still true and still matters alongside the health check: the moment a cached remote client fails a real request (`resolveDSN`), its entry is evicted from `remoteClientCache` immediately (`remoteClientCache.evict`, `internal/controller/remote_client.go`), rather than waiting for a future reconcile to keep reusing a client already known to be broken — so (a) a transient failure gets a genuinely fresh connection/transport on the very next attempt instead of reusing one that just failed, and (b) once an external rotator *does* rewrite the kubeconfig Secret with fresh credentials, the next reconcile picks that new content up immediately with no orphaned old entry left sitting in the cache. [Exec-based kubeconfig auth plugins](#kubeconfig-restrictions), which would otherwise get transparent, automatic refresh from client-go itself with no evict/re-fetch cycle needed at all, remain rejected outright, precisely because that same "runs a local process to get fresh credentials" mechanism is what makes them unsafe to accept from a tenant-controlled Secret.
- **The remote client cache's time-based eviction is independent of the health check.** Entries unused for over an hour are swept out (`internal/controller/remote_client.go`'s `Start`, registered as a manager-owned background task in `SetupWithManager`, running every 10 minutes) — a kubeconfig still referenced by a live `PostgresWatch` gets its cache entry refreshed at least every 30 seconds (the reconciler's status-refresh cadence), so this only ever reclaims clients for kubeconfigs that were rotated away from or whose `PostgresWatch`/Secret was deleted, never one still genuinely in use. By design, eviction isn't triggered directly by a Secret's deletion (it just stops being asked for and ages out on the next sweep), and [cluster health checks](#cluster-health-checks) above — a proactive, read-only reachability signal on `PostgresRadarCluster.status` — don't feed back into evicting or refreshing this cache; the two mechanisms serve different purposes (observability vs. request-path correctness) and are intentionally kept decoupled.
- **DSN-to-Postgres network reachability is always the operator's responsibility.** `remoteClusterRef`/`cnpgClusterRef` only ever grant the hub cluster *Kubernetes API* access to the spoke (an authenticated HTTPS call to read a Secret or a `Cluster`'s status) — they say nothing about, and cannot provide, network routability for the actual Postgres wire-protocol TCP connection the Collector makes once `resolveDSN` hands it a connection string. A DSN naming CloudNativePG's default in-cluster Service DNS host (`<cluster>-rw.<namespace>.svc`) is only resolvable and routable *inside* the spoke cluster itself; reaching it from the hub requires some real cross-cluster network path already in place (a shared VPC, a service-mesh multi-cluster mesh, Submariner, a VPN, or a CloudNativePG Service of type `LoadBalancer`/`ExternalName` with a genuinely routable address) — see [Real two-cluster end-to-end test](testing.md#e2e-kind-multicluster) for exactly this constraint surfacing (and a CI-only workaround) in a `kind`-based test rig specifically. This is an inherent property of running two separate Kubernetes clusters, not something a future version of this feature could remove.

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
- [Configuration Reference](configuration.md) — the full `PostgresWatch` spec field table, including `remoteClusterRef`, `cnpgClusterRef`, and the deprecated `remoteClusterSecretRef`, plus `--cluster-health-check-interval`.
- [Testing](testing.md#e2e-kind-multicluster) — the real two-`kind`-cluster end-to-end test proving `remoteClusterRef`, `cnpgClusterRef`, and the health-check reconciler against real API servers, including a simulated credential rotation.
- [Roadmap](roadmap.md) — where fleet/multi-cluster support sits against the rest of the version roadmap.

