#!/usr/bin/env python3
# Copyright 2026 The pg-regression-radar Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Guard for the `docs-unversioned` pull request label.

The label means "this PR only edits the version-independent docs pages (home,
roadmap and playground) and the assets only they use, and its changes go live
for every docs version as soon as it merges". If a labeled PR changes anything
else, this posts (or updates) a warning comment on it; once the PR is back to touching only those pages, or
the label is removed, the comment is deleted. It never fails the check -- the
comment is the alert, and a maintainer decides what to do with it.

Runs from a `pull_request_target` workflow, so it only talks to the GitHub API
through `gh` and never checks out or executes anything from the PR.

Environment: GH_TOKEN, GITHUB_REPOSITORY, PR_NUMBER.
"""

import json
import os
import subprocess
import sys

LABEL = "docs-unversioned"
# Keep in sync with hooks.py's UNVERSIONED_PAGES.
UNVERSIONED_PAGES = frozenset(
    {"docs/index.md", "docs/roadmap.md", "docs/playground.md", "docs/our-math.md"}
)
# Files only the unversioned build uses (see mkdocs-unversioned.yml): the
# roadmap's card styling and script, and that build's own config. They reach
# the site together with the pages, on merge, so a PR that changes them is
# still an "unversioned-pages-only" change. Add a file here when it is wired
# into mkdocs-unversioned.yml and nothing in the versioned build.
UNVERSIONED_ASSETS = frozenset(
    {
        "docs/stylesheets/roadmap.css",
        "docs/javascripts/roadmap.js",
        "docs/assets/playground/playground.css",
        "docs/assets/playground/playground.js",
        "docs/assets/playground/playground-core.js",
        "docs/assets/math/math.css",
        "docs/assets/math/math.js",
        "docs/assets/math/math-core.js",
        "docs/assets/math/math-bg.js",
        "docs/assets/math/fonts/noto-sans-math.woff2",
        "docs/assets/math/fonts/OFL.txt",
        "mkdocs-unversioned.yml",
    }
)
ALLOWED_FILES = UNVERSIONED_PAGES | UNVERSIONED_ASSETS
MARKER = "<!-- docs-unversioned-guard -->"
MAX_LISTED = 50
DOCS_URL = "https://joao00001.github.io/pg-regression-radar/latest/ci-cd/#docs-unversioned-guardyml-on-pr-label-changes"


def gh(*args, payload=None):
    result = subprocess.run(
        ["gh", "api", *args],
        input=None if payload is None else json.dumps(payload),
        capture_output=True,
        text=True,
        check=True,
    )
    return result.stdout


def paginate(endpoint):
    # `--paginate` with `--jq '.[]'` emits one JSON document per element.
    out = gh("--paginate", endpoint, "--jq", ".[]")
    return [json.loads(line) for line in out.splitlines() if line.strip()]


def unexpected_files(changed):
    """`changed` holds every path a PR touches, including a renamed file's old
    path. Returns the ones that aren't unversioned pages or their assets."""
    return sorted(set(changed) - ALLOWED_FILES)


def build_comment(offenders):
    listed = [f"- `{path.replace('`', '')}`".replace("\n", " ") for path in offenders[:MAX_LISTED]]
    if len(offenders) > MAX_LISTED:
        listed.append(f"- ...and {len(offenders) - MAX_LISTED} more")
    return "\n".join(
        [
            MARKER,
            f"### :warning: `{LABEL}` label: unexpected files changed",
            "",
            f"The `{LABEL}` label is only for changes to the docs pages that are **not versioned**: "
            "the home page (`docs/index.md`), the roadmap (`docs/roadmap.md`) and the playground "
            "(`docs/playground.md`), \"Our math\" (`docs/our-math.md`), plus the files only their build uses (the roadmap's `roadmap.css` / "
            "`roadmap.js`, the playground's `docs/assets/playground/` and the math page's `docs/assets/math/` scripts, styles and font, and `mkdocs-unversioned.yml`). "
            "They are published once, independently of any release, so merging this PR changes them "
            "for every docs version immediately.",
            "",
            "This PR also changes:",
            "",
            *listed,
            "",
            "Those files are versioned (or aren't docs at all) and wouldn't be published the same way. "
            f"Either split them into a separate PR, or remove the `{LABEL}` label if this isn't an "
            f"unversioned-pages-only change. See [CI/CD]({DOCS_URL}) for details.",
        ]
    )


def main():
    repo = os.environ["GITHUB_REPOSITORY"]
    number = os.environ["PR_NUMBER"]

    labels = {label["name"] for label in json.loads(gh(f"repos/{repo}/pulls/{number}"))["labels"]}
    offenders = []
    if LABEL in labels:
        changed = set()
        for item in paginate(f"repos/{repo}/pulls/{number}/files"):
            changed.add(item["filename"])
            if item.get("previous_filename"):
                changed.add(item["previous_filename"])
        offenders = unexpected_files(changed)

    existing = [
        c
        for c in paginate(f"repos/{repo}/issues/{number}/comments")
        if MARKER in c["body"] and c["user"]["type"] == "Bot"
    ]

    if not offenders:
        for comment in existing:
            gh("-X", "DELETE", f"repos/{repo}/issues/comments/{comment['id']}")
        print("OK: nothing to flag.")
        return

    body = build_comment(offenders)
    if existing:
        gh("-X", "PATCH", f"repos/{repo}/issues/comments/{existing[0]['id']}", "--input", "-", payload={"body": body})
        for extra in existing[1:]:
            gh("-X", "DELETE", f"repos/{repo}/issues/comments/{extra['id']}")
    else:
        gh("-X", "POST", f"repos/{repo}/issues/{number}/comments", "--input", "-", payload={"body": body})
    # File names are attacker-controlled, so they stay out of workflow commands.
    print(f"::warning::PR has the '{LABEL}' label but also changes {len(offenders)} other file(s); see the PR comment.")


if __name__ == "__main__":
    sys.exit(main())
