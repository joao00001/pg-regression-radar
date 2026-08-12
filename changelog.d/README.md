# changelog.d/

News fragments for [`towncrier`](https://towncrier.readthedocs.io/), consumed
by `towncrier build` to generate `CHANGELOG.md` at release time. See
[CONTRIBUTING.md's "Release notes / changeset fragments"
section](../CONTRIBUTING.md#release-notes--changeset-fragments) for the full
rules — this file is just a quick, non-normative example.

This file itself is not a fragment: `towncrier` ignores any file named
`README.md` in this directory automatically (along with `.gitignore`,
`.gitkeep`, and its own template), so it's safe to keep this explanation
here between releases without it ever ending up in the generated changelog.

## What a real fragment looks like

A PR titled `feat(collector): add per-database sample retention override`
and numbered, say, `#123` would add exactly one file,
`changelog.d/123.feat.md`, containing exactly this and nothing else:

```
Added a per-database override for sample retention, so a single noisy
database no longer forces a shorter retention window for the rest of the
fleet.
```

That's it — one continuous paragraph, capitalized, ending in `.`/`!`/`?`,
20-400 characters, no heading, no code block, and not repeating the `feat:`
type prefix (the changelog's "Features" section already says that). The
filename's `feat` must match the PR title's Conventional Commits type, and
`123` must be the PR's own number.

## Why there's no example fragment sitting in this directory

Every change already shipped up to and including this repository's most
recent commits at the time this fragment system was introduced predates the
requirement, so there is nothing genuinely "unreleased" to backfill a
fragment for — and this very change (introducing the fragment tooling) is a
`ci`-type PR, which is explicitly exempt from needing one (see
`.github/workflows/changelog-fragment-check.yml`). The next PR titled
`feat`, `fix`, or `perf` is the first one that will actually add a real file
here.
