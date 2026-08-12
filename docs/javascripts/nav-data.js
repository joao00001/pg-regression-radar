/*
 * Placeholder, overwritten at build time by hooks.py's on_post_build with
 * the real top-level nav tree (see hooks.py's module doc comment). Exists
 * as a real file under docs/javascripts/ purely so mkdocs' normal
 * extra_javascript handling copies it into the built site and inserts the
 * <script> tag on every page; the hook then replaces its contents in the
 * built site_dir with the actual data. `mkdocs serve`/a build that
 * somehow skips the hook still gets a harmless empty tree here, so
 * nav-flyout.js degrades to "no flyouts" instead of erroring.
 */
window.PGRR_NAV_TREE = [];
