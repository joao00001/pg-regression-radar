# Security Model & Threat Model

*Formal threat model and security contract for who may configure pg-regression-radar, which Secrets it may read, and where it may connect in each supported security profile.*

## Overview

This page defines Stage 1's security contract for `cmd/manager` mode: trust assumptions, protected assets, authorization boundaries, and profile-specific operational rules. It is intentionally concrete: an administrator should be able to answer (a) who can create each CRD, (b) which Secrets may be read, (c) where the manager may connect, and (d) what changes between `controlled`, `hardened`, and `relay-only`.

The model applies to the Kubernetes/CRD deployment path (`PostgresWatch` + `DeploySource`), including fleet mode via `spec.remoteClusterSecretRef`. It does **not** cover host/OS hardening, cloud IAM design outside Kubernetes, or arbitrary third-party webhook endpoints beyond the controls listed here.

## Security profiles

| Profile | Intended environment | Trust assumption | Tenancy model | Default recommendation |
|---|---|---|---|---|
| `controlled` | Single-tenant cluster or one trusted platform team | People with namespace write access are trusted operators | Single team or tightly coordinated teams | Good baseline when operational simplicity matters most |
| `hardened` | Shared cluster with multiple teams and sensitive production workloads | CRD authors are **not** automatically trusted to select any Secret/destination | Multi-team, namespace-isolated | Recommended for most production shared clusters |
| `relay-only` | Highly restricted environments (regulated or strict network controls) | Manager should not be used as a direct cross-cluster credential broker | Multi-team with central integration point | Use when direct DB Secret access by manager is not acceptable |

### `controlled` profile (single-tenant, trusted teams)

Supported:

- `PostgresWatch` and `DeploySource` can be created by trusted team operators in their namespace.
- `spec.remoteClusterSecretRef` is allowed when the same trusted team also owns the remote-cluster kubeconfig lifecycle.
- Alert destinations may be team-defined, still subject to built-in URL checks.

Practical example:

- A single platform team runs one cluster, owns all namespaces, and uses one manager instance to watch multiple Postgres clusters (including one remote spoke) using team-managed kubeconfig Secrets.

### `hardened` profile (multi-team, sensitive production)

Supported:

- Teams may create `PostgresWatch`/`DeploySource` only in their own namespace.
- Only approved identities may apply the Secret consent label (`pg-regression-radar.io/allow-postgreswatch-access=true`).
- Remote-cluster references are allowed only for platform-approved kubeconfig Secrets.
- Alert destinations should be restricted with `--alerting-allowed-destinations` and egress policy.

Practical example:

- Team A can create a `PostgresWatch`, but cannot force the manager to read Team B's Secret because unlabeled Secrets are rejected and consent labeling is centrally restricted by admission policy.

### `relay-only` profile (highly restricted)

Supported:

- Teams may define correlation intent (`PostgresWatch` + `DeploySource`) through platform-approved templates/policies, but should not manage remote-cluster references directly.
- `spec.remoteClusterSecretRef` is disallowed by policy (admission/RBAC convention) unless approved as break-glass by platform admins.
- Manager network egress is restricted to approved alert relays and required in-cluster APIs.

Not supported (by contract):

- Tenant-managed direct remote-cluster kubeconfig usage.
- Arbitrary tenant-defined external alert endpoints.

Practical example:

- A regulated environment routes all alerts through an internal relay service; teams cannot point `alerting.url` to internet endpoints and cannot attach remote kubeconfigs to watches.

## Trust model

### Actors

| Actor | Typical identity | Trusted for | Not trusted for (without extra controls) |
|---|---|---|---|
| Cluster administrator | Cluster-admin / platform SRE | Cluster-wide RBAC, admission policy, namespace boundaries | Day-to-day app release intent in every team |
| Team lead | Namespace admin role | Creating team-scoped watches/sources, selecting approved Secrets | Global Secret brokering across namespaces/teams |
| Application developer | CI/CD or developer role with limited namespace write | Proposing `DeploySource` changes for owned app | Choosing privileged Secrets or remote kubeconfigs |
| Operator (manager ServiceAccount) | `manager-role` ClusterRole binding | Reading referenced Secrets and reconciling CRDs within granted RBAC | Making trust decisions beyond configured labels/RBAC/policies |

### Protected assets

| Asset | Why sensitive |
|---|---|
| PostgreSQL DSN credentials (`dsnSecretRef`) | Direct database access; may include high-privilege roles |
| Remote-cluster kubeconfig Secrets (`remoteClusterSecretRef`) | Delegates Kubernetes API access to spoke clusters |
| Alert destination configuration (`alerting.url`, webhook targets) | Potential data exfiltration/SSRF route |
| Deployment metadata (`DeployEvent`, app/revision/cluster mapping) | Operational intelligence about release cadence and topology |

### Trust boundaries

| Boundary | Crossing event | Main risk | Mitigation |
|---|---|---|---|
| Tenant authoring CRDs -> manager reading Secrets | A `PostgresWatch` references `dsnSecretRef`/`remoteClusterSecretRef` | Confused deputy (manager reads Secrets broader than author's own rights) | Secret consent label check before use ([Multi-Cluster: Secret consent label](multi-cluster.md#secret-consent-label)) |
| Hub cluster -> spoke cluster Kubernetes API | `remoteClusterSecretRef` kubeconfig is used to build remote client | Credential/plugin abuse, proxy-based traffic redirection | Kubeconfig auth restrictions (`exec`, `auth-provider`, `proxy-url` rejected) ([Multi-Cluster: Kubeconfig restrictions](multi-cluster.md#kubeconfig-restrictions)) |
| Manager -> external alert endpoint | Notifier sends payloads to configured destination | SSRF/data exfiltration to unsafe endpoints | URL validation + optional strict allowlist ([Alerting: Destination validation](alerting.md#destination-validation-ssrf-hardening)) |

### Operator responsibilities vs Kubernetes responsibilities

| Concern | Kubernetes platform is responsible for | pg-regression-radar operator is responsible for |
|---|---|---|
| Who can create/modify CRDs | RBAC/admission for `PostgresWatch` and `DeploySource` | Respecting CRD intent, not bypassing API authorization |
| Secret ownership and eligibility | Secret creation rights, namespace isolation, consent-label policy | Refusing unlabeled Secrets before reading contents |
| Remote cluster access scope | Issuing least-privilege kubeconfigs in spoke clusters | Rejecting unsafe kubeconfig auth/proxy mechanisms |
| Network destination control | NetworkPolicy, egress firewall, DNS policy | Rejecting obviously unsafe alert destinations; honoring allowlist config |

## Permission matrix (security contract)

Legend: ✅ allowed by profile contract; ⚠️ allowed only for approved identities/policies; ❌ disallowed by profile contract.

| Capability | `controlled` | `hardened` | `relay-only` |
|---|---|---|---|
| Create `PostgresWatch` resource | ✅ Cluster admin, team lead | ✅ Team lead in own namespace; cluster admin | ✅ Team lead in own namespace; cluster admin |
| Create/modify `DeploySource` resource | ✅ Cluster admin, team lead, app developer (team policy) | ⚠️ Team lead/app developer in own namespace only, with stricter admission | ⚠️ Team lead/app developer in own namespace only, often templated via platform workflow |
| Manager reads DSN Secret (`spec.dsnSecretRef`) | ✅ If Secret is consent-labeled | ✅ If Secret is consent-labeled and label assignment is restricted | ⚠️ Prefer platform-managed relay/credentials; direct reads only for explicitly approved Secrets |
| Configure remote cluster reference (`spec.remoteClusterSecretRef`) | ✅ Trusted team operators or cluster admin | ⚠️ Platform-approved identities only; tenant use gated by policy | ❌ Tenant-managed remote references; break-glass platform admin only |

### Which Secrets may be read

The manager may read only Secrets that satisfy **all** of the following:

1. Are reachable through manager RBAC in the hub cluster (`config/rbac/role.yaml` for hub reads).
2. Are explicitly consent-labeled: `pg-regression-radar.io/allow-postgreswatch-access=true`.
3. When remote-cluster mode is used, are reachable by the kubeconfig identity in the spoke cluster.

In short: label consent is required, RBAC scope still applies, and remote access is additionally constrained by spoke-side RBAC.

### Where the manager may connect

| Connection target | `controlled` | `hardened` | `relay-only` |
|---|---|---|---|
| Hub Kubernetes API | Required | Required | Required |
| Spoke Kubernetes API via `remoteClusterSecretRef` | Allowed | Allowed only for platform-approved references | Disallowed by default |
| PostgreSQL endpoint from DSN | Allowed per referenced Secret | Allowed per approved Secret + tighter namespace policy | Usually via approved internal path only |
| Alert endpoints (`alerting.url`) | Team-defined with built-in validation | Restricted allowlist + network egress controls | Internal relay destinations only |

## Supported scenarios and limitations

### Namespace isolation assumptions

- `PostgresWatch` and `DeploySource` are namespace-scoped resources; this model assumes teams do **not** get arbitrary write across other teams' namespaces.
- In fleet mode, default remote Secret lookup uses the same namespace name as the watch unless `remoteNamespace` is explicitly set ([Multi-Cluster: What actually happens on reconcile](multi-cluster.md#what-actually-happens-on-reconcile)).

### Multi-tenancy restrictions by profile

- `controlled`: relies mostly on trust and process; technical controls are still present but may be less restrictive operationally.
- `hardened`: requires admission/RBAC reinforcement around consent label use and remote reference ownership.
- `relay-only`: intentionally removes tenant-controlled remote references and external destination freedom.

### Scope limits of this model

This document defines the contract for CRD-driven access mediation and manager connection surfaces. It does not guarantee:

- host-level compromise resistance,
- protection from all DNS rebinding variants,
- automatic remote kubeconfig credential rotation,
- full policy examples for every admission controller product.

## Existing security controls and where they live

| Control | Purpose | Documented in | Implemented in |
|---|---|---|---|
| Secret consent label (`pg-regression-radar.io/allow-postgreswatch-access=true`) | Prevent confused-deputy Secret reads from arbitrary references | [Multi-Cluster: Secret consent label](multi-cluster.md#secret-consent-label), [Configuration Reference](configuration.md#postgreswatch-spec-fields) | `internal/controller/secret_consent.go`, enforced in `internal/controller/postgreswatch_controller.go` |
| Optional admission policy restricting consent label setters | Ensure only approved identities can opt Secrets in | [`docs/policies/kyverno-consent-label-policy.yaml`](policies/kyverno-consent-label-policy.yaml) | Cluster admission engine (example policy file in repo) |
| Kubeconfig restrictions for remote cluster refs | Block `exec` plugins, `auth-provider`, and `proxy-url` in tenant-supplied kubeconfigs | [Multi-Cluster: Kubeconfig restrictions](multi-cluster.md#kubeconfig-restrictions) | `internal/controller/remote_client.go` (`validateKubeconfigAuth`) |
| Alert destination validation and allowlist support | Reduce SSRF/exfiltration risk for alerting destinations | [Alerting: Destination validation](alerting.md#destination-validation-ssrf-hardening) | `internal/alerting` notifier construction and destination validation path |
| Webhook shared-secret authentication | Prevent unauthenticated deploy-event injection | [Deploy Sources & Webhooks: Webhook authentication](webhooks.md#webhook-authentication) | `internal/cli/ingester.go` (`X-Webhook-Token` constant-time check) |
| Manager RBAC split (hub Secrets vs spoke kubeconfig rights) | Clarify and constrain what manager SA can do in hub cluster | [Multi-Cluster: RBAC](multi-cluster.md#rbac-two-separate-concerns) | `config/rbac/role.yaml` + spoke-cluster RBAC provided by kubeconfig owner |

## See also

- [Multi-Cluster (Fleet) Mode](multi-cluster.md) — remote-cluster kubeconfig flow, trust boundaries, and known gaps.
- [Configuration Reference](configuration.md) — `PostgresWatch` and `DeploySource` fields used in this contract.
- [Alerting](alerting.md) — destination validation, allowlist behavior, and SSRF caveats.
- [Deploy Sources & Webhooks](webhooks.md) — webhook authentication and source integration surface.
- [Roadmap](roadmap.md) — where this formal model fits in the security roadmap.
