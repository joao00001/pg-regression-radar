<p align="center">
  <img src="docs/assets/brand/social-preview.png" alt="pg-regression-radar: find the deploy that broke your Postgres queries" width="100%">
</p>

<p align="center">
  <a href="https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/joao00001/pg-regression-radar/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/joao00001/pg-regression-radar/actions/workflows/codeql.yml/badge.svg"></a>
  <a href="https://github.com/joao00001/pg-regression-radar/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/joao00001/pg-regression-radar?label=release&color=blue"></a>
  <a href="https://pkg.go.dev/github.com/joao00001/pg-regression-radar"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/joao00001/pg-regression-radar.svg"></a>
  <a href="https://goreportcard.com/report/github.com/joao00001/pg-regression-radar"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/joao00001/pg-regression-radar"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-blue.svg"></a>
  <a href="https://joao00001.github.io/pg-regression-radar/support-matrix/"><img alt="PostgreSQL 16 | 17 | 18" src="https://img.shields.io/badge/postgresql-16%20%7C%2017%20%7C%2018-336791.svg?logo=postgresql&logoColor=white"></a>
</p>

<p align="center">
  <a href="https://joao00001.github.io/pg-regression-radar/">Documentation</a> ·
  <a href="https://joao00001.github.io/pg-regression-radar/installation/">Installation</a> ·
  <a href="https://joao00001.github.io/pg-regression-radar/getting-started/">Getting started</a> ·
  <a href="https://joao00001.github.io/pg-regression-radar/roadmap/">Roadmap</a>
</p>

## Overview

pg-regression-radar detects PostgreSQL query-latency regressions and attributes each one to the deploy that caused it.

It samples `pg_stat_statements`, ingests deploy events from your GitOps tooling (Argo CD, Argo Rollouts, Flux, or a generic webhook), and runs a two-stage statistical test on every query: E-divisive change-point location, confirmed by Welch's t-test. When a shift is significant, it reports the query, the deploy, the size of the change and a confidence score, and sends the result to Slack, Microsoft Teams, PagerDuty or a custom webhook.

It targets teams that run Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona and others) and ship application changes several times a day, where "which deploy caused this?" is otherwise answered by hand from dashboards.

## Features

- **Statistical detection.** Change-point location plus a significance test, not a before/after average. See [Detection Algorithm](https://joao00001.github.io/pg-regression-radar/detection-algorithm/).
- **Deploy correlation, or none.** Regressions are tied to deploy events from Argo CD, Argo Rollouts, Flux, generic webhooks and Kubernetes, or detected on a rolling schedule with no deploy at all. See [Periodic Detection](https://joao00001.github.io/pg-regression-radar/periodic-detection/).
- **Plan-diff correlation.** Captures `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` around a regression to separate a changed plan from the same plan running slower.
- **One binary, four commands.** `pg-regression-radar operator` runs everything in one process for a single cluster; `manager` is the leader-elected, CRD-driven controller for a fleet; `collector` and `ingester` run the metric scraper and the deploy-webhook receiver on their own. See [Architecture](https://joao00001.github.io/pg-regression-radar/architecture/).
- **Multi-cluster.** One manager can watch `PostgresWatch` resources whose databases live in other Kubernetes clusters. See [Multi-Cluster Mode](https://joao00001.github.io/pg-regression-radar/multi-cluster/).
- **Flexible sample source.** Scrapes `pg_stat_statements` directly, or reads from an existing OpenTelemetry/Prometheus pipeline. See [OTel/Prometheus Source](https://joao00001.github.io/pg-regression-radar/otel-prometheus-source/).
- **Audit trail.** In manager mode every regression is stored as a `PerformanceRegression` resource (`kubectl get performanceregressions -A`).
- **Optional auto-abort.** Can abort an Argo Rollouts canary when a regression crosses your confidence threshold. Disabled by default and scoped per `PostgresWatch`. See [Auto-Abort](https://joao00001.github.io/pg-regression-radar/auto-abort/).

## How it works

```mermaid
flowchart LR
    PG[("PostgreSQL<br/>pg_stat_statements")] -- scrape --> COL["Collector"]
    DS["Deploy sources<br/>ArgoCD · Argo Rollouts · Flux"] -- "webhook / watch" --> ING["Deploy Event Ingester"]
    SCHED(["Rolling schedule<br/>no deploy needed"]) -. "on a schedule" .-> CE
    COL -- samples --> CE["Correlation Engine<br/>E-divisive change-point + Welch's t-test"]
    ING -- "deploy events" --> CE
    CE -- alert --> ALERT["Alerting<br/>Slack · Teams · PagerDuty · custom"]
    CE -- "manager mode" --> CR[("PerformanceRegression<br/>status (kubectl-visible)")]
```

1. The **Collector** scrapes `pg_stat_statements` on an interval and keeps a bounded time series per `queryid`.
2. The **Deploy Event Ingester** receives webhooks from Argo CD, Argo Rollouts and Flux and stores normalised `DeployEvent` records.
3. The **Correlation Engine** runs detection for each deploy event, or on a rolling schedule.
4. **Alerting** sends the query text, latency before and after, change factor and confidence.

## Example

Full walkthrough: [Regression Scenario Example](https://joao00001.github.io/pg-regression-radar/examples/regression-scenario/).

1. `checkout-api` deploys as `v1`. A frequently run query has a baseline latency of about 3 ms.
2. Revision `v2` drops the index behind `orders.customer_id`. The same query now takes about 49 ms.
3. The change point is located, Welch's t-test confirms it is not noise, and the regression is attributed to the `v2` deploy: a 15.7x change with a p-value of 0.02 (`confidenceScore` 0.98, defined as 1 − p, not a probability).

![checkout-api query latency: flat at about 3 ms through deploy v1, then about 49 ms after deploy v2, with the detected regression annotated at 15.7x and 0.98 confidence](https://github.com/user-attachments/assets/78ef1e6c-9512-4d1a-8f60-b5dd341c1f5e)

<sub>Illustrative rendering of the numbers the scripted example produces. The project emits alerts and custom resources; it does not ship a dashboard.</sub>

## Quick start

**Want to see it work first?** The [lab](https://joao00001.github.io/pg-regression-radar/lab/) runs in any Kubernetes cluster with three `kubectl apply` commands: no database, ArgoCD or Slack of your own. It ships a bad deploy and shows the radar catching it.

Install the CLI and run it against a single cluster:

```bash
go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest

pg-regression-radar operator \
  --dsn "postgres://user:password@localhost:5432/mydb?sslmode=disable" \
  --cluster-name my-cluster \
  --slack-url https://hooks.slack.com/services/XXX/YYY/ZZZ \
  --source-type argocd
```

`--dsn` takes a full PostgreSQL connection URI (`postgres://user:password@host:port/dbname?sslmode=disable`).

From a clone, use `go run ./cmd/operator` with the same flags.

For Docker and Helm installs, the HA CRD-driven manager and every flag, see [Installation](https://joao00001.github.io/pg-regression-radar/installation/), [Getting Started](https://joao00001.github.io/pg-regression-radar/getting-started/), [Quickstart Validation](https://joao00001.github.io/pg-regression-radar/quickstart-validation/) and the [Configuration Reference](https://joao00001.github.io/pg-regression-radar/configuration/).

## Compatibility and security

- **PostgreSQL 16, 17 and 18**, including community builds, CloudNativePG and EDB Postgres Advanced/Extended Server, tested in CI. See the [Support Matrix](https://joao00001.github.io/pg-regression-radar/support-matrix/).
- **Signed releases.** Container images and the Helm chart are Cosign keyless-signed, with SLSA provenance and an SBOM. See [Supply Chain Verification](https://joao00001.github.io/pg-regression-radar/supply-chain/).
- **Documented threat model** with three security profiles (`controlled`, `hardened`, `relay-only`) that govern which Secrets may be read and where the manager may connect. See the [Security Model](https://joao00001.github.io/pg-regression-radar/security-model/).

## Documentation

Full documentation is at **[joao00001.github.io/pg-regression-radar](https://joao00001.github.io/pg-regression-radar/)**.

| Area | Pages |
|---|---|
| Get started | [Installation](https://joao00001.github.io/pg-regression-radar/installation/), [Getting Started](https://joao00001.github.io/pg-regression-radar/getting-started/), [Quickstart Validation](https://joao00001.github.io/pg-regression-radar/quickstart-validation/), [Regression Scenario](https://joao00001.github.io/pg-regression-radar/examples/regression-scenario/) |
| Architecture | [Overview](https://joao00001.github.io/pg-regression-radar/architecture/), [Detection Algorithm](https://joao00001.github.io/pg-regression-radar/detection-algorithm/), [Collector Internals](https://joao00001.github.io/pg-regression-radar/collector-internals/), [Multi-Cluster](https://joao00001.github.io/pg-regression-radar/multi-cluster/), [Security Model](https://joao00001.github.io/pg-regression-radar/security-model/), [Deployment Security](https://joao00001.github.io/pg-regression-radar/deployment-security/) |
| Reference | [Configuration](https://joao00001.github.io/pg-regression-radar/configuration/), [API](https://joao00001.github.io/pg-regression-radar/api-reference/), [API Versioning](https://joao00001.github.io/pg-regression-radar/api-versioning/), [Persistence](https://joao00001.github.io/pg-regression-radar/persistence/), [Webhooks](https://joao00001.github.io/pg-regression-radar/webhooks/), [Alerting](https://joao00001.github.io/pg-regression-radar/alerting/), [Auto-Abort](https://joao00001.github.io/pg-regression-radar/auto-abort/), [Periodic Detection](https://joao00001.github.io/pg-regression-radar/periodic-detection/), [Support Matrix](https://joao00001.github.io/pg-regression-radar/support-matrix/) |
| Project | [Testing](https://joao00001.github.io/pg-regression-radar/testing/), [CI/CD](https://joao00001.github.io/pg-regression-radar/ci-cd/), [Backporting](https://joao00001.github.io/pg-regression-radar/backporting/), [Supply Chain](https://joao00001.github.io/pg-regression-radar/supply-chain/), [Branch Protection](https://joao00001.github.io/pg-regression-radar/branch-protection/), [Roadmap](https://joao00001.github.io/pg-regression-radar/roadmap/) |

The site is built from [`docs/`](docs/) with MkDocs Material. Read [`docs/TEMPLATE.md`](docs/TEMPLATE.md) before adding a page. To preview locally:

```bash
pip install -r requirements-docs.txt
mkdocs serve   # http://127.0.0.1:8000
```

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers commit and PR conventions (Conventional Commits, DCO sign-off, squash merges) and branch naming; please also read the [Code of Conduct](CODE_OF_CONDUCT.md). Before opening a pull request:

```bash
go build ./...
go vet ./...
go test ./...
```

## License

Released under the [Apache License 2.0](LICENSE).
