/*
 * "Our math": the interactive figures of docs/our-math.md. Every number comes
 * from math-core.js (the engine's own arithmetic); this file only draws it.
 *
 * Each figure is a [data-w="name"] block holding sliders ([data-c]), their
 * read-outs ([data-o]), SVG plots ([data-r]) and a read-out table
 * ([data-r="kv"]). Everything is plain SVG, no library. Honors
 * prefers-reduced-motion (no scan animation, no scroll reveal) and has no
 * effect without JavaScript beyond the static text and equations.
 */
(function () {
  "use strict";

  var M = window.PgrrMath;
  if (!M || !document.querySelector(".pgrr-math")) return;

  var NS = "http://www.w3.org/2000/svg";
  var reduce = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;

  // ---- tiny helpers ---------------------------------------------------------
  function el(tag, attrs, parent) {
    var n = document.createElementNS(NS, tag);
    for (var k in attrs) n.setAttribute(k, attrs[k]);
    if (parent) parent.appendChild(n);
    return n;
  }

  function f(x, d) {
    if (x === null || x === undefined || !isFinite(x)) return "—";
    return x.toFixed(d === undefined ? 2 : d);
  }

  function pct(x, d) {
    return isFinite(x) ? (x * 100).toFixed(d === undefined ? 0 : d) + "%" : "—";
  }

  function sig(x) {
    if (x === null || x === undefined || !isFinite(x)) return "—";
    if (x === 0) return "0";
    if (x < 0.001) return x.toExponential(1);
    return x < 0.1 ? x.toFixed(4) : x.toFixed(3);
  }

  function clamp(v, a, b) { return Math.min(b, Math.max(a, v)); }

  function val(root, name) { return root._p[name]; }
  function part(root, name) { return root.querySelector('[data-r="' + name + '"]'); }

  // ---- text that scrambles and resolves, like the home diagram's data labels ------
  var GLYPHS = "abcdef0123456789$&=>%#@*+-|:<~";
  function setText(el, text) {
    if (el._t === text) return;
    var prev = el._t || "";
    el._t = text;
    // numbers that merely change are written straight away, so a figure that is
    // moving stays smooth; only a change of wording scrambles
    var skel = text.replace(/[-+−]?\d+(\.\d+)?(e[-+]?\d+)?/g, "#");
    var same = el._k === skel;
    el._k = skel;
    if (same || reduce || !el.isConnected) { cancelAnimationFrame(el._raf); el.textContent = text; return; }
    cancelAnimationFrame(el._raf);
    var t0 = performance.now(), plan = [], i;
    for (i = 0; i < text.length; i++) plan.push({ d: i * 24 + Math.random() * 120, dur: 200 + Math.random() * 180 });
    function frame(now) {
      var e = now - t0, s = "", done = true;
      for (var k = 0; k < text.length; k++) {
        var c = text.charAt(k);
        if (c === " ") { s += c; continue; }
        var te = e - plan[k].d;
        if (te < 0) { s += prev.charAt(k) || " "; done = false; }
        else if (te < plan[k].dur) { s += GLYPHS.charAt(Math.floor(Math.random() * GLYPHS.length)); done = false; }
        else s += c;
      }
      el.textContent = s;
      if (!done) el._raf = requestAnimationFrame(frame);
      else el.textContent = text;
    }
    el._raf = requestAnimationFrame(frame);
  }

  function out(root, name, text) {
    var o = root.querySelector('[data-o="' + name + '"]');
    if (o) setText(o, text);
  }

  function kv(root, rows) {
    var d = part(root, "kv");
    if (!d) return;
    if (d.children.length !== rows.length) {
      var html = "";
      for (var i = 0; i < rows.length; i++) html += "<div><dt></dt><dd></dd></div>";
      d.innerHTML = html;
    }
    for (var k = 0; k < rows.length; k++) {
      var c = d.children[k], dt = c.firstChild, dd = c.lastChild;
      if (dt._t !== rows[k][0]) { dt.textContent = rows[k][0]; dt._t = rows[k][0]; }
      dd.className = rows[k][2] || "";
      setText(dd, rows[k][1]);
    }
  }

  function pickOf(r, list) { return list[Math.floor(r() * list.length)]; }

  var INT = { calls: 1, na: 1, nb: 1, n: 1, s: 1, at: 1, scrape: 1 };
  var LOGP = { n: 1 };
  var STEP = { reset: 1, seed: 1 };

  // Plays a figure on its own. Every `period` ms new target parameters are
  // drawn and the figure glides there: the parameters are interpolated frame
  // by frame and the plots, read-outs and verdict are recomputed on each one,
  // so the picture moves continuously instead of jumping. Pauses off screen;
  // with reduced motion it shows the first state and stays still.
  // mode "swap" (for figures that are random draws, not smooth functions of
  // their parameters): the plot dissolves with blur and comes back redrawn.
  function drive(root, render, gen, opts) {
    opts = opts || {};
    var n = 0, rng = M.mulberry32(opts.seed || 3), visible = true, period = opts.period || 6000;
    var swap = opts.mode === "swap", glide = opts.glide || 1300, busy = false;
    root._p = {};
    function take(p) { for (var k in p) root._p[k] = p[k]; }
    take(gen(n++, rng));
    render();
    window.addEventListener("pgrr:resize", opts.redraw || render);
    if (reduce) return;
    if ("IntersectionObserver" in window) {
      new IntersectionObserver(function (en) { visible = en[0].isIntersecting; }, { threshold: 0.15 }).observe(root);
    }

    function toSwap() {
      var svgs = root.querySelectorAll(".pgrr-plot"), i;
      for (i = 0; i < svgs.length; i++) svgs[i].classList.add("is-swap");
      setTimeout(function () {
        take(gen(n++, rng));
        render();
        for (var i = 0; i < svgs.length; i++) svgs[i].classList.remove("is-swap");
      }, 380);
    }

    function toGlide() {
      var next = gen(n++, rng), a = {}, k, t0 = null;
      for (k in next) a[k] = root._p[k];
      busy = true;
      function frame(ts) {
        if (t0 === null) t0 = ts;
        var u = clamp((ts - t0) / glide, 0, 1);
        var e = u < 0.5 ? 4 * u * u * u : 1 - Math.pow(-2 * u + 2, 3) / 2;
        for (var k in next) {
          var x = a[k], y = next[k], v;
          if (STEP[k] || typeof y !== "number") v = u < 0.02 ? x : y;
          else if (LOGP[k]) v = Math.exp(Math.log(x) + (Math.log(y) - Math.log(x)) * e);
          else v = x + (y - x) * e;
          if (INT[k]) v = Math.round(v);
          root._p[k] = v;
        }
        render();
        if (u < 1) requestAnimationFrame(frame);
        else busy = false;
      }
      requestAnimationFrame(frame);
    }

    function loop() {
      if (visible && !document.hidden && !busy) { if (swap) toSwap(); else toGlide(); }
      setTimeout(loop, period);
    }
    setTimeout(loop, period);
  }

  // ---- a small plotting surface -----------------------------------------------
  function Plot(svg, o) {
    var vb = svg.getAttribute("viewBox").split(/\s+/).map(Number);
    this.svg = svg;
    this.W = vb[2];
    this.H = vb[3];
    this.ml = o.ml === undefined ? 40 : o.ml;
    this.mr = o.mr === undefined ? 12 : o.mr;
    this.mt = o.mt === undefined ? 12 : o.mt;
    this.mb = o.mb === undefined ? 24 : o.mb;
    this.xr = o.x;
    this.yr = o.y;
    this.xlog = !!o.xlog;
    this.ylog = !!o.ylog;
    this.refit();
    Plot.all.push(this);
  }

  Plot.all = [];

  // One viewBox unit is one CSS pixel, so labels keep their size on a phone.
  Plot.prototype.refit = function () {
    var w = this.svg.getBoundingClientRect().width;
    if (w >= 200) {
      this.W = Math.round(w);
      this.svg.setAttribute("viewBox", "0 0 " + this.W + " " + this.H);
    }
  };

  Plot.prototype.sx = function (v) {
    var a = this.xr[0], b = this.xr[1];
    var t = this.xlog ? (Math.log(v) - Math.log(a)) / (Math.log(b) - Math.log(a)) : (v - a) / (b - a);
    return this.ml + t * (this.W - this.ml - this.mr);
  };

  Plot.prototype.sy = function (v) {
    var a = this.yr[0], b = this.yr[1];
    var t = this.ylog ? (Math.log(v) - Math.log(a)) / (Math.log(b) - Math.log(a)) : (v - a) / (b - a);
    return this.H - this.mb - t * (this.H - this.mt - this.mb);
  };

  Plot.prototype.inv = function (px) {
    var t = (px - this.ml) / (this.W - this.ml - this.mr);
    var a = this.xr[0], b = this.xr[1];
    return this.xlog ? Math.exp(Math.log(a) + t * (Math.log(b) - Math.log(a))) : a + t * (b - a);
  };

  Plot.prototype.clear = function () {
    while (this.svg.firstChild) this.svg.removeChild(this.svg.firstChild);
    return this;
  };

  Plot.prototype.axes = function (xt, yt, xf, yf, xlab) {
    var s = this.svg, i, v;
    for (i = 0; i < yt.length; i++) {
      v = this.sy(yt[i]);
      el("line", { "class": "gd", x1: this.ml, x2: this.W - this.mr, y1: v, y2: v }, s);
      var t = el("text", { x: this.ml - 7, y: v + 3, "text-anchor": "end" }, s);
      t.textContent = yf ? yf(yt[i]) : yt[i];
    }
    for (i = 0; i < xt.length; i++) {
      v = this.sx(xt[i]);
      el("line", { "class": "ax", x1: v, x2: v, y1: this.H - this.mb, y2: this.H - this.mb + 3 }, s);
      var u = el("text", { x: v, y: this.H - this.mb + 14, "text-anchor": "middle" }, s);
      u.textContent = xf ? xf(xt[i]) : xt[i];
    }
    el("line", { "class": "ax", x1: this.ml, x2: this.W - this.mr, y1: this.H - this.mb, y2: this.H - this.mb }, s);
    el("line", { "class": "ax", x1: this.ml, x2: this.ml, y1: this.mt, y2: this.H - this.mb }, s);
    if (xlab) {
      var l = el("text", { x: this.W - this.mr, y: this.H - 2, "text-anchor": "end" }, s);
      l.textContent = xlab;
    }
    return this;
  };

  Plot.prototype.path = function (pts, cls, close) {
    var d = "";
    for (var i = 0; i < pts.length; i++) {
      d += (i ? "L" : "M") + this.sx(pts[i][0]).toFixed(1) + "," + this.sy(pts[i][1]).toFixed(1);
    }
    if (close !== undefined) {
      d += "L" + this.sx(pts[pts.length - 1][0]).toFixed(1) + "," + this.sy(close).toFixed(1);
      d += "L" + this.sx(pts[0][0]).toFixed(1) + "," + this.sy(close).toFixed(1) + "Z";
    }
    return el("path", { "class": cls, d: d }, this.svg);
  };

  Plot.prototype.vline = function (x, cls, label, lcls, dy) {
    var X = this.sx(x);
    el("line", { "class": cls, x1: X, x2: X, y1: this.mt, y2: this.H - this.mb }, this.svg);
    if (label) {
      var t = el("text", { x: X + 4, y: this.mt + 9 + (dy || 0), "class": lcls || "" }, this.svg);
      t.textContent = label;
    }
  };

  // A drafting dimension: a line between two ticks with arrowheads and its value.
  Plot.prototype.dim = function (x1, x2, y, label) {
    var a = this.sx(x1), b = this.sx(x2), Y = y, g = this.svg;
    el("line", { "class": "dm", x1: a, x2: b, y1: Y, y2: Y }, g);
    el("line", { "class": "dm", x1: a, x2: a, y1: Y - 5, y2: Y + 5 }, g);
    el("line", { "class": "dm", x1: b, x2: b, y1: Y - 5, y2: Y + 5 }, g);
    el("path", { "class": "dm", d: "M" + (a + 6) + "," + (Y - 2.5) + "L" + a + "," + Y + "L" + (a + 6) + "," + (Y + 2.5) }, g);
    el("path", { "class": "dm", d: "M" + (b - 6) + "," + (Y - 2.5) + "L" + b + "," + Y + "L" + (b - 6) + "," + (Y + 2.5) }, g);
    var t = el("text", { "class": "dm-t", x: (a + b) / 2, y: Y - 6, "text-anchor": "middle" }, g);
    t.textContent = label;
  };

  Plot.prototype.hline = function (y, x1, x2, cls) {
    var Y = this.sy(y);
    el("line", { "class": cls, x1: this.sx(x1), x2: this.sx(x2), y1: Y, y2: Y }, this.svg);
  };

  Plot.prototype.dot = function (x, y, r, cls) {
    return el("circle", { "class": cls || "dot", cx: this.sx(x), cy: this.sy(y), r: r }, this.svg);
  };

  Plot.prototype.text = function (x, y, s, cls, anchor) {
    var t = el("text", { x: x, y: y, "class": cls || "", "text-anchor": anchor || "start" }, this.svg);
    t.textContent = s;
    return t;
  };

  function ticks(a, b, n) {
    var step = (b - a) / n, r = [];
    for (var i = 0; i <= n; i++) r.push(a + i * step);
    return r;
  }

  // Log-normal noise around `level`, the same family as math-core's generate().
  function noisy(rng, level, cv) {
    var s = Math.sqrt(Math.log(1 + cv * cv));
    return level * Math.exp(s * M.gauss(rng) - (s * s) / 2);
  }

  // ======================================================================
  // Pipeline strip: lights up as the lab runs.
  // ======================================================================
  function initPipe(root) {
    var stages = root.querySelectorAll(".pgrr-stage");
    var order = ["samples", "floors", "prefilter", "edivisive", "tolerance", "welch", "verdict"];
    // index of the stage that stops the analysis, per reason (7 = none stops)
    var stopAt = { samples: 1, executions: 1, prefilter: 2, nochangepoint: 3, tolerance: 4, pvalue: 5, detected: 7 };

    window.addEventListener("pgrr:lab", function (e) {
      var reason = e.detail.reason;
      var stop = stopAt[reason];
      root.classList.remove("is-idle");
      for (var i = 0; i < stages.length; i++) {
        var s = stages[i];
        s.classList.toggle("is-pass", i < stop || stop === 7);
        s.classList.toggle("is-stop", i === stop && stop !== 7);
        s.classList.toggle("is-skip", i > stop && stop !== 7);
        s.style.setProperty("--lit", i < stop || stop === 7 ? 1 : 0);
      }
      root.setAttribute("data-order", order.join(","));
    });
  }

  // ======================================================================
  // Lab: the whole engine on a synthetic query.
  // ======================================================================
    var REASONS = {
    detected: "All gates passed: a ≥20% shift, located near the deploy and significant at α = 0.05.",
    samples: "Fewer than 10 usable scrapes in a window. The honest answer is \"we cannot tell\".",
    executions: "Fewer than 100 calls in a window, so a window mean is too imprecise to compare.",
    prefilter: "The post-deploy mean is less than 20% above the pre-deploy mean: stopped before the expensive stages.",
    nochangepoint: "E-divisive found no split with Q > 0.",
    tolerance: "The change point sits too far from the deploy to be attributed to it.",
    pvalue: "The shift is real-looking but not significant at α = 0.05, so it is not reported."
  };

  function initLab(root) {
    var chart = new Plot(part(root, "chart"), { x: [-30, 30], y: [0, 1], ml: 44, mb: 26, mt: 14 });
    var curve = new Plot(part(root, "curve"), { x: [-30, 30], y: [0, 1], ml: 44, mb: 20, mt: 8 });

    function render() {
      var size = val(root, "size") / 100, cv = val(root, "cv"), calls = val(root, "calls");
      var lag = val(root, "lag"), phi = val(root, "phi"), scrape = val(root, "scrape");
      out(root, "size", "+" + Math.round(size * 100) + "%");
      out(root, "cv", f(cv, 1));
      out(root, "calls", String(calls));
      out(root, "lag", (lag >= 0 ? "+" : "") + f(lag, 1) + " min");
      out(root, "phi", f(phi, 2));
      out(root, "scrape", scrape + " s");

      var W = 30, tol = Math.max(2, 0.2 * W);
      var samples = M.generate({ windowMin: W, scrapeSec: scrape, base: 10, size: size, lagMin: lag, cv: cv, callsPerScrape: calls, phi: phi, seed: val(root, "seed") });
      var r = M.analyse(samples, { latencyThreshold: 0.2, pThreshold: 0.05, minSamples: 10, minExecutions: 100, toleranceMin: tol });

      // ---- series ----
      var xs = [], top = 0, i;
      for (i = 0; i < samples.length; i++) {
        if (samples[i].calls <= 0) continue;
        var v = samples[i].totalMs / samples[i].calls;
        xs.push([samples[i].t, v]);
        if (v > top) top = v;
      }
      top = Math.max(top, 10 * (1 + size)) * 1.12;
      chart.yr = [0, top];
      chart.clear().axes(ticks(-30, 30, 6), ticks(0, top, 4).map(function (y) { return Math.round(y); }),
        function (x) { return (x > 0 ? "+" : "") + x; }, function (y) { return y; }, "minutes from the deploy");
      chart.text(chart.ml - 38, chart.mt + 4, "ms", "");
      var bandL = chart.sx(-tol), bandR = chart.sx(tol);
      el("rect", { "class": "band", x: bandL, y: chart.mt, width: bandR - bandL, height: chart.H - chart.mt - chart.mb }, chart.svg);
      chart.dim(-tol, tol, chart.H - chart.mb - 12, "2τ = " + f(2 * tol, 0) + " min");
      chart.vline(0, "vl vl--deploy", "deploy", "lbl-c");
      for (i = 0; i < xs.length; i++) chart.dot(xs[i][0], xs[i][1], 2.4, "dot");
      if (r.meanPre) {
        chart.hline(r.meanPre, -30, 0, "ln ln--b ln--dash");
        chart.hline(r.meanPost, 0, 30, "ln ln--b ln--dash");
      }
      if (r.cp) {
        chart.vline(r.changeAtMin, "vl vl--cp", "change " + (r.changeAtMin >= 0 ? "+" : "") + f(r.changeAtMin, 1), "lbl-a", 12);
      }

      // ---- energy curve ----
      if (r.curve.length) {
        var qmax = 0;
        for (i = 0; i < r.curve.length; i++) if (r.curve[i].q > qmax) qmax = r.curve[i].q;
        curve.yr = [0, qmax * 1.1 || 1];
        curve.clear().axes([], [], null, null, null);
        var pts = r.curve.map(function (c) { return [r.times[c.k], Math.max(0, c.q)]; });
        curve.path(pts, "fill-a", 0);
        curve.path(pts, "ln ln--a");
        if (r.cp) curve.dot(r.changeAtMin, r.cp.q, 3.5, "dot dot--a");
        curve.text(curve.ml - 7, curve.mt + 8, "Q(k)", "", "end");
      } else {
        curve.clear().axes([], [], null, null, null);
        curve.text(curve.W / 2, curve.H / 2, "energy curve is computed only for shifts that pass the pre-filter", "", "middle");
      }

      // ---- readouts ----
      var w = r.welch;
      kv(root, [
        ["factor f", r.factor ? f(r.factor, 2) + "×" : "—", r.factor - 1 >= 0.2 ? "is-a" : ""],
        ["Q at change", r.cp ? f(r.cp.q, 2) : "—"],
        ["|change − deploy|", r.cp ? f(r.distanceMin, 1) + " / " + f(tol, 1) + " min" : "—", r.cp ? (r.distanceMin <= tol ? "is-ok" : "is-bad") : ""],
        ["t", w ? f(w.t, 2) : "—"],
        ["df", w ? f(w.df, 1) : "—"],
        ["p", w ? sig(w.p) : "—", w ? (w.p <= 0.05 ? "is-ok" : "is-bad") : ""],
        ["score 1 − p", w ? f(1 - w.p, 4) : "—"],
        ["calls / window", String(Math.min(r.preCalls, r.postCalls))]
      ]);
      var vd = part(root, "verdict");
      vd.setAttribute("data-v", r.verdict);
      vd.querySelector("b").textContent = r.verdict;
      vd.querySelector("span").textContent = REASONS[r.reason];

      window.dispatchEvent(new CustomEvent("pgrr:lab", { detail: { reason: r.reason, verdict: r.verdict } }));
    }

    drive(root, render, function (n, r) {
      if (n === 0) return { size: 45, cv: 1.2, calls: 40, lag: 1, phi: 0, scrape: 60, seed: 7 };
      return {
        size: pickOf(r, [0, 10, 25, 45, 70, 100]), cv: pickOf(r, [0.6, 1.2, 2, 3]),
        calls: pickOf(r, [1, 6, 20, 40, 120]), lag: pickOf(r, [-6, -2, 0, 1, 3, 8, 12]),
        phi: pickOf(r, [0, 0, 0.5, 0.9]), scrape: 60, seed: 7
      };
    }, { period: 3800, glide: 1500 });
  }

  // ======================================================================
  // §1 Counters: cumulative mean versus interval mean.
  // ======================================================================
  function initCounters(root) {
    var plot = new Plot(part(root, "chart"), { x: [0, 40], y: [0, 30], ml: 40, mb: 26 });

    function render() {
      var size = val(root, "size") / 100, at = val(root, "at"), resetOn = !!val(root, "reset");
      out(root, "reset", resetOn ? "minute 8" : "none");
      out(root, "size", "+" + Math.round(size * 100) + "%");
      out(root, "at", "minute " + at);
      var rng = M.mulberry32(11), base = 10, calls = 60, T = 0, C = 0;
      var inter = [], cum = [], i, top = base * (1 + size) * 1.25;
      for (i = 1; i <= 40; i++) {
        var level = i >= at ? base * (1 + size) : base;
        var x = noisy(rng, level, 0.12);
        if (resetOn && i === 8) { T = 0; C = 0; continue; } // the counters restart: this interval is discarded
        T += x * calls;
        C += calls;
        inter.push([i, x]);
        cum.push([i, T / C]);
      }
      plot.yr = [0, top];
      plot.clear().axes(ticks(0, 40, 8), ticks(0, top, 4).map(function (y) { return Math.round(y); }), null, null, "minute");
      plot.text(plot.ml - 38, plot.mt + 4, "ms", "");
      plot.vline(at, "vl vl--deploy", "step", "lbl-c");
      if (resetOn) plot.vline(8, "vl", "reset", "");
      var segs = [[], []];
      cum.forEach(function (p) { segs[p[0] < 8 || !resetOn ? 0 : 1].push(p); });
      segs.forEach(function (s) { if (s.length > 1) plot.path(s, "ln ln--m ln--dash"); });
      plot.path(inter.filter(function (p) { return p[0] < 8 || !resetOn; }), "ln ln--a");
      if (resetOn) plot.path(inter.filter(function (p) { return p[0] > 8; }), "ln ln--a");
      var li = inter[inter.length - 1][1], lc = cum[cum.length - 1][1];
      kv(root, [
        ["true step", pct(size), ""],
        ["interval mean reads", pct(li / base - 1), "is-a"],
        ["cumulative reads", pct(lc / base - 1), ""],
        ["calls since step", String((41 - at) * calls), ""]
      ]);
    }

    drive(root, render, function (n, r) {
      return { size: pickOf(r, [50, 100, 150, 200]), at: pickOf(r, [10, 14, 18, 22, 26]), reset: n % 2 };
    }, { period: 3200, glide: 1300, seed: 11 });
  }

  // ======================================================================
  // §2 Gates: precision of a window mean.
  // ======================================================================
  function initGates(root) {
    var plot = new Plot(part(root, "chart"), { x: [10, 2000], y: [0, 1], xlog: true, ml: 40, mb: 26 });

    function render() {
      var cv = val(root, "cv"), n = val(root, "n"), s = val(root, "s");
      out(root, "cv", f(cv, 1));
      out(root, "n", String(n));
      out(root, "s", String(s));
      var half = function (N) { return 1.96 * cv / Math.sqrt(N); };
      var top = Math.min(2, Math.max(0.2, half(10)));
      plot.yr = [0, top];
      var pts = [], N;
      for (N = 10; N <= 2000; N *= 1.06) pts.push([N, Math.min(top, half(N))]);
      plot.clear().axes([10, 30, 100, 300, 1000, 2000], ticks(0, top, 4),
        function (x) { return x; }, function (y) { return Math.round(y * 100) + "%"; }, "calls in the window");
      plot.text(plot.ml - 38, plot.mt + 4, "±95%", "");
      plot.vline(100, "vl vl--deploy", "floor N = 100", "lbl-c");
      plot.path(pts, "fill-a", 0);
      plot.path(pts, "ln ln--a");
      plot.dot(clamp(n, 10, 2000), Math.min(top, half(n)), 4, "dot dot--a");
      var okN = n >= 100, okS = s >= 10;
      kv(root, [
        ["SE of the mean", pct(cv / Math.sqrt(n), 1), ""],
        ["95% half-width", "±" + pct(half(n), 1), "is-a"],
        ["scrapes ≥ 10", okS ? "yes" : "no", okS ? "is-ok" : "is-bad"],
        ["calls ≥ 100", okN ? "yes" : "no", okN ? "is-ok" : "is-bad"],
        ["answer", okN && okS ? "analyse" : "InsufficientData", okN && okS ? "is-ok" : "is-bad"]
      ]);
    }

    drive(root, render, function (n, r) { return { cv: pickOf(r, [0.5, 1, 1.5, 2.5]), n: pickOf(r, [40, 100, 100, 300, 900, 1500]), s: pickOf(r, [6, 12, 12, 20, 30]) }; }, { period: 3000, glide: 1300, seed: 21 });
  }

  // ======================================================================
  // §3 Energy statistic: a draggable split.
  // ======================================================================
  function initEnergy(root) {
    var N = 60;
    var chart = new Plot(part(root, "chart"), { x: [0, N], y: [0, 1], ml: 40, mb: 22 });
    var curve = new Plot(part(root, "curve"), { x: [0, N], y: [0, 1], ml: 40, mb: 18, mt: 8 });
    var series = [], eCurve = [], m = M.minSegLen(N), k = 30, best = null, scanning = 0;

    function gen() {
      var size = val(root, "size") / 100, cv = val(root, "cv"), rng = M.mulberry32(val(root, "seed")), i;
      series = [];
      for (i = 0; i < N; i++) series.push(noisy(rng, 10 * (i >= N / 2 ? 1 + size : 1), cv * 0.3));
      eCurve = M.energyCurve(series, m);
      best = M.bestChangePoint(series, m);
      k = best ? best.index : 30;
    }

    function qAt(kk) {
      for (var i = 0; i < eCurve.length; i++) if (eCurve[i].k === kk) return eCurve[i].q;
      return 0;
    }

    function draw() {
      var size = val(root, "size") / 100, cv = val(root, "cv");
      out(root, "size", "+" + Math.round(size * 100) + "%");
      out(root, "cv", f(cv, 1));
      var top = 0, i;
      for (i = 0; i < N; i++) if (series[i] > top) top = series[i];
      top = Math.max(top, 10 * (1 + size)) * 1.1;
      chart.yr = [0, top];
      chart.clear().axes(ticks(0, N, 6), ticks(0, top, 4).map(function (y) { return Math.round(y); }), null, null, "scrape index");
      var left = series.slice(0, k), right = series.slice(k);
      var ml = M.mean(left), mr = M.mean(right);
      chart.hline(ml, 0, k, "ln ln--b ln--dash");
      chart.hline(mr, k, N, "ln ln--a ln--dash");
      for (i = 0; i < N; i++) chart.dot(i, series[i], 2.6, i < k ? "dot dot--b" : "dot dot--a");
      if (best) chart.vline(best.index, "vl vl--deploy", "argmax", "lbl-c");
      chart.vline(k, "vl vl--cp");
      el("circle", { "class": "knob", cx: chart.sx(k), cy: chart.H - chart.mb, r: 6 }, chart.svg);

      var qmax = 0;
      for (i = 0; i < eCurve.length; i++) if (eCurve[i].q > qmax) qmax = eCurve[i].q;
      curve.yr = [0, qmax * 1.15 || 1];
      curve.clear().axes([], [], null, null, null);
      var pts = eCurve.map(function (c) { return [c.k, Math.max(0, c.q)]; });
      curve.path(pts, "fill-a", 0);
      curve.path(pts, "ln ln--a");
      if (best) curve.vline(best.index, "vl vl--deploy");
      curve.dot(k, Math.max(0, qAt(k)), 4, "dot dot--a");
      curve.text(curve.ml - 7, curve.mt + 8, "Q(k)", "", "end");
      kv(root, [
        ["split k", String(k), ""],
        ["Q(k)", f(qAt(k), 2), "is-a"],
        ["argmax k*", best ? String(best.index) : "—", ""],
        ["Q(k*)", best ? f(best.q, 2) : "—", ""],
        ["mean left → right", f(ml, 1) + " → " + f(mr, 1), ""]
      ]);
    }

    // The split sweeps the series over and over: it slides to the right, the
    // curve Q(k) grows behind it, and it settles on the maximum before going again.
    var onScreen = true;
    if ("IntersectionObserver" in window) {
      new IntersectionObserver(function (en) { onScreen = en[0].isIntersecting; }, { threshold: 0.1 }).observe(root);
    }
    function sweep(ts) {
      requestAnimationFrame(sweep);
      if (!onScreen || document.hidden) return;
      var u = (ts % 6200) / 3800;
      if (u < 1) {
        var e = u < 0.5 ? 2 * u * u : 1 - Math.pow(-2 * u + 2, 2) / 2;
        k = Math.round(m + (N - 2 * m) * e);
      } else if (best) k = best.index;
      draw();
    }

    drive(root, function () { var keep = k; gen(); k = keep; draw(); }, function (n, r) {
      if (n === 0) return { size: 50, cv: 1, seed: 6 };
      return { size: pickOf(r, [20, 35, 50, 80, 100]), cv: pickOf(r, [0.6, 1, 1.6, 2.4]), seed: 6 };
    }, { period: 4500, glide: 1800, redraw: draw });
    if (reduce) { if (best) k = best.index; draw(); }
    else requestAnimationFrame(sweep);
  }

  // ======================================================================
  // §4 Tolerance window.
  // ======================================================================
  function initTol(root) {
    var plot = new Plot(part(root, "chart"), { x: [-60, 60], y: [0, 1], ml: 30, mr: 30, mb: 8, mt: 8 });

    // One rule is the analysis window; the shaded band is the tolerance around
    // the deploy; the dot is the located change, joined to the deploy by the
    // distance that is being compared with the band.
    function render() {
      var W = val(root, "w"), cp = val(root, "cp"), tol = Math.max(2, 0.2 * W);
      out(root, "w", f(W, 0) + " min");
      out(root, "cp", (cp >= 0 ? "+" : "") + f(cp, 1) + " min");
      var g = plot.clear().svg, Y = 78, inside = Math.abs(cp) <= tol;
      var x0 = plot.sx(0), xc = plot.sx(clamp(cp, -60, 60));
      el("rect", { "class": "band", x: plot.sx(-tol), y: Y - 24, width: plot.sx(tol) - plot.sx(-tol), height: 48 }, g);
      el("line", { "class": "ax", x1: plot.sx(-W), x2: plot.sx(W), y1: Y, y2: Y }, g);
      [-W, W].forEach(function (w) {
        el("line", { "class": "ax", x1: plot.sx(w), x2: plot.sx(w), y1: Y - 6, y2: Y + 6 }, g);
        plot.text(plot.sx(w), Y + 22, w < 0 ? "−W" : "+W", "", "middle");
      });
      el("line", { "class": "vl vl--deploy", x1: x0, x2: x0, y1: Y - 34, y2: Y + 34 }, g);
      plot.text(x0, Y - 42, "deploy", "lbl-c", "middle");
      el("line", { x1: x0, x2: xc, y1: Y, y2: Y, "class": "ln " + (inside ? "ln--a" : "ln--m"), "stroke-width": 3 }, g);
      el("circle", { "class": inside ? "dot dot--a" : "dot", cx: xc, cy: Y, r: 5.5, opacity: 1 }, g);
      plot.text(xc, Y + 50, "change " + (cp >= 0 ? "+" : "−") + f(Math.abs(cp), 1), inside ? "lbl-a" : "", "middle");
      plot.dim(-tol, tol, Y + 88, "2τ = " + f(2 * tol, 1) + " min");
      kv(root, [
        ["tolerance τ", f(tol, 1) + " min", ""],
        ["|change − deploy|", f(Math.abs(cp), 1) + " min", inside ? "is-ok" : "is-bad"],
        ["attribution", inside ? "this deploy" : "background", inside ? "is-ok" : "is-bad"]
      ]);
    }

    drive(root, render, function (n, r) { return { w: pickOf(r, [20, 30, 45, 60]), cp: pickOf(r, [-18, -9, -3.5, 0.5, 2, 4.5, 7, 14]) }; }, { period: 3000, glide: 1300, seed: 31 });
  }

  // ======================================================================
  // §5 Welch's t-test.
  // ======================================================================
  function initWelch(root) {
    var plot = new Plot(part(root, "chart"), { x: [-6, 6], y: [0, 0.45], ml: 40, mb: 26 });

    function render() {
      var d = val(root, "d") / 100, cv = val(root, "cv"), na = val(root, "na"), nb = val(root, "nb");
      out(root, "d", "+" + Math.round(d * 100) + "%");
      out(root, "cv", f(cv, 2));
      out(root, "na", String(na));
      out(root, "nb", String(nb));
      var sa = cv, sb = cv * (1 + d);
      var va = (sa * sa) / na, vb = (sb * sb) / nb, se = Math.sqrt(va + vb);
      var t = d / se;
      var df = ((va + vb) * (va + vb)) / ((va * va) / (na - 1) + (vb * vb) / (nb - 1));
      var p = M.tDistPValue(Math.abs(t), df);
      var lim = clamp(Math.ceil(Math.abs(t) + 1.5), 5, 14);
      plot.xr = [-lim, lim];
      plot.clear().axes(ticks(-lim, lim, lim <= 6 ? 6 : 7).map(function (x) { return Math.round(x); }), [0, 0.1, 0.2, 0.3, 0.4], null,
        function (y) { return y.toFixed(1); }, "t");
      var pts = [], x, a = Math.abs(t) > lim ? lim : Math.abs(t);
      for (x = -lim; x <= lim + 1e-9; x += lim / 120) pts.push([x, M.tPdf(x, df)]);
      var tails = [pts.filter(function (q) { return q[0] <= -a; }), pts.filter(function (q) { return q[0] >= a; })];
      tails.forEach(function (tl) { if (tl.length > 1) plot.path(tl, "fill-a", 0); });
      plot.path(pts, "ln ln--a");
      plot.vline(a, "vl vl--cp", "|t| = " + f(Math.abs(t), 2), "lbl-a");
      plot.vline(-a, "vl vl--cp");
      plot.text(plot.sx(0), plot.sy(0.22), "shaded area = p", "", "middle");
      kv(root, [
        ["t", f(t, 2), ""],
        ["df (Welch–Satterthwaite)", f(df, 1), ""],
        ["p", sig(p), p <= 0.05 ? "is-ok" : "is-bad"],
        ["score 1 − p", f(1 - p, 4), "is-a"],
        ["vs α = 0.05", p <= 0.05 ? "significant" : "not significant", p <= 0.05 ? "is-ok" : "is-bad"]
      ]);
    }

    drive(root, render, function (n, r) { return { d: pickOf(r, [5, 12, 25, 40, 80]), cv: pickOf(r, [0.2, 0.4, 0.7]), na: pickOf(r, [8, 20, 30, 60, 100]), nb: pickOf(r, [8, 20, 30, 60, 100]) }; }, { period: 3000, glide: 1300, seed: 41 });
  }

  // ======================================================================
  // §6 What 1 − p means.
  // ======================================================================
  function initSbb(root) {
    var plot = new Plot(part(root, "chart"), { x: [1e-5, 0.3], y: [0, 1], xlog: true, ml: 40, mb: 26 });

    function render() {
      var p = Math.pow(10, val(root, "p"));
      out(root, "p", sig(p));
      var pts = [], q, ps = [];
      for (q = 1e-5; q <= 0.3; q *= 1.08) { pts.push([q, M.sbbBound(q)]); ps.push([q, q]); }
      plot.clear().axes([1e-5, 1e-4, 1e-3, 1e-2, 0.1], [0, 0.25, 0.5, 0.75, 1],
        function (x) { return x >= 0.1 ? "0.1" : "1e" + Math.round(Math.log10(x)); }, function (y) { return y.toFixed(2); }, "p-value (log scale)");
      plot.path(ps, "ln ln--m ln--dash");
      plot.path(pts, "fill-a", 0);
      plot.path(pts, "ln ln--a");
      var b = M.sbbBound(p);
      plot.vline(p, "vl vl--deploy");
      plot.dot(p, b, 4, "dot dot--a");
      plot.text(plot.ml + 8, plot.sy(0.9), "P(no change) ≥ −ep ln p / (1 − ep ln p)", "lbl-a");
      plot.text(plot.ml + 8, plot.sy(0.82), "dashed: p itself", "");
      kv(root, [
        ["p", sig(p), ""],
        ["score 1 − p", f(1 - p, 4), ""],
        ["P(no change) ≥", b === null ? "—" : f(b, 4), "is-a"],
        ["in odds", b === null ? "—" : "1 : " + f((1 - b) / b, 1), ""]
      ]);
    }

    drive(root, render, function (n, r) { return { p: pickOf(r, [-4.5, -3, -2.3, -1.3, -0.9, -0.6]) }; }, { period: 2800, glide: 1200, seed: 51 });
  }

  // ======================================================================
  // §7 Selection bias lab.
  // ======================================================================
  function initSelection(root) {
    var plot = new Plot(part(root, "chart"), { x: [0, 1], y: [0, 1], ml: 40, mb: 26 });
    var BINS = 20, hist = [], total = 0, p05 = 0, p01 = 0, job = 0, seed = 1;

    function reset() {
      hist = [];
      for (var i = 0; i < BINS; i++) hist.push(0);
      total = 0; p05 = 0; p01 = 0;
    }

    function draw() {
      out(root, "n", String(val(root, "n")));
      out(root, "phi", f(val(root, "phi"), 1));
      var expect = Math.max(total / BINS, 1);
      var top = Math.max(expect * 1.4, Math.max.apply(null, hist) * 1.12, 10);
      plot.yr = [0, top];
      plot.clear().axes(ticks(0, 1, 5), ticks(0, top, 4).map(function (y) { return Math.round(y); }), function (x) { return x.toFixed(1); }, null, "p at the split E-divisive chose");
      var i;
      for (i = 0; i < BINS; i++) {
        var x0 = plot.sx(i / BINS) + 1, x1 = plot.sx((i + 1) / BINS) - 1, y = plot.sy(hist[i]);
        el("rect", { "class": i === 0 ? "fill-a" : "fill-m", x: x0, y: y, width: Math.max(1, x1 - x0), height: plot.sy(0) - y }, plot.svg);
        if (i === 0) el("rect", { "class": "ln ln--a", fill: "none", x: x0, y: y, width: Math.max(1, x1 - x0), height: plot.sy(0) - y }, plot.svg);
      }
      if (total) plot.hline(total / BINS, 0, 1, "ln ln--c ln--dash");
      kv(root, [
        ["series run", String(total), ""],
        ["p < 0.05", total ? pct(p05 / total, 1) : "—", total ? "is-bad" : ""],
        ["p < 0.01", total ? pct(p01 / total, 1) : "—", total ? "is-bad" : ""],
        ["nominal", "5% / 1%", ""]
      ]);
    }

    function one(n, phi, rng) {
      var s = [], prev = 0, i;
      for (i = 0; i < n; i++) {
        prev = phi * prev + Math.sqrt(1 - phi * phi) * M.gauss(rng);
        s.push(Math.exp(0.5 * prev));
      }
      var cp = M.bestChangePoint(s, M.minSegLen(n));
      if (!cp) return null;
      return M.welch(s.slice(0, cp.index), s.slice(cp.index)).p;
    }

    function run() {
      var id = ++job, n = val(root, "n"), phi = val(root, "phi"), want = 600, done = 0;
      reset();
      seed++;
      var rng = M.mulberry32(seed * 7919);
      function chunk() {
        if (job !== id) return;
        var t0 = performance.now();
        while (done < want && (reduce || performance.now() - t0 < 10)) {
          var p = one(n, phi, rng);
          done++;
          if (p === null) continue;
          hist[Math.min(BINS - 1, Math.floor(p * BINS))]++;
          total++;
          if (p < 0.05) p05++;
          if (p < 0.01) p01++;
        }
        draw();
        if (done < want) requestAnimationFrame(chunk);
      }
      requestAnimationFrame(chunk);
    }

    reset();
    drive(root, function () { job++; draw(); run(); }, function (n, r) {
      return { n: pickOf(r, [30, 60, 120, 60]), phi: pickOf(r, [0, 0, 0.5, 0.8]) };
    }, { period: 5200, seed: 71, redraw: draw, mode: "swap" });
  }

  // ======================================================================
  // §8 Power.
  // ======================================================================
  function initPower(root) {
    var plot = new Plot(part(root, "chart"), { x: [0.05, 1], y: [10, 10000], xlog: true, ylog: true, ml: 46, mb: 26 });

    function render() {
      var cv = val(root, "cv"), d = val(root, "d") / 100;
      out(root, "cv", f(cv, 1));
      out(root, "d", "+" + Math.round(d * 100) + "%");
      var pts = [], q;
      for (q = 0.05; q <= 1.0001; q *= 1.05) pts.push([q, clamp(M.callsNeeded(cv, q), 10, 10000)]);
      plot.clear().axes([0.05, 0.1, 0.2, 0.5, 1], [10, 100, 1000, 10000],
        function (x) { return Math.round(x * 100) + "%"; }, function (y) { return y >= 1000 ? y / 1000 + "k" : y; }, "relative change to detect");
      plot.text(plot.ml - 42, plot.mt + 4, "calls", "");
      plot.path(pts, "fill-a", 10);
      plot.path(pts, "ln ln--a");
      var n = M.callsNeeded(cv, d);
      plot.vline(d, "vl vl--deploy");
      plot.dot(d, clamp(n, 10, 10000), 4, "dot dot--a");
      kv(root, [
        ["calls per window", String(Math.ceil(n)), "is-a"],
        ["per minute (30 min)", f(n / 30, 1), ""],
        ["half the change", String(Math.ceil(M.callsNeeded(cv, d / 2))), ""],
        ["≥ floor of 100?", n >= 100 ? "above" : "below", n >= 100 ? "" : "is-bad"]
      ]);
    }

    drive(root, render, function (n, r) { return { cv: pickOf(r, [0.5, 1, 1.5, 2, 3]), d: pickOf(r, [10, 20, 30, 50, 80]) }; }, { period: 3000, glide: 1300, seed: 61 });
  }

  // ======================================================================
  // Title figure: the energy statistic scanning a series, forever.
  // ======================================================================
  function initHero(root) {
    var N = 60, m = M.minSegLen(N), rng = M.mulberry32(6), series = [], i;
    for (i = 0; i < N; i++) series.push(noisy(rng, 10 * (i >= 30 ? 1.5 : 1), 0.3));
    var curve = M.energyCurve(series, m), best = M.bestChangePoint(series, m);
    var a = new Plot(part(root, "chart"), { x: [0, N], y: [0, 20], ml: 8, mr: 8, mt: 10, mb: 8 });
    var b = new Plot(part(root, "curve"), { x: [0, N], y: [0, 1], ml: 8, mr: 8, mt: 6, mb: 8 });
    var qmax = 0, visible = true, t0 = null;
    curve.forEach(function (c) { if (c.q > qmax) qmax = c.q; });
    b.yr = [0, qmax * 1.1];

    function draw(u) {
      // u in [0,1]: how far the scan has gone; > 1 holds on the answer
      var k = Math.round(m + (N - 2 * m) * clamp(u, 0, 1)), j;
      a.clear(); b.clear();
      var L = M.mean(series.slice(0, k)), R = M.mean(series.slice(k));
      a.hline(L, 0, k, "ln ln--b ln--dash");
      a.hline(R, k, N, "ln ln--a ln--dash");
      for (j = 0; j < N; j++) a.dot(j, series[j], 2.8, j < k ? "dot dot--b" : "dot dot--a");
      a.vline(k, "vl vl--cp");
      var pts = curve.filter(function (c) { return c.k <= k; }).map(function (c) { return [c.k, Math.max(0, c.q)]; });
      if (pts.length > 1) { b.path(pts, "fill-a", 0); b.path(pts, "ln ln--a"); }
      b.hline(0, 0, N, "ax");
      if (u >= 1 && best) { b.dot(best.index, best.q, 4, "dot dot--a"); b.vline(best.index, "vl vl--deploy"); }
    }

    if (reduce) { draw(1); return; }
    new IntersectionObserver(function (en) { visible = en[0].isIntersecting; }, { threshold: 0 }).observe(root);
    function tick(ts) {
      if (visible) {
        if (t0 === null) t0 = ts;
        var u = ((ts - t0) % 9000) / 6500; // 6.5 s scan, 2.5 s hold
        draw(u);
      }
      requestAnimationFrame(tick);
    }
    window.addEventListener("pgrr:resize", function () { draw(1); });
    requestAnimationFrame(tick);
  }

  // The page's name in the header is written with math symbols,
  // letter by letter (Our math -> ουг mατℏ). The real words stay for screen
  // readers and as the link's title, and they come back, scrambling in, while
  // the pointer or the keyboard focus is on a link.
  (function () {
    var SYM = {
      a: "α", b: "β", c: "∁", d: "∂", e: "ε", f: "∫", g: "ℊ", h: "ℏ", i: "ⅈ", j: "ⅉ", k: "κ", l: "ℓ", m: "m",
      n: "η", o: "ο", p: "ρ", q: "ℚ", r: "г", s: "σ", t: "τ", u: "υ", v: "ν", w: "ϖ", x: "×", y: "γ", z: "ℤ"
    };
    function sym(str) {
      var out = "";
      for (var i = 0; i < str.length; i++) {
        var c = str.charAt(i), m = SYM[c.toLowerCase()];
        out += m || c;
      }
      return out;
    }

    function convert(host, plain, hoverOn) {
      host.textContent = "";
      var vis = document.createElement("span");
      vis.className = "pgrr-sym";
      vis.setAttribute("aria-hidden", "true");
      var sr = document.createElement("span");
      sr.className = "pgrr-sr";
      sr.textContent = plain;
      host.appendChild(vis);
      host.appendChild(sr);
      var symbols = sym(plain);
      vis._t = symbols;
      vis.textContent = symbols;
      if (hoverOn) {
        var on = function () { setText(vis, plain); };
        var off = function () { setText(vis, symbols); };
        hoverOn.addEventListener("mouseenter", on);
        hoverOn.addEventListener("focus", on);
        hoverOn.addEventListener("mouseleave", off);
        hoverOn.addEventListener("blur", off);
      }
    }

    document.body.classList.add("pgrr-on-math");
    var tops = document.querySelectorAll(".md-header__title .md-header__topic .md-ellipsis");
    if (tops.length > 1) {
      var span = tops[tops.length - 1];
      convert(span, span.textContent.trim(), null);
    }
  })();

  // ---- boot --------------------------------------------------------------------
  var INIT = {
    pipe: initPipe, hero: initHero, lab: initLab, counters: initCounters, gates: initGates, energy: initEnergy,
    tol: initTol, welch: initWelch, sbb: initSbb, selection: initSelection, power: initPower
  };

  var figs = document.querySelectorAll("[data-w]");
  // the pipeline listens before the lab announces itself
  var order = ["pipe"];
  var i;
  for (i = 0; i < order.length; i++) {
    var p = document.querySelector('[data-w="' + order[i] + '"]');
    if (p) INIT[order[i]](p);
  }
  for (i = 0; i < figs.length; i++) {
    var name = figs[i].getAttribute("data-w");
    if (name !== "pipe" && INIT[name]) INIT[name](figs[i]);
  }

  // Keep the plots one-pixel-per-unit when the window changes size.
  var rz = 0;
  window.addEventListener("resize", function () {
    clearTimeout(rz);
    rz = setTimeout(function () {
      Plot.all.forEach(function (pl) { pl.refit(); });
      window.dispatchEvent(new CustomEvent("pgrr:resize"));
    }, 120);
  });

  // Reveal on scroll.
  if (!reduce && "IntersectionObserver" in window) {
    var targets = document.querySelectorAll(".pgrr-math .pgrr-text, .pgrr-math .pgrr-lab, .pgrr-math .pgrr-limits > div, .pgrr-math .pgrr-pipe");
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting) { en.target.classList.add("is-in"); io.unobserve(en.target); }
      });
    }, { rootMargin: "0px 0px -8% 0px", threshold: 0.04 });
    for (i = 0; i < targets.length; i++) {
      targets[i].classList.add("pgrr-reveal");
      io.observe(targets[i]);
    }
  }
})();
