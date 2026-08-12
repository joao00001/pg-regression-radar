# Roadmap

*Where the project has been, and what's still open — including operational follow-ups that aren't code changes.*

## Overview

This page tracks both feature roadmap and known operational/robustness gaps, so "what's next" lives in one place instead of being scattered across commit messages and release notes.

## Version roadmap

- **MVP (done):** Collector + Ingester + Correlation Engine + Slack alerting + Helm chart.
- **v0.2 (done):** Argo Rollouts and Flux source types — see [Deploy Sources & Webhooks](webhooks.md).
- **v0.3 (done):** Real CRDs (`PostgresWatch`, `DeploySource`, `PerformanceRegression`) reconciled by `cmd/manager` via controller-runtime, with leader-election HA — see [Architecture Overview](architecture.md).
- **v0.4 (not started):** GitHub/GitLab PR comment on detected regression; Grafana annotation.
- **v1.0 (not started):** OLM bundle for OperatorHub.io; multi-cluster support.

## Known robustness gaps

- **No dedup of findings across a `queryid` rotation.** As documented in [Collector Internals](collector-internals.md), a query whose `queryid` rotates mid-window may be analyzed once per queryid, each pulling in the same fingerprint-merged data, producing duplicate `PerformanceRegression` results for what is really one regression.
- **In-memory state doesn't yet backfill from the persistent store on restart.** See [Persistence](persistence.md#trade-offs-and-limitations) — the Postgres-backed history is durable, but the live Collector/Ingester still start cold on every restart.
- **Full kind + CloudNativePG + ArgoCD cluster validation hasn't been done.** The [manual e2e workflow](testing.md#manual-e2e-real-container) validates the built container artifact via plain Docker (a real image, a real Postgres container, a real webhook) rather than a full Kubernetes cluster with CloudNativePG and ArgoCD actually installed — a deliberate scope choice to get a real, reliable smoke test shipped first.
- **`pg_store_plans` integration is a natural follow-up to plan-diff correlation.** `--capture-plans` (see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional)) uses `EXPLAIN (GENERIC_PLAN)`, which reflects the planner's default, parameter-independent cost estimate — not the real plan Postgres would choose for the actual (possibly skewed) parameter values a regression involved. The `pg_store_plans` extension records real, per-execution plans with real parameter values and has no PostgreSQL 16 requirement, but needs an extra extension most clusters won't already have installed. Real `auto_explain` log ingestion is a second, heavier alternative (a full log-shipping pipeline) with the same real-plan benefit. Both are deliberately out of scope for the `EXPLAIN (GENERIC_PLAN)` work above.

## Operational follow-ups

- **Publish install artifacts.** No container image is published to a registry (GHCR or otherwise) and no Helm chart repository exists yet — see [Installation](installation.md). Every install path today starts from a `git clone` or, for the Go binaries only, `go install .../cmd/operator@<tag>` against the module proxy directly.
- **Branch protection review.** See [Branch Protection](branch-protection.md) — the CI checks in [CI/CD](ci-cd.md) only have real effect once `main`'s ruleset actually requires each one by name.
- **GitHub Discussions link.** `.github/ISSUE_TEMPLATE/config.yml` links to the repo's Discussions tab; whether Discussions is actually enabled on the repo hasn't been confirmed.

## See also

- [CI/CD](ci-cd.md) — the workflows referenced by the operational follow-ups above.
- [Persistence](persistence.md) and [Collector Internals](collector-internals.md) — the two pages with the most detail on the robustness gaps above.
