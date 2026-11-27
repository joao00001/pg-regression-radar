import { h, s } from '../dom.js';
import { gradeLevel, tone, stateLabel } from '../model.js';

// The stable reason code of a log line or a condition, as a link to the docs page
// that says what it means and how to fix it. Without a link it is plain text, so
// the code can still be searched for in the manager log.
export function reasonTag(code, docs) {
  if (!code) return null;
  return docs
    ? h('a', { class: 'reason', href: docs, target: '_blank', rel: 'noopener noreferrer', title: 'What this means and how to fix it', text: code })
    : h('span', { class: 'reason', text: code });
}

// Tone as a shape as well as a colour, drawn here so no font has to have it.
const SHAPES = {
  watching: () => s('circle', { class: 'hollow', cx: 4.5, cy: 4.5, r: 3.6 }),
  ok: () => s('circle', { cx: 4.5, cy: 4.5, r: 4 }),
  regressed: () => s('polygon', { points: '4.5,0.5 8.5,8 0.5,8' }),
  acted: () => s('rect', { x: 0.8, y: 0.8, width: 7.4, height: 7.4 }),
  quiet: () => s('polygon', { class: 'hollow', points: '4.5,0.6 8.4,4.5 4.5,8.4 0.6,4.5' }),
  info: () => s('circle', { class: 'hollow', cx: 4.5, cy: 4.5, r: 3.6 }),
  warn: () => s('polygon', { points: '4.5,0.5 8.5,8 0.5,8' }),
  bad: () => s('polygon', { points: '4.5,0.4 8.6,4.5 4.5,8.6 0.4,4.5' }),
};
export const glyph = (t) => s('svg', { class: 'glyph', viewBox: '0 0 9 9', 'aria-hidden': 'true' }, (SHAPES[t] || SHAPES.info)());

export function gbar(level) {
  return h('span', { class: 'gbar', 'aria-hidden': 'true' }, [0, 1, 2, 3].map((i) => {
    const el = h('i', { class: i < level ? 'on' : '' });
    el.style.setProperty('--i', String(i));
    return el;
  }));
}

// A grade is a letter and how strong that evidence is, out of four.
export function gradeBadge(grade, note, bar = true) {
  return h('span', { class: `grade grade-${grade || 'none'}`, title: note || (grade ? '' : 'Nothing regressed, so there is nothing to grade.') },
    h('b', { text: grade || '–' }), bar ? gbar(gradeLevel(grade)) : null,
    h('span', { class: 'sr-only', text: grade ? ` grade ${grade}` : ' no grade' }));
}

export function stateTag(d) {
  const t = tone(d);
  return h('span', { class: `state tone-${t}` }, glyph(t), stateLabel(d));
}

// Keeps a list's elements between renders. An item whose data did not change keeps
// its element (so a hover, a focus or a running animation survives a live refresh);
// a changed one is replaced and marked `changed`, a new one `fresh`, and the CSS
// pulses or slides them once. Elements the data no longer has are removed.
export function reconcile(parent, items, { key, sig, build }) {
  for (const c of [...parent.children]) if (c.dataset.key === undefined) c.remove();
  const old = new Map([...parent.children].map((n) => [n.dataset.key, n]));
  let ref = parent.firstElementChild;
  items.forEach((item, i) => {
    const k = key(item), sg = sig(item);
    let el = old.get(k);
    old.delete(k);
    if (el && el.dataset.sig === sg) {
      // unchanged
    } else {
      const n = build(item);
      n.dataset.key = k;
      n.dataset.sig = sg;
      n.style.setProperty('--i', String(Math.min(i, 12)));
      n.classList.add(el ? 'changed' : 'fresh');
      if (el) { if (el === ref) ref = el.nextElementSibling; el.replaceWith(n); }
      el = n;
    }
    if (el === ref) ref = ref.nextElementSibling;
    else parent.insertBefore(el, ref);
  });
  for (const n of old.values()) n.remove();
}

// A panel's small heading. A type with no heading of its own shows one only when
// the dashboard file gave the panel a title, so the page stays as quiet as it was.
export function heading(ctx, own, aside) {
  const text = ctx.explicitTitle || own;
  if (!text) return null;
  return h('h3', { class: 'ph' }, text, aside != null ? h('span', { class: 'count', text: String(aside) }) : null);
}

export const emptyNote = (text) => h('p', { class: 'empty', text });

let nextUid = 0;
export const uid = () => `p${++nextUid}`;

// Puts a panel's parts in its host, leaving out the ones it does not have.
export const mount = (host, ...kids) => host.replaceChildren(...kids.filter(Boolean));
