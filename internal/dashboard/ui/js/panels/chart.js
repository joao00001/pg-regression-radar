import { h, s } from '../dom.js';
import { extent, linear, niceTicks, pathD, medianGap, baselineBefore, nearest } from '../chartmath.js';
import { clock, ms } from '../model.js';

const M = { l: 52, r: 96, t: 14, b: 26 };
const STRIP = 34;

function toPoints(list) {
  return (list || []).map((p) => ({ at: Date.parse(p.at), meanMs: p.meanMs, n: p.n }));
}

// Draws one query's canary-versus-stable chart into el. The series are exactly
// what the engine compared; nothing is recomputed here except the pre-deploy
// average, which is only a reference line.
export function renderChart(el, series, label, ui = {}) {
  el.replaceChildren();
  if (!series) return;
  const trt = toPoints(series.treatment);
  const ctl = toPoints(series.control);
  const hasControl = ctl.length > 0;
  const deployAt = Date.parse(series.deployAt);
  const names = hasControl ? { t: 'Canary', c: 'Stable pods' } : { t: label || 'This query', c: '' };
  // The panel's settings: the data as a table instead of a picture, and what the picture carries.
  const asTable = ui.viz === 'table';
  const wantBase = ui.baseline !== false, wantStrip = ui.samples !== false, fromZero = ui.zero !== false;
  const STRIP_H = wantStrip ? STRIP : 0;

  const legend = h('ul', { class: 'legend' },
    h('li', null, h('span', { class: 'swatch s-trt', 'aria-hidden': 'true' }), names.t),
    hasControl ? h('li', null, h('span', { class: 'swatch s-ctl', 'aria-hidden': 'true' }), names.c) : null,
    h('li', null, h('span', { class: 'swatch s-dep', 'aria-hidden': 'true' }), 'Deploy'),
    wantBase && Number.isFinite(baselineBefore(trt.length ? trt : ctl, deployAt))
      ? h('li', null, h('span', { class: 'swatch s-base', 'aria-hidden': 'true' }), `Before the deploy: ${ms(baselineBefore(trt.length ? trt : ctl, deployAt))}`) : null);

  const plot = h('div', { class: 'plot' });
  const tip = h('div', { class: 'tip', role: 'status', hidden: true });
  const startOpen = asTable || !!ui.tableOpen;
  const tableBtn = h('button', { class: 'btn', type: 'button', 'aria-expanded': String(startOpen) }, startOpen ? 'Hide table' : 'Show as table');
  const table = h('div', { class: 'datatable', hidden: !startOpen });
  tableBtn.addEventListener('click', () => {
    const open = table.hidden;
    table.hidden = !open;
    tableBtn.setAttribute('aria-expanded', String(open));
    tableBtn.textContent = open ? 'Hide table' : 'Show as table';
    if (ui.onTable) ui.onTable(open);
  });

  const caption = hasControl
    ? 'Mean duration of this query on the canary and on the stable pods, from the same scrapes. The red band is the time after the deploy.'
    : 'Mean duration of this query before and after the deploy; the red band is the time after it. There is no stable-pods line: this verdict did not rest on a concurrent control.';
  el.append(...[h('div', { class: 'chart-head' }, legend, asTable ? null : tableBtn), asTable ? null : h('div', { class: 'plot-wrap' }, plot, tip),
    h('p', { class: 'fig' }, h('b', { text: 'Fig. 1' }), caption), table].filter(Boolean));
  if (asTable) table.classList.add('tall');

  if (!trt.length && !asTable) {
    plot.append(h('p', { class: 'empty' }, 'No series for this query yet.'));
    return;
  }

  let first = !!ui.enter;
  const draw = () => {
    const W = Math.max(280, plot.clientWidth || 640);
    // The panel decides how tall the picture is: all the height the plot has.
    const H = Math.max(140, (plot.clientHeight || 280) - STRIP_H);
    const all = [...trt, ...ctl];
    const [t0, t1] = extent([...all.map((p) => p.at), deployAt]);
    const [ymin, ymax] = extent(all.map((p) => p.meanMs));
    const yTop = ymax * 1.12 || 1;
    // From zero, a change is as big as it is; fitted to the data, a small one looks large.
    const base0 = fromZero ? 0 : Math.max(0, ymin - (ymax - ymin) * 0.15 - (ymax === ymin ? ymin * 0.1 : 0));
    const x = linear(t0, t1, M.l, W - M.r);
    const y = linear(base0, yTop, H - M.b, M.t);
    const gap = medianGap(trt.map((p) => p.at)) * 2.5 || Infinity;
    const base = baselineBefore(trt.length ? trt : ctl, deployAt);

    const svg = s('svg', { class: first ? 'chart enter' : 'chart', width: W, height: H + STRIP_H, viewBox: `0 0 ${W} ${H + STRIP_H}`, role: 'img',
      'aria-label': `${names.t}${hasControl ? ' against ' + names.c.toLowerCase() : ''}: mean duration over time, with the deploy marked.` });

    for (const v of niceTicks(base0, yTop, 4)) {
      svg.append(s('line', { class: 'grid', x1: M.l, x2: W - M.r, y1: y(v), y2: y(v) }),
        s('text', { class: 'tick', x: M.l - 8, y: y(v) + 4, 'text-anchor': 'end', text: String(v) }));
    }
    svg.append(s('text', { class: 'tick', x: M.l - 8, y: M.t - 4, 'text-anchor': 'end', text: 'ms' }));
    const xt = niceTicks(0, 1, 5).map((f) => t0 + f * (t1 - t0));
    for (const t of xt) svg.append(s('text', { class: 'tick', x: x(t), y: H - 8, 'text-anchor': 'middle', text: clock(t) }));

    // Everything after the deploy is washed in red, as in the docs' figure.
    svg.append(s('rect', { class: 'wash', x: x(deployAt), y: M.t, width: Math.max(0, W - M.r - x(deployAt)), height: H - M.b - M.t }));

    if (wantBase && Number.isFinite(base)) {
      svg.append(s('line', { class: 'baseline', x1: M.l, x2: W - M.r, y1: y(base), y2: y(base) }));
    }
    svg.append(
      s('line', { class: 'deploy', x1: x(deployAt), x2: x(deployAt), y1: M.t, y2: H - M.b }),
      s('text', { class: 'deploy-label', x: x(deployAt) + 5, y: M.t + 9, text: `Deploy ${clock(deployAt)}` }));

    // pathLength normalises the line, so the draw-in animation needs no measuring.
    if (hasControl) svg.append(s('path', { class: 'line ctl', pathLength: 1, d: pathD(ctl.map((p) => [x(p.at), y(p.meanMs)]), gap) }));
    svg.append(s('path', { class: 'line trt', pathLength: 1, d: pathD(trt.map((p) => [x(p.at), y(p.meanMs)]), gap) }));

    // Direct labels at the right end of each line.
    const end = (arr) => arr[arr.length - 1];
    const labels = [{ p: end(trt), cls: 'trt', text: names.t }];
    if (hasControl) labels.push({ p: end(ctl), cls: 'ctl', text: names.c });
    labels.sort((a, b) => y(a.p.meanMs) - y(b.p.meanMs));
    let lastY = -99;
    for (const l of labels) {
      const yy = Math.max(y(l.p.meanMs) + 4, lastY + 14);
      lastY = yy;
      svg.append(s('text', { class: `endlabel ${l.cls}`, x: x(l.p.at) + 7, y: yy, text: l.text }));
    }

    // How thin the evidence was: calls (or sampled executions) behind each point.
    const nmax = Math.max(1, ...trt.map((p) => p.n));
    const bw = Math.max(1, Math.min(8, ((W - M.l - M.r) / Math.max(1, trt.length)) - 1));
    if (wantStrip) svg.append(s('text', { class: 'tick', x: M.l - 8, y: H + 12, 'text-anchor': 'end', text: 'n' }));
    for (const p of wantStrip ? trt : []) {
      const hh = Math.max(1, (p.n / nmax) * (STRIP - 8));
      svg.append(s('rect', { class: 'nbar', x: x(p.at) - bw / 2, y: H + STRIP - 4 - hh, width: bw, height: hh, rx: 1 }));
    }

    // Hover layer.
    const cross = s('line', { class: 'cross', y1: M.t, y2: H - M.b, visibility: 'hidden' });
    const dotT = s('circle', { class: 'dot trt', r: 4, visibility: 'hidden' });
    const dotC = s('circle', { class: 'dot ctl', r: 4, visibility: 'hidden' });
    const hit = s('rect', { x: M.l, y: M.t, width: W - M.l - M.r, height: H - M.t - M.b, fill: 'transparent' });
    const times = trt.map((p) => p.at);
    const move = (ev) => {
      const r = svg.getBoundingClientRect();
      const t = t0 + ((ev.clientX - r.left - M.l) / (W - M.l - M.r)) * (t1 - t0);
      const i = nearest(times, t);
      if (i < 0) return;
      const p = trt[i];
      const c = hasControl ? ctl[nearest(ctl.map((q) => q.at), p.at)] : null;
      cross.setAttribute('x1', x(p.at)); cross.setAttribute('x2', x(p.at)); cross.setAttribute('visibility', 'visible');
      dotT.setAttribute('cx', x(p.at)); dotT.setAttribute('cy', y(p.meanMs)); dotT.setAttribute('visibility', 'visible');
      if (c) { dotC.setAttribute('cx', x(c.at)); dotC.setAttribute('cy', y(c.meanMs)); dotC.setAttribute('visibility', 'visible'); }
      tip.replaceChildren(
        h('strong', { text: clock(p.at, true) }),
        h('div', null, h('span', { class: 'swatch s-trt', 'aria-hidden': 'true' }), `${names.t}: ${ms(p.meanMs)}`, h('span', { class: 'muted', text: `  n=${p.n}` })),
        c ? h('div', null, h('span', { class: 'swatch s-ctl', 'aria-hidden': 'true' }), `${names.c}: ${ms(c.meanMs)}`, h('span', { class: 'muted', text: `  n=${c.n}` })) : null);
      tip.hidden = false;
      const left = Math.min(x(p.at) + 12, W - 190);
      tip.style.left = `${Math.max(0, left)}px`;
      tip.style.top = `${M.t + 6}px`;
    };
    const leave = () => { cross.setAttribute('visibility', 'hidden'); dotT.setAttribute('visibility', 'hidden'); dotC.setAttribute('visibility', 'hidden'); tip.hidden = true; };
    hit.addEventListener('mousemove', move);
    hit.addEventListener('mouseleave', leave);
    svg.append(cross, dotT, dotC, hit);
    plot.replaceChildren(svg);
    first = false;
  };
  if (!asTable) draw();
  if (!asTable && typeof ResizeObserver !== 'undefined') {
    let w = plot.clientWidth, hh = plot.clientHeight;
    new ResizeObserver(() => {
      if (plot.clientWidth !== w || plot.clientHeight !== hh) { w = plot.clientWidth; hh = plot.clientHeight; draw(); }
    }).observe(plot);
  }

  // The same numbers as a table, for anyone who cannot read the picture.
  const rows = trt.slice(asTable ? -400 : -60).map((p) => {
    const c = hasControl ? ctl[nearest(ctl.map((q) => q.at), p.at)] : null;
    return h('tr', null, h('td', { text: clock(p.at, true) }), h('td', { class: 'tn', text: ms(p.meanMs) }),
      hasControl ? h('td', { class: 'tn', text: c ? ms(c.meanMs) : '–' }) : null, h('td', { class: 'tn', text: String(p.n) }));
  });
  table.append(h('table', null,
    h('thead', null, h('tr', null, h('th', { text: 'Time' }), h('th', { class: 'tn', text: names.t }), hasControl ? h('th', { class: 'tn', text: names.c }) : null, h('th', { class: 'tn', text: 'n' }))),
    h('tbody', null, rows)));
}
