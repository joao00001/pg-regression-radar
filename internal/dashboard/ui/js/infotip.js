// The info button at the top right of a panel: a few sentences on what the panel
// shows and how to read it. Hover or keyboard focus shows it; a click pins it so it
// can be read at leisure, and Escape or a click elsewhere closes it. One popover
// serves every panel, and it is built with the same safe builders as the rest of
// the page (text only, never markup).
import { h, s } from './dom.js';

const GAP = 8;
const EDGE = 8;
let tip = null, shownFor = null, pinned = false, showTimer = 0, hideTimer = 0;

function ensure() {
  if (tip) return tip;
  tip = h('div', { class: 'infotip', id: 'infotip', role: 'tooltip', hidden: true });
  document.body.append(tip);
  document.addEventListener('pointerdown', (e) => {
    if (!pinned) return;
    if (tip.contains(e.target) || (shownFor && shownFor.contains(e.target))) return;
    hide();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !shownFor) return;
    const btn = shownFor;
    hide();
    if (btn.isConnected) btn.focus();
  });
  window.addEventListener('resize', () => { if (shownFor) position(shownFor); });
  // A tip that is only being hovered would be left behind by a scroll; a pinned one follows its button.
  window.addEventListener('scroll', () => { if (!shownFor) return; if (pinned) position(shownFor); else hide(); }, true);
  return tip;
}

// Where the tip goes: next to the info button but outside its own panel, so it never
// covers what it is describing. Below, above, to the right and to the left of the panel are
// tried; the one that overlaps the panel least wins, and the nearest of those. Only
// when the screen has no room at all does it settle over the panel's edge.
function position(btn) {
  const pr = (btn.closest('.panel') || btn).getBoundingClientRect();
  const br = btn.getBoundingClientRect();
  tip.style.setProperty('left', '0px');
  tip.style.setProperty('top', '0px');
  const w = tip.offsetWidth, hh = tip.offsetHeight;
  const vw = document.documentElement.clientWidth, vh = document.documentElement.clientHeight;
  const cx = (x) => Math.max(EDGE, Math.min(x, vw - w - EDGE));
  const cy = (y) => Math.max(EDGE, Math.min(y, vh - hh - EDGE));
  const overlap = (x, y) => Math.max(0, Math.min(x + w, pr.right) - Math.max(x, pr.left)) * Math.max(0, Math.min(y + hh, pr.bottom) - Math.max(y, pr.top));
  const tries = [
    [br.right - w, pr.bottom + GAP],  // below the panel, its right edge on the info button's
    [br.right - w, pr.top - GAP - hh], // above
    [pr.right + GAP, br.top],          // to the right
    [pr.left - GAP - w, br.top],       // to the left
  ].map(([x, y]) => {
    const px = cx(x), py = cy(y);
    const dist = Math.hypot(px + w / 2 - (br.left + br.width / 2), py + hh / 2 - (br.top + br.height / 2));
    return { x: px, y: py, score: overlap(px, py) * 1000 + dist };
  });
  const best = tries.reduce((m, t) => (t.score < m.score ? t : m));
  tip.style.setProperty('left', `${Math.round(best.x)}px`);
  tip.style.setProperty('top', `${Math.round(best.y)}px`);
}

function show(btn, content, pin) {
  clearTimeout(showTimer); clearTimeout(hideTimer);
  ensure();
  if (shownFor && shownFor !== btn) { shownFor.setAttribute('aria-expanded', 'false'); shownFor.removeAttribute('aria-describedby'); }
  shownFor = btn;
  pinned = pin;
  tip.replaceChildren(h('strong', { text: content.title }), h('p', { text: content.text }));
  tip.classList.toggle('pinned', pin);
  tip.hidden = false;
  btn.setAttribute('aria-expanded', 'true');
  btn.setAttribute('aria-describedby', 'infotip');
  position(btn);
}

function hide() {
  clearTimeout(showTimer); clearTimeout(hideTimer);
  if (!tip || !shownFor) return;
  shownFor.setAttribute('aria-expanded', 'false');
  shownFor.removeAttribute('aria-describedby');
  shownFor = null;
  pinned = false;
  tip.hidden = true;
  tip.classList.remove('pinned');
}

export const closeInfoTip = hide;

// An info button. `content()` is read each time the tip opens, so a panel
// that was retitled or re-described is always shown as it now is.
export function infoButton(content) {
  const btn = h('button', { class: 'p-info', type: 'button', 'aria-expanded': 'false' },
    s('svg', { viewBox: '0 0 16 16', width: '14', height: '14', 'aria-hidden': 'true', focusable: 'false' },
      s('circle', { cx: '8', cy: '8', r: '6.3', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.4' }),
      s('path', { d: 'M8 7.2v4', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.5', 'stroke-linecap': 'butt' }),
      s('circle', { cx: '8', cy: '4.9', r: '0.95', fill: 'currentColor' })));
  const label = () => `About ${content().title}`;
  btn.setAttribute('aria-label', label());
  btn.addEventListener('pointerenter', (e) => {
    if (e.pointerType !== 'mouse' || pinned) return;
    clearTimeout(hideTimer);
    showTimer = setTimeout(() => show(btn, content(), false), 120);
  });
  btn.addEventListener('pointerleave', (e) => {
    if (e.pointerType !== 'mouse') return;
    clearTimeout(showTimer);
    if (!pinned) hideTimer = setTimeout(() => { if (shownFor === btn) hide(); }, 100);
  });
  btn.addEventListener('focus', () => {
    btn.setAttribute('aria-label', label());
    if (btn.matches(':focus-visible') && !pinned) show(btn, content(), false);
  });
  btn.addEventListener('blur', () => { if (shownFor === btn) hide(); });
  btn.addEventListener('click', () => {
    if (shownFor === btn && pinned) hide();
    else show(btn, content(), true);
  });
  return btn;
}
