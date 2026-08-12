# CI/CD

*Every GitHub Actions workflow in this repo, what it checks, and when it runs.*

## Overview

pg-regression-radar runs seven workflows plus Dependabot. This page is a map of what each one does; see [Testing](testing.md) for how to reproduce the test-related ones locally, and [Branch Protection](branch-protection.md) for how (and whether) they're actually enforced as required checks on `main`.

## `ci.yml` — on every push/PR to `main`

| Job | What it proves |
|---|---|
| `Build & Test` | `go build ./...`, `go vet ./...`, `go test -race ./...` against fakes/mocks — no external services. |
| `Integration tests (real PostgreSQL)` | A `postgres-version: [16, 17, 18]` matrix — runs the `integration`-tagged tests in `internal/storage/postgres`, `internal/collector`, and `internal/e2e` against a real `postgres:16`/`postgres:17`/`postgres:18` container (one job per version) with `pg_stat_statements` preloaded. See [Support Matrix](support-matrix.md#officially-supported-postgresql-versions) for why 16 is the floor. |
| `Integration tests (EDB Postgres)` | Same integration test suite, run against EDB Postgres Advanced Server and EDB Postgres Extended Server images — but only when the `EDB_SUBSCRIPTION_TOKEN` repository secret is configured (those images live behind a private, authenticated registry), and with `continue-on-error: true` so an absent secret or a real failure here can't break CI for anyone else. See [Support Matrix](support-matrix.md#ci-coverage-for-edb-distributions) for the full rationale. |
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

## `release.yml` — on pushing a `v*` tag

Publishes a release: builds, scans, signs, and pushes every artifact this repo ships, gated on pushing a tag matching `v*` (e.g. `git tag v0.3.0 && git push origin v0.3.0`) — never on an ordinary merge to `main`, so there is no continuously-updated "edge" image, only real tagged releases. See [Installation](installation.md#option-3-pull-or-build-the-container-image) for the resulting pull/install commands.

| Job | What it does |
|---|---|
| `images` (matrix: `cli`, `operator`, `manager`, `collector`, `ingester`) | For each of the Dockerfile's five `--target` values: builds the image (loaded into the runner's local Docker daemon, not yet pushed), generates an SPDX SBOM with [`anchore/sbom-action`](https://github.com/anchore/sbom-action) (Syft under the hood), scans it with [`aquasecurity/trivy-action`](https://github.com/aquasecurity/trivy-action) and **fails the job on any CRITICAL-severity finding** (nothing is pushed if this fails), then — only once the scan passes — pushes `ghcr.io/joao00001/pg-regression-radar/<target>:<tag>` and `:latest`, signs the pushed digest keylessly with [`cosign`](https://github.com/sigstore/cosign) (GitHub Actions OIDC exchanged with Sigstore's public Fulcio CA — no stored private key), and attaches the SBOM to the image as a signed in-toto attestation (`cosign attest`). |
| `helm-chart` (needs `images`) | Packages `deploy/helm/deploylens` with `--version`/`--app-version` derived from the pushed tag (chart version strips the tag's leading `v` for SemVer compliance; `appVersion` keeps it, matching the image tags) and pushes it as an OCI artifact to `oci://ghcr.io/joao00001/charts`. Runs after `images` so a chart is never published pointing at images that failed to build/scan/publish. |

**Permissions:** `packages: write` (both jobs, to push to `ghcr.io`) and `id-token: write` (the `images` job only, for cosign's OIDC-to-Fulcio exchange). Both are satisfied entirely by the built-in `GITHUB_TOKEN` — no registry credential secret is configured or needed, since GHCR is free for public repositories and accepts `GITHUB_TOKEN` directly.

**One-time manual prerequisite:** GHCR pushes need Settings → Actions → General → Workflow permissions set to "Read and write permissions" on this repository — that setting can't be applied from workflow YAML, and without it every push in this workflow fails with 403 regardless of the `permissions:` block above. See the workflow's own top comment.

**Chart versioning:** `deploy/helm/deploylens/Chart.yaml`'s checked-in `version`/`appVersion` are development-time placeholders only — `helm package --version --app-version` overrides both unconditionally from the pushed git tag at release time, so they never need editing in lockstep with a release. See [Roadmap](roadmap.md#operational-follow-ups) for the trade-off this implies.

**Not yet proven green in CI:** like `e2e-kind.yml` before its first real `workflow_dispatch` run, this workflow's commands were checked against current upstream `docker/build-push-action`, `docker/login-action`, `aquasecurity/trivy-action`, `anchore/sbom-action`, and `sigstore/cosign-installer` documentation, and the YAML is syntax- and `actionlint`-clean, but the environment this was authored in had no Docker daemon at all (not even a local `docker build` of the five targets could be exercised) and obviously no real tag push against the real `ghcr.io` registry. Treat the first real release as part of reviewing the change that introduced this workflow — see [Roadmap](roadmap.md#operational-follow-ups).

## Dependabot (`.github/dependabot.yml`)

Weekly update checks for `gomod` and `github-actions` ecosystems, with `chore(deps)` / `ci(deps)` commit-message prefixes matching this project's Conventional Commits convention.

## Branch protection

None of the above has real effect unless `main`'s ruleset/branch protection actually requires each check by name — see [Branch Protection](branch-protection.md) for the exact check names and the reasoning behind what's (and isn't) required for a solo-maintainer repo.

## See also

- [Testing](testing.md) — reproducing the test-related jobs locally.
- [Installation](installation.md) — pulling/verifying the images and Helm chart `release.yml` publishes.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions/distributions and exactly which of them each CI job verifies.
- [Branch Protection](branch-protection.md) — whether these checks actually block anything.
- [Roadmap](roadmap.md) — CI/CD items still open (branch protection review, Discussions link, `release.yml` follow-ups).
