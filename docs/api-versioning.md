# API Versioning & Compatibility Policy

*What `v1alpha1` means in practice, what guarantees (and non-guarantees) come with it, and how the project plans to graduate CRDs to stable versions.*

## Overview

Every CRD shipped by pg-regression-radar today — `PostgresWatch`, `DeploySource`, and `PerformanceRegression` — is at version `v1alpha1`. This page explains what that version label commits to, what it explicitly does not commit to, and what a user who stores or schemas-depends on these resources should expect across releases.

## What `v1alpha1` means

`v1alpha1` follows the Kubernetes API stability convention documented in [Kubernetes API versioning](https://kubernetes.io/docs/reference/using-api/#api-versioning):

- **May change in incompatible ways in any release, including minor and patch releases.** Fields can be renamed, removed, have their type changed, or have new required fields added without a deprecation window.
- **No guarantee that stored objects can be read after an upgrade.** If a field is removed or renamed and you have live resources that use it, you will need to re-create or migrate those resources manually.
- **Recommended only for short-lived experimentation and development clusters**, not for production workloads where schema stability is a hard requirement.

This is intentional and not a quality judgment — the CRD surface is still being shaped by real-world feedback, and `v1alpha1` preserves the freedom to make the right design choice without a migration burden.

## What is (and is not) guaranteed today

| Guarantee | Status |
|---|---|
| Kubernetes API server accepts the CRDs | ✅ guaranteed |
| `spec` fields documented in [Configuration Reference](configuration.md#postgreswatch-spec-fields) exist and behave as documented | ✅ guaranteed within a release |
| Fields and their types are stable across minor or patch releases | ❌ **not** guaranteed at `v1alpha1` |
| Stored resources survive an in-place upgrade without manual action | ❌ **not** guaranteed at `v1alpha1` |
| A `conversion webhook` exists to migrate old stored versions | ❌ not implemented at `v1alpha1` |
| Schema changes are announced in release notes | ✅ best-effort — breaking changes will be called out in the release note; check before upgrading |

## Practical guidance for users

**If you are experimenting or running in a non-production cluster:** use the CRDs as-is. Check the release notes before upgrading; any field-level breaking change will be explicitly noted there.

**If you need stability today:** pin your Helm install to a specific chart version (`helm install ... --version <chart-version>`) and do not upgrade without reading the release notes for every version in between. Because there is no conversion webhook yet, upgrading may require deleting and re-creating `PostgresWatch` and `DeploySource` resources.

**If you are writing automation or tooling against the CRD schema** (e.g. a Crossplane composition, a GitOps repo, or a controller of your own): treat the schema as volatile. Subscribe to releases on GitHub to be notified when schema changes land.

## Planned graduation path

The project intends to graduate the CRDs through the standard Kubernetes API maturity ladder:

1. **`v1alpha1` (current):** schema is being shaped. Breaking changes permitted without deprecation period.
2. **`v1beta1` (target: v0.5 or later):** schema is considered mostly stable. Breaking changes require a deprecation notice in at least one minor release and a migration note in the release. A conversion webhook will be introduced to support stored-version migration.
3. **`v1` (target: v1.0 or later):** schema is stable. No breaking changes without a full deprecation cycle (minimum two minor releases). Stored objects from any prior `v1` sub-version are guaranteed to be readable after upgrade.

There is no firm timeline for `v1beta1` or `v1`. Graduation will follow demonstrated stability of the schema in real-world use, not a fixed calendar. Updates to the graduation timeline will be tracked in [Roadmap](roadmap.md).

## How breaking changes are communicated

Before `v1beta1`, any field-level breaking change in a `v1alpha1` CRD will be:

1. Called out explicitly in the GitHub release notes under a **"⚠ Breaking changes"** heading.
2. Accompanied by a migration note describing what resources need to be updated (delete-and-recreate, field rename, etc.).
3. Reflected in an updated [Configuration Reference](configuration.md#postgreswatch-spec-fields) in the same release.

After `v1beta1`, the project additionally commits to a deprecation annotation (`deprecated: true` in the field's OpenAPI description) for at least one minor release before removal.

## See also

- [Configuration Reference](configuration.md#postgreswatch-spec-fields) — current `PostgresWatch` spec fields and their defaults.
- [Roadmap](roadmap.md) — the v1.0 milestone and what else is still open.
- [Installation](installation.md) — pinning a specific Helm chart version.
