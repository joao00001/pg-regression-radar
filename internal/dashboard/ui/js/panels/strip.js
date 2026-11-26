import { h } from '../dom.js';
import { statusItems } from '../model.js';
import { glyph } from './common.js';

const WORD = { ok: 'ok', info: '', warn: 'attention', bad: 'problem' };

// The state of the radar as one quiet line of small text. Only a thing that needs
// a person is pulled out below it, in the open, with its explanation.
export function radarStatePanel(host) {
  host.classList.add('p-state');
  return {
    render(view) {
      // The summary already says whether the data is recorded; say it once.
      const items = statusItems(view.overview, view.config, view.now).filter((i) => i.id !== 'origin');
      const row = h('ul', { class: 'state-row' },
        items.map((i) => h('li', { class: `tone-${i.tone}`, title: i.detail, tabindex: '0' },
          (i.tone === 'warn' || i.tone === 'bad') ? glyph(i.tone) : null,
          `${i.label} `, h('b', { text: i.value }),
          WORD[i.tone] ? h('span', { class: 'sr-only', text: ` (${WORD[i.tone]})` }) : null)));
      const attention = items.filter((i) => i.tone === 'warn' || i.tone === 'bad');
      const kids = [row];
      if (attention.length) {
        kids.push(h('ul', { class: 'attention' }, attention.map((i) => h('li', { class: `tone-${i.tone}` },
          glyph(i.tone), h('span', null, h('strong', { text: `${i.label}: ${i.value}. ` }), i.detail)))));
      }
      host.replaceChildren(...kids);
    },
  };
}
