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

  var running = false, visible = true, t0 = 0, prevT = -1, prevCycle = 0;

  function frame(ts) {
    if (!running) return;
    if (!t0) t0 = ts;
    var el = (ts - t0) / 1000;
    var cycle = Math.floor(el / CYCLE);
    var t = el - cycle * CYCLE;
    var odd = cycle % 2 === 1;
    var steps = odd ? alt : main;

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
