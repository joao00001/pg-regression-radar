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

"""MkDocs build hook (https://www.mkdocs.org/user-guide/configuration/#hooks)
that feeds docs/javascripts/nav-flyout.js the full top-level nav tree.

Why this exists: the site's top tabs bar has a handful of umbrella entries
(Get Started, Architecture, Reference, Contributing) that group several
pages each. nav-flyout.js expands the header downward on hover to reveal
those pages, Apple-nav style, instead of Material's default of only
showing a section's children in the *sidebar*, and only once you're
already on a page inside that section.

The problem that solves: Material's own rendered HTML never puts a
section's children in the tabs bar markup itself, and the sidebar nav
tree for OTHER sections isn't present in a given page's HTML at all — so
there's nothing for a hover script to read client-side, on any page, for
every section. This hook walks the resolved Navigation object (which,
server/build-side, always has the complete tree, unlike any single
rendered page) and writes it as a plain JSON data file next to the other
built JS, once, at build time.
"""

import json
import os

_nav = None


def _serialize(items):
    """Turn a list of mkdocs Page/Section/Link nav items into the plain
    {title, url, children} shape nav-flyout.js expects. Recursive so a
    future nested section (none exist today) still degrades sensibly
    instead of silently dropping items."""
    result = []
    for item in items:
        entry = {"title": item.title}
        children = getattr(item, "children", None)
        if children:
            entry["children"] = _serialize(children)
        elif getattr(item, "url", None) is not None:
            entry["url"] = item.url
        result.append(entry)
    return result


def on_nav(nav, config, files):
    """Keep a reference to the live Navigation object rather than
    serializing right away: for a nav entry with no explicit title (this
    repo has one -- the bare `architecture.md` used to make that
    section's own tab clickable), `Page.title` is still None at this
    point in the build. It's only derived from the page's own H1 once
    that page is actually rendered, which happens later. `nav.items`
    holds references to the same Page objects mkdocs mutates in place as
    it renders each page, so reading them again in `on_post_build`
    (after every page has been processed) sees the final titles."""
    global _nav
    _nav = nav
    return nav


def on_post_build(config, **kwargs):
    out_dir = os.path.join(config["site_dir"], "javascripts")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, "nav-data.js")
    tree = _serialize(_nav.items) if _nav is not None else []
    with open(out_path, "w", encoding="utf-8") as f:
        f.write("window.PGRR_NAV_TREE = ")
        json.dump(tree, f)
        f.write(";\n")
