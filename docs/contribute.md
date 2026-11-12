---
title: Contribute
hide:
  - navigation
  - toc
---

<div class="pgrr-home pgrr-contribute" markdown="1">

<header class="ct-head" markdown="1">
<h1>Build it <em>with us</em></h1>
<p class="ct-lead" markdown="1">pg-regression-radar is open source and small enough to read in an afternoon. Break it on your own Postgres, fix a typo, review a pull request or take on a real issue. All of it counts, and you don't need anyone's permission to start.</p>
<p class="ct-actions" markdown="1">[Find a first issue](https://github.com/joao00001/pg-regression-radar/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22){ .md-button .md-button--primary }
[Join the Discord]({{ discord_url }}){ .md-button }
[Read the guide](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md){ .md-button }</p>
</header>

<svg class="ct-sprite" aria-hidden="true" focusable="false" xmlns="http://www.w3.org/2000/svg"><symbol id="ct-i-run" viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M7.5 9.5 10.5 12l-3 2.5M13 15h3.5"/></symbol><symbol id="ct-i-docs" viewBox="0 0 24 24"><path d="M6 3.5h8.5L19 8v12.5H6z"/><path d="M14 3.5V8.5h5M9 13h7M9 16.5h5"/></symbol><symbol id="ct-i-issue" viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="1.6"/></symbol><symbol id="ct-i-review" viewBox="0 0 24 24"><circle cx="7" cy="6.5" r="2.3"/><circle cx="7" cy="17.5" r="2.3"/><circle cx="17" cy="17.5" r="2.3"/><path d="M7 8.8v6.4M17 15.2v-4.7a3 3 0 0 0-3-3h-2.5M13.8 4.8l-2.3 2.7 2.3 2.7"/></symbol><symbol id="ct-i-idea" viewBox="0 0 24 24"><path d="M9.2 17.5h5.6M10 20.5h4M12 3.5a6 6 0 0 0-3.6 10.8c.7.6 1.1 1.3 1.1 2.2h5c0-.9.4-1.6 1.1-2.2A6 6 0 0 0 12 3.5z"/></symbol><symbol id="ct-i-talk" viewBox="0 0 24 24"><path d="M4.5 5.5h15v10h-7.2L8 19.5v-4H4.5z"/><path d="M8.5 9.5h7M8.5 12.5h4.5"/></symbol></svg>

## Ways to help

<div class="ct-ways ct-reveal">
<a class="ct-way" href="https://github.com/joao00001/pg-regression-radar/issues/new?template=bug_report.yml"><span class="ct-tag">No code needed</span><svg class="ct-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#ct-i-run"/></svg><span class="ct-way-title">Run it on your Postgres</span><span class="ct-way-desc">Point it at a real database and tell us what breaks, what confuses you, or what's missing.</span><span class="ct-cta">Report a bug &rarr;</span></a>
<a class="ct-way" href="https://github.com/joao00001/pg-regression-radar/tree/main/docs"><span class="ct-tag">No code needed</span><svg class="ct-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#ct-i-docs"/></svg><span class="ct-way-title">Fix or write docs</span><span class="ct-way-desc">A typo, an unclear step, a missing example. Docs changes are the easiest first pull request.</span><span class="ct-cta">Open the docs folder &rarr;</span></a>
<a class="ct-way" href="https://github.com/joao00001/pg-regression-radar/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22"><span class="ct-tag">Code</span><svg class="ct-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#ct-i-issue"/></svg><span class="ct-way-title">Take a good first issue</span><span class="ct-way-desc">Scoped for newcomers, with context and a place to start. Say in a comment that you're on it.</span><span class="ct-cta">See the issues &rarr;</span></a>
<a class="ct-way" href="https://github.com/joao00001/pg-regression-radar/pulls"><span class="ct-tag">Review</span><svg class="ct-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#ct-i-review"/></svg><span class="ct-way-title">Review a pull request</span><span class="ct-way-desc">Read a diff, run it, say what you found. A fresh pair of eyes is one of the most useful things you can give.</span><span class="ct-cta">Open pull requests &rarr;</span></a>
<a class="ct-way" href="https://github.com/joao00001/pg-regression-radar/issues/new?template=feature_request.yml"><span class="ct-tag">Idea</span><svg class="ct-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#ct-i-idea"/></svg><span class="ct-way-title">Propose a feature</span><span class="ct-way-desc">Have a case the radar misses? Describe the query or the deploy that slipped through.</span><span class="ct-cta">Request a feature &rarr;</span></a>
<a class="ct-way" href="https://github.com/joao00001/pg-regression-radar/discussions"><span class="ct-tag">Community</span><svg class="ct-icon" viewBox="0 0 24 24" aria-hidden="true"><use href="#ct-i-talk"/></svg><span class="ct-way-title">Help someone get unstuck</span><span class="ct-way-desc">Answer a question, or share how you run it. Setups from real clusters teach us the most.</span><span class="ct-cta">Join the discussions &rarr;</span></a>
</div>

## Your first pull request

<div class="ct-steps ct-reveal" markdown="1">

<div class="ct-step" markdown="1">
<div class="ct-num">01</div>
<div class="ct-step-title">Fork and clone</div>
<div class="ct-step-desc">Fork the repository on GitHub, then clone your copy.</div>

```bash
git clone https://github.com/<you>/pg-regression-radar
cd pg-regression-radar
```
</div>

<div class="ct-step" markdown="1">
<div class="ct-num">02</div>
<div class="ct-step-title">Check it builds</div>
<div class="ct-step-desc">The unit tests need no database or cluster.</div>

```bash
go build ./... && go vet ./... && go test ./...
```
</div>

<div class="ct-step" markdown="1">
<div class="ct-num">03</div>
<div class="ct-step-title">Branch and commit</div>
<div class="ct-step-desc">Conventional Commits, signed off with <code>-s</code>.</div>

```bash
git switch -c fix/short-description
git commit -s -m "fix(collector): short summary"
```
</div>

<div class="ct-step" markdown="1">
<div class="ct-num">04</div>
<div class="ct-step-title">Open the pull request</div>
<div class="ct-step-desc">Push the branch and open the PR. CI checks the title, the sign-off, formatting and tests, and a maintainer takes it from there.</div>

```bash
git push -u origin fix/short-description
```
</div>

</div>

<div class="ct-note ct-reveal" markdown="1">
<p markdown="1">**A small fix?** Just open the pull request. **Something bigger?** Open an issue first so we agree on the approach before you write it. The [full guide](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md) covers branch names, tests, docs and release notes, and our [testing](testing.md) and [CI/CD](ci-cd.md) pages show what runs on your PR.</p>
</div>

## What to expect

<div class="ct-expect ct-reveal" markdown="1">

<div class="ct-cell" markdown="1">
<div class="ct-cell-title">Talk first for big changes</div>
<div class="ct-cell-desc">Typos and bounded fixes can go straight to a pull request. Anything larger starts as an issue, which saves you from writing code that needs a different design.</div>
</div>

<div class="ct-cell" markdown="1">
<div class="ct-cell-title">CI does the checking</div>
<div class="ct-cell-desc">Formatting, vet, tests, sign-off and the PR title run on every pull request. From a fork, a maintainer may need to approve the first run and add a label for the slower integration suites.</div>
</div>

<div class="ct-cell" markdown="1">
<div class="ct-cell-title">Messy commits are fine</div>
<div class="ct-cell-desc">Pull requests are squash-merged, so only the PR title ends up in history. Commit as often as you like while you iterate.</div>
</div>

</div>

## Come say hi

<div class="ct-talk ct-reveal">
<a class="ct-chat ct-chat--discord" href="{{ discord_url }}"><span class="ct-tag">Chat</span><span class="ct-chat-title">{{ discord_name }} on Discord</span><span class="ct-chat-desc">Ask a question, show your setup, or get unblocked while you work on a pull request. No question is too basic.</span><span class="ct-cta">Join the server &rarr;</span></a>
<a class="ct-chat" href="https://github.com/joao00001/pg-regression-radar/discussions"><span class="ct-tag">Forum</span><span class="ct-chat-title">GitHub Discussions</span><span class="ct-chat-desc">Longer questions and ideas, written down where anyone can search for them later.</span><span class="ct-cta">Browse discussions &rarr;</span></a>
</div>

## Contributors

<div class="ct-people-wrap ct-reveal">
<p class="ct-people-note"><span class="ct-people-count" aria-live="polite"></span><span class="ct-people-text">Everyone who has shipped code to the project.</span></p>
<div class="ct-people" data-repo="joao00001/pg-regression-radar" data-state="loading">
<a class="ct-you" href="https://github.com/joao00001/pg-regression-radar/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22" aria-label="Become a contributor: pick a good first issue"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 6v12M6 12h12"/></svg></a>
</div>
<p class="ct-people-fallback" hidden>Couldn't load the list right now. <a href="https://github.com/joao00001/pg-regression-radar/graphs/contributors">See everyone on GitHub</a>.</p>
</div>

<p class="pgrr-foot" markdown="1">Everyone taking part follows the [Code of Conduct](https://github.com/joao00001/pg-regression-radar/blob/main/CODE_OF_CONDUCT.md).</p>

</div>
