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

"""Publish the unversioned docs pages (home + roadmap) to a gh-pages checkout.

mike owns the per-version directories of the gh-pages branch (`v1.5.0/`,
`latest/`, ...) plus `versions.json`. This script owns everything else at the
branch root: it replaces it with a fresh build of mkdocs-unversioned.yml, so
the home page and the roadmap are identical whichever version a reader picks.

It also turns the `index.html` and `roadmap/index.html` of every already
published version that still ships its own copy (releases cut before these
pages were unversioned) into redirects to the root copies, so those pages
change retroactively too. Versions built from the current mkdocs.yml don't
have a roadmap, and their `index.html` is already a redirect to their first
docs page (marked with hooks.py's REDIRECT_MARKER); both are left alone.

It only touches the filesystem; committing and pushing is up to the caller.
"""

import argparse
import json
import os
import shutil
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
from hooks import REDIRECT_MARKER, redirect_html  # noqa: E402


def version_dirs(target):
    """(versions, aliases) mike manages, as directory names."""
    path = os.path.join(target, "versions.json")
    # Refuse to guess which directories are versions: deleting the wrong ones
    # would destroy published docs.
    if not os.path.isfile(path):
        sys.exit(f"{path} not found; refusing to publish without knowing which directories belong to mike")
    with open(path, encoding="utf-8") as f:
        versions = json.load(f)
    names = {entry["version"] for entry in versions}
    aliases = {alias for entry in versions for alias in entry.get("aliases", [])}
    return names, aliases


def clear_root(target, keep):
    for name in os.listdir(target):
        if name in keep or name == "versions.json" or name.startswith("."):
            continue
        path = os.path.join(target, name)
        if os.path.isdir(path) and not os.path.islink(path):
            shutil.rmtree(path)
        else:
            os.remove(path)


def copy_site(site, target):
    for root, _dirs, files in os.walk(site):
        rel = os.path.relpath(root, site)
        dest = target if rel == "." else os.path.join(target, rel)
        os.makedirs(dest, exist_ok=True)
        for name in files:
            # sitemap.xml.gz embeds a timestamp, so it would make every
            # publish a non-empty commit.
            if name.endswith(".gz"):
                continue
            shutil.copy2(os.path.join(root, name), os.path.join(dest, name))


def redirect_old_pages(target, names, site_url):
    redirected = []
    for name in sorted(names):
        for rel, dest in (("index.html", ""), (os.path.join("roadmap", "index.html"), "roadmap/")):
            path = os.path.join(target, name, rel)
            if not os.path.isfile(path) or os.path.islink(path):
                continue
            with open(path, encoding="utf-8", errors="replace") as f:
                if REDIRECT_MARKER in f.read():
                    continue
            with open(path, "w", encoding="utf-8") as f:
                f.write(redirect_html(site_url + dest))
            redirected.append(os.path.join(name, rel))
    return redirected


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--site", required=True, help="built output of mkdocs-unversioned.yml")
    parser.add_argument("--target", required=True, help="gh-pages checkout")
    parser.add_argument("--site-url", required=True, help="public root URL of the docs site")
    args = parser.parse_args()

    site_url = args.site_url.rstrip("/") + "/"
    names, aliases = version_dirs(args.target)
    clear_root(args.target, names | aliases)
    copy_site(args.site, args.target)
    # Aliases (e.g. `latest`) only hold redirects into a real version, which
    # is where the pages actually get replaced.
    for path in redirect_old_pages(args.target, names, site_url):
        print(f"redirected {path}")


if __name__ == "__main__":
    main()
