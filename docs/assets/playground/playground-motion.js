// Motion for the playground card: the command types itself in when the card
// scrolls into view, and a run is shown as it happens (progress rail, typed
// log, results that land row by row). Everything here is decoration: the text
// is always complete in the DOM, and reduced-motion readers get it instantly.

export const reduced = () => !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- typing -----------------------------------------------------------------

const caret = () => Object.assign(document.createElement('span'), { className: 'pgrr-caret', textContent: '▍', ariaHidden: 'true' });

function textNodes(el) {
  const out = [];
  const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = walk.nextNode(); n; n = walk.nextNode()) out.push(n);
  return out;
}

/** Types the text already in `el` (spans and all) one character at a time. */
async function typeInto(el, { cps = 60, signal }) {
  const nodes = textNodes(el).filter((n) => n.data.length);
  const full = nodes.map((n) => n.data);
  for (const n of nodes) n.data = '';
  const c = caret();
  el.append(c);
  try {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = 1; j <= full[i].length; j++) {
        if (signal.aborted) throw new DOMException('skip', 'AbortError');
        nodes[i].data = full[i].slice(0, j);
        nodes[i].parentNode.insertBefore(c, nodes[i].nextSibling);
        await sleep(1000 / cps + (Math.random() * 16));
      }
    }
  } finally {
    for (let i = 0; i < nodes.length; i++) nodes[i].data = full[i];
    c.remove();
  }
}

const reveal = (el) => el.classList.remove('pgrr-await');

/**
 * Plays the card's first appearance once, the first time its first line scrolls into view.
 * Anything the reader does to the card (a click, a key) finishes it at once.
 */
export function introOnView(card, parts) {
  const items = [...parts.scenarioBlock, ...parts.optionBlock, ...parts.tail];
  if (reduced() || !('IntersectionObserver' in window)) return;
  for (const el of items) el.classList.add('pgrr-await');
  const ctl = new AbortController();
  const finish = () => {
    ctl.abort();
    for (const el of items) reveal(el);
    card.removeEventListener('pointerdown', finish);
    card.removeEventListener('keydown', finish);
  };
  card.addEventListener('pointerdown', finish, { once: true });
  card.addEventListener('keydown', finish, { once: true });

  const stagger = async (els, gap) => {
    for (const el of els) {
      if (ctl.signal.aborted) return;
      reveal(el);
      await sleep(gap);
    }
  };

  const play = async () => {
    try {
      const o = { signal: ctl.signal };
      await sleep(250);
      await typeInto(parts.promptScenarios, { ...o, cps: 55 });
      await stagger(parts.scenarioBlock, 90);
      await sleep(220);
      await typeInto(parts.promptRun, { ...o, cps: 50 });
      await stagger(parts.optionBlock, 110);
      await sleep(120);
      await stagger(parts.tail, 140);
    } catch (e) {
      if (e.name !== 'AbortError') throw e;
    } finally {
      finish();
    }
  };

  // Hidden until it plays, so nothing flashes in full and then empties.
  for (const p of [parts.promptScenarios, parts.promptRun]) p.classList.add('pgrr-await');
  const io = new IntersectionObserver((entries) => {
    if (!entries.some((e) => e.isIntersecting)) return;
    io.disconnect();
    for (const p of [parts.promptScenarios, parts.promptRun]) reveal(p);
    play();
  }, { threshold: 1, rootMargin: '0px 0px -12% 0px' });
  io.observe(parts.promptScenarios);
}

// ---- the log line -----------------------------------------------------------

let logToken = 0;
/** Types `text` into `el`; a newer call cancels the one in flight. */
export async function typeLog(el, text) {
  const token = ++logToken;
  if (!text) { el.textContent = ''; return; }
  const full = `> ${text}`;
  if (reduced()) { el.textContent = full; return; }
  for (let i = 1; i <= full.length; i++) {
    if (token !== logToken) return;
    el.textContent = full.slice(0, i);
    await sleep(i % 3 ? 9 : 14);
  }
}

// ---- results ----------------------------------------------------------------

const GLYPHS = '0123456789';
function scramble(td, ms) {
  const final = td.textContent;
  if (!/\d/.test(final)) return;
  const t0 = performance.now();
  const tick = (now) => {
    const k = Math.min(1, (now - t0) / ms);
    td.textContent = [...final].map((ch, i) => {
      if (!/\d/.test(ch)) return ch;
      return i / final.length < k ? ch : GLYPHS[(Math.random() * 10) | 0];
    }).join('');
    if (k < 1) requestAnimationFrame(tick); else td.textContent = final;
  };
  requestAnimationFrame(tick);
}

/** Rows land one after another, their numbers settle, verdicts stamp in. */
export function landResults(root) {
  if (reduced()) return;
  const card = root.querySelector('.pgrr-term');
  card?.animate([{ opacity: 0, transform: 'translateY(10px)' }, { opacity: 1, transform: 'none' }],
    { duration: 380, easing: 'cubic-bezier(.2,.7,.2,1)' });
  const summary = root.querySelector('.pgrr-summary');
  if (summary) typeLogLike(summary);
  root.querySelectorAll('.pgrr-table tbody tr').forEach((tr, i) => {
    const delay = 260 + i * 140;
    tr.animate([{ opacity: 0, transform: 'translateX(-14px)' }, { opacity: 1, transform: 'none' }],
      { duration: 420, delay, easing: 'cubic-bezier(.2,.7,.2,1)', fill: 'backwards' });
    tr.querySelectorAll('.pgrr-val').forEach((td) => setTimeout(() => scramble(td, 520), delay));
    const badge = tr.querySelector('.pgrr-badge');
    if (badge) {
      const detected = badge.classList.contains('pgrr-Detected');
      badge.animate(detected
        ? [{ transform: 'scale(1.7) rotate(-6deg)', opacity: 0 }, { transform: 'scale(.94)', opacity: 1, offset: .6 }, { transform: 'none', opacity: 1 }]
        : [{ opacity: 0, transform: 'scale(.8)' }, { opacity: 1, transform: 'none' }],
      { duration: detected ? 520 : 320, delay: delay + 120, easing: 'cubic-bezier(.2,.8,.2,1)', fill: 'backwards', transformOrigin: 'left center' });
      if (detected) {
        tr.animate([{ boxShadow: 'inset 3px 0 0 var(--h-accent-solid, #e11d21), inset 0 0 0 100vmax rgba(225,29,33,.14)' },
          { boxShadow: 'inset 3px 0 0 var(--h-accent-solid, #e11d21), inset 0 0 0 100vmax rgba(225,29,33,0)' }],
        { duration: 1400, delay: delay + 500, easing: 'ease-out' });
      }
    }
  });
}

function typeLogLike(el) {
  const text = el.textContent;
  let i = 0;
  const step = () => {
    i += 2;
    el.textContent = text.slice(0, i);
    if (i < text.length) setTimeout(step, 12);
  };
  el.textContent = '';
  step();
}

/** Draws the chart's two lines from left to right. */
export function drawLines(svg) {
  if (reduced()) return;
  for (const p of svg.querySelectorAll('.pgrr-line-int, .pgrr-line-cum')) {
    const len = p.getTotalLength?.();
    if (!len) continue;
    const dashed = p.classList.contains('pgrr-line-cum');
    if (dashed) { p.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 700, delay: 300, fill: 'backwards' }); continue; }
    p.animate([{ strokeDasharray: `${len} ${len}`, strokeDashoffset: len }, { strokeDasharray: `${len} ${len}`, strokeDashoffset: 0 }],
      { duration: 900, easing: 'cubic-bezier(.3,.6,.2,1)' });
  }
}
