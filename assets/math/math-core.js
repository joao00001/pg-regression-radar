/*
 * "Our math": the statistics behind a PerformanceRegression, in plain
 * JavaScript with no DOM, so the page's widgets and a Node test run the same
 * code.
 *
 * Every function here is a port of the one of the same name in
 * internal/correlation/engine.go (EDivisive, bestChangePoint, welchTTest,
 * tDistPValue, incompleteBeta, betaCF, changePointMinSegLen,
 * computeChangeStats, evaluateQuery). Keep them in step: the page says "the
 * same arithmetic as the engine", and internal/correlation/mathpage_test.go compares both
 * on fixed series (see testdata/mathpage_fixtures.json).
 */
(function (root) {
  "use strict";

  // ---- random numbers (seeded, so a reader can replay an experiment) --------
  function mulberry32(seed) {
    var a = seed >>> 0;
    return function () {
      a = (a + 0x6d2b79f5) >>> 0;
      var t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  // Standard normal by Box-Muller.
  function gauss(rng) {
    var u = 0;
    while (u === 0) u = rng();
    return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * rng());
  }

  // ---- descriptive statistics -------------------------------------------------
  function mean(v) {
    if (!v.length) return 0;
    var s = 0;
    for (var i = 0; i < v.length; i++) s += v[i];
    return s / v.length;
  }

  // Sample variance (divides by n-1), like the engine's `variance`.
  function variance(v) {
    if (v.length < 2) return 0;
    var m = mean(v), s = 0;
    for (var i = 0; i < v.length; i++) {
      var d = v[i] - m;
      s += d * d;
    }
    return s / (v.length - 1);
  }

  // ---- Welch's t-test ------------------------------------------------------------
  // ln Gamma by the Lanczos approximation (g = 7, n = 9); the engine uses
  // math.Lgamma.
  var LANCZOS = [
    0.99999999999980993, 676.5203681218851, -1259.1392167224028,
    771.32342877765313, -176.61502916214059, 12.507343278686905,
    -0.13857109526572012, 9.9843695780195716e-6, 1.5056327351493116e-7
  ];

  function lgamma(x) {
    if (x < 0.5) return Math.log(Math.PI / Math.abs(Math.sin(Math.PI * x))) - lgamma(1 - x);
    x -= 1;
    var a = LANCZOS[0], t = x + 7.5;
    for (var i = 1; i < 9; i++) a += LANCZOS[i] / (x + i);
    return 0.5 * Math.log(2 * Math.PI) + (x + 0.5) * Math.log(t) - t + Math.log(a);
  }

  function logBeta(a, b) {
    return lgamma(a) + lgamma(b) - lgamma(a + b);
  }

  // Continued fraction of the incomplete beta function (Lentz's method).
  function betaCF(x, a, b) {
    var MAXIT = 200, EPS = 3e-7, FPMIN = 1e-30;
    var qab = a + b, qap = a + 1, qam = a - 1;
    var c = 1, d = 1 - (qab * x) / qap;
    if (Math.abs(d) < FPMIN) d = FPMIN;
    d = 1 / d;
    var h = d;
    for (var m = 1; m <= MAXIT; m++) {
      var mf = m;
      var aa = (mf * (b - mf) * x) / ((qam + 2 * mf) * (a + 2 * mf));
      d = 1 + aa * d;
      if (Math.abs(d) < FPMIN) d = FPMIN;
      c = 1 + aa / c;
      if (Math.abs(c) < FPMIN) c = FPMIN;
      d = 1 / d;
      h *= d * c;
      aa = (-(a + mf) * (qab + mf) * x) / ((a + 2 * mf) * (qap + 2 * mf));
      d = 1 + aa * d;
      if (Math.abs(d) < FPMIN) d = FPMIN;
      c = 1 + aa / c;
      if (Math.abs(c) < FPMIN) c = FPMIN;
      d = 1 / d;
      var delta = d * c;
      h *= delta;
      if (Math.abs(delta - 1) < EPS) break;
    }
    return h;
  }

  // Regularised incomplete beta function I_x(a, b).
  function incompleteBeta(x, a, b) {
    if (x < 0 || x > 1) return 0;
    if (x === 0) return 0;
    if (x === 1) return 1;
    var lbeta = logBeta(a, b);
    var front = Math.exp(Math.log(x) * a + Math.log(1 - x) * b - lbeta) / a;
    if (x > (a + 1) / (a + b + 2)) return 1 - incompleteBeta(1 - x, b, a);
    return front * betaCF(x, a, b);
  }

  // Two-tailed p-value of |t| on df degrees of freedom: I_{df/(df+t^2)}(df/2, 1/2).
  function tDistPValue(absT, df) {
    if (df <= 0) return 1;
    var x = df / (df + absT * absT);
    return incompleteBeta(x, df / 2, 0.5);
  }

  // Student's t density, for drawing the curve.
  function tPdf(t, df) {
    var lc = lgamma((df + 1) / 2) - lgamma(df / 2) - 0.5 * Math.log(df * Math.PI);
    return Math.exp(lc - ((df + 1) / 2) * Math.log(1 + (t * t) / df));
  }

  // Welch's t-test: t, Welch-Satterthwaite degrees of freedom, two-tailed p.
  // a is the segment before the change point, b the one after (like the engine).
  function welch(a, b) {
    var n1 = a.length, n2 = b.length;
    if (n1 < 2 || n2 < 2) return { t: 0, df: 0, p: 1, se: 0, m1: mean(a), m2: mean(b), v1: 0, v2: 0, n1: n1, n2: n2 };
    var v1 = variance(a), v2 = variance(b), m1 = mean(a), m2 = mean(b);
    var se = Math.sqrt(v1 / n1 + v2 / n2);
    if (se === 0) {
      return { t: m1 !== m2 ? Infinity : 0, df: n1 + n2 - 2, p: m1 !== m2 ? 0 : 1, se: 0, m1: m1, m2: m2, v1: v1, v2: v2, n1: n1, n2: n2 };
    }
    var t = (m1 - m2) / se;
    var w1 = v1 / n1, w2 = v2 / n2;
    var df = ((w1 + w2) * (w1 + w2)) / ((w1 * w1) / (n1 - 1) + (w2 * w2) / (n2 - 1));
    return { t: t, df: df, p: tDistPValue(Math.abs(t), df), se: se, m1: m1, m2: m2, v1: v1, v2: v2, n1: n1, n2: n2 };
  }

  // ---- E-divisive means ------------------------------------------------------------
  // Minimum segment length: n/10, at least 3 (changePointMinSegLen).
  function minSegLen(n) {
    var m = Math.floor(n / 10);
    return m < 3 ? 3 : m;
  }

  // The energy statistic at every admissible split k (series[:k] vs series[k:]),
  // computed with the engine's incremental sums (O(n^2) in total):
  //   Q(k) = (2 na nb / (na + nb)) * (mean|a-b| - mean|a-a'|/2 - mean|b-b'|/2)
  // where the within-segment means run over unordered pairs. Returns
  // [{k, q}], or [] when the series is shorter than 2*m.
  function energyCurve(series, m) {
    var n = series.length, out = [];
    if (n < 2 * m) return out;
    var i, j;
    var sumAB = 0, sumAA = 0, sumBB = 0;
    for (i = 0; i < m; i++) for (j = m; j < n; j++) sumAB += Math.abs(series[i] - series[j]);
    for (i = 0; i < m; i++) for (j = i + 1; j < m; j++) sumAA += Math.abs(series[i] - series[j]);
    for (i = m; i < n; i++) for (j = i + 1; j < n; j++) sumBB += Math.abs(series[i] - series[j]);

    function q(k) {
      var na = k, nb = n - k;
      var ab = sumAB / (na * nb);
      var aa = na < 2 ? 0 : (2 * sumAA) / (na * (na - 1));
      var bb = nb < 2 ? 0 : (2 * sumBB) / (nb * (nb - 1));
      return ((2 * na * nb) / (na + nb)) * (ab - aa / 2 - bb / 2);
    }

    out.push({ k: m, q: q(m) });
    for (var k = m + 1; k <= n - m; k++) {
      var moved = series[k - 1];
      for (i = 0; i < k - 1; i++) sumAB -= Math.abs(series[i] - moved);
      for (j = k; j < n; j++) sumAB += Math.abs(moved - series[j]);
      for (j = k; j < n; j++) sumBB -= Math.abs(moved - series[j]);
      for (i = 0; i < k - 1; i++) sumAA += Math.abs(series[i] - moved);
      out.push({ k: k, q: q(k) });
    }
    return out;
  }

  // The most significant single change point (EDivisive with maxPoints = 1):
  // the split with the largest Q, kept only if Q > 0. Ties keep the earliest
  // split, like the engine's strict ">".
  function bestChangePoint(series, m) {
    var curve = energyCurve(series, m);
    var best = null;
    for (var i = 0; i < curve.length; i++) {
      if (best === null || curve[i].q > best.q) best = curve[i];
    }
    if (best === null || best.q <= 0) return null;
    return { index: best.k, q: best.q };
  }

  // ---- the engine's decision (computeChangeStats + evaluateQuery) ------------------
  // samples: [{t (minutes from the deploy), calls, totalMs}], in time order.
  // cfg: {latencyThreshold, pThreshold, minSamples, minExecutions, toleranceMin}
  // Returns every intermediate number, and the verdict:
  //   "InsufficientData" | "NoRegression" | "Detected", with `reason` saying which
  //   gate stopped it.
  function analyse(samples, cfg) {
    var pre = [], post = [], i;
    for (i = 0; i < samples.length; i++) {
      var s = samples[i];
      if (s.calls <= 0) continue; // an interval with no calls carries no latency
      if (s.t <= 0) pre.push(s);
      if (s.t >= 0) post.push(s);
    }
    function calls(list) {
      var c = 0;
      for (var k = 0; k < list.length; k++) c += list[k].calls;
      return c;
    }
    function lat(list) {
      return list.map(function (x) { return x.totalMs / x.calls; });
    }
    function windowMean(list) {
      var c = 0, t = 0;
      for (var k = 0; k < list.length; k++) { c += list[k].calls; t += list[k].totalMs; }
      return c ? t / c : 0;
    }
    var r = {
      pre: pre, post: post,
      preCalls: calls(pre), postCalls: calls(post),
      verdict: "NoRegression", reason: "", stage: 0,
      meanPre: 0, meanPost: 0, factor: 0,
      series: [], times: [], m: 0, curve: [], cp: null, changeAtMin: null, distanceMin: null,
      welch: null, significant: false
    };
    var tooFewSamples = pre.length < cfg.minSamples || post.length < cfg.minSamples;
    var tooFewExec = r.preCalls < cfg.minExecutions || r.postCalls < cfg.minExecutions;
    if (tooFewSamples || tooFewExec) {
      r.verdict = "InsufficientData";
      r.reason = tooFewSamples ? "samples" : "executions";
      return r;
    }
    r.meanPre = windowMean(pre);
    r.meanPost = windowMean(post);
    r.factor = r.meanPre > 0 ? r.meanPost / r.meanPre : 0;
    if (r.factor - 1 < cfg.latencyThreshold) {
      r.reason = "prefilter";
      r.stage = 0;
      return r;
    }
    // Stage 1: the merged, chronological series.
    var all = pre.concat(post).sort(function (a, b) { return a.t - b.t; });
    r.times = all.map(function (x) { return x.t; });
    r.series = lat(all);
    r.m = minSegLen(r.series.length);
    r.curve = energyCurve(r.series, r.m);
    r.cp = bestChangePoint(r.series, r.m);
    if (!r.cp) {
      r.reason = "nochangepoint";
      r.stage = 1;
      return r;
    }
    r.changeAtMin = r.times[r.cp.index];
    // Stage 2: Welch on the two real segments.
    r.welch = welch(r.series.slice(0, r.cp.index), r.series.slice(r.cp.index));
    r.significant = r.welch.p <= cfg.pThreshold;
    r.distanceMin = Math.abs(r.changeAtMin);
    r.stage = 2;
    if (r.distanceMin > cfg.toleranceMin) {
      r.reason = "tolerance";
      return r;
    }
    if (!r.significant) {
      r.reason = "pvalue";
      return r;
    }
    r.verdict = "Detected";
    r.reason = "detected";
    return r;
  }

  // ---- series generators for the widgets -------------------------------------------
  // One scrape interval's samples around a deploy at t = 0.
  //   opts: {windowMin, scrapeSec, base (ms), size (relative step, 0.5 = +50%),
  //          lagMin (minutes after the deploy that the step really lands),
  //          cv (per-call coefficient of variation), callsPerScrape,
  //          phi (AR(1) coefficient of the log-latency noise), seed}
  function generate(opts) {
    var rng = mulberry32(opts.seed);
    var step = opts.scrapeSec / 60;
    var half = Math.round(opts.windowMin / step);
    var out = [], prev = 0;
    var sigma = Math.sqrt(Math.log(1 + opts.cv * opts.cv));
    var phi = opts.phi || 0;
    for (var i = -half; i < half; i++) {
      var t = (i + 0.5) * step;
      var level = t >= opts.lagMin ? opts.base * (1 + opts.size) : opts.base;
      var c = Math.max(1, Math.round(opts.callsPerScrape * (1 + 0.25 * gauss(rng))));
      // log-normal per-call latency: the mean of c calls has CV ~ cv / sqrt(c)
      var z = gauss(rng);
      prev = phi * prev + Math.sqrt(1 - phi * phi) * z;
      var x = level * Math.exp((sigma / Math.sqrt(c)) * prev - (sigma * sigma) / (2 * c));
      out.push({ t: t, calls: c, totalMs: x * c });
    }
    return out;
  }

  // ---- other closed forms used on the page ------------------------------------------
  // Sellke, Bayarri and Berger (2001): for p < 1/e, the evidence against "no
  // change" is at most 1 / (-e p ln p), so with even prior odds the posterior
  // probability that nothing changed is at least 1 / (1 + 1/B), B = -e p ln p.
  // Returns null where the calibration does not apply (p >= 1/e, or p <= 0).
  function sbbBound(p) {
    if (!(p > 0) || p >= 1 / Math.E) return null;
    var b = -Math.E * p * Math.log(p);
    return 1 / (1 + 1 / b);
  }

  // Calls needed per window to detect a relative change delta at 5% size and
  // 80% power, for per-call coefficient of variation cv: 2 (1.96 + 0.84)^2 cv^2 / delta^2.
  function callsNeeded(cv, delta) {
    var z = 1.96 + 0.84;
    return (2 * z * z * cv * cv) / (delta * delta);
  }

  var api = {
    mulberry32: mulberry32, gauss: gauss, mean: mean, variance: variance,
    lgamma: lgamma, incompleteBeta: incompleteBeta, tDistPValue: tDistPValue, tPdf: tPdf, welch: welch,
    minSegLen: minSegLen, energyCurve: energyCurve, bestChangePoint: bestChangePoint,
    analyse: analyse, generate: generate, sbbBound: sbbBound, callsNeeded: callsNeeded
  };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  root.PgrrMath = api;
})(typeof window !== "undefined" ? window : globalThis);
