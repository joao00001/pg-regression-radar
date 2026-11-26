import { h } from '../dom.js';
import { timeline, compareEvaluations, percent, times, clock } from '../model.js';
import { gradeBadge, heading, emptyNote, mount } from './common.js';

const actionWord = (a) => ({ none: 'no action', alert: 'alert', hold: 'pause', rollback: 'abort' }[a] || a || '');

// The judgements of the picked deploy in order. The latest is compared with the
// one before it; pick two others to see how the evidence moved between them. An
// older judgement keeps only the queries that were flagged, and the panel says so.
export function evaluationsPanel(host, ctx) {
  host.classList.add('p-evals');
  return {
    render(view) {
      const c = view.kase;
      const head = heading(ctx, 'Evaluations');
      if (!c) { mount(host, head, emptyNote(view.scopeNote || 'Pick a deploy to see how the radar judged it over time.')); return; }
      const t = timeline(c.history);
      if (!t.length) { mount(host, head, emptyNote('The radar has not judged this deploy yet.')); return; }
      const cmp = ctx.ui.compare[c.id] || {};
      const b = Number.isInteger(cmp.b) && cmp.b < t.length ? cmp.b : t.length - 1;
      const a = Number.isInteger(cmp.a) && cmp.a < t.length && cmp.a !== b ? cmp.a : Math.max(0, b - 1);
      const set = (patch) => { ctx.ui.compare[c.id] = { a, b, ...patch }; ctx.actions.rerender(); };

      const list = h('ol', { class: 'evlist' }, t.map((e) => h('li', { class: `ev${e.index === a ? ' is-a' : ''}${e.index === b ? ' is-b' : ''}` },
        h('span', { class: 'ev-n', text: `#${e.index + 1}` }),
        h('span', { class: 'ev-time', title: clock(e.at, true), text: clock(e.at) }),
        gradeBadge(e.grade, '', false),
        h('span', { class: 'ev-what', text: e.health === 'Regressed' ? `${percent(e.impact)} added · ${actionWord(e.action)}` : e.health.toLowerCase() }),
        h('span', { class: 'ev-pick' },
          h('button', { class: 'chip', type: 'button', 'aria-pressed': String(e.index === a), disabled: e.index === b, onclick: () => set({ a: e.index }), title: 'Compare from this judgement', text: 'from' }),
          h('button', { class: 'chip', type: 'button', 'aria-pressed': String(e.index === b), disabled: e.index === a, onclick: () => set({ b: e.index }), title: 'Compare to this judgement', text: 'to' })))));

      let diff = null;
      if (t.length > 1 && a !== b) {
        const [from, to] = a < b ? [a, b] : [b, a];
        const cmpData = compareEvaluations(c.history[from], c.history[to]);
        const arrow = (x, y, f) => (x === y ? f(x) : `${f(x)} → ${f(y)}`);
        const dash = (v) => v || '–';
        diff = h('div', { class: 'evdiff' },
          h('p', { class: 'evdiff-head' }, `#${from + 1} → #${to + 1}`),
          h('dl', { class: 'evsum' },
            h('div', null, h('dt', { text: 'Grade' }), h('dd', { text: arrow(cmpData.grade[0], cmpData.grade[1], dash) })),
            h('div', null, h('dt', { text: 'Added DB time' }), h('dd', { text: arrow(cmpData.impact[0], cmpData.impact[1], percent) })),
            h('div', null, h('dt', { text: 'Queries flagged' }), h('dd', { text: arrow(cmpData.regressed[0], cmpData.regressed[1], String) })),
            h('div', null, h('dt', { text: 'Coverage' }), h('dd', { text: arrow(cmpData.coverage[0], cmpData.coverage[1], percent) }))),
          cmpData.units.length ? h('div', { class: 'table-scroll' }, h('table', { class: 'evtable' },
            h('thead', null, h('tr', null, h('th', { text: 'Query' }), h('th', { class: 'tn', text: `#${from + 1}` }), h('th', { class: 'tn', text: `#${to + 1}` }), h('th', { class: 'tn', text: 'Change' }))),
            h('tbody', null, cmpData.units.slice(0, 12).map((u) => h('tr', null,
              h('td', null, h('button', { class: 'link', type: 'button', onclick: () => ctx.actions.pickUnit(u.key), text: u.label })),
              h('td', { class: 'tn', title: u.a ? `${times(u.a.low)} to ${times(u.a.high)}` : 'Not recorded: older judgements keep only the flagged queries', text: u.a ? times(u.a.ratio) : 'not kept' }),
              h('td', { class: 'tn', title: u.b ? `${times(u.b.low)} to ${times(u.b.high)}` : 'Not recorded: older judgements keep only the flagged queries', text: u.b ? times(u.b.ratio) : 'not kept' }),
              h('td', { class: 'tn', text: Number.isFinite(u.delta) ? `${u.delta >= 1 ? '+' : ''}${((u.delta - 1) * 100).toFixed(0)}%` : '–' })))))) : h('p', { class: 'muted', text: 'No queries were flagged in either judgement.' }),
          h('p', { class: 'fig' }, 'An older judgement keeps only the queries that were flagged then, so a query shown as "not kept" is not recorded there, not necessarily fine.'));
      }
      mount(host, head, list, diff);
    },
  };
}
