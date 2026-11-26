import { h } from '../dom.js';
import { countTo, replay } from '../motion.js';
import { summary as summarize } from '../model.js';
import { glyph } from './common.js';

const KINDS = [['regressed', 'regressed, no action'], ['acted', 'regressed, acted on'], ['watching', 'being judged'], ['ok', 'healthy']];

// The top of the board: one sentence that answers "is anything wrong?" (its last
// word in bold, as the docs set their titles), a thin bar with one segment per
// deploy, worst first, and the counts in small type.
export function summaryPanel(host, ctx) {
  host.classList.add('p-summary');
  const origin = h('span', { class: 'origin' });
  const lead = h('h1', { 'aria-live': 'polite' });
  const detail = h('p', { class: 'detail' });
  const bar = h('div', { class: 'segbar', role: 'img' });
  const counts = h('ul', { class: 'counts' }, KINDS.map(([k, label]) => h('li', { class: `tone-${k}`, 'data-k': k }, glyph(k), h('b', { text: '0' }), label)));
  const wantBar = ctx.options.bar !== false, wantCounts = ctx.options.counts !== false;
  bar.hidden = !wantBar;
  counts.hidden = !wantCounts;
  host.classList.toggle('no-bar', !wantBar && !wantCounts);
  host.append(h('div', { class: 'sum-text' }, origin, lead, detail), wantBar || wantCounts ? h('div', { class: 'sum-bar' }, bar, counts) : null);
  let leadSig = '', barSig = '';

  return {
    render(view) {
      const sum = summarize(view.deploys);
      const fixture = view.config && view.config.origin === 'fixture';
      origin.textContent = fixture ? 'Recorded sample, not a live cluster' : 'Live cluster';

      const sig = `${sum.lead}|${sum.detail}`;
      if (sig !== leadSig) {
        const first = leadSig === '';
        leadSig = sig;
        const i = sum.lead.lastIndexOf(' ');
        lead.replaceChildren(...(i < 0 ? [h('b', { text: sum.lead })] : [sum.lead.slice(0, i + 1), h('b', { text: sum.lead.slice(i + 1) })]));
        detail.textContent = sum.detail;
        if (!first) { replay(lead, 'swap'); replay(detail, 'swap'); }
      }
      for (const [k] of KINDS) {
        countTo(counts.querySelector(`[data-k="${k}"] b`), sum.counts[k], (v) => String(Math.round(v)), `${ctx.uid}:sum:${k}`);
      }
      const bs = JSON.stringify(sum.segs);
      if (bs !== barSig) {
        barSig = bs;
        bar.replaceChildren(...sum.segs.map((g, i) => {
          const seg = h('span', { class: `seg tone-${g.tone}` });
          seg.style.flexGrow = String(g.n);
          seg.style.setProperty('--i', String(i));
          return seg;
        }));
      }
      bar.setAttribute('aria-label', `${sum.total} ${sum.total === 1 ? 'deploy' : 'deploys'}: ${KINDS.map(([k, l]) => `${sum.counts[k]} ${l}`).join(', ')}`);
    },
  };
}
