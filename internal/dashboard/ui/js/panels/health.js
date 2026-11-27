import { h } from '../dom.js';
import { sourceRows, safetyRows, relative, clock } from '../model.js';
import { glyph, heading, emptyNote, mount, reasonTag } from './common.js';

const WORD = { ok: 'ok', info: 'off', warn: 'attention', bad: 'problem' };

// Whether each source the radar reads is answering, how recent its data is and
// what is missing. A verdict is only as good as the data under it.
export function sourceHealthPanel(host, ctx) {
  host.classList.add('p-sources');
  return {
    render(view) {
      const rows = sourceRows(view.overview, view.now);
      const head = heading(ctx, 'Source health');
      if (!rows.length) { mount(host, head, emptyNote('No PostgresWatch is running on this manager, so there is nothing to read from.')); return; }
      mount(host, head, h('div', { class: 'table-scroll' }, h('table', { class: 'sources' },
        h('thead', null, h('tr', null, h('th', { text: 'Source' }), h('th', { text: 'Watch' }), h('th', { text: 'State' }), h('th', { text: 'What it means' }), h('th', { class: 'tn', text: 'Newest data' }))),
        h('tbody', null, rows.map((r) => h('tr', { class: `tone-${r.tone}` },
          h('td', { text: r.source }),
          h('td', null, r.watch ? h('span', { class: 'ns', text: r.watch }) : null),
          h('td', null, h('span', { class: 'state' }, glyph(r.tone), r.value, h('span', { class: 'sr-only', text: ` (${WORD[r.tone]})` }))),
          h('td', { class: 'muted' }, r.message || r.detail, r.reason ? [' ', reasonTag(r.reason, r.docs)] : null),
          h('td', { class: 'tn' }, r.at ? h('span', { 'data-rel': r.at, 'data-fmt': 'plain', title: clock(r.at, true), text: relative(r.at, view.now) }) : '–')))))));
    },
  };
}

// What the radar may do here: whether it can act, in which namespaces, and whether
// the breaker has stopped it.
export function safetyPanel(host, ctx) {
  host.classList.add('p-safety');
  return {
    render(view) {
      const rows = safetyRows(view.overview);
      const head = heading(ctx, 'Safety');
      if (!rows.length) { mount(host, head, emptyNote('No PostgresWatch is running on this manager.')); return; }
      const sc = (view.overview && view.overview.scope) || {};
      mount(host, head,
        h('ul', { class: 'safety' }, rows.map((r) => h('li', null,
          h('div', { class: 'safety-name' }, h('strong', { text: r.name }), h('span', { class: 'ns', text: r.namespace })),
          h('dl', null,
            h('div', null, h('dt', { text: 'Mode' }), h('dd', { text: r.mode }), h('dd', { class: 'muted', text: r.mode === 'enforce' ? 'Decisions can pause or abort a rollout.' : 'Judging and logging only; nothing is paused or aborted.' })),
            h('div', { class: `tone-${r.actingTone}` }, h('dt', { text: 'May act' }), h('dd', null, glyph(r.actingTone), r.acting), h('dd', { class: 'muted' }, r.actingMessage || r.actingDetail, r.actingReason ? [' ', reasonTag(r.actingReason, r.actingDocs)] : null)),
            h('div', { class: `tone-${r.breakerTone}` }, h('dt', { text: 'Breaker' }), h('dd', null, glyph(r.breakerTone), r.breaker), h('dd', { class: 'muted' }, r.breakerMessage || r.breakerDetail, r.breakerReason ? [' ', reasonTag(r.breakerReason, r.breakerDocs)] : null)),
            h('div', null, h('dt', { text: 'Acts in' }), h('dd', { text: r.namespaces.length ? r.namespaces.join(', ') : 'any namespace it watches' }))))),
        ),
        h('p', { class: 'fig' }, h('b', { text: 'Visible here' }), sc.all ? 'Every namespace the manager sees.' : `Only ${(sc.namespaces || []).join(', ') || 'nothing'}: the manager's allowlist applies to everything on this page.`));
    },
  };
}
