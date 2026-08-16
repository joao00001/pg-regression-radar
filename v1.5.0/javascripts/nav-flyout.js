/*
 * Apple-nav-style umbrella tabs: hovering a top-level tab that has pages
 * grouped under it (Get Started, Architecture, Reference, Contributing —
 * see mkdocs.yml's nav comment) expands the tabs bar downward, in normal
 * document flow, to reveal those pages. No extra clickable control is
 * added to the tab row, nothing floats over the page content, and the
 * only trigger is hover (mouseenter/mouseleave) or keyboard focus —
 * clicking a tab still navigates exactly as it always did.
 *
 * This intentionally does NOT use position:absolute/fixed: the flyout
 * panel is a normal block element appended as a sibling of
 * `.md-tabs__list` inside the tabs bar's own `.md-grid` wrapper, animated
 * open via `max-height` so it participates in real layout -- the sticky
 * header genuinely grows taller and pushes the page content below it
 * down, instead of overlaying a floating card on top of it.
 *
 * The list of pages behind each tab isn't available in any single page's
 * rendered HTML (Material only renders a section's children into the
 * *sidebar*, and only for the section the current page is inside) -- so
 * hooks.py generates docs/javascripts/nav-data.js at build time with the
 * full tree (every top-level tab, each with its `children` if it's a
 * group), which this script reads as `window.PGRR_NAV_TREE`.
 *
 * Those URLs are root-relative (e.g. "installation/", "" for the home
 * page) because nav-data.js is one static file shared by every page --
 * it has no way to know, for itself, how deep the *current* page is
 * relative to the site root, which is what a real relative link needs
 * (e.g. "installation/" from the testing/ page would wrongly resolve to
 * testing/installation/). Rather than guess, this reads the correct
 * prefix straight from Material's own logo link (`.md-logo`, present on
 * every page, always pointing at the site root -- "." on the root page
 * itself, ".." from any one-level-deep page, etc.) and builds every
 * flyout href from that.
 */
(function () {
  function setup() {
    var tabsList = document.querySelector(".md-tabs__list");
    var grid = tabsList && tabsList.parentElement; // .md-grid
    var tree = window.PGRR_NAV_TREE;
    var logo = document.querySelector(".md-logo");
    if (!tabsList || !grid || !tree || tree.length === 0 || !logo) {
      return;
    }
    if (tabsList.dataset.pgrrFlyoutInit) {
      return;
    }
    tabsList.dataset.pgrrFlyoutInit = "true";

    var rootPrefix = logo.getAttribute("href"); // e.g. "." or ".."

    function hrefFor(url) {
      return /^[a-z]+:\/\//i.test(url) ? url : rootPrefix + "/" + url;
    }

    var tabItems = Array.prototype.slice
      .call(tabsList.children)
      .filter(function (el) {
        return el.tagName === "LI";
      });
    // Material renders exactly one <li> per top-level nav.yml entry, in
    // the same order -- so tabItems[i] and tree[i] describe the same tab.
    if (tabItems.length !== tree.length) {
      return; // Nav changed shape without a matching rebuild; bail out safely.
    }

    var panel = document.createElement("div");
    panel.className = "pgrr-tabs-flyout";
    panel.hidden = true;
    var panelList = document.createElement("ul");
    panel.appendChild(panelList);
    grid.appendChild(panel);

    var CLOSE_DELAY_MS = 150;
    var closeTimer = null;
    var openFor = null; // the <li> currently expanded, or null

    function renderChildren(children) {
      panelList.innerHTML = "";
      children.forEach(function (child) {
        var li = document.createElement("li");
        if (child.url !== undefined) {
          var a = document.createElement("a");
          a.href = hrefFor(child.url);
          a.textContent = child.title;
          li.appendChild(a);
        } else {
          // No plausible case today (groups are one level deep), but
          // degrade to a plain label rather than silently drop it.
          li.textContent = child.title;
        }
        panelList.appendChild(li);
      });
    }

    function openPanelFor(li, entry) {
      clearTimeout(closeTimer);
      if (openFor === li && !panel.hidden) {
        return;
      }
      renderChildren(entry.children);
      openFor = li;
      panel.hidden = false;
      // Force layout so the transition from the previous height (0, or
      // whatever the last-open panel's content height was) is visible
      // rather than jumping straight to the new content's height.
      panel.style.maxHeight = "0px";
      // eslint-disable-next-line no-unused-expressions
      panel.offsetHeight; // Reflow before changing max-height again.
      panel.style.maxHeight = panel.scrollHeight + "px";
    }

    var CLOSE_TRANSITION_MS = 220; // matches extra.css's 200ms transition, plus slack

    function closePanelNow() {
      panel.style.maxHeight = "0px";
      openFor = null;
      // Wait for the collapse transition to actually finish before
      // setting `hidden` (display: none) -- doing it immediately would
      // cut the animation off and make the panel just vanish. If
      // another tab opens before this fires, openFor will already be
      // set again by then, so this no-ops instead of hiding it.
      setTimeout(function () {
        if (!openFor) {
          panel.hidden = true;
        }
      }, CLOSE_TRANSITION_MS);
    }

    function scheduleClose() {
      clearTimeout(closeTimer);
      closeTimer = setTimeout(closePanelNow, CLOSE_DELAY_MS);
    }

    tabItems.forEach(function (li, i) {
      var entry = tree[i];
      if (!entry.children || entry.children.length === 0) {
        return; // Plain tab (e.g. "Home") — no flyout.
      }
      li.addEventListener("mouseenter", function () {
        openPanelFor(li, entry);
      });
      li.addEventListener("mouseleave", scheduleClose);
      var link = li.querySelector(".md-tabs__link");
      if (link) {
        link.addEventListener("focus", function () {
          openPanelFor(li, entry);
        });
      }
    });

    panel.addEventListener("mouseenter", function () {
      clearTimeout(closeTimer);
    });
    panel.addEventListener("mouseleave", scheduleClose);

    // Keyboard: close once focus leaves both the active tab and the panel.
    document.addEventListener("focusout", function (event) {
      if (!openFor) {
        return;
      }
      var next = event.relatedTarget;
      if (next && (openFor.contains(next) || panel.contains(next))) {
        return;
      }
      setTimeout(function () {
        var active = document.activeElement;
        if (openFor && !openFor.contains(active) && !panel.contains(active)) {
          closePanelNow();
        }
      }, 0);
    });
    panel.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && openFor) {
        var link = openFor.querySelector(".md-tabs__link");
        closePanelNow();
        if (link) {
          link.focus();
        }
      }
    });

    // If the window resizes while a panel is open, recompute its height
    // (wrapping/reflow inside the panel can change how tall it is).
    window.addEventListener("resize", function () {
      if (openFor && !panel.hidden) {
        panel.style.maxHeight = panel.scrollHeight + "px";
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", setup);
  } else {
    setup();
  }
})();
