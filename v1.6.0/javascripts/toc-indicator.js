/*
 * A dot on the table of contents' track (the right sidebar) that follows the
 * active entry. Material marks the entry for the heading being read with
 * `md-nav__link--active` as the page scrolls; this watches that class and
 * moves the dot with a "pull": the leading end stretches to the new entry
 * first, then the trailing end catches up. Styles: docs/stylesheets/extra.css.
 * Without this script the active entry is just bold.
 */
(function () {
  "use strict";

  var list = document.querySelector(
    ".md-sidebar--secondary .md-nav--secondary > .md-nav__list"
  );
  if (!list) return;

  var SIZE = 7;
  var STRETCH_MS = 170;
  var SETTLE_MS = 280;
  var dot = document.createElement("span");
  dot.className = "pgrr-toc-dot";
  dot.setAttribute("aria-hidden", "true");
  list.appendChild(dot);

  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)");
  var target = null;
  var timer = 0;
  var frame = 0;

  function activeLink() {
    var all = list.querySelectorAll("a.md-nav__link--active");
    // Above the first heading nothing is active yet: rest on the first entry.
    return all.length ? all[all.length - 1] : list.querySelector("a.md-nav__link");
  }

  // Top of a dot centred on the link's first line, relative to the list.
  function topFor(link) {
    var cs = window.getComputedStyle(link);
    var line = parseFloat(cs.lineHeight) || link.getBoundingClientRect().height;
    var pad = parseFloat(cs.paddingTop) || 0;
    var y = link.getBoundingClientRect().top - list.getBoundingClientRect().top;
    return y + pad + (line - SIZE) / 2;
  }

  function place(top, height, ms, easing) {
    dot.style.transitionDuration = ms + "ms";
    if (easing) dot.style.transitionTimingFunction = easing;
    dot.style.top = top + "px";
    dot.style.height = height + "px";
  }

  function moveTo(top) {
    window.clearTimeout(timer);
    var first = dot.style.opacity !== "1";
    dot.style.opacity = "1";
    if (first || reduce.matches) {
      place(top, SIZE, 0);
      target = top;
      return;
    }
    if (top === target) return;
    var from = parseFloat(window.getComputedStyle(dot).top) || 0;
    target = top;
    var span = Math.abs(top - from);
    // Stretch from where the dot is to the new entry (downwards the bottom
    // end leads, upwards the top end does), then let the tail catch up.
    place(Math.min(from, top), span + SIZE, STRETCH_MS, "cubic-bezier(.2,.7,.2,1)");
    timer = window.setTimeout(function () {
      place(top, SIZE, SETTLE_MS, "cubic-bezier(.5,0,.2,1)");
    }, STRETCH_MS - 20);
  }

  function update() {
    frame = 0;
    var link = activeLink();
    if (!link || !link.offsetParent) {
      window.clearTimeout(timer);
      dot.style.opacity = "0";
      target = null;
      return;
    }
    moveTo(topFor(link));
  }

  function schedule() {
    if (!frame) frame = window.requestAnimationFrame(update);
  }

  new MutationObserver(schedule).observe(list, {
    attributes: true,
    attributeFilter: ["class"],
    subtree: true,
  });
  window.addEventListener("resize", function () {
    target = null; // re-measure without animating
    dot.style.opacity = "0";
    schedule();
  });
  window.addEventListener("load", function () {
    target = null;
    dot.style.opacity = "0";
    schedule();
  });
  schedule();
})();
