/*
 * Home page: a live, simulated "regression capture".
 *
 * Four queries stream latency samples across a log-scale chart. Every so often
 * a deploy lands (a dashed marker travels with the data); one query steps up,
 * a scan beam sweeps from the deploy to the present, the regression is
 * detected (rings, a tag, the query turns accent) and a while later it is
 * rolled back. The data is made up: nothing here talks to a server.
 *
 * Interactive: hover (or touch) pauses the stream and scrubs a crosshair with
 * a tooltip; the legend chips pick which query the next deploy hits; the
 * button injects a deploy now. Honors prefers-reduced-motion (no animation
 * loop, it redraws on interaction only) and pauses while off screen / hidden.
 */
(function () {
  "use strict";

  var root = document.querySelector(".pgrr-radar");
  if (!root) return;
  var canvas = root.querySelector(".pgrr-radar-canvas");
  var stage = root.querySelector(".pgrr-radar-stage");
  var miniCanvas = root.querySelector(".pgrr-radar-mini");
  var legendEl = root.querySelector(".pgrr-radar-legend");
  var statusEl = root.querySelector(".pgrr-radar-status");
  var liveEl = root.querySelector(".pgrr-radar-live");
  var btn = root.querySelector(".pgrr-radar-inject");
  var tip = root.querySelector(".pgrr-radar-tip");
  if (!canvas || !stage) return;

  var ctx = canvas.getContext("2d");
  var mctx = miniCanvas ? miniCanvas.getContext("2d") : null;
  var reduce = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;

  // ---- model ----------------------------------------------------------
  var RATE = 20;            // samples per second
  var DT = 1 / RATE;
  var STEP = 3;             // pixels per sample (60 px/s)
  var REGRESS_FOR = 9;      // seconds a regression lasts before the rollback
  var DETECT_AFTER = 1.5;   // seconds from deploy to detection
  var AUTO_EVERY = 11;      // seconds between automatic deploys

  var SERIES = [
    { name: "orders", sql: "SELECT … customer_id", id: "4821", base: 4.1 },
    { name: "users", sql: "UPDATE … last_seen", id: "1093", base: 7.2 },
    { name: "invoices", sql: "INSERT INTO invoices", id: "7740", base: 12.5 },
    { name: "search", sql: "SELECT … ILIKE", id: "3356", base: 22 }
  ];
  var SERVICES = [
    ["checkout-api", "v2"], ["orders-svc", "v14"], ["billing-api", "v7"],
    ["search-api", "v31"], ["users-svc", "v9"]
  ];

  var LMIN = Math.log10(2);
  var LMAX = Math.log10(110);

  var n = 0;                // absolute index of the newest sample
  var acc = 0;              // time accumulated toward the next sample
  var autoIn = 2;           // seconds until the next automatic deploy
  var selected = -1;        // series the next deploy hits (-1 = random)
  var events = [];
  var state = SERIES.map(function (s) { return { e: 0, m: 1, hist: [] }; });
  var svcCursor = 0;

  function randn() {
    return (Math.random() + Math.random() + Math.random() - 1.5) / 0.5;
  }

  function pushSample() {
    n += 1;
    for (var i = 0; i < SERIES.length; i++) {
      var st = state[i];
      st.e = st.e * 0.9 + randn() * 0.045;
      var target = 1;
      for (var k = 0; k < events.length; k++) {
        var ev = events[k];
        if (ev.culprit === i && n >= ev.n && n < ev.rollbackN) target = ev.mult;
      }
      st.m += (target - st.m) * 0.45;
      var v = SERIES[i].base * Math.exp(st.e) * st.m;
      if (Math.random() < 0.012) v *= 1.5 + Math.random() * 0.6;
      st.hist.push(v);
      if (st.hist.length > 700) st.hist.shift();
    }
    // detection and expiry
    for (var j = events.length - 1; j >= 0; j--) {
      var e2 = events[j];
      if (!e2.detected && n >= e2.n + DETECT_AFTER * RATE) {
        e2.detected = true;
        e2.pingAt = performance.now();
        announce(e2);
        blip();
      }
      if (n - e2.n > (REGRESS_FOR + 40) * RATE) events.splice(j, 1);
    }
  }

  function injectDeploy() {
    var c = selected >= 0 ? selected : Math.floor(Math.random() * SERIES.length);
    var maxMult = Math.min(10, 85 / SERIES[c].base);
    var mult = 3 + Math.random() * (maxMult - 3);
    var svc = SERVICES[svcCursor++ % SERVICES.length];
    events.push({
      n: n,
      culprit: c,
      mult: mult,
      conf: 0.9 + Math.random() * 0.09,
      label: svc[0] + " · " + svc[1],
      rollbackN: n + REGRESS_FOR * RATE,
      detected: false,
      pingAt: 0,
      beamAt: performance.now()
    });
    autoIn = AUTO_EVERY;
  }

  function advance(seconds) {
    acc += seconds;
    while (acc >= DT) {
      acc -= DT;
      autoIn -= DT;
      if (autoIn <= 0) injectDeploy();
      pushSample();
    }
  }

  // ---- colors (read from the page's theme tokens) ------------------------
  var C = {};
  var probe = document.createElement("canvas").getContext("2d");

  function rgb(color, fallback) {
    probe.fillStyle = "#000";
    probe.fillStyle = color || fallback;
    var s = probe.fillStyle;
    if (s.charAt(0) === "#") {
      var h = s.slice(1);
      return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
    }
    var m = s.match(/[\d.]+/g) || [128, 128, 128];
    return [+m[0], +m[1], +m[2]];
  }

  function rgba(c, a) {
    return "rgba(" + c[0] + "," + c[1] + "," + c[2] + "," + a + ")";
  }

  function readColors() {
    var cs = getComputedStyle(root);
    var g = function (name, fb) { return cs.getPropertyValue(name).trim() || fb; };
    C.fg = rgb(g("--h-fg"), "#f4f6f4");
    C.muted = rgb(g("--h-muted"), "#a3a8a7");
    C.icon = rgb(g("--h-icon"), "#aeb2b1");
    C.border = rgb(g("--h-border"), "#3a3d3d");
    C.accent = rgb(g("--h-accent"), "#ff6b6b");
    C.solid = rgb(g("--h-accent-solid"), "#e03131");
    C.panel = rgb(g("--h-panel"), "#0b0c0e");
    C.card = rgb(g("--h-card"), "#1d1f1f");
  }

  // ---- layout ---------------------------------------------------------------
  var W = 0, H = 0, dpr = 1;
  var PAD = { l: 52, r: 28, t: 46, b: 30 };

  function resize() {
    var r = stage.getBoundingClientRect();
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = Math.max(280, Math.round(r.width));
    H = Math.max(180, Math.round(r.height));
    canvas.width = W * dpr;
    canvas.height = H * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    PAD.l = W < 520 ? 50 : 52;
    PAD.r = W < 520 ? 84 : 94;
    // a chart is one sample per STEP pixels; keep enough history for the width
    PAD.visible = Math.ceil((W - PAD.l - PAD.r) / STEP) + 4;
    if (miniCanvas) {
      var ms = 44;
      miniCanvas.width = ms * dpr;
      miniCanvas.height = ms * dpr;
      mctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    }
    draw();
  }

  function yOf(v) {
    var t = (Math.log10(Math.max(v, 1)) - LMIN) / (LMAX - LMIN);
    t = Math.max(0, Math.min(1, t));
    return PAD.t + (1 - t) * (H - PAD.t - PAD.b);
  }

  function xOfIndex(idx) {
    return W - PAD.r - (n - idx) * STEP;
  }

  // ---- hover state -------------------------------------------------------------
  var hover = { on: false, x: 0, y: 0 };

  // ---- drawing -----------------------------------------------------------------
  function roundRect(x, y, w, h, r) {
    ctx.beginPath();
    ctx.moveTo(x + r, y);
    ctx.arcTo(x + w, y, x + w, y + h, r);
    ctx.arcTo(x + w, y + h, x, y + h, r);
    ctx.arcTo(x, y + h, x, y, r);
    ctx.arcTo(x, y, x + w, y, r);
    ctx.closePath();
  }

  // Path for series i; optionally only the samples with absolute index in
  // [from, to]. Returns false when nothing is visible.
  function plotLine(i, from, to) {
    var hist = state[i].hist;
    var len = hist.length;
    var first = Math.max(0, len - PAD.visible);
    ctx.beginPath();
    var started = false;
    for (var k = first; k < len; k++) {
      var idx = n - (len - 1 - k);
      if (from !== undefined && (idx < from || idx > to)) continue;
      var x = xOfIndex(idx);
      if (x < PAD.l - 2) continue;
      var y = yOf(hist[k]);
      if (!started) { ctx.moveTo(x, y); started = true; } else { ctx.lineTo(x, y); }
    }
    return started;
  }

  function draw() {
    if (!W) return;
    var now = performance.now();
    ctx.clearRect(0, 0, W, H);
    ctx.font = "10px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
    ctx.textBaseline = "middle";

    // log grid
    var ticks = [2, 5, 10, 20, 50, 100];
    for (var t = 0; t < ticks.length; t++) {
      var gy = Math.round(yOf(ticks[t])) + 0.5;
      var major = ticks[t] === 10 || ticks[t] === 100;
      ctx.strokeStyle = rgba(C.border, major ? 0.8 : 0.4);
      ctx.lineWidth = 1;
      ctx.setLineDash(major ? [] : [2, 4]);
      ctx.beginPath();
      ctx.moveTo(PAD.l, gy);
      ctx.lineTo(W - PAD.r, gy);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = rgba(C.muted, major ? 0.95 : 0.6);
      ctx.textAlign = "right";
      ctx.fillText(ticks[t] + " ms", PAD.l - 8, gy);
    }

    // clip everything to the plot area
    ctx.save();
    ctx.beginPath();
    ctx.rect(PAD.l, 0, W - PAD.l - PAD.r + 6, H);
    ctx.clip();

    // deploy markers + scan beams (behind the lines)
    var k, ev, x0;
    for (k = 0; k < events.length; k++) {
      ev = events[k];
      x0 = xOfIndex(ev.n);
      if (x0 < PAD.l - 120) continue;

      var beamT = (now - ev.beamAt) / 1100;
      if (beamT >= 0 && beamT < 1) {
        var ease = 1 - Math.pow(1 - beamT, 3);
        var bx = x0 + ease * (W - PAD.r - x0);
        var grad = ctx.createLinearGradient(bx - 90, 0, bx, 0);
        grad.addColorStop(0, rgba(C.solid, 0));
        grad.addColorStop(1, rgba(C.solid, 0.22 * (1 - beamT)));
        ctx.fillStyle = grad;
        ctx.fillRect(bx - 90, PAD.t - 8, 90, H - PAD.t - PAD.b + 8);
        ctx.fillStyle = rgba(C.accent, 0.8 * (1 - beamT));
        ctx.fillRect(bx, PAD.t - 8, 1.5, H - PAD.t - PAD.b + 8);
      }

      var rolled = n >= ev.rollbackN;
      ctx.strokeStyle = rgba(C.accent, 0.55);
      ctx.lineWidth = 1;
      ctx.setLineDash([4, 4]);
      ctx.lineDashOffset = -((now / 60) % 8);
      ctx.beginPath();
      ctx.moveTo(Math.round(x0) + 0.5, PAD.t - 6);
      ctx.lineTo(Math.round(x0) + 0.5, H - PAD.b);
      ctx.stroke();
      ctx.setLineDash([]);
      ctx.lineDashOffset = 0;

      // marker chip
      var label = ev.label;
      var tw = ctx.measureText(label).width;
      var cw = tw + 22, cx = x0 - 6, cy = 12;
      ctx.fillStyle = rgba(C.card, 0.96);
      roundRect(cx, cy, cw, 20, 10);
      ctx.fill();
      ctx.strokeStyle = rgba(C.accent, 0.5);
      ctx.stroke();
      ctx.fillStyle = rgba(C.accent, 1);
      ctx.beginPath();
      ctx.arc(cx + 9, cy + 10, 2.6, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = rgba(C.fg, 0.92);
      ctx.textAlign = "left";
      ctx.fillText(label, cx + 16, cy + 10.5);

      if (rolled) {
        var rx = xOfIndex(ev.rollbackN);
        ctx.strokeStyle = rgba(C.icon, 0.45);
        ctx.setLineDash([2, 5]);
        ctx.beginPath();
        ctx.moveTo(Math.round(rx) + 0.5, PAD.t + 6);
        ctx.lineTo(Math.round(rx) + 0.5, H - PAD.b);
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.fillStyle = rgba(C.muted, 0.9);
        ctx.fillText("rollback", rx + 6, H - PAD.b - 8);
      }
    }

    // which series is currently "hot" (an unrolled, detected regression)
    var hot = {};
    for (k = 0; k < events.length; k++) {
      ev = events[k];
      if (ev.detected && n < ev.rollbackN) hot[ev.culprit] = true;
    }

    // series lines: every query in the calm tone; the stretch between a
    // detected deploy and its rollback is repainted in the accent on top
    var i;
    var dimAll = hover.on;
    for (i = 0; i < SERIES.length; i++) {
      var isSel = selected === i;
      var alpha = selected >= 0 && !isSel ? 0.28 : 0.62 + (isSel ? 0.3 : 0);
      if (!plotLine(i)) continue;
      ctx.strokeStyle = rgba(C.icon, dimAll ? alpha * 0.85 : alpha);
      ctx.lineWidth = isSel ? 2 : 1.5;
      ctx.lineJoin = "round";
      ctx.stroke();
    }
    for (k = 0; k < events.length; k++) {
      ev = events[k];
      if (!ev.detected) continue;
      var from = ev.n - 2;
      var to = Math.min(n, ev.rollbackN + 2);
      if (!plotLine(ev.culprit, from, to)) continue;
      var sx = Math.max(PAD.l, xOfIndex(from));
      var ex = xOfIndex(to);
      ctx.save();
      ctx.lineTo(ex, H - PAD.b);
      ctx.lineTo(sx, H - PAD.b);
      ctx.closePath();
      var ag = ctx.createLinearGradient(0, PAD.t, 0, H - PAD.b);
      ag.addColorStop(0, rgba(C.solid, 0.22));
      ag.addColorStop(1, rgba(C.solid, 0));
      ctx.fillStyle = ag;
      ctx.fill();
      ctx.restore();
      plotLine(ev.culprit, from, to);
      ctx.shadowColor = rgba(C.solid, 0.7);
      ctx.shadowBlur = 12;
      ctx.strokeStyle = rgba(C.accent, 1);
      ctx.lineWidth = 2.4;
      ctx.lineJoin = "round";
      ctx.stroke();
      ctx.shadowBlur = 0;
    }

    // head dots (the newest sample), pulsing
    if (!hover.on) {
      for (i = 0; i < SERIES.length; i++) {
        var hv = state[i].hist[state[i].hist.length - 1];
        var hx = W - PAD.r, hy = yOf(hv);
        var hotI = !!hot[i];
        var pulse = (now / (hotI ? 700 : 1400) + i * 0.25) % 1;
        ctx.strokeStyle = rgba(hotI ? C.accent : C.icon, (1 - pulse) * (hotI ? 0.7 : 0.35));
        ctx.lineWidth = 1.2;
        ctx.beginPath();
        ctx.arc(hx, hy, 3 + pulse * (hotI ? 13 : 8), 0, Math.PI * 2);
        ctx.stroke();
        ctx.fillStyle = rgba(hotI ? C.accent : C.fg, hotI ? 1 : 0.85);
        ctx.beginPath();
        ctx.arc(hx, hy, hotI ? 3.6 : 2.6, 0, Math.PI * 2);
        ctx.fill();
      }
    }

    // detection: rings on the change point + a tag that rides along
    for (k = 0; k < events.length; k++) {
      ev = events[k];
      if (!ev.detected) continue;
      var cpx = xOfIndex(ev.n + 3);
      if (cpx < PAD.l + 20) continue;
      var hist = state[ev.culprit].hist;
      var hi = hist.length - 1 - (n - (ev.n + 8));
      var cpv = hist[Math.max(0, Math.min(hist.length - 1, hi))];
      var cpy = yOf(cpv);
      var age = (now - ev.pingAt) / 1000;
      for (var r = 0; r < 3; r++) {
        var p = age - r * 0.28;
        if (p < 0 || p > 1.6) continue;
        var q = p / 1.6;
        ctx.strokeStyle = rgba(C.accent, (1 - q) * 0.85);
        ctx.lineWidth = 1.5;
        ctx.beginPath();
        ctx.arc(cpx, cpy, 6 + q * 34, 0, Math.PI * 2);
        ctx.stroke();
      }
      ctx.fillStyle = rgba(C.accent, 1);
      ctx.beginPath();
      ctx.arc(cpx, cpy, 4, 0, Math.PI * 2);
      ctx.fill();
      ctx.strokeStyle = rgba(C.panel, 1);
      ctx.lineWidth = 2;
      ctx.stroke();

      // tag, fading in, then fading as it nears the left edge
      var tagA = Math.min(1, age / 0.35) * Math.min(1, (cpx - PAD.l - 20) / 90);
      if (tagA > 0.02) {
        var ty = Math.max(PAD.t + 6, cpy - 54);
        var txt = SERIES[ev.culprit].name + "  ×" + ev.mult.toFixed(1);
        var sub = "confidence " + ev.conf.toFixed(2);
        ctx.font = "600 11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
        var w1 = ctx.measureText(txt).width;
        ctx.font = "10px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
        var w2 = ctx.measureText(sub).width;
        var bw = Math.max(w1, w2) + 22, bh = 38;
        var bx2 = cpx + 14;
        if (bx2 + bw > W - 6) bx2 = cpx - 14 - bw;
        ctx.globalAlpha = tagA;
        ctx.strokeStyle = rgba(C.accent, 0.6);
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(cpx, cpy);
        ctx.lineTo(bx2 < cpx ? bx2 + bw : bx2, ty + bh / 2);
        ctx.stroke();
        ctx.fillStyle = rgba(C.card, 0.97);
        roundRect(bx2, ty, bw, bh, 9);
        ctx.fill();
        ctx.strokeStyle = rgba(C.accent, 0.7);
        ctx.stroke();
        ctx.fillStyle = rgba(C.accent, 1);
        ctx.font = "600 11px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
        ctx.textAlign = "left";
        ctx.fillText(txt, bx2 + 11, ty + 13);
        ctx.fillStyle = rgba(C.muted, 1);
        ctx.font = "10px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
        ctx.fillText(sub, bx2 + 11, ty + 27);
        ctx.globalAlpha = 1;
      }
    }

    // hover crosshair
    if (hover.on) {
      var hx2 = Math.max(PAD.l, Math.min(W - PAD.r, hover.x));
      var back = Math.round((W - PAD.r - hx2) / STEP);
      ctx.strokeStyle = rgba(C.fg, 0.35);
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(Math.round(hx2) + 0.5, PAD.t - 8);
      ctx.lineTo(Math.round(hx2) + 0.5, H - PAD.b);
      ctx.stroke();
      for (i = 0; i < SERIES.length; i++) {
        var h = state[i].hist;
        var v2 = h[Math.max(0, h.length - 1 - back)];
        var py = yOf(v2);
        ctx.fillStyle = rgba(hot[i] ? C.accent : C.fg, 1);
        ctx.beginPath();
        ctx.arc(W - PAD.r - back * STEP, py, hot[i] ? 4.5 : 3.5, 0, Math.PI * 2);
        ctx.fill();
        ctx.strokeStyle = rgba(C.panel, 1);
        ctx.lineWidth = 1.5;
        ctx.stroke();
      }
    }

    ctx.restore();

    // each query is named at the head of its own line (no legend needed)
    var lab = [];
    for (i = 0; i < SERIES.length; i++) {
      var lh = state[i].hist;
      var lvv = lh[lh.length - 1];
      lab.push({ i: i, y: yOf(lvv), v: lvv });
    }
    lab.sort(function (a, b) { return a.y - b.y; });
    for (k = 1; k < lab.length; k++) {
      if (lab[k].y - lab[k - 1].y < 14) lab[k].y = lab[k - 1].y + 14;
    }
    var lx = W - PAD.r + 14;
    ctx.textAlign = "left";
    for (k = 0; k < lab.length; k++) {
      var L = lab[k];
      var isH = !!hot[L.i];
      var ly = Math.min(L.y, H - PAD.b);
      var vtxt = L.v < 10 ? L.v.toFixed(1) : String(Math.round(L.v));
      ctx.font = (isH ? "600 " : "") + "10px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";
      ctx.fillStyle = isH ? rgba(C.accent, 1) : rgba(C.muted, 0.95);
      ctx.fillText(SERIES[L.i].name, lx, ly);
      var nw = ctx.measureText(SERIES[L.i].name + " ").width;
      ctx.fillStyle = isH ? rgba(C.accent, 1) : rgba(C.fg, 0.9);
      ctx.fillText(vtxt, lx + nw, ly);
    }
    ctx.font = "10px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";

    // time axis (relative)
    ctx.fillStyle = rgba(C.muted, 0.7);
    ctx.textAlign = "right";
    ctx.fillText("now", W - PAD.r, H - 11);
    ctx.textAlign = "left";
    ctx.fillText("−" + Math.round((W - PAD.l - PAD.r) / STEP / RATE) + " s", PAD.l, H - 11);

    drawMini(now);
  }

  // ---- tooltip --------------------------------------------------------------------
  function updateTip() {
    if (!tip) return;
    if (!hover.on) { tip.hidden = true; return; }
    var hx = Math.max(PAD.l, Math.min(W - PAD.r, hover.x));
    var back = Math.round((W - PAD.r - hx) / STEP);
    var idx = n - back;
    var rows = "";
    for (var i = 0; i < SERIES.length; i++) {
      var h = state[i].hist;
      var v = h[Math.max(0, h.length - 1 - back)];
      var base = SERIES[i].base;
      var factor = v / base;
      var hotRow = factor > 2.2;
      rows += '<div class="pgrr-tip-row' + (hotRow ? " is-hot" : "") + '"><span>' +
        SERIES[i].name + "</span><b>" + (v < 10 ? v.toFixed(1) : Math.round(v)) +
        " ms</b>" + (hotRow ? "<em>×" + factor.toFixed(1) + "</em>" : "") + "</div>";
    }
    var near = "";
    for (var k = 0; k < events.length; k++) {
      var ev = events[k];
      if (idx >= ev.n && idx < ev.rollbackN) near = ev.label;
    }
    var secs = (back / RATE).toFixed(1);
    tip.innerHTML = '<div class="pgrr-tip-head">' + (back === 0 ? "now" : "−" + secs + " s") +
      (near ? "<span>after " + near + "</span>" : "") + "</div>" + rows;
    tip.hidden = false;
    var tw = tip.offsetWidth;
    var left = hx + 16;
    if (left + tw > W - 6) left = hx - 16 - tw;
    tip.style.left = Math.max(6, left) + "px";
    tip.style.top = "56px";
  }

  // ---- legend -----------------------------------------------------------------------
  var chips = [];
  function buildLegend() {
    if (!legendEl) return;
    legendEl.innerHTML = "";
    SERIES.forEach(function (s, i) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "pgrr-q";
      b.setAttribute("aria-pressed", "false");
      b.title = s.sql + "  (queryid " + s.id + ") — click to aim the next deploy at this query";
      b.innerHTML = '<i class="pgrr-q-sw"></i><span>' + s.name + '</span><code>–</code>';
      b.addEventListener("click", function () {
        selected = selected === i ? -1 : i;
        syncChips();
        if (reduce) draw();
      });
      legendEl.appendChild(b);
      chips.push(b);
    });
  }

  function syncChips() {
    chips.forEach(function (b, i) {
      b.setAttribute("aria-pressed", selected === i ? "true" : "false");
      b.classList.toggle("is-selected", selected === i);
      b.classList.toggle("is-muted", selected >= 0 && selected !== i);
    });
  }

  var lastChipUpdate = 0;
  function updateChips(now) {
    if (now - lastChipUpdate < 120) return;
    lastChipUpdate = now;
    var hot = {};
    for (var k = 0; k < events.length; k++) {
      var ev = events[k];
      if (ev.detected && n < ev.rollbackN) hot[ev.culprit] = true;
    }
    chips.forEach(function (b, i) {
      var h = state[i].hist;
      var v = h[h.length - 1];
      b.lastChild.textContent = (v < 10 ? v.toFixed(1) : Math.round(v)) + " ms";
      b.classList.toggle("is-hot", !!hot[i]);
    });
  }

  function announce(ev) {
    var msg = "Regression detected: " + SERIES[ev.culprit].name + " ×" + ev.mult.toFixed(1) +
      " after " + ev.label;
    if (statusEl) {
      statusEl.textContent = "Regression detected";
      statusEl.classList.add("is-alert");
      clearTimeout(announce.t);
      announce.t = setTimeout(function () {
        statusEl.classList.remove("is-alert");
        statusEl.textContent = "Live · 4 queries";
      }, 5200);
    }
    if (liveEl) liveEl.textContent = msg;
  }

  // ---- mini radar --------------------------------------------------------------------
  var blips = [];
  function blip() {
    blips.push({ a: Math.random() * Math.PI * 2, r: 0.35 + Math.random() * 0.5, t: performance.now() });
  }

  function drawMini(now) {
    if (!mctx) return;
    var s = 44, c = s / 2, R = 19;
    mctx.clearRect(0, 0, s, s);
    mctx.lineWidth = 1;
    for (var r = 1; r <= 3; r++) {
      mctx.strokeStyle = rgba(C.border, r === 3 ? 1 : 0.7);
      mctx.beginPath();
      mctx.arc(c, c, (R * r) / 3, 0, Math.PI * 2);
      mctx.stroke();
    }
    mctx.strokeStyle = rgba(C.border, 0.7);
    mctx.beginPath();
    mctx.moveTo(c - R, c); mctx.lineTo(c + R, c);
    mctx.moveTo(c, c - R); mctx.lineTo(c, c + R);
    mctx.stroke();
    var ang = (now / 1800) % (Math.PI * 2);
    if (reduce) ang = -Math.PI / 4;
    // sweep: a fading wedge behind the leading line
    var steps = 18;
    for (var i = 0; i < steps; i++) {
      var a0 = ang - ((i + 1) / steps) * 1.1;
      var a1 = ang - (i / steps) * 1.1;
      mctx.fillStyle = rgba(C.solid, 0.34 * (1 - i / steps));
      mctx.beginPath();
      mctx.moveTo(c, c);
      mctx.arc(c, c, R, a0, a1);
      mctx.closePath();
      mctx.fill();
    }
    mctx.strokeStyle = rgba(C.accent, 1);
    mctx.lineWidth = 1.4;
    mctx.beginPath();
    mctx.moveTo(c, c);
    mctx.lineTo(c + Math.cos(ang) * R, c + Math.sin(ang) * R);
    mctx.stroke();
    for (var b = blips.length - 1; b >= 0; b--) {
      var age = (now - blips[b].t) / 1000;
      if (age > 6) { blips.splice(b, 1); continue; }
      var bx = c + Math.cos(blips[b].a) * R * blips[b].r;
      var by = c + Math.sin(blips[b].a) * R * blips[b].r;
      var fade = Math.max(0, 1 - age / 6);
      mctx.fillStyle = rgba(C.accent, 0.25 + 0.75 * fade);
      mctx.beginPath();
      mctx.arc(bx, by, 2.2, 0, Math.PI * 2);
      mctx.fill();
      if (age < 1.2) {
        mctx.strokeStyle = rgba(C.accent, (1 - age / 1.2) * 0.8);
        mctx.lineWidth = 1;
        mctx.beginPath();
        mctx.arc(bx, by, 2 + age * 8, 0, Math.PI * 2);
        mctx.stroke();
      }
    }
    mctx.fillStyle = rgba(C.fg, 0.9);
    mctx.beginPath();
    mctx.arc(c, c, 1.6, 0, Math.PI * 2);
    mctx.fill();
  }

  // ---- loop ---------------------------------------------------------------------------
  var running = false, last = 0, visible = true;

  function frame(ts) {
    if (!running) return;
    var dt = Math.min(0.1, (ts - last) / 1000);
    last = ts;
    if (!hover.on) advance(dt);
    draw();
    updateChips(ts);
    updateTip();
    requestAnimationFrame(frame);
  }

  function start() {
    if (reduce || running || !visible || document.hidden) return;
    running = true;
    last = performance.now();
    requestAnimationFrame(frame);
  }

  function stop() { running = false; }

  // ---- input --------------------------------------------------------------------------------
  function pointerTo(e) {
    var r = canvas.getBoundingClientRect();
    hover.x = e.clientX - r.left;
    hover.y = e.clientY - r.top;
  }

  stage.addEventListener("pointerenter", function (e) {
    hover.on = true;
    pointerTo(e);
    if (reduce) { draw(); updateTip(); }
  });
  stage.addEventListener("pointermove", function (e) {
    hover.on = true;
    pointerTo(e);
    if (reduce) { draw(); updateTip(); }
  });
  stage.addEventListener("pointerleave", function () {
    hover.on = false;
    if (tip) tip.hidden = true;
    if (reduce) draw();
  });

  if (btn) {
    btn.addEventListener("click", function () {
      injectDeploy();
      if (reduce) {
        advance(DETECT_AFTER + 1);
        draw();
      }
      btn.classList.remove("is-pressed");
      void btn.offsetWidth;
      btn.classList.add("is-pressed");
    });
  }

  // ---- boot ---------------------------------------------------------------------------------
  buildLegend();
  readColors();

  // history to fill the chart, then a few seconds with a deploy in them so the
  // first paint already shows a marker and a detected regression
  var pre = Math.ceil(1100 / STEP) + 8;
  for (var p = 0; p < pre; p++) pushSample();
  for (var q = 0; q < 8 * RATE; q++) {
    autoIn -= DT;
    if (autoIn <= 0) injectDeploy();
    pushSample();
  }
  blips.length = 0;
  for (var e = 0; e < events.length; e++) {
    if (events[e].detected) { events[e].pingAt = performance.now(); }
    events[e].beamAt = performance.now() - 5000;
  }
  blip();

  root.setAttribute("data-ready", "");
  resize();
  updateChips(1e9);
  if (statusEl) {
    clearTimeout(announce.t);
    statusEl.classList.remove("is-alert");
    statusEl.textContent = "Live · 4 queries";
  }

  if ("ResizeObserver" in window) {
    new ResizeObserver(resize).observe(stage);
  } else {
    window.addEventListener("resize", resize);
  }

  new MutationObserver(function () { readColors(); draw(); }).observe(document.body, {
    attributes: true,
    attributeFilter: ["data-md-color-scheme"]
  });

  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (entries) {
      visible = entries[0].isIntersecting;
      if (visible) start(); else stop();
    }, { threshold: 0.05 }).observe(root);
  }
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) stop(); else start();
  });

  start();
})();
