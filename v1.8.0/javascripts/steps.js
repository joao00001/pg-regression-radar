/*
 * Turns a run of numbered section headings ("## 1) Set up ...",
 * "## 2) Start ...", ...) into steps, like a numbered list's steps in
 * extra.css ("Numbered lists as steps"): each heading and the content under
 * it is wrapped in a `.pgrr-step` section carrying the number, and the
 * "1)" prefix is dropped from the title because the circle shows it.
 *
 * Only a run that counts up from 1 with at least two headings is touched,
 * so a stray "3) ..." heading is left alone. Anchors, the table of contents
 * and the permalink stay as they are (only the leading number is removed
 * from the visible title). Without this script the headings simply keep
 * their "1)" prefix.
 */
(function () {
  "use strict";

  var PREFIX = /^\s*(\d+)\)\s+/;

  function numberOf(el) {
    if (!el || el.tagName !== "H2") return null;
    var first = el.firstChild;
    if (!first || first.nodeType !== 3) return null;
    var m = first.nodeValue.match(PREFIX);
    return m ? parseInt(m[1], 10) : null;
  }

  function build() {
    var root = document.querySelector("article.md-content__inner");
    if (!root) return;
    var kids = Array.prototype.slice.call(root.children);

    // Find the run: consecutive numbered h2s 1, 2, 3, ... (other content may
    // sit between them; an unnumbered h2 ends the run).
    var run = [];
    for (var i = 0; i < kids.length; i++) {
      var n = numberOf(kids[i]);
      if (n === run.length + 1) {
        run.push(i);
      } else if (kids[i].tagName === "H2" && run.length > 0) {
        break;
      }
    }
    if (run.length < 2) return;

    // Each step ends where the next one starts; the last one ends at the next
    // h2 (or the end of the page content).
    var end = kids.length;
    for (var j = run[run.length - 1] + 1; j < kids.length; j++) {
      if (kids[j].tagName === "H2") {
        end = j;
        break;
      }
    }
    for (var s = run.length - 1; s >= 0; s--) {
      var from = run[s];
      var to = s === run.length - 1 ? end : run[s + 1];
      var section = document.createElement("section");
      section.className = "pgrr-step";
      section.setAttribute("data-step", String(s + 1));
      var heading = kids[from];
      heading.firstChild.nodeValue = heading.firstChild.nodeValue.replace(PREFIX, "");
      root.insertBefore(section, heading);
      for (var k = from; k < to; k++) section.appendChild(kids[k]);
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", build);
  } else {
    build();
  }
})();
