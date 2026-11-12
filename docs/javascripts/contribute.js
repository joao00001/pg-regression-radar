/*
 * Contribute page (docs/contribute.md).
 *
 *  - The title block's background is a slowly drifting git graph on a canvas:
 *    a main line, branches that fork off it and merge back, commits as dots.
 *    Every few branches one is drawn in the accent colour (a pull request) and
 *    pulses where it merges. The commits carry the photos of the real
 *    contributors (the list people() loads), nodes pop in as they enter, light
 *    runs along the main line and the whole graph leans with the pointer. It
 *    keeps clear of the text (the CSS masks the left),
 *    follows the color scheme, honors prefers-reduced-motion (one still frame),
 *    pauses while off screen or in a hidden tab, and is decorative (aria-hidden).
 *  - Sections wipe in as they scroll into view (only when JavaScript runs).
 *  - The ways-to-help cells carry a soft light that follows the pointer.
 *  - The contributors' photos come from the public GitHub API (people, the few
 *    bots listed in BOTS, and anyone credited in a Co-authored-by trailer), photo only, and
 *    tilt toward the pointer. The last answer is cached for a few hours, so a
 *    reload (or GitHub's anonymous rate limit) never leaves the section empty.
 */
(function () {
  "use strict";

  var NS = "pgrr-contributors-v3";
  // Accounts of type "Bot" are left out, except these (the API reports them as
  // contributors too). Lower case; compared without regard to case.
  var BOTS = ["github-actions[bot]", "claude", "claude[bot]"];
  // The contributors API only counts commit authors. People credited in a
  // `Co-authored-by:` trailer are read from the commit messages instead; their
  // account comes from a GitHub noreply address (ID+login@users.noreply.github.com)
  // or from this table, for addresses that are not one.
  var CO_AUTHOR_ACCOUNTS = { "noreply@anthropic.com": "claude" };
  var COMMIT_PAGES = 3;
  var TTL = 6 * 60 * 60 * 1000;

  function reducedMotion() {
    return !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  }

  function init() {
    var root = document.querySelector(".pgrr-contribute");
    if (!root || root.getAttribute("data-ct-ready")) return;
    root.setAttribute("data-ct-ready", "1");
    root.classList.add("ct-js");
    reveal(root);
    glow(root);
    graph(root.querySelector(".ct-head"));
    people(root.querySelector(".ct-people"));
  }

  // ---- reveal on scroll ----------------------------------------------------------------
  function reveal(root) {
    var items = root.querySelectorAll(".ct-reveal");
    if (reducedMotion() || !("IntersectionObserver" in window)) {
      Array.prototype.forEach.call(items, function (el) { el.classList.add("is-in"); });
      return;
    }
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) {
          e.target.classList.add("is-in");
          io.unobserve(e.target);
        }
      });
    }, { threshold: 0.12, rootMargin: "0px 0px -6% 0px" });
    Array.prototype.forEach.call(items, function (el) { io.observe(el); });
  }

  // ---- pointer light on the ways-to-help cells ------------------------------------------
  function glow(root) {
    root.addEventListener("pointermove", function (e) {
      var cell = e.target.closest && e.target.closest("a.ct-way");
      if (!cell) return;
      var r = cell.getBoundingClientRect();
      cell.style.setProperty("--mx", (e.clientX - r.left) + "px");
      cell.style.setProperty("--my", (e.clientY - r.top) + "px");
    }, { passive: true });
  }

  // ---- git graph -------------------------------------------------------------------------
  function graph(head) {
    if (!head || head.querySelector(".ct-bg")) return;
    var box = document.createElement("div");
    box.className = "ct-bg";
    box.setAttribute("aria-hidden", "true");
    var canvas = document.createElement("canvas");
    box.appendChild(canvas);
    head.insertBefore(box, head.firstChild);
    var ctx = canvas.getContext("2d");
    if (!ctx) return;

    var SPEED = 20;          // px of graph that pass per second
    var SPACING = 300;       // average distance between branches
    var LANE = 38;           // vertical distance between lanes
    var OFFSETS = [-2, 1, -1, 2];
    var FPS = 120;           // effectively the display's own rate
    var W = 0, H = 0, dpr = 1;
    var c = { line: [160, 160, 160], dot: [0, 0, 0], accent: [220, 38, 38], fg: [255, 255, 255] };
    var reduce = reducedMotion();
    var born = performance.now();
    var faces = [];          // contributors' photos: { img, ok }
    var px = 0, py = 0, tx = 0, ty = 0;   // pointer lean, current and target (-1..1)

    function setPeople(list) {
      faces = [];
      (list || []).slice(0, 40).forEach(function (p) {
        var f = { img: new Image(), ok: false };
        f.img.onload = function () { f.ok = true; if (!running) redraw(); };
        f.img.src = p.avatar;
        faces.push(f);
      });
    }

    function hash(n) {
      var h = (n * 374761393 + 668265263) | 0;
      h = (h ^ (h >>> 13)) * 1274126177;
      h = h ^ (h >>> 16);
      return (h >>> 0) / 4294967295;
    }

    function parse(str, fallback) {
      var probe = document.createElement("canvas").getContext("2d");
      probe.fillStyle = "#000";
      probe.fillStyle = (str || "").trim() || fallback;
      var s = probe.fillStyle;
      if (s.charAt(0) === "#") {
        return [parseInt(s.slice(1, 3), 16), parseInt(s.slice(3, 5), 16), parseInt(s.slice(5, 7), 16)];
      }
      var m = s.match(/[\d.]+/g);
      return m ? [+m[0], +m[1], +m[2]] : [255, 255, 255];
    }

    function readColors() {
      var cs = getComputedStyle(head.closest(".pgrr-home") || head);
      c.line = parse(cs.getPropertyValue("--h-muted"), "#a3a8a7");
      c.dot = parse(cs.getPropertyValue("--h-hero-bg"), "#000");
      c.accent = parse(cs.getPropertyValue("--h-accent-solid"), "#dc2626");
      c.fg = parse(cs.getPropertyValue("--h-fg"), "#f4f6f4");
    }

    function rgba(a, alpha) {
      return "rgba(" + a[0] + "," + a[1] + "," + a[2] + "," + alpha + ")";
    }

    function resize() {
      var r = head.getBoundingClientRect();
      W = Math.max(1, Math.round(r.width));
      H = Math.max(1, Math.round(r.height));
      dpr = Math.min(window.devicePixelRatio || 1, 2);
      canvas.width = Math.round(W * dpr);
      canvas.height = Math.round(H * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      readColors();
    }

    // branch i forks from the main line at x0 and merges back after len px
    function branch(i) {
      var x0 = i * SPACING + hash(i) * 90;
      var len = 200 + hash(i + 101) * 170;
      var lane = OFFSETS[((i % 4) + 4) % 4];
      var commits = 2 + Math.floor(hash(i + 7) * 3);
      return { x0: x0, x1: x0 + len, lane: lane, commits: commits, pr: ((i % 5) + 5) % 5 === 2, i: i };
    }

    function ease(p) {            // overshoots a little, then settles
      var q = p - 1;
      return 1 + 2.70158 * q * q * q + 1.70158 * q * q;
    }

    // nodes grow as they come in from the right edge
    function pop(x) {
      if (reduce) return 1;
      var p = (W + 36 - x) / 150;
      return p <= 0 ? 0 : p >= 1 ? 1 : ease(p);
    }

    function frame(t) {
      ctx.clearRect(0, 0, W, H);
      var ready = faces.filter(function (f) { return f.ok; });
      function who(n) { return ready.length ? ready[Math.floor(hash(n * 7 + 3) * ready.length)] : null; }
      var life = reduce ? 1 : Math.min(1, (performance.now() - born) / 1400);
      px += (tx - px) * 0.06;
      py += (ty - py) * 0.06;
      ctx.save();
      ctx.globalAlpha = life * (2 - life);
      ctx.translate(-px * 14, -py * 8);
      var offset = t * SPEED;
      var mainY = H * 0.5;
      var first = Math.floor((offset - 260) / SPACING) - 1;
      var last = Math.ceil((offset + W + 80) / SPACING) + 1;
      var bs = [];
      for (var i = first; i <= last; i++) bs.push(branch(i));

      ctx.lineCap = "round";
      ctx.lineJoin = "round";

      // main line
      ctx.strokeStyle = rgba(c.line, 0.34);
      ctx.lineWidth = 1.2;
      ctx.beginPath();
      ctx.moveTo(-30, mainY);
      ctx.lineTo(W + 30, mainY);
      ctx.stroke();

      // main-line commits, evenly spaced; every few carry a contributor's photo
      var step = 74;
      var k0 = Math.floor(offset / step);
      for (var k = k0; k <= k0 + Math.ceil(W / step) + 1; k++) {
        var kx = k * step - offset;
        var kf = ((k % 3) + 3) % 3 === 0 ? who(k + 500) : null;
        if (kf) face(kx, mainY, 10, kf, false, 1);
        else dot(kx, mainY, 3.2, false, 0.8);
      }

      bs.forEach(function (b) {
        var x0 = b.x0 - offset, x1 = b.x1 - offset;
        if (x1 + 40 < 0 || x0 - 40 > W) return;
        var y = mainY + b.lane * LANE;
        var fork = 34;
        var col = b.pr ? c.accent : c.line;
        var alpha = b.pr ? 0.9 : 0.4;
        ctx.strokeStyle = rgba(col, alpha);
        ctx.lineWidth = b.pr ? 1.7 : 1.2;
        ctx.beginPath();
        ctx.moveTo(x0, mainY);
        ctx.bezierCurveTo(x0 + fork * 0.6, mainY, x0 + fork * 0.4, y, x0 + fork, y);
        ctx.lineTo(x1 - fork, y);
        ctx.bezierCurveTo(x1 - fork * 0.4, y, x1 - fork * 0.6, mainY, x1, mainY);
        ctx.stroke();

        // commits along the branch
        var span = (x1 - fork) - (x0 + fork);
        var mid = Math.floor(b.commits / 2);
        var bf = who(b.i);
        for (var j = 0; j < b.commits; j++) {
          var cx = x0 + fork + span * ((j + 0.5) / b.commits);
          if (j === mid && bf) {
            face(cx, y, b.pr ? 17 : 13, bf, b.pr, 1);
            if (b.pr && !reduce) {
              // a pull request breathes
              var br = 22 + Math.sin(t * 2.2 + b.i) * 3;
              ctx.strokeStyle = rgba(c.accent, 0.28);
              ctx.lineWidth = 1.2;
              ctx.beginPath();
              ctx.arc(cx, y, br * pop(cx), 0, Math.PI * 2);
              ctx.stroke();
            }
          } else {
            dot(cx, y, b.pr ? 3.6 : 3, b.pr, 1);
          }
        }

        // the merge: a ring that spreads and fades while the graph moves on
        if (b.pr) {
          var since = -x1;
          var life = 150;
          if (since > -10 && since < life) {
            var p = Math.max(0, since) / life;
            ctx.strokeStyle = rgba(c.accent, 0.7 * (1 - p));
            ctx.lineWidth = 1.4;
            ctx.beginPath();
            ctx.arc(x1, mainY, 4 + p * 22, 0, Math.PI * 2);
            ctx.stroke();
            var p2 = (Math.max(0, since) - 40) / life;
            if (p2 > 0 && p2 < 1) {
              ctx.strokeStyle = rgba(c.accent, 0.45 * (1 - p2));
              ctx.beginPath();
              ctx.arc(x1, mainY, 4 + p2 * 22, 0, Math.PI * 2);
              ctx.stroke();
            }
          }
          dot(x1, mainY, 4, true, 1);
        }
      });

      if (!reduce) pulses(t, offset, mainY, bs, step);
      ctx.restore();
    }

    // ---- pulses: a commit travelling from one profile to the next ----------------------------
    var PV = 90;          // px/s along the path
    var TAIL = 78;

    function bez(out, x0, y0, x1, y1, x2, y2, x3, y3) {
      for (var i = 1; i <= 10; i++) {
        var u = i / 10, v = 1 - u;
        out.push([
          v * v * v * x0 + 3 * v * v * u * x1 + 3 * v * u * u * x2 + u * u * u * x3,
          v * v * v * y0 + 3 * v * v * u * y1 + 3 * v * u * u * y2 + u * u * u * y3
        ]);
      }
    }

    function lengths(pts) {
      var cum = [0];
      for (var i = 1; i < pts.length; i++) {
        cum.push(cum[i - 1] + Math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]));
      }
      return cum;
    }

    function at(pts, cum, d) {
      if (d <= 0) return pts[0];
      var n = pts.length - 1;
      if (d >= cum[n]) return pts[n];
      var i = 1;
      while (cum[i] < d) i++;
      var u = (d - cum[i - 1]) / (cum[i] - cum[i - 1] || 1);
      return [pts[i - 1][0] + (pts[i][0] - pts[i - 1][0]) * u, pts[i - 1][1] + (pts[i][1] - pts[i - 1][1]) * u];
    }

    // light with a tail at distance d along the path
    function runner(pts, cum, d, col, a) {
      var N = 14;
      ctx.lineWidth = 2;
      for (var i = 0; i < N; i++) {
        var p0 = at(pts, cum, d - TAIL * (i + 1) / N);
        var p1 = at(pts, cum, d - TAIL * i / N);
        ctx.strokeStyle = rgba(col, a * Math.pow(1 - i / N, 1.6));
        ctx.beginPath();
        ctx.moveTo(p0[0], p0[1]);
        ctx.lineTo(p1[0], p1[1]);
        ctx.stroke();
      }
      var h = at(pts, cum, d);
      ctx.beginPath();
      ctx.arc(h[0], h[1], 7, 0, Math.PI * 2);
      ctx.fillStyle = rgba(col, a * 0.22);
      ctx.fill();
      ctx.beginPath();
      ctx.arc(h[0], h[1], 2.6, 0, Math.PI * 2);
      ctx.fillStyle = rgba(col, a);
      ctx.fill();
    }

    // a ring that spreads from a profile when the commit reaches it
    function landing(x, y, r0, p, col, reach) {
      if (p <= 0 || p >= 1) return;
      ctx.strokeStyle = rgba(col, 0.85 * (1 - p) * (1 - p));
      ctx.lineWidth = 2 * (1 - p) + 0.6;
      ctx.beginPath();
      ctx.arc(x, y, r0 + 2 + p * reach, 0, Math.PI * 2);
      ctx.stroke();
    }

    function pulses(t, offset, mainY, bs, step) {
      var trig = W * 0.6;     // a pulse leaves where the graph starts to fade in
      var RING = 0.9;         // seconds a landing ring lives
      ctx.lineCap = "round";

      // a pull request: leaves its branch's fork, passes the author's photo, merges
      // into the main line and lands on the next profile there
      bs.forEach(function (b) {
        var el = t - (b.x0 - trig) / SPEED;
        if (el < 0) return;
        var x0 = b.x0 - offset, x1 = b.x1 - offset, fork = 34;
        var y = mainY + b.lane * LANE;
        var kt = Math.ceil((b.x1 + 30) / step / 3) * 3;      // next main-line profile
        var tx2 = kt * step - offset;
        var pts = [[x0, mainY]];
        bez(pts, x0, mainY, x0 + fork * 0.6, mainY, x0 + fork * 0.4, y, x0 + fork, y);
        pts.push([x1 - fork, y]);
        bez(pts, x1 - fork, y, x1 - fork * 0.4, y, x1 - fork * 0.6, mainY, x1, mainY);
        pts.push([tx2, mainY]);
        var cum = lengths(pts);
        var total = cum[cum.length - 1];
        var d = el * PV;
        var col = b.pr ? c.accent : c.fg;
        // the author's photo, reached on the straight part of the branch
        var span = (x1 - fork) - (x0 + fork);
        var mid = Math.floor(b.commits / 2);
        var fx = x0 + fork + span * ((mid + 0.5) / b.commits);
        var dFace = cum[10] + (fx - (x0 + fork));
        if (d - dFace > 0) landing(fx, y, b.pr ? 17 : 13, (d - dFace) / PV / RING, col, 18);
        if (d - total > 0) landing(tx2, mainY, 10, (d - total) / PV / (RING + 0.2), col, b.pr ? 30 : 20);
        if (d > 0 && d < total + 10) runner(pts, cum, Math.min(d, total), col, b.pr ? 0.95 : 0.6);
      });

      // a commit on the main line: from one profile to the next
      var kmin = Math.floor((offset + trig - 600) / step / 3) * 3;
      for (var k = kmin; k < kmin + 3 * Math.ceil(700 / step / 3) + 6; k += 3) {
        if (hash(k + 900) > 0.55) continue;
        var kx = k * step;
        var el2 = t - (kx - trig) / SPEED;
        var dist = 3 * step;
        if (el2 < 0) continue;
        var sx = kx - offset;
        var d2 = el2 * PV;
        if (d2 > dist + RING * PV + 30) continue;
        var pts2 = [[sx, mainY], [sx + dist, mainY]];
        var cum2 = [0, dist];
        if (d2 > 0 && d2 < dist + 10) runner(pts2, cum2, Math.min(d2, dist), c.fg, 0.55);
        if (d2 > dist) landing(sx + dist, mainY, 10, (d2 - dist) / PV / RING, c.fg, 18);
      }
    }

    // a commit drawn as its author's photo
    function face(x, y, r, f, accent, a) {
      if (x < -20 || x > W + 40) return;
      var rr = r * pop(x);
      if (rr < 0.5) return;
      ctx.save();
      ctx.beginPath();
      ctx.arc(x, y, rr, 0, Math.PI * 2);
      ctx.fillStyle = rgba(c.dot, 1);
      ctx.fill();
      ctx.clip();
      ctx.drawImage(f.img, x - rr, y - rr, rr * 2, rr * 2);
      ctx.restore();
      ctx.beginPath();
      ctx.arc(x, y, rr, 0, Math.PI * 2);
      ctx.lineWidth = accent ? 2 : 1.4;
      ctx.strokeStyle = rgba(accent ? c.accent : c.line, accent ? 1 : 0.8 * a);
      ctx.stroke();
    }

    function dot(x, y, r, accent, a) {
      if (x < -10 || x > W + 40) return;
      r *= pop(x);
      if (r < 0.4) return;
      ctx.beginPath();
      ctx.arc(x, y, r, 0, Math.PI * 2);
      ctx.fillStyle = rgba(c.dot, 1);
      ctx.fill();
      ctx.lineWidth = 1.4;
      ctx.strokeStyle = rgba(accent ? c.accent : c.line, accent ? 1 : 0.75 * a);
      ctx.stroke();
    }

    // ---- loop ----
    var visible = true, running = false, last = 0, t0 = performance.now(), shift = 0;

    function tick(now) {
      if (!running) return;
      requestAnimationFrame(tick);
      if (now - last < 1000 / FPS) return;
      last = now;
      frame((now - t0) / 1000 + shift);
    }

    function start() {
      if (running || reduce || !visible || document.hidden) return;
      running = true;
      t0 = performance.now();
      requestAnimationFrame(tick);
    }

    function stop() {
      if (!running) return;
      shift += (performance.now() - t0) / 1000;
      running = false;
    }

    function redraw() {
      frame(reduce ? 40 : shift + (running ? (performance.now() - t0) / 1000 : 0));
    }

    var host = head.closest(".pgrr-contribute");
    if (host) host.addEventListener("ct:people", function (e) { setPeople(e.detail); });
    if (!reduce) {
      head.addEventListener("pointermove", function (e) {
        var r = head.getBoundingClientRect();
        tx = ((e.clientX - r.left) / r.width) * 2 - 1;
        ty = ((e.clientY - r.top) / r.height) * 2 - 1;
      }, { passive: true });
      head.addEventListener("pointerleave", function () { tx = 0; ty = 0; });
    }

    resize();
    frame(40);
    if ("ResizeObserver" in window) {
      new ResizeObserver(function () { resize(); redraw(); }).observe(head);
    } else {
      window.addEventListener("resize", function () { resize(); frame(40); });
    }
    if ("IntersectionObserver" in window) {
      new IntersectionObserver(function (es) {
        visible = es[0].isIntersecting;
        visible ? start() : stop();
      }).observe(head);
    }
    document.addEventListener("visibilitychange", function () { document.hidden ? stop() : start(); });
    // follow the light/dark switch
    new MutationObserver(function () { readColors(); frame(shift + (running ? (performance.now() - t0) / 1000 : 0)); })
      .observe(document.body, { attributes: true, attributeFilter: ["data-md-color-scheme"] });
    start();
  }

  // ---- contributors ---------------------------------------------------------------------------
  function readCache() {
    try {
      var raw = JSON.parse(localStorage.getItem(NS) || "null");
      if (raw && Array.isArray(raw.list)) return raw;
    } catch (e) { /* storage unavailable */ }
    return null;
  }

  function writeCache(list) {
    try { localStorage.setItem(NS, JSON.stringify({ t: Date.now(), list: list })); } catch (e) { /* ignore */ }
  }

  function api(url) {
    return fetch(url, { headers: { Accept: "application/vnd.github+json" } }).then(function (res) {
      if (!res.ok) throw new Error("HTTP " + res.status);
      return res.json();
    });
  }

  // logins credited in `Co-authored-by:` trailers, from the latest commits
  function coAuthors(repo) {
    var found = [];
    var re = /^co-authored-by:[^<\n]*<([^>\n]+)>/gim;
    function page(n) {
      return api("https://api.github.com/repos/" + repo + "/commits?per_page=100&page=" + n).then(function (commits) {
        (commits || []).forEach(function (c) {
          var msg = (c && c.commit && c.commit.message) || "";
          var m;
          re.lastIndex = 0;
          while ((m = re.exec(msg))) {
            var email = m[1].trim().toLowerCase();
            var noreply = email.match(/^(?:\d+\+)?([^@]+)@users\.noreply\.github\.com$/);
            var login = noreply ? noreply[1] : CO_AUTHOR_ACCOUNTS[email];
            if (login) found.push(login);
          }
        });
        return commits && commits.length === 100 && n < COMMIT_PAGES ? page(n + 1) : found;
      });
    }
    return page(1).catch(function () { return found; });
  }

  function people(box) {
    if (!box) return;
    var repo = box.getAttribute("data-repo");
    var wrap = box.closest(".ct-people-wrap");
    var count = wrap && wrap.querySelector(".ct-people-count");
    var fallback = wrap && wrap.querySelector(".ct-people-fallback");
    var cached = readCache();

    var skels = [];
    for (var s = 0; s < 6; s++) {
      var sk = document.createElement("span");
      sk.className = "ct-skel";
      sk.setAttribute("aria-hidden", "true");
      box.insertBefore(sk, box.firstChild);
      skels.push(sk);
    }

    function render(list) {
      skels.forEach(function (n) { if (n.parentNode) n.parentNode.removeChild(n); });
      Array.prototype.forEach.call(box.querySelectorAll(".ct-person"), function (n) { n.parentNode.removeChild(n); });
      var you = box.querySelector(".ct-you");
      list.forEach(function (p, i) {
        var a = document.createElement("a");
        a.className = "ct-person";
        a.href = p.url;
        a.target = "_blank";
        a.rel = "noopener";
        a.setAttribute("aria-label", p.login + " on GitHub");
        a.style.setProperty("--i", i);
        var img = document.createElement("img");
        img.onerror = function () {
          // an account that no longer resolves: drop its tile
          if (a.parentNode) a.parentNode.removeChild(a);
          if (count) {
            var left = box.querySelectorAll(".ct-person").length;
            count.textContent = left + (left === 1 ? " contributor" : " contributors");
          }
        };
        img.src = p.avatar;
        img.alt = "";
        img.width = 64;
        img.height = 64;
        img.loading = "lazy";
        img.decoding = "async";
        a.appendChild(img);
        box.insertBefore(a, you);
      });
      box.setAttribute("data-state", "ready");
      var host = box.closest(".pgrr-contribute");
      if (host) host.dispatchEvent(new CustomEvent("ct:people", { detail: list }));
      if (count) count.textContent = list.length + (list.length === 1 ? " contributor" : " contributors");
      if (fallback) fallback.hidden = true;
      tilt(box);
    }

    function fail() {
      skels.forEach(function (n) { if (n.parentNode) n.parentNode.removeChild(n); });
      box.setAttribute("data-state", "error");
      if (fallback) fallback.hidden = false;
    }

    if (cached && Date.now() - cached.t < TTL && cached.list.length) {
      render(cached.list);
      return;
    }
    if (typeof fetch !== "function") { cached && cached.list.length ? render(cached.list) : fail(); return; }

    api("https://api.github.com/repos/" + repo + "/contributors?per_page=100").then(function (rows) {
      var seen = {};
      var list = [];
      (rows || []).forEach(function (r) {
        if (!r || !r.login || seen[r.login.toLowerCase()]) return;
        if (r.type !== "User" && BOTS.indexOf(String(r.login).toLowerCase()) < 0) return;
        seen[r.login.toLowerCase()] = 1;
        var avatar = String(r.avatar_url || "");
        avatar += (avatar.indexOf("?") < 0 ? "?" : "&") + "s=160";
        list.push({ login: r.login, url: r.html_url, avatar: avatar });
      });
      // then everyone credited as a co-author who is not already there
      return coAuthors(repo).then(function (logins) {
        logins.forEach(function (login) {
          var key = login.toLowerCase();
          if (seen[key]) return;
          seen[key] = 1;
          list.push({ login: login, url: "https://github.com/" + login, avatar: "https://github.com/" + login + ".png?size=160" });
        });
        if (!list.length) throw new Error("empty");
        writeCache(list);
        render(list);
      });
    }).catch(function () {
      if (cached && cached.list.length) render(cached.list); else fail();
    });
  }

  // photos lean toward the pointer
  function tilt(box) {
    if (reducedMotion() || box.getAttribute("data-tilt")) return;
    box.setAttribute("data-tilt", "1");
    box.addEventListener("pointermove", function (e) {
      var a = e.target.closest && e.target.closest("a.ct-person");
      if (!a) return;
      var r = a.getBoundingClientRect();
      var dx = (e.clientX - r.left) / r.width - 0.5;
      var dy = (e.clientY - r.top) / r.height - 0.5;
      a.style.setProperty("--ry", (dx * 16).toFixed(1) + "deg");
      a.style.setProperty("--rx", (-dy * 16).toFixed(1) + "deg");
    }, { passive: true });
    box.addEventListener("pointerout", function (e) {
      var a = e.target.closest && e.target.closest("a.ct-person");
      if (!a) return;
      a.style.removeProperty("--rx");
      a.style.removeProperty("--ry");
    });
  }

  if (window.document$ && window.document$.subscribe) {
    window.document$.subscribe(init);
  } else if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
