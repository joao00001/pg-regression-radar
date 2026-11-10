/*
 * "Our math": the one background on the page, behind the title block. It is a
 * contour map (level sets of a seeded height field, found by marching
 * squares) drawn in level by level and then drifting slowly: level sets are
 * what an energy landscape is made of.
 *
 * Decorative (aria-hidden, no pointer events), follows the color scheme
 * through CSS, and holds still with prefers-reduced-motion.
 */
(function () {
  "use strict";

  var host = document.querySelector(".pgrr-math header.pgrr-paper");
  if (!host) return;

  var reduce = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
  var NS = "http://www.w3.org/2000/svg";

  function hash(x, y, s) {
    var h = (x * 374761393 + y * 668265263 + s * 2147483647) | 0;
    h = (h ^ (h >>> 13)) * 1274126177;
    return ((h ^ (h >>> 16)) >>> 0) / 4294967295;
  }

  function smooth(t) { return t * t * (3 - 2 * t); }

  function vnoise(x, y, s) {
    var xi = Math.floor(x), yi = Math.floor(y), xf = smooth(x - xi), yf = smooth(y - yi);
    var a = hash(xi, yi, s), b = hash(xi + 1, yi, s), c = hash(xi, yi + 1, s), d = hash(xi + 1, yi + 1, s);
    return a + (b - a) * xf + (c - a) * yf + (a - b - c + d) * xf * yf;
  }

  var box = document.createElement("div");
  box.className = "pgrr-bg pgrr-bg--topo";
  box.setAttribute("aria-hidden", "true");
  host.insertBefore(box, host.firstChild);
  var svg = document.createElementNS(NS, "svg");
  svg.setAttribute("preserveAspectRatio", "xMidYMid slice");
  box.appendChild(svg);

  var GW = 84, GH = 52, LEVELS = 14, SEED = 3;
  var h = [], x, y;
  for (y = 0; y <= GH; y++) {
    h[y] = [];
    for (x = 0; x <= GW; x++) {
      h[y][x] = 0.7 * vnoise(x / 11, y / 11, SEED) + 0.3 * vnoise(x / 5.5 + 40, y / 5.5 + 40, SEED + 1);
    }
  }
  svg.setAttribute("viewBox", "0 0 " + GW + " " + GH);

  function seg(d, x1, y1, x2, y2) { return d + "M" + x1.toFixed(2) + " " + y1.toFixed(2) + "L" + x2.toFixed(2) + " " + y2.toFixed(2); }

  for (var L = 0; L < LEVELS; L++) {
    var lv = 0.22 + (0.56 * L) / (LEVELS - 1), d = "";
    for (y = 0; y < GH; y++) {
      for (x = 0; x < GW; x++) {
        var a = h[y][x], b = h[y][x + 1], c = h[y + 1][x + 1], e = h[y + 1][x];
        var idx = (a > lv ? 8 : 0) | (b > lv ? 4 : 0) | (c > lv ? 2 : 0) | (e > lv ? 1 : 0);
        if (idx === 0 || idx === 15) continue;
        var t = { x: x + (lv - a) / (b - a), y: y }, r = { x: x + 1, y: y + (lv - b) / (c - b) };
        var bt = { x: x + (lv - e) / (c - e), y: y + 1 }, lf = { x: x, y: y + (lv - a) / (e - a) };
        switch (idx) {
          case 1: case 14: d = seg(d, lf.x, lf.y, bt.x, bt.y); break;
          case 2: case 13: d = seg(d, bt.x, bt.y, r.x, r.y); break;
          case 3: case 12: d = seg(d, lf.x, lf.y, r.x, r.y); break;
          case 4: case 11: d = seg(d, t.x, t.y, r.x, r.y); break;
          case 5: d = seg(seg(d, t.x, t.y, lf.x, lf.y), bt.x, bt.y, r.x, r.y); break;
          case 6: case 9: d = seg(d, t.x, t.y, bt.x, bt.y); break;
          case 7: case 8: d = seg(d, t.x, t.y, lf.x, lf.y); break;
          case 10: d = seg(seg(d, t.x, t.y, r.x, r.y), lf.x, lf.y, bt.x, bt.y); break;
        }
      }
    }
    var p = document.createElementNS(NS, "path");
    p.setAttribute("d", d);
    p.setAttribute("class", L % 5 === 0 ? "is-index" : "");
    p.setAttribute("vector-effect", "non-scaling-stroke");
    p.style.setProperty("--i", L);
    svg.appendChild(p);
  }
  if (reduce) box.classList.add("is-in", "is-still");
  else requestAnimationFrame(function () { box.classList.add("is-in"); });
})();
