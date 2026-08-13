/*
 * JS-driven open/close for Material's `.md-version` selector, replacing
 * its default hover/focus-within-only trigger (see extra.css's
 * `.md-version:hover`/`:focus-within` rule, kept purely as a no-JS
 * fallback -- this script takes over as soon as it runs).
 *
 * A raw CSS :hover dropdown -- what Material ships by default -- has two
 * real usability problems, not just a cosmetic one:
 *
 *   1. No click/tap support. `:hover` never fires reliably on touch --
 *      Material's own CSS even ships a `@media (hover:none)` "hoverfix"
 *      animation hack to paper over iOS Safari's sticky-hover bug, which
 *      is a workaround for :hover-only triggering, not a real fix. This
 *      adds an actual click/tap toggle instead, following the WAI-ARIA
 *      "menu button" pattern
 *      (https://www.w3.org/WAI/ARIA/apg/patterns/menu-button/) -- the
 *      same interaction GitHub's own branch/tag selector uses.
 *   2. No hover-intent delay. Moving the pointer from the button to the
 *      popped-out list crosses a small visual gap; if that crossing
 *      isn't instant, native :hover is lost mid-way and the menu snaps
 *      shut, which reads as flickery rather than fluid. This borrows
 *      docs/javascripts/nav-flyout.js's own CLOSE_DELAY_MS technique for
 *      the exact same gap-crossing problem there: open immediately,
 *      close only after a short delay, cancelled if the pointer lands
 *      back inside before it fires.
 *
 * extra.css's `.md-version--open` rule is what this script actually
 * toggles -- see its comment for the CSS side of this split.
 */
(function () {
  var CLOSE_DELAY_MS = 150; // matches nav-flyout.js's own hover-intent delay

  function setup() {
    var root = document.querySelector(".md-version");
    var trigger = root && root.querySelector(".md-version__current");
    var list = root && root.querySelector(".md-version__list");
    if (!root || !trigger || !list) {
      return; // No version selector on this build (e.g. mike not configured).
    }
    if (root.dataset.pgrrVersionInit) {
      return;
    }
    root.dataset.pgrrVersionInit = "true";

    trigger.setAttribute("aria-haspopup", "true");
    trigger.setAttribute("aria-expanded", "false");

    var closeTimer = null;

    function isOpen() {
      return root.classList.contains("md-version--open");
    }

    function open() {
      clearTimeout(closeTimer);
      if (isOpen()) {
        return;
      }
      root.classList.add("md-version--open");
      trigger.setAttribute("aria-expanded", "true");
    }

    function closeNow() {
      clearTimeout(closeTimer);
      root.classList.remove("md-version--open");
      trigger.setAttribute("aria-expanded", "false");
    }

    function scheduleClose() {
      clearTimeout(closeTimer);
      closeTimer = setTimeout(closeNow, CLOSE_DELAY_MS);
    }

    // Click/tap toggles -- the primary interaction on touch, and a
    // "pin it open" affordance on desktop alongside hover below.
    trigger.addEventListener("click", function (event) {
      event.preventDefault();
      if (isOpen()) {
        closeNow();
      } else {
        open();
      }
    });

    // Hover-intent: open immediately on entering either the trigger or
    // the list (they're one region for this purpose), close after a
    // short delay on leaving both -- see this file's top comment.
    root.addEventListener("mouseenter", open);
    root.addEventListener("mouseleave", scheduleClose);

    // Keyboard: focus anywhere inside opens it; only close once focus
    // has genuinely left the whole component, mirroring
    // nav-flyout.js's own document-level focusout listener.
    trigger.addEventListener("focus", open);
    document.addEventListener("focusout", function (event) {
      if (!isOpen()) {
        return;
      }
      var next = event.relatedTarget;
      if (next && root.contains(next)) {
        return;
      }
      // relatedTarget is unreliable across browsers on blur; confirm
      // against the real activeElement on the next tick instead of
      // trusting it alone.
      setTimeout(function () {
        if (isOpen() && !root.contains(document.activeElement)) {
          closeNow();
        }
      }, 0);
    });

    // Escape closes and returns focus to the trigger; clicking anywhere
    // outside the component closes it -- both standard parts of the
    // WAI-ARIA menu-button pattern referenced above.
    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && isOpen()) {
        closeNow();
        trigger.focus();
      }
    });
    document.addEventListener("click", function (event) {
      if (isOpen() && !root.contains(event.target)) {
        closeNow();
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", setup);
  } else {
    setup();
  }
})();
