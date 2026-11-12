/*
 * Home page: the recorded two-terminal demo, played automatically.
 *
 * The clip starts when it scrolls into view, pauses when it leaves, and loops.
 * The only control is a click (or Enter/Space) on the video, which pauses and
 * resumes it; a centered button appears on hover. Pausing also freezes the
 * status line and the step segments, because they follow the video clock. Above it, a status line follows the video clock (what
 * the current step is, and the real elapsed time of the run, since the waits
 * were sped up) and a row of segments fills as the steps go by. The captions
 * are annotations written for this page; the video itself is the raw terminals.
 * With reduced motion requested, nothing autoplays and the native controls stay.
 */
(function () {
  "use strict";

  var root = document.querySelector(".pgrr-demo");
  if (!root) return;
  var video = root.querySelector(".pgrr-demo-video");
  var statusEl = root.querySelector(".pgrr-demo-status");
  var clockEl = root.querySelector(".pgrr-demo-clock");
  var stepsEl = root.querySelector(".pgrr-demo-steps");
  var stage = root.querySelector(".pgrr-demo-stage");
  if (!stage || !video || !statusEl || !clockEl || !stepsEl) return;

  var reduce = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (reduce) return; // keep the poster and the native controls

  // Start of each step, in video seconds, and what it says.
  var STEPS = [
    [0, "Radar starts: it scrapes pg_stat_statements every 2 s"],
    [9.64, "The query is served by an index"],
    [13.36, "Steady traffic: 100 queries per second on the same query"],
    [17.35, "Deploy v1 registered through the webhook: the baseline"],
    [47.85, "Deploy v2 ships and drops the index"],
    [54.79, "Same query, now a sequential scan"],
    [64.99, "Regression detected, linked to deploy v2: about 400x slower"],
    [82.99, "Stopped with Ctrl-C"]
  ];
  var ALERT = 6; // index of the detection step
  // [video seconds, real seconds since the radar started]: the run lasted 3 min 28 s.
  var KNOTS = [[0.0,0],[2.45,0],[2.55,0],[4.68,0],[4.78,0],[7.63,0],[7.64,0.01],[9.64,5.0],[11.94,5.0],[11.96,5.03],[13.36,7.03],[15.35,7.03],[17.35,32.03],[20.2,32.03],[20.21,32.04],[21.85,35.02],[23.85,40.02],[25.85,45.02],[27.85,50.02],[29.85,55.02],[31.85,60.02],[33.85,65.02],[35.85,70.02],[37.85,75.02],[39.85,80.02],[41.85,85.02],[43.85,90.02],[45.85,95.02],[47.85,108.04],[49.45,108.04],[49.48,108.08],[52.33,108.08],[52.34,108.08],[53.72,110.02],[54.79,111.09],[57.09,111.09],[57.12,111.12],[58.99,115.02],[60.99,120.02],[62.99,125.02],[64.99,130.02],[66.99,135.01],[68.99,140.02],[70.99,145.01],[72.99,150.01],[74.99,155.01],[76.99,160.01],[78.99,165.01],[80.99,170.01],[82.99,207.12],[87.49,207.12]];

  video.removeAttribute("controls");
  video.controls = false;
  video.muted = true;
  video.loop = true;
  root.classList.add("is-auto");

  // The stage is the one control: click or Enter/Space pauses and resumes.
  var userPaused = false;
  stage.setAttribute("role", "button");
  stage.setAttribute("tabindex", "0");
  function label() {
    stage.setAttribute("aria-label", userPaused ? "Play video" : "Pause video");
    stage.setAttribute("aria-pressed", userPaused ? "true" : "false");
    root.classList.toggle("is-paused", userPaused);
  }
  function pop() {
    root.classList.remove("is-pop");
    void root.offsetWidth;
    root.classList.add("is-pop");
  }
  function toggle() {
    userPaused = !userPaused;
    label();
    pop();
    if (userPaused) stop(); else if (inView && !document.hidden) start();
  }
  stage.addEventListener("click", toggle);
  stage.addEventListener("keydown", function (e) {
    if (e.key === "Enter" || e.key === " " || e.key === "Spacebar") {
      e.preventDefault();
      toggle();
    }
  });
  label();

  var segs = [];
  for (var i = 0; i < STEPS.length; i++) {
    var s = document.createElement("span");
    s.className = "pgrr-demo-seg" + (i === ALERT ? " is-alert" : "");
    stepsEl.appendChild(s);
    segs.push(s);
  }

  function real(t) {
    for (var k = 1; k < KNOTS.length; k++) {
      if (t <= KNOTS[k][0]) {
        var a = KNOTS[k - 1], b = KNOTS[k];
        return a[1] + (b[1] - a[1]) * ((t - a[0]) / Math.max(1e-6, b[0] - a[0]));
      }
    }
    return KNOTS[KNOTS.length - 1][1];
  }
  function mmss(sec) {
    sec = Math.max(0, Math.floor(sec));
    return ("0" + Math.floor(sec / 60)).slice(-2) + ":" + ("0" + (sec % 60)).slice(-2);
  }

  var cur = -1;
  function paint() {
    var t = video.currentTime || 0;
    var dur = video.duration || KNOTS[KNOTS.length - 1][0];
    var idx = 0;
    for (var i = 0; i < STEPS.length; i++) if (t >= STEPS[i][0]) idx = i;
    if (idx !== cur) {
      cur = idx;
      statusEl.textContent = "Step " + (idx + 1) + " of " + STEPS.length + " \u00b7 " + STEPS[idx][1];
      statusEl.classList.toggle("is-alert", idx === ALERT);
      statusEl.classList.remove("is-swap");
      void statusEl.offsetWidth;
      statusEl.classList.add("is-swap");
    }
    for (var j = 0; j < segs.length; j++) {
      var from = STEPS[j][0], to = j + 1 < STEPS.length ? STEPS[j + 1][0] : dur;
      var p = t <= from ? 0 : t >= to ? 1 : (t - from) / (to - from);
      segs[j].style.setProperty("--p", p.toFixed(3));
    }
    var speed = real(Math.min(dur, t + 0.5)) - real(Math.max(0, t - 0.5));
    clockEl.textContent = "Real " + mmss(real(t)) + (speed > 1.6 ? " \u00b7 sped up" : "");
  }

  var inView = false;
  var raf = 0;
  function loop() {
    paint();
    raf = requestAnimationFrame(loop);
  }
  function start() {
    var p = video.play();
    if (p && p.catch) p.catch(function () {});
    if (!raf) raf = requestAnimationFrame(loop);
  }
  function stop() {
    video.pause();
    if (raf) { cancelAnimationFrame(raf); raf = 0; }
  }

  paint();
  if ("IntersectionObserver" in window) {
    new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        inView = e.isIntersecting;
        if (inView && !document.hidden && !userPaused) start(); else stop();
      });
    }, { threshold: 0.4 }).observe(root);
  } else {
    inView = true;
    if (!userPaused) start();
  }
  document.addEventListener("visibilitychange", function () {
    if (document.hidden) stop(); else if (inView && !userPaused) start();
  });
})();
