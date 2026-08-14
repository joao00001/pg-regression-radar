# Changelog

All notable changes to this project are documented in this file. The format
loosely follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the
category names are this project's own (matching the type/scope table in
[CONTRIBUTING.md](CONTRIBUTING.md#commit-messages-conventional-commits))
rather than Keep a Changelog's stricter Added/Changed/Fixed vocabulary,
since a flat categorization loses the thematic grouping that makes these
notes readable.

**How this file is maintained:** every PR that ships a user-facing `feat`,
`fix`, or `perf` change adds its own newsfragment under `changelog.d/`
(format documented in [CONTRIBUTING.md](CONTRIBUTING.md#release-notes-changeset-fragments),
enforced by `.github/workflows/changelog-fragment-check.yml`). Before
cutting a release tag, the maintainer runs `towncrier build --version
vX.Y.Z --yes`, which compiles every fragment currently in `changelog.d/`
into a new dated section here and deletes the consumed fragments —
`.github/workflows/release.yml`'s `release-notes` job then extracts that
section verbatim as the GitHub Release body. See `towncrier.toml` for the
category configuration.

**Scope note on `v1.0.0-rc1` below:** that section was a one-time historical
backfill — generated with [git-cliff](https://git-cliff.org) from `git log
v0.1.0..main` (47 commits) and hand-reorganized into the thematic sections
below — covering everything that shipped before the newsfragment process
above existed. Since it predates any real `changelog.d/` fragments, it was
hand-labeled with this release's version rather than produced by `towncrier
build` (there was nothing for that command to consume yet). `git-cliff` and
`cliff.toml` are retired and not part of the ongoing process; every future
entry in this file comes from `changelog.d/` fragments via `towncrier
build` as described above.

<!-- towncrier release notes start -->

## v1.3.0 - 2026-08-14

### Features

- Added Prometheus counters for detected regressions and webhook notification successes or failures, so operators can alert on broken delivery paths and chart alerting reliability from `/metrics`. (#87)

### Fixes

- Webhook notifications now drain response bodies before closing them so Go can reuse keep-alive HTTP connections between alerts. (#89)
- Malformed PostgreSQL DSNs that look like redacted or broken connection URIs are now rejected at operator startup with a clear parse error instead of silently falling back to localhost until scrape time. (#90)
- Collector scrapes now cap `pg_stat_statements` query text at the configured `--max-query-text-len` budget instead of always asking PostgreSQL for up to 500 characters per row. (#91)
- Manager startup failures that prevent controller informers from reaching the Kubernetes API now best-effort mark existing PostgresWatch and DeploySource resources with a non-empty degraded status instead of leaving them blank. (#92)
- Periodic regressions now carry the configured namespace, so alerts, queries, and Kubernetes resources stay scoped to the same namespace as the PostgresWatch or operator configuration. (#93)


## v1.2.0 - 2026-08-14

### Features

- Alerting now supports more than Slack: `--alert-format`/`spec.alerting.format` selects between `slack` (default, unchanged), `teams` (Microsoft Teams Incoming Webhook), `pagerduty` (Events API v2, opens/updates an incident), or `custom` (a user-supplied Go `text/template`, for any destination that isn't one of the built-in three). `slackWebhookUrl`/`--slack-url` keep working exactly as before. See [Alerting](https://joao00001.github.io/pg-regression-radar/alerting/) for the full field/flag reference and the custom template's available fields. (#71)
- Regression detection no longer requires a tracked deploy. `--periodic-detection`/`spec.periodicDetection.enabled` runs the same E-divisive/Welch's-t-test analysis on a rolling schedule, per query, independent of any `DeployEvent` — catching regressions with no deploy behind them (autovacuum lag, index bloat, stale planner stats, organic growth) that were previously invisible to this tool. A re-arm state machine suppresses repeat alerts for an ongoing episode until the query recovers. Deploy-triggered detection is unaffected either way; the two paths run side by side. See [Periodic Detection](https://joao00001.github.io/pg-regression-radar/periodic-detection/) for configuration and the false-positive caveat, and [ADR-0001](https://joao00001.github.io/pg-regression-radar/adr/0001-deploy-independent-regression-detection/) for the design rationale. (#73)


## v1.1.0 - 2026-08-13

### Features

- Make deploy-event retry analysis observable: the operator and manager now log when a deploy event is registered for retry and when its analysis window elapses, and expose a `pg_regression_radar_pending_deploy_events` gauge, so an operator can tell "still waiting for data" apart from a silent failure and confirm memory stays bounded over long uptimes. (#65)
- Add a native Kubernetes watch deploy source: setting `sourceType: kubernetes` on a `DeploySource` CR watches a Deployment or StatefulSet directly and emits a deploy event once its rollout completes, so clusters with no ArgoCD, Argo Rollouts, or Flux installed can still be correlated against — no webhook required. (#66)
- Add opt-in Argo Rollouts auto-abort: setting `autoAbort.enabled` on a `PostgresWatch` now aborts the Argo Rollouts canary behind a sufficiently confident detected regression automatically, instead of only alerting and waiting for a human to act, with its own confidence threshold and RBAC gate, and the outcome recorded on the resulting `PerformanceRegression`. (#67)


## v1.0.2 - 2026-08-13

### Fixes

- Fix regression detection silently missing real regressions: both `operator` and `manager` used to analyse a deploy event exactly once, immediately on arrival, well before enough post-deploy data could exist — now analysis retries every poll tick until the deploy's analysis window closes, deduplicating notifications so a detected regression is still only ever reported once. (#64)


## v1.0.1 - 2026-08-12

### Fixes

- Add a separate `docker/login-action` step to the release workflow's Helm chart job, so `cosign` can find registry credentials it needs to sign the chart — `helm registry login` alone writes to a config file `cosign` never reads, which made the chart-signing step fail with an authentication error on every release. (#60)


## v1.0.0 - 2026-08-12

### Features

- Add `spec.capturePlans` to `PostgresWatch`, so the CRD-driven `manager` mode can attach a plan-diff summary to a detected `PerformanceRegression`'s `status.planDiffSummary`, matching the standalone `operator` CLI's existing `--capture-plans` behavior. (#51)
- Add `spec.remoteNamespace` to `PostgresWatch`, letting a fleet's remote-cluster DSN Secret live in a differently-named namespace than the hub-side `PostgresWatch`, instead of requiring matching namespace names on both sides. (#58)
- Fleet mode now evicts a remote cluster's cached client immediately after it fails a real request, so a transient network blip or a genuinely rotated kubeconfig no longer keeps a broken connection in circulation until a future reconcile happens to notice. (#59)

### Fixes

- Evict entries from the manager's remote-cluster client cache after an hour of disuse, so a rotated-away kubeconfig or a deleted fleet member's cached client no longer stays in memory for the life of the manager process. (#52)

## v1.0.0-rc1 - 2026-08-12

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

### Multi-cluster (Fleet)

- Add `spec.remoteClusterSecretRef` support: `PostgresWatchReconciler` can
  now resolve a `PostgresWatch`'s DSN Secret from a *different* Kubernetes
  cluster than the manager itself, via a kubeconfig Secret in the hub
  cluster — closing the gap where the only prior option was manually
  copying the Secret between clusters
  (`feat(controller)`, [`bc0ab5f`](https://github.com/joao00001/pg-regression-radar/commit/bc0ab5f)).

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
controller-runtime mode — see [`docs/roadmap.md`](docs/roadmap.md#feature-roadmap)
for what's shipped since. Predates this file; not reconstructed here since
`git-cliff`'s useful range starts at this tag.
