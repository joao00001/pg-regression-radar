<!--
PR title must follow Conventional Commits — e.g. "fix(collector): bound sample retention".
See CONTRIBUTING.md for the full type/scope list. A CI check enforces this.
-->

## What does this PR do?

<!-- One or two sentences. What changed, and why. -->

## Related issue

<!-- Closes #123, or "n/a" for small/obvious changes. -->

## How was this tested?

<!-- e.g. "go test ./...", "ran the collector integration test against a
local Postgres", "manually verified against a kind cluster". Be specific —
"tests pass" alone doesn't tell a reviewer what's actually been exercised. -->

## Checklist

- [ ] PR title follows [Conventional Commits](CONTRIBUTING.md#commit-messages-conventional-commits) (`type(scope): summary`)
- [ ] Commits are signed off (`git commit -s`) — see [DCO](CONTRIBUTING.md#pull-requests)
- [ ] `go build ./... && go vet ./... && go test ./...` pass locally
- [ ] Added/updated tests for the behavior this PR changes
- [ ] Updated `README.md` (or another doc) if this changes user-facing behavior, a flag, or a CRD field
- [ ] This PR is scoped to one logical change
