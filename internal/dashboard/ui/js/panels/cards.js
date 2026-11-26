import { h } from '../dom.js';
import { relative, clock, gradeOf, tone, stateLabel, pickDeploys } from '../model.js';
import { gradeBadge, stateTag, reconcile, glyph } from './common.js';

function card(d, nowMs, onSelect) {
  const next = d.nextEvaluation ? Date.parse(d.nextEvaluation) : null;
  return h('li', { class: 'card-li' }, h('button', {
    class: `card tone-${tone(d)}`, type: 'button', onclick: () => onSelect(d.id),
  },
  h('span', { class: 'card-top' },
    h('span', { class: 'card-name' }, h('strong', { text: d.app }), h('span', { class: 'ns', text: d.namespace })),
    gradeBadge(gradeOf(d), d.latest && d.latest.gradeNote, false)),
  h('span', { class: 'card-state' }, stateTag(d)),
  h('span', { class: 'sentence', text: d.sentence }),
  h('span', { class: 'meta' },
    h('span', { 'data-rel': Date.parse(d.at), 'data-fmt': 'deployed', text: `deployed ${relative(Date.parse(d.at), nowMs)}`, title: clock(Date.parse(d.at)) }),
    next ? h('span', { 'data-rel': next, 'data-fmt': 'next', text: `next check ${relative(next, nowMs)}` }) : null)));
}

// The same deploy as one line: its state, name, age and grade.
function row(d, nowMs, onSelect) {
  const at = Date.parse(d.at);
  return h('li', { class: 'card-li' }, h('button', { class: `card row tone-${tone(d)}`, type: 'button', title: d.sentence, onclick: () => onSelect(d.id) },
    glyph(tone(d)),
    h('span', { class: 'row-name' }, h('strong', { text: d.app }), h('span', { class: 'ns', text: d.namespace })),
    h('span', { class: 'row-state', text: stateLabel(d) }),
    h('span', { class: 'row-when', 'data-rel': at, 'data-fmt': 'plain', title: clock(at), text: relative(at, nowMs) }),
    gradeBadge(gradeOf(d), d.latest && d.latest.gradeNote, false)));
}

// The red rule beside the selected card. It slides from card to card; the first
// time it appears it is placed without travelling from the top.
function placeIndicator(ind, ul, selectedId) {
  const li = [...ul.children].find((n) => n.dataset.key === selectedId);
  if (!li) { ind.style.opacity = '0'; return; }
  const first = !ind.dataset.placed;
  if (first) ind.style.transition = 'none';
  ind.style.height = `${li.offsetHeight}px`;
  ind.style.transform = `translate(${li.offsetLeft}px, ${li.offsetTop}px)`;
  ind.style.opacity = '1';
  if (first) { void ind.offsetWidth; ind.style.transition = ''; ind.dataset.placed = '1'; }
}

export function deployCardsPanel(host, ctx) {
  host.classList.add('p-cards');
  host.classList.toggle('as-list', ctx.options.viz === 'list');
  const count = h('span', { class: 'count' });
  const head = h('h3', { class: 'ph' }, ctx.explicitTitle || 'Deploys', count);
  const ind = h('span', { class: 'ind', 'aria-hidden': 'true' });
  const ul = h('ul');
  host.append(head, h('div', { class: 'scroll' }, ind, ul));
  let last = null;
  if (typeof ResizeObserver !== 'undefined') new ResizeObserver(() => last && placeIndicator(ind, ul, last)).observe(host);
  if (document.fonts && document.fonts.ready) document.fonts.ready.then(() => last && placeIndicator(ind, ul, last));

  return {
    render(view) {
      const { list: deploys, matching, total } = pickDeploys(view.deploys, ctx.options);
      count.textContent = total > deploys.length ? `${deploys.length} of ${total}` : String(deploys.length);
      if (!deploys.length) {
        ul.replaceChildren(h('li', { class: 'empty' }, total
          ? 'No deploy matches the filter of this panel. Change "Show" in its settings.'
          : 'No deploys in this time range. The radar judges a deploy about 10 minutes after it is registered; widen the range or wait for the next one.'));
        return;
      }
      void matching;
      const make = ctx.options.viz === 'list' ? row : card;
      reconcile(ul, deploys, { key: (d) => d.id, sig: (d) => JSON.stringify(d), build: (d) => make(d, view.now, ctx.actions.select) });
      for (const li of ul.children) li.firstElementChild.setAttribute('aria-pressed', String(li.dataset.key === view.selected));
      last = view.selected;
      placeIndicator(ind, ul, last);
    },
  };
}
