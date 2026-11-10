/*
 * Progressive enhancement for docs/roadmap.md: a status filter, a "show more"
 * button and "expand all". The page is fully readable without it (every card
 * is visible and each card's details is a native <details>).
 */
(function () {
  "use strict";

  var LIMIT = 9;
  var STATUS_ORDER = ["planned", "proposed", "gap", "followup", "deferred"];
  var STATUS_LABEL = {
    planned: "Planned",
    proposed: "Proposed",
    gap: "Known gaps",
    followup: "Follow-ups",
    deferred: "Deferred"
  };

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  function init() {
    var list = document.querySelector(".rm-list");
    if (!list || list.getAttribute("data-rm-ready")) return;
    list.setAttribute("data-rm-ready", "1");

    var items = Array.prototype.slice.call(list.querySelectorAll(".rm-item"));
    var state = { status: "all", area: null, showAll: false };

    Array.prototype.forEach.call(document.querySelectorAll("[data-rm-count]"), function (n) {
      n.textContent = String(items.length);
    });

    // --- toolbar ---
    var toolbar = el("div", "rm-toolbar");
    var chips = el("div", "rm-chips");
    chips.setAttribute("role", "group");
    chips.setAttribute("aria-label", "Filter by status");
    var chipEls = {};

    function icon(key) {
      var ns = "http://www.w3.org/2000/svg";
      var svg = document.createElementNS(ns, "svg");
      svg.setAttribute("class", "rm-icon");
      svg.setAttribute("viewBox", "0 0 24 24");
      svg.setAttribute("aria-hidden", "true");
      var use = document.createElementNS(ns, "use");
      use.setAttributeNS("http://www.w3.org/1999/xlink", "xlink:href", "#rm-i-" + key);
      use.setAttribute("href", "#rm-i-" + key);
      svg.appendChild(use);
      return svg;
    }

    function addChip(key, label, n) {
      var b = el("button", "rm-chip");
      b.type = "button";
      b.setAttribute("data-status", key);
      if (key !== "all") b.appendChild(icon(key));
      b.appendChild(document.createTextNode(label));
      b.appendChild(el("small", null, String(n)));
      b.addEventListener("click", function () {
        state.status = key;
        state.showAll = false;
        render();
      });
      chipEls[key] = b;
      chips.appendChild(b);
    }

    addChip("all", "All", items.length);
    STATUS_ORDER.forEach(function (s) {
      var n = items.filter(function (i) { return i.getAttribute("data-status") === s; }).length;
      if (n) addChip(s, STATUS_LABEL[s], n);
    });
    toolbar.appendChild(chips);

    var pillHost = el("span");
    toolbar.appendChild(pillHost);

    var expandBtn = el("button", "rm-btn", "Expand all");
    expandBtn.type = "button";
    toolbar.appendChild(expandBtn);

    var count = el("p", "rm-count");
    count.setAttribute("aria-live", "polite");
    var moreWrap = el("div", "rm-more-wrap");
    var moreBtn = el("button", "rm-btn");
    moreBtn.type = "button";
    moreWrap.appendChild(moreBtn);

    list.parentNode.insertBefore(toolbar, list);
    list.parentNode.insertBefore(count, list);
    list.parentNode.insertBefore(moreWrap, list.nextSibling);

    function visibleItems() {
      return items.filter(function (i) { return !i.hidden; });
    }

    expandBtn.addEventListener("click", function () {
      var vis = visibleItems();
      var open = vis.some(function (i) {
        var d = i.querySelector("details");
        return d && !d.open;
      });
      vis.forEach(function (i) {
        var d = i.querySelector("details");
        if (d) d.open = open;
      });
      expandBtn.textContent = open ? "Collapse all" : "Expand all";
    });

    moreBtn.addEventListener("click", function () {
      state.showAll = true;
      render();
    });

    function render() {
      var matches = items.filter(function (i) {
        return (state.status === "all" || i.getAttribute("data-status") === state.status) &&
               (!state.area || i.getAttribute("data-area") === state.area);
      });
      var limited = state.status === "all" && !state.area && !state.showAll;
      var shown = limited ? matches.slice(0, LIMIT) : matches;

      items.forEach(function (i) { i.hidden = shown.indexOf(i) === -1; });
      Object.keys(chipEls).forEach(function (k) {
        chipEls[k].setAttribute("aria-pressed", String(k === state.status));
      });

      pillHost.textContent = "";
      if (state.area) {
        var pill = el("span", "rm-area-pill", "Area: " + state.area);
        var x = el("button", null, "×");
        x.type = "button";
        x.setAttribute("aria-label", "Clear area filter");
        x.addEventListener("click", function () { state.area = null; render(); });
        pill.appendChild(x);
        pillHost.appendChild(pill);
      }

      var hiddenCount = matches.length - shown.length;
      moreWrap.hidden = hiddenCount <= 0;
      moreBtn.textContent = "Show " + hiddenCount + " more";
      count.textContent = "Showing " + shown.length + " of " + items.length + " items";
      expandBtn.textContent = "Expand all";
    }

    Array.prototype.forEach.call(document.querySelectorAll(".rm-feature[data-area]"), function (a) {
      a.addEventListener("click", function (e) {
        e.preventDefault();
        state.area = a.getAttribute("data-area");
        state.status = "all";
        render();
        var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
        toolbar.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
      });
    });

    render();
  }

  if (window.document$ && window.document$.subscribe) {
    window.document$.subscribe(init);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
