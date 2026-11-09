# Backporting

*How a merged fix gets applied to older, still-maintained tool versions — the `backport <branch>` label, `release-X.Y` maintenance branches, and what happens when a cherry-pick conflicts.*

## Overview

Backporting is how a fix that lands on `main` also reaches a tool version that's already shipped and still supported — the maintainer decides *which* versions a merged pull request should affect, by applying one `backport release-X.Y` label per affected line. Everything from there — cherry-picking the change, opening a pull request per target branch, and recovering when a cherry-pick doesn't apply cleanly — is automated by [`korthout/backport-action`](https://github.com/korthout/backport-action) (`.github/workflows/backport.yml`), not hand-rolled scripting. This page covers the full lifecycle: how a `release-X.Y` branch comes to exist, the label convention, what a conflict looks like and how to resolve it, and how a maintenance branch cuts its own patch releases afterwards.

## How to backport a merged pull request

Apply a `backport release-X.Y` label to the pull request — before or after it merges. One label per tool version line the fix should also reach, e.g. both `backport release-1.5` and `backport release-1.6` to backport to two supported lines from the same PR. Once the PR is merged, `backport.yml` takes over automatically:

1. It confirms every `backport <branch>` label actually names an existing `release-X.Y` maintenance branch — a typo, or a label naming a line that's never had a branch created, fails loudly with a comment on the original PR rather than silently doing nothing or cherry-picking to the wrong place.
2. For each valid target, it cherry-picks the pull request's squash-merge commit (see [CONTRIBUTING.md: Squash merge only](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#pull-requests) — there's always exactly one commit to pick) onto a new branch off `release-X.Y`, using [`-x`](https://git-scm.com/docs/git-cherry-pick#Documentation/git-cherry-pick.txt--x) so the resulting commit records which original commit it came from.
3. It opens a pull request per target branch, titled `[Backport release-X.Y] <original title>`, labeled `backport`, with the original author added as an assignee.
4. It comments back on the original pull request with the result.

Only a maintainer applies these labels — a contributor can request a backport in the PR description, but deciding which shipped versions are affected (and therefore need the fix) is a judgment call, not something to automate from the PR's own content.

## Maintenance branches: `release-X.Y`

A `release-X.Y` branch (e.g. `release-1.6`) is the only valid backport target, and is **the** branch that line's own patch releases (`v1.6.1`, `v1.6.2`, ...) get cut from afterwards — `main` keeps moving forward (`v1.7.0`, `v2.0.0`, ...) and never looks at that branch again.

These branches aren't created or maintained by hand. `.github/workflows/release-branches.yml` watches the exact same `push: tags: v*` event `release.yml` does (see [below](#the-release-job-is-unaffected) for why that doesn't put `release.yml` itself at any risk) and, whenever the pushed tag is the *first* release of a minor/major line (patch `0`, e.g. `v1.6.0`, not `v1.6.1`), it:

- creates `release-X.Y` pointed at that tag's commit, if it doesn't already exist, and
- creates the matching `backport release-X.Y` label, if it doesn't already exist.

A patch tag (`v1.6.1`) does nothing here — its `release-1.6` branch is assumed to already exist from when `v1.6.0` shipped. A pre-release tag (`v2.0.0-rc1`, same convention [`docs.yml`](ci-cd.md#docsyml-two-independent-jobs-check-every-prpush-and-deploy-only-on-a-release-tag) uses to decide what becomes the `latest` docs alias) never gets a maintenance branch either — only a real `vX.Y.0` does.

In practice, this means every minor/major line this project has ever shipped becomes backport-eligible automatically the moment it ships, with zero manual setup — the label simply doesn't exist yet for a line nobody has asked to maintain further, and starts existing the instant someone does.

## Resolving a backport conflict

Not every cherry-pick applies cleanly — a maintenance branch can diverge from `main` in ways that create a real textual conflict. `backport.yml` sets `experimental.conflict_resolution: draft_commit_conflicts` (rather than the action's default, which is just to fail), so a conflict never dead-ends as a failed Actions run with nothing to act on:

1. The action commits the conflict as-is (conflict markers included) and opens a **draft** pull request against the target `release-X.Y` branch.
2. It comments on the original pull request with a link to that draft and instructions to continue.
3. To resolve it:

   ```bash
   git fetch origin <the backport branch the action pushed>
   git checkout <that branch>
   # resolve the conflict markers in the affected file(s)
   git add <resolved files>
   git commit  # no --amend - the conflicted commit stays as the audit trail of what was attempted
   git push
   ```

4. Once the branch builds and passes CI (see [CI on backport pull requests](#ci-on-backport-pull-requests) below), mark the draft ready for review and merge it like any other pull request.

Auto-merge is never enabled on a draft created this way (GitHub doesn't allow auto-merge on drafts), so a conflicting backport always gets a human's eyes before it merges, by construction.

## Cutting a patch release from a maintenance branch

Once one or more backport pull requests merge into `release-X.Y`, that branch accumulates pending `changelog.d/` fragments exactly like `main` does — each backported commit carries its original fragment file along with it (the cherry-pick reapplies that file's addition, unchanged). `release-prep.yml` now also runs on `release-X.Y` branches (not just `main`), and opens a `ci: prepare release vX.Y.(Z+1)` pull request the same way it does for `main`, with one important difference: the next version it computes is **relative to that branch's own tag history** (`git describe`, not a repo-wide "highest tag" search), so `release-1.6` always proposes `v1.6.(Z+1)` regardless of how far `main` has moved on. It also refuses to compute a version at all if that branch's name and its own latest reachable tag ever disagree (e.g. `release-1.6` whose history doesn't actually contain a `v1.6.x` tag) — a configuration problem on a maintenance branch should fail loudly, not quietly produce a wrong version.

From there, releasing a patch version works exactly like releasing from `main`: review and merge the release-prep PR, then

```bash
git checkout release-1.6
git pull
git tag v1.6.1
git push origin v1.6.1
```

### The release job is unaffected

Pushing `v1.6.1` from `release-1.6` triggers `release.yml` exactly the same way pushing `v1.7.0` from `main` does — `release.yml`'s trigger (`push: tags: v*`) and every one of its jobs (`images`, `manifests`, `helm-chart`, `release-notes`) already only cares about the pushed tag and the commit it points at, never which branch it was pushed from. Nothing in this feature touches `release.yml`'s own file or job graph at all: `release-branches.yml` and the `release-prep.yml`/`ci.yml`/`go-quality.yml`/`pr-title.yml` changes described on this page are each independent workflow files or additive trigger scopes, with no `needs:`/shared-job relationship to `release.yml`. See [CI/CD](ci-cd.md#releaseyml-on-pushing-a-v-tag) for the full breakdown of what that workflow does.

## CI on backport pull requests

A backport pull request goes through the same quality bar as any other, with two deliberate exceptions:

| Check | Runs on backport PRs? | Why |
|---|---|---|
| `ci.yml` (build/test/integration), `go-quality.yml` (lint/fmt/vuln) | **Yes** | A cherry-pick that applies cleanly *textually* can still fail to build or pass tests against a branch that has otherwise diverged — these need to gate a merge into `release-X.Y` exactly as they would into `main`. |
| `pr-title.yml` (Conventional Commits title) | **No** | The backport PR's title is machine-generated (`[Backport release-X.Y] <original title>`), which isn't itself a Conventional Commits type — and the original title already passed this check once, on `main`, before it was ever eligible to be backported. |
| `changelog-fragment-check.yml` (news fragment required) | **No** | The fragment was already validated on the original PR and travels with the cherry-picked commit unchanged — requiring a second one here would be redundant, and would break on the same title-prefix mismatch as `pr-title.yml`. |
| `dco.yml` (sign-off) | **No** (scoped to `main` only, unchanged) | The original commit was already signed off; this workflow's scope wasn't widened for backporting. |

If resolving a conflict substantially changes the fix for that specific version (not just a mechanical conflict-marker resolution), nothing stops a reviewer from manually adding or adjusting a `changelog.d/` fragment on the backport PR — it just isn't enforced by CI the way it is on `main`, since that's a judgment call about how different the backported fix really is.

## Safety and governance notes

A few things were deliberately built in rather than left to convention:

- **One source of truth for targets.** `backport.yml`'s `validate-targets` job is the only place target branches are computed from labels — it disables the backport-action's own independent `label_pattern` matching (`label_pattern: ""`) and passes its already-validated list via `target_branches` instead, so there's no second mechanism that could ever disagree with the first.
- **Existence is checked, not assumed.** A `backport <branch>` label that doesn't resolve to a real branch fails the run with a comment explaining exactly which label is wrong, instead of the backport-action failing deeper into its own run with a less actionable git-level error.
- **Maintenance branches only ever bump their own patch version.** `release-prep.yml`'s version-bump logic takes a different path on a `release-X.Y` ref than on `main`: it always increments the patch number there, even if a stray `*.feat.md` fragment somehow ended up on that branch, because a maintenance line's entire purpose is patch releases.
- **Concurrency is scoped correctly in both new workflows.** `backport.yml` groups by the originating PR number (so re-triggers for the same PR queue instead of racing); `release-prep.yml` now groups by `github.ref_name` (so a `release-1.6` prep PR and a `main` prep PR never share — or silently clobber — the same branch/PR).

## See also

- [CI/CD](ci-cd.md) — every workflow referenced on this page, including `backport.yml` and `release-branches.yml`'s own entries.
- [Branch Protection](branch-protection.md) — whether `release-X.Y` branches should carry the same required checks as `main`.
- [Contributing Guide](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md#backporting-a-merged-pull-request) — the contributor-facing summary of this page.
