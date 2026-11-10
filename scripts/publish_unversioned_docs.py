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

It also turns the `roadmap/index.html` of every already published version that
still ships its own copy (releases cut before the roadmap was unversioned) into
a redirect to the root roadmap, so the roadmap changes retroactively too.

It also turns the `index.html` of those versions (the old home page each one
shipped before the split) into a redirect to the root home page, so the
"Home" link of their menu, their logo and `latest/` land on the one home page
instead of an outdated copy. Nothing on the site links to a version's bare
root as a way into its docs (the root menu links to specific pages, e.g.
`latest/installation/`), so this cannot loop. Versions that already redirect
(built from the current mkdocs.yml, whose menu's "Home" already is the root
page) are left alone. Which kind a version is gets read off its own pages: an
old version's menu links "Home" to `..`, its own root. That also tells a root
this script already pointed at the first docs page apart from a new version's
own redirect to it: only the former has such a menu link, and it is pointed at
the home page.

It only touches the filesystem; committing and pushing is up to the caller.
"""

import argparse
import json
import os
import re
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


# The menu entry an old version's pages have for its own (old) home page:
# `<a href=".." class="md-nav__link"> ... Home`.
_HOME_LINK_RE = re.compile(r'<a href="\.\./?" class="md-nav__link">\s*<span class="md-ellipsis">\s*Home\b')


def _redirect_page(path, url, force=False):
    """Replace the real page at `path` with a redirect to `url`; False if it is
    missing, a symlink, or already a redirect (any redirect, unless `force`,
    which only skips one that already goes to `url`)."""
    if not os.path.isfile(path) or os.path.islink(path):
        return False
    with open(path, encoding="utf-8", errors="replace") as f:
        text = f.read()
    if REDIRECT_MARKER in text and not (force and f'url={url}"' not in text):
        return False
    with open(path, "w", encoding="utf-8") as f:
        f.write(redirect_html(url))
    return True


def _links_home_to_root(version_dir):
    """True if the version's own pages link their menu's "Home" to the
    version's root, i.e. that root is (or stands in for) its home page."""
    path = os.path.join(version_dir, "installation", "index.html")
    if not os.path.isfile(path):
        return False
    with open(path, encoding="utf-8", errors="replace") as f:
        return _HOME_LINK_RE.search(f.read()) is not None


def redirect_old_pages(target, names, site_url):
    redirected = []
    for name in sorted(names):
        # The roadmap lives once, at the site root.
        rel = os.path.join("roadmap", "index.html")
        if _redirect_page(os.path.join(target, name, rel), site_url + "roadmap/"):
            redirected.append(os.path.join(name, rel))
        # The old home page: to the root home page (see the module docstring).
        rel = "index.html"
        if _links_home_to_root(os.path.join(target, name)) and _redirect_page(
            os.path.join(target, name, rel), site_url, force=True
        ):
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
