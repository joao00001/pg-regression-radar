import { h } from '../dom.js';
import { clock, relative, feedEntries } from '../model.js';
import { reconcile, heading } from './common.js';

export function decisionFeedPanel(host, ctx) {
  host.classList.add('p-feed');
  const table = ctx.options.viz === 'table';
  const ol = h('ol');
  const grid = h('div', { class: 'table-scroll feed-table' });
  host.append(heading(ctx, 'Activity'), table ? grid : ol);
  let tableSig = '';
  return {
    render(view) {
      const entries = feedEntries([...view.cases.values()], ctx.options.limit, ctx.options.show);
      if (table) { drawTable(grid, entries, view.now, ctx, tableSig, (v) => { tableSig = v; }); return; }
      if (!entries.length) {
        ol.replaceChildren(h('li', { class: 'empty' }, ctx.options.show === 'all' ? 'Nothing has happened yet in this range.' : 'Nothing matches the filter of this panel.'));
        return;
      }
      reconcile(ol, entries, {
        key: (e) => `${e.deployId}|${e.kind}|${e.at}|${e.text}`,
        sig: (e) => JSON.stringify([e.bad, e.detail]),
        build: (e) => h('li', { class: `feed-item${e.bad ? ' bad' : ''} kind-${e.kind}` },
          h('span', { class: 'feed-time', 'data-rel': e.at, 'data-fmt': 'plain', title: clock(e.at, true), text: relative(e.at, view.now) }),
          h('span', { class: 'feed-body' },
            h('button', { class: 'link', type: 'button', onclick: () => ctx.actions.select(e.deployId), text: e.app }),
            h('span', { class: 'ns', text: ` ${e.namespace}` }),
            h('span', { class: 'feed-text', text: e.text }),
            e.detail ? h('span', { class: 'feed-detail', text: e.detail }) : null)),
      });
    },
  };
}

// The same entries as rows: when, which deploy, what happened.
function drawTable(el, entries, now, ctx, prev, remember) {
  const sig = JSON.stringify(entries.map((e) => [e.deployId, e.kind, e.at, e.text, e.detail]));
  if (sig === prev) return;
  remember(sig);
  if (!entries.length) { el.replaceChildren(h('p', { class: 'empty', text: ctx.options.show === 'all' ? 'Nothing has happened yet in this range.' : 'Nothing matches the filter of this panel.' })); return; }
  el.replaceChildren(h('table', { class: 'feedtab' },
    h('thead', null, h('tr', null, h('th', { text: 'When' }), h('th', { text: 'Deploy' }), h('th', { text: 'What happened' }), h('th', { text: 'Detail' }))),
    h('tbody', null, entries.map((e) => h('tr', { class: e.bad ? 'bad' : '' },
      h('td', { class: 'ev-time', 'data-rel': e.at, 'data-fmt': 'plain', title: clock(e.at, true), text: relative(e.at, now) }),
      h('td', null, h('button', { class: 'link', type: 'button', onclick: () => ctx.actions.select(e.deployId), text: e.app }), h('span', { class: 'ns', text: ` ${e.namespace}` })),
      h('td', { text: e.text }),
      h('td', { class: 'muted', text: e.detail || '' }))))));
}
