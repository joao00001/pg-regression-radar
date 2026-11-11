# RBAC and access

*What permissions the radar asks for and why, how to check them, and the consent label for Secrets.*

## Overview

Kubernetes decides every request with **RBAC** (role-based access control): a *subject* (a user or a ServiceAccount) is granted *verbs* (`get`, `list`, `create`, ...) on *resources* (`pods`, `secrets`, `postgreswatches`, ...), cluster-wide or inside one namespace. The radar's manager is a program calling the Kubernetes API, so it has a ServiceAccount with a role, and that role is the whole of its power. This page explains the objects involved, then walks through what the radar gets and why.

## The objects

| Object | What it is | Scope |
|---|---|---|
| **ServiceAccount** | the identity a pod runs as when it calls the API | namespaced |
| **Role** | a list of permissions (verbs on resources) | one namespace |
| **ClusterRole** | the same, but cluster-wide, or for cluster-scoped resources | cluster |
| **RoleBinding** | grants a Role (or ClusterRole) to subjects **inside one namespace** | namespaced |
| **ClusterRoleBinding** | grants a ClusterRole to subjects **across the whole cluster** | cluster |

A role on its own does nothing; a binding is what attaches it to an identity. A rule looks like this:

```yaml
rules:
  - apiGroups: ["apps"]                  # the API group ("" is the core group: pods, secrets, services)
    resources: ["deployments"]
    verbs: ["get", "list", "watch"]      # read-only
```

The verbs that matter: `get` (one object), `list` and `watch` (many objects, and following changes), `create`, `update`, `patch`, `delete`. Controllers nearly always need `get`, `list` and `watch` together, because they keep a local cache fed by a watch.

## Testing permissions: `kubectl auth can-i`

You never have to guess what an identity can do:

```bash
kubectl auth can-i create customresourcedefinitions                      # you
kubectl auth can-i get secrets -n <ns>
kubectl auth can-i list deployments -A \
  --as=system:serviceaccount:<ns>:<serviceaccount>                       # the radar's ServiceAccount
kubectl auth can-i --list --as=system:serviceaccount:<ns>:<serviceaccount> -n <ns>
```

The ServiceAccount's name is `<fullname>` unless you set `serviceAccount.name`; `kubectl get serviceaccount -n <ns>` shows it. `--as` needs permission to impersonate, which cluster admins have.

A refused request is reported as `forbidden`, with the exact verb, resource and identity in the message:

```text
deployments.apps is forbidden: User "system:serviceaccount:radar:radar-deploylens"
cannot list resource "deployments" in API group "apps" at the cluster scope
```

That message is a complete diagnosis: that identity needs `list` on `deployments` in the `apps` group.

## What the radar's manager gets

The chart creates, in `manager` mode, a ServiceAccount, a ClusterRole with a ClusterRoleBinding, and a namespaced Role for leader election. Nothing in them can write to your workloads or read your Pods.

| Permission | Resources | Why |
|---|---|---|
| read | `postgreswatches`, `deploysources`, `postgresradarclusters` | to know what to do |
| update status | the `/status` of those kinds, and of `performanceregressions` | to report `phase` and `message` |
| read, create, update, patch | `performanceregressions` | to record each detection |
| read | `deployments`, `statefulsets` (`apps`) | the `kubernetes` deploy source watches rollouts. It never writes to a workload. |
| read | `secrets` (core group) | to resolve `dsnSecretRef` and other referenced Secrets |
| read, create, update, delete | `leases` (`coordination.k8s.io`), **in the manager's namespace only** | leader election |
| `get`, `patch` on `rollouts` | `argoproj.io`, **only if** `manager.autoAbort.rbacEnabled` | the optional [auto-abort](../auto-abort.md) feature |

Two things in that table are deliberate:

- **The cluster-wide Secret read.** The manager must be able to read a Secret in any namespace where someone creates a `PostgresWatch`, so it cannot be limited to one namespace. That is a big power, and the next section is what stops it being abused.
- **Everything optional is off by default.** Argo Rollouts access is granted only when you ask for it, so a cluster without it, or a team that never enables auto-abort, does not get the permission for nothing.

```bash
kubectl get clusterrole,clusterrolebinding | grep <fullname>
kubectl describe clusterrole <fullname>-manager-role
```

`operator` mode never talks to the Kubernetes API, so it needs none of this. You can even stop mounting a token in its pods with `serviceAccount.automountServiceAccountToken=false` ([Hardening](../deployment-security.md#serviceaccount-token-automount)). Keep it `true` in `manager` mode.

### The administrator role

The chart also creates a ClusterRole named `<fullname>-cluster-registry-admin`, which can fully manage `PostgresRadarCluster` objects (registrations of remote clusters). It is **not** bound to anyone. Bind it to your platform administrators, never to the manager's own ServiceAccount. See [Multi-Cluster (Fleet) Mode](../multi-cluster.md).

## The consent label

The cluster-wide Secret read creates a classic problem, a *confused deputy*. Suppose a team has permission to create `PostgresWatch` objects in their namespace, but not to read Secrets there. They could write `dsnSecretRef: {name: some-other-teams-secret}` and have the manager, which *can* read it, use it on their behalf. The manager's power would be exercised at the request of someone who does not have it.

The fix is in the radar, not in RBAC. The manager refuses to use any Secret unless its owner has opted it in with a label:

```bash
kubectl label secret orders-dsn -n <ns> pg-regression-radar.io/allow-postgreswatch-access=true
```

- If the label is missing, the `PostgresWatch` goes to `status.phase: Failed`, with the reason in `status.message`. It never reads the contents.
- Setting the label needs the same permission as editing the Secret. So only someone who could already change the Secret can opt it in.
- This applies to both `dsnSecretRef` and the kubeconfig Secret used for [remote clusters](../multi-cluster.md#secret-consent-label).

In a **single-team** cluster this is a one-line step and no more thought is needed. In a **shared** cluster, treat the label as a privilege of its own: if tenants can edit their own Secrets, they can label them too, and a tenant who can both label a Secret and create a `PostgresWatch` can point the radar at anything they can write. The repo ships three ready-to-adapt policies for locking this down:

| File | What it is |
|---|---|
| `docs/policies/rbac-consent-labeler-example.yaml` | a dedicated ServiceAccount and role that are the *only* identities allowed to set the label |
| `docs/policies/validating-admission-policy-consent-label.yaml` | a built-in (CEL) `ValidatingAdmissionPolicy` rejecting the label from anyone else; needs no extra software |
| `docs/policies/kyverno-consent-label-policy.yaml` | the same rule as a Kyverno policy, if you already run Kyverno |

The reasoning and the full threat model are in [Security Model](../security-model.md) and [Multi-Cluster (Fleet) Mode](../multi-cluster.md#secret-consent-label).

## Pod Security

Separate from RBAC, the cluster can restrict *what a pod may do* once it is allowed to exist: run as root, mount the host, add capabilities. **Pod Security Admission** does this with labels on the namespace:

```bash
kubectl label namespace <ns> \
  pod-security.kubernetes.io/enforce=restricted \
  pod-security.kubernetes.io/warn=restricted
```

The chart's defaults already satisfy the strictest level (`restricted`): non-root user, no privilege escalation, all capabilities dropped, a read-only root filesystem and the default seccomp profile. So you can enforce `restricted` on the radar's namespace without changing a value. See [Hardening (Helm)](../deployment-security.md#pod-security-standards-pss-restricted).

## Quick reference

| You see | It means | Fix |
|---|---|---|
| `forbidden` in the manager's logs | its role is missing a verb or resource | `kubectl auth can-i ... --as=system:serviceaccount:<ns>:<sa>`, then check the ClusterRole and its binding |
| `PostgresWatch` in `Failed`, message mentions the label | the Secret has no consent label | label the Secret ([above](#the-consent-label)) |
| `Error from server (Forbidden)` on your own `kubectl apply` | **your** user lacks the permission, not the radar | `kubectl auth can-i <verb> <resource> -n <ns>` |
| `cannot create resource "customresourcedefinitions"` on install | CRDs are cluster-scoped and you are not a cluster admin | ask an admin to install the CRDs once |
| pods rejected with `violates PodSecurity` | a namespace policy conflicts with an overridden security context | remove the override or relax the namespace label |

## See also

- [Config and Secrets](config-secrets.md): creating and labelling the DSN Secret.
- [Security Model](../security-model.md): the radar's threat model.
- [Hardening (Helm)](../deployment-security.md): NetworkPolicy, quotas, Pod Security.
- [Multi-Cluster (Fleet) Mode](../multi-cluster.md): the RBAC split between hub and spoke clusters.
