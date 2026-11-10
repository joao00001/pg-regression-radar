/*
 * Two things on every fenced code block:
 *
 * 1. A header row for blocks that don't have a `title="..."`: it shows the
 *    block's language. The language comes from the
 * `language-*` class on the block (`pygments_lang_class: true` in the
 * markdown_extensions of mkdocs.yml and mkdocs-unversioned.yml); a block
 *    without that class, or a page where this script doesn't run, just has no
 *    header row.
 * 2. A "Report incorrect code" button that opens the repo's "Documentation
 *    issue" form (.github/ISSUE_TEMPLATE/docs_issue.yml) with the page and
 *    the block's code already filled in. The repo's address is read from the
 *    header's repository link, so no button appears if a page has none.
 *
 * Styling lives in extra.css ("Code blocks").
 */
(function () {
  "use strict";

  var NAMES = {
    bash: "Bash",
    sh: "Shell",
    shell: "Shell",
    console: "Console",
    yaml: "YAML",
    yml: "YAML",
    json: "JSON",
    sql: "SQL",
    go: "Go",
    gotemplate: "Go template",
    promql: "PromQL",
    toml: "TOML",
    ini: "INI",
    diff: "Diff",
    markdown: "Markdown",
    dockerfile: "Dockerfile",
    text: "Text",
    none: "Text",
  };

  function languageOf(block) {
    var match = (block.className || "").match(/(?:^|\s)language-([\w+#-]+)/);
    return match ? match[1].toLowerCase() : null;
  }

  function nameOf(lang) {
    return NAMES[lang] || lang.charAt(0).toUpperCase() + lang.slice(1);
  }

  var MAX_SNIPPET = 1500; // keeps the prefilled address well under URL limits

  var ICON =
    '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" ' +
    'stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">' +
    '<circle cx="12" cy="12" r="9.25"/><path d="M12 7.5v5.5"/><path d="M12 16.4v.1"/></svg>';

  function repoUrl() {
    var link = document.querySelector("a.md-source");
    return link && link.href ? link.href.replace(/\/+$/, "") : null;
  }

  function issueUrl(repo, block) {
    var code = block.querySelector("code");
    var snippet = (code ? code.innerText : "").replace(/\s+$/, "");
    if (snippet.length > MAX_SNIPPET) snippet = snippet.slice(0, MAX_SNIPPET) + "\n...";
    var h1 = document.querySelector("h1");
    var heading = (h1 ? h1.textContent : document.title).replace(/[¶\s]+$/, "").trim();
    var params = new URLSearchParams({
      template: "docs_issue.yml",
      title: "docs: incorrect code in \"" + heading + "\"",
      page: location.href.split("#")[0],
      snippet: snippet,
    });
    return repo + "/issues/new?" + params.toString();
  }

  function addReportButton(repo, block) {
    if (block.querySelector(":scope > .pgrr-report")) return;
    var link = document.createElement("a");
    link.className = "pgrr-report";
    link.href = issueUrl(repo, block);
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    link.title = "Report incorrect code";
    link.setAttribute("aria-label", "Report incorrect code");
    link.innerHTML = ICON;
    block.appendChild(link);
  }

  function enhance() {
    var repo = repoUrl();
    var blocks = document.querySelectorAll("div.highlight");
    Array.prototype.forEach.call(blocks, function (block) {
      if (repo) addReportButton(repo, block);
      if (block.querySelector(":scope > .filename")) return; // has a title
      var lang = languageOf(block);
      if (!lang) return;
      var bar = document.createElement("span");
      bar.className = "filename";
      bar.textContent = nameOf(lang);
      block.insertBefore(bar, block.firstChild);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", enhance);
  } else {
    enhance();
  }
})();
