/*
 * Home page, "How it works": a small pulse of light travels along the
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

  var anchor = edges[edges.length - 1];
  var pulses = [];
  for (var i = 0; i < edges.length; i++) {
    var len = edges[i].getTotalLength();
    var accent = edges[i].classList.contains("pgrr-edge--accent");
    var tail = document.createElementNS(NS, "path");
    var head = document.createElementNS(NS, "path");
    [tail, head].forEach(function (p, k) {
      p.setAttribute("d", edges[i].getAttribute("d"));
      p.setAttribute("class", "pgrr-pulse " + (k ? "pgrr-pulse--head" : "pgrr-pulse--tail") + (accent ? " pgrr-pulse--accent" : ""));
      p.setAttribute("aria-hidden", "true");
      p.style.strokeDasharray = "0 " + (len + 200);
    });
    anchor.parentNode.insertBefore(tail, anchor.nextSibling);
    anchor.parentNode.insertBefore(head, tail.nextSibling);
    pulses.push({ tail: tail, head: head, len: len });
  }

  function place(p, prog) {
    // a dash of length `d` whose leading edge sits at prog * (len + d)
    function set(el, d) {
      if (prog <= 0 || prog >= 1) { el.style.strokeDasharray = "0 " + (p.len + 200); return; }
      var h = prog * (p.len + d);
      el.style.strokeDasharray = d + " " + (p.len + 200);
      el.style.strokeDashoffset = String(d - h);
    }
    set(p.tail, 70);
    set(p.head, 16);
  }

  function ease(t) { return t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2; }

  function hit(n, strong) {
    var el = nodes[n];
    el.classList.remove("pgrr-hit");
    void el.getBoundingClientRect(); // restart the transition
    el.classList.add("pgrr-hit");
    setTimeout(function () { el.classList.remove("pgrr-hit"); }, 120);
    if (strong) {
      var ring = el.cloneNode(false);
      ring.setAttribute("class", "pgrr-ring");
      ring.setAttribute("aria-hidden", "true");
      el.parentNode.insertBefore(ring, el.nextSibling);
      setTimeout(function () { ring.remove(); }, 1300);
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

    for (var q = 0; q < pulses.length; q++) place(pulses[q], 0);
    for (var s = 0; s < steps.length; s++) {
      var st = steps[s];
      if (t >= st.from && t <= st.to) {
        var prog = ease((t - st.to + (st.to - st.from)) / (st.to - st.from));
        for (var k = 0; k < st.e.length; k++) place(pulses[st.e[k]], prog);
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
    for (var q = 0; q < pulses.length; q++) place(pulses[q], 0);
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
