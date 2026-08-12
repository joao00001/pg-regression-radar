# pg-regression-radar

*Detects PostgreSQL query performance regressions and pinpoints which Kubernetes deployment caused them.*

## The problem

Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona...) deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts). When query latency spikes, the first question in every post-mortem is:

> **"Which deploy caused this?"**

Today that question is answered manually: open Grafana, look at `pg_stat_statements`, try to recall when the last deploy happened, cross-reference timestamps by eye. No open-source tool closes that loop automatically.

pg-regression-radar does.

## How it works

```
┌──────────────────────┐      ┌───────────────────────┐
│  Postgres (CNPG)     │      │  ArgoCD / Argo        │
│  pg_stat_statements  │      │  Rollouts / Flux      │
└──────────┬───────────┘      └──────────┬────────────┘
           │ scrape (30-60s)             │ webhook
           ▼                             ▼
   ┌────────────────┐            ┌───────────────────┐
   │  Collector     │            │  Deploy Event     │
   │  (Go)          │            │  Ingester (Go)    │
   └───────┬────────┘            └─────────┬─────────┘
           │                               │
           ▼                               ▼
   ┌─────────────────────────────────────────────┐
   │   In-memory time-series store               │
   │   (Prometheus metrics exposed on /metrics)  │
   └───────────────────┬─────────────────────────┘
                        ▼
              ┌────────────────────┐
              │  Correlation       │
              │  Engine (Go)       │
              │  - E-divisive      │
              │    change-point    │
              │  - Welch's t-test  │
              └─────────┬──────────┘
                        ▼
              ┌────────────────────┐
              │  Alerting          │
              │  - Slack / webhook │
              │  - CRD status      │
              └────────────────────┘
```

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
