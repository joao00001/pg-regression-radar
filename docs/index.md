---
hide:
  - toc
---

<div class="pgrr-home" markdown="1">

<section class="pgrr-hero" markdown="1">
<div markdown="1">

<span class="pgrr-kicker">PostgreSQL on Kubernetes</span>

# pg-regression-radar

<p class="pgrr-lead">Detects PostgreSQL query performance regressions and pinpoints which Kubernetes deployment caused them.</p>

[Get started](installation.md){ .md-button .md-button--primary }
[How it works](#how-it-works){ .md-button }

</div>
<div class="pgrr-hero-code" markdown="1">

```json title="PerformanceRegression"
{
  "name": "argocd-my-app-1234567890-q8675309",
  "namespace": "production",
  "deployEventId": "argocd-my-app-1234567890",
  "queryId": 8675309,
  "queryText": "SELECT * FROM orders WHERE user_id = $1",
  "status": "Detected",
  "confidenceScore": 0.98,
  "meanLatencyBefore": 4.2,
  "meanLatencyAfter": 13.7,
  "latencyChangeFactor": 3.26,
  "externalCauseSuspected": false,
  "planDiffSummary": "root node changed from Index Scan to Seq Scan",
  "createdAt": "2026-08-11T12:35:00Z"
}
```

<p class="pgrr-hero-cap" markdown="1">What a detection looks like. See the [API reference](api-reference.md#performanceregression).</p>

</div>
</section>

## The problem

Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona...) deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts). When query latency spikes, the first question in every post-mortem is:

<p class="pgrr-question">Which deploy caused this?</p>

Today that question is answered manually: open Grafana, look at `pg_stat_statements`, try to recall when the last deploy happened, cross-reference timestamps by eye. No open-source tool closes that loop automatically.

**pg-regression-radar does.**

## Regression capture

<div class="pgrr-radar">
<div class="pgrr-radar-bar">
<span class="pgrr-radar-status" aria-hidden="true">Live · 4 queries</span>
<button type="button" class="pgrr-radar-inject">Inject deploy</button>
</div>
<div class="pgrr-radar-stage">
<canvas class="pgrr-radar-canvas" role="img" aria-label="Animated chart of query latency on a log scale. Deploys appear as dashed markers; after one, a query steps up and is flagged as a regression."></canvas>
<div class="pgrr-radar-tip" hidden></div>
</div>
<p class="pgrr-radar-note">Simulated data. Hover the chart to pause and inspect it, or inject a deploy.</p>
<p class="pgrr-radar-live" role="status"></p>
</div>

## How it works

<div class="pgrr-diagram">
<svg viewBox="0 0 1280 390" xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="pgrr-arch-title pgrr-arch-desc">
<title id="pgrr-arch-title">pg-regression-radar architecture</title>
<desc id="pgrr-arch-desc">Postgres and deploy sources feed the Collector and the Deploy Event Ingester, which write into an in-memory time-series store read by the Correlation Engine, which fires Alerting and, in manager mode, a PerformanceRegression status. A rolling schedule can also trigger the Correlation Engine directly, independent of any deploy.</desc>
<defs>
<marker id="pgrr-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6.5" markerHeight="6.5" orient="auto-start-reverse"><path class="pgrr-arrowhead" d="M0,0 L10,5 L0,10 z"></path></marker>
<marker id="pgrr-arrow-accent" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6.5" markerHeight="6.5" orient="auto-start-reverse"><path class="pgrr-arrowhead pgrr-arrowhead--accent" d="M0,0 L10,5 L0,10 z"></path></marker>
</defs>
<!-- edges first, so the boxes sit on top of their ends -->
<path class="pgrr-edge" d="M250,138 L360,138" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-edge" d="M250,318 L360,318" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-edge" d="M590,138 C635,138 635,185 680,185" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-edge" d="M590,318 C635,318 635,245 680,245" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-edge pgrr-edge--idle" d="M810,72 L810,130" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-edge pgrr-edge--accent" d="M940,185 C985,185 985,138 1030,138" marker-end="url(#pgrr-arrow-accent)"></path>
<path class="pgrr-edge pgrr-edge--accent" d="M940,245 C985,245 985,318 1030,318" marker-end="url(#pgrr-arrow-accent)"></path>
<!-- edge labels -->
<text class="pgrr-l" x="305" y="124">scrape</text>
<text class="pgrr-l" x="305" y="304">webhook / watch</text>
<text class="pgrr-l" x="635" y="122">samples</text>
<text class="pgrr-l" x="635" y="346">deploy events</text>
<text class="pgrr-l pgrr-l--idle" x="824" y="106">on a schedule</text>
<text class="pgrr-l" x="985" y="122">alert</text>
<text class="pgrr-l" x="978" y="346">manager mode</text>
<!-- sources -->
<rect class="pgrr-node" x="20" y="90" width="230" height="96" rx="16"></rect>
<g class="pgrr-ic" transform="translate(123,104)"><ellipse cx="12" cy="5" rx="8" ry="3"></ellipse><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"></path></g>
<text class="pgrr-t" x="135" y="152">Postgres</text>
<text class="pgrr-s" x="135" y="172">pg_stat_statements</text>
<rect class="pgrr-node" x="20" y="270" width="230" height="96" rx="16"></rect>
<g class="pgrr-ic" transform="translate(123,284)"><circle cx="6" cy="5" r="2.2"></circle><circle cx="6" cy="19" r="2.2"></circle><circle cx="18" cy="9" r="2.2"></circle><path d="M6 7.2v9.6M18 11.2c0 4-6 3-11 6"></path></g>
<text class="pgrr-t" x="135" y="332">Deploy sources</text>
<text class="pgrr-s" x="135" y="352">ArgoCD, Rollouts, Flux, K8s</text>
<!-- ingestion -->
<rect class="pgrr-node" x="360" y="90" width="230" height="96" rx="16"></rect>
<g class="pgrr-ic" transform="translate(463,104)"><path d="M3 12h4l3-8 4 16 3-8h4"></path></g>
<text class="pgrr-t" x="475" y="152">Collector</text>
<text class="pgrr-s" x="475" y="172">bounded in-memory series</text>
<rect class="pgrr-node" x="360" y="270" width="230" height="96" rx="16"></rect>
<g class="pgrr-ic" transform="translate(463,284)"><path d="M12 4v11M7 11l5 5 5-5M4 20h16"></path></g>
<text class="pgrr-t" x="475" y="332">Deploy event ingester</text>
<text class="pgrr-s" x="475" y="352">normalised DeployEvent records</text>
<!-- engine and its schedule -->
<rect class="pgrr-node pgrr-node--idle" x="680" y="10" width="260" height="62" rx="16"></rect>
<text class="pgrr-t pgrr-t--idle" x="810" y="37">Rolling schedule</text>
<text class="pgrr-s pgrr-s--idle" x="810" y="57">periodic, no deploy needed</text>
<rect class="pgrr-node pgrr-node--engine" x="680" y="130" width="260" height="170" rx="20"></rect>
<g class="pgrr-ic pgrr-ic--engine" transform="translate(798,148)"><circle cx="12" cy="12" r="9"></circle><circle cx="12" cy="12" r="4.6"></circle><path d="M12 12l6.4-6.4"></path></g>
<text class="pgrr-t pgrr-t--engine" x="810" y="198">Correlation engine</text>
<text class="pgrr-s" x="810" y="224">E-divisive change point</text>
<text class="pgrr-s" x="810" y="244">+ Welch's t-test</text>
<!-- outputs -->
<rect class="pgrr-node" x="1030" y="90" width="230" height="96" rx="16"></rect>
<g class="pgrr-ic" transform="translate(1133,104)"><path d="M6 9a6 6 0 0 1 12 0c0 6 2 7 2 7H4s2-1 2-7"></path><path d="M10 20a2 2 0 0 0 4 0"></path></g>
<text class="pgrr-t" x="1145" y="152">Alerting</text>
<text class="pgrr-s" x="1145" y="172">Slack, Teams, PagerDuty, custom</text>
<rect class="pgrr-node" x="1030" y="270" width="230" height="96" rx="16"></rect>
<g class="pgrr-ic" transform="translate(1133,284)"><rect x="5" y="4" width="14" height="17" rx="2"></rect><path d="M9 12.5l2 2 4-4.2"></path></g>
<text class="pgrr-t" x="1145" y="332">Regression status</text>
<text class="pgrr-s" x="1145" y="352">PerformanceRegression CRD</text>
</svg>
</div>

<div class="pgrr-cards pgrr-cards--steps" markdown="1">

<div class="pgrr-card" markdown="1">
<span class="pgrr-num">1</span>
<span class="pgrr-card-title">Collector</span>
<p class="pgrr-card-desc" markdown="1">Scrapes `pg_stat_statements` on an interval and keeps a bounded in-memory time series per `queryid`.</p>
<p class="pgrr-card-links" markdown="1">[Architecture Overview](architecture.md) [Collector Internals](collector-internals.md)</p>
</div>

<div class="pgrr-card" markdown="1">
<span class="pgrr-num">2</span>
<span class="pgrr-card-title">Deploy Event Ingester</span>
<p class="pgrr-card-desc" markdown="1">Receives webhooks from ArgoCD, Argo Rollouts, and Flux and stores normalised `DeployEvent` records.</p>
<p class="pgrr-card-links" markdown="1">[Deploy Sources & Webhooks](webhooks.md)</p>
</div>

<div class="pgrr-card" markdown="1">
<span class="pgrr-num">3</span>
<span class="pgrr-card-title">Correlation Engine</span>
<p class="pgrr-card-desc" markdown="1">Runs a real two-stage detection for every deploy event: E-divisive change-point location, then Welch's t-test confirmation.</p>
<p class="pgrr-card-links" markdown="1">[Detection Algorithm](detection-algorithm.md)</p>
</div>

<div class="pgrr-card" markdown="1">
<span class="pgrr-num">4</span>
<span class="pgrr-card-title">Alerting</span>
<p class="pgrr-card-desc" markdown="1">Fires a Slack-compatible webhook with the query text, latency before/after, change factor, and confidence score.</p>
<p class="pgrr-card-links" markdown="1">[Alerting](alerting.md)</p>
</div>

</div>

## Where to go next

- New to the project? Start with [Installation](installation.md), then [Getting Started](getting-started.md).
- Deciding between the two ways to run it? See [Architecture Overview](architecture.md).
- Looking for a specific flag or CRD field? See [Configuration Reference](configuration.md).
- Want to contribute? See [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md) in the repo root.

<p class="pgrr-foot" markdown="1">Licensed under Apache 2.0 — see [LICENSE](https://github.com/joao00001/pg-regression-radar/blob/main/LICENSE).</p>

</div>
