// Motion that explains, never decorates. Everything here checks the person's
// reduced-motion setting first and falls back to showing the final state at once.

export const reduced = () => typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;

// Count a number to its value. It starts from what that number last showed
// (keyed), so a live update moves from the old value to the new one instead of
// restarting from zero.
const shown = new Map();
const raf = new WeakMap();
export function countTo(el, to, fmt, key) {
  const from = shown.has(key) ? shown.get(key) : 0;
  shown.set(key, to);
  if (raf.has(el)) cancelAnimationFrame(raf.get(el));
  if (reduced() || !Number.isFinite(to) || !Number.isFinite(from) || from === to) { el.textContent = fmt(to); return; }
  const t0 = performance.now(), dur = 700;
  const step = (t) => {
    const k = Math.min(1, (t - t0) / dur);
    const e = 1 - Math.pow(1 - k, 4);
    el.textContent = fmt(from + (to - from) * e);
    if (k < 1) raf.set(el, requestAnimationFrame(step)); else el.textContent = fmt(to);
  };
  el.textContent = fmt(from);
  raf.set(el, requestAnimationFrame(step));
}

// Give each direct child of a block an index, for staggered entrances.
export function stagger(el) {
  [...el.children].forEach((c, i) => c.style.setProperty('--i', String(Math.min(i, 8))));
}

// One class, removed and added again, replays a CSS animation.
export function replay(el, cls) {
  el.classList.remove(cls);
  void el.offsetWidth;
  el.classList.add(cls);
}
