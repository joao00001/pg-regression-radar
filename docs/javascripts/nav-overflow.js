/*
 * Collapses overflowing top-level nav tabs into a "More" dropdown instead
 * of letting the tabs bar scroll sideways.
 *
 * Material's own `.md-tabs__list` is `overflow: auto; white-space: nowrap`,
 * i.e. a horizontally-scrollable strip with its native scrollbar hidden
 * (`scrollbar-width: none`) -- so with this repo's 14 top-level nav
 * entries, several tabs are only reachable by scrolling/dragging
 * sideways, with no visual hint that they exist. extra.css turns that
 * `overflow` into `hidden` (clipping instead of scrolling); this script is
 * what keeps the clipped tabs reachable, by moving whichever tabs don't
 * fit into a "More" menu that expands on hover/focus.
 *
 * The tabs bar (`.md-tabs`) is hidden entirely below Material's own
 * 76.234375em breakpoint (mobile/tablet use the drawer nav instead), so
 * this only ever needs to run at desktop widths -- see the width guard in
 * relayout() below.
 */
(function () {
  function setup() {
    var tabsList = document.querySelector(".md-tabs__list");
    if (!tabsList || tabsList.dataset.pgrrOverflowInit) {
      return;
    }
    tabsList.dataset.pgrrOverflowInit = "true";

    var items = Array.prototype.slice
      .call(tabsList.children)
      .filter(function (el) {
        return el.tagName === "LI";
      });
    if (items.length === 0) {
      return;
    }

    var moreItem = document.createElement("li");
    moreItem.className = "md-tabs__item pgrr-tabs-more";
    moreItem.innerHTML =
      '<a href="#" class="md-tabs__link pgrr-tabs-more__link" aria-haspopup="true">' +
      'More <span aria-hidden="true">▾</span></a>' +
      '<ul class="pgrr-tabs-more__menu"></ul>';
    tabsList.appendChild(moreItem);

    var moreLink = moreItem.querySelector(".pgrr-tabs-more__link");
    var moreMenu = moreItem.querySelector(".pgrr-tabs-more__menu");
    moreLink.addEventListener("click", function (event) {
      event.preventDefault();
    });

    // Reserved width for the "More" toggle itself. Not measured
    // dynamically (that would require briefly showing it, which is more
    // complexity than the payoff justifies) -- generous enough for the
    // "More ▾" label in any of Material's bundled fonts.
    var MORE_BUTTON_WIDTH = 96;

    function relayout() {
      // Tabs bar is hidden below the desktop breakpoint; nothing to do.
      if (tabsList.clientWidth === 0) {
        return;
      }

      // Reset: everything back in its original place, dropdown emptied.
      items.forEach(function (li) {
        if (li.nextSibling !== moreItem && li.parentElement === tabsList) {
          tabsList.insertBefore(li, moreItem);
        }
        li.style.display = "";
      });
      while (moreMenu.firstChild) {
        moreMenu.removeChild(moreMenu.firstChild);
      }
      moreItem.style.display = "none";
      moreItem.classList.remove("md-tabs__item--active");

      var available = tabsList.clientWidth;
      var total = items.reduce(function (sum, li) {
        return sum + li.getBoundingClientRect().width;
      }, 0);
      if (total <= available) {
        return; // Everything fits — no "More" menu needed.
      }

      moreItem.style.display = "";
      var budget = available - MORE_BUTTON_WIDTH;
      var running = 0;
      var overflowStart = items.length;
      for (var i = 0; i < items.length; i++) {
        running += items[i].getBoundingClientRect().width;
        if (running > budget) {
          overflowStart = i;
          break;
        }
      }

      var anyActiveHidden = false;
      for (var j = overflowStart; j < items.length; j++) {
        var li = items[j];
        var link = li.querySelector(".md-tabs__link");
        if (!link) {
          continue;
        }
        var entry = document.createElement("li");
        var a = document.createElement("a");
        a.href = link.getAttribute("href");
        a.textContent = link.textContent.trim();
        if (li.classList.contains("md-tabs__item--active")) {
          a.classList.add("pgrr-tabs-more__link--active");
          anyActiveHidden = true;
        }
        entry.appendChild(a);
        moreMenu.appendChild(entry);
        li.style.display = "none";
      }

      if (anyActiveHidden) {
        moreItem.classList.add("md-tabs__item--active");
      }
    }

    relayout();

    var resizeTimer;
    window.addEventListener("resize", function () {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(relayout, 150);
    });

    // Web fonts loading after the first layout pass can change tab widths
    // enough to matter (14 tabs, mostly multi-word labels).
    if (document.fonts && document.fonts.ready) {
      document.fonts.ready.then(relayout);
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", setup);
  } else {
    setup();
  }
})();
