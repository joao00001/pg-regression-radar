# Roadmap

*What's still open — planned features, known gaps, and operational follow-ups. What's already shipped lives in [CHANGELOG.md](changelog.md) and in each feature's own docs page, not repeated here.*

## Overview

This page tracks only what's *not* done yet: planned features, known robustness gaps, and operational follow-ups that aren't code changes. It is not a running history of delivered work — once an item here ships, it's removed from this page, not reworded as "done"; its details live in [CHANGELOG.md](changelog.md) (what shipped and when) and the relevant feature's own docs page (how it works today).

## Feature roadmap

This project does not commit features to specific version numbers — a version tag marks when something shipped, not a promise of what a future one will contain. What's below has no version committed yet:

- GitHub/GitLab PR comment on detected regression; Grafana annotation.
- An OLM bundle for OperatorHub.io.

## Known robustness gaps

- **Real `auto_explain` log ingestion is not yet built.** `internal/planner` captures a query's execution plan from either `pg_store_plans` (the real, previously-executed plan — see [Testing](testing.md#real-pg_store_plans-extension) for how it's validated against a real install) or `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` (an estimate, PostgreSQL 16+ only) and diffs two captures — see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional). A full `auto_explain` log-shipping pipeline remains a heavier, not-yet-started alternative for clusters that have neither `pg_store_plans` nor PostgreSQL 16 available — see the "Honest limitations" note on [Detection Algorithm](detection-algorithm.md#honest-limitations).
- **No real ArgoCD or `pg_store_plans` in the kind + CloudNativePG e2e.** The [kind + CloudNativePG e2e workflow](testing.md#e2e-kind-cloudnativepg) (`.github/workflows/e2e-kind.yml`) posts a `sourceType: generic` webhook directly to the DeploySource route rather than standing up ArgoCD's Application controller and Notifications engine (the part that actually emits a real `on-sync-succeeded` webhook — see [Deploy Sources & Webhooks](webhooks.md)); that's a second, mostly-orthogonal integration surface, a natural follow-up once someone needs to validate ArgoCD's own webhook delivery specifically. Separately, it doesn't run with `pg_store_plans` on the CloudNativePG instance: that C extension isn't compiled into CloudNativePG's default operand images, and building/maintaining a custom operand image just for this smoke test hasn't been judged worth it yet (narrower than "is `pg_store_plans` validated against a real server at all" — see [Testing](testing.md#real-pg_store_plans-extension) for that validation, which runs outside of CloudNativePG specifically).
- **Neither kind-based e2e workflow has a real GitHub Actions run yet.** `.github/workflows/e2e-kind.yml` and `.github/workflows/e2e-kind-multicluster.yml` were both authored, actionlint/YAML-checked, and validated against real clusters in a local sandbox that lacks GitHub Actions' kernel support for `kind`/Docker-in-Docker (see [Multi-Cluster: Operational considerations](multi-cluster.md#operational-considerations) for the networking workaround that local validation needed) — but neither has actually executed as a real `workflow_dispatch`/labeled-PR run on an actual GitHub Actions runner. Treat each workflow's first real run as part of reviewing the change that introduces it, not as an already-passing check.

## Operational follow-ups

Shipped operational work (publishing signed release artifacts, per-release docs versioning, per-PR release-notes fragments) is documented where it actually lives — [CI/CD](ci-cd.md) and [Installation](installation.md) — and logged in [CHANGELOG.md](changelog.md), rather than listed again here once it's done. What's below is genuinely still open:

- **Branch protection review.** See [Branch Protection](branch-protection.md) — the CI checks in [CI/CD](ci-cd.md) only have real effect once `main`'s ruleset actually requires each one by name.
- **GitHub Discussions link.** `.github/ISSUE_TEMPLATE/config.yml` links to the repo's Discussions tab; whether Discussions is actually enabled on the repo hasn't been confirmed.

## 7-step security and robustness checklist status

This repository's code/docs/workflow-addressable checklist has 6 of its 7 steps complete — security model and trust boundaries, secret-consent and policy controls, alert destination controls, Kubernetes hardening defaults, the tenant-safe remote cluster model, and supply-chain controls, in that order. Each is documented in full on its own page, not restated here: [Security Model](security-model.md), [Multi-Cluster (Fleet) Mode](multi-cluster.md), [Deployment Security](deployment-security.md), and [Supply Chain](supply-chain.md) cover how; [CHANGELOG.md](changelog.md) covers when.

Step 7 remains intentionally **operational**, and genuinely open:

- **External-user quickstart validation is reproducibly documented but not auto-provable from code alone.** The execution checklist exists in [Quickstart Validation](quickstart-validation.md), but completion still depends on a real human/operator run in an external environment.

## See also

- [CHANGELOG.md](changelog.md) — what's already shipped, release by release; this page only tracks what's still open.
- [CI/CD](ci-cd.md) — the workflows referenced by the operational follow-ups above.
- [Persistence](persistence.md) and [Collector Internals](collector-internals.md) — the two pages with the most detail on the robustness gaps above.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions, including real `kind` + CloudNativePG validation coverage (single- and two-cluster) and the remaining `pg_store_plans`-on-CloudNativePG-operand-image follow-up referenced above.
