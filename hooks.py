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
that implements the split between versioned docs and the unversioned home
page, roadmap, playground and "Our math" (mkdocs.yml vs. mkdocs-unversioned.yml): rewriting links between
the two, giving the unversioned build the same menu as the versioned one, and
giving each version's root a redirect in place of the home page.
"""

import functools
import json
import os
import posixpath
import re
import subprocess

import yaml


# Pages that live outside mike's per-release versioning: published once, at
# the site root, from `main` (see mkdocs-unversioned.yml and
# .github/workflows/docs.yml's `deploy-unversioned` job). Source path under
# docs/ -> path under the site root.
UNVERSIONED_PAGES = {
    "index.md": "",
    "roadmap.md": "roadmap/",
    "playground.md": "playground/",
    "our-math.md": "our-math/",
    "contribute.md": "contribute/",
}

# Matches either an inline code span (returned untouched) or the
# `](target.md#frag)` part of an inline Markdown link whose target is a
# relative .md path.
_LINK_RE = re.compile(
    r"(?P<code>(`+)[^`]*?\2)"
    r"|\]\((?P<path>(?![a-z][a-z0-9+.-]*:|#|/)[^)#\s]*\.md)(?P<frag>#[^)\s]*)?\)",
    re.IGNORECASE,
)
_FENCE_RE = re.compile(r"^\s*(```|~~~)")

REDIRECT_MARKER = "pgrr-redirect"


def _is_unversioned(config):
    return (config.get("extra") or {}).get("pgrr_build") == "unversioned"


def redirect_html(url):
    """A tiny meta-refresh page (plus a script that keeps the URL's #fragment)
    marked with REDIRECT_MARKER so scripts/publish_unversioned_docs.py can tell
    it apart from a real rendered page."""
    return (
        '<!doctype html>\n<html lang="en"><head><meta charset="utf-8">\n'
        "<title>Redirecting...</title>\n"
        f'<link rel="canonical" href="{url}">\n'
        '<meta name="robots" content="noindex">\n'
        f'<meta http-equiv="refresh" content="0; url={url}">\n'
        f'<script>location.replace({json.dumps(url)} + location.hash);</script>\n'
        f"<!-- {REDIRECT_MARKER} -->\n"
        f'</head><body>Redirecting to <a href="{url}">{url}</a>...</body></html>\n'
    )


def _nav_title(config, src_uri):
    """Title for a bare nav entry (a page listed without one): its first `# `
    heading, like MkDocs itself would use, or the file name as a fallback."""
    try:
        with open(os.path.join(config["docs_dir"], src_uri), encoding="utf-8") as f:
            for line in f:
                if line.startswith("# "):
                    return line[2:].strip()
    except OSError:
        pass
    return os.path.splitext(os.path.basename(src_uri))[0].replace("-", " ").title()


def _unversioned_nav_item(title, target, config):
    """One nav entry of the unversioned build, derived from mkdocs.yml's.

    The unversioned pages (home, roadmap, playground) stay local pages (so
    their tab shows as active); every other page becomes an absolute link into the `latest`
    docs, which is where this build can't reach but readers want to go.
    Entries that already are URLs (the CHANGELOG/CONTRIBUTING links) stay."""
    site_url = config["site_url"].rstrip("/") + "/"
    local = {site_url + path: src for src, path in UNVERSIONED_PAGES.items()}
    if target in local:
        target = local[target]
    elif target.endswith(".md") and target not in UNVERSIONED_PAGES and "://" not in target:
        title = title or _nav_title(config, target)
        page = target[: -len(".md")]
        page = page[: -len("index")] if page == "index" or page.endswith("/index") else page + "/"
        target = site_url + "latest/" + page
    return {title: target} if title else target


def _unversioned_nav(items, config):
    out = []
    for item in items:
        if isinstance(item, dict):
            ((title, value),) = item.items()
            if isinstance(value, list):
                out.append({title: _unversioned_nav(value, config)})
            else:
                out.append(_unversioned_nav_item(title, value, config))
        else:
            out.append(_unversioned_nav_item(None, item, config))
    return out


class _NavLoader(yaml.SafeLoader):
    """Reads mkdocs.yml for its `nav` only. The file also holds `!!python/name:`
    tags (the Mermaid fence), which safe_load refuses; they are not needed
    here, so they load as None."""


_NavLoader.add_multi_constructor("tag:yaml.org,2002:python/", lambda loader, suffix, node: None)


def on_config(config, **kwargs):
    """Give the unversioned build (home + roadmap) the same menu as the
    versioned docs instead of a hand-copied one that drifts.

    mkdocs-unversioned.yml inherits mkdocs.yml but only builds two pages, so
    it can't reuse that `nav` as is (it names pages this build doesn't have).
    Derive it from mkdocs.yml's `nav` here: see _unversioned_nav_item."""
    if not _is_unversioned(config):
        return config
    base = os.path.join(os.path.dirname(os.path.abspath(config["config_file_path"])), "mkdocs.yml")
    with open(base, encoding="utf-8") as f:
        config["nav"] = _unversioned_nav(yaml.load(f, Loader=_NavLoader)["nav"], config)
    return config


def on_pre_build(config, **kwargs):
    """Copy the repo-root CHANGELOG.md into docs/changelog.md so it's a
    real page in the nav (see mkdocs.yml) instead of only being reachable
    via an absolute GitHub blob URL.

    Why a hook instead of just putting CHANGELOG.md under docs/ directly:
    it needs to stay at the repo root (GitHub renders a root CHANGELOG.md
    specially, and CONTRIBUTING.md / release.yml / towncrier.toml all
    reference it there) — see docs/ci-cd.md's docs.yml section. Runs in
    `on_pre_build`, before MkDocs scans `docs_dir` for files, so the
    generated copy is picked up as an ordinary page like any other.

    Two of CHANGELOG.md's relative links break once copied a directory
    level deeper into docs/ and need rewriting; everything else in the
    file (commit/PR links) is already an absolute github.com URL and is
    left untouched:

    - `CONTRIBUTING.md#...` (repo root) has no equivalent under docs/ at
      all, so it's rewritten to an absolute GitHub blob URL.
    - `docs/roadmap.md#...` needs the now-redundant `docs/` prefix
      stripped, since changelog.md itself now lives inside docs/.
    """
    if _is_unversioned(config):
        return
    repo_root = os.path.dirname(os.path.abspath(config["config_file_path"]))
    src = os.path.join(repo_root, "CHANGELOG.md")
    if not os.path.exists(src):
        return

    with open(src, encoding="utf-8") as f:
        content = f.read()

    content = re.sub(
        r"\]\(CONTRIBUTING\.md(#[^)]*)?\)",
        lambda m: "](https://github.com/joao00001/pg-regression-radar/blob/main/CONTRIBUTING.md"
        + (m.group(1) or "")
        + ")",
        content,
    )
    content = content.replace("](docs/roadmap.md", "](roadmap.md")

    dest_dir = os.path.join(repo_root, config["docs_dir"])
    os.makedirs(dest_dir, exist_ok=True)
    with open(os.path.join(dest_dir, "changelog.md"), "w", encoding="utf-8") as f:
        f.write(content)


def _rewrite_target(src_uri, path, frag, config):
    """Return the replacement URL for a relative `.md` link found on page
    `src_uri`, or None to leave it for MkDocs to resolve as usual.

    Versioned build: links to an unversioned page point at its one canonical
    copy at the site root. Unversioned build: links to any other page point at
    the `latest` docs, since that's what a version-less page can sensibly
    reference."""
    site_url = config["site_url"].rstrip("/") + "/"
    resolved = posixpath.normpath(posixpath.join(posixpath.dirname(src_uri), path))
    if _is_unversioned(config):
        if resolved in UNVERSIONED_PAGES:
            return None
        page = resolved[: -len(".md")]
        if page == "index" or page.endswith("/index"):
            page = page[: -len("index")]
        else:
            page += "/"
        return site_url + "latest/" + page + (frag or "")
    if resolved in UNVERSIONED_PAGES:
        return site_url + UNVERSIONED_PAGES[resolved] + (frag or "")
    return None


_RELEASE_TAG_RE = re.compile(r"^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)*$")


@functools.lru_cache(maxsize=None)
def _release_tag():
    """The release the docs being built describe, as a tag (`v1.8.0`).

    Pages write image tags, chart versions and `go install` pins with the
    `release_tag` / `release_version` placeholders (double curly braces), so every
    version of the docs shows its own release instead of a stale or generic
    number. Where the value comes from, first match wins:

    1. PGRR_DOCS_VERSION, which docs.yml sets to the tag it deploys (this is
       what keeps a redeploy of an old tag from a newer ref correct);
    2. the tag checked out (a build from a tag, as the propagation script does);
    3. the most recent `v*` tag, so a local `mkdocs serve` of unreleased work
       shows the latest release;
    4. the literal `vX.Y.Z`, e.g. in a shallow clone with no tags.
    """
    env = os.environ.get("PGRR_DOCS_VERSION", "").strip()
    if env:
        env = env if env.startswith("v") else "v" + env
        if not _RELEASE_TAG_RE.match(env):
            raise ValueError(f"PGRR_DOCS_VERSION={env!r} is not a release tag like v1.8.0")
        return env
    root = os.path.dirname(os.path.abspath(__file__))
    for args in (
        ("describe", "--tags", "--exact-match", "HEAD"),
        ("describe", "--tags", "--abbrev=0", "--match", "v[0-9]*"),
    ):
        try:
            out = subprocess.run(("git",) + args, cwd=root, capture_output=True, text=True, check=True).stdout.strip()
        except (OSError, subprocess.CalledProcessError):
            continue
        if _RELEASE_TAG_RE.match(out):
            return out
    return "vX.Y.Z"


def on_page_markdown(markdown, page, config, files):
    """Rewrite relative links that cross the versioned/unversioned boundary
    (see UNVERSIONED_PAGES), skipping fenced and inline code so documentation
    that shows example Markdown isn't altered. Also fills in the release
    placeholders (see _release_tag), code blocks included."""
    tag = _release_tag()
    markdown = markdown.replace("{{ release_tag }}", tag).replace("{{ release_version }}", tag[1:])
    # The chat server is set once, in mkdocs.yml's `extra.discord`.
    discord = (config.get("extra") or {}).get("discord") or {}
    markdown = markdown.replace("{{ discord_url }}", str(discord.get("url", ""))).replace(
        "{{ discord_name }}", str(discord.get("name", "Discord"))
    )

    def sub(match):
        if match.group("code"):
            return match.group(0)
        url = _rewrite_target(page.file.src_uri, match.group("path"), match.group("frag"), config)
        return match.group(0) if url is None else f"]({url})"

    out = []
    in_fence = False
    for line in markdown.split("\n"):
        if _FENCE_RE.match(line):
            in_fence = not in_fence
            out.append(line)
        elif in_fence:
            out.append(line)
        else:
            out.append(_LINK_RE.sub(sub, line))
    return "\n".join(out)


def on_post_build(config, **kwargs):
    if not _is_unversioned(config):
        # The home page isn't part of a versioned build (see UNVERSIONED_PAGES),
        # so a version's own root needs somewhere to land.
        with open(os.path.join(config["site_dir"], "index.html"), "w", encoding="utf-8") as f:
            f.write(redirect_html("installation/"))
