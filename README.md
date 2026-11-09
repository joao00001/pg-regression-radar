<div align="center">

# pg-regression-radar

### The Postgres performance regression detector that tells you *which deploy* broke your query latency.

[![CI](https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml/badge.svg)](https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml)
[![CodeQL](https://github.com/joao00001/pg-regression-radar/actions/workflows/codeql.yml/badge.svg)](https://github.com/joao00001/pg-regression-radar/actions/workflows/codeql.yml)
[![Latest release](https://img.shields.io/github/v/release/joao00001/pg-regression-radar?label=release&color=blue)](https://github.com/joao00001/pg-regression-radar/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/joao00001/pg-regression-radar.svg)](https://pkg.go.dev/github.com/joao00001/pg-regression-radar)
[![Go Report Card](https://goreportcard.com/badge/github.com/joao00001/pg-regression-radar)](https://goreportcard.com/report/github.com/joao00001/pg-regression-radar)
[![Docs](https://img.shields.io/badge/docs-joao00001.github.io-blue.svg)](https://joao00001.github.io/pg-regression-radar/)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Supply chain](https://img.shields.io/badge/supply%20chain-cosign%20%2B%20SLSA%20%2B%20SBOM-success.svg)](https://joao00001.github.io/pg-regression-radar/supply-chain/)
[![PostgreSQL](https://img.shields.io/badge/postgresql-16%20%7C%2017%20%7C%2018-336791.svg?logo=postgresql&logoColor=white)](https://joao00001.github.io/pg-regression-radar/support-matrix/)

**[Full documentation →](https://joao00001.github.io/pg-regression-radar)**

</div>

---

## The problem

Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona…) deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts). When query latency spikes, the first question in every post-mortem is: **"Which deploy caused this?"**

Today that's answered manually: open Grafana, look at `pg_stat_statements`, try to recall when the last deploy happened, cross-reference timestamps by eye. **pg-regression-radar closes that loop automatically** — using a real two-stage change-point detection and statistical significance test, not a naive before/after average — and names the exact query, the exact deploy, and the confidence behind the call.

## Why pg-regression-radar

**Detection you can trust**
- **Real statistics, not an eyeballed graph.** E-divisive change-point location confirmed by Welch's t-test — see [Detection Algorithm](https://joao00001.github.io/pg-regression-radar/detection-algorithm/).
- **Deploy-correlated *and* deploy-independent.** Ties regressions to ArgoCD/Argo Rollouts/Flux/generic/Kubernetes deploy events, or runs on a rolling schedule with no deploy at all — see [Periodic Detection](https://joao00001.github.io/pg-regression-radar/periodic-detection/).
- **Plan-diff correlation.** Captures `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` around a regression to tell "planner picked a worse plan" apart from "the same plan just got slower."

**Runs the way your fleet actually looks**
- **Two deployment modes, one engine.** A zero-RBAC standalone CLI (`cmd/operator`) for a single cluster, or a CRD-driven, highly-available controller (`cmd/manager`, leader-elected) for a whole fleet — see [Architecture Overview](https://joao00001.github.io/pg-regression-radar/architecture/).
- **Multi-cluster (fleet) mode.** One manager watches any number of `PostgresWatch` CRs, including Postgres clusters living in entirely different Kubernetes clusters — see [Multi-Cluster (Fleet) Mode](https://joao00001.github.io/pg-regression-radar/multi-cluster/).
- **Plugs into what you already run.** Native `pg_stat_statements` scraping, or an OpenTelemetry/Prometheus-backed sample source for fleets that already run an OTel Collector — see [OTel/Prometheus Sample Source](https://joao00001.github.io/pg-regression-radar/otel-prometheus-source/).

**Closes the loop, safely**
- **Opt-in auto-abort.** Aborts a bad Argo Rollouts canary itself the instant a regression clears your confidence bar — disabled by default, scoped per `PostgresWatch` — see [Auto-Abort](https://joao00001.github.io/pg-regression-radar/auto-abort/).
- **Alerts wherever your team already looks.** Slack, Microsoft Teams, PagerDuty, or a custom `text/template` webhook — see [Alerting](https://joao00001.github.io/pg-regression-radar/alerting/).
- **A persistent audit trail.** `cmd/manager` mode records every regression as a `PerformanceRegression` CR (`kubectl get performanceregressions -A`), not just a one-off Slack message.

**Ships like it's meant for production**
- **Signed, attested, and verifiable.** Every release's container images and Helm chart are Cosign keyless-signed with SLSA provenance and an SBOM, traceable back to the exact source commit — see [Supply Chain Verification](https://joao00001.github.io/pg-regression-radar/supply-chain/).
- **A documented threat model**, not an afterthought — three security profiles (`controlled` / `hardened` / `relay-only`) with explicit rules for which Secrets may be read and where the manager may connect — see [Security Model](https://joao00001.github.io/pg-regression-radar/security-model/).
- **Officially supports PostgreSQL 16, 17, and 18** (community, CloudNativePG, EDB Postgres Advanced/Extended Server), verified in CI on every single PR — see [Support Matrix](https://joao00001.github.io/pg-regression-radar/support-matrix/).
- **Engineering rigor to match:** 16 CI/CD workflows gate every change — unit tests, real-Postgres integration tests across all three supported versions, real-envtest controller tests, containerized single- and multi-cluster e2e, `golangci-lint`, `gosec`, `govulncheck`, CodeQL, DCO sign-off, and a maintained-release-branch backport pipeline — see [CI/CD](https://joao00001.github.io/pg-regression-radar/ci-cd/) and [Backporting](https://joao00001.github.io/pg-regression-radar/backporting/).

## Architecture

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

1. **Collector** scrapes `pg_stat_statements` on an interval and keeps a bounded in-memory time series per `queryid`.
2. **Deploy Event Ingester** receives webhooks from ArgoCD, Argo Rollouts, and Flux and stores normalised `DeployEvent` records.
3. **Correlation Engine** runs the two-stage detection (E-divisive change-point location, then Welch's t-test confirmation) for every deploy event — or on a rolling schedule, independent of any deploy.
4. **Alerting** fires a Slack-compatible webhook with the query text, latency before/after, change factor, and confidence score.

See [Architecture Overview](https://joao00001.github.io/pg-regression-radar/architecture/) for the full package layout and the two ways to run it.

## See it in practice

A concrete, reproducible walkthrough (full script in [Regression Scenario Example](https://joao00001.github.io/pg-regression-radar/examples/regression-scenario/)):

1. `checkout-api` deploys as revision `v1`. Baseline latency for a frequently-run query: ~3ms.
2. Revision `v2` ships and silently drops the index behind `orders.customer_id`. The same query now takes ~49ms.
3. The Correlation Engine's E-divisive change-point detection locates the shift, Welch's t-test confirms it's statistically significant (not noise), and the result is tied back to the `v2` deploy event.

```text
regression detected: app=checkout-api namespace=demo revision=v2 query_id=123456
latency_before_ms=3.1 latency_after_ms=48.7 change=15.7x confidence=0.98
```

No manual Grafana digging, no cross-referencing deploy timestamps by eye — just the query, the deploy, and the confidence behind the call. The full example is scripted end-to-end, from `docker run postgres` to this exact output, in about 10 minutes.

## Quick start

Install the single unified CLI (no clone needed):

```bash
go install github.com/joao00001/pg-regression-radar/cmd/pg-regression-radar@latest

pg-regression-radar operator \
  --dsn "postgres://user:pass@localhost:5432/mydb?sslmode=disable" \
  --cluster-name my-cluster \
  --slack-url https://hooks.slack.com/services/XXX/YYY/ZZZ \
  --source-type argocd
```
> **Note:** The `--dsn` value must be a full PostgreSQL connection URI of the form
> `postgres://username:password@host:port/dbname?sslmode=disable`.
> Every DSN shown in these docs with `******` in place of the credentials is for documentation
> safety only and cannot be used as-is — replace it with your real connection string.


Or, from a clone: `go run ./cmd/operator --dsn ... --cluster-name ... --source-type argocd`.

See **[Installation](https://joao00001.github.io/pg-regression-radar/installation/)** for every install option (Go install, source, Docker, Helm), **[Getting Started](https://joao00001.github.io/pg-regression-radar/getting-started/)** for the CRD-driven (`manager`, HA) and Helm-based paths, **[Quickstart Validation](https://joao00001.github.io/pg-regression-radar/quickstart-validation/)** for an end-to-end external-user validation flow, and **[Configuration Reference](https://joao00001.github.io/pg-regression-radar/configuration/)** for every flag.

## Documentation

<details open>
<summary><strong>Get started</strong></summary>

| | |
|---|---|
| [Installation](https://joao00001.github.io/pg-regression-radar/installation/) | Obtaining the binaries, container image, or Helm chart. |
| [Getting Started](https://joao00001.github.io/pg-regression-radar/getting-started/) | Running each of the three supported forms. |
| [Quickstart Validation](https://joao00001.github.io/pg-regression-radar/quickstart-validation/) | An end-to-end validation flow for a brand-new install. |
| [Regression Scenario Example](https://joao00001.github.io/pg-regression-radar/examples/regression-scenario/) | A worked, reproducible example from deploy to detected regression. |

</details>

<details>
<summary><strong>Architecture</strong></summary>

| | |
|---|---|
| [Architecture Overview](https://joao00001.github.io/pg-regression-radar/architecture/) | The four engine packages, and the two ways to run them. |
| [Detection Algorithm](https://joao00001.github.io/pg-regression-radar/detection-algorithm/) | The E-divisive + Welch's t-test two-stage pipeline, and plan-diff correlation. |
| [Collector Internals](https://joao00001.github.io/pg-regression-radar/collector-internals/) | Retention, `queryid` rotation, and Postgres version-detection details. |
| [OTel/Prometheus Sample Source](https://joao00001.github.io/pg-regression-radar/otel-prometheus-source/) | Feeding the Correlation Engine from Prometheus instead of a direct Postgres scrape. |
| [Multi-Cluster (Fleet) Mode](https://joao00001.github.io/pg-regression-radar/multi-cluster/) | Watching Postgres clusters that live in other Kubernetes clusters. |
| [Security Model & Threat Model](https://joao00001.github.io/pg-regression-radar/security-model/) | Trust assumptions, Secrets access, and the three security profiles. |
| [Deployment Security (Helm)](https://joao00001.github.io/pg-regression-radar/deployment-security/) | NetworkPolicy, Pod Security Standards, and other optional hardening. |

</details>

<details>
<summary><strong>Reference</strong></summary>

| | |
|---|---|
| [Configuration Reference](https://joao00001.github.io/pg-regression-radar/configuration/) | Every flag and CRD field, with defaults. |
| [API Reference](https://joao00001.github.io/pg-regression-radar/api-reference/) | The JSON shape of `DeployEvent` and `PerformanceRegression`. |
| [API Versioning & Compatibility](https://joao00001.github.io/pg-regression-radar/api-versioning/) | What's covered by this project's compatibility promise. |
| [Persistence](https://joao00001.github.io/pg-regression-radar/persistence/) | The optional Postgres-backed state store. |
| [Deploy Sources & Webhooks](https://joao00001.github.io/pg-regression-radar/webhooks/) | Wiring up ArgoCD, Argo Rollouts, Flux, or a custom system. |
| [Alerting](https://joao00001.github.io/pg-regression-radar/alerting/) | Slack, Teams, PagerDuty, and custom webhook formats. |
| [Auto-Abort (Argo Rollouts)](https://joao00001.github.io/pg-regression-radar/auto-abort/) | Closing the loop between detection and aborting a bad canary. |
| [Periodic Detection](https://joao00001.github.io/pg-regression-radar/periodic-detection/) | Regression detection on a rolling schedule, with no deploy involved. |
| [Support Matrix](https://joao00001.github.io/pg-regression-radar/support-matrix/) | Officially supported PostgreSQL versions (16, 17, 18) and distributions. |

</details>

<details>
<summary><strong>Project & contributing</strong></summary>

| | |
|---|---|
| [Testing](https://joao00001.github.io/pg-regression-radar/testing/) | Unit, integration, envtest, and the containerized manual e2e workflow. |
| [CI/CD](https://joao00001.github.io/pg-regression-radar/ci-cd/) | Every workflow in this repo, what it checks, and when. |
| [Backporting](https://joao00001.github.io/pg-regression-radar/backporting/) | Shipping a merged fix to supported `release-X.Y` maintenance versions. |
| [Supply Chain Verification](https://joao00001.github.io/pg-regression-radar/supply-chain/) | Verifying a release artifact's signature and provenance yourself. |
| [Branch Protection](https://joao00001.github.io/pg-regression-radar/branch-protection/) | Which checks are required before a merge, and why. |
| [Roadmap](https://joao00001.github.io/pg-regression-radar/roadmap/) | What's planned, and known gaps. |

</details>

The docs site is built from [`docs/`](docs/) with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/) — see [`docs/TEMPLATE.md`](docs/TEMPLATE.md) before adding a page, and [`.github/workflows/docs.yml`](.github/workflows/docs.yml) for how it deploys. To preview locally:

```bash
pip install --break-system-packages -r requirements-docs.txt
mkdocs serve   # http://127.0.0.1:8000
```

## Contributing

Pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for commit/PR conventions (Conventional Commits, DCO sign-off, squash merges) and branch naming, and our [Code of Conduct](CODE_OF_CONDUCT.md). Quick local checks:

```bash
go build ./...
go vet ./...
go test ./...
```

## License

Apache 2.0 — see [LICENSE](LICENSE)
