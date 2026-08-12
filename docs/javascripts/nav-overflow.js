/*
 * Collapses overflowing top-level nav tabs into a hover-only dropdown,
 * instead of letting the tabs bar scroll sideways.
 *
 * Material's own `.md-tabs__list` is `overflow: auto; white-space: nowrap`,
 * i.e. a horizontally-scrollable strip with its native scrollbar hidden
 * (`scrollbar-width: none`) -- so with this repo's 14 top-level nav
 * entries, several tabs were only reachable by scrolling/dragging
 * sideways, with no visual hint that they exist. extra.css turns that
 * `overflow` into `hidden` (clipping instead of scrolling) so nothing
 * visibly spills out of the bar; this script is what keeps the clipped
 * tabs reachable.
 *
 * The overflow indicator is a plain "⋯" appended to the tab row -- not a
 * labelled "More" button -- styled identically to a normal tab so it
 * reads as part of the row rather than a foreign control. Hovering (or
 * focusing, for keyboard use) it reveals the hidden tabs in a dropdown.
 *
 * That dropdown is deliberately NOT nested inside `.md-tabs__list`: since
 * that element clips overflow (see above), anything positioned below its
 * own box -- which a `top: 100%` dropdown necessarily is -- would be
 * clipped away too, appearing to "not work" even though the CSS :hover
 * rule revealing it is perfectly correct. Instead the dropdown is a
 * single element appended once to <body>, shown/hidden via JS
 * (mouseenter/mouseleave with a short close delay so moving the pointer
 * from the trigger into the menu doesn't close it, plus focus/blur for
 * the keyboard), and positioned with `position: fixed` from the
 * trigger's live bounding box every time it opens.
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

    // ---- The trigger: lives in the tab row, styled as a tab. ----
    var moreItem = document.createElement("li");
    moreItem.className = "md-tabs__item pgrr-tabs-more";
    var trigger = document.createElement("button");
    trigger.type = "button";
    trigger.className = "md-tabs__link pgrr-tabs-more__trigger";
    trigger.setAttribute("aria-haspopup", "true");
    trigger.setAttribute("aria-expanded", "false");
    trigger.setAttribute("aria-label", "More sections");
    trigger.textContent = "⋯"; // ⋯
    moreItem.appendChild(trigger);
    tabsList.appendChild(moreItem);

    // ---- The dropdown: lives at the end of <body>, not inside the
    // clipped tabs list (see file doc comment above). ----
    var menu = document.createElement("ul");
    menu.className = "pgrr-tabs-more__menu";
    menu.hidden = true;
    // Parked off-screen before the first real position is computed, so
    // that briefly un-hiding it to measure its width (see positionMenu)
    // never paints it at its DOM-order fallback position (the very end
    // of <body>, since `position: fixed` with no top/left set renders at
    // its static position).
    menu.style.top = "-9999px";
    menu.style.left = "-9999px";
    document.body.appendChild(menu);

    var CLOSE_DELAY_MS = 150;
    var closeTimer = null;

    function positionMenu() {
      var rect = trigger.getBoundingClientRect();
      menu.style.top = Math.round(rect.bottom) + "px";
      // Right-align the menu to the trigger, then clamp so it never runs
      // off the left edge of the viewport.
      var menuWidth = menu.offsetWidth;
      var left = Math.round(rect.right - menuWidth);
      menu.style.left = Math.max(8, left) + "px";
    }

    function openMenu() {
      clearTimeout(closeTimer);
      if (menu.children.length === 0) {
        return;
      }
      menu.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
      positionMenu();
    }

    function closeMenuNow() {
      menu.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
    }

    function scheduleClose() {
      clearTimeout(closeTimer);
      closeTimer = setTimeout(closeMenuNow, CLOSE_DELAY_MS);
    }

    trigger.addEventListener("mouseenter", openMenu);
    trigger.addEventListener("mouseleave", scheduleClose);
    trigger.addEventListener("focus", openMenu);
    menu.addEventListener("mouseenter", function () {
      clearTimeout(closeTimer);
    });
    menu.addEventListener("mouseleave", scheduleClose);
    // Keyboard: close once focus leaves both the trigger and the menu.
    document.addEventListener("focusout", function (event) {
      var next = event.relatedTarget;
      if (next && (trigger.contains(next) || menu.contains(next))) {
        return;
      }
      if (document.activeElement === trigger || menu.contains(document.activeElement)) {
        return;
      }
      closeMenuNow();
    });
    // Click support purely as a touch/trackpad fallback (hover doesn't
    // exist on touch); does not change the hover-first behaviour above.
    trigger.addEventListener("click", function () {
      if (menu.hidden) {
        openMenu();
      } else {
        closeMenuNow();
      }
    });

    // Keyboard support: the menu lives at the end of <body> in DOM order
    // (see the file doc comment on why), so it is NOT next in the Tab
    // sequence after the trigger. ArrowDown/Enter/Space on the trigger
    // jumps focus straight into the menu instead of relying on Tab
    // order; Escape inside the menu returns focus to the trigger.
    trigger.addEventListener("keydown", function (event) {
      if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        openMenu();
        var first = menu.querySelector("a");
        if (first) {
          first.focus();
        }
      }
    });
    menu.addEventListener("keydown", function (event) {
      if (event.key === "Escape") {
        closeMenuNow();
        trigger.focus();
      }
    });

    // Reserved width for the trigger itself. Not measured dynamically
    // (that would require briefly showing it, which is more complexity
    // than the payoff justifies) -- generous enough for the "⋯" glyph
    // plus its tab padding in any of Material's bundled fonts.
    var TRIGGER_WIDTH = 56;

    function relayout() {
      // Tabs bar is hidden below the desktop breakpoint; nothing to do.
      if (tabsList.clientWidth === 0) {
        return;
      }

      // Reset: everything back visible, dropdown emptied and closed.
      items.forEach(function (li) {
        li.style.display = "";
      });
      menu.innerHTML = "";
      closeMenuNow();
      moreItem.style.display = "none";
      moreItem.classList.remove("md-tabs__item--active");

      var available = tabsList.clientWidth;
      var total = items.reduce(function (sum, li) {
        return sum + li.getBoundingClientRect().width;
      }, 0);
      if (total <= available) {
        return; // Everything fits — no overflow indicator needed.
      }

      moreItem.style.display = "";
      var budget = available - TRIGGER_WIDTH;
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
        menu.appendChild(entry);
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
