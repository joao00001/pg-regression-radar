# Branch Protection

*The GitHub repository setting (not a file in this repo) that decides whether the CI checks in [CI/CD](ci-cd.md) actually block anything.*

## Overview

Nothing in this repository can enforce this — it's a GitHub setting only a repo admin can change — which is exactly why it's easy to add a CI check and forget that it isn't actually required anywhere. Without a ruleset or branch protection rule requiring each check by name, every check in [CI/CD](ci-cd.md) (build/test, real-Postgres integration, real-envtest, golangci-lint, gofmt, PR title, DCO) is purely informational: it can fail and a merge can still happen anyway.

GitHub's admin bypass permission is a separate setting from whether a check is *required* — an admin can have bypass and the checks can still be wired up as required, so they run and show pass/fail on every push and PR. The two aren't in tension; this page only covers the required-checks side.

## Where to configure it

GitHub has two mechanisms; use whichever this repo already has active. A push rejected (or, for an admin with bypass, accepted with a `Bypassed rule violations for refs/heads/main` message) mentioning "rule violations" specifically means a **ruleset** is active, not classic branch protection:

- **Rulesets (current):** Settings → Rules → Rulesets → edit (or create) the ruleset targeting `main`.
- **Classic branch protection:** Settings → Branches → Branch protection rules → edit (or add) a rule for `main`.

## Required status checks to add

These are the exact check names as they appear in the Checks tab (each is a job's `name:` field in the corresponding workflow file — see that file if a name ever needs to be cross-checked after an edit):

| Check name | Workflow | Runs on |
|---|---|---|
| `Build & Test` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Integration tests (real PostgreSQL): postgres:16` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Integration tests (real PostgreSQL): postgres:17` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Integration tests (real PostgreSQL): postgres:18` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Controller tests (real kube-apiserver)` | `.github/workflows/ci.yml` | push + PR to `main` |
| `golangci-lint` | `.github/workflows/go-quality.yml` | push + PR to `main` |
| `gofmt` | `.github/workflows/go-quality.yml` | push + PR to `main` |
| `Enforce Conventional Commits title` | `.github/workflows/pr-title.yml` | PR to `main` |
| `Check sign-off (DCO)` | `.github/workflows/dco.yml` | PR to `main` |

`Integration tests (real PostgreSQL)` is now a `postgres-version: [16, 17, 18]` matrix (see [Support Matrix](support-matrix.md#officially-supported-postgresql-versions)), so it shows up as three separate checks above instead of one — require all three, not just one, or a regression against 17/18 only could still merge.

`Integration tests (EDB Postgres): EDB Postgres Advanced Server (EPAS)` / `... Extended Server (PGE)` (also in `ci.yml`) are deliberately **not** in this list: that job is entirely skipped unless the `EDB_SUBSCRIPTION_TOKEN` repository secret is configured (see [Support Matrix](support-matrix.md#ci-coverage-for-edb-distributions)) and runs with `continue-on-error: true` even when it does run, so making it a required check would either block every fork PR (secret never present there) or silently pass on real failures — neither is what "required" should mean.

`Manual E2E (real container)` (`e2e-manual.yml`) is deliberately **not** in this list — it's `workflow_dispatch`-only, so it never runs against a PR and can't be a required check.

## Other settings worth enabling alongside the checks

- **Require a pull request before merging** — confirm it's on and scoped to `main`.
- **Require branches to be up to date before merging** — otherwise a status check can pass against a stale base and still land a broken merge.
- **Keep bypass scoped to repo admins only** (not "everyone", not a public team) — bypass shouldn't be wider than "people who genuinely need to override CI".
- **Do not allow force pushes / Do not allow deletions** — protects the commit history CI has already validated.
- **Require approvals** — GitHub doesn't count a self-review as an approval, so this only makes sense once there's more than one maintainer or regular external contributors; turn it on (1 approval) at that point, per [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md)'s squash-merge policy.

## See also

- [CI/CD](ci-cd.md) — every check referenced in the table above.
- [Roadmap](roadmap.md) — this is tracked there as an open operational follow-up.
