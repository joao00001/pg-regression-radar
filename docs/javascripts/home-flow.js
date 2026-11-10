/*
 * Home page, "How it works": a soft beam of light travels along the
 * architecture diagram's edges in the order the data really flows
 * (sources -> collector/ingester -> correlation engine -> alerting/status),
 * and each node lights up briefly as the pulse reaches it. Every second loop
 * the engine is triggered by the rolling schedule instead, since it can run
 * without a deploy.
 *
 * The diagram stays fully readable without this script; with
 * prefers-reduced-motion it stays static. Animation pauses while the diagram
 * is off screen or the tab is hidden. Purely decorative (aria-hidden).
 */
(function () {
  "use strict";

  var host = document.querySelector(".pgrr-diagram");
  var svg = host && host.querySelector("svg");
  if (!svg) return;
  if (window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches) return;

  var NS = "http://www.w3.org/2000/svg";
  var edges = svg.querySelectorAll(".pgrr-edge");
  var nodes = svg.querySelectorAll(".pgrr-node");
  if (edges.length < 7 || nodes.length < 8) return;

  // edges: 0 postgres>collector, 1 deploy>ingester, 2 collector>engine, 3 ingester>engine,
  //        4 schedule>engine, 5 engine>alerting, 6 engine>status
  // nodes: 0 postgres, 1 deploy, 2 collector, 3 ingester, 4 schedule, 5 engine, 6 alerting, 7 status
  var CYCLE = 7.2;
  var main = [
    { e: [0, 1], from: 0.0, to: 0.9 },
    { e: [2, 3], from: 1.0, to: 2.0 },
    { e: [5, 6], from: 2.45, to: 3.35 }
  ];
  var alt = [
    { e: [4], from: 0.9, to: 2.0 },
    { e: [5, 6], from: 2.45, to: 3.35 }
  ];
  var hitsMain = [[0.0, [0, 1]], [0.9, [2, 3]], [2.0, [5], true], [3.35, [6, 7]]];
  var hitsAlt = [[0.9, [4]], [2.0, [5], true], [3.35, [6, 7]]];

  var defs = svg.querySelector("defs");
  var anchor = edges[edges.length - 1];
  var icons = svg.querySelectorAll(".pgrr-ic");
  var NODE_ICON = { 0: 0, 1: 1, 2: 2, 3: 3, 5: 4, 6: 5, 7: 6 };

  // Each beam is a soft gradient (transparent -> light -> transparent) used as the
  // stroke of its edge; moving the gradient's two end points moves the light.
  var beams = [];
  for (var i = 0; i < edges.length; i++) {
    var path = edges[i];
    var len = path.getTotalLength();
    var a0 = path.getPointAtLength(0), a1 = path.getPointAtLength(len);
    var accent = path.classList.contains("pgrr-edge--accent");
    var grad = document.createElementNS(NS, "linearGradient");
    grad.setAttribute("id", "pgrr-beam-" + i);
    grad.setAttribute("gradientUnits", "userSpaceOnUse");
    var stops = [[0, 0], [0.7, 1], [1, 0]];
    stops.forEach(function (st) {
      var el = document.createElementNS(NS, "stop");
      el.setAttribute("offset", st[0]);
      el.setAttribute("class", "pgrr-beam-stop" + (accent ? " pgrr-beam-stop--accent" : ""));
      el.setAttribute("stop-opacity", st[1]);
      grad.appendChild(el);
    });
    defs.appendChild(grad);
    var glow = document.createElementNS(NS, "path");
    var core = document.createElementNS(NS, "path");
    [glow, core].forEach(function (el, k) {
      el.setAttribute("d", path.getAttribute("d"));
      el.setAttribute("class", "pgrr-beam " + (k ? "pgrr-beam--core" : "pgrr-beam--glow"));
      el.setAttribute("stroke", "url(#pgrr-beam-" + i + ")");
      el.setAttribute("aria-hidden", "true");
      el.style.opacity = "0";
      anchor.parentNode.insertBefore(el, anchor.nextSibling);
    });
    var dx = a1.x - a0.x, dy = a1.y - a0.y, chord = Math.sqrt(dx * dx + dy * dy) || 1;
    beams.push({ grad: grad, glow: glow, core: core, x: a0.x, y: a0.y, ux: dx / chord, uy: dy / chord, chord: chord, span: Math.max(70, chord * 0.5) });
  }

  function place(b, prog) {
    if (prog <= 0 || prog >= 1) { b.glow.style.opacity = b.core.style.opacity = "0"; return; }
    var head = prog * (b.chord + b.span * 0.3);
    var tail = head - b.span;
    b.grad.setAttribute("x1", b.x + b.ux * tail);
    b.grad.setAttribute("y1", b.y + b.uy * tail);
    b.grad.setAttribute("x2", b.x + b.ux * head);
    b.grad.setAttribute("y2", b.y + b.uy * head);
    // fade the whole beam in and out near the ends so it never pops
    var edge = Math.min(1, prog / 0.12, (1 - prog) / 0.12);
    b.core.style.opacity = String(edge);
    b.glow.style.opacity = String(edge * 0.35);
  }

  function ease(t) { return t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2; }

  function hit(n, strong) {
    var el = nodes[n];
    var ic = icons[NODE_ICON[n]];
    el.classList.remove("pgrr-hit");
    if (ic) ic.classList.remove("pgrr-hit");
    void el.getBoundingClientRect(); // restart the transition
    el.classList.add("pgrr-hit");
    if (ic) ic.classList.add("pgrr-hit");
    setTimeout(function () {
      el.classList.remove("pgrr-hit");
      if (ic) ic.classList.remove("pgrr-hit");
    }, 140);
    if (strong) {
      // a short light runs once around the engine's border while it correlates
      var g = document.createElementNS(NS, "g");
      g.setAttribute("class", "pgrr-bbeam");
      g.setAttribute("aria-hidden", "true");
      [0, 1].forEach(function (k) {
        var r = el.cloneNode(false);
        r.setAttribute("class", "pgrr-bbeam-rect " + (k ? "pgrr-bbeam-rect--core" : "pgrr-bbeam-rect--glow"));
        r.setAttribute("pathLength", "100");
        g.appendChild(r);
      });
      el.parentNode.insertBefore(g, el.nextSibling);
      setTimeout(function () { g.remove(); }, 1900);
    }
  }

  // ---- live data above each card ---------------------------------------------------
  // Real field names from the docs (pg_stat_statements row, DeployEvent,
  // PerformanceRegression); the values are made up and change on every loop.
  var APPS = [["checkout-api", "v42"], ["billing", "v118"], ["search-svc", "v9"], ["orders", "v57"]];
  var QUERIES = [["8675309", 4.2], ["2241907", 2.8], ["5530418", 6.1], ["9017734", 3.4]];
  var scenario = null;

  function rnd(a, b) { return a + Math.random() * (b - a); }
  function pick(list, last) {
    var x;
    do { x = list[Math.floor(Math.random() * list.length)]; } while (x === last && list.length > 1);
    return x;
  }
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function clock(base, plus) {
    var d = new Date(base + plus * 1000);
    return pad(d.getUTCHours()) + ":" + pad(d.getUTCMinutes()) + ":" + pad(d.getUTCSeconds()) + "Z";
  }
  function commas(n) { return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ","); }

  var lastApp = null, lastQuery = null;
  function newScenario() {
    var app = lastApp = pick(APPS, lastApp);
    var q = lastQuery = pick(QUERIES, lastQuery);
    var before = q[1] * rnd(0.85, 1.2);
    var factor = +rnd(2.4, 5.2).toFixed(2);
    var after = before * factor;
    var pv = +rnd(0.0008, 0.012).toFixed(4);
    var now = Date.now();
    var rev = Math.floor(rnd(0x1000000, 0xfffffff)).toString(16);
    scenario = {
      app: app[0], tag: app[1], qid: q[0], before: before.toFixed(1), after: after.toFixed(1), factor: factor.toFixed(2),
      calls: commas(Math.floor(rnd(1800, 9400))), samples: Math.floor(rnd(8, 30)),
      p: pv, conf: Math.min(0.99, 1 - pv).toFixed(2), rev: rev.slice(0, 7),
      src: pick(["argocd", "rollouts", "flux"]), deployAt: clock(now, -150), changeAt: clock(now, -150 + Math.floor(rnd(50, 110)))
    };
  }

  // each entry: [label, value, accent?] pairs per line
  function lines(n, c) {
    switch (n) {
      case 0: return [[["queryid ", c.qid]], [["calls ", c.calls], [" · mean ", c.before + " ms"]]];
      case 1: return [[[c.src + " · ", c.app]], [["rev ", c.rev], [" · ", c.tag]]];
      case 2: return [[["scrape ", "every 60s"]], [[c.samples + " samples · mean ", c.before + " ms"]]];
      case 3: return [[["DeployEvent ", c.deployAt]], [["production · ", "prod-cluster"]]];
      case 4: return [[["every ", "15m"]], [["window ", "60m"], [" · ", "no deploy"]]];
      case 5: return [[["change point ", c.changeAt]], [["p = ", c.p.toFixed(4)], [" · confidence ", c.conf, 1]]];
      case 6: return [[[c.before + " → ", c.after + " ms", 1]], [["× ", c.factor, 1], [" · confidence ", c.conf]]];
      case 7: return [[["status ", "Detected", 1]], [["plan ", "Index Scan → Seq Scan"]]];
    }
  }

  // x (centre, or right edge for the schedule), first line y, anchor
  var SPOT = {
    0: [135, 56], 1: [135, 236], 2: [475, 56], 3: [475, 236],
    4: [662, 27, "end"], 5: [810, 328], 6: [1145, 56], 7: [1145, 236]
  };

  var dataEls = {};
  function buildData() {
    var tail = svg.lastElementChild;
    for (var n in SPOT) {
      var g = document.createElementNS(NS, "g");
      g.setAttribute("class", "pgrr-data");
      g.setAttribute("aria-hidden", "true");
      for (var l = 0; l < 2; l++) {
        var t = document.createElementNS(NS, "text");
        t.setAttribute("class", "pgrr-d pgrr-d" + l + (SPOT[n][2] ? " pgrr-d--end" : ""));
        t.setAttribute("x", SPOT[n][0]);
        t.setAttribute("y", SPOT[n][1] + l * 19);
        g.appendChild(t);
      }
      svg.appendChild(g);
      dataEls[n] = g;
    }
  }

  function fill(g, n) {
    var ls = lines(+n, scenario);
    for (var l = 0; l < 2; l++) {
      var t = g.childNodes[l];
      while (t.firstChild) t.removeChild(t.firstChild);
      ls[l].forEach(function (part) {
        var a = document.createElementNS(NS, "tspan");
        a.textContent = part[0];
        t.appendChild(a);
        var v = document.createElementNS(NS, "tspan");
        v.setAttribute("class", "pgrr-dv" + (part[2] ? " pgrr-dv--a" : ""));
        v.textContent = part[1];
        t.appendChild(v);
      });
    }
  }

  function dataIn(n) {
    var g = dataEls[n];
    if (!g || !scenario) return;
    fill(g, n);
    g.style.transition = "none";
    g.setAttribute("class", "pgrr-data");
    void g.getBoundingClientRect();
    g.style.transition = "";
    g.setAttribute("class", "pgrr-data is-on");
  }
  function dataOut(n) {
    var g = dataEls[n];
    if (g) g.setAttribute("class", "pgrr-data is-out");
  }
  function dataClear() {
    for (var n in dataEls) dataEls[n].setAttribute("class", "pgrr-data");
  }

  // [time, node, "in" | "out"]: data shows as the light leaves/arrives, holds, then lifts away
  var dataMain = [
    [0.05, 0, "in"], [0.05, 1, "in"], [1.75, 0, "out"], [1.75, 1, "out"],
    [0.9, 2, "in"], [0.9, 3, "in"], [2.9, 2, "out"], [2.9, 3, "out"],
    [2.0, 5, "in"], [4.5, 5, "out"],
    [3.35, 6, "in"], [3.35, 7, "in"], [5.6, 6, "out"], [5.6, 7, "out"]
  ];
  var dataAlt = [
    [0.1, 4, "in"], [2.6, 4, "out"],
    [2.0, 5, "in"], [4.5, 5, "out"],
    [3.35, 6, "in"], [3.35, 7, "in"], [5.6, 6, "out"], [5.6, 7, "out"]
  ];

  buildData();

  var running = false, visible = true, t0 = 0, prevT = -1, prevCycle = 0;

  function frame(ts) {
    if (!running) return;
    if (!t0) t0 = ts;
    var el = (ts - t0) / 1000;
    var cycle = Math.floor(el / CYCLE);
    var t = el - cycle * CYCLE;
    var odd = cycle % 2 === 1;
    var steps = odd ? alt : main;
    if (prevT < 0 || cycle !== prevCycle) newScenario();

    for (var q = 0; q < beams.length; q++) place(beams[q], 0);
    for (var s = 0; s < steps.length; s++) {
      var st = steps[s];
      if (t >= st.from && t <= st.to) {
        var prog = ease((t - st.to + (st.to - st.from)) / (st.to - st.from));
        for (var k = 0; k < st.e.length; k++) place(beams[st.e[k]], prog);
      }
    }

    var hits = odd ? hitsAlt : hitsMain;
    for (var h = 0; h < hits.length; h++) {
      var at = hits[h][0];
      var crossed = cycle === prevCycle ? (prevT < at && t >= at) : (at <= t);
      if (crossed) hits[h][1].forEach(function (n) { hit(n, hits[h][2]); });
    }
    var ev = odd ? dataAlt : dataMain;
    for (var d = 0; d < ev.length; d++) {
      var dt = ev[d][0];
      var due = cycle === prevCycle && prevT >= 0 ? (prevT < dt && t >= dt) : (dt <= t);
      if (due) { if (ev[d][2] === "in") dataIn(ev[d][1]); else dataOut(ev[d][1]); }
    }
    prevT = t; prevCycle = cycle;
    requestAnimationFrame(frame);
  }

  function start() {
    if (running || !visible || document.hidden) return;
    running = true;
    t0 = 0; prevT = -1; prevCycle = 0;
    requestAnimationFrame(frame);
  }
  function stop() {
    running = false;
    for (var q = 0; q < beams.length; q++) place(beams[q], 0);
    dataClear();
  }

  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (entries) {
      visible = entries[0].isIntersecting;
      if (visible) start(); else stop();
    }, { threshold: 0.2 }).observe(host);
  }
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) stop(); else start();
  });
  start();
})();
