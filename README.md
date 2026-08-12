# pg-regression-radar — Postgres Performance Regression Detector

[![CI](https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml/badge.svg)](https://github.com/joao00001/pg-regression-radar/actions/workflows/ci.yml)
[![Docs](https://img.shields.io/badge/docs-joao00001.github.io-blue.svg)](https://joao00001.github.io/pg-regression-radar/)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

> **pg-regression-radar** observes every query on your Postgres cluster and tells you, with
> statistical evidence, which specific Kubernetes deployment degraded performance —
> before it becomes an incident.

**Full documentation: https://joao00001.github.io/pg-regression-radar**

---

## The problem

Teams running Postgres on Kubernetes (CloudNativePG, Zalando, Crunchy, Percona…) deploy applications many times a day via GitOps (ArgoCD, Flux, Argo Rollouts). When query latency spikes, the first question in every post-mortem is: **"Which deploy caused this?"** Today that's answered manually — pg-regression-radar closes the loop automatically, using real change-point detection and statistical significance testing, not a naive before/after average.

## Quick start

```bash
go run ./cmd/operator \
  --dsn "postgres://user:pass@localhost:5432/mydb?sslmode=disable" \
  --cluster-name my-cluster \
  --slack-url https://hooks.slack.com/services/XXX/YYY/ZZZ \
  --source-type argocd
```

See **[Getting Started](https://joao00001.github.io/pg-regression-radar/getting-started/)** for the CRD-driven (`cmd/manager`, HA) and Helm-based install paths, and **[Configuration Reference](https://joao00001.github.io/pg-regression-radar/configuration/)** for every flag.

## Documentation

| | |
|---|---|
| [Installation](https://joao00001.github.io/pg-regression-radar/installation/) | Obtaining the binaries, container image, or Helm chart. |
| [Getting Started](https://joao00001.github.io/pg-regression-radar/getting-started/) | Running each of the three supported forms. |
| [Architecture Overview](https://joao00001.github.io/pg-regression-radar/architecture/) | The four engine packages, and the two ways to run them. |
| [Detection Algorithm](https://joao00001.github.io/pg-regression-radar/detection-algorithm/) | The E-divisive + Welch's t-test two-stage pipeline. |
| [Configuration Reference](https://joao00001.github.io/pg-regression-radar/configuration/) | Every flag and CRD field, with defaults. |
| [Persistence](https://joao00001.github.io/pg-regression-radar/persistence/) | The optional Postgres-backed state store. |
| [Deploy Sources & Webhooks](https://joao00001.github.io/pg-regression-radar/webhooks/) | Wiring up ArgoCD, Argo Rollouts, Flux, or a custom system. |
| [Testing](https://joao00001.github.io/pg-regression-radar/testing/) | Unit, integration, envtest, and the containerized manual e2e workflow. |
| [CI/CD](https://joao00001.github.io/pg-regression-radar/ci-cd/) | Every workflow in this repo, what it checks, and when. |
| [Roadmap](https://joao00001.github.io/pg-regression-radar/roadmap/) | Version plan and known gaps. |

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

Apache 2.0 — see [LICENSE](LICENSE).
