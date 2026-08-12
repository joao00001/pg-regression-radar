# Changelog

All notable changes to this project are documented in this file. The format
loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the
category names are this project's own (matching the type/scope table in
[CONTRIBUTING.md](CONTRIBUTING.md#commit-messages-conventional-commits))
rather than Keep a Changelog's stricter Added/Changed/Fixed vocabulary,
since a flat categorization loses the thematic grouping that makes these
notes readable.

**How this file is maintained:** `.github/workflows/release.yml`'s
`release-notes` job uses [git-cliff](https://git-cliff.org) (config:
`cliff.toml`) to generate a GitHub Release body automatically for every new
tag, straight from Conventional Commits on `git log`. This file's own
**[Unreleased]** section below was produced the same way — `git-cliff
--config cliff.toml v0.1.0..main` — and then hand-reorganized into the
thematic sections below (raw tool output is one flat list per commit type,
not grouped by subsystem the way a reader actually wants). Every commit
between `v0.1.0` and the current tip of `main` is accounted for below; see
this repository's own git history (`git log v0.1.0..main`) to check. Once
the next tag is cut, this section becomes that release's dated entry and a
fresh `[Unreleased]` section starts above it — either by hand, or by
re-running `git-cliff` and re-applying the same manual grouping pass.

**Scope note:** this covers everything merged to `main` through `v0.1.0..main`
(47 commits). It does **not** yet include `ci/release-publishing` (adds
`release.yml` itself: GHCR image/Helm publishing, Trivy, SBOM, cosign) or
this changelog-automation work, both still on unmerged branches stacked in
that order ahead of `main` as of this writing. See
[`docs/release-notes/v1.0-draft.md`](docs/release-notes/v1.0-draft.md) for
the release-scoping discussion that separates "in the next release" from
"deferred," including those two branches.

## [Unreleased]

### Detection & Correlation

- Deduplicate `PerformanceRegression` findings across a `queryid` rotation,
  which previously produced one duplicate finding per rotated `queryid` for
  what was really a single ongoing regression
  (`fix(correlation)`, [`4c62488`](https://github.com/joao00001/pg-regression-radar/commit/4c62488)).
- Add `EXPLAIN (FORMAT JSON, GENERIC_PLAN)`-based plan-diff correlation
  (PostgreSQL 16+): captures and diffs a query's execution plan around a
  detected regression, an estimate-based fallback source
  (`feat`, PR [#31](https://github.com/joao00001/pg-regression-radar/pull/31), [`84dc8e8`](https://github.com/joao00001/pg-regression-radar/commit/84dc8e8)).
- Add `pg_store_plans` as the *preferred* plan-diff capture source over the
  `GENERIC_PLAN` estimate above, when the extension is installed — it
  records the real, previously-executed plan with real parameter values
  instead of the planner's generic estimate
  (`feat(planner)`, [`b611f50`](https://github.com/joao00001/pg-regression-radar/commit/b611f50)).
- Backfill `cmd/manager`'s in-memory `Collector`/`Engine` state from the
  Postgres persistence backend on restart, so a manager restart no longer
  silently resets detection state to empty — landed iteratively across two
  commits plus the merges that reconciled the feature branch with `main`
  mid-flight (`feat(operator)`,
  [`14c3574`](https://github.com/joao00001/pg-regression-radar/commit/14c3574) and
  [`5007b66`](https://github.com/joao00001/pg-regression-radar/commit/5007b66); merge commits
  [`eaca170`](https://github.com/joao00001/pg-regression-radar/commit/eaca170),
  [`a1b5340`](https://github.com/joao00001/pg-regression-radar/commit/a1b5340)).

> **Known gap, not yet closed:** plan-diff correlation (both capture
> sources above) is implemented and unit-tested, but nothing in
> `internal/correlation`/`internal/alerting` calls `planner.CapturePlan` yet
> — a `PerformanceRegression` alert does not carry a plan diff end-to-end,
> and `pg_store_plans` has never been exercised against a real PostgreSQL
> server with the extension installed. See
> [`docs/roadmap.md`](docs/roadmap.md#known-robustness-gaps) and the v1.0
> draft's gaps section.

### Multi-cluster (Fleet)

- Add `spec.remoteClusterSecretRef` support: `PostgresWatchReconciler` can
  now resolve a `PostgresWatch`'s DSN Secret from a *different* Kubernetes
  cluster than the manager itself, via a kubeconfig Secret in the hub
  cluster — closing the gap where the only prior option was manually
  copying the Secret between clusters
  (`feat(controller)`, [`bc0ab5f`](https://github.com/joao00001/pg-regression-radar/commit/bc0ab5f)).

> **Known gaps, not yet closed:** no kubeconfig rotation/expiration
> handling, the remote client cache never evicts entries, no
> remote-namespace override, the CloudNativePG `Cluster` resource itself is
> never read remotely, and this has not been validated against two real
> Kubernetes clusters (tests use a fake client plus a syntactically valid
> but unreachable kubeconfig). See
> [`docs/roadmap.md`](docs/roadmap.md#multi-cluster-support-in-detail).

### Security

- Add `X-Webhook-Token` bearer-token authentication to the ingester's
  webhook endpoint, closing the gap where any network path to the ingester
  could post a deploy-event webhook with no authentication at all
  (`fix(ingester)`, [`faf94e4`](https://github.com/joao00001/pg-regression-radar/commit/faf94e4)).

### Performance & Scalability

- Replace the O(n³) inner loops of the E-Divisive change-point algorithm
  with an O(n²) formulation
  (`perf`, [`627d5cb`](https://github.com/joao00001/pg-regression-radar/commit/627d5cb)).
- Replace `Store.All()`-based O(N) poll loops with a cursor-based
  `Store.Since()` across the collector/ingester poll path
  (`perf`, PR [#27](https://github.com/joao00001/pg-regression-radar/pull/27), [`9a80538`](https://github.com/joao00001/pg-regression-radar/commit/9a80538)).
- Cursor-based poll draining and binary-search `EventsInRange` in the
  ingester, replacing a linear scan
  (`perf`, [`6dd4659`](https://github.com/joao00001/pg-regression-radar/commit/6dd4659)).
- Replace an O(N×M) `latestSampleTime` computation with an O(1)
  `Collector.LastScrapeTime()`
  (`perf`, [`0b27f86`](https://github.com/joao00001/pg-regression-radar/commit/0b27f86)).
- Halve `Engine.Analyse`'s `SamplesInRange` lock acquisitions
  (`fix(correlation)`, [`af9d21e`](https://github.com/joao00001/pg-regression-radar/commit/af9d21e)).
- Pre-allocate slices in `ingester.Store` and the in-memory stores to
  reduce GC pressure under load
  (`perf`, [`0226995`](https://github.com/joao00001/pg-regression-radar/commit/0226995)).

### CLI & Developer Experience

- Add a unified `pg-regression-radar` CLI so `go install` sets up all
  subcommands from a single binary instead of four separate `go install`
  targets
  (`feat(cli)`, [`6cac0e9`](https://github.com/joao00001/pg-regression-radar/commit/6cac0e9)).
- Add `--version` and `--dry-run` flags to all four binaries
  (`feat(cli)`, [`d973795`](https://github.com/joao00001/pg-regression-radar/commit/d973795)).

### CI/CD & Quality

- Add a real `kind` + CloudNativePG cluster e2e validation for `cmd/manager`
  (`.github/workflows/e2e-kind.yml`): a real cluster, a real CloudNativePG
  operator and `Cluster`, the Helm chart, and a `PostgresWatch` reading its
  DSN from the CloudNativePG-generated Secret
  (`ci(e2e)`, PR [#37](https://github.com/joao00001/pg-regression-radar/pull/37), [`66261fb`](https://github.com/joao00001/pg-regression-radar/commit/66261fb)).
- Document and enforce a PostgreSQL 16/17/18 + EDB Postgres Advanced/Extended
  Server support matrix in CI, including a matrixed integration-test job
  (`docs(support-matrix)`, [`ae93e47`](https://github.com/joao00001/pg-regression-radar/commit/ae93e47)).
- Expand e2e coverage for recently-landed features against a real
  PostgreSQL instance — landed iteratively across two commits
  (`test(e2e)`, [`30b63fe`](https://github.com/joao00001/pg-regression-radar/commit/30b63fe) and
  PR [#42](https://github.com/joao00001/pg-regression-radar/pull/42), [`2e57051`](https://github.com/joao00001/pg-regression-radar/commit/2e57051)).
- Use structurally distinct probe queries in e2e tests so
  `pg_stat_statements` can't merge them into one `queryid`
  (`fix(e2e)`, PR [#29](https://github.com/joao00001/pg-regression-radar/pull/29), [`a6e82a4`](https://github.com/joao00001/pg-regression-radar/commit/a6e82a4)).
- Resolve `golangci-lint` findings and pin a patched Go toolchain version in
  CI (`fix(ci)`, PR [#28](https://github.com/joao00001/pg-regression-radar/pull/28), [`2c50107`](https://github.com/joao00001/pg-regression-radar/commit/2c50107)).

> **Known gap, not yet closed:** `e2e-kind.yml` has been authored and
> syntax/`actionlint`-checked but has never actually run end-to-end — the
> sandbox it was written in has no Docker-in-Docker/`kind` support. Treat
> its first real `workflow_dispatch` run as part of reviewing whichever
> change lands next. See
> [`docs/roadmap.md`](docs/roadmap.md#known-robustness-gaps).

### Documentation

- Add a structured `docs/` site built with MkDocs Material, slim down
  `README.md` to a quick-start pointer at it
  (`docs`, [`4a22bbc`](https://github.com/joao00001/pg-regression-radar/commit/4a22bbc)), and follow-on styling/nav
  fixes: true black/white palette with backdrop blur
  ([`c673b27`](https://github.com/joao00001/pg-regression-radar/commit/c673b27)), transparent tabs bar
  ([`fffe907`](https://github.com/joao00001/pg-regression-radar/commit/fffe907)), thinner active-tab underline
  ([`c02fffe`](https://github.com/joao00001/pg-regression-radar/commit/c02fffe)), an Installation page plus
  active-tab contrast fix ([`068eff5`](https://github.com/joao00001/pg-regression-radar/commit/068eff5)), collapsing
  overflowing nav tabs into a real hover mega-menu across four follow-up
  fixes ([`a4f86d5`](https://github.com/joao00001/pg-regression-radar/commit/a4f86d5),
  [`fd60fe9`](https://github.com/joao00001/pg-regression-radar/commit/fd60fe9),
  [`a5f1d79`](https://github.com/joao00001/pg-regression-radar/commit/a5f1d79),
  [`c9454b1`](https://github.com/joao00001/pg-regression-radar/commit/c9454b1)).
- Move `.github/BRANCH_PROTECTION.md`'s content into the docs site and
  delete the standalone file
  (`docs(ci)`, [`e474939`](https://github.com/joao00001/pg-regression-radar/commit/e474939); housekeeping merge/delete
  [`6175a0f`](https://github.com/joao00001/pg-regression-radar/commit/6175a0f),
  [`05d4f40`](https://github.com/joao00001/pg-regression-radar/commit/05d4f40)).
- Document least-privilege PostgreSQL role guidance for `--dsn`
  (`docs`, [`2dbc330`](https://github.com/joao00001/pg-regression-radar/commit/2dbc330)).
- Document the `PostgresWatch`/`DeploySource`/`PerformanceRegression`
  `v1alpha1` CRD compatibility policy
  (`docs(api)`, [`5a4a744`](https://github.com/joao00001/pg-regression-radar/commit/5a4a744); review-nit follow-up
  [`dccabcd`](https://github.com/joao00001/pg-regression-radar/commit/dccabcd)).
- Fix the Flux Provider authentication example in the webhooks doc to use
  the `headers` field, matching the new webhook-token auth above
  (`docs(webhooks)`, [`1196f20`](https://github.com/joao00001/pg-regression-radar/commit/1196f20)).
- Fix a typo (`fix(doc)`, [`b749609`](https://github.com/joao00001/pg-regression-radar/commit/b749609) — originally
  committed as `fiz(doc)`, a typo in the commit type itself).

### Dependencies & Chores

- Weekly Dependabot bumps to five `github-actions` dependencies:
  `actions/setup-python` 5→7 ([`5304ae3`](https://github.com/joao00001/pg-regression-radar/commit/5304ae3)),
  `actions/upload-pages-artifact` 3→5 ([`73c5383`](https://github.com/joao00001/pg-regression-radar/commit/73c5383)),
  `actions/deploy-pages` 4→5 ([`bf75348`](https://github.com/joao00001/pg-regression-radar/commit/bf75348)),
  `actions/upload-artifact` 4→7 ([`e52fab1`](https://github.com/joao00001/pg-regression-radar/commit/e52fab1)),
  `actions/download-artifact` 4→8 ([`9ea1bab`](https://github.com/joao00001/pg-regression-radar/commit/9ea1bab)).
- Fix `dependabot.yml` formatting
  (`fix`, [`edcb2eb`](https://github.com/joao00001/pg-regression-radar/commit/edcb2eb); merged back in via
  [`c1e8543`](https://github.com/joao00001/pg-regression-radar/commit/c1e8543)).

## [0.1.0]

MVP: Collector + Ingester + Correlation Engine + Slack alerting + Helm
chart, plus Argo Rollouts/Flux deploy-source support and the real
`PostgresWatch`/`DeploySource`/`PerformanceRegression` CRD-driven
controller-runtime mode — see [`docs/roadmap.md`](docs/roadmap.md#version-roadmap)
for the version-by-version breakdown. Predates this file; not reconstructed
here since `git-cliff`'s useful range starts at this tag.
