# pg-regression-radar

*Detects PostgreSQL query performance regressions and pinpoints which Kubernetes deployment caused them.*

## The problem

Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona...) deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts). When query latency spikes, the first question in every post-mortem is:

> **"Which deploy caused this?"**

Today that question is answered manually: open Grafana, look at `pg_stat_statements`, try to recall when the last deploy happened, cross-reference timestamps by eye. No open-source tool closes that loop automatically.

pg-regression-radar does.

## How it works

<div class="pgrr-arch-diagram">
<style>
.pgrr-arch-diagram svg { width: 100%; height: auto; display: block; }
.pgrr-arch-diagram .pgrr-flow {
  stroke-dasharray: 5 6;
  animation: pgrr-dash 1.6s linear infinite;
}
.pgrr-arch-diagram .pgrr-flow-slow {
  animation-duration: 3.2s;
}
@keyframes pgrr-dash {
  to { stroke-dashoffset: -22; }
}
@media (prefers-reduced-motion: reduce) {
  .pgrr-arch-diagram .pgrr-flow,
  .pgrr-arch-diagram .pgrr-flow-slow {
    animation: none;
  }
}
</style>
<svg viewBox="0 0 880 460" xmlns="http://www.w3.org/2000/svg" role="img" aria-labelledby="pgrr-arch-title pgrr-arch-desc">
<title id="pgrr-arch-title">pg-regression-radar architecture</title>
<desc id="pgrr-arch-desc">Postgres and deploy sources feed the Collector and Deploy Event Ingester, which write into an in-memory time-series store read by the Correlation Engine, which fires Alerting. A rolling schedule can also trigger the Correlation Engine directly, independent of any deploy.</desc>
<defs>
<pattern id="pgrr-dots" width="18" height="18" patternUnits="userSpaceOnUse">
<circle cx="2" cy="2" r="1" fill="var(--md-default-fg-color--lightest, #cfcfcf)"></circle>
</pattern>
<marker id="pgrr-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
<path d="M0,0 L10,5 L0,10 z" fill="var(--md-default-fg-color--lighter, #6b6b6b)"></path>
</marker>
<marker id="pgrr-arrow-muted" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
<path d="M0,0 L10,5 L0,10 z" fill="var(--md-default-fg-color--lightest, #cfcfcf)"></path>
</marker>
</defs>

<!-- background texture -->
<rect x="0" y="0" width="880" height="460" fill="url(#pgrr-dots)" opacity="0.5"></rect>

<!-- boxes (drawn first so arrows/labels can overlap their borders on top) -->
<rect x="40" y="20" width="220" height="64" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5"></rect>
<text x="150" y="48" text-anchor="middle" font-size="16" font-weight="600" fill="var(--md-default-fg-color, #1a1a1a)">Postgres</text>
<text x="150" y="68" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">pg_stat_statements</text>

<rect x="620" y="20" width="220" height="64" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5"></rect>
<text x="730" y="48" text-anchor="middle" font-size="16" font-weight="600" fill="var(--md-default-fg-color, #1a1a1a)">Deploy sources</text>
<text x="730" y="68" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">ArgoCD, Rollouts, Flux, K8s</text>

<rect x="40" y="140" width="220" height="56" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5"></rect>
<text x="150" y="174" text-anchor="middle" font-size="16" font-weight="600" fill="var(--md-default-fg-color, #1a1a1a)">Collector</text>

<rect x="620" y="140" width="220" height="56" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5"></rect>
<text x="730" y="174" text-anchor="middle" font-size="16" font-weight="600" fill="var(--md-default-fg-color, #1a1a1a)">Deploy event ingester</text>

<rect x="350" y="140" width="180" height="56" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5" stroke-dasharray="4 3"></rect>
<text x="440" y="164" text-anchor="middle" font-size="15" font-weight="600" fill="var(--md-default-fg-color--lighter, #6b6b6b)">Rolling schedule</text>
<text x="440" y="182" text-anchor="middle" font-size="11" fill="var(--md-default-fg-color--lightest, #9a9a9a)">periodic, no deploy needed</text>

<rect x="90" y="380" width="220" height="56" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5"></rect>
<text x="200" y="406" text-anchor="middle" font-size="16" font-weight="600" fill="var(--md-default-fg-color, #1a1a1a)">Alerting</text>
<text x="200" y="424" text-anchor="middle" font-size="11" fill="var(--md-default-fg-color--lighter, #6b6b6b)">Slack, Teams, PagerDuty, custom</text>

<rect x="560" y="380" width="280" height="56" rx="10" fill="var(--md-default-bg-color, #fff)" stroke="var(--md-default-fg-color--lightest, #cfcfcf)" stroke-width="1.5"></rect>
<text x="700" y="406" text-anchor="middle" font-size="15" font-weight="600" fill="var(--md-default-fg-color, #1a1a1a)">PerformanceRegression status</text>
<text x="700" y="424" text-anchor="middle" font-size="11" fill="var(--md-default-fg-color--lighter, #6b6b6b)">manager mode only</text>

<rect x="290" y="250" width="300" height="80" rx="14" fill="var(--md-default-bg-color, #fff)" stroke="#7c4dff" stroke-width="2"></rect>
<text x="440" y="281" text-anchor="middle" font-size="17" font-weight="700" fill="#7c4dff">Correlation engine</text>
<text x="440" y="301" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">E-divisive change point</text>
<text x="440" y="317" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">+ Welch's t-test</text>

<!-- edges (drawn after boxes so they overlap borders, not the other way around; the two curves into the Correlation engine box stop exactly at its top border instead of overshooting inside it) -->
<path class="pgrr-flow" d="M150,84 L150,140" stroke="var(--md-default-fg-color--lighter, #6b6b6b)" stroke-width="1.5" fill="none" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-flow" d="M730,84 L730,140" stroke="var(--md-default-fg-color--lighter, #6b6b6b)" stroke-width="1.5" fill="none" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-flow" d="M180,196 C220,230 320,242 380,250" stroke="var(--md-default-fg-color--lighter, #6b6b6b)" stroke-width="1.5" fill="none" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-flow" d="M700,196 C650,230 560,242 500,250" stroke="var(--md-default-fg-color--lighter, #6b6b6b)" stroke-width="1.5" fill="none" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-flow pgrr-flow-slow" d="M440,196 L440,250" stroke="var(--md-default-fg-color--lightest, #9a9a9a)" stroke-width="1.5" stroke-dasharray="4 4" fill="none" marker-end="url(#pgrr-arrow-muted)"></path>
<path class="pgrr-flow" d="M370,330 C300,350 250,360 210,380" stroke="var(--md-default-fg-color--lighter, #6b6b6b)" stroke-width="1.5" fill="none" marker-end="url(#pgrr-arrow)"></path>
<path class="pgrr-flow" d="M510,330 C570,350 640,360 690,380" stroke="var(--md-default-fg-color--lighter, #6b6b6b)" stroke-width="1.5" fill="none" marker-end="url(#pgrr-arrow)"></path>

<!-- edge labels: background chip matches page background so it breaks the line under it, then centered text on top -->
<rect x="120" y="103" width="60" height="18" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="150" y="116" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">scrape</text>
<rect x="670" y="103" width="120" height="18" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="730" y="116" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">webhook / watch</text>
<rect x="240" y="224" width="66" height="18" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="273" y="236" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">samples</text>
<rect x="551" y="224" width="106" height="18" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="604" y="236" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">deploy events</text>
<rect x="390" y="215" width="100" height="16" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="440" y="227" text-anchor="middle" font-size="11" font-style="italic" fill="var(--md-default-fg-color--lightest, #9a9a9a)">on a schedule</text>
<rect x="256" y="346" width="46" height="18" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="279" y="359" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">alert</text>
<rect x="558" y="346" width="92" height="18" fill="var(--md-default-bg-color, #fff)"></rect>
<text x="604" y="359" text-anchor="middle" font-size="12" fill="var(--md-default-fg-color--lighter, #6b6b6b)">manager mode</text>
</svg>
</div>

1. **Collector** scrapes `pg_stat_statements` on an interval and keeps a bounded in-memory time series per `queryid`. See [Architecture Overview](architecture.md) and [Collector Internals](collector-internals.md).
2. **Deploy Event Ingester** receives webhooks from ArgoCD, Argo Rollouts, and Flux and stores normalised `DeployEvent` records. See [Deploy Sources & Webhooks](webhooks.md).
3. **Correlation Engine** runs a real two-stage detection (E-divisive change-point location, then Welch's t-test confirmation) for every deploy event. See [Detection Algorithm](detection-algorithm.md).
4. **Alerting** fires a Slack-compatible webhook with the query text, latency before/after, change factor, and confidence score.

## Where to go next

- New to the project? Start with [Installation](installation.md), then [Getting Started](getting-started.md).
- Deciding between the two ways to run it? See [Architecture Overview](architecture.md).
- Looking for a specific flag or CRD field? See [Configuration Reference](configuration.md).
- Want to contribute? See [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md) in the repo root.

Licensed under Apache 2.0 — see [LICENSE](https://github.com/joao00001/pg-regression-radar/blob/main/LICENSE).
