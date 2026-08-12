# Page Template

*This page defines the structure every other page in this docs site follows. It isn't linked from anywhere except the site's own "Meta" nav section — copy its shape when adding a new page, don't link to it from content pages.*

## Why a single template

Before this docs site existed, project documentation was scattered across `README.md` (grown well past what a landing page should hold), scattered code comments, and one-off files like `CONTRIBUTING.md` and `.github/BRANCH_PROTECTION.md`, each with its own structure and level of detail. A single template keeps every page predictable to both read and write: anyone opening a new page already knows where the summary is, where the real content starts, and where to look for related reading.

## The structure

Every page in `docs/` (except this one and `index.md`, which is deliberately a landing page) follows this shape:

```markdown
# Page Title

*One-sentence summary of what this page covers and who it's for, in italics, directly under the title.*

## Overview

A short paragraph (2-4 sentences) of context: what this page covers and why
it matters, before diving into specifics.

## <First real section>

Content. Use H2 (`##`) for top-level sections within the page, H3 (`###`)
for subsections. Prefer tables for reference material (flags, fields,
defaults); prefer prose for anything that needs to explain *why*, not just
*what*.

## See also

- [Related Page](relative-link.md) — one line on why it's relevant.
```

## Conventions within that structure

- **The italic summary line is mandatory and must fit on one line.** It's what a reader scanning the nav decides "is this the page I want" from — if it needs two sentences, the page is probably trying to cover too much and should split.
- **"Overview" is always the first H2.** Even a page that's mostly a reference table (like [Configuration Reference](configuration.md)) gets 2-4 sentences of "what this is and when you'd look at it" before the table starts.
- **Code blocks are fenced with a language tag** (` ```bash `, ` ```go `, ` ```yaml `, ` ```json `) so syntax highlighting and the copy button both work.
- **Internal links are relative** (`[Detection Algorithm](detection-algorithm.md)`), not absolute GitHub URLs — they need to work both on the rendered site and when reading the raw Markdown in the repo.
- **"See also" is optional but encouraged**, and only lists pages, not external references — external links belong inline, in context, next to the claim they support.
- **Reference tables document defaults, not just types.** A flag/field table always has a "Default" column, even when the default is "*(required)*" or empty string — an absent default is itself information.
