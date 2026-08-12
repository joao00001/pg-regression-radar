# Branch Protection

*The GitHub repository setting (not a file in this repo) that decides whether the CI checks in [CI/CD](ci-cd.md) actually block anything.*

## Overview

Nothing in this repository can enforce this — it's a GitHub setting only a repo admin can change — which is exactly why it's easy to add a CI check and forget that it isn't actually required anywhere. Without a ruleset or branch protection rule requiring each check by name, every check in [CI/CD](ci-cd.md) (build/test, real-Postgres integration, real-envtest, golangci-lint, gofmt, PR title, DCO) is purely informational: it can fail and a merge can still happen anyway.

## Why this page exists

A push to `main` on 2026-08-11 was accepted with:

```
remote: Bypassed rule violations for refs/heads/main:
remote:
remote: - Changes must be made through a pull request.
```

That message means a **ruleset** (GitHub's newer rule mechanism, distinct from classic branch protection) already exists on `main` requiring PRs, but the account pushing had bypass permission, so it didn't actually block anything.

For a solo-maintainer repo (this one), bypass for repo admins is correct, not a gap to close: without it, the maintainer couldn't merge their own PR (GitHub won't count a self-review as a required approval) or push a hotfix when there's nobody else to review it. Removing bypass entirely would lock the one person who can approve changes out of approving them. The actual gap is narrower than "bypass exists": the checks below need to be **required** on the ruleset so they still run and show pass/fail on every push and PR, even though the maintainer retains the ability to override them when they judge it necessary. Bypass being available for deliberate use is fine; the checks silently not being wired up as required at all is the thing worth fixing.

## Where to configure it

GitHub has two mechanisms; use whichever this repo already has active (the bypass message above indicates it's currently a **ruleset**):

- **Rulesets (current):** Settings → Rules → Rulesets → edit (or create) the ruleset targeting `main`.
- **Classic branch protection:** Settings → Branches → Branch protection rules → edit (or add) a rule for `main`.

## Required status checks to add

These are the exact check names as they appear in the Checks tab (each is a job's `name:` field in the corresponding workflow file — see that file if a name ever needs to be cross-checked after an edit):

| Check name | Workflow | Runs on |
|---|---|---|
| `Build & Test` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Integration tests (real PostgreSQL)` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Controller tests (real kube-apiserver)` | `.github/workflows/ci.yml` | push + PR to `main` |
| `golangci-lint` | `.github/workflows/go-quality.yml` | push + PR to `main` |
| `gofmt` | `.github/workflows/go-quality.yml` | push + PR to `main` |
| `Enforce Conventional Commits title` | `.github/workflows/pr-title.yml` | PR to `main` |
| `Check sign-off (DCO)` | `.github/workflows/dco.yml` | PR to `main` |

`Manual E2E (real container)` (`e2e-manual.yml`) is deliberately **not** in this list — it's `workflow_dispatch`-only, so it never runs against a PR and can't be a required check.

## Other settings worth enabling alongside the checks

- **Require a pull request before merging** — already implied by the bypass message above; just confirm it's on and scoped to `main`.
- **Require branches to be up to date before merging** — otherwise a status check can pass against a stale base and still land a broken merge.
- **Keep bypass scoped to repo admins only** (not "everyone", not a public team) — as the sole maintainer today, that's just you, which is the correct and necessary setup. Revisit this list if/when co-maintainers join, so bypass doesn't quietly stay wider than "people who genuinely need to override CI".
- **Do not allow force pushes / Do not allow deletions** — protects the commit history CI has already validated.
- **Require approvals** — leave off for a solo-maintainer repo (GitHub doesn't let you approve your own PR, so this would just block merges); turn on (1 approval) once there's a second maintainer or regular external contributors, per [CONTRIBUTING.md](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md)'s squash-merge policy.

## See also

- [CI/CD](ci-cd.md) — every check referenced in the table above.
- [Roadmap](roadmap.md) — this is tracked there as an open operational follow-up.
