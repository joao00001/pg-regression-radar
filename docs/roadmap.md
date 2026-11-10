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

## Detection and attribution quality

*Open items from a code audit of `internal/correlation`, `internal/collector`, `internal/planner`, and `internal/ingester` — each is a gap in what the engine can currently claim, not a bug in what it already does. Ordered by how much they affect the trustworthiness of a reported regression, not by planned release.*

### Statistical interpretation

- **`MinExecutions` counts samples, not executions.** `Engine.computeChangeStats` compares `len(preSamples)` and `len(postSamples)` against `--min-executions`, so with a 60-second scrape ten samples may stand for ten executions or ten million. The flag name promises the second meaning and the code delivers the first. The fix is to separate a minimum number of samples per segment from a minimum number of executions behind each observation — which needs the per-interval execution count to be available on the sample.
- **`ConfidenceScore` is `1 - pValue`, which is not a probability.** A p-value of 0.001 does not mean a 99.9% chance the regression is real, and says nothing about the probability that a deploy caused it; the README's "0.98 confidence" example invites exactly that reading. Planned direction: expose the raw `pValue` (and an adjusted one, once the item below lands) on `PerformanceRegression`, document `ConfidenceScore` as a significance-derived score rather than a probability, and only later introduce a score with explicit attribution semantics. Renaming or removing the existing CRD field is a compatibility decision covered by [API Versioning](api-versioning.md), not something to do in passing.
- **The Welch's t-test is applied to segments chosen by the same data.** Stage 1 searches every candidate split for the best E-divisive change point and stage 2 then tests that split as if it had been chosen independently, which makes the p-value optimistic (post-selection inference). The stage 0 pre-filter and the `LatencyChangeThreshold` mask most of the effect on typical series, but the p-value should not be read as a calibrated 5% false-positive rate. Options, roughly in order of cost: a minimum effect-size floor alongside the p-value; empirical calibration against recorded no-regression series; a permutation test that repeats the full change-point search on each permuted series. Series with autocorrelated noise are the case most worth measuring first.
- **No multiple-comparison control across queries.** One `Analyse` or `AnalysePeriodic` pass can evaluate up to the top 500 queries, each against `PValueThreshold`, and periodic analysis repeats that on every tick. A false-discovery-rate correction (Benjamini-Hochberg is the obvious first candidate) is one option; ranking and surfacing regressions by their share of total execution time is another that also helps operators triage. Whichever is chosen, the more conservative behaviour should be a separate mode, not a silent change to what `PValueThreshold` means.

### Deploy attribution

- **Attribution is by time proximity only, and the proximity check is symmetric.** `evaluateQuery` rejects a change point only when its absolute distance from the deploy exceeds `ChangePointTolerance`, so a shift that happened *before* the event can still be attributed to it. That is sometimes right — the ArgoCD and Argo Rollouts parsers stamp `DeployEvent.Timestamp` with `time.Now()` when the webhook arrives (typically at sync completion), while the real effect can start when the first replicas roll — and sometimes wrong. [Detection Algorithm](detection-algorithm.md) currently justifies the tolerance only by effects *lagging* a deploy, which is the opposite case. Planned direction: an asymmetric window whose lead and lag depend on how the event's timestamp was obtained. The OTLP source already lets an operator choose the span's `start` or `end`; the webhook sources have no equivalent, so a lightweight first step is to record a timestamp-quality indicator (source-provided vs. receipt time) on the event rather than adding separate started/completed fields to the public `DeployEvent` type.
- **No way to say "several deploys could explain this."** `Engine.Analyse` evaluates one event at a time and every tracked query is a candidate for every event on the same watch — nothing links a `queryid` to an application, and `pg_stat_statements` does not record one. Two deploys inside one tolerance window therefore each produce their own regression for the same change point. A first step that needs no new data: when more than one event falls inside the window, mark the result `Ambiguous` and list the candidate events instead of presenting one as the cause. Further outcomes (`Unattributed` when no event is close enough, `InsufficientEvidence`) and candidate ranking by cluster, namespace, and workload can follow once that vocabulary exists.
- **No signal for "everything got slower."** Whether a regression is local to one query or shared by most tracked queries at the same moment is already computable from data the collector holds, and separates "this deploy changed this query" from "the database is saturated". Reporting the fraction of other tracked queries that shifted at the same change point is a cheap first control signal; richer workload context (CPU, connections, lock waits, I/O) is deliberately not planned until there is evidence the cheaper signal is not enough, since requiring it would raise the barrier to adoption.

### Plan-diff depth

- **`planner.Diff` compares only the root node type, total estimated cost, and snapshot source.** A root that stays an `Index Scan` while its row estimate moves by four orders of magnitude, or a join order change below an unchanged root, is invisible to it. Walking the whole plan tree — scan types, index names, join methods and order, estimated rows, and sorts — would make the hint far more useful for the "why". Actual rows and buffer counts are only worth adding for sources that record them; `EXPLAIN (GENERIC_PLAN)` never does.

### Normalising non-`pg_stat_statements` sources

- **`SampleSource` abstracts where samples come from, not what a sample means.** `internal/telemetry/promsource` already requires a per-interval mean, but nothing in the interface enforces it, and a future histogram- or percentile-based source would be indistinguishable from a mean-based one to the engine. Making the meaning of each observation explicit (interval mean, cumulative mean, histogram-derived, gauge) in the sample type — with a single normalisation step in front of the engine rather than per-source conventions — would keep the engine independent of the ingestion protocol. Percentile (p95/p99) detection needs its own statistical path and calibration; feeding percentiles through the mean-based Welch test is not an option.

### Deferred until there is real-world usage data

These are consciously not planned yet: without external users and an objective definition of "correct attribution" there is nothing to calibrate them against.

- Block-permutation or block-bootstrap significance for autocorrelated series, and adaptive baseline models.
- Histogram and tail-latency change detection.
- Using OpenTelemetry traces as extra attribution evidence (which service, endpoint, and application version was affected). Traces give context and execution paths, not a counterfactual, so they should inform an attribution result rather than prove one.
- Control-group methods (difference-in-differences against comparable queries), counterfactual baselines from history, and empirically calibrated attribution probabilities.

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
