# CI/CD

*Every GitHub Actions workflow in this repo, what it checks, and when it runs.*

## Overview

pg-regression-radar runs six workflows plus Dependabot. This page is a map of what each one does; see [Testing](testing.md) for how to reproduce the test-related ones locally, and [Branch Protection](branch-protection.md) for how (and whether) they're actually enforced as required checks on `main`.

## `ci.yml` — on every push/PR to `main`

| Job | What it proves |
|---|---|
| `Build & Test` | `go build ./...`, `go vet ./...`, `go test -race ./...` against fakes/mocks — no external services. |
| `Integration tests (real PostgreSQL)` | Runs the `integration`-tagged tests in `internal/storage/postgres`, `internal/collector`, and `internal/e2e` against a real `postgres:16` container with `pg_stat_statements` preloaded. |
| `Controller tests (real kube-apiserver)` | Runs `internal/controller`'s `TestEnvtest_*` suite against a real, locally-provisioned kube-apiserver + etcd via controller-runtime's envtest. |

## `go-quality.yml` — on every push/PR to `main`

| Job | What it proves |
|---|---|
| `golangci-lint` | Static analysis beyond `go vet` (pinned version `v2.12.2`, no repo-specific config — runs with the tool's default linter set). |
| `gofmt` | Every tracked `.go` file is `gofmt`-clean. |

## `pr-title.yml` — on PR open/edit/sync

Validates the PR title against [Conventional Commits](https://www.conventionalcommits.org/) using the exact types/scopes documented in [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#commit-messages-conventional-commits) — the title becomes the squash-merge commit message on `main`, so it's enforced the same way a commit message would be.

## `dco.yml` — on every PR to `main`

Verifies every commit in the PR carries a `Signed-off-by:` trailer (DCO), via a hand-rolled `git`/`grep` check rather than a third-party Action — DCO enforcement is simple enough that pulling in an external dependency for it isn't worth the supply-chain surface.

## `e2e-manual.yml` — manual only (`workflow_dispatch`)

The containerized, real-artifact end-to-end smoke test — see [Testing](testing.md#manual-e2e-real-container) for the full description. Deliberately not wired to push/PR: it spins up multiple long-lived containers and sleeps through real wall-clock windows, so it's opt-in rather than adding minutes to every PR.

## `e2e-kind.yml` — manual only (`workflow_dispatch`)

The real-Kubernetes end-to-end test for the CRD-driven mode (`cmd/manager`) — see [Testing](testing.md#e2e-kind-cloudnativepg) for the full description. Creates a real `kind` cluster, installs the real CloudNativePG operator and a real `Cluster`, installs pg-regression-radar via the Helm chart, and asserts a real `PerformanceRegression` CR is created. Real ArgoCD and `pg_store_plans` are deliberately out of scope for this first pass — see [Roadmap](roadmap.md). Same opt-in reasoning as `e2e-manual.yml`: this is even heavier (a full cluster plus two operators), so it's `workflow_dispatch`-only too.

## Dependabot (`.github/dependabot.yml`)

Weekly update checks for `gomod` and `github-actions` ecosystems, with `chore(deps)` / `ci(deps)` commit-message prefixes matching this project's Conventional Commits convention.

## Branch protection

None of the above has real effect unless `main`'s ruleset/branch protection actually requires each check by name — see [Branch Protection](branch-protection.md) for the exact check names and the reasoning behind what's (and isn't) required for a solo-maintainer repo.

## See also

- [Testing](testing.md) — reproducing the test-related jobs locally.
- [Branch Protection](branch-protection.md) — whether these checks actually block anything.
- [Roadmap](roadmap.md) — CI/CD items still open (branch protection review, Discussions link).
