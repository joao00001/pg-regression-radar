/*
 * Adds a title bar to fenced code blocks that don't have a `title="..."`:
 * the bar shows the block's language. The language comes from the
 * `language-*` class on the block (`pygments_lang_class: true` in the
 * markdown_extensions of mkdocs.yml and mkdocs-unversioned.yml); a block
 * without that class, or a page where this script doesn't run, just has no
 * bar. Styling lives in extra.css ("Code blocks").
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

  function enhance() {
    var blocks = document.querySelectorAll("div.highlight");
    Array.prototype.forEach.call(blocks, function (block) {
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
