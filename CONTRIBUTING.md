# Contributing to pg-regression-radar

Thanks for considering a contribution. This document covers everything you
need to open a pull request that's easy to review and easy to merge.

By participating in this project you agree to follow the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Before you start

For anything beyond a small fix (typo, docs, a bounded bug fix), please open
an issue first to discuss the approach. It saves both of us time if a PR
turns out to need a different design than what you had in mind.

## Development setup

```bash
go build ./...      # build all binaries
go vet ./...         # static analysis
go test ./...        # unit tests (fakes/mocks only, no external services needed)
```

Some suites need real infrastructure (a live PostgreSQL, or a real
kube-apiserver) and are gated behind a build tag / `-run` filter so they
don't run by default. They mirror the two extra jobs CI runs on every PR —
see the [README's testing section](README.md#contributing) for the exact
commands. You don't need to run these locally for a small change; CI will
run them on your PR either way, but if you're touching `internal/collector`,
`internal/storage`, `internal/controller`, or `internal/e2e`, running the
relevant one locally before pushing will save you a slow CI feedback loop.

## Branch naming

`<type>/<short-description>`, matching the commit type below, e.g.:

- `feat/argo-rollouts-cluster-field`
- `fix/collector-memory-leak`
- `docs/contributing-guide`

## Commit messages: Conventional Commits

Every commit message (and PR title — see below) must follow
[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short, imperative summary>

<optional body: what changed and, more importantly, why>
```

**Types:**

| Type | Use for |
|---|---|
| `feat` | A new feature or capability |
| `fix` | A bug fix |
| `docs` | Documentation only |
| `test` | Adding or fixing tests, no production code change |
| `refactor` | Code change that neither fixes a bug nor adds a feature |
| `perf` | A change that specifically improves performance |
| `chore` | Build process, dependency bumps, tooling — no source impact |
| `ci` | Changes to GitHub Actions workflows |

**Scopes** (optional, but encouraged — this repo has clear component
boundaries, so use them):

`collector`, `correlation`, `ingester`, `alerting`, `storage`, `controller`,
`api`, `operator`, `manager`, `helm`, `e2e`, `ci`, `deps`

Examples:

```
feat(controller): add leader election to cmd/manager
fix(collector): bound sample retention to avoid unbounded memory growth
test(e2e): add full pipeline regression-detection integration test
docs(readme): document the postgres persistence backend
```

Why bother: this project squash-merges PRs (see below), so your PR title
*becomes* the permanent commit message on `main`. A consistent format also
enables automatic changelog generation later, and it's the single fastest
way for someone reading `git log` six months from now to find every change
to a given component.

## Pull requests

- **PR title must also follow Conventional Commits** — a CI check
  (`pr-title.yml`) enforces this and will fail the PR otherwise. If your PR
  has multiple commits with different types, pick the type that best
  describes the PR as a whole; you don't need every individual commit to
  be independently well-formed, since squash-merge collapses them anyway.
- **Squash merge only.** This keeps `main` as one clean, Conventional
  Commits-formatted commit per PR instead of a pile of "wip" / "fix typo"
  commits. Feel free to commit as messily as you want while iterating —
  only the PR title matters for the final history.
- **Sign off your commits (DCO).** Every commit must include a
  `Signed-off-by` trailer certifying you wrote the change or otherwise have
  the right to submit it under this project's license (the
  [Developer Certificate of Origin](https://developercertificate.org/)).
  The easiest way is to always commit with `-s`:

  ```bash
  git commit -s -m "fix(collector): bound sample retention"
  ```

  Forgot to sign off? Fix your last commit with:

  ```bash
  git commit --amend -s --no-edit
  git push --force-with-lease
  ```

  A CI check (`dco.yml`) verifies every commit in the PR is signed off.
- **Keep PRs focused.** One logical change per PR. A PR that fixes a bug
  and refactors an unrelated function is two PRs.
- **Tests.** New behavior needs a test; bug fixes should include a test
  that fails without the fix. `go build ./... && go vet ./... && go test
  ./...` must pass — CI will run this plus the real-infrastructure suites
  automatically.
- **Docs.** If you change a flag, a CRD field, or user-facing behavior,
  update the relevant section of `README.md` in the same PR.

## Code style

Run `gofmt -l .` before pushing — CI doesn't currently fail on formatting,
but a clean `gofmt` is expected. There's no separate style guide beyond
that: match the conventions already used in the file/package you're
editing (this codebase leans on doc comments that explain *why*, not just
what — see any file under `internal/` for the tone to match).

## Questions

Open a [discussion or issue](../../issues) — there's no separate mailing
list or chat for this project yet.
