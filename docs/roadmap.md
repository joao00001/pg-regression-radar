# Roadmap

*Where the project has been, and what's still open — including operational follow-ups that aren't code changes.*

## Overview

This page tracks both feature roadmap and known operational/robustness gaps, so "what's next" lives in one place instead of being scattered across commit messages and release notes.

## Feature roadmap

This project does not commit features to specific version numbers — a version tag marks when something shipped, not a promise of what a future one will contain. Work below is grouped by whether it has shipped, not by which release it's slated for.

**Shipped:**

- Collector + Ingester + Correlation Engine + Slack alerting + Helm chart.
- Argo Rollouts and Flux source types — see [Deploy Sources & Webhooks](webhooks.md).
- Real CRDs (`PostgresWatch`, `DeploySource`, `PerformanceRegression`) reconciled by `cmd/manager` via controller-runtime, with leader-election HA — see [Architecture Overview](architecture.md).
- Multi-cluster (fleet) support via `spec.remoteClusterSecretRef` — see below for what's still open within it.

**Planned for a future release** (no version committed):

- GitHub/GitLab PR comment on detected regression; Grafana annotation.
- An OLM bundle for OperatorHub.io.

### Multi-cluster support, in detail

`cmd/manager` reconciling many `PostgresWatch` CRs at once, each with its own isolated `Collector`/`Engine`/`Notifier` (`internal/controller/registry.go`), predates this entry and was never actually the missing piece — that's "many Postgres clusters reachable over the network from one manager," which has worked since the CRD controller shipped. The real gap was **the manager reaching a Postgres cluster whose CloudNativePG-generated DSN Secret lives in a different Kubernetes cluster than the manager itself** — until now the only way to make that work was manually copying the Secret into the manager's own cluster, a pure operational workaround with no code support.

`spec.remoteClusterSecretRef` (see [Multi-Cluster (Fleet) Mode](multi-cluster.md) and [Configuration Reference](configuration.md#postgreswatch-spec-fields)) closes that gap: a kubeconfig Secret in the hub cluster lets `PostgresWatchReconciler` build a `client.Client` for a remote cluster and read that cluster's DSN Secret through it, following the same hub-spoke pattern Cluster API / Argo CD / Open Cluster Management use. What's still open, deliberately left out of this first pass:

- **No kubeconfig rotation/expiration handling beyond evict-on-failure.** A bare static token that genuinely expires still fails DSN resolution (`status.phase: Failed`, same as any other bad DSN) — no code in the hub cluster has the authority to mint a fresh credential it didn't issue. See [Multi-Cluster (Fleet) Mode](multi-cluster.md#known-gaps-and-deliberate-scope-cuts) for what evict-on-failure does and doesn't cover.
- **The CloudNativePG `Cluster` resource itself is never read remotely** — only the generated DSN Secret. `clusterName` stays a free-text label either way.
- **Not validated against two real Kubernetes clusters.** Tests use `sigs.k8s.io/controller-runtime/pkg/client/fake` plus a syntactically valid kubeconfig pointing at an unreachable address, not an actual second `kind` cluster. The [kind + CloudNativePG e2e workflow](testing.md#e2e-kind-cloudnativepg) below validates the single-cluster case against a real cluster; extending it to a second `kind` cluster for the hub-spoke path is a natural follow-up, not yet done.

## Known robustness gaps

- **Plan-diff correlation is now wired into both alerting paths; still not validated against a real `pg_store_plans` install.** `internal/planner` can capture a query's execution plan from either `pg_store_plans` (preferred — the real, previously-executed plan) or `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` (fallback — an estimate, PostgreSQL 16+ only) and diff two captures — see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional). The standalone `operator` CLI (`--capture-plans`) has called `planner.Diff` and attached the result to its Slack notification for a while; the CRD-driven `cmd/manager` path did not — `spec.capturePlans` on `PostgresWatch` now closes that gap, so a `PerformanceRegression` custom resource's `status.planDiffSummary` is populated the same way. What's still open: (a) the `pg_store_plans` path has only been exercised against a fake `*sql.DB` in unit tests — it has not been verified against a real PostgreSQL server with `pg_store_plans` actually installed, since that's a third-party C extension this project's sandboxed CI/dev environments can't install; (b) real `auto_explain` log ingestion remains a heavier, not-yet-started alternative for clusters that don't have `pg_store_plans` available either — see the "Honest limitations" note on [Detection Algorithm](detection-algorithm.md#honest-limitations).
- **Full kind + CloudNativePG cluster validation is now done; real ArgoCD validation is not.** The [kind + CloudNativePG e2e workflow](testing.md#e2e-kind-cloudnativepg) (`.github/workflows/e2e-kind.yml`) closes the "no real Kubernetes cluster" half of this gap: a real `kind` cluster, a real CloudNativePG operator and `Cluster`, pg-regression-radar installed via the real Helm chart in `mode=manager`, and a `PostgresWatch` reading its DSN from the CloudNativePG-generated Secret directly. What's still open, deliberately left out of this pass:
  - **No real ArgoCD.** The workflow posts a `sourceType: generic` webhook directly to the DeploySource route rather than standing up ArgoCD's Application controller and Notifications engine (the part that actually emits a real `on-sync-succeeded` webhook — see [Deploy Sources & Webhooks](webhooks.md)). That's a second, mostly-orthogonal integration surface on top of everything the workflow already does; a natural follow-up once someone needs to validate ArgoCD's own webhook delivery specifically, not just the manager's handling of a webhook once it arrives.
  - **No `pg_store_plans` on the CloudNativePG instance.** It's a C extension that isn't compiled into CloudNativePG's default operand images; building and maintaining a custom operand image just for this smoke test wasn't judged worth it yet (see the `pg_store_plans` entry below, which this would be a natural companion to).
  - **Single-cluster only.** The kind e2e installs CloudNativePG and the manager in the same cluster; it doesn't exercise `spec.remoteClusterSecretRef` (see [Multi-Cluster (Fleet) Mode](multi-cluster.md) above) against a second real `kind` cluster — that gap is tracked separately, above.
  - **Authored, not yet proven green in CI.** The sandbox this workflow was written in has no Docker-in-Docker/`kind` support, so `kind create cluster` could never actually run there. The workflow's commands were checked against current upstream CloudNativePG/kind/Helm documentation and the YAML was syntax- and `actionlint`-checked, but the very first real `workflow_dispatch` run of it should be treated as part of reviewing the change that introduced it, not as an already-passing check.
- **`pg_store_plans` integration is a natural follow-up to plan-diff correlation.** `--capture-plans` (see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional)) uses `EXPLAIN (GENERIC_PLAN)`, which reflects the planner's default, parameter-independent cost estimate — not the real plan Postgres would choose for the actual (possibly skewed) parameter values a regression involved. The `pg_store_plans` extension records real, per-execution plans with real parameter values and has no PostgreSQL 16 requirement, but needs an extra extension most clusters won't already have installed. Real `auto_explain` log ingestion is a second, heavier alternative (a full log-shipping pipeline) with the same real-plan benefit. Both are deliberately out of scope for the `EXPLAIN (GENERIC_PLAN)` work above.

## Operational follow-ups

Shipped operational work (publishing signed release artifacts, per-release docs versioning, per-PR release-notes fragments) is documented where it actually lives — [CI/CD](ci-cd.md) and [Installation](installation.md) — rather than listed again here once it's done. What's below is genuinely still open:

- **Branch protection review.** See [Branch Protection](branch-protection.md) — the CI checks in [CI/CD](ci-cd.md) only have real effect once `main`'s ruleset actually requires each one by name.
- **GitHub Discussions link.** `.github/ISSUE_TEMPLATE/config.yml` links to the repo's Discussions tab; whether Discussions is actually enabled on the repo hasn't been confirmed.

## See also

- [CI/CD](ci-cd.md) — the workflows referenced by the operational follow-ups above.
- [Persistence](persistence.md) and [Collector Internals](collector-internals.md) — the two pages with the most detail on the robustness gaps above.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions, including the CloudNativePG validation gap and the not-yet-implemented `pg_store_plans` follow-up referenced above.
