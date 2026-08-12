#!/usr/bin/env python3
"""Validate a towncrier changelog fragment's content against this repo's
strict news-fragment format.

Used by .github/workflows/changelog-fragment-check.yml; see
CONTRIBUTING.md's "Release notes / changeset fragments" section for the
full, human-readable rules this script enforces. Kept as a small stdlib-only
script (no dependency on towncrier itself, which only validates filenames/
types, not paragraph shape) so the exact rule that failed is always named
explicitly instead of surfacing a generic "invalid fragment" error.

Usage: check_changelog_fragment.py <path/to/NNN.type.md>
Exit code 0 if the fragment is valid, 1 if any rule is violated (with a
message identifying exactly which one), 2 on a usage error.
"""
import re
import sys

MIN_LEN = 20
MAX_LEN = 400
TYPE_PREFIXES = ("feat:", "fix:", "perf:", "feat(", "fix(", "perf(")
DOC_LINK = "CONTRIBUTING.md#release-notes--changeset-fragments"


def fail(rule: str, detail: str) -> None:
    print(f"::error::Changelog fragment violates rule [{rule}]: {detail}")
    print(f"::error::See {DOC_LINK} for the required format.")
    sys.exit(1)


def main() -> None:
    if len(sys.argv) != 2:
        print("usage: check_changelog_fragment.py <fragment path>", file=sys.stderr)
        sys.exit(2)

    path = sys.argv[1]
    with open(path, "r", encoding="utf-8") as f:
        raw = f.read()
    raw_lines = raw.splitlines()

    # Rule 1: exactly one paragraph — no blank line between the first and
    # last non-blank line (leading/trailing blank lines are tolerated and
    # ignored, but a blank line surrounded by content means >1 paragraph).
    non_blank_idx = [i for i, l in enumerate(raw_lines) if l.strip() != ""]
    if not non_blank_idx:
        fail(
            "1-single-paragraph",
            "the file is empty (or whitespace-only); it must contain exactly "
            "one paragraph of release-note text.",
        )
    first, last = non_blank_idx[0], non_blank_idx[-1]
    for i in range(first, last + 1):
        if raw_lines[i].strip() == "":
            fail(
                "1-single-paragraph",
                "found a blank line between content lines — the fragment "
                "must be a single continuous paragraph, not multiple "
                "paragraphs separated by a blank line.",
            )

    text = "\n".join(raw_lines[first:last + 1]).strip()

    # Rule 4: no markdown heading line.
    for l in raw_lines:
        if l.strip().startswith("#"):
            fail(
                "4-no-heading",
                f"line starts with '#' ('{l.strip()[:40]}'), which renders "
                "as a markdown heading — write plain prose instead.",
            )

    # Rule 5: no code block (fenced or indented).
    for l in raw_lines:
        if "```" in l:
            fail(
                "5-no-code-block",
                "found a ``` fenced code block delimiter — fragments must "
                "be plain prose, not code.",
            )
        if re.match(r"^(    |\t)\S", l):
            fail(
                "5-no-code-block",
                f"line '{l[:40]}' is indented 4+ spaces (or a tab), which "
                "renders as an indented code block — remove the "
                "indentation.",
            )

    # Rule 2: starts with an uppercase letter.
    if not text[:1].isupper():
        fail(
            "2-starts-uppercase",
            "the paragraph must start with an uppercase letter; it starts "
            f"with '{text[:20]}'.",
        )

    # Rule 3: ends with '.', '!' or '?'.
    if text[-1:] not in (".", "!", "?"):
        fail(
            "3-ends-with-punctuation",
            "the paragraph must end with '.', '!' or '?'; it ends with "
            f"'...{text[-20:]}'.",
        )

    # Rule 6: must not start by repeating the type prefix (redundant with
    # the changelog section it will be rendered under).
    lowered = text.lower()
    for prefix in TYPE_PREFIXES:
        if lowered.startswith(prefix):
            fail(
                "6-no-redundant-type-prefix",
                f"the paragraph starts with '{text[:len(prefix)]}', "
                "repeating the Conventional Commits type prefix — the "
                "changelog section already indicates the type, so start "
                "directly with the description instead.",
            )

    # Rule 7: length bounds.
    if len(text) < MIN_LEN:
        fail(
            "7-length",
            f"the paragraph is {len(text)} characters, below the "
            f"{MIN_LEN}-character minimum — write a complete sentence "
            "describing the user-facing change.",
        )
    if len(text) > MAX_LEN:
        fail(
            "7-length",
            f"the paragraph is {len(text)} characters, above the "
            f"{MAX_LEN}-character maximum — this is a release-note summary, "
            "not a full description; trim it (put extra detail in the PR "
            "description instead).",
        )

    print(f"OK: {path} is a valid changelog fragment ({len(text)} characters).")


if __name__ == "__main__":
    main()
