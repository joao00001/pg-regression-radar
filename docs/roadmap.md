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
- Multi-cluster (fleet) support via `spec.remoteClusterRef` (cluster registry) with backward-compatible `spec.remoteClusterSecretRef` deprecation path — see below for what's still open within it.

**Planned for a future release** (no version committed):

- GitHub/GitLab PR comment on detected regression; Grafana annotation.
- An OLM bundle for OperatorHub.io.
- OpenTelemetry as an alternative, opt-in input to the Correlation Engine (Prometheus-backed `SampleSource` first) — see below for the phased plan and why this is additive, not a replacement for `internal/collector.Collector`.

### Multi-cluster support, in detail

`cmd/manager` reconciling many `PostgresWatch` CRs at once, each with its own isolated `Collector`/`Engine`/`Notifier` (`internal/controller/registry.go`), predates this entry and was never actually the missing piece — that's "many Postgres clusters reachable over the network from one manager," which has worked since the CRD controller shipped. The real gap was **the manager reaching a Postgres cluster whose CloudNativePG-generated DSN Secret lives in a different Kubernetes cluster than the manager itself**.

That gap is now addressed in two paths documented in [Multi-Cluster (Fleet) Mode](multi-cluster.md):

- **Preferred:** `spec.remoteClusterRef` points to an admin-managed `PostgresRadarCluster` registry entry.
- **Backward compatibility:** deprecated `spec.remoteClusterSecretRef` still works in `controlled` security profile, and is rejected in `hardened`.

Remaining deliberate scope cuts are operational/advanced hardening items:

- **No kubeconfig rotation/expiration handling beyond evict-on-failure.** A static token that genuinely expires still fails DSN resolution (`status.phase: Failed`) until external rotation updates the Secret.
- **The CloudNativePG `Cluster` resource itself is not read remotely** — only the generated DSN Secret.
- **Not yet validated against two real Kubernetes clusters in CI.** Existing e2e validates real single-cluster manager mode; two-cluster hub-spoke e2e remains open.

### OpenTelemetry as a pluggable analysis input, in detail

The OpenTelemetry Collector's `postgresqlreceiver` (opentelemetry-collector-contrib) now scrapes `pg_stat_statements` natively — per-query latency, call counts, and cache stats, exposed as a `postgresql.query.execution.time` metric plus a `db.server.top_query` log record. That means the "scrape `pg_stat_statements` and expose per-query stats" half of this project is being commoditized by the wider ecosystem. Nothing in OTel's core semantic conventions or collector-contrib, however, does deploy-correlated change-point detection (see [Detection Algorithm](detection-algorithm.md)) or `EXPLAIN` plan-diff capture (see [Detection Algorithm: Plan-diff correlation](detection-algorithm.md#plan-diff-correlation-optional)) — that pairing is this project's actual differentiated value, not the collection step.

This is a low-risk pivot because the seam already exists and requires no changes to either: `correlation.Engine` depends only on a two-method `SampleSource` interface (`SamplesInRange`, `AllQueryIDs` — `internal/correlation/engine.go`), not on `internal/collector.Collector` concretely, and deploy events reach `Engine.Analyse` as a plain `v1alpha1.DeployEvent` value regardless of which webhook `DeploySource` produced it (see [Deploy Sources & Webhooks](webhooks.md)). A new input just has to satisfy the same interfaces.

Phased plan:

1. **Prometheus-backed `SampleSource` (additive, opt-in).** `internal/telemetry/promsource` (shipped) implements `correlation.SampleSource` via PromQL range queries against whatever a fleet's OTel Collector `prometheusexporter` is already serving — see [OpenTelemetry / Prometheus Sample Source](otel-prometheus-source.md) for a real caveat found while building it: `postgresqlreceiver`'s stock `postgresql.query.execution.time` metric has no per-query attribution as of this writing (it's scoped to `db.namespace` only), so this source requires a custom recording rule or OTel pipeline to supply a per-queryid metric, rather than working out of the box against a bare OTel Collector install. Not yet wired in: it still needs to be plugged in as an alternative to `internal/collector.Collector` at the same injection point (`internal/controller/postgreswatch_controller.go`'s `startWatch`) behind a new opt-in flag/CRD field — that's Phase 1b below. Default behavior is unchanged either way — `internal/collector.Collector`'s direct `pg_stat_statements` scraping remains the zero-extra-infrastructure default and is never removed; this is for fleets that already run an OTel Collector and would rather not run a second scraper against the same database.
2. **OTel-sourced deploy events (lower priority).** Normalize an OTel span (e.g. an ArgoCD sync span) into a `v1alpha1.DeployEvent` through the same `EventStore.Add` path existing webhook `DeploySource`s already use. Deferred behind step 1 gaining real usage, since ArgoCD/Flux/GitHub/generic webhooks already cover this directly today.
3. **Export `PerformanceRegression` as an OTLP-compatible signal.** Once a regression is detected — regardless of which `SampleSource` fed it — emit it as an OTLP log record or span event too, so any OTel-native backend (Grafana, Datadog, etc.) can consume a detected regression without this project's own dashboard API. Complementary to, not a replacement for, existing Slack alerting.

Deliberately staying out of scope for all of the above: `EXPLAIN`/plan-diff capture. No OTel component or semantic convention captures execution plans as of this writing — that stays `internal/planner`'s own logic regardless of which `SampleSource` is active.

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

## 7-step security and robustness checklist status

This repository's code/docs/workflow-addressable checklist is now effectively closed for steps 1-6:

1. **Security model and trust boundaries:** documented in [Security Model](security-model.md).
2. **Secret-consent and policy controls:** implemented (`internal/controller/secret_consent.go`) with admission-policy examples in `docs/policies/`.
3. **Alert destination controls:** destination policy (`permissive`/`allowlist`/`relay-only`) implemented in code and wired through Helm values/templates/docs.
4. **Kubernetes hardening defaults:** Helm exposes pod security context, NetworkPolicy/Quota/LimitRange, and ServiceAccount token automount control.
5. **Tenant-safe remote cluster model:** registry-backed `PostgresRadarCluster` + `remoteClusterRef` path implemented; legacy `remoteClusterSecretRef` is deprecated compatibility.
6. **Supply-chain controls:** release workflow builds/scans/signs artifacts, attaches SBOM attestations, and now publishes SLSA provenance attestations.

Step 7 remains intentionally **operational**:

- **External-user quickstart validation is reproducibly documented but not auto-provable from code alone.** The execution checklist exists in [Quickstart Validation](quickstart-validation.md), but completion still depends on a real human/operator run in an external environment.

## See also

- [CI/CD](ci-cd.md) — the workflows referenced by the operational follow-ups above.
- [Persistence](persistence.md) and [Collector Internals](collector-internals.md) — the two pages with the most detail on the robustness gaps above.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions, including the CloudNativePG validation gap and the not-yet-implemented `pg_store_plans` follow-up referenced above.
- [OpenTelemetry / Prometheus Sample Source](otel-prometheus-source.md) — Phase 1a of the OpenTelemetry plan above, already shipped.
