/*
 * Marks callouts (admonitions) whose title is just their type ("Note",
 * "Warning", ...) so extra.css can hide it: the icon and the colors already
 * say the type, and a title is kept only when someone wrote one. Without this
 * script the default title simply shows. Styling lives in extra.css
 * ("Callouts").
 */
(function () {
  "use strict";

  function mark() {
    var callouts = document.querySelectorAll(".admonition");
    Array.prototype.forEach.call(callouts, function (callout) {
      var title = callout.querySelector(":scope > .admonition-title");
      if (!title) return;
      var type = Array.prototype.filter.call(callout.classList, function (c) {
        return c !== "admonition" && c !== "inline" && c !== "end";
      })[0];
      if (type && title.textContent.trim().toLowerCase() === type.toLowerCase()) {
        title.classList.add("pgrr-default");
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", mark);
  } else {
    mark();
  }
})();
