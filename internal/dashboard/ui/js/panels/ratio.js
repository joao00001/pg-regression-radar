import { h, s } from '../dom.js';
import { ratioRows, ratioAxis, ratioPos, times } from '../model.js';
import { heading, emptyNote, mount } from './common.js';

const ROW = 24, TOP = 26, BOTTOM = 8, LABEL = 0.38;

// Every query's ratio with the interval it could plausibly be in, on one axis,
// with a line at 1.0 where nothing changed. Right of the line is slower. A ratio
// is drawn filled when the radar flagged the query, and in the colour of its
// direction only when its whole interval is on one side of 1.0.
export function ratioChartPanel(host, ctx) {
  host.classList.add('p-ratio');
  const plot = h('div', { class: 'ratio-plot' });
  let last = null;
  const draw = () => {
    if (!last) return;
    const { rows, axis, unit } = last;
    // clientWidth includes the padding that keeps the chart off the panel's edges.
    const cs = getComputedStyle(plot);
    const inner = plot.clientWidth - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
    const W = Math.max(240, inner || 360);
    const labelW = Math.round(W * LABEL), x0 = labelW + 8, x1 = W - 16;
    const H = TOP + rows.length * ROW + BOTTOM;
    const X = (v) => { const { pos } = ratioPos(v, axis); return x0 + pos * (x1 - x0); };
    const svg = s('svg', { class: 'ratio', width: W, height: H, viewBox: `0 0 ${W} ${H}`, role: 'img',
      'aria-label': `${rows.length} queries: each one's slowdown ratio with its interval. ${rows.filter((r) => r.rejected).length} flagged. A ratio of 1 means nothing changed.` });
    for (const t of axis.ticks) {
      svg.append(s('line', { class: t === 1 ? 'one' : 'grid', x1: X(t), x2: X(t), y1: TOP - 6, y2: H - BOTTOM }),
        s('text', { class: `tick${t === 1 ? ' one' : ''}`, x: X(t), y: 10, 'text-anchor': 'middle', text: `${t}×` }));
    }
    rows.forEach((r, i) => {
      const y = TOP + i * ROW + ROW / 2;
      const dir = r.moved ? (r.ratio > 1 ? 'slow' : 'fast') : 'flat';
      const lo = ratioPos(r.low, axis), hi = ratioPos(r.high, axis);
      const g = s('g', { class: `rrow ${dir}${r.rejected ? ' flagged' : ''}${r.key === unit ? ' sel' : ''}`, tabindex: '0', role: 'button',
        'aria-label': `${r.label}: ${times(r.ratio)}, from ${times(r.low)} to ${times(r.high)}${r.rejected ? ', flagged' : ''}`, 'aria-pressed': String(r.key === unit) });
      g.append(
        s('title', { text: `${r.label}\n${times(r.ratio)} (95% between ${times(r.low)} and ${times(r.high)})${r.rejected ? '\nFlagged by the radar' : ''}` }),
        s('rect', { class: 'hit', x: 0, y: y - ROW / 2, width: W, height: ROW }),
        s('text', { class: 'rlabel', x: 0, y: y + 4, text: clip(r.label, Math.floor(labelW / 6.4)) }),
        s('line', { class: 'ci', x1: X(r.low), x2: X(r.high), y1: y, y2: y }),
        lo.clipped ? s('path', { class: 'clip', d: `M${x0 + 5},${y - 4}L${x0},${y}L${x0 + 5},${y + 4}` }) : null,
        hi.clipped ? s('path', { class: 'clip', d: `M${x1 - 5},${y - 4}L${x1},${y}L${x1 - 5},${y + 4}` }) : null,
        r.rejected ? s('circle', { class: 'pt', cx: X(r.ratio), cy: y, r: 4.5 }) : s('circle', { class: 'pt hollow', cx: X(r.ratio), cy: y, r: 3.8 }));
      const pick = () => ctx.actions.pickUnit(r.key);
      g.addEventListener('click', pick);
      g.addEventListener('keydown', (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(); } });
      svg.append(g);
    });
    plot.replaceChildren(svg);
  };
  if (typeof ResizeObserver !== 'undefined') {
    let w = plot.clientWidth;
    new ResizeObserver(() => { if (plot.clientWidth !== w) { w = plot.clientWidth; draw(); } }).observe(plot);
  }
  return {
    render(view) {
      const head = heading(ctx, 'Ratio per query');
      const c = view.kase, ev = c && c.history && c.history[c.history.length - 1];
      if (!c) { mount(host, head, emptyNote(view.scopeNote || 'Pick a deploy to see how each of its queries moved.')); last = null; return; }
      if (!ev) { mount(host, head, emptyNote('Appears once the radar has judged this deploy.')); last = null; return; }
      const rows = ratioRows(ev.units, ctx.options.limit, ctx.options.show);
      if (!rows.length) { mount(host, head, emptyNote(ctx.query && ctx.query.length ? 'No query of this deploy matches the query of this panel.' : ctx.options.show === 'flagged' ? 'No query is flagged. Change "Show" in the settings of this panel to see all of them.' : 'No query had enough data for a ratio.')); last = null; return; }
      last = { rows, axis: ratioAxis(rows), unit: view.unit };
      mount(host, head, plot,
        h('p', { class: 'fig' }, h('b', { text: 'Reading it' }), 'Each line is the range the slowdown could be in. Right of 1× is slower, left is faster. A filled dot was flagged by the radar.'));
      draw();
    },
  };
}

const clip = (t, n) => (t.length > n ? `${t.slice(0, Math.max(1, n - 1))}…` : t);
