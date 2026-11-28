// The alerts, drawn at the top right of the page. The rules are in alerts.js;
// this only shows them and passes on what the person does.
//
// Each alert is one card, kept between redraws and found again by its key, so that a
// card can be animated out while the others stay where they are. When the trouble is
// over (the deploy is healthy again) the card says so before it goes: its colour moves
// from red to the healthy teal, its words blur into the new ones, its shape turns into
// the healthy circle, and then it slides away while the stack closes up underneath.
import { h, s } from './dom.js';
import { clock } from './model.js';
import { glyph, reasonTag } from './panels/common.js';
import { dismissKey } from './alerts.js';

// More than this is a wall; the rest is one click away. A phone has room for one.
const narrow = typeof matchMedia === 'function' ? matchMedia('(max-width: 560px)') : null;
const calm = typeof matchMedia === 'function' ? matchMedia('(prefers-reduced-motion: reduce)') : null;
const still = () => !!(calm && calm.matches);

// The beats of the resolution, in ms. Once the new words are in, they stay for HOLD so they
// can be read, and for longer while the pointer or the keyboard is on the card.
const T = { hold: 1500, exit: 560, collapse: 440, collapseAt: 290, afterHeld: 700 };
const EASE_OUT = 'cubic-bezier(0.22, 1, 0.36, 1)';
const EASE_IN = 'cubic-bezier(0.4, 0, 0.8, 0.5)';
const EASE_IO = 'cubic-bezier(0.65, 0, 0.35, 1)';
const SPRING = 'cubic-bezier(0.34, 1.56, 0.64, 1)';

// What a resolved alert says about itself.
function healed(a) {
  return a.kind === 'breaker'
    ? { kind: 'Resolved', title: 'The radar acts by itself again', text: 'The circuit breaker is closed.' }
    : { kind: 'Healthy', title: `${a.app} is healthy again`, text: 'The engine now reads this deploy as healthy.' };
}

// Text that can be swapped for other text with a blur: both layers sit in the same cell, so
// nothing moves while one dissolves into the other.
const swap = (text, cls) => h('span', { class: `swap ${cls || ''}`.trim() }, h('span', { class: 'swap-old', text }), h('span', { class: 'swap-new', 'aria-hidden': 'true' }));

const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const done = (anim) => (anim && anim.finished ? anim.finished.catch(() => {}) : Promise.resolve());

export function createAlertBar({ el, alerts, hrefFor, settings, sound }) {
  // What the person chose (see settings.js): how many cards, which alerts, what happens
  // when one clears by itself. A phone has room for one card whatever is chosen.
  const policy = () => settings.get();
  const shownMax = () => (narrow && narrow.matches ? 1 : policy().max);
  const visible = () => { const all = alerts.visible(); return policy().level === 'critical' ? all.filter((a) => a.level === 'critical') : all; };
  const ring = (kind) => { if (sound) sound.play(kind); };
  let expanded = false, first = true, resolvedNow = new Map();
  const announced = new Set();
  const cards = new Map(); // key -> { slot, article, sig, leaving, kept, held, skip, release, freed }
  const live = h('div', { class: 'sr-only', role: 'status', 'aria-live': 'polite' });
  const tools = h('div', { class: 'alert-tools' });
  const list = h('div', { class: 'alert-list' }, tools);
  const scroll = h('div', { class: 'alert-scroll' }, list);
  // The backdrop behind the cards: a stack of blurs, each wider and fainter than the
  // one above it, so the page loses focus gradually towards the cards instead of at an edge.
  const veil = h('div', { class: 'alerts-veil', 'aria-hidden': 'true' }, [0, 1, 2, 3, 4, 5].map((n) => {
    const layer = h('i');
    layer.style.setProperty('--n', String(n));
    return layer;
  }));
  el.append(live, veil, scroll);

  // Under the sticky header, wherever it is tall enough to reach (the edit bar adds a row).
  const header = document.querySelector('header.top');
  function place() {
    // The blur reaches --veil-t above the cards; they start that far below the header so the
    // blur begins at the header's edge and never touches the header itself.
    const reach = parseFloat(getComputedStyle(el).getPropertyValue('--veil-t')) || 0;
    const top = (header ? Math.max(0, header.getBoundingClientRect().bottom) : 0) + reach;
    el.style.setProperty('--alerts-top', `${Math.round(top)}px`);
  }
  window.addEventListener('resize', place);
  if (narrow && narrow.addEventListener) narrow.addEventListener('change', () => draw());
  if (header && typeof ResizeObserver === 'function') new ResizeObserver(place).observe(header);

  // ---- one card -------------------------------------------------------------------------

  const sigOf = (a) => JSON.stringify([a.title, a.text, a.missing, a.reason, a.at, a.level]);

  // Severity is a word above the title, in the colour of the shape in front of it.
  function build(a, fresh, c) {
    const critical = a.level === 'critical';
    const key = dismissKey(a);
    const href = a.deployId ? hrefFor(a.deployId) : '';
    const article = h('article', { class: `alert ${critical ? 'alert-critical' : 'alert-warn'}${fresh ? ' alert-new' : ''}`, 'data-key': key },
      h('div', { class: 'alert-main' },
        h('div', { class: 'alert-head alert-kicker' },
          h('span', { class: 'alert-kind' },
            h('span', { class: 'alert-mark', 'aria-hidden': 'true' },
              h('span', { class: 'mark-old' }, glyph(critical ? 'bad' : 'regressed')),
              h('span', { class: 'mark-new' }, glyph('ok'))),
            swap(critical ? 'Critical' : 'Needs attention', 'kind-swap')),
          a.at ? h('time', { class: 'alert-time', 'data-at': a.at, datetime: new Date(a.at).toISOString(), text: clock(a.at) }) : null),
        h('p', { class: 'alert-title' }, href ? h('a', { class: 'alert-hit', href }, swap(a.title)) : swap(a.title)),
        h('p', { class: 'alert-sub' }, h('b', { text: a.app }), ` in ${a.namespace}`),
        a.text ? h('p', { class: 'alert-text' }, swap(a.text)) : null,
        a.missing ? h('p', { class: 'alert-missing', text: 'The manager no longer reports this. It may have restarted. Dismiss it once you have checked.' }) : null,
        (a.reason || href) ? h('div', { class: 'alert-extras' },
          a.reason ? reasonTag(a.reason, a.docs) : null,
          href ? h('span', { class: 'alert-go', 'aria-hidden': 'true', text: 'Open case ›' }) : null) : null),
      h('button', {
        class: 'alert-x', type: 'button', 'aria-label': `Dismiss: ${a.title}, ${a.app}`, title: 'Dismiss',
        onclick: () => {
          if (c.kept) { c.kept = false; leave(key, c, null, 0); return; } // a healed card the person asked to keep
          if (c.leaving) { if (c.release) c.release(); return; } // already on its way out: let it go now
          alerts.dismiss(key); draw();
        },
      }, s('svg', { viewBox: '0 0 10 10', 'aria-hidden': 'true' }, s('path', { d: 'M2 2l6 6M8 2l-6 6' }))));
    // The pointer or the keyboard on a card keeps it from going while someone is reading it.
    article.addEventListener('pointerenter', () => { c.held = true; });
    article.addEventListener('pointerleave', () => { c.held = false; if (c.freed) c.freed(); });
    article.addEventListener('focusin', () => { c.held = true; });
    article.addEventListener('focusout', () => { c.held = false; if (c.freed) c.freed(); });
    return article;
  }

  function make(a, fresh) {
    const c = { slot: h('div', { class: 'alert-slot' }), article: null, sig: sigOf(a), leaving: false, kept: false, held: false, skip: false, release: null, freed: null };
    c.article = build(a, fresh, c);
    c.slot.append(c.article);
    return c;
  }

  // ---- reconcile ------------------------------------------------------------------------

  function draw(silent) {
    const all = visible();
    const shown = expanded ? all : all.slice(0, shownMax());
    const rest = all.length - shown.length;
    const want = new Map(shown.map((a) => [dismissKey(a), a]));

    // Cards that are no longer wanted leave: healed ones with the resolution, the rest quietly.
    let n = 0;
    for (const [key, c] of cards) {
      if (want.has(key) || c.leaving || c.kept) continue;
      const r = resolvedNow.get(key);
      leave(key, c, r || null, r ? 0 : n++ * 40);
    }
    resolvedNow = new Map();

    // Cards that are wanted: made, refreshed if their words changed, and put in order.
    let prev = null;
    for (const a of shown) {
      const key = dismissKey(a);
      let c = cards.get(key);
      if (c && (c.leaving || c.kept)) { c.slot.remove(); cards.delete(key); c = null; } // it came back before it went
      if (!c) { c = make(a, !first); cards.set(key, c); if (!first && !silent) ring(a.level === 'critical' ? 'critical' : 'alert'); }
      else if (c.sig !== sigOf(a)) {
        c.sig = sigOf(a);
        const next = build(a, false, c);
        c.article.replaceWith(next); c.article = next;
      }
      // Leaving cards stay where they are; the wanted ones are ordered around them.
      let ref = prev ? prev.nextSibling : list.firstChild;
      while (ref && ref !== c.slot && ref.classList && ref.classList.contains('is-leaving')) ref = ref.nextSibling;
      if (ref !== c.slot) list.insertBefore(c.slot, ref || tools);
      prev = c.slot;
    }

    // One quiet row under the stack, like "Show more" and "Clear all" in Notification Center.
    setTools([
      rest > 0 ? h('button', { class: 'alert-more', type: 'button', onclick: () => { expanded = true; draw(); }, text: `${rest} more` }) : null,
      expanded && all.length > shownMax() ? h('button', { class: 'alert-link', type: 'button', onclick: () => { expanded = false; draw(); }, text: 'Show fewer' }) : null,
      all.length > 1 ? h('button', { class: 'alert-link', type: 'button', onclick: clearAll, text: 'Clear all' }) : null,
    ].filter(Boolean)); // replaceChildren turns a null into the text "null"
    if (shown.length) unfadeVeil();
    settle();
    place();
  }

  function clearAll() {
    for (const a of visible()) alerts.dismiss(dismissKey(a));
    for (const [key, c] of cards) if (c.kept) { c.kept = false; leave(key, c, null, 0); }
    draw();
  }

  // The row under the stack goes away softly when it is no longer needed, not in one frame.
  let toolsGen = 0;
  function setTools(items) {
    const gen = ++toolsGen;
    if (!items.length && tools.firstChild && !still()) {
      Promise.all([...tools.children].map((b) => done(b.animate([{ opacity: 1, filter: 'blur(0px)' }, { opacity: 0, filter: 'blur(4px)' }], { duration: 240, easing: 'ease-in', fill: 'forwards' }))))
        .then(() => { if (gen === toolsGen) { tools.replaceChildren(); tools.hidden = true; } });
      return;
    }
    tools.replaceChildren(...items);
    tools.hidden = !items.length;
  }

  // The blur behind the cards fades with the last card, rather than being switched off after it.
  // Each layer fades on its own: a parent with opacity would cut the layers off from the page.
  let veilFade = [];
  function fadeVeil() {
    if (veilFade.length || still()) return;
    veilFade = [...veil.children].map((i) => i.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 620, delay: 60, easing: 'ease-in-out', fill: 'forwards' }));
  }
  function unfadeVeil() { veilFade.forEach((a) => a.cancel()); veilFade = []; }

  // The panel is only there while there is something in it, a card on its way out included.
  function settle() {
    const empty = cards.size === 0 && visible().length === 0;
    el.hidden = empty;
    if (empty) unfadeVeil(); // hidden now; ready at full strength for the next alert
  }

  // ---- leaving --------------------------------------------------------------------------

  async function leave(key, c, resolved, delay) {
    c.leaving = true;
    c.slot.classList.add('is-leaving');
    let free;
    const gone = new Promise((r) => { free = r; });
    c.release = () => { c.skip = true; free(); if (c.freed) c.freed(); };
    const alive = () => cards.get(key) === c && c.leaving; // a relapse puts a new card in its place
    if (delay) await wait(delay);
    if (!alive()) return;

    // What the person chose for an alert that clears by itself: say so and then go (the
    // default), go quietly, or stay, healed, until they dismiss it.
    const mode = resolved ? policy().resolved : 'quiet';
    if (resolved && mode !== 'quiet') {
      if (still()) { applyHealed(c, resolved); ring('healthy'); if (mode === 'say') await Promise.race([wait(T.hold + 500), gone]); }
      else { setTimeout(() => ring('healthy'), 200); await resolveMotion(c, resolved); if (mode === 'say') await Promise.race([wait(T.hold), gone]); }
      if (!alive()) return;
      if (mode === 'keep') { c.leaving = false; c.kept = true; c.release = null; c.slot.classList.remove('is-leaving'); return; }
      await heldUntilFree(c, gone);
      if (!alive()) return;
    } else if (resolved) ring('healthy');
    if ([...cards.values()].every((x) => x.leaving) && !visible().length) fadeVeil();
    await exit(c, mode === 'quiet');
    if (!alive()) return;

    const hadFocus = c.slot.contains(document.activeElement);
    c.slot.remove(); cards.delete(key);
    if (hadFocus) { const next = list.querySelector('.alert-x, .alert-more, .alert-link'); if (next) next.focus(); }
    settle(); place();
  }

  // Wait while someone has the pointer or the keyboard on the card, and a moment after.
  function heldUntilFree(c, gone) {
    if (!c.held || c.skip) return Promise.resolve();
    return Promise.race([gone, new Promise((r) => {
      c.freed = () => { if (c.held && !c.skip) return; c.freed = null; if (c.skip) r(); else setTimeout(r, T.afterHeld); };
    })]);
  }

  // The card is swept to the right, blurring and fading, and only once it is mostly gone does
  // the space close up under it, so the stack follows it and nothing is ever cut off.
  async function exit(c, quick) {
    const slot = c.slot, art = c.article;
    if (still()) {
      await done(art.animate([{ opacity: 1 }, { opacity: 0 }], { duration: 200, fill: 'forwards' }));
      return;
    }
    const slide = art.animate([
      { opacity: 1, transform: 'translateX(0) scale(1)', filter: 'blur(0px)' },
      { opacity: 0.5, transform: `translateX(${quick ? 8 : 14}px) scale(0.985)`, filter: 'blur(3px)', offset: 0.42 },
      { opacity: 0, transform: `translateX(${quick ? 22 : 38}px) scale(0.95)`, filter: 'blur(10px)' },
    ], { duration: quick ? 300 : T.exit, easing: EASE_IN, fill: 'forwards' });
    await wait(quick ? 120 : T.collapseAt);
    const height = slot.getBoundingClientRect().height - parseFloat(getComputedStyle(slot).paddingTop || '0');
    const close = slot.animate([
      { height: `${height}px`, marginBottom: '0px' },
      { height: '0px', marginBottom: '-8px' }, // the grid's gap goes with it
    ], { duration: quick ? 260 : T.collapse, easing: EASE_IO, fill: 'forwards' });
    await Promise.all([done(slide), done(close)]);
  }

  // ---- the resolution -------------------------------------------------------------------

  // The healed words and tone, with no motion.
  function applyHealed(c, r) {
    const say = healed(r), art = c.article;
    art.dataset.tone = 'ok';
    const set = (sel, text) => {
      const next = art.querySelector(`${sel} .swap-new`), old = art.querySelector(`${sel} .swap-old`);
      if (!next || !old) return;
      next.textContent = text; next.style.opacity = '1'; old.style.display = 'none';
    };
    set('.kind-swap', say.kind); set('.alert-title', say.title); set('.alert-text', say.text);
    art.querySelectorAll('.alert-extras .reason').forEach((x) => { x.style.visibility = 'hidden'; });
    art.querySelector('.mark-old').style.display = 'none';
    art.querySelector('.mark-new').style.opacity = '1';
  }

  // Colour, words and shape change together, in a short cascade from the top line down.
  async function resolveMotion(c, r) {
    const say = healed(r), art = c.article;
    const before = art.getBoundingClientRect().height;
    const olds = [...art.querySelectorAll('.swap-old')];
    art.dataset.tone = 'ok'; // the red (or amber) moves to the healthy teal through CSS transitions

    // Words: the old ones dissolve into a blur as the new ones come into focus.
    const blurSwap = (sel, text, delay) => {
      const old = art.querySelector(`${sel} .swap-old`), next = art.querySelector(`${sel} .swap-new`);
      if (!old || !next) return [];
      next.textContent = text;
      return [
        old.animate([{ opacity: 1, filter: 'blur(0px)', transform: 'translateY(0)' }, { opacity: 0, filter: 'blur(7px)', transform: 'translateY(-3px)' }], { delay, duration: 320, easing: 'ease-in', fill: 'both' }),
        next.animate([{ opacity: 0, filter: 'blur(7px)', transform: 'translateY(3px)' }, { opacity: 1, filter: 'blur(0px)', transform: 'translateY(0)' }], { delay: delay + 110, duration: 460, easing: EASE_OUT, fill: 'both' }),
      ];
    };
    const anims = [
      ...blurSwap('.kind-swap', say.kind, 0),
      ...blurSwap('.alert-title', say.title, 90),
      ...blurSwap('.alert-text', say.text, 180),
    ];

    // Shape: the diamond (or triangle) turns away and the healthy circle springs in.
    anims.push(
      art.querySelector('.mark-old').animate([{ opacity: 1, transform: 'rotate(0) scale(1)', filter: 'blur(0px)' }, { opacity: 0, transform: 'rotate(90deg) scale(0.4)', filter: 'blur(3px)' }], { duration: 300, delay: 40, easing: 'ease-in', fill: 'both' }),
      art.querySelector('.mark-new').animate([{ opacity: 0, transform: 'rotate(-90deg) scale(0.3)', filter: 'blur(3px)' }, { opacity: 1, transform: 'rotate(0) scale(1)', filter: 'blur(0px)' }], { duration: 520, delay: 160, easing: SPRING, fill: 'both' }),
    );

    // The reason code no longer applies: it dissolves too.
    art.querySelectorAll('.alert-extras .reason').forEach((x) => anims.push(x.animate([{ opacity: 1, filter: 'blur(0px)' }, { opacity: 0, filter: 'blur(4px)' }], { duration: 300, delay: 100, fill: 'both' })));

    // The new words may need less (or more) room than the old ones. Measure the height they
    // will have, then let the card grow or shrink to it while the words change, so the height
    // moves as one with everything else instead of jumping at the end.
    olds.forEach((o) => { o.style.display = 'none'; });
    const after = art.getBoundingClientRect().height;
    olds.forEach((o) => { o.style.display = ''; });
    let resize = null;
    if (Math.abs(before - after) > 1) {
      // No clipping here: overflow:hidden on a frosted card squares off its corners and drops
      // the backdrop blur for a moment. The old words are gone before the card gets smaller.
      resize = art.animate([{ height: `${before}px` }, { height: `${after}px` }], { duration: 560, delay: 300, easing: EASE_IO, fill: 'both' });
    }

    await Promise.all([...anims, resize].map(done));
    // Keep the end state without the animations, so nothing is left running.
    anims.forEach((a) => { try { a.commitStyles(); a.cancel(); } catch (_) { /* a removed node has nothing to commit */ } });
    olds.forEach((o) => { o.style.display = 'none'; });
    if (resize) resize.cancel();
  }

  // ---- announcing -----------------------------------------------------------------------

  // Say what changed, once, for a screen reader; what was already there on load is not news.
  function announce(resolved) {
    const said = [];
    for (const a of visible()) {
      const k = dismissKey(a);
      if (!announced.has(k)) { announced.add(k); if (!first) said.push(`${a.title}: ${a.app} in ${a.namespace}.`); }
    }
    for (const r of resolved) {
      said.push(r.kind === 'breaker' ? `The breaker of ${r.app} in ${r.namespace} is closed. Its alert was removed.` : `${r.app} in ${r.namespace} is healthy again. Its alert was removed.`);
    }
    if (first) {
      const n = visible().length;
      if (n) said.push(`${n} alert${n === 1 ? '' : 's'} need${n === 1 ? 's' : ''} attention.`);
    }
    first = false;
    if (said.length) live.textContent = said.join(' ');
  }

  // A choice that changes what is shown redraws at once, without a sound: nothing new happened.
  settings.subscribe((now, before) => {
    if (now.dismissal !== before.dismissal) alerts.remembering(now.dismissal === 'persist');
    // The cards on screen are kept, not drawn again, so their clock times are told the new zone.
    if (now.timezone !== before.timezone) for (const t of el.querySelectorAll('.alert-time[data-at]')) t.textContent = clock(Number(t.dataset.at));
    if (now.level !== before.level || now.max !== before.max || now.timezone !== before.timezone) draw(true);
  });

  return {
    // Read the newest state and draw it.
    update(input) {
      const { resolved } = alerts.update(input);
      resolvedNow = new Map(resolved.map((r) => [dismissKey(r), r]));
      draw();
      announce(resolved);
    },
  };
}
