import { h } from '../dom.js';
import { renderChart } from './chart.js';
import { heading, emptyNote, mount } from './common.js';
import { labelUnits } from '../model.js';

const seen = new Set();

// The picked deploy's measurement over time, canary against control, with the
// deploy marked. The query it draws is the one picked in the evidence table or
// in the list here.
export function canaryChartPanel(host, ctx) {
  host.classList.add('p-canary');
  const body = h('div', { class: 'chart-host' });
  let shown = '';
  return {
    render(view) {
      const c = view.kase;
      const last = c && c.history && c.history[c.history.length - 1];
      const title = ctx.explicitTitle || 'Canary against stable';
      if (!c) { mount(host, h('h3', { class: 'ph' }, title), emptyNote(view.scopeNote || 'Pick a deploy to see its canary against the stable pods.')); shown = ''; return; }
      if (!last) {
        mount(host, h('h3', { class: 'ph' }, 'Waiting for the first judgement'), emptyNote('The radar compares the queries before and after a deploy, and needs about ten minutes of data after it to say anything. The evidence and the chart appear here as soon as it has judged.'));
        shown = '';
        return;
      }
      const units = last.units || [];
      if (!units.length && ctx.query && ctx.query.length) { mount(host, h('h3', { class: 'ph' }, title), emptyNote('No query of this deploy matches the query of this panel.')); shown = ''; return; }
      const u = units.find((x) => x.key === view.unit);
      const names = labelUnits(units);
      const picker = units.length > 1 ? h('select', { class: 'unit-pick', 'aria-label': 'Query to chart', onchange: (e) => ctx.actions.pickUnit(e.target.value) },
        units.map((x) => h('option', { value: x.key, selected: x.key === view.unit, text: `${names.get(x.key)}${x.rejected ? ' (flagged)' : ''}` }))) : null;
      // The frame is rebuilt only when what it holds changes; the chart inside redraws on its own.
      const sig = `${c.id}|${view.unit}|${units.length}`;
      if (sig !== shown || !body.isConnected) {
        shown = sig;
        mount(host, h('div', { class: 'ph-row' }, h('h3', { class: 'ph' }, title), picker), body);
      }
      const key = `${ctx.uid}|${c.id}|${view.unit}`;
      const enter = !seen.has(key) && !!view.series;
      if (view.series) seen.add(key);
      if (view.unit && !view.series && !view.seriesMissing) { body.replaceChildren(emptyNote('Loading the series…')); return; }
      if (!view.series && view.seriesMissing) { body.replaceChildren(emptyNote('No series was recorded for this query. Pick another from the list.')); return; }
      const o = ctx.options;
      renderChart(body, view.series, u && names.get(u.key), {
        enter, tableOpen: ctx.ui.tableOpen, onTable: (v) => { ctx.ui.tableOpen = v; },
        viz: o.viz, baseline: o.baseline, samples: o.samples, zero: o.zero,
      });
    },
  };
}
