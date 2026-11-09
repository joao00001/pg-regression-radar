# CI/CD

*Every GitHub Actions workflow in this repo, what it checks, and when it runs.*

## Overview

pg-regression-radar runs fifteen workflows plus Dependabot. This page is a map of what each one does; see [Testing](testing.md) for how to reproduce the test-related ones locally, [Branch Protection](branch-protection.md) for how (and whether) they're actually enforced as required checks on `main`, [Backporting](backporting.md) for the two workflows (`backport.yml`, `release-branches.yml`) and the `release-prep.yml` changes that exist specifically to support shipping a fix to an already-released tool version, and [`branch-cleanup.yml`](#branch-cleanupyml-on-a-pr-closing-merged-or-not) below for how this repo keeps disposable branches (including the ones those two workflows create) from piling up.

**Security scanning, end to end:** this repo uses only tools that are free for a public repository, spread across the layer each is actually good at — no single tool covers all of these:

| Layer | Tool | Where | Blocking? |
|---|---|---|---|
| Go source (semantic SAST) | CodeQL | `codeql.yml` | No — code scanning alerts, by design (see below) |
| Go source (pattern-based security linter) | gosec | `go-quality.yml` | Not yet — first run, no baseline (see below) |
| Go dependencies (known, reachable CVEs) | govulncheck | `go-quality.yml` | Not yet — first run, no baseline (see below) |
| Go dependencies (version currency) | Dependabot | `dependabot.yml` | N/A — opens update PRs, doesn't gate CI |
| Container base images (version currency) | Dependabot (`docker` ecosystem) | `dependabot.yml` | N/A — opens update PRs, doesn't gate CI |
| Built container images (known CVEs in OS packages/libraries) | Trivy | `release.yml` | **Yes** — CRITICAL-severity finding fails the `images` job, nothing is pushed |

CodeQL findings are never blocking by design — `github/codeql-action/analyze` only fails the workflow on a tool/build error, never on an actual finding; results surface as Security tab alerts instead, the same UX as Dependabot alerts. gosec and govulncheck, by contrast, *can* fail their own step (`continue-on-error: true` is what's currently absorbing that) — both are first-time additions to this repo with no established clean baseline yet, so they start in report-only mode. Once a run of each comes back clean (or any real findings are triaged and fixed/suppressed), removing `continue-on-error: true` from that job is the one-line change that makes it a real gate. Trivy in `release.yml` predates this and already gates for real, because it was added with a known-clean baseline from the start.

## `ci.yml` — on every push/PR to `main` or a `release-*` branch

Also runs against `release-*` branches (not just `main`) so a backport pull request gets the exact same build/test gate before merging into a maintenance line — see [Backporting](backporting.md#ci-on-backport-pull-requests) for the full rationale and which checks are *not* extended this way.

| Job | What it proves |
|---|---|
| `Build & Test` | `go build ./...`, `go vet ./...`, `go test -race ./...` against fakes/mocks — no external services. |
| `Integration tests (real PostgreSQL)` | A `postgres-version: [16, 17, 18]` matrix — runs the `integration`-tagged tests in `internal/storage/postgres`, `internal/collector`, and `internal/e2e` against a real `postgres:16`/`postgres:17`/`postgres:18` container (one job per version) with `pg_stat_statements` preloaded. See [Support Matrix](support-matrix.md#officially-supported-postgresql-versions) for why 16 is the floor. |
| `Integration tests (EDB Postgres)` | Same integration test suite, run against EDB Postgres Advanced Server and EDB Postgres Extended Server images — but only when the `EDB_SUBSCRIPTION_TOKEN` repository secret is configured (those images live behind a private, authenticated registry), and with `continue-on-error: true` so an absent secret or a real failure here can't break CI for anyone else. See [Support Matrix](support-matrix.md#ci-coverage-for-edb-distributions) for the full rationale. |
| `Integration tests (real pg_store_plans extension)` | Builds a real PostgreSQL 17 server with the real, upstream [`pg_store_plans`](https://github.com/ossc-db/pg_store_plans) extension compiled via PGXS (`scripts/pg_store_plans`), and runs `internal/planner`'s `pg_store_plans`-specific integration test against it — proving `CapturePlanFromStorePlans` reads back a real plan for a real, actually-executed query, not just a fake `*sql.DB`. Unlike the EDB job above, everything this pulls from is public and unauthenticated, so it runs unconditionally, with no `continue-on-error`. See [Testing](testing.md#real-pg_store_plans-extension) and [Roadmap](roadmap.md#known-robustness-gaps). |
| `Controller tests (real kube-apiserver)` | Runs `internal/controller`'s `TestEnvtest_*` suite against a real, locally-provisioned kube-apiserver + etcd via controller-runtime's envtest. |

Every `go test`-based job above (every row except `Helm render tests`, which is shell, not Go) runs its tests through [`gotestsum`](https://github.com/gotestyourself/gotestsum) instead of plain `go test`, producing a real JUnit XML report alongside the usual console output. Each job uploads its report as a build artifact (`junit-unit`, `junit-integration-postgres-16`/`17`/`18`, `junit-integration-edb-epas`/`pge`, `junit-integration-pg-store-plans`, `junit-envtest`) with `if: always()`, so a report exists whether the job passes or fails. [`test-report.yml`](#test-reportyml-on-ciymls-completion) below turns these into GitHub Check Runs.

## `test-report.yml` — on `ci.yml`'s completion

A separate `workflow_run`-triggered workflow that downloads every `junit-*` artifact `ci.yml` uploaded and feeds each one to [`dorny/test-reporter`](https://github.com/dorny/test-reporter), producing one GitHub Check Run per job — a pass/fail/skipped count, a per-test-case listing, and inline annotations pointing at the exact test (and, where the stack trace allows it, the exact source line) that failed. This replaces the single opaque pass/fail tick a plain `go test` step leaves behind with the kind of structured, drill-down test report CI for a project like this should have.

It's a separate workflow (triggered by `workflow_run`, not a job appended to `ci.yml`) specifically so it always runs with this repository's own read-write `GITHUB_TOKEN`, even when the triggering `ci.yml` run came from a pull request opened from a fork. GitHub Actions always gives a `pull_request`-triggered workflow a read-only token on fork PRs — which can never create a Check Run regardless of the `permissions:` block — but a `workflow_run` workflow always executes against this repo's default branch with its own normal permissions, no matter whose fork triggered the run it's reporting on. This is `dorny/test-reporter`'s own documented pattern for public repositories (see its README's "Recommended setup for public repositories" section).

One consequence: a PR that only changes this file won't see it react to its own `ci.yml` run until after it's merged — `workflow_run` always resolves the workflow definition from the base branch, never the PR branch.

## `go-quality.yml` — on every push/PR to `main` or a `release-*` branch

Also extended to `release-*` branches for the same reason as `ci.yml` above — a backport PR's lint/vuln/security gate shouldn't be weaker than a normal PR's.

| Job | What it proves |
|---|---|
| `golangci-lint` | Static analysis beyond `go vet` (pinned version `v2.12.2`, no repo-specific config — runs with the tool's default linter set, which does **not** include a security-focused linter; that's what `gosec` below is for). |
| `gofmt` | Every tracked `.go` file is `gofmt`-clean. |
| `govulncheck` | Whether any dependency in `go.sum` has a known vulnerability that's actually reachable from this code's own call graph (not just present in the dependency tree) — see the Security scanning table above for why it's report-only for now. |
| `gosec` | Pattern-based security static analysis (hardcoded credentials, weak crypto, SQL string concatenation, etc.) — see the Security scanning table above for why it's report-only for now. Findings are uploaded as SARIF to Security -> Code scanning regardless of outcome. |

## `pr-title.yml` — on PR open/edit/sync, targeting `main`

Validates the PR title against [Conventional Commits](https://www.conventionalcommits.org/) using the exact types/scopes documented in [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#commit-messages-conventional-commits) — the title becomes the squash-merge commit message on `main`, so it's enforced the same way a commit message would be. Scoped to pull requests targeting `main` only: a backport pull request's title (`[Backport release-X.Y] <original title>`, generated by `backport.yml`) isn't itself Conventional-Commits-shaped, and doesn't need to be — see [Backporting](backporting.md#ci-on-backport-pull-requests).

## `changelog-fragment-check.yml` — on PR open/edit/sync

Enforces the "changeset"/news-fragment convention described in [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#release-notes--changeset-fragments): any PR whose title's Conventional Commits type is `feat`, `fix`, or `perf` must add exactly one file to `changelog.d/` named `<PR-number>.<type>.md`, and that file's content must pass seven format rules (single paragraph, capitalized, ends in `.`/`!`/`?`, no heading, no code block, doesn't repeat the type prefix, 20-400 characters). PRs of any other type (`docs`, `test`, `refactor`, `chore`, `ci`) are exempt — they don't describe anything an end user would see in release notes.

| Step | What it does |
|---|---|
| Determine PR type | Parses the PR title with the same type vocabulary `pr-title.yml`'s `types:` list uses (kept manually in sync — see the comment in both files). |
| Gate | Only `feat`/`fix`/`perf` require a fragment; anything else short-circuits with a no-op pass. |
| Find the fragment | Diffs the PR against its base ref for added files under `changelog.d/`, and fails with a specific message if none, more than one, or a wrongly-named file was added. |
| Validate content | Runs `scripts/check_changelog_fragment.py <file>` — a small stdlib-only Python script (no `towncrier` dependency needed for this part) that checks each of the seven content rules independently and fails with the exact rule number and a human-readable reason (e.g. `[3-ends-with-punctuation]`), plus a link back to CONTRIBUTING.md. |

This is a separate workflow from `pr-title.yml` (rather than a job appended to it) because `pr-title.yml` runs on `pull_request_target` (needed so it can comment on forked PRs without leaking secrets), while this check only ever needs to read the PR's own diff and is safer as plain `pull_request`.

## `codeql.yml` — on push/PR to `main`, plus a weekly schedule

Semantic SAST for Go via GitHub's own CodeQL engine — see the Security scanning table above for how this fits alongside `gosec`/`govulncheck`/Trivy. Runs the `security-and-quality` query suite (CodeQL's default, not the broader `security-extended` suite — same "keep signal-to-noise high, no repo-specific config" preference as `golangci-lint`'s default linter set). The weekly schedule (Mondays) catches new findings against unchanged code when CodeQL's own query packs are updated upstream, the same rationale as running Dependabot/`govulncheck` on a cadence rather than push-only.

Findings appear under Security -> Code scanning alerts, never as a failed check — see the Security scanning table above for why that's true of code scanning generically, not a severity threshold that could be tuned.

**One-time manual note:** if this repo ever enables "Default setup" for code scanning in Settings -> Code security, GitHub manages its own CodeQL workflow and this file becomes redundant with it — the two should not both run. No such setup exists yet, so `codeql.yml` is the only CodeQL workflow.

## `dco.yml` — on every PR to `main`

Verifies every commit in the PR carries a `Signed-off-by:` trailer (DCO), via a hand-rolled `git`/`grep` check rather than a third-party Action — DCO enforcement is simple enough that pulling in an external dependency for it isn't worth the supply-chain surface.

## `release-prep.yml` — on push to `main` or a `release-*` branch touching `changelog.d/`

Automates the one manual step the `changelog.d/` fragment system was always meant to make possible to automate: building `CHANGELOG.md`. On every push to `main` **or any `release-X.Y` maintenance branch** that touches `changelog.d/` (and via `workflow_dispatch` on demand), it checks whether any real fragment is pending (anything besides `changelog.d/README.md`), and if so:

| Step | What it does |
|---|---|
| Compute next version | Runs `git describe --tags --abbrev=0 --match 'v*'` — **relative to the current ref**, not a repo-wide tag search — to find the latest version reachable from here, then bumps it: any pending `*.feat.md` fragment means a minor bump (patch resets to `0`); `fix`/`perf`-only fragments mean a patch bump. Same convention as semantic-release/Conventional Commits — the highest-impact pending fragment type decides the bump. There's no major-bump case, since this repo's fragment vocabulary (`towncrier.toml`) has no "breaking" type yet. On a `release-X.Y` branch, this always resolves to a patch bump regardless of fragment type (a maintenance line only ever ships patches), and the workflow fails loudly if that branch's own name ever disagrees with the major.minor of the latest tag it can actually reach — a mismatch there means the branch is misconfigured, not something to silently paper over. |
| Build `CHANGELOG.md` | `towncrier build --version vX.Y.Z --yes` — the exact command the repo owner used to run by hand. |
| Open or update a PR | [`peter-evans/create-pull-request`](https://github.com/peter-evans/create-pull-request) pushes the result to a reused `release-prep-${{ github.ref_name }}` branch (based off whichever ref triggered the run) and opens (or updates, if one's already open) a PR titled `ci: prepare release vX.Y.Z`, signed off as `github-actions[bot]` so `dco.yml` passes it. |

If another `feat`/`fix`/`perf` PR merges while the release-prep PR is still open, the next run recomputes the version and content from scratch and updates the *same* PR — there is never more than one open at a time per ref (`concurrency: group: release-prep-${{ github.ref_name }}` also prevents two runs against the same branch from racing to push it, while letting `main` and every `release-X.Y` line prepare their own release independently of each other).

This automates *building* the changelog, not *deciding when to release*: a person still reviews the compiled `CHANGELOG.md` diff like any other PR, merges it, and only then tags — see [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#release-notes--changeset-fragments). Full auto-tagging (building the changelog *and* creating/pushing the `vX.Y.Z` tag with no review step) was deliberately not chosen: it would need CI to either force-push a moved tag or bypass this repo's own branch protection to push straight to `main`/`release-X.Y`, both of which trade away the review step for not much real benefit — the changelog-building half was the actual repetitive manual work, and this workflow already removes exactly that.

**One-time manual prerequisite**, same shape as `release.yml`'s GHCR note: Settings → Actions → General → Workflow permissions must allow "Read and write permissions", **and** "Allow GitHub Actions to create and approve pull requests" must be checked — without the latter, `create-pull-request` fails to open the PR even though this workflow's own `permissions:` block is otherwise sufficient.

See [Backporting](backporting.md#cutting-a-patch-release-from-a-maintenance-branch) for the full walkthrough of cutting a patch release from a `release-X.Y` branch once one or more backport PRs have merged into it.

## `backport.yml` — on a PR merging into `main`

Implements the `backport release-X.Y` label described in full on [Backporting](backporting.md). Triggered by `pull_request_target: types: [closed]`, scoped to PRs targeting `main`, and gated on `github.event.pull_request.merged` — closing a PR without merging it never does anything here.

| Job | What it does |
|---|---|
| `validate-targets` | Reads every `backport <branch>` label on the merged PR, parses it against `^backport (release-\d+\.\d+)$`, de-duplicates, and confirms each named branch actually exists (`GET /repos/{owner}/{repo}/branches/{branch}`) — a label naming a branch that was never created (typo, or a line nobody's maintaining) fails the job with a comment on the PR explaining exactly which label is wrong, rather than silently skipping it or letting the backport action fail deeper into its own run. Outputs the validated, space-delimited branch list. |
| `backport` | Only runs if `validate-targets` succeeds. Invokes [`korthout/backport-action@v4`](https://github.com/korthout/backport-action) with `target_branches` set to that validated list (its own independent `label_pattern` matching is disabled — `validate-targets` is the single source of truth for targets), `cherry_picking: auto` (picks the one squash-merge commit; see [CONTRIBUTING.md: Squash merge only](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#pull-requests)), and `experimental.conflict_resolution: draft_commit_conflicts` so a cherry-pick that doesn't apply cleanly becomes a draft PR with the conflict committed as-is, instead of a bare failed Actions run. |

`concurrency: group: backport-${{ github.event.pull_request.number }}` ensures re-runs for the same PR (e.g. a label added after the fact) queue rather than race each other; it never cancels an in-flight backport (`cancel-in-progress: false`), since a half-applied cherry-pick is worse than a slightly delayed one.

## `release-branches.yml` — on pushing a `v*` tag

Keeps the set of valid `backport release-X.Y` targets up to date with zero manual branch-cutting. Triggered by the same `push: tags: v*` event `release.yml` reacts to, but is a fully independent workflow file with no shared jobs — see [Backporting: the release job is unaffected](backporting.md#the-release-job-is-unaffected) for why this can't regress `release.yml` itself.

For a pushed tag `vX.Y.Z`: pre-release tags (containing `-`, e.g. `v2.0.0-rc1`) and patch releases (`Z != 0`) are skipped — only the first release of a minor/major line (`vX.Y.0`) does anything. For such a tag, it idempotently (checking first, so a re-run or a manually pre-created branch/label is a no-op, not an error):

- creates branch `release-X.Y` pointed at that tag's commit, and
- creates the matching `backport release-X.Y` label.

See [Backporting: Maintenance branches](backporting.md#maintenance-branches-release-xy) for the full lifecycle this feeds into.

## `branch-cleanup.yml` — on a PR closing (merged or not)

Deletes a pull request's head branch the moment it closes, whether merged or closed without merging — feature branches, `backport.yml`'s own per-target branches, and `release-prep.yml`'s reused `release-prep-${{ github.ref_name }}` branch all get cleaned up this way instead of piling up indefinitely.

Deliberately a workflow rather than just the repository's native **Settings → General → "Automatically delete head branches"** toggle (which is **not** enabled on this repo): that setting only fires on merge, never on a PR closed without merging, and this repo wants both. Keeping it as one workflow, rather than a mix of that setting plus a workflow for the other case, avoids two independent mechanisms that could ever disagree.

Triggered by `pull_request: types: [closed]` with no branch filter, so it reacts to a PR closing against `main`, a `release-X.Y` branch, or anywhere else:

| Safeguard | Why |
|---|---|
| Job gated on `head.repo.full_name == github.repository` | A PR opened from a fork has its head branch *in the fork*, not this repo — never attempted, and couldn't succeed with this repo's own token anyway. |
| `main` and any `release-\d+\.\d+` branch name are never deleted | These are long-lived branches this repo depends on, even in the unusual case one of them is ever a PR's *head* rather than its usual role as a base (e.g. a hypothetical "merge release-1.6 fixes forward into main" PR). Note this regex deliberately does **not** match `release-prep-main`/`release-prep-release-1.6` — those *are* meant to be cleaned up. |
| A real branch protection rule, if one exists on the branch | Checked independently of the name-based check above, as a second safety net for any branch this repo ever protects that the naming check doesn't happen to catch. |
| Already-deleted branch (404/422 from the delete call) | Treated as a no-op, not a failure — e.g. if someone already clicked GitHub's own "Delete branch" button. |

## `e2e-manual.yml` — manual only (`workflow_dispatch`)

The containerized, real-artifact end-to-end smoke test — see [Testing](testing.md#manual-e2e-real-container) for the full description. Deliberately not wired to push/PR: it spins up multiple long-lived containers and sleeps through real wall-clock windows, so it's opt-in rather than adding minutes to every PR.

## `e2e-kind.yml` — manual (`workflow_dispatch`) or opt-in via the `run-e2e-kind` PR label

The real-Kubernetes end-to-end test for the CRD-driven mode (`cmd/manager`) — see [Testing](testing.md#e2e-kind-cloudnativepg) for the full description. Creates a real `kind` cluster, installs the real CloudNativePG operator and a real `Cluster`, installs pg-regression-radar via the Helm chart, and asserts a real `PerformanceRegression` CR is created. Real ArgoCD and `pg_store_plans` are deliberately out of scope for this first pass — see [Roadmap](roadmap.md). Same opt-in reasoning as `e2e-manual.yml`: this is even heavier (a full cluster plus two operators), so it doesn't run on every push/PR by default.

It's opt-in on a per-PR basis, though: adding the `run-e2e-kind` label to a pull request runs this workflow against it, and every subsequent push to that PR re-runs it automatically for as long as the label stays attached (the job's `if:` checks the label on both the `labeled` and `synchronize` pull_request events — see the workflow's own header comment). Adding any other label never triggers it. Remove the label, or never add it, and this workflow costs nothing on that PR.

On every run (manual or label-triggered), a final `Collect cluster diagnostics` step writes everything useful the kind cluster still knows — the `PostgresWatch`/`DeploySource`/`PerformanceRegression` resources, the CloudNativePG `Cluster`'s full YAML and `describe` output, cluster events, and the manager's and CloudNativePG controller's logs — to files, which an `if: always()` step then uploads as the `e2e-kind-diagnostics-<run-id>-<attempt>` build artifact. This matters specifically here because helm/kind-action tears the entire kind cluster down the instant the job ends (pass or fail): anything not captured to a file before that happens is gone for good, so this is the only way to debug a failure (or confirm a pass for the right reason) after the fact.

## `e2e-kind-multicluster.yml` — manual (`workflow_dispatch`) or opt-in via the `run-e2e-kind-multicluster` PR label

The real-two-cluster end-to-end test for fleet mode — see [Testing](testing.md#e2e-kind-multicluster) for the full description. Creates two real `kind` clusters (`hub`, `spoke`): the spoke runs the real CloudNativePG operator and a real `Cluster`; the hub runs this commit's manager (via the Helm chart) with a `PostgresWatch` that reaches across to the spoke using `remoteClusterRef` and the new `cnpgClusterRef`. Asserts both `PostgresWatch.status.phase=Running` and `PostgresRadarCluster.status.ready=true`, then rotates the registered kubeconfig to an invalid credential and back to prove the health-check reconciler both detects and recovers from a real credential rotation. Same opt-in reasoning as `e2e-kind.yml`, on its own label so either suite can be run independently: this one stands up two clusters instead of one, so it's heavier still.

It's opt-in on a per-PR basis: adding the `run-e2e-kind-multicluster` label to a pull request runs this workflow against it, and every subsequent push to that PR re-runs it automatically for as long as the label stays attached — identical `labeled`/`synchronize` gating to `e2e-kind.yml`, just on its own label. A final `Collect cluster diagnostics` step uploads the `e2e-kind-multicluster-diagnostics-<run-id>-<attempt>` build artifact before both kind clusters are torn down, for the same reason as `e2e-kind.yml`'s equivalent step.

## `release.yml` — on pushing a `v*` tag

Publishes a release: builds, scans, signs, and pushes every artifact this repo ships, gated on pushing a tag matching `v*` (e.g. `git tag v0.3.0 && git push origin v0.3.0`) — never on an ordinary merge to `main`, so there is no continuously-updated "edge" image, only real tagged releases. See [Installation](installation.md#option-3-pull-or-build-the-container-image) for the resulting pull/install commands.

| Job | What it does |
|---|---|
| `images` (matrix: `cli`/`operator`/`manager`/`collector`/`ingester` × `linux/amd64`/`linux/arm64`) | For each of the Dockerfile's five `--target` values, on each platform: builds the arch-specific image (loaded into the runner's local Docker daemon, not yet pushed — cross-compiled via the Dockerfile's `--platform=$BUILDPLATFORM` build stage + `GOARCH=$TARGETARCH`, not run under QEMU emulation, though [`docker/setup-qemu-action`](https://github.com/docker/setup-qemu-action) is still registered defensively), generates an SPDX SBOM with [`anchore/sbom-action`](https://github.com/anchore/sbom-action) (Syft under the hood — retried once automatically on failure, since this step's syft download from github.com's Releases CDN has hit a transient connection failure in a real release run before; a second consecutive failure still fails the job for real), scans it with [`aquasecurity/trivy-action`](https://github.com/aquasecurity/trivy-action) and **fails the job on any CRITICAL-severity finding** (nothing is pushed if this fails), then — only once the scan passes — pushes it under an arch-specific tag, `ghcr.io/joao00001/pg-regression-radar/<target>:<tag>-<arch>` (never directly as `:<tag>`/`:latest` — see `manifests` below), signs that digest keylessly with [`cosign`](https://github.com/sigstore/cosign), attaches the SBOM as a signed in-toto attestation (`cosign attest`), and publishes build provenance attestations via `actions/attest-build-provenance` (SLSA format). |
| `manifests` (needs `images`; matrix: `cli`/`operator`/`manager`/`collector`/`ingester`) | Combines each target's two already-pushed, already-scanned arch-specific images into the single multi-arch `:<tag>` and `:latest` tags everything else in this repo actually references, via `docker buildx imagetools create` — a registry-side operation with no rebuilding, re-pulling, or re-scanning. Also cosign-signs the resulting manifest-list digest and publishes a provenance attestation for that digest, so both signature and SLSA provenance verification work directly against the plain `:<tag>`/`:latest` tag most people pull. |
| `helm-chart` (needs `manifests`) | Packages `deploy/helm/deploylens` with `--version`/`--app-version` derived from the pushed tag (chart version strips the tag's leading `v` for SemVer compliance; `appVersion` keeps it, matching the image tags), pushes it as an OCI artifact to `oci://ghcr.io/joao00001/charts`, parses the digest out of `helm push`'s own `Digest: sha256:...` output line, signs that digest keylessly with `cosign`, and publishes a provenance attestation for that digest. Runs after `manifests` so a chart is never published pointing at images that failed to build/scan/publish/combine. |
| `release-notes` (needs `images`, `manifests`, `helm-chart`) | Extracts the section of `CHANGELOG.md` matching the pushed tag (from its `## vX.Y.Z - <date>` heading up to, but not including, the next `## ` heading, via a small `awk` script) and publishes it as the body of a GitHub Release via [`softprops/action-gh-release`](https://github.com/softprops/action-gh-release). That section only exists because `release-prep.yml` (below) built it and it was merged to `main` *before* the tag was pushed — this job fails loudly (rather than publishing an empty release) if no matching section is found. Runs last, after every other artifact-publishing job, so a release is never announced before its images/manifests/chart actually exist. |

**Release notes are `CHANGELOG.md`-derived, not generated from commit history in this workflow.** An earlier draft of this workflow considered generating release notes directly from commit messages via [`git-cliff`](https://git-cliff.org/) (with a checked-in `cliff.toml`), but that never actually landed on `main`, and the `changelog.d/` fragment system above supersedes it: fragments are written by a human in user-facing language and enforced per-PR in CI (`changelog-fragment-check.yml`), which a commit-message-driven changelog can't guarantee. There is no `git-cliff` usage or `cliff.toml` anywhere in this repo.

**Supply chain and signing:** every artifact published by this workflow is cryptographically signed with [Cosign](https://github.com/sigstore/cosign) using keyless signing — the runner exchanges its own GitHub Actions OIDC token for a short-lived certificate from [Sigstore Fulcio](https://github.com/sigstore/fulcio), and the signature is recorded in the public [Rekor](https://github.com/sigstore/rekor) transparency log. No stored private key is needed or used. Signing covers: each per-arch image digest (in the `images` job), each multi-arch manifest-list digest (in the `manifests` job), and the Helm OCI artifact digest (in the `helm-chart` job). Each per-arch image also carries an SPDX-JSON SBOM attached as a signed in-toto attestation via `cosign attest`, and release artifacts now also receive build provenance attestations via `actions/attest-build-provenance` (SLSA format). The SBOM is generated by [`anchore/sbom-action`](https://github.com/anchore/sbom-action) (Syft) from the locally-built image before it is pushed; a persistent generation failure (after one automatic retry) fails the job and blocks the release. See [Supply Chain Verification](supply-chain.md) for the verification commands.

**Permissions:** `packages: write` (`images`, `manifests`, and `helm-chart`, to push to `ghcr.io`), `id-token: write` (`images`, `manifests`, and `helm-chart`, for cosign's OIDC-to-Fulcio exchange), and `contents: write` (`release-notes` only, for `softprops/action-gh-release` to create the Release). All three are satisfied entirely by the built-in `GITHUB_TOKEN` — no registry credential secret is configured or needed, since GHCR is free for public repositories and accepts `GITHUB_TOKEN` directly.

**One-time manual prerequisite:** GHCR pushes need Settings → Actions → General → Workflow permissions set to "Read and write permissions" on this repository — that setting can't be applied from workflow YAML, and without it every push in this workflow fails with 403 regardless of the `permissions:` block above. See the workflow's own top comment.

**Chart versioning:** `deploy/helm/deploylens/Chart.yaml`'s checked-in `version`/`appVersion` are development-time placeholders only — `helm package --version --app-version` overrides both unconditionally from the pushed git tag at release time, so they never need editing in lockstep with a release. This is a deliberate design choice, not a gap.

**Proven against real releases:** this workflow has run for real against `v1.0.0` and `v1.0.1` — real pushes to `ghcr.io`, real keyless signing via GitHub's OIDC-to-Fulcio exchange, and a real Helm OCI chart push all succeeded, including the `linux/arm64` cross-compilation. The one real failure found so far was in `v1.0.0`'s `helm-chart` job: `cosign sign` failed with `UNAUTHORIZED` because `helm registry login` writes credentials to a different config file than cosign's default keychain reads. Fixed in `v1.0.1` by adding a separate `docker/login-action` step before the cosign step. Not independently confirmed: an actual `arm64` machine pulling and running the resulting multi-arch image, as opposed to the cross-platform build itself succeeding.

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

**Outdated-version warning banner:** `overrides/main.html` (wired in via `mkdocs.yml`'s `theme.custom_dir: overrides`) overrides Material's `outdated` block so a visitor browsing any version other than the one aliased `latest` sees a banner above the header linking back to the site root — see [Material's version-warning docs](https://squidfunk.github.io/mkdocs-material/setup/setting-up-versioning/#version-warning). The banner's markup is baked into every version's build identically; Material's own client-side JS decides at page-load time (by comparing the current version against `versions.json`) whether to actually unhide it, so this needs no per-version build logic of its own.

## Dependabot (`.github/dependabot.yml`)

Weekly update checks for `gomod`, `github-actions`, and `docker` ecosystems, with `chore(deps)` / `ci(deps)` commit-message prefixes matching this project's Conventional Commits convention. The `docker` entry covers the base image(s) in the repo-root Dockerfile's `FROM` lines — the same five-target multi-stage build `release.yml` publishes from — so a base-image security patch shows up as an ordinary Dependabot PR, the same way a vulnerable Go dependency would.

This is version-currency only (opens a PR when a newer version exists); it's not what actually blocks a known-vulnerable dependency or image from shipping — see the Security scanning table above for `govulncheck` (Go deps) and Trivy (built images), which check for actual CVEs rather than just staleness.

## Branch protection

None of the above has real effect unless `main`'s (and, where applicable, each `release-X.Y` branch's) ruleset/branch protection actually requires each check by name — see [Branch Protection](branch-protection.md) for the exact check names and which settings are worth enabling alongside them.

## See also

- [Testing](testing.md) — reproducing the test-related jobs locally.
- [Backporting](backporting.md) — the `backport release-X.Y` label, maintenance branches, and the conflict-resolution flow.
- [Installation](installation.md) — pulling/verifying the images and Helm chart `release.yml` publishes.
- [Supply Chain Verification](supply-chain.md) — operator commands to verify artifact signatures, SBOMs, and provenance.
- [Support Matrix](support-matrix.md) — officially supported PostgreSQL versions/distributions and exactly which of them each CI job verifies.
- [Branch Protection](branch-protection.md) — whether these checks actually block anything.
- [Roadmap](roadmap.md) — CI/CD items still open (branch protection review, Discussions link, `release.yml` follow-ups).

