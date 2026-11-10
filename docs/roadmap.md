---
hide:
  - navigation
  - toc
---

# Roadmap

*What's still open — planned features, known gaps, and operational follow-ups. What's already shipped lives in [CHANGELOG.md](changelog.md) and in each feature's own docs page, not repeated here.*

<svg class="rm-sprite" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg"><symbol id="rm-i-planned" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></symbol><symbol id="rm-i-proposed" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" stroke-dasharray="3.4 3.4"/><path d="M12 8.5v7M8.5 12h7"/></symbol><symbol id="rm-i-gap" viewBox="0 0 24 24"><path d="M12 3.5 2.8 19.5h18.4L12 3.5z"/><path d="M12 10v4.2M12 17.1v.01"/></symbol><symbol id="rm-i-followup" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M8 12h8M13 8.5l3.5 3.5-3.5 3.5"/></symbol><symbol id="rm-i-deferred" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M10 9v6M14 9v6"/></symbol></svg>

## Overview

This page tracks only what's *not* done yet: planned features, known robustness gaps, and operational follow-ups that aren't code changes. It is not a running history of delivered work — once an item here ships, it's removed from this page, not reworded as "done"; its details live in [CHANGELOG.md](changelog.md) (what shipped and when) and the relevant feature's own docs page (how it works today).

<div class="rm-glance" markdown="1">
<div class="rm-stat"><span class="rm-stat-num" data-rm-count>20</span><span class="rm-stat-label">open items below</span></div>
<div class="rm-stat rm-stat--progress"><span class="rm-stat-num">6<small> / 7</small></span><span class="rm-stat-label">security and robustness checklist steps complete</span><span class="rm-steps" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i class="rm-step-open"></i></span></div>
</div>

<details class="rm-notes" markdown="1">
<summary><span class="rm-more">How to read this page</span><span class="rm-less">Hide</span></summary>

This project does not commit features to specific version numbers — a version tag marks when something shipped, not a promise of what a future one will contain. What's below has no version committed yet:

*See the **Planned** items below.*

*Quarters on each item are estimates of when we expect to pick it up, not commitments or release dates. Items marked Unscheduled have no target.*

*Open items from a code audit of `internal/correlation`, `internal/collector`, `internal/planner`, and `internal/ingester` — each is a gap in what the engine can currently claim, not a bug in what it already does. Ordered by how much they affect the trustworthiness of a reported regression, not by planned release.*

Shipped operational work (publishing signed release artifacts, per-release docs versioning, per-PR release-notes fragments) is documented where it actually lives — [CI/CD](ci-cd.md) and [Installation](installation.md) — and logged in [CHANGELOG.md](changelog.md), rather than listed again here once it's done. What's below is genuinely still open:

*See the **Follow-up** items below.*

These are consciously not planned yet: without external users and an objective definition of "correct attribution" there is nothing to calibrate them against.

*See the **Deferred** items below.*

### Security and robustness checklist

This repository's code/docs/workflow-addressable checklist has 6 of its 7 steps complete — security model and trust boundaries, secret-consent and policy controls, alert destination controls, Kubernetes hardening defaults, the tenant-safe remote cluster model, and supply-chain controls, in that order. Each is documented in full on its own page, not restated here: [Security Model](security-model.md), [Multi-Cluster (Fleet) Mode](multi-cluster.md), [Deployment Security](deployment-security.md), and [Supply Chain](supply-chain.md) cover how; [CHANGELOG.md](changelog.md) covers when.

Step 7 remains intentionally **operational**, and genuinely open:

*See the **External-user quickstart validation** item below.*

</details>

## Key initiatives

<div class="rm-features">
<a class="rm-feature" href="#roadmap-items" data-area="Statistics"><span class="rm-kicker">Key initiative</span><span class="rm-feature-title">Trustworthy statistics</span><span class="rm-feature-desc">Make the significance the engine reports mean what it says: honest thresholds, calibrated p-values and multiple-comparison control.</span><span class="rm-fields"><span><b>Open items</b>4</span><span><b>Impact</b>High</span><span><b>Timeline</b>Q4 2026 – Q2 2027</span></span><span class="rm-cta">View items &rarr;</span></a>
<a class="rm-feature" href="#roadmap-items" data-area="Attribution"><span class="rm-kicker">Key initiative</span><span class="rm-feature-title">Honest attribution</span><span class="rm-feature-desc">Say when a deploy is the likely cause, when several could be, and when the database as a whole slowed down.</span><span class="rm-fields"><span><b>Open items</b>3</span><span><b>Impact</b>High</span><span><b>Timeline</b>Q1 2027</span></span><span class="rm-cta">View items &rarr;</span></a>
<a class="rm-feature" href="#roadmap-items" data-area="Validation"><span class="rm-kicker">Key initiative</span><span class="rm-feature-title">Proven in real environments</span><span class="rm-feature-desc">Close the gap between what CI proves and what operators will run: real-runner e2e, real ArgoCD, and an external-user quickstart.</span><span class="rm-fields"><span><b>Open items</b>3</span><span><b>Impact</b>Medium</span><span><b>Timeline</b>Q4 2026 – Q1 2027</span></span><span class="rm-cta">View items &rarr;</span></a>
</div>

## Roadmap items {: #roadmap-items }

<div class="rm-list" markdown="1">

<div class="rm-item" data-status="proposed" data-area="Statistics" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Statistics</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">Welch's t-test ignores how many calls stand behind each observation</div>

Stage 2 weighs a one-call interval like a ten-thousand-call one, while the reported window means are call-weighted.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

The reported `MeanLatencyBefore`/`MeanLatencyAfter` are total time over total calls, but the t-test compares unweighted per-interval means. A ratio-estimator test (mean = total time / total calls, with a cluster-robust variance that treats each scrape interval as a cluster) is consistent with the reported numbers and, in simulation with bursty traffic and log-normal latency, kept the 5% size while raising power for a +30% regression from 41% to 73% at 3 calls per interval. It would also make `--min-executions` act directly on the precision of the test. Per-call variance from `stddev_exec_time` is a further input that is not used yet.

</details>
</div>

<div class="rm-item" data-status="gap" data-area="Validation" data-quarter="Q4 2026" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--gap" role="img" aria-label="Known gap" title="Known gap"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-gap"/></svg></span><span class="rm-area">Validation</span><span class="rm-quarter">Q4 2026</span></div>

<div class="rm-title" markdown="span">Neither kind-based e2e workflow has a real GitHub Actions run yet</div>

Both kind-based workflows were validated locally but have never run on a real GitHub Actions runner.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

`.github/workflows/e2e-kind.yml` and `.github/workflows/e2e-kind-multicluster.yml` were both authored, actionlint/YAML-checked, and validated against real clusters in a local sandbox that lacks GitHub Actions' kernel support for `kind`/Docker-in-Docker (see [Multi-Cluster: Operational considerations](multi-cluster.md#operational-considerations) for the networking workaround that local validation needed) — but neither has actually executed as a real `workflow_dispatch`/labeled-PR run on an actual GitHub Actions runner. Treat each workflow's first real run as part of reviewing the change that introduces it, not as an already-passing check.

</details>
</div>

<div class="rm-item" data-status="followup" data-area="Operations" data-quarter="Q4 2026" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--followup" role="img" aria-label="Follow-up" title="Follow-up"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-followup"/></svg></span><span class="rm-area">Operations</span><span class="rm-quarter">Q4 2026</span></div>

<div class="rm-title" markdown="span">Branch protection review</div>

Required checks only have effect once `main`'s ruleset requires each one by name.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

See [Branch Protection](branch-protection.md) — the CI checks in [CI/CD](ci-cd.md) only have real effect once `main`'s ruleset actually requires each one by name.

</details>
</div>

<div class="rm-item" data-status="followup" data-area="Operations" data-quarter="Q4 2026" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--followup" role="img" aria-label="Follow-up" title="Follow-up"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-followup"/></svg></span><span class="rm-area">Operations</span><span class="rm-quarter">Q4 2026</span></div>

<div class="rm-title" markdown="span">GitHub Discussions link</div>

The issue template links to Discussions; whether it is enabled on the repo hasn't been confirmed.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

`.github/ISSUE_TEMPLATE/config.yml` links to the repo's Discussions tab; whether Discussions is actually enabled on the repo hasn't been confirmed.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Statistics" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Statistics</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">The Welch's t-test is applied to segments chosen by the same data</div>

The significance test runs on a split the same data picked, so its p-value is optimistic. Calibrate it before reading it as a 5% rate.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

Stage 1 searches every candidate split for the best E-divisive change point and stage 2 then tests that split as if it had been chosen independently, which makes the p-value optimistic (post-selection inference). The stage 0 pre-filter and the `LatencyChangeThreshold` mask most of the effect on typical series, but the p-value should not be read as a calibrated 5% false-positive rate: measured with the project's own E-divisive and Welch code on no-change series, 35-39% had p < 0.05 with independent noise and 60-99% with autocorrelated noise, and through the full engine up to 8% of no-change analyses were reported `Detected` with autocorrelated, heavy-tailed noise. Options, roughly in order of cost: a minimum effect-size floor alongside the p-value; empirical calibration against recorded no-regression series; a permutation test that repeats the full change-point search on each permuted series. Series with autocorrelated noise are the case most worth measuring first.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Attribution" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Attribution</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">Attribution is by time proximity only, and the proximity check is symmetric</div>

A shift before the deploy can still be blamed on it. Make the window asymmetric, depending on how the event was timestamped.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

`evaluateQuery` rejects a change point only when its absolute distance from the deploy exceeds `ChangePointTolerance`, so a shift that happened *before* the event can still be attributed to it. That is sometimes right — the ArgoCD and Argo Rollouts parsers stamp `DeployEvent.Timestamp` with `time.Now()` when the webhook arrives (typically at sync completion), while the real effect can start when the first replicas roll — and sometimes wrong. [Detection Algorithm](detection-algorithm.md) currently justifies the tolerance only by effects *lagging* a deploy, which is the opposite case. Planned direction: an asymmetric window whose lead and lag depend on how the event's timestamp was obtained. The OTLP source already lets an operator choose the span's `start` or `end`; the webhook sources have no equivalent, so a lightweight first step is to record a timestamp-quality indicator (source-provided vs. receipt time) on the event rather than adding separate started/completed fields to the public `DeployEvent` type.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Attribution" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Attribution</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">No way to say "several deploys could explain this."</div>

When several deploys could explain a change, say so with an Ambiguous result instead of presenting one as the cause.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

`Engine.Analyse` evaluates one event at a time and every tracked query is a candidate for every event on the same watch — nothing links a `queryid` to an application, and `pg_stat_statements` does not record one. Two deploys inside one tolerance window therefore each produce their own regression for the same change point. A first step that needs no new data: when more than one event falls inside the window, mark the result `Ambiguous` and list the candidate events instead of presenting one as the cause. Further outcomes (`Unattributed` when no event is close enough, `InsufficientEvidence`) and candidate ranking by cluster, namespace, and workload can follow once that vocabulary exists.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Attribution" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Attribution</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">No signal for "everything got slower."</div>

Report how many other queries shifted at the same moment, to tell a deploy-local change from a saturated database.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

Whether a regression is local to one query or shared by most tracked queries at the same moment is already computable from data the collector holds, and separates "this deploy changed this query" from "the database is saturated". Reporting the fraction of other tracked queries that shifted at the same change point is a cheap first control signal; richer workload context (CPU, connections, lock waits, I/O) is deliberately not planned until there is evidence the cheaper signal is not enough, since requiring it would raise the barrier to adoption.

</details>
</div>

<div class="rm-item" data-status="gap" data-area="Validation" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--gap" role="img" aria-label="Known gap" title="Known gap"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-gap"/></svg></span><span class="rm-area">Validation</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">No real ArgoCD or `pg_store_plans` in the kind + CloudNativePG e2e</div>

The CloudNativePG e2e posts a generic webhook instead of a real ArgoCD one, and runs without `pg_store_plans`.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

The [kind + CloudNativePG e2e workflow](testing.md#e2e-kind-cloudnativepg) (`.github/workflows/e2e-kind.yml`) posts a `sourceType: generic` webhook directly to the DeploySource route rather than standing up ArgoCD's Application controller and Notifications engine (the part that actually emits a real `on-sync-succeeded` webhook — see [Deploy Sources & Webhooks](webhooks.md)); that's a second, mostly-orthogonal integration surface, a natural follow-up once someone needs to validate ArgoCD's own webhook delivery specifically. Separately, it doesn't run with `pg_store_plans` on the CloudNativePG instance: that C extension isn't compiled into CloudNativePG's default operand images, and building/maintaining a custom operand image just for this smoke test hasn't been judged worth it yet (narrower than "is `pg_store_plans` validated against a real server at all" — see [Testing](testing.md#real-pg_store_plans-extension) for that validation, which runs outside of CloudNativePG specifically).

</details>
</div>

<div class="rm-item" data-status="planned" data-area="Integrations" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--planned" role="img" aria-label="Planned" title="Planned"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-planned"/></svg></span><span class="rm-area">Integrations</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">PR comments and Grafana annotations</div>

GitHub/GitLab PR comment on detected regression; Grafana annotation.
{ .rm-summary }
</div>

<div class="rm-item" data-status="followup" data-area="Validation" data-quarter="Q1 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--followup" role="img" aria-label="Follow-up" title="Follow-up"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-followup"/></svg></span><span class="rm-area">Validation</span><span class="rm-quarter">Q1 2027</span></div>

<div class="rm-title" markdown="span">External-user quickstart validation is reproducibly documented but not auto-provable from code alone</div>

Step 7 of the checklist needs a real operator run in an external environment, which code alone can't prove.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

The execution checklist exists in [Quickstart Validation](quickstart-validation.md), but completion still depends on a real human/operator run in an external environment.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Statistics" data-quarter="Q2 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Statistics</span><span class="rm-quarter">Q2 2027</span></div>

<div class="rm-title" markdown="span">No multiple-comparison control across queries</div>

Up to 500 queries are tested per pass with no correction. Options: false-discovery-rate control, or ranking by impact.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

One `Analyse` or `AnalysePeriodic` pass can evaluate up to the top 500 queries, each against `PValueThreshold`, and periodic analysis repeats that on every tick. A false-discovery-rate correction (Benjamini-Hochberg is the obvious first candidate) is one option; ranking and surfacing regressions by their share of total execution time is another that also helps operators triage. Whichever is chosen, the more conservative behaviour should be a separate mode, not a silent change to what `PValueThreshold` means.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Plan-diff" data-quarter="Q2 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Plan-diff</span><span class="rm-quarter">Q2 2027</span></div>

<div class="rm-title" markdown="span">`planner.Diff` compares only the root node type, total estimated cost, and snapshot source</div>

The diff only compares the root node, total cost and source. Walking the whole plan tree would explain the “why”.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

A root that stays an `Index Scan` while its row estimate moves by four orders of magnitude, or a join order change below an unchanged root, is invisible to it. Walking the whole plan tree — scan types, index names, join methods and order, estimated rows, and sorts — would make the hint far more useful for the "why". Actual rows and buffer counts are only worth adding for sources that record them; `EXPLAIN (GENERIC_PLAN)` never does.

</details>
</div>

<div class="rm-item" data-status="proposed" data-area="Sources" data-quarter="Q2 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--proposed" role="img" aria-label="Proposed" title="Proposed"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-proposed"/></svg></span><span class="rm-area">Sources</span><span class="rm-quarter">Q2 2027</span></div>

<div class="rm-title" markdown="span">`SampleSource` abstracts where samples come from, not what a sample means</div>

Make explicit what each observation means, so new sources can't be mixed into the same statistical test by accident.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

`internal/telemetry/promsource` already requires a per-interval mean, but nothing in the interface enforces it, and a future histogram- or percentile-based source would be indistinguishable from a mean-based one to the engine. Making the meaning of each observation explicit (interval mean, cumulative mean, histogram-derived, gauge) in the sample type — with a single normalisation step in front of the engine rather than per-source conventions — would keep the engine independent of the ingestion protocol. Percentile (p95/p99) detection needs its own statistical path and calibration; feeding percentiles through the mean-based Welch test is not an option.

</details>
</div>

<div class="rm-item" data-status="planned" data-area="Integrations" data-quarter="Q2 2027" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--planned" role="img" aria-label="Planned" title="Planned"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-planned"/></svg></span><span class="rm-area">Integrations</span><span class="rm-quarter">Q2 2027</span></div>

<div class="rm-title" markdown="span">OLM bundle for OperatorHub.io</div>

An OLM bundle for OperatorHub.io.
{ .rm-summary }
</div>

<div class="rm-item" data-status="gap" data-area="Plan-diff" data-quarter="Unscheduled" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--gap" role="img" aria-label="Known gap" title="Known gap"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-gap"/></svg></span><span class="rm-area">Plan-diff</span><span class="rm-quarter">Unscheduled</span></div>

<div class="rm-title" markdown="span">Real `auto_explain` log ingestion is not yet built</div>

A log-shipping pipeline for clusters that have neither `pg_store_plans` nor PostgreSQL 16.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

`internal/planner` captures a query's execution plan from either `pg_store_plans` (the real, previously-executed plan — see [Testing](testing.md#real-pg_store_plans-extension) for how it's validated against a real install) or `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` (an estimate, PostgreSQL 16+ only) and diffs two captures — see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional). A full `auto_explain` log-shipping pipeline remains a heavier, not-yet-started alternative for clusters that have neither `pg_store_plans` nor PostgreSQL 16 available — see the "Honest limitations" note on [Detection Algorithm](detection-algorithm.md#honest-limitations).

</details>
</div>

<div class="rm-item" data-status="deferred" data-area="Statistics" data-quarter="Unscheduled" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--deferred" role="img" aria-label="Deferred" title="Deferred"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-deferred"/></svg></span><span class="rm-area">Statistics</span><span class="rm-quarter">Unscheduled</span></div>

<div class="rm-title" markdown="span">Block bootstrap and adaptive baselines</div>

Better significance for autocorrelated series, and baselines that adapt over time.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

Block-permutation or block-bootstrap significance for autocorrelated series, and adaptive baseline models.

</details>
</div>

<div class="rm-item" data-status="deferred" data-area="Statistics" data-quarter="Unscheduled" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--deferred" role="img" aria-label="Deferred" title="Deferred"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-deferred"/></svg></span><span class="rm-area">Statistics</span><span class="rm-quarter">Unscheduled</span></div>

<div class="rm-title" markdown="span">Histogram and tail-latency detection</div>

Detect regressions in the tail of the distribution, not only in the mean.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

Histogram and tail-latency change detection.

</details>
</div>

<div class="rm-item" data-status="deferred" data-area="Attribution" data-quarter="Unscheduled" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--deferred" role="img" aria-label="Deferred" title="Deferred"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-deferred"/></svg></span><span class="rm-area">Attribution</span><span class="rm-quarter">Unscheduled</span></div>

<div class="rm-title" markdown="span">Traces as attribution evidence</div>

Traces add context and execution paths to an attribution; they don't prove one.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

Using OpenTelemetry traces as extra attribution evidence (which service, endpoint, and application version was affected). Traces give context and execution paths, not a counterfactual, so they should inform an attribution result rather than prove one.

</details>
</div>

<div class="rm-item" data-status="deferred" data-area="Attribution" data-quarter="Unscheduled" markdown="1">
<div class="rm-meta"><span class="rm-status rm-status--deferred" role="img" aria-label="Deferred" title="Deferred"><svg class="rm-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#rm-i-deferred"/></svg></span><span class="rm-area">Attribution</span><span class="rm-quarter">Unscheduled</span></div>

<div class="rm-title" markdown="span">Control groups and causal methods</div>

Compare against control queries and history to estimate calibrated attribution probabilities.
{ .rm-summary }

<details markdown="1">
<summary><span class="rm-more">Read more</span><span class="rm-less">Show less</span></summary>

Control-group methods (difference-in-differences against comparable queries), counterfactual baselines from history, and empirically calibrated attribution probabilities.

</details>
</div>

</div>

**Have an idea?** [Open a feature request](https://github.com/joao00001/pg-regression-radar/issues/new/choose) — dates are not promised, but this page is where accepted ideas land.

## See also

- [CHANGELOG.md](changelog.md) — what's already shipped, release by release; this page only tracks what's still open.
- [CI/CD](ci-cd.md) — the workflows referenced by the operational follow-ups above.
- [Persistence](persistence.md) and [Collector Internals](collector-internals.md) — the two pages with the most detail on the robustness gaps above.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions and distributions, including real `kind` + CloudNativePG validation coverage (single- and two-cluster) and the remaining `pg_store_plans`-on-CloudNativePG-operand-image follow-up referenced above.
