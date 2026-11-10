/*
 * Progressive enhancement for docs/roadmap.md: a status filter, a "show more"
 * button and "expand all". The page is fully readable without it (every cell
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

  /*
   * The title block's background: after shadcn.io's "Beams". Twenty thin
   * curves cross the block on the diagonal, each drawn once as an SVG stroke
   * and lit by a bright head that travels along it, every curve on its
   * own 4-7 s cycle so they never move in step. The paths come from a seeded
   * generator so the pattern is the same on every load. Decorative: aria-hidden, no pointer
   * events, and still under prefers-reduced-motion.
   */
  function beams() {
    var head = document.querySelector(".pgrr-roadmap .rm-head");
    if (!head || head.querySelector(".rm-bg")) return;
    var NS = "http://www.w3.org/2000/svg";
    var seed = 11;
    function rnd() {
      seed = (seed + 0x6d2b79f5) | 0;
      var t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    }

    var box = el("div", "rm-bg");
    box.setAttribute("aria-hidden", "true");
    var svg = document.createElementNS(NS, "svg");
    svg.setAttribute("viewBox", "0 0 1200 420");
    svg.setAttribute("preserveAspectRatio", "xMidYMid slice");

    var defs = document.createElementNS(NS, "defs");
    var grad = document.createElementNS(NS, "linearGradient");
    grad.setAttribute("id", "rm-beam-grad");
    grad.setAttribute("gradientUnits", "userSpaceOnUse");
    grad.setAttribute("x1", "0");
    grad.setAttribute("y1", "0");
    grad.setAttribute("x2", "1200");
    grad.setAttribute("y2", "420");
    [["0", "rm-stop-a"], ["0.55", "rm-stop-b"], ["1", "rm-stop-c"]].forEach(function (st) {
      var stop = document.createElementNS(NS, "stop");
      stop.setAttribute("offset", st[0]);
      stop.setAttribute("class", st[1]);
      grad.appendChild(stop);
    });
    defs.appendChild(grad);
    svg.appendChild(defs);

    var N = 20;
    var beams = [];
    for (var i = 0; i < N; i++) {
      var t = i / (N - 1);
      var x0 = -80, y0 = -120 + t * 520 + (rnd() - 0.5) * 40;
      var x1 = 1280, y1 = 20 + t * 560 + (rnd() - 0.5) * 60;
      var d = "M" + x0 + " " + y0.toFixed(1) +
        " C" + (x0 + 360 + rnd() * 120).toFixed(1) + " " + (y0 + 40 + rnd() * 120).toFixed(1) +
        " " + (x1 - 420 - rnd() * 120).toFixed(1) + " " + (y1 - 160 - rnd() * 120).toFixed(1) +
        " " + x1 + " " + y1.toFixed(1);

      var guide = document.createElementNS(NS, "path");
      guide.setAttribute("d", d);
      guide.setAttribute("class", "rm-guide");
      svg.appendChild(guide);

      var beam = document.createElementNS(NS, "path");
      beam.setAttribute("d", d);
      beam.setAttribute("class", "rm-beam");
      svg.appendChild(beam);
      beams.push({ el: beam, dur: 4 + rnd() * 3.2, delay: rnd() * 7, rest: rnd() });
    }

    box.appendChild(svg);
    head.insertBefore(box, head.firstChild);

    // Dash lengths come from the real path length, in user units, and the head
    // is moved with the Web Animations API: no pathLength or CSS-variable
    // tricks that some browsers handle differently.
    var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    beams.forEach(function (b) {
      var len = b.el.getTotalLength();
      var headLen = len * 0.3;
      b.el.style.strokeDasharray = headLen + " " + len * 2;
      if (reduce) {
        b.el.style.strokeDashoffset = String(headLen - b.rest * len * 1.2);
        b.el.style.opacity = "0.8";
        return;
      }
      b.el.animate(
        [
          { strokeDashoffset: headLen, opacity: 0, offset: 0 },
          { strokeDashoffset: headLen - len * 0.12 * 1.3, opacity: 1, offset: 0.12 },
          { strokeDashoffset: -len * 0.88, opacity: 1, offset: 0.88 },
          { strokeDashoffset: -len, opacity: 0, offset: 1 }
        ],
        { duration: b.dur * 1000, delay: -b.delay * 1000, iterations: Infinity, easing: "linear" }
      );
    });
  }

  var reduce = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  var GLYPHS = "01234567890/\\<>+=-_:;#%&*";

  /*
   * Text that resolves out of noise, like the home diagram's and the math
   * page's figures: each character of every text node under `root` is replaced
   * by a random glyph and settles, left to right, within `ms`. The markup (a
   * <code> inside a title, say) is kept; only the text nodes change. A slight
   * blur (the rm-scr class) clears as it settles.
   */
  function scramble(root, ms) {
    if (reduce || !root || !root.isConnected) return;
    var nodes = [];
    var walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    var n;
    while ((n = walker.nextNode())) {
      if (n.data.trim()) nodes.push({ node: n, text: n.data });
    }
    if (!nodes.length) return;
    var total = nodes.reduce(function (t, x) { return t + x.text.length; }, 0);
    cancelAnimationFrame(root._rmRaf);
    root.classList.add("rm-scr");
    var start = performance.now();
    function frame(now) {
      var k = Math.min(1, (now - start) / ms);
      var done = Math.floor(k * total * 1.15);
      var seen = 0;
      nodes.forEach(function (x) {
        var out = "";
        for (var i = 0; i < x.text.length; i++) {
          var c = x.text.charAt(i);
          out += seen + i < done || c === " " ? c : GLYPHS.charAt((Math.random() * GLYPHS.length) | 0);
        }
        seen += x.text.length;
        x.node.data = out;
      });
      if (k < 1) {
        root._rmRaf = requestAnimationFrame(frame);
      } else {
        nodes.forEach(function (x) { x.node.data = x.text; });
        root.classList.remove("rm-scr");
      }
    }
    root._rmRaf = requestAnimationFrame(frame);
  }

  /*
   * Reveal on scroll: each cell fades up out of a slight blur as it enters the
   * viewport, a row at a time (a short stagger across the cells that arrive
   * together). Classes are added here, so with no JavaScript, or with reduced
   * motion, everything is simply visible. Items that are hidden by the filter
   * are shown by render() with is-in already set.
   */
  function reveal(items) {
    if (reduce || !("IntersectionObserver" in window)) return;
    var cells = Array.prototype.slice.call(
      document.querySelectorAll(".pgrr-roadmap .rm-stat, .pgrr-roadmap .rm-feature, .pgrr-roadmap .rm-overview, .pgrr-roadmap details.rm-notes")
    ).concat(items);
    var io = new IntersectionObserver(function (entries) {
      var k = 0;
      entries.forEach(function (e) {
        if (!e.isIntersecting) return;
        var t = e.target;
        io.unobserve(t);
        t.style.setProperty("--d", Math.min(k++, 6) * 70 + "ms");
        t.classList.add("is-in");
      });
    }, { threshold: 0.12, rootMargin: "0px 0px -6% 0px" });
    cells.forEach(function (c) {
      if (c.classList.contains("is-in")) return;
      c.classList.add("rm-reveal");
      io.observe(c);
    });
  }

  /*
   * Traces on the grid: a few short red pulses enter from the edges of the item
   * list and run along the hairlines between the cells, turning at the
   * crossings, until they leave or reach a dead end. The lines are read from
   * the cells themselves (every visible item's right and bottom edge, plus the
   * list's left and top), so it follows the filter, "show more" and an opened
   * card with no extra bookkeeping. A canvas drawn only while the list is on
   * screen; off under prefers-reduced-motion.
   */
  function traces(list) {
    if (reduce || !list || list._rmTraces) return;
    list._rmTraces = true;
    var cv = document.createElement("canvas");
    cv.className = "rm-trace";
    cv.setAttribute("aria-hidden", "true");
    list.appendChild(cv);
    var ctx = cv.getContext("2d");

    var W = 0, H = 0, dpr = 1, segs = [], pulses = [], color = "#e5484d";
    var SPEED = 190;      // px per second
    var TRAIL = 170;      // px of tail
    var MAX_PULSES = 6;
    var TOL = 3;
    var onScreen = false, last = 0, nextSpawn = 0, raf = 0, relayoutTimer = 0;

    function layout() {
      var cs = getComputedStyle(list);
      color = cs.getPropertyValue("--h-accent-solid").trim() || color;
      var rect = list.getBoundingClientRect();
      W = Math.round(list.clientWidth);
      H = Math.round(list.clientHeight);
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      cv.width = W * dpr;
      cv.height = H * dpr;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      segs = [];
      Array.prototype.forEach.call(list.querySelectorAll(".rm-item"), function (it) {
        if (it.hidden || !it.offsetWidth) return;
        var x = it.offsetLeft - list.clientLeft, y = it.offsetTop - list.clientTop;
        var w = it.offsetWidth, h = it.offsetHeight;
        segs.push({ ax: x + w - 0.5, ay: y, bx: x + w - 0.5, by: y + h });
        segs.push({ ax: x, ay: y + h - 0.5, bx: x + w, by: y + h - 0.5 });
        if (x < 2) segs.push({ ax: 0.5, ay: y, bx: 0.5, by: y + h });
        if (y < 2) segs.push({ ax: x, ay: 0.5, bx: x + w, by: 0.5 });
      });
      pulses = [];
      void rect;
    }

    function near(px, py, qx, qy) {
      return Math.abs(px - qx) <= TOL && Math.abs(py - qy) <= TOL;
    }

    // a segment to leave a crossing by: any other segment with an end there
    function branch(px, py, from) {
      var out = [];
      segs.forEach(function (s) {
        if (s === from) return;
        if (near(px, py, s.ax, s.ay)) out.push({ s: s, fx: s.ax, fy: s.ay, tx: s.bx, ty: s.by });
        else if (near(px, py, s.bx, s.by)) out.push({ s: s, fx: s.bx, fy: s.by, tx: s.ax, ty: s.ay });
      });
      return out[(Math.random() * out.length) | 0] || null;
    }

    function spawn() {
      var edge = segs.filter(function (s) {
        return [[s.ax, s.ay], [s.bx, s.by]].some(function (p) {
          return p[0] <= TOL || p[0] >= W - TOL || p[1] <= TOL;
        });
      });
      if (!edge.length) return;
      var s = edge[(Math.random() * edge.length) | 0];
      var a = s.ax <= TOL || s.ax >= W - TOL || s.ay <= TOL;
      var from = a ? { x: s.ax, y: s.ay, tx: s.bx, ty: s.by } : { x: s.bx, y: s.by, tx: s.ax, ty: s.ay };
      pulses.push({
        seg: s, x: from.x, y: from.y, tx: from.tx, ty: from.ty,
        trail: [{ x: from.x, y: from.y }], age: 0, dist: 0, dead: false
      });
    }

    function step(p, dt) {
      var move = SPEED * dt;
      p.dist += move;
      while (move > 0 && !p.dead) {
        var dx = p.tx - p.x, dy = p.ty - p.y, len = Math.hypot(dx, dy);
        if (len <= move) {
          p.x = p.tx;
          p.y = p.ty;
          move -= len;
          p.trail.push({ x: p.x, y: p.y });
          var nb = p.dist > 1400 ? null : branch(p.x, p.y, p.seg);
          if (!nb) {
            p.dead = true;
          } else {
            p.seg = nb.s;
            p.x = nb.fx;
            p.y = nb.fy;
            p.tx = nb.tx;
            p.ty = nb.ty;
          }
        } else {
          p.x += (dx / len) * move;
          p.y += (dy / len) * move;
          move = 0;
        }
      }
      p.trail.push({ x: p.x, y: p.y });
      var total = 0, k = p.trail.length - 1;
      while (k > 0 && total < TRAIL) {
        total += Math.hypot(p.trail[k].x - p.trail[k - 1].x, p.trail[k].y - p.trail[k - 1].y);
        k--;
      }
      if (k > 0) p.trail.splice(0, k);
    }

    function draw(p, fade) {
      var n = p.trail.length;
      ctx.lineCap = "round";
      ctx.lineJoin = "round";
      ctx.strokeStyle = color;
      ctx.lineWidth = 2;
      var acc = 0, total = 0, i;
      for (i = 1; i < n; i++) total += Math.hypot(p.trail[i].x - p.trail[i - 1].x, p.trail[i].y - p.trail[i - 1].y);
      for (i = n - 1; i > 0; i--) {
        var seg = Math.hypot(p.trail[i].x - p.trail[i - 1].x, p.trail[i].y - p.trail[i - 1].y);
        var t = 1 - acc / Math.max(total, 1);
        ctx.globalAlpha = Math.max(0, Math.pow(t, 1.4)) * fade;
        ctx.beginPath();
        ctx.moveTo(p.trail[i - 1].x, p.trail[i - 1].y);
        ctx.lineTo(p.trail[i].x, p.trail[i].y);
        ctx.stroke();
        acc += seg;
      }
      ctx.globalAlpha = 1;
    }

    function frame(now) {
      raf = 0;
      if (!onScreen || document.hidden) return;
      var dt = Math.min(0.05, (now - last) / 1000 || 0.016);
      last = now;
      ctx.clearRect(0, 0, W, H);
      if (segs.length) {
        if (pulses.length < MAX_PULSES && now >= nextSpawn) {
          spawn();
          nextSpawn = now + 350 + Math.random() * 900;
        }
        pulses.forEach(function (p) {
          if (p.dead) {
            // let the tail drain into the head's last point
            p.trail.shift();
          } else {
            step(p, dt);
          }
          p.age += dt;
          draw(p, Math.min(1, p.age * 3));
        });
        pulses = pulses.filter(function (p) { return !(p.dead && p.trail.length < 2); });
      }
      raf = requestAnimationFrame(frame);
    }

    function kick() {
      if (!raf && onScreen && !document.hidden) {
        last = performance.now();
        raf = requestAnimationFrame(frame);
      }
    }

    function relayout() {
      clearTimeout(relayoutTimer);
      relayoutTimer = setTimeout(function () { layout(); kick(); }, 140);
    }

    layout();
    if ("ResizeObserver" in window) new ResizeObserver(relayout).observe(list);
    new MutationObserver(relayout).observe(list, { attributes: true, subtree: true, attributeFilter: ["hidden", "open", "class"] });
    window.addEventListener("resize", relayout);
    document.addEventListener("visibilitychange", kick);
    new IntersectionObserver(function (entries) {
      onScreen = entries[0].isIntersecting;
      if (onScreen) kick();
    }).observe(list);
  }

  function init() {
    beams();
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
        render(true);
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

    /*
     * "Read more". The text of each item is wrapped in .rm-body so it can fade
     * in on its own, and opening or closing one is animated with FLIP: the
     * other cards are measured before and after the change and glide from
     * where they were to where they land, instead of jumping when the open
     * card takes the full row. The opening card itself grows out of its old
     * box (a clip that opens) and its text settles out of noise. With reduced
     * motion, or no JavaScript, it is the plain native <details>.
     */
    var EASE = "cubic-bezier(0.2, 0.7, 0.2, 1)";

    items.forEach(function (it) {
      var d = it.querySelector("details");
      if (!d || d.querySelector(":scope > .rm-body")) return;
      var body = el("div", "rm-body");
      Array.prototype.slice.call(d.children).forEach(function (c) {
        if (c.tagName !== "SUMMARY") body.appendChild(c);
      });
      d.appendChild(body);
    });

    function snapshot() {
      return items.filter(function (i) { return !i.hidden; }).map(function (i) {
        return { i: i, r: i.getBoundingClientRect() };
      });
    }

    function glide(first, skip) {
      first.forEach(function (f) {
        if (f.i === skip) return;
        var l = f.i.getBoundingClientRect();
        var dx = f.r.left - l.left;
        var dy = f.r.top - l.top;
        if (Math.abs(dx) + Math.abs(dy) > 1) {
          f.i.animate(
            [{ transform: "translate(" + dx + "px," + dy + "px)" }, { transform: "none" }],
            { duration: 540, easing: EASE }
          );
        }
      });
    }

    function openItem(it) {
      var d = it.querySelector("details");
      var body = d.querySelector(":scope > .rm-body");
      it._busy = true;
      var first = snapshot();
      var me = first.filter(function (f) { return f.i === it; })[0];
      d.open = true;
      var l = it.getBoundingClientRect();
      glide(first, it);

      var grow = it.animate(
        [
          {
            transform: "translate(" + (me.r.left - l.left) + "px," + (me.r.top - l.top) + "px)",
            clipPath: "inset(0 " + Math.max(0, (1 - me.r.width / l.width) * 100) + "% " +
              Math.max(0, (1 - me.r.height / l.height) * 100) + "% 0)"
          },
          { transform: "none", clipPath: "inset(0 0% 0% 0)" }
        ],
        { duration: 560, easing: EASE }
      );
      body.animate(
        [{ opacity: 0, transform: "translateY(-8px)" }, { opacity: 1, transform: "none" }],
        { duration: 420, delay: 180, easing: EASE, fill: "backwards" }
      );
      setTimeout(function () { scramble(body, 650); }, 160);
      setTimeout(function () {
        var r = it.getBoundingClientRect();
        if (r.top < 96 || r.bottom > window.innerHeight) {
          it.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
      }, 420);
      grow.finished.then(
        function () { it._busy = false; },
        function () { it._busy = false; }
      );
    }

    function closeItem(it) {
      var d = it.querySelector("details");
      var body = d.querySelector(":scope > .rm-body");
      it._busy = true;
      var out = body.animate(
        [{ opacity: 1, transform: "none" }, { opacity: 0, transform: "translateY(-6px)" }],
        { duration: 170, easing: "ease-in", fill: "forwards" }
      );
      out.finished.then(function () {
        var first = snapshot();
        d.open = false;
        out.cancel();
        glide(first, null);
        it._busy = false;
      });
    }

    list.addEventListener("click", function (e) {
      var sum = e.target.closest ? e.target.closest(".rm-item details > summary") : null;
      if (!sum || reduce || !list.contains(sum)) return;
      e.preventDefault();
      var it = sum.closest(".rm-item");
      if (it._busy) return;
      if (sum.parentNode.open) closeItem(it);
      else openItem(it);
    });

    expandBtn.addEventListener("click", function () {
      var vis = visibleItems();
      var open = vis.some(function (i) {
        var d = i.querySelector("details");
        return d && !d.open;
      });
      var first = reduce ? null : snapshot();
      vis.forEach(function (i) {
        var d = i.querySelector("details");
        if (!d || d.open === open) return;
        d.open = open;
        if (open && !reduce) {
          var b = d.querySelector(":scope > .rm-body");
          if (b) b.animate([{ opacity: 0, transform: "translateY(-8px)" }, { opacity: 1, transform: "none" }], { duration: 420, delay: 160, easing: EASE, fill: "backwards" });
        }
      });
      if (first) glide(first, null);
      expandBtn.textContent = open ? "Collapse all" : "Expand all";
    });

    moreBtn.addEventListener("click", function () {
      state.showAll = true;
      render(true);
    });

    var pending = null;

    function settle() {
      if (pending) {
        clearTimeout(pending);
        pending = null;
      }
      items.forEach(function (i) { i.classList.remove("rm-leave", "rm-enter"); });
    }

    function render(animate) {
      var matches = items.filter(function (i) {
        return (state.status === "all" || i.getAttribute("data-status") === state.status) &&
               (!state.area || i.getAttribute("data-area") === state.area);
      });
      var limited = state.status === "all" && !state.area && !state.showAll;
      var shown = limited ? matches.slice(0, LIMIT) : matches;

      settle();
      var wasVisible = items.filter(function (i) { return !i.hidden; });
      var entering = shown.filter(function (i) { return wasVisible.indexOf(i) === -1; });
      var leaving = wasVisible.filter(function (i) { return shown.indexOf(i) === -1; });
      var motion = animate && !reduce;

      function apply() {
        items.forEach(function (i) { i.hidden = shown.indexOf(i) === -1; });
        entering.forEach(function (i, k) {
          i.classList.add("is-in");
          if (!motion) return;
          i.style.setProperty("--i", String(Math.min(k, 8)));
          i.classList.add("rm-enter");
          [".rm-title", ".rm-quarter", ".rm-area"].forEach(function (sel) {
            var t = i.querySelector(sel);
            if (t) setTimeout(function () { scramble(t, 520); }, Math.min(k, 8) * 55);
          });
        });
        if (motion) {
          pending = setTimeout(function () {
            pending = null;
            entering.forEach(function (i) { i.classList.remove("rm-enter"); });
          }, 1100);
        }
      }

      if (motion && leaving.length) {
        leaving.forEach(function (i, k) {
          i.style.setProperty("--i", String(Math.min(k, 8)));
          i.classList.add("rm-leave");
        });
        pending = setTimeout(function () {
          pending = null;
          leaving.forEach(function (i) { i.classList.remove("rm-leave"); });
          apply();
        }, 200 + Math.min(leaving.length, 9) * 25);
      } else {
        apply();
      }

      Object.keys(chipEls).forEach(function (k) {
        chipEls[k].setAttribute("aria-pressed", String(k === state.status));
      });

      pillHost.textContent = "";
      if (state.area) {
        var pill = el("span", "rm-area-pill", "Area: " + state.area);
        var x = el("button", null, "\u00d7");
        x.type = "button";
        x.setAttribute("aria-label", "Clear area filter");
        x.addEventListener("click", function () { state.area = null; render(true); });
        pill.appendChild(x);
        pillHost.appendChild(pill);
      }

      var hiddenCount = matches.length - shown.length;
      moreWrap.hidden = hiddenCount <= 0;
      moreBtn.textContent = "Show " + hiddenCount + " more";
      var line = "Showing " + shown.length + " of " + items.length + " items";
      if (count.textContent !== line) {
        count.textContent = line;
        if (animate) scramble(count, 420);
      }
      expandBtn.textContent = "Expand all";
    }

    Array.prototype.forEach.call(document.querySelectorAll(".rm-feature[data-area]"), function (a) {
      a.addEventListener("click", function (e) {
        e.preventDefault();
        state.area = a.getAttribute("data-area");
        state.status = "all";
        render(true);
        toolbar.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
      });
    });

    render(false);
    reveal(items);
    traces(list);
  }

  if (window.document$ && window.document$.subscribe) {
    window.document$.subscribe(init);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
