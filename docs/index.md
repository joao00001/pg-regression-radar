---
hide:
  - navigation
  - toc
---

<div class="pgrr-home" markdown="1">

<section class="pgrr-hero" markdown="1">
<div markdown="1">

# pg-regression-radar

<p class="pgrr-lead">Detects PostgreSQL query performance regressions and pinpoints which Kubernetes deployment caused them.</p>

[Try it in your browser](playground.md){ .md-button .md-button--primary }
[Install](installation.md){ .md-button }
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

<div class="pgrr-problem" markdown="1">

<div class="pgrr-problem-lead" markdown="1">

<p class="pgrr-problem-ctx">Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona...) deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts). When query latency spikes, the first question in every post-mortem is:</p>

<p class="pgrr-question">Which <em>deploy</em> caused this?</p>

</div>

<div class="pgrr-problem-today">
<p class="pgrr-problem-label">Answered by hand, today</p>
<ul class="pgrr-manual">
<li>Open Grafana</li>
<li>Look at <code>pg_stat_statements</code></li>
<li>Try to recall when the last deploy happened</li>
<li>Cross-reference timestamps by eye</li>
</ul>
<p class="pgrr-problem-note">Few tools connect those dots for you.</p>
<p class="pgrr-problem-answer"><strong>pg-regression-radar does.</strong></p>
</div>

</div>

## <span class="pgrr-kick">Live detection</span>Watch a regression *get caught*<span class="pgrr-sub">A deploy lands, latency steps up, and the change point is attributed to it.</span> { #regression-capture .pgrr-title data-toc-label="Regression capture" }

<div class="pgrr-radar">
<div class="pgrr-radar-bar">
<span class="pgrr-radar-status" aria-hidden="true">Watching 4 queries</span>
<button type="button" class="pgrr-radar-inject">Inject deploy</button>
</div>
<div class="pgrr-radar-stage">
<canvas class="pgrr-radar-canvas" role="img" aria-label="Animated chart of query latency on a log scale. Deploys appear as dashed markers; after one, a query steps up and is flagged as a regression."></canvas>
<div class="pgrr-radar-tip" hidden></div>
</div>
<p class="pgrr-radar-note">Simulated data. Hover the chart to pause and inspect it, or inject a deploy.</p>
<p class="pgrr-radar-live" role="status"></p>
</div>

## <span class="pgrr-kick">Real run</span>The same thing, *for real*<span class="pgrr-sub">Commands on the left, the radar on the right. A deploy drops an index and the radar flags the query.</span> { #real-run .pgrr-title data-toc-label="Real run" }

<div class="pgrr-demo">
<div class="pgrr-radar-bar pgrr-demo-bar">
<span class="pgrr-radar-status pgrr-demo-status" aria-live="off">Recorded from a real run</span>
<span class="pgrr-demo-clock" aria-hidden="true"></span>
</div>
<div class="pgrr-demo-steps" aria-hidden="true"></div>
<div class="pgrr-demo-stage">
<video class="pgrr-demo-video" controls muted loop playsinline preload="metadata" tabindex="-1" disablepictureinpicture controlslist="nodownload noplaybackrate" poster="assets/demo/radar-demo-light-poster.jpg" data-dark-src="assets/demo/radar-demo-dark.mp4" data-dark-poster="assets/demo/radar-demo-dark-poster.jpg" aria-label="Two terminals recorded side by side. On the left, commands start steady query traffic, send two deploy events and drop an index. On the right, the radar logs the deploys and then reports a regression on the slowed query, about 400 times slower.">
<source src="assets/demo/radar-demo-light.mp4" type="video/mp4">
</video>
<span class="pgrr-demo-toggle" aria-hidden="true"><svg class="pgrr-demo-i pgrr-demo-i--pause" viewBox="0 0 24 24"><rect x="6.5" y="5" width="3.6" height="14" rx="1.1"/><rect x="13.9" y="5" width="3.6" height="14" rx="1.1"/></svg><svg class="pgrr-demo-i pgrr-demo-i--play" viewBox="0 0 24 24"><path d="M8 5.4v13.2a.9.9 0 0 0 1.4.8l10.2-6.6a.9.9 0 0 0 0-1.5L9.4 4.6A.9.9 0 0 0 8 5.4z"/></svg></span>
</div>
<p class="pgrr-radar-note pgrr-demo-note">Captured on a real PostgreSQL. Waiting time sped up.</p>
</div>

## <span class="pgrr-kick">Architecture</span>From scrape to *alert*<span class="pgrr-sub">Four components turn pg_stat_statements and deploy webhooks into a single answer.</span> { #how-it-works .pgrr-title data-toc-label="How it works" }

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
<rect class="pgrr-node" data-step="1" x="360" y="90" width="230" height="96" rx="16"></rect>
<text class="pgrr-n" data-node="2" data-step="1" x="380" y="117">01</text>
<g class="pgrr-ic" transform="translate(463,104)"><path d="M3 12h4l3-8 4 16 3-8h4"></path></g>
<text class="pgrr-t" x="475" y="152">Collector</text>
<text class="pgrr-s" x="475" y="172">bounded in-memory series</text>
<rect class="pgrr-node" data-step="2" x="360" y="270" width="230" height="96" rx="16"></rect>
<text class="pgrr-n" data-node="3" data-step="2" x="380" y="297">02</text>
<g class="pgrr-ic" transform="translate(463,284)"><path d="M12 4v11M7 11l5 5 5-5M4 20h16"></path></g>
<text class="pgrr-t" x="475" y="332">Deploy event ingester</text>
<text class="pgrr-s" x="475" y="352">normalised DeployEvent records</text>
<!-- engine and its schedule -->
<rect class="pgrr-node pgrr-node--idle" x="680" y="10" width="260" height="62" rx="16"></rect>
<text class="pgrr-t pgrr-t--idle" x="810" y="37">Rolling schedule</text>
<text class="pgrr-s pgrr-s--idle" x="810" y="57">periodic, no deploy needed</text>
<rect class="pgrr-node pgrr-node--engine" data-step="3" x="680" y="130" width="260" height="170" rx="20"></rect>
<text class="pgrr-n" data-node="5" data-step="3" x="702" y="159">03</text>
<g class="pgrr-ic pgrr-ic--engine" transform="translate(798,148)"><circle cx="12" cy="12" r="9"></circle><circle cx="12" cy="12" r="4.6"></circle><path d="M12 12l6.4-6.4"></path></g>
<text class="pgrr-t pgrr-t--engine" x="810" y="198">Correlation engine</text>
<text class="pgrr-s" x="810" y="224">E-divisive change point</text>
<text class="pgrr-s" x="810" y="244">+ Welch's t-test</text>
<!-- outputs -->
<rect class="pgrr-node" data-step="4" x="1030" y="90" width="230" height="96" rx="16"></rect>
<text class="pgrr-n" data-node="6" data-step="4" x="1050" y="117">04</text>
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
<span class="pgrr-num" data-stage="collect">01</span>
<span class="pgrr-card-title">Collector</span>
<p class="pgrr-card-desc" markdown="1">Scrapes `pg_stat_statements` on an interval and keeps a bounded in-memory time series per `queryid`.</p>
<p class="pgrr-card-links" markdown="1">[Architecture Overview](architecture.md) [Collector Internals](collector-internals.md)</p>
</div>

<div class="pgrr-card" markdown="1">
<span class="pgrr-num" data-stage="ingest">02</span>
<span class="pgrr-card-title">Deploy Event Ingester</span>
<p class="pgrr-card-desc" markdown="1">Receives webhooks from ArgoCD, Argo Rollouts, and Flux and stores normalised `DeployEvent` records.</p>
<p class="pgrr-card-links" markdown="1">[Deploy Sources & Webhooks](webhooks.md)</p>
</div>

<div class="pgrr-card" markdown="1">
<span class="pgrr-num" data-stage="correlate">03</span>
<span class="pgrr-card-title">Correlation Engine</span>
<p class="pgrr-card-desc" markdown="1">Runs a real two-stage detection for every deploy event: E-divisive change-point location, then Welch's t-test confirmation.</p>
<p class="pgrr-card-links" markdown="1">[Detection Algorithm](detection-algorithm.md)</p>
</div>

<div class="pgrr-card" markdown="1">
<span class="pgrr-num" data-stage="alert">04</span>
<span class="pgrr-card-title">Alerting</span>
<p class="pgrr-card-desc" markdown="1">Fires a Slack-compatible webhook with the query text, latency before/after, change factor, and confidence score.</p>
<p class="pgrr-card-links" markdown="1">[Alerting](alerting.md)</p>
</div>

</div>

## Where to go next

- Want to see it work first? Open the [playground](playground.md): no install needed.
- New to the project? Start with [Installation](installation.md), then [Getting Started](getting-started.md).
- Deciding between the two ways to run it? See [Architecture Overview](architecture.md).
- Looking for a specific flag or CRD field? See [Configuration Reference](configuration.md).
- Want to help? See [how to contribute](contribute.md): good first issues, a first pull request in four steps, and the chat.


</div>
