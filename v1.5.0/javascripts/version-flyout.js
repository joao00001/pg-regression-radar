/*
 * Replaces Material's floating `.md-version` menu with a push-down panel,
 * the same pattern nav-flyout.js already uses for the tabs menu -- see
 * extra.css's `.pgrr-version-flyout` rule for the CSS side of this.
 *
 * Unlike the tabs flyout, `.md-version` doesn't exist in the page's
 * server-rendered HTML at all: Material injects it into the header
 * client-side, asynchronously, only after it fetches versions.json (see
 * https://github.com/jimporter/mike -- that file is what mike's `deploy`
 * writes to the gh-pages branch). A naive DOMContentLoaded check for
 * `.md-version` can easily run *before* that fetch resolves and find
 * nothing -- silently doing nothing on every page load, which is exactly
 * what happened here the first time this script was written. Polling
 * briefly for the element to appear (rather than assuming it's already
 * there) is what actually fixes that.
 *
 * Once `.md-version` shows up, this moves the real `.md-version__list`
 * node (not a clone -- its hrefs and current-version marking are already
 * correct as Material built them) out from its `position: absolute`
 * spot and into `.pgrr-version-flyout`, a plain block appended at the
 * end of `.md-header`. Because `.md-header` is `position: sticky`,
 * growing it via max-height (exactly like `.pgrr-tabs-flyout` already
 * does for `.md-tabs`) pushes the tabs bar and page content below it
 * down instead of floating a card over either.
 *
 * Interaction follows the WAI-ARIA "menu button" pattern
 * (https://www.w3.org/WAI/ARIA/apg/patterns/menu-button/) -- the same
 * one GitHub's own branch/tag selector uses: click/tap toggles it (the
 * only reliable trigger on touch, where :hover never fires consistently
 * -- Material's own CSS even ships a `@media (hover:none)` "hoverfix"
 * animation hack for this exact gap), hover opens immediately and closes
 * after a short delay (nav-flyout.js's own CLOSE_DELAY_MS technique, so
 * crossing the gap between the button and the panel doesn't flicker it
 * shut), Escape closes and returns focus, and clicking outside closes it.
 */
(function () {
  var POLL_INTERVAL_MS = 100;
  var POLL_TIMEOUT_MS = 5000; // give up quietly if versions.json never resolves
  var CLOSE_DELAY_MS = 150; // matches nav-flyout.js's own hover-intent delay
  var CLOSE_TRANSITION_MS = 220; // matches extra.css's 200ms transition, plus slack

  function setup(root) {
    if (root.dataset.pgrrVersionInit) {
      return;
    }
    root.dataset.pgrrVersionInit = "true";

    var trigger = root.querySelector(".md-version__current");
    var list = root.querySelector(".md-version__list");
    var header = document.querySelector(".md-header");
    if (!trigger || !list || !header) {
      return;
    }

    var panel = document.createElement("div");
    panel.className = "pgrr-version-flyout";
    panel.hidden = true;
    panel.appendChild(list); // reparents the real node -- see this file's top comment
    header.appendChild(panel);

    trigger.setAttribute("aria-haspopup", "true");
    trigger.setAttribute("aria-expanded", "false");

    var closeTimer = null;
    var open = false;
    var suppressFocusOpen = false; // see the Escape handler below

    function openPanel() {
      clearTimeout(closeTimer);
      if (open) {
        return;
      }
      open = true;
      trigger.setAttribute("aria-expanded", "true");
      panel.hidden = false;
      // Force layout so the transition from 0 is visible rather than
      // jumping straight to the content's real height -- same trick
      // nav-flyout.js uses for the tabs panel.
      panel.style.maxHeight = "0px";
      // eslint-disable-next-line no-unused-expressions
      panel.offsetHeight; // Reflow before changing max-height again.
      panel.style.maxHeight = panel.scrollHeight + "px";
    }

    function closePanel() {
      clearTimeout(closeTimer);
      open = false;
      trigger.setAttribute("aria-expanded", "false");
      panel.style.maxHeight = "0px";
      // Wait for the collapse transition to actually finish before
      // hiding it -- doing that immediately would cut the animation off
      // and make the panel just vanish.
      setTimeout(function () {
        if (!open) {
          panel.hidden = true;
        }
      }, CLOSE_TRANSITION_MS);
    }

    function scheduleClose() {
      clearTimeout(closeTimer);
      closeTimer = setTimeout(closePanel, CLOSE_DELAY_MS);
    }

    // Click/tap toggles -- the primary interaction on touch, and a
    // "pin it open" affordance on desktop alongside hover below.
    trigger.addEventListener("click", function (event) {
      event.preventDefault();
      if (open) {
        closePanel();
      } else {
        openPanel();
      }
    });

    // Hover-intent: open immediately on entering either the trigger or
    // the panel, close after a short delay on leaving both -- see this
    // file's top comment.
    root.addEventListener("mouseenter", openPanel);
    root.addEventListener("mouseleave", scheduleClose);
    panel.addEventListener("mouseenter", function () {
      clearTimeout(closeTimer);
    });
    panel.addEventListener("mouseleave", scheduleClose);

    // Keyboard: focus anywhere inside opens it; only close once focus
    // has genuinely left both the trigger and the panel, mirroring
    // nav-flyout.js's own document-level focusout listener.
    trigger.addEventListener("focus", function () {
      if (suppressFocusOpen) {
        return;
      }
      openPanel();
    });
    document.addEventListener("focusout", function (event) {
      if (!open) {
        return;
      }
      var next = event.relatedTarget;
      if (next && (root.contains(next) || panel.contains(next))) {
        return;
      }
      setTimeout(function () {
        var active = document.activeElement;
        if (open && !root.contains(active) && !panel.contains(active)) {
          closePanel();
        }
      }, 0);
    });

    // Escape closes and returns focus to the trigger; clicking anywhere
    // outside the component closes it -- both standard parts of the
    // WAI-ARIA menu-button pattern referenced above.
    //
    // trigger.focus() below fires the "focus" listener registered above,
    // which normally reopens the panel -- that's correct for Tab-ing in,
    // but here it would silently undo the very close this handler just
    // did, making Escape a no-op. suppressFocusOpen is a one-tick flag
    // so that specific, self-inflicted focus event is ignored without
    // otherwise touching how focus opens the panel.
    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && open) {
        closePanel();
        suppressFocusOpen = true;
        trigger.focus();
        setTimeout(function () {
          suppressFocusOpen = false;
        }, 0);
      }
    });
    document.addEventListener("click", function (event) {
      if (open && !root.contains(event.target) && !panel.contains(event.target)) {
        closePanel();
      }
    });

    // If the window resizes while open, recompute the panel's height --
    // wrapping/reflow inside it can change how tall it actually is.
    window.addEventListener("resize", function () {
      if (open && !panel.hidden) {
        panel.style.maxHeight = panel.scrollHeight + "px";
      }
    });
  }

  function waitForVersionSelector() {
    var existing = document.querySelector(".md-version");
    if (existing) {
      setup(existing);
      return;
    }
    var elapsed = 0;
    var interval = setInterval(function () {
      var el = document.querySelector(".md-version");
      if (el) {
        clearInterval(interval);
        setup(el);
        return;
      }
      elapsed += POLL_INTERVAL_MS;
      if (elapsed >= POLL_TIMEOUT_MS) {
        clearInterval(interval); // no version selector on this build -- give up quietly
      }
    }, POLL_INTERVAL_MS);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", waitForVersionSelector);
  } else {
    waitForVersionSelector();
  }
})();
