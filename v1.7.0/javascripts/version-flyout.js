/*
 * The version selector as a dropdown menu.
 *
 * `.md-version` is not in the page's server-rendered HTML: Material injects it
 * into the header client-side, after it fetches versions.json (the file mike's
 * `deploy` writes to the gh-pages branch). A DOMContentLoaded check can run
 * before that fetch resolves and find nothing, so this polls briefly for it.
 *
 * Once it exists, the real `.md-version__list` node (not a clone: its hrefs and
 * the current-version marking are already right) is moved out of Material's
 * hover-only card into `.pgrr-vmenu`, a floating panel appended to the header
 * and placed under the button. It floats over the page; it does not grow the
 * header. See extra.css's "Version selector" section for the look.
 *
 * Interaction follows the WAI-ARIA menu-button pattern: click or tap toggles
 * (the one trigger that works on touch), Arrow keys move through the versions,
 * Home/End jump, Escape closes and returns focus to the button, Tab or a click
 * outside closes. Hover does nothing: a menu that opens by itself on the way to
 * the search box is more of a nuisance than a convenience.
 */
(function () {
  var POLL_INTERVAL_MS = 100;
  var POLL_TIMEOUT_MS = 5000; // give up quietly if versions.json never resolves
  var CLOSE_TRANSITION_MS = 200; // extra.css's 160ms fade, plus slack
  var GAP_PX = 8;

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
    panel.className = "pgrr-vmenu";
    panel.hidden = true;
    panel.appendChild(list); // the real node, see this file's top comment
    header.appendChild(panel);

    list.setAttribute("role", "menu");
    list.setAttribute("aria-label", "Documentation version");
    var links = Array.prototype.slice.call(list.querySelectorAll(".md-version__link"));
    links.forEach(function (a) {
      a.setAttribute("role", "menuitem");
      a.setAttribute("tabindex", "-1");
    });

    // Material does not mark the version being read, so do it here: the link
    // whose folder holds this page (hrefs end in "/", so v1.0.0 never matches
    // v1.0.0-rc1).
    links.forEach(function (a) {
      var base;
      try {
        base = new URL(a.getAttribute("href"), location.href).pathname;
      } catch (e) {
        return;
      }
      if (base && location.pathname.indexOf(base) === 0 && base !== "/") {
        a.classList.add("is-current");
        a.setAttribute("aria-current", "true");
      }
    });

    trigger.setAttribute("aria-haspopup", "menu");
    trigger.setAttribute("aria-expanded", "false");
    trigger.setAttribute("role", "button");
    trigger.setAttribute("tabindex", "0");

    var open = false;
    var hideTimer = null;

    function place() {
      var t = trigger.getBoundingClientRect();
      var h = header.getBoundingClientRect();
      var w = panel.offsetWidth;
      var left = t.left - h.left;
      var maxLeft = document.documentElement.clientWidth - w - 12 - h.left;
      panel.style.left = Math.max(12 - h.left, Math.min(left, maxLeft)) + "px";
      panel.style.top = t.bottom - h.top + GAP_PX + "px";
    }

    function current() {
      for (var i = 0; i < links.length; i++) {
        if (links[i].classList.contains("is-current")) {
          return links[i];
        }
      }
      return links[0];
    }

    function openMenu(focusItem) {
      clearTimeout(hideTimer);
      if (!open) {
        open = true;
        trigger.setAttribute("aria-expanded", "true");
        root.classList.add("is-open");
        panel.hidden = false;
        place();
        // paint the hidden state once so the fade and rise are seen
        // eslint-disable-next-line no-unused-expressions
        panel.offsetHeight;
        panel.classList.add("is-open");
      }
      if (focusItem) {
        var target = focusItem === "last" ? links[links.length - 1] : focusItem === "current" ? current() : links[0];
        if (target) {
          target.focus();
        }
      }
    }

    function closeMenu(returnFocus) {
      if (!open) {
        return;
      }
      open = false;
      trigger.setAttribute("aria-expanded", "false");
      root.classList.remove("is-open");
      panel.classList.remove("is-open");
      hideTimer = setTimeout(function () {
        if (!open) {
          panel.hidden = true;
        }
      }, CLOSE_TRANSITION_MS);
      if (returnFocus) {
        trigger.focus();
      }
    }

    trigger.addEventListener("click", function (event) {
      event.preventDefault();
      if (open) {
        closeMenu(false);
      } else {
        openMenu(false);
      }
    });

    trigger.addEventListener("keydown", function (event) {
      if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openMenu("current");
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        openMenu("last");
      }
    });

    panel.addEventListener("keydown", function (event) {
      var i = links.indexOf(document.activeElement);
      if (event.key === "ArrowDown") {
        event.preventDefault();
        links[(i + 1) % links.length].focus();
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        links[(i - 1 + links.length) % links.length].focus();
      } else if (event.key === "Home") {
        event.preventDefault();
        links[0].focus();
      } else if (event.key === "End") {
        event.preventDefault();
        links[links.length - 1].focus();
      } else if (event.key === "Escape") {
        event.preventDefault();
        closeMenu(true);
      } else if (event.key === "Tab") {
        closeMenu(false);
      }
    });

    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && open) {
        closeMenu(true);
      }
    });

    document.addEventListener("click", function (event) {
      if (open && !root.contains(event.target) && !panel.contains(event.target)) {
        closeMenu(false);
      }
    });

    window.addEventListener("resize", function () {
      if (open) {
        place();
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
        clearInterval(interval); // no version selector on this build: give up quietly
      }
    }, POLL_INTERVAL_MS);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", waitForVersionSelector);
  } else {
    waitForVersionSelector();
  }
})();
