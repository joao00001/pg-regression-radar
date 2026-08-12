# CI/CD

*Every GitHub Actions workflow in this repo, what it checks, and when it runs.*

## Overview

pg-regression-radar runs nine workflows plus Dependabot. This page is a map of what each one does; see [Testing](testing.md) for how to reproduce the test-related ones locally, and [Branch Protection](branch-protection.md) for how (and whether) they're actually enforced as required checks on `main`.

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

## `changelog-fragment-check.yml` — on PR open/edit/sync

Enforces the "changeset"/news-fragment convention described in [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#release-notes--changeset-fragments): any PR whose title's Conventional Commits type is `feat`, `fix`, or `perf` must add exactly one file to `changelog.d/` named `<PR-number>.<type>.md`, and that file's content must pass seven format rules (single paragraph, capitalized, ends in `.`/`!`/`?`, no heading, no code block, doesn't repeat the type prefix, 20-400 characters). PRs of any other type (`docs`, `test`, `refactor`, `chore`, `ci`) are exempt — they don't describe anything an end user would see in release notes.

| Step | What it does |
|---|---|
| Determine PR type | Parses the PR title with the same type vocabulary `pr-title.yml`'s `types:` list uses (kept manually in sync — see the comment in both files). |
| Gate | Only `feat`/`fix`/`perf` require a fragment; anything else short-circuits with a no-op pass. |
| Find the fragment | Diffs the PR against its base ref for added files under `changelog.d/`, and fails with a specific message if none, more than one, or a wrongly-named file was added. |
| Validate content | Runs `scripts/check_changelog_fragment.py <file>` — a small stdlib-only Python script (no `towncrier` dependency needed for this part) that checks each of the seven content rules independently and fails with the exact rule number and a human-readable reason (e.g. `[3-ends-with-punctuation]`), plus a link back to CONTRIBUTING.md. |

This is a separate workflow from `pr-title.yml` (rather than a job appended to it) because `pr-title.yml` runs on `pull_request_target` (needed so it can comment on forked PRs without leaking secrets), while this check only ever needs to read the PR's own diff and is safer as plain `pull_request`.

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
| `release-notes` (needs `images`, `helm-chart`) | Extracts the section of `CHANGELOG.md` matching the pushed tag (from its `## vX.Y.Z - <date>` heading up to, but not including, the next `## ` heading, via a small `awk` script) and publishes it as the body of a GitHub Release via [`softprops/action-gh-release`](https://github.com/softprops/action-gh-release). That section only exists because the repo owner ran `towncrier build --version vX.Y.Z --yes` (see [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#release-notes--changeset-fragments)) and committed the result *before* pushing the tag — this job fails loudly (rather than publishing an empty release) if no matching section is found. Runs last, after both artifact-publishing jobs, so a release is never announced before its images/chart actually exist. |

**Release notes are `CHANGELOG.md`-derived, not generated from commit history in this workflow.** An earlier draft of this workflow considered generating release notes directly from commit messages via [`git-cliff`](https://git-cliff.org/) (with a checked-in `cliff.toml`), but that never actually landed on `main`, and the `changelog.d/` fragment system above supersedes it: fragments are written by a human in user-facing language and enforced per-PR in CI (`changelog-fragment-check.yml`), which a commit-message-driven changelog can't guarantee. There is no `git-cliff` usage or `cliff.toml` anywhere in this repo.

**Permissions:** `packages: write` (`images` and `helm-chart`, to push to `ghcr.io`), `id-token: write` (`images` only, for cosign's OIDC-to-Fulcio exchange), and `contents: write` (`release-notes` only, for `softprops/action-gh-release` to create the Release). All three are satisfied entirely by the built-in `GITHUB_TOKEN` — no registry credential secret is configured or needed, since GHCR is free for public repositories and accepts `GITHUB_TOKEN` directly.

**One-time manual prerequisite:** GHCR pushes need Settings → Actions → General → Workflow permissions set to "Read and write permissions" on this repository — that setting can't be applied from workflow YAML, and without it every push in this workflow fails with 403 regardless of the `permissions:` block above. See the workflow's own top comment.

**Chart versioning:** `deploy/helm/deploylens/Chart.yaml`'s checked-in `version`/`appVersion` are development-time placeholders only — `helm package --version --app-version` overrides both unconditionally from the pushed git tag at release time, so they never need editing in lockstep with a release. See [Roadmap](roadmap.md#operational-follow-ups) for the trade-off this implies.

**Not yet proven green in CI:** like `e2e-kind.yml` before its first real `workflow_dispatch` run, this workflow's commands were checked against current upstream `docker/build-push-action`, `docker/login-action`, `aquasecurity/trivy-action`, `anchore/sbom-action`, `sigstore/cosign-installer`, and `softprops/action-gh-release` documentation, and the YAML is syntax- and `actionlint`-clean, but the environment this was authored in had no Docker daemon at all (not even a local `docker build` of the five targets could be exercised) and obviously no real tag push against the real `ghcr.io` registry or a real GitHub Release creation. Treat the first real release as part of reviewing the change that introduced this workflow — see [Roadmap](roadmap.md#operational-follow-ups).

## `docs.yml` — two independent jobs: `check` (every PR/push) and `deploy` (only on a release tag)

Builds this documentation site with MkDocs Material and publishes a **permanently versioned** copy of it to GitHub Pages at <https://joao00001.github.io/pg-regression-radar/> using [mike](https://github.com/jimporter/mike) — every tag gets its own browsable, never-overwritten copy of the site, a version selector renders in the header (Material's native mike integration, see `mkdocs.yml`'s `extra.version` block), and a `latest` alias always points at the newest *stable* release.

This used to be one job that redeployed the live site on every push to `main` touching `docs/**`. That meant the public site could describe a `feat` that had merged but not shipped in any tagged image yet — nothing enforced "the docs you're reading match something you can actually install." It's now split into two jobs specifically to close that gap:

| Job | Trigger | Publishes? |
|---|---|---|
| `check` | Push or PR to `main` (PRs path-filtered to `docs/**`/`mkdocs.yml`/`hooks.py`/`requirements-docs.txt`; plain pushes to `main` unconditionally, matching `ci.yml`'s own pattern) | No — `mkdocs build --strict` only, same broken-link/nav gate as before, just never publishes anything. |
| `deploy` | Push of a `v*` tag, or `workflow_dispatch` with an explicit `tag` input (for re-running a failed deploy — it checks out that exact tag, never whatever ref the workflow happens to run from) | Yes — via `mike deploy`/`mike set-default`, straight to the `gh-pages` branch. |

`deploy` runs unconditionally on every tag push, with no path filter (mirroring `release.yml`'s own tag-triggered, always-runs behavior) — a release always gets a matching docs deploy, even when that specific tag's commit didn't touch anything under `docs/`.

**Stable vs. pre-release tags:** a tag containing a `-` (this repo's existing release-candidate convention, e.g. `v1.0.0-rc1`) is deployed as its own permanent, linkable version but never becomes the `latest` alias — only a tag with no `-` (a real `vX.Y.Z` release) moves `latest`, and with it, what a visitor sees by default at the site's root.

**One-time manual prerequisite:** Settings → Pages → Source must be "Deploy from a branch", branch `gh-pages`, folder `/ (root)` — **not** "GitHub Actions". mike commits the built site directly to the `gh-pages` branch itself (see [mike's own "How It Works"](https://github.com/jimporter/mike#how-it-works)); there's no artifact-upload step here for the old "GitHub Actions" Pages source to serve.

**Changelog, on the site itself:** `docs/changelog.md` isn't a real file in the repo — `hooks.py`'s `on_pre_build` hook copies the root `CHANGELOG.md` into it at build time (rewriting the two relative links that would otherwise break a directory level deeper: `CONTRIBUTING.md` → an absolute GitHub blob URL, `docs/roadmap.md` → `roadmap.md`) and it's listed in `mkdocs.yml`'s `nav` as a top-level "Changelog" page. Because it's built fresh per version, each mike-deployed version of the site shows `CHANGELOG.md` exactly as it existed at that tag — a real per-release changelog, not just a link out to GitHub.

**Version indicator:** `docs/installation.md`'s image-tag examples remain illustrative placeholders (e.g. `:v0.3.0`) with an explicit "substitute the actual latest tag" note — deliberately not kept in lockstep with the mike version selector, to avoid a second place that goes stale. The version a visitor is actually looking at is shown by Material's version selector itself (top of every page), sourced from mike's `versions.json`, not hand-maintained anywhere.

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
