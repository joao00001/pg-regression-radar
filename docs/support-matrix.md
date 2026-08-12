# Support Matrix

*Officially supported PostgreSQL versions and distributions, what "supported" means for each, and which project features require which minimum version or extension.*

## Overview

This page is the single source of truth for "will pg-regression-radar work against my Postgres, and how thoroughly is that combination actually tested." It covers two independent axes — **PostgreSQL major version** and **distribution** (community/vanilla, CloudNativePG, EDB Postgres Advanced Server, EDB Postgres Extended Server) — plus a feature-by-feature breakdown of minimum version/extension requirements, since "supported" doesn't mean every feature is available on every combination.

## Officially supported PostgreSQL versions

| Version | Status | Verified in CI |
|---|---|---|
| 16 | Officially supported | Yes — every PR (`integration-postgres` matrix, `.github/workflows/ci.yml`) |
| 17 | Officially supported | Yes — every PR (`integration-postgres` matrix, `.github/workflows/ci.yml`) |
| 18 | Officially supported | Yes — every PR (`integration-postgres` matrix, `.github/workflows/ci.yml`) |

**16 is the floor of the officially tested matrix**, not an arbitrary cutoff: it's the minimum version required by this project's newest feature, `EXPLAIN (FORMAT JSON, GENERIC_PLAN)`-based plan-diff capture (see [Feature requirements by version](#feature-requirements-by-version-or-extension) below). Rather than support a matrix where some officially-listed versions can't run every feature, the floor was raised to match the newest requirement already in the codebase.

!!! note "PostgreSQL versions below 16 — degraded mode, not refused"
    pg-regression-radar does **not** refuse to connect to, or artificially block, a PostgreSQL server older than 16. `internal/collector` detects `server_version_num` at startup and already falls back to the pre-13 `total_time`/`mean_time` `pg_stat_statements` column names when needed (see [Collector Internals](collector-internals.md#pg_stat_statements-column-names-vary-by-postgresql-major-version)), and `internal/planner.CapturePlan` returns `planner.ErrUnsupportedVersion` (logged once at `Info`, not fatal) rather than failing the scrape when the server is older than 16 (see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional)).

    In practice, a server on PostgreSQL 14 or 15 gets full regression detection (collector, correlation engine, alerting) exactly as documented, minus:

    - The optional `--capture-plans` plan-diff hint (`EXPLAIN GENERIC_PLAN`, PostgreSQL 16+ only).
    - Any assumption this matrix's CI coverage says about that specific version — a pre-16 server has never been exercised by `integration-postgres`, only by ad hoc local testing, if at all.

    Versions below 14 are untested and unsupported in any sense — CloudNativePG's own currently-supported releases only ship PostgreSQL 14+ (see [Collector Internals](collector-internals.md#pg_stat_statements-column-names-vary-by-postgresql-major-version)), so there is no realistic deployment target below that line anyway.

## Supported distributions

| Distribution | Images | CI coverage | Status |
|---|---|---|---|
| Community / vanilla PostgreSQL | Official `postgres:16`/`postgres:17`/`postgres:18` images, Docker Hub | Full matrix, every PR | Officially supported |
| [CloudNativePG](https://cloudnative-pg.io/) | Runs community or EDB operand images under its own `Cluster` CRD; referenced throughout this project's docs (see [index](index.md), [Getting Started](getting-started.md), [Installation](installation.md)) as the primary Kubernetes deployment target | Not yet run end-to-end against a real `kind` + CloudNativePG cluster — see the honest gap below | Officially supported as a deployment target |
| EDB Postgres Advanced Server (EPAS) | `docker.enterprisedb.com/k8s/edb-postgres-advanced` (private registry) | Attempted on every PR only when the `EDB_SUBSCRIPTION_TOKEN` repository secret is configured; otherwise skipped — see [CI coverage for EDB distributions](#ci-coverage-for-edb-distributions) | Officially supported; automatic CI verification is conditional |
| EDB Postgres Extended Server (PGE) | `docker.enterprisedb.com/k8s/edb-postgres-extended` (private registry) | Same as EPAS above | Officially supported; automatic CI verification is conditional |

### CloudNativePG: deployment target vs. validated

pg-regression-radar's Helm chart, CRDs, and docs consistently treat CloudNativePG as the reference way to run Postgres on Kubernetes, and nothing in the codebase is CloudNativePG-specific in a way that would make it *not* work (it talks to Postgres over a normal DSN, exactly the same as any other Postgres). What hasn't happened yet is a real, automated proof of that: as tracked in [Roadmap → Known robustness gaps](roadmap.md#known-robustness-gaps), the closest thing to an end-to-end validation today (`.github/workflows/e2e-manual.yml`) runs plain Docker containers, not a `kind` cluster with CloudNativePG actually installed and reconciling a `Cluster`. If that gap has since been closed by another change, [Roadmap](roadmap.md) is the page to check for the current status — update this paragraph to match if so.

### CI coverage for EDB distributions

EDB Postgres Advanced Server and EDB Postgres Extended Server images are **not publicly pullable**: they live behind `docker.enterprisedb.com`, which requires an authenticated EDB subscription (`docker login -u k8s -p <token>`) to pull at all — see [EDB's private registry docs](https://www.enterprisedb.com/docs/postgres_for_kubernetes/latest/private_edb_registries/). GitHub Actions never exposes repository secrets to `pull_request` runs triggered from a fork, so essentially every external contributor's PR runs with no way to authenticate to that registry.

To keep the matrix honest without breaking CI for the common case (no EDB subscription configured), `.github/workflows/ci.yml` has a separate `integration-edb-postgres` job that:

1. Only runs `if` the `EDB_SUBSCRIPTION_TOKEN` repository secret is non-empty — otherwise the job is skipped entirely (shows as skipped in the Actions UI, not failed, and doesn't block merging).
2. Runs with `continue-on-error: true` even when it does run, so a mismatch between this workflow's `docker run` invocation and the real EDB image's entrypoint conventions (which this environment could not verify against the real, credential-gated image) can't turn an otherwise-green PR red.

**Maintainers with an EDB subscription:** configure the `EDB_SUBSCRIPTION_TOKEN` secret under the repository's Settings → Secrets and variables → Actions to enable this job's coverage. Until that's done, EPAS and PGE remain **documented** as officially supported distributions, but are **not verified automatically on every PR** — only community `postgres:16`/`17`/`18` get that automatic, unconditional coverage. This mirrors the same "opt-in when it needs resources not every environment has" pattern `e2e-manual.yml` already uses for its own credential/resource-heavy checks (see [CI/CD](ci-cd.md#e2e-manualyml-manual-only-workflow_dispatch)).

## Feature requirements by version or extension

| Feature | Minimum requirement | Notes |
|---|---|---|
| `pg_stat_statements` | Required, all supported versions | The core data source for every sample the [Collector](collector-internals.md) reads; there is no code path that runs without it. Must be loaded via `shared_preload_libraries` (a server restart, not just `ALTER SYSTEM` + reload — see the comment in `.github/workflows/ci.yml`'s `integration-postgres` job). |
| `pg_stat_statements` column names | PostgreSQL 13+ uses `total_exec_time`/`mean_exec_time`; below that, `total_time`/`mean_time` | Handled automatically — the collector detects `server_version_num` once at startup and picks the right column names (see [Collector Internals](collector-internals.md#pg_stat_statements-column-names-vary-by-postgresql-major-version)). Every version in the officially supported matrix (16-18) uses the modern names; this only matters for the degraded-mode case below 16. |
| `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` | PostgreSQL **16+** | The plan-diff capture source behind the optional `--capture-plans` flag (`internal/planner.CapturePlan`) — see [Detection Algorithm](detection-algorithm.md#plan-diff-correlation-optional). Returns `planner.ErrUnsupportedVersion` below PostgreSQL 16; this is exactly why the matrix floor above is 16. |
| `pg_store_plans` extension | Not yet implemented — **roadmap item**, no code in `internal/planner` depends on it today | Tracked in [Roadmap → Known robustness gaps](roadmap.md#known-robustness-gaps) as a natural follow-up to `GENERIC_PLAN`-based plan-diff: it would capture the real, per-execution plan (not an estimate) with no PostgreSQL 16 requirement of its own. When it lands, expect an extension-version gate (`pg_store_plans` **>= 1.6**, per its own release notes) before trusting its `queryid` column as safe to join against `pg_stat_statements` — this page will be updated with the concrete requirement once that code exists; don't take a dependency on `pg_store_plans` support today. |
| `--state-backend=postgres` (optional durable state) | No extra version requirement beyond the base matrix above | Plain DDL/DML against a `regression_radar` schema — see [Persistence](persistence.md). Works identically across 16/17/18 and, in degraded mode, below 16 too. |

## See also

- [Roadmap](roadmap.md) — the CloudNativePG kind-cluster validation gap and the not-yet-implemented `pg_store_plans` follow-up referenced above, plus everything else still open.
- [Installation](installation.md) and [Getting Started](getting-started.md) — obtaining and running pg-regression-radar against any of the distributions above.
- [Detection Algorithm](detection-algorithm.md) — the full detail on `GENERIC_PLAN`-based plan-diff capture.
- [Collector Internals](collector-internals.md) — `pg_stat_statements` column-name handling across versions.
- [CI/CD](ci-cd.md) — every workflow in this repo, including the `integration-postgres` matrix and `integration-edb-postgres` gated job described above.
