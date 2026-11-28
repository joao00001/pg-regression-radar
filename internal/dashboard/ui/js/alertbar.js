// The alerts, drawn at the top right of the page. The rules are in alerts.js;
// this only shows them and passes on what the person does.
import { h } from './dom.js';
import { clock } from './model.js';
import { glyph, reasonTag } from './panels/common.js';
import { dismissKey } from './alerts.js';

// More than this is a wall; the rest is one click away. A phone has room for one.
const narrow = typeof matchMedia === 'function' ? matchMedia('(max-width: 560px)') : null;
const shownMax = () => (narrow && narrow.matches ? 1 : 3);

export function createAlertBar({ el, alerts, hrefFor }) {
  let expanded = false, drawn = '', announced = new Set(), first = true, drawnKeys = new Set();
  const live = h('div', { class: 'sr-only', role: 'status', 'aria-live': 'polite' });
  const list = h('div', { class: 'alert-list' });
  el.append(live, list);

  // Under the sticky header, wherever it is tall enough to reach (the edit bar adds a row).
  const header = document.querySelector('header.top');
  function place() {
    const top = header ? Math.max(0, header.getBoundingClientRect().bottom) + 12 : 12;
    el.style.setProperty('--alerts-top', `${Math.round(top)}px`);
  }
  window.addEventListener('resize', place);
  if (narrow && narrow.addEventListener) narrow.addEventListener('change', () => draw(true));
  if (header && typeof ResizeObserver === 'function') new ResizeObserver(place).observe(header);

  function card(a, fresh) {
    const critical = a.level === 'critical';
    const key = dismissKey(a);
    return h('article', { class: `alert ${critical ? 'alert-critical' : 'alert-warn'}${fresh ? ' alert-new' : ''}`, 'data-key': key, 'aria-label': `${a.title}: ${a.app}` },
      h('span', { class: `alert-mark tone-${critical ? 'regressed' : 'acted'}` }, glyph(critical ? 'bad' : 'regressed')),
      h('div', { class: 'alert-body' },
        h('p', { class: 'alert-title', text: a.title }),
        h('p', { class: 'alert-what' }, h('strong', { text: a.app }), ' ', h('span', { class: 'muted', text: a.namespace })),
        a.text ? h('p', { class: 'alert-text', text: a.text }) : null,
        a.missing ? h('p', { class: 'alert-missing', text: 'The manager no longer reports this. It may have restarted, which clears its history. Dismiss it once you have checked.' }) : null,
        h('p', { class: 'alert-meta' },
          a.at ? h('span', { class: 'muted', text: `since ${clock(a.at)}` }) : null,
          a.reason ? reasonTag(a.reason, a.docs) : null,
          a.deployId ? h('a', { class: 'alert-open', href: hrefFor(a.deployId), text: 'Open the case' }) : null)),
      h('button', { class: 'alert-x', type: 'button', 'aria-label': `Dismiss: ${a.title}, ${a.app}`, title: 'Dismiss', onclick: () => { alerts.dismiss(key); draw(true); } }, '×'));
  }

  function draw(force) {
    const all = alerts.visible();
    const sig = JSON.stringify([all.map((a) => [dismissKey(a), a.title, a.text, a.missing, a.reason, a.at]), expanded]);
    if (!force && sig === drawn) return;
    drawn = sig;
    const hadFocus = list.contains(document.activeElement);
    const shown = expanded ? all : all.slice(0, shownMax());
    const rest = all.length - shown.length;
    list.replaceChildren(...[
      ...shown.map((a) => card(a, !first && !drawnKeys.has(dismissKey(a)))),
      rest > 0 ? h('button', { class: 'alert-more', type: 'button', onclick: () => { expanded = true; draw(true); }, text: `${rest} more alert${rest === 1 ? '' : 's'}` }) : null,
      all.length > 1 ? h('div', { class: 'alert-tools' },
        expanded && all.length > shownMax() ? h('button', { class: 'alert-link', type: 'button', onclick: () => { expanded = false; draw(true); }, text: 'Show fewer' }) : null,
        h('button', { class: 'alert-link', type: 'button', onclick: () => { alerts.dismissAll(); draw(true); }, text: 'Dismiss all' })) : null,
    ].filter(Boolean)); // replaceChildren turns a null into the text "null"
    drawnKeys = new Set(shown.map(dismissKey));
    el.hidden = all.length === 0;
    place();
    // A card that held the keyboard focus is gone; keep the person in the list rather than at the top of the page.
    if (hadFocus && !list.contains(document.activeElement)) {
      const next = list.querySelector('.alert-x, .alert-more, .alert-link');
      if (next) next.focus();
    }
  }

  // Say what changed, once, for a screen reader; what was already there on load is not news.
  function announce(resolved) {
    const said = [];
    for (const a of alerts.visible()) {
      const k = dismissKey(a);
      if (!announced.has(k)) { announced.add(k); if (!first) said.push(`${a.title}: ${a.app} in ${a.namespace}.`); }
    }
    for (const r of resolved) {
      said.push(r.kind === 'breaker' ? `The breaker of ${r.app} in ${r.namespace} is closed. Its alert was removed.` : `${r.app} in ${r.namespace} is healthy again. Its alert was removed.`);
    }
    if (first) {
      const n = alerts.visible().length;
      if (n) said.push(`${n} alert${n === 1 ? '' : 's'} need${n === 1 ? 's' : ''} attention.`);
    }
    first = false;
    if (said.length) live.textContent = said.join(' ');
  }

  return {
    // Read the newest state and draw it.
    update(input) {
      const { resolved } = alerts.update(input);
      draw(false);
      announce(resolved);
    },
  };
}
