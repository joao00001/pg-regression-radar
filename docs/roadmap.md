# Roadmap

*Where the project has been, and what's still open — including operational follow-ups that aren't code changes.*

## Overview

This page tracks both feature roadmap and known operational/robustness gaps, so "what's next" lives in one place instead of being scattered across commit messages and release notes.

## Version roadmap

- **MVP (done):** Collector + Ingester + Correlation Engine + Slack alerting + Helm chart.
- **v0.2 (done):** Argo Rollouts and Flux source types — see [Deploy Sources & Webhooks](webhooks.md).
- **v0.3 (done):** Real CRDs (`PostgresWatch`, `DeploySource`, `PerformanceRegression`) reconciled by `cmd/manager` via controller-runtime, with leader-election HA — see [Architecture Overview](architecture.md).
- **v0.4 (not started):** GitHub/GitLab PR comment on detected regression; Grafana annotation.
- **v1.0 (in progress):** OLM bundle for OperatorHub.io (not started); multi-cluster support — **partially done**, see below.

### Multi-cluster support, in detail

`cmd/manager` reconciling many `PostgresWatch` CRs at once, each with its own isolated `Collector`/`Engine`/`Notifier` (`internal/controller/registry.go`), predates this entry and was never actually the missing piece — that's "many Postgres clusters reachable over the network from one manager," which has worked since the CRD controller shipped in v0.3. The real gap was **the manager reaching a Postgres cluster whose CloudNativePG-generated DSN Secret lives in a different Kubernetes cluster than the manager itself** — until now the only way to make that work was manually copying the Secret into the manager's own cluster, a pure operational workaround with no code support.

`spec.remoteClusterSecretRef` (see [Multi-Cluster (Fleet) Mode](multi-cluster.md) and [Configuration Reference](configuration.md#postgreswatch-spec-fields)) closes that gap: a kubeconfig Secret in the hub cluster lets `PostgresWatchReconciler` build a `client.Client` for a remote cluster and read that cluster's DSN Secret through it, following the same hub-spoke pattern Cluster API / Argo CD / Open Cluster Management use. What's still open, deliberately left out of this first pass:

- **No kubeconfig rotation/expiration handling.** An expired token in a kubeconfig Secret just starts failing DSN resolution (`status.phase: Failed`, same as any other bad DSN) — there's no automatic refresh.
- **The remote client cache (`internal/controller/remote_client.go`) never evicts entries.** Every distinct kubeconfig seen gets a cached client that lives for the life of the manager process — fine at the fleet sizes this project targets, not fine at very large scale.
- **No remote-namespace override field.** The DSN Secret is looked up in a same-named namespace on the remote cluster; fleets whose hub/spoke namespace names don't line up 1:1 aren't supported yet.
- **The CloudNativePG `Cluster` resource itself is never read remotely** — only the generated DSN Secret. `clusterName` stays a free-text label either way.
- **Not validated against two real Kubernetes clusters.** Tests use `sigs.k8s.io/controller-runtime/pkg/client/fake` plus a syntactically valid kubeconfig pointing at an unreachable address, not an actual second `kind` cluster — the same sandboxed-CI limitation as the existing "full kind + CloudNativePG + ArgoCD cluster validation" gap below, doubled.

## Known robustness gaps

- **Real-plan correlation still isn't implemented.** `--capture-plans` now adds `EXPLAIN (GENERIC_PLAN)`-based hints (see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional)), but ingestion of real `auto_explain` output and true per-execution plan capture remain future work.
- **No dedup of findings across a `queryid` rotation.** As documented in [Collector Internals](collector-internals.md), a query whose `queryid` rotates mid-window may be analyzed once per queryid, each pulling in the same fingerprint-merged data, producing duplicate `PerformanceRegression` results for what is really one regression.
- **Full kind + CloudNativePG + ArgoCD cluster validation hasn't been done.** The [manual e2e workflow](testing.md#manual-e2e-real-container) validates the built container artifact via plain Docker (a real image, a real Postgres container, a real webhook) rather than a full Kubernetes cluster with CloudNativePG and ArgoCD actually installed — a deliberate scope choice to get a real, reliable smoke test shipped first.
- **`pg_store_plans` integration is a natural follow-up to plan-diff correlation.** `--capture-plans` (see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional)) uses `EXPLAIN (GENERIC_PLAN)`, which reflects the planner's default, parameter-independent cost estimate — not the real plan Postgres would choose for the actual (possibly skewed) parameter values a regression involved. The `pg_store_plans` extension records real, per-execution plans with real parameter values and has no PostgreSQL 16 requirement, but needs an extra extension most clusters won't already have installed. Real `auto_explain` log ingestion is a second, heavier alternative (a full log-shipping pipeline) with the same real-plan benefit. Both are deliberately out of scope for the `EXPLAIN (GENERIC_PLAN)` work above.

## Operational follow-ups

- **Publish install artifacts.** No container image is published to a registry (GHCR or otherwise) and no Helm chart repository exists yet — see [Installation](installation.md). Every install path today starts from a `git clone` or, for the Go binaries only, `go install .../cmd/operator@<tag>` against the module proxy directly.
- **Branch protection review.** See [Branch Protection](branch-protection.md) — the CI checks in [CI/CD](ci-cd.md) only have real effect once `main`'s ruleset actually requires each one by name.
- **GitHub Discussions link.** `.github/ISSUE_TEMPLATE/config.yml` links to the repo's Discussions tab; whether Discussions is actually enabled on the repo hasn't been confirmed.

## See also

- [CI/CD](ci-cd.md) — the workflows referenced by the operational follow-ups above.
- [Persistence](persistence.md) and [Collector Internals](collector-internals.md) — the two pages with the most detail on the robustness gaps above.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions, including the CloudNativePG validation gap and the not-yet-implemented `pg_store_plans` follow-up referenced above.
