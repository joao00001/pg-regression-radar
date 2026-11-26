// The arithmetic of the chart, apart from drawing it, so it can be tested.

export function extent(values) {
  let lo = Infinity, hi = -Infinity;
  for (const v of values) { if (v < lo) lo = v; if (v > hi) hi = v; }
  return Number.isFinite(lo) ? [lo, hi] : [0, 1];
}

export function linear(d0, d1, r0, r1) {
  const span = d1 - d0 || 1;
  return (x) => r0 + ((x - d0) / span) * (r1 - r0);
}

// Round tick values at 1, 2 or 5 times a power of ten, between lo and hi, about count of them.
export function niceTicks(lo, hi, count = 4) {
  if (!(hi > lo)) return [lo];
  const raw = (hi - lo) / count;
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  // The nice step that gives the number of ticks closest to the one asked for.
  const span = hi - lo;
  const step = [1, 2, 5, 10].map((m) => m * p).reduce((best, s) => (Math.abs(span / s - count) < Math.abs(span / best - count) ? s : best));
  const out = [];
  for (let v = Math.ceil(lo / step) * step; v <= hi + 1e-9; v += step) out.push(+v.toFixed(10));
  return out;
}

// A path through points [[x, y], …], broken wherever two points are further
// apart than maxGap on x (a stretch with no data is not drawn as if it had some).
export function pathD(points, maxGap = Infinity) {
  let d = '', prev = null;
  for (const [x, y] of points) {
    d += prev === null || x - prev > maxGap ? `M${x.toFixed(1)},${y.toFixed(1)}` : `L${x.toFixed(1)},${y.toFixed(1)}`;
    prev = x;
  }
  return d;
}

// The median gap between consecutive timestamps: the scrape interval.
export function medianGap(times) {
  const g = [];
  for (let i = 1; i < times.length; i++) g.push(times[i] - times[i - 1]);
  g.sort((a, b) => a - b);
  return g.length ? g[Math.floor(g.length / 2)] : 0;
}

// Weighted mean of the points before t, or NaN when there are none.
export function baselineBefore(points, t) {
  let sum = 0, n = 0;
  for (const p of points) {
    if (p.at < t) { const w = p.n > 0 ? p.n : 1; sum += p.meanMs * w; n += w; }
  }
  return n ? sum / n : NaN;
}

// The index of the point whose time is nearest to t.
export function nearest(times, t) {
  if (!times.length) return -1;
  let lo = 0, hi = times.length - 1;
  while (hi - lo > 1) { const m = (lo + hi) >> 1; if (times[m] < t) lo = m; else hi = m; }
  return Math.abs(times[lo] - t) <= Math.abs(times[hi] - t) ? lo : hi;
}
