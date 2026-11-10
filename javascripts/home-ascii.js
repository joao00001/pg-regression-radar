/*
 * Home page: the hero's background (title and code block share it) is a slowly
 * drifting field of ASCII characters on a canvas.
 *
 * Soft noise decides, per character cell, how "dense" the field is. Sparse
 * areas are dots and colons, denser ones are + - | : patterns, and the densest
 * spell a repeating word that scrolls sideways; where the density changes,
 * characters flicker through a few symbols. The field is faded out around the
 * text so it never fights with it (the code card is opaque and simply floats
 * over the field), and it follows the page's color scheme.
 *
 * Honors prefers-reduced-motion (one static frame), pauses while off screen or
 * in a hidden tab, and is purely decorative (aria-hidden).
 */
(function () {
  "use strict";

  var cell = document.querySelector(".pgrr-hero");
  if (!cell) return;

  var WORD = "REGRESSION";
  var SYMBOLS = "$&=>70%#@*";
  var FONT = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
  var FS = 10;       // font size in css px
  var CW = 6.2;      // cell width
  var CH = 9.6;      // cell height
  var LEVELS = 6;    // alpha buckets, so each frame sets fillStyle only a few times
  var FPS = 24;

  var canvas = document.createElement("canvas");
  canvas.className = "pgrr-hero-ascii";
  canvas.setAttribute("aria-hidden", "true");
  // last child, so the hero's `> div:first-child` styles keep matching the title cell
  cell.appendChild(canvas);
  var ctx = canvas.getContext("2d");
  if (!ctx) return;

  var reduce = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;

  // ---- noise ----------------------------------------------------------------
  function hash(x, y, z) {
    var h = (x * 374761393 + y * 668265263 + z * 2147483647) | 0;
    h = (h ^ (h >>> 13)) * 1274126177;
    h = h ^ (h >>> 16);
    return (h >>> 0) / 4294967295;
  }

  function smooth(t) { return t * t * (3 - 2 * t); }

  function vnoise(x, y, z) {
    var xi = Math.floor(x), yi = Math.floor(y), zi = Math.floor(z);
    var xf = smooth(x - xi), yf = smooth(y - yi), zf = smooth(z - zi);
    function l(a, b, t) { return a + (b - a) * t; }
    var c000 = hash(xi, yi, zi), c100 = hash(xi + 1, yi, zi);
    var c010 = hash(xi, yi + 1, zi), c110 = hash(xi + 1, yi + 1, zi);
    var c001 = hash(xi, yi, zi + 1), c101 = hash(xi + 1, yi, zi + 1);
    var c011 = hash(xi, yi + 1, zi + 1), c111 = hash(xi + 1, yi + 1, zi + 1);
    return l(l(l(c000, c100, xf), l(c010, c110, xf), yf),
             l(l(c001, c101, xf), l(c011, c111, xf), yf), zf);
  }

  function field(i, j, t) {
    // two octaves drifting in different directions
    var a = vnoise(i * 0.028 + t * 0.06, j * 0.055 - t * 0.035, t * 0.1);
    var b = vnoise(i * 0.085 - t * 0.045, j * 0.15 + t * 0.05, t * 0.17 + 7.3);
    return a * 0.72 + b * 0.28;
  }

  // ---- layout: where the text is, so the field can stay clear of it ------------------
  var W = 0, H = 0, dpr = 1, cols = 0, rows = 0, avoid = null, rgb = [255, 255, 255];

  function textRects() {
    var base = cell.getBoundingClientRect();
    var rects = [];
    function push(r) {
      if (r.width < 1 || r.height < 1) return;
      rects.push([r.left - base.left, r.top - base.top, r.right - base.left, r.bottom - base.top]);
    }
    var nodes = cell.querySelectorAll("h1, .pgrr-lead, .md-button, .pgrr-hero-cap");
    for (var k = 0; k < nodes.length; k++) {
      var n = nodes[k];
      if (n.tagName === "H1" || n.classList.contains("pgrr-lead") || n.classList.contains("pgrr-hero-cap")) {
        var range = document.createRange();
        range.selectNodeContents(n);
        push(range.getBoundingClientRect());
      } else {
        push(n.getBoundingClientRect());
      }
    }
    return rects;
  }

  function buildAvoid() {
    var rects = textRects();
    avoid = new Float32Array(cols * rows);
    for (var j = 0; j < rows; j++) {
      for (var i = 0; i < cols; i++) {
        var x = (i + 0.5) * CW, y = (j + 0.5) * CH, dmin = 1e9;
        for (var k = 0; k < rects.length; k++) {
          var r = rects[k];
          var dx = Math.max(r[0] - x, 0, x - r[2]);
          var dy = Math.max(r[1] - y, 0, y - r[3]);
          var d = Math.sqrt(dx * dx + dy * dy);
          if (d < dmin) dmin = d;
        }
        var t = Math.max(0, Math.min(1, (dmin - 10) / 46));
        avoid[j * cols + i] = smooth(t);
      }
    }
  }

  function readColor() {
    var cs = getComputedStyle(cell.closest(".pgrr-home") || cell);
    var c = (cs.getPropertyValue("--h-fg") || "#f4f6f4").trim();
    var probe = document.createElement("canvas").getContext("2d");
    probe.fillStyle = "#fff";
    probe.fillStyle = c;
    var s = probe.fillStyle;
    if (s.charAt(0) === "#") {
      rgb = [parseInt(s.slice(1, 3), 16), parseInt(s.slice(3, 5), 16), parseInt(s.slice(5, 7), 16)];
    } else {
      var m = s.match(/[\d.]+/g);
      if (m) rgb = [+m[0], +m[1], +m[2]];
    }
  }

  function resize() {
    var r = cell.getBoundingClientRect();
    W = Math.max(1, Math.round(r.width));
    H = Math.max(1, Math.round(r.height));
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    canvas.width = Math.round(W * dpr);
    canvas.height = Math.round(H * dpr);
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    cols = Math.ceil(W / CW);
    rows = Math.ceil(H / CH);
    buildAvoid();
    draw(performance.now());
  }

  // ---- drawing --------------------------------------------------------------------------
  var buckets = [];
  for (var b = 0; b < LEVELS; b++) buckets.push([]);

  function draw(now) {
    var t = now / 1000;
    ctx.clearRect(0, 0, W, H);
    ctx.font = FS + "px " + FONT;
    ctx.textBaseline = "top";
    for (var q = 0; q < LEVELS; q++) buckets[q].length = 0;
    var shift = Math.floor(t * 5);
    var tick = Math.floor(t * 9);

    for (var j = 0; j < rows; j++) {
      for (var i = 0; i < cols; i++) {
        var v = field(i, j, t) * avoid[j * cols + i];
        // the field is only ever mid-range; stretch it so it reaches the top levels
        v = (v - 0.24) * 1.75;
        if (v < 0.14) continue;
        if (v > 1) v = 1;
        var ch;
        if (v < 0.3) {
          ch = ".";
        } else if (v < 0.46) {
          ch = ((i + j) & 1) ? ":" : ".";
        } else if (v < 0.64) {
          var h2 = hash(i >> 1, j, 3);
          ch = h2 < 0.34 ? "+" : h2 < 0.6 ? ":" : h2 < 0.82 ? "|" : "-";
        } else {
          // the densest areas spell the word, scrolling sideways, each row offset
          ch = WORD.charAt(((i + shift + j * 3) % WORD.length + WORD.length) % WORD.length);
          // a few characters flicker through symbols where the density is still changing
          if (v < 0.8 && hash(i, j, tick) < 0.07) {
            ch = SYMBOLS.charAt(Math.floor(hash(j, i, tick) * SYMBOLS.length));
          }
        }
        var lvl = Math.min(LEVELS - 1, Math.floor(v * LEVELS));
        buckets[lvl].push(ch, i * CW, j * CH);
      }
    }

    for (var L = 0; L < LEVELS; L++) {
      var list = buckets[L];
      if (!list.length) continue;
      var a = 0.1 + (L / (LEVELS - 1)) * 0.5;
      ctx.fillStyle = "rgba(" + rgb[0] + "," + rgb[1] + "," + rgb[2] + "," + a.toFixed(3) + ")";
      for (var k = 0; k < list.length; k += 3) ctx.fillText(list[k], list[k + 1], list[k + 2]);
    }
  }

  // ---- loop -------------------------------------------------------------------------------
  var running = false, visible = true, last = 0, t0 = performance.now();

  function frame(ts) {
    if (!running) return;
    if (ts - last >= 1000 / FPS) {
      last = ts;
      draw(ts - t0 + 4000);
    }
    requestAnimationFrame(frame);
  }

  function start() {
    if (reduce || running || !visible || document.hidden) return;
    running = true;
    requestAnimationFrame(frame);
  }

  function stop() { running = false; }

  readColor();
  resize();

  if ("ResizeObserver" in window) {
    new ResizeObserver(resize).observe(cell);
  } else {
    window.addEventListener("resize", resize);
  }
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(function () { buildAvoid(); draw(performance.now()); });
  }
  new MutationObserver(function () { readColor(); draw(performance.now() - t0 + 4000); }).observe(document.body, {
    attributes: true,
    attributeFilter: ["data-md-color-scheme"]
  });
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (entries) {
      visible = entries[0].isIntersecting;
      if (visible) start(); else stop();
    }, { threshold: 0.05 }).observe(cell);
  }
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) stop(); else start();
  });

  start();
})();
