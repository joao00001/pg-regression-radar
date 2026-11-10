// The "Command line" terminal on the playground page: types out the real
// `pg-regression-radar <command> --help` text (captured from the binary by
// scripts/build_playground.sh into cli-help.json), flag by flag, and cycles
// through the commands. The text is always complete in the DOM once a run
// ends; reduced-motion readers get it immediately and nothing autoplays.

const ROOT = document.getElementById('pgrr-cli');
const reduced = () => !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v == null) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  el.append(...kids.flat().filter((x) => x !== false && x != null));
  return el;
}
const span = (cls, text) => h('span', { class: cls }, text);

// ---- turning help text into styled lines -----------------------------------

const FLAG = /^ {2}-{1,2}([\w.-]+)(?: (\S+))?$/;
const DEFAULT_TAIL = /^(.*?)(\(default [^)]*\))\s*$/s;

function describe(text) {
  const m = DEFAULT_TAIL.exec(text);
  return m ? [m[1], span('pgrr-cli-def', m[2])] : [text];
}

function render(text) {
  const lines = text.replace(/\n+$/, '').split('\n');
  const out = [];
  let para = [];
  // The binary hard-wraps its text at 80 columns; rejoin it so it reflows at any width.
  const flush = () => { if (para.length) out.push(h('div', { class: 'pgrr-cli-row' }, para.join(' '))); para = []; };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const f = FLAG.exec(line);
    if (f && /^ {4}\s*\S/.test(lines[i + 1] ?? '')) {
      flush();
      const [, name, type] = f;
      out.push(h('div', { class: 'pgrr-cli-flag' },
        h('div', { class: 'pgrr-cli-sig' }, '  ', span('pgrr-cli-name', `--${name}`), type ? ' ' : false, type ? span('pgrr-cli-type', type) : false),
        h('div', { class: 'pgrr-cli-desc' }, ...describe(lines[++i].trim()))));
      continue;
    }
    const cmd = /^ {2}(operator|manager|collector|ingester|version|help) +(.*)$/.exec(line);
    if (cmd) {
      flush();
      let desc = cmd[2];
      while (/^ {14}\S/.test(lines[i + 1] ?? '')) desc += ` ${lines[++i].trim()}`;
      out.push(h('div', { class: 'pgrr-cli-cmdrow' }, span('pgrr-cli-cmd', cmd[1]), span('pgrr-cli-cmddesc', desc)));
      continue;
    }
    if (/^(Usage|Commands|Usage of [\w-]+):$/.test(line.trim())) { flush(); out.push(h('div', { class: 'pgrr-cli-row pgrr-cli-head' }, line)); continue; }
    const url = /^(Docs: )(\S+)$/.exec(line);
    if (url) { flush(); out.push(h('div', { class: 'pgrr-cli-row' }, url[1], span('pgrr-cli-url', url[2]))); continue; }
    if (!line.trim()) { flush(); out.push(h('div', { class: 'pgrr-cli-gap' })); continue; }
    if (/^ {2}\S/.test(line)) { flush(); out.push(h('div', { class: 'pgrr-cli-row pgrr-cli-indent' }, line.trim())); continue; }
    para.push(line.trim());
  }
  flush();
  return out;
}

// ---- typing -----------------------------------------------------------------

function textNodes(el) {
  const out = [];
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode(); n; n = w.nextNode()) if (n.data.length) out.push(n);
  return out;
}

const caret = () => h('span', { class: 'pgrr-caret', 'aria-hidden': 'true' }, '▍');

/**
 * Reveals every text node under `el` in order, at `cps` characters a second
 * with a small beat between nodes (so each flag lands as its own step).
 * Resolves true when complete, false if cancelled.
 */
function reveal(el, { cps, beat = 5, follow, token, current }) {
  const nodes = textNodes(el);
  const full = nodes.map((n) => n.data);
  for (const n of nodes) n.data = '';
  const c = caret();
  el.append(c);
  const total = full.reduce((a, s) => a + s.length + beat, 0);
  const t0 = performance.now();
  return new Promise((resolve) => {
    let shown = -1;
    const finishNow = () => {
      nodes.forEach((n, i) => { n.data = full[i]; });
      c.remove();
    };
    const frame = (now) => {
      if (token.cancelled) { c.remove(); return resolve(false); }
      if (token.skip) { finishNow(); follow?.(); return resolve(true); }
      let budget = Math.min(total, ((now - t0) / 1000) * cps);
      if (Math.floor(budget) === shown) { requestAnimationFrame(frame); return; }
      shown = Math.floor(budget);
      let last = null;
      for (let i = 0; i < nodes.length; i++) {
        const take = Math.max(0, Math.min(full[i].length, budget));
        nodes[i].data = full[i].slice(0, take);
        if (take > 0) last = nodes[i];
        budget -= full[i].length + beat;
        if (budget < 0) break;
      }
      if (last) last.parentNode.insertBefore(c, last.nextSibling);
      follow?.();
      if (now - t0 >= (total / cps) * 1000) { finishNow(); follow?.(); return resolve(true); }
      requestAnimationFrame(frame);
    };
    requestAnimationFrame(frame);
  });
}

// ---- the card -----------------------------------------------------------------

async function init() {
  if (!ROOT) return;
  let data;
  try {
    const res = await fetch(new URL('./cli-help.json', import.meta.url));
    if (!res.ok) return;
    data = await res.json();
  } catch { return; }
  const { binary, commands } = data;
  if (!commands?.length) return;

  const stage = ROOT.parentElement;
  const tabs = commands.map((c, i) => h('button', {
    type: 'button', role: 'tab', class: 'pgrr-cli-tab', id: `pgrr-cli-tab-${c.id}`,
    'aria-selected': String(i === 0), 'aria-controls': 'pgrr-cli-screen', tabindex: i === 0 ? '0' : '-1',
  }, c.id));
  const bar = h('div', { class: 'filename pgrr-cli-bar' },
    h('div', { class: 'pgrr-cli-tabs', role: 'tablist', 'aria-label': 'Command' }, tabs));
  const promptEl = h('div', { class: 'pgrr-cli-prompt' });
  const outEl = h('div', { class: 'pgrr-cli-out' });
  const screen = h('div', { class: 'pgrr-screen pgrr-cli-screen', id: 'pgrr-cli-screen', role: 'tabpanel', 'aria-label': 'Help text' }, promptEl, outEl);
  ROOT.replaceChildren(h('section', { class: 'pgrr-term pgrr-cli' }, bar, screen));
  stage.hidden = false;
  stage.previousElementSibling?.classList.contains('pg-cli-heading') && (stage.previousElementSibling.hidden = false);

  const state = { i: 0, token: null, auto: false, started: false };
  // No scrollbar: the typing itself carries the text up as it grows.
  const follow = () => { screen.scrollTop = screen.scrollHeight; };

  const select = (i) => {
    state.i = i;
    tabs.forEach((t, k) => { t.setAttribute('aria-selected', String(k === i)); t.tabIndex = k === i ? 0 : -1; });
    screen.setAttribute('aria-labelledby', tabs[i].id);
  };

  const promptFor = (c) => h('span', {}, span('pgrr-ps', '$'), ' ', span('pgrr-cli-bin', binary), ' ', span('pgrr-cmd', c.argv.split(' ')[0]), c.argv.includes(' ') ? [' ', span('pgrr-flag', c.argv.split(' ').slice(1).join(' '))] : false);

  const idle = () => {
    const p = h('div', { class: 'pgrr-cli-idle' }, span('pgrr-ps', '$'), ' ', h('span', { class: 'pgrr-caret', 'aria-hidden': 'true' }, '▍'));
    outEl.append(p);
  };

  async function play(i, { typed = true } = {}) {
    state.token && (state.token.cancelled = true);
    const token = (state.token = { cancelled: false, skip: false });
    select(i);
    const c = commands[i];
    screen.scrollTop = 0;
    outEl.replaceChildren(...render(c.text));
    promptEl.replaceChildren(promptFor(c));
    if (!typed || reduced()) { idle(); return true; }
    screen.setAttribute('aria-busy', 'true');
    outEl.setAttribute('aria-hidden', 'true');
    outEl.style.visibility = 'hidden';
    try {
      if (!(await reveal(promptEl, { cps: 24, beat: 3, token }))) return false;
      await sleep(260);
      if (token.cancelled) return false;
      outEl.style.visibility = '';
      const words = textNodes(outEl).reduce((a, n) => a + n.data.length, 0);
      const cps = Math.min(900, Math.max(320, words / 6.5));
      if (!(await reveal(outEl, { cps, beat: 14, follow, token }))) return false;
      idle();
      follow();
      return true;
    } finally {
      if (!token.cancelled) { outEl.style.visibility = ''; outEl.removeAttribute('aria-hidden'); screen.removeAttribute('aria-busy'); }
    }
  }

  const stopAuto = () => { state.auto = false; };
  async function autoplay() {
    state.auto = true;
    for (let i = 0; i < commands.length && state.auto; i++) {
      const ok = await play(i);
      if (!ok || !state.auto) return;
      await sleep(1700);
    }
    state.auto = false;
  }

  tabs.forEach((t, i) => {
    t.addEventListener('click', () => { stopAuto(); play(i); });
    t.addEventListener('keydown', (e) => {
      const d = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
      if (!d) return;
      e.preventDefault();
      const n = (i + d + tabs.length) % tabs.length;
      tabs[n].focus(); tabs[n].click();
    });
  });
  // Clicking the text finishes what is typing and leaves the choice to the reader.
  screen.addEventListener('pointerdown', () => { stopAuto(); if (state.token) state.token.skip = true; });

  if (reduced() || !('IntersectionObserver' in window)) { play(0, { typed: false }); return; }
  // Empty frame until it plays: show the first command's prompt, no output yet.
  promptEl.replaceChildren(promptFor(commands[0]));
  new IntersectionObserver((entries, io) => {
    if (!entries.some((e) => e.isIntersecting) || state.started) return;
    state.started = true;
    io.disconnect();
    autoplay();
  }, { threshold: 0.35 }).observe(ROOT);
}
init();
