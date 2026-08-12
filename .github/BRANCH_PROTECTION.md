# Branch protection checklist (maintainer-only)

This isn't enforced by any file in this repo — it's a GitHub repository
setting only a repo admin can change, so it's documented here instead of
automated. Without it, every CI check added so far (build/test, real-Postgres
integration, real-envtest, golangci-lint, gofmt, PR title, DCO) is purely
informational: it can fail and a merge can still happen anyway.

## Why this file exists

A push to `main` on 2026-08-11 was accepted with:

```
remote: Bypassed rule violations for refs/heads/main:
remote:
remote: - Changes must be made through a pull request.
```

That message means a **ruleset** (GitHub's newer rule mechanism, distinct
from classic branch protection) already exists on `main` requiring PRs, but
the account pushing had bypass permission, so it didn't actually block
anything. Two separate things need to be true for the checks below to have
real effect:

1. The ruleset/branch protection rule requires each check by name.
2. Nobody — including repo admins — has bypass permission on it (or bypass
   is at least deliberately scoped, not a default nobody remembered to turn
   off).

## Where to configure it

GitHub has two mechanisms; use whichever this repo already has active
(the bypass message above indicates it's currently a **ruleset**):

- **Rulesets (current):** Settings → Rules → Rulesets → edit (or create) the
  ruleset targeting `main`.
- **Classic branch protection:** Settings → Branches → Branch protection
  rules → edit (or add) a rule for `main`.

## Required status checks to add

These are the exact check names as they appear in the Checks tab (each is a
job's `name:` field in the corresponding workflow file — see that file if a
name ever needs to be cross-checked after an edit):

| Check name | Workflow | Runs on |
|---|---|---|
| `Build & Test` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Integration tests (real PostgreSQL)` | `.github/workflows/ci.yml` | push + PR to `main` |
| `Controller tests (real kube-apiserver)` | `.github/workflows/ci.yml` | push + PR to `main` |
| `golangci-lint` | `.github/workflows/go-quality.yml` | push + PR to `main` |
| `gofmt` | `.github/workflows/go-quality.yml` | push + PR to `main` |
| `Enforce Conventional Commits title` | `.github/workflows/pr-title.yml` | PR to `main` |
| `Check sign-off (DCO)` | `.github/workflows/dco.yml` | PR to `main` |

`Manual E2E (real container)` (`e2e-manual.yml`) is deliberately **not** in
this list — it's `workflow_dispatch`-only, so it never runs against a PR and
can't be a required check.

## Other settings worth enabling alongside the checks

- **Require a pull request before merging** — already implied by the
  bypass message above; just confirm it's on and scoped to `main`.
- **Require branches to be up to date before merging** — otherwise a status
  check can pass against a stale base and still land a broken merge.
- **Restrict who can bypass this rule** — set to nobody (or a very small,
  deliberate list), so the bypass seen above is a conscious exception, not a
  default. Solo-maintainer repos still benefit: it stops an accidental
  `git push origin main` from skipping every check above.
- **Do not allow force pushes / Do not allow deletions** — protects the
  commit history CI has already validated.
- **Require approvals** — optional for a solo-maintainer repo today; worth
  turning on (1 approval) once there's a second maintainer or regular
  external contributors, per `CONTRIBUTING.md`'s squash-merge policy.
