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
see [Testing](https://joao00001.github.io/pg-regression-radar/testing/) in
the docs site for the exact commands. You don't need to run these locally
for a small change; CI will
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
  update the relevant page under [`docs/`](docs/) in the same PR (see
  [`docs/TEMPLATE.md`](docs/TEMPLATE.md) for the structure every page
  follows), plus `README.md` if it affects the quick-start commands there.

## Release notes / changeset fragments

If your PR changes anything a user of this project would notice — a new
feature, a bug fix, a performance improvement — it must also add a **news
fragment**: a small file describing that change in plain, user-facing
language, separate from your commit message (which is technical, aimed at
someone reading `git log`, not release notes). This project uses
[`towncrier`](https://towncrier.readthedocs.io/) to compile these fragments
into `CHANGELOG.md` at release time — the same pattern pytest, Twisted, and
the changesets tooling in the JS ecosystem use, and for the same reason: a
changelog entry written by the person making the change, in the same PR, is
far more accurate than one reconstructed later from commit messages or
squash-merge titles.

### When it's required

Exactly the PR types that describe user-facing behavior:

| PR title type | Fragment required? |
|---|---|
| `feat` | Yes |
| `fix` | Yes |
| `perf` | Yes |
| `docs`, `test`, `refactor`, `chore`, `ci` | No |

This is enforced in CI by `.github/workflows/changelog-fragment-check.yml`,
which reads your PR title's Conventional Commits type (the same type
`pr-title.yml` already validates) to decide whether a fragment is required,
then checks the fragment itself if so.

### How to add one

Add exactly one file to `changelog.d/`, named:

```
<PR-number>.<type>.md
```

where `<type>` is `feat`, `fix`, or `perf` — matching your PR title's type
exactly. For example, PR #123 titled `feat(collector): add per-database
sample retention override` adds `changelog.d/123.feat.md`. You won't know
your PR number until you open the PR (or you can guess it — GitHub issue and
PR numbers share one counter, so the next PR number is usually easy to
predict from the most recent one); the CI check runs on every push to the
PR, so you can add the file first and rename it once you know the real
number if you guessed wrong.

The file's content must be:

1. **Exactly one paragraph** — a single continuous block of text, no blank
   line in the middle.
2. **Start with a capital letter.**
3. **End with `.`, `!`, or `?`.**
4. **No markdown heading** (no line starting with `#`).
5. **No code block** (no `` ``` `` fence, no 4-space/tab-indented block).
6. **Not start by repeating the type** — don't write "Fixed:" or
   "feat(collector):" at the start; the changelog section your fragment
   lands in already says that.
7. **20-400 characters** — a release-note sentence, not an essay. Put extra
   detail in the PR description instead.

A minimal valid example:

```
Added a per-database override for sample retention, so a single noisy
database no longer forces a shorter retention window for the rest of the
fleet.
```

See [`changelog.d/README.md`](changelog.d/README.md) for a second worked
example. `.github/workflows/changelog-fragment-check.yml` fails your PR with
a specific rule number (e.g. `[3-ends-with-punctuation]`) if any of the
above isn't met — it's meant to be self-explanatory from the CI error alone.

### Testing your fragment locally

```bash
pip install towncrier
towncrier build --draft --version v0.0.0-preview
```

`--draft` only prints what the compiled `CHANGELOG.md` section would look
like; it writes nothing and doesn't touch your fragment files.

### How releases consume fragments (repo owner only)

This repo's tags are pushed manually, not cut by a release bot. Before
tagging a release:

```bash
towncrier build --version vX.Y.Z --yes
git add CHANGELOG.md changelog.d/
git commit -s -m "docs(changelog): release vX.Y.Z"
git tag vX.Y.Z
git push origin main vX.Y.Z
```

`towncrier build` rewrites `CHANGELOG.md` with a new `## vX.Y.Z - <date>`
section compiled from every fragment currently in `changelog.d/` (grouped
under "Features"/"Fixes"/"Performance"), and removes the fragments it just
consumed via `git rm`. Commit that change, *then* tag it — `release.yml`'s
`release-notes` job extracts the section matching the pushed tag straight
out of the committed `CHANGELOG.md` and uses it as the GitHub Release body,
so the section must already exist on the tagged commit. See
[CI/CD](docs/ci-cd.md#changelog-fragment-checkyml-on-pr-openeditsync) for the full pipeline.

## Code style

Run `gofmt -l $(git ls-files '*.go')` before pushing — a `gofmt` job in CI
(`go-quality.yml`) fails the build on any unformatted file. There's no
separate style guide beyond that: match the conventions already used in the
file/package you're editing (this codebase leans on doc comments that
explain *why*, not just
what — see any file under `internal/` for the tone to match).

## Questions

Open a [discussion or issue](../../issues) — there's no separate mailing
list or chat for this project yet.
