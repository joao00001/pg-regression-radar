import { h } from '../dom.js';
import { tone, relative, clock, percent, times, gradeOf, labelUnits, evidenceRows } from '../model.js';
import { gradeBadge, stateTag, heading, emptyNote, mount } from './common.js';
import { countTo } from '../motion.js';
import { ratioChartPanel } from './ratio.js';

const GRADE_KEY = [
  ['A', 'Concurrent control: the same query on the pods the deploy did not reach, at the same time. The only evidence that justifies an abort.'],
  ['B', 'Synthetic control, built from queries the deploy did not touch and checked against fake deploy times. Enough to pause.'],
  ['C', "The query's own history only. It can alert, not act."],
  ['D', 'Not enough data to say anything.'],
];

const noDeploy = (view) => emptyNote((view && view.scopeNote) || 'Pick a deploy to see what the radar saw and why it decided what it did.');
const lastOf = (c) => c.history && c.history[c.history.length - 1];

// The picked deploy: what it is, what state it is in, what the radar said.
export function deployHeadPanel(host, ctx) {
  host.classList.add('p-head');
  return {
    render(view) {
      const c = view.kase;
      if (!c) { mount(host, heading(ctx, ''), noDeploy(view)); return; }
      const next = c.nextEvaluation ? Date.parse(c.nextEvaluation) : null;
      mount(host, heading(ctx, ''), h('header', { class: `detail-head tone-${tone(c)}` },
        h('div', { class: 'title-row' },
          h('h2', null, h('span', { text: `${c.namespace} / ` }), h('b', { text: c.app })),
          stateTag(c),
          ctx.board !== 'case' ? h('a', { class: 'link open-case', href: `#/case/${encodeURIComponent(c.id)}`, text: 'Open as a case' }) : null),
        h('p', { class: 'headline', text: c.sentence }),
        h('p', { class: 'facts' },
          `Deployed ${clock(Date.parse(c.at))} (${relative(Date.parse(c.at), view.now)})`,
          c.revision ? `, revision ${c.revision}` : '',
          `, ${c.evaluations} ${c.evaluations === 1 ? 'judgement' : 'judgements'}`,
          next ? `, next check ${relative(next, view.now)}` : '')));
    },
  };
}

function stat(id, key, value, fmt, label, suffix) {
  const v = h('span', { class: 'v' });
  countTo(v, value, fmt, `${id}:${key}`);
  return h('div', { class: 'stat' }, h('span', { class: 'num' }, v, suffix ? h('span', { class: 'of', text: suffix }) : null), h('span', { class: 'label', text: label }));
}

// The picked deploy in numbers.
export function figuresPanel(host, ctx) {
  host.classList.add('p-figures');
  return {
    render(view) {
      const c = view.kase, last = c && lastOf(c);
      if (!c) { mount(host, heading(ctx, ''), noDeploy(view)); return; }
      if (!c.latest || !last) { mount(host, heading(ctx, ''), emptyNote('The numbers appear once the radar has judged this deploy.')); return; }
      const d = c.latest, grade = gradeOf(c), k = `${ctx.uid}:${c.id}`, o = ctx.options;
      const stats = [
        o.grade !== false ? h('div', { class: 'stat' }, h('span', { class: 'num' }, gradeBadge(grade, d.gradeNote)), h('span', { class: 'label', text: grade ? 'Evidence grade' : 'No evidence grade' })) : null,
        o.impact !== false ? stat(k, 'impact', d.impact, percent, 'Added DB time') : null,
        o.attribution !== false ? stat(k, 'attr', d.attribution, percent, 'Attributed to deploy') : null,
        o.regressed !== false ? stat(k, 'reg', last.counts.regressed, (v) => String(Math.round(v)), 'Queries regressed', ` / ${last.counts.tested}`) : null,
        o.coverage !== false ? stat(k, 'cov', last.coverage, percent, 'Coverage') : null,
      ].filter(Boolean);
      mount(host, heading(ctx, ''), stats.length ? h('div', { class: 'stats' }, stats) : emptyNote('Every figure is hidden in the settings of this panel.'));
    },
  };
}

// The reasoning behind the grade, in words.
export function whyPanel(host, ctx) {
  host.classList.add('p-why');
  return {
    render(view) {
      const c = view.kase, last = c && lastOf(c);
      const head = h('h3', { class: 'ph' }, ctx.explicitTitle || 'Why it decided this');
      if (!c) { mount(host, head, noDeploy(view)); return; }
      if (!c.latest || !last) { mount(host, head, emptyNote('Nothing to explain yet: the radar has not judged this deploy.')); return; }
      const d = c.latest;
      mount(host, head, h('div', { class: 'why-body' },
        h('p', { text: d.gradeNote }),
        d.wanted !== d.action ? h('p', { class: 'capped' }, `The evidence called for "${d.wanted}", but a safety guard held it at "${d.action}".`) : null,
        d.reasons && d.reasons.length ? h('ul', { class: 'reasons' }, d.reasons.map((r) => h('li', { text: r }))) : null,
        last.cappedBy && last.cappedBy.length ? h('p', { class: 'capped' }, 'What kept the evidence from a higher grade: ', last.cappedBy.join(', '), '.') : null));
    },
  };
}

// Per query: how it moved, with the interval around it. Picking a row picks the
// query the canary chart draws.
export function evidencePanel(host, ctx) {
  host.classList.add('p-evidence');
  // The same queries drawn as ratios: the ratio chart, with this panel's filter and count.
  if (ctx.options.viz === 'ratio') {
    return ratioChartPanel(host, { ...ctx, explicitTitle: ctx.explicitTitle || 'Evidence', options: { show: ctx.options.show, limit: ctx.options.limit } });
  }
  return {
    render(view) {
      const c = view.kase, last = c && lastOf(c);
      const ui = ctx.ui;
      const all = (last && last.units) || [];
      const units = evidenceRows(all, ctx.options);
      const head = heading(ctx, 'Evidence', all.length || null);
      if (!c) { mount(host, head, noDeploy(view)); return; }
      if (!all.length) { mount(host, head, emptyNote(last ? (ctx.query && ctx.query.length ? 'No query of this deploy matches the query of this panel.' : 'No query had enough data to be tested.') : 'The evidence appears once the radar has judged this deploy.')); return; }
      if (!units.length) { mount(host, head, emptyNote('No query is flagged. Change "Show" in the settings of this panel to see all of them.')); return; }
      const limit = ctx.options.limit;
      const shown = ui.showAll ? units : units.slice(0, limit);
      const names = labelUnits(all);
      const o = ctx.options;
      // Without the SQL text a row is a statement and a table; say how to see more.
      const hidden = !units.some((u) => u.text);
      const hint = !hidden ? null : view.config && view.config.showQueries
        ? 'The manager sent no query text. Start the manager with --observe-allow-queries to see it.'
        : 'Query text is hidden. Start the dashboard with --show-queries (and the manager with --observe-allow-queries) to see it.';
      mount(host, head,
        h('div', { class: 'table-scroll' }, h('table', { class: 'evidence' },
          h('thead', null, h('tr', null,
            h('th', { text: 'Query' }), h('th', { text: 'Grade' }), h('th', { class: 'tn', text: 'Slower by' }),
            h('th', { class: 'tn', text: 'Share of time' }), o.flags !== false ? h('th', { text: 'Flags' }) : null, o.counts !== false ? h('th', { class: 'tn', title: 'Observations before and after the deploy', text: 'Before / after' }) : null)),
          h('tbody', null, shown.map((u) => h('tr', { class: u.key === view.unit ? 'selected' : '' },
            h('td', null, h('button', { class: 'link', type: 'button', 'aria-pressed': String(u.key === view.unit), onclick: () => ctx.actions.pickUnit(u.key) },
              names.get(u.key)), h('span', { class: 'keyid', title: `fingerprint ${u.key}`, text: ` ${u.key.slice(0, 6)}` }), u.text ? h('code', { class: 'sql', text: u.text }) : null),
            h('td', null, u.rejected ? h('span', { class: 'ugrade', title: u.gradeNote }, h('b', { text: u.grade }), h('span', { class: 'sr-only', text: ` grade ${u.grade}` })) : h('span', { class: 'muted', text: u.kind.toLowerCase() })),
            h('td', { class: 'tn', title: `95% sequence ${times(u.ratioLow)} to ${times(u.ratioHigh)}`, text: u.rejected ? `${times(u.ratio)}` : '–' }),
            h('td', { class: 'tn', text: percent(u.impactShare) }),
            o.flags !== false ? h('td', null, (u.flags || []).map((f) => h('span', { class: 'flag', title: f.text, text: f.code }))) : null,
            o.counts !== false ? h('td', { class: 'tn', text: `${u.preN} / ${u.postN}` }) : null))))),
        hint ? h('p', { class: 'fig hint', text: hint }) : null,
        units.length > limit ? h('button', { class: 'btn more', type: 'button', onclick: () => { ui.showAll = !ui.showAll; ctx.actions.rerender(); }, text: ui.showAll ? 'Show fewer' : `Show all ${units.length}` }) : null,
        h('details', { class: 'key', open: ui.keyOpen, ontoggle: (e) => { ui.keyOpen = e.target.open; } }, h('summary', { text: 'What do the grades mean?' }),
          h('dl', null, GRADE_KEY.map(([g, text]) => [h('dt', null, gradeBadge(g)), h('dd', { text })]))));
    },
  };
}

// What the radar did about the picked deploy, step by step.
export function ledgerPanel(host, ctx) {
  host.classList.add('p-ledger');
  return {
    render(view) {
      const c = view.kase;
      const head = heading(ctx, 'What the radar did');
      if (!c) { mount(host, head, noDeploy(view)); return; }
      if (!c.actions || !c.actions.length) { mount(host, head, emptyNote('The radar has not acted on this deploy.')); return; }
      mount(host, head, h('ol', { class: 'ledger' }, c.actions.map((a) => h('li', null,
        h('span', { class: 'feed-time', text: clock(Date.parse(a.at), true) }),
        h('strong', { text: a.action }), h('span', { text: a.result }), a.detail ? h('span', { class: 'muted', text: a.detail }) : null))));
    },
  };
}
