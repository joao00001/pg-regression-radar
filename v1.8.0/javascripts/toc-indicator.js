/*
 * A dot on the table of contents' track (the right sidebar) that follows the
 * active entry, the way mintlify.com/docs does: a small circle that slides to
 * the entry being read. Material marks that entry with `md-nav__link--active`
 * as the page scrolls; this watches the class and moves the dot. Styles
 * (size, easing) live in docs/stylesheets/extra.css. Without this script the
 * active entry is simply coloured.
 */
(function () {
  "use strict";

  var list = document.querySelector(
    ".md-sidebar--secondary .md-nav--secondary > .md-nav__list"
  );
  if (!list) return;

  var SIZE = 6;
  var dot = document.createElement("span");
  dot.className = "pgrr-toc-dot";
  dot.setAttribute("aria-hidden", "true");
  list.appendChild(dot);

  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)");
  var placed = false;
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

  function moveTo(top) {
    var first = !placed;
    placed = true;
    dot.style.opacity = "1";
    // First placement (and reduced motion) jumps; later ones slide via CSS.
    dot.style.transitionDuration = first || reduce.matches ? "0ms" : "";
    dot.style.top = top + "px";
  }

  function update() {
    frame = 0;
    var link = activeLink();
    if (!link || !link.offsetParent) {
      dot.style.opacity = "0";
      placed = false;
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
    placed = false; // re-measure without animating
    schedule();
  });
  window.addEventListener("load", function () {
    placed = false;
    schedule();
  });
  schedule();
})();
