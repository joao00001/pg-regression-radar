import { client, getConfig, checkVersion, events, layoutBundle, linkStatus, getProfile, putProfile, forgetProfile } from './api.js';
import { clockOffset, estimateNow, defaultSelection, defaultUnit, relative, docsHref } from './model.js';
import { createAlerts } from './alerts.js';
import { createAlertBar } from './alertbar.js';
import * as R from './range.js';
import * as L from './layout.js';
import { createStore } from './store.js';
import { createBoard } from './board.js';
import { createFocus } from './focus.js';
import { closeInfoTip } from './infotip.js';
import { createChrome } from './chrome.js';
import { replay } from './motion.js';
import { s as svgEl, h } from './dom.js';

const $ = (id) => document.getElementById(id);

const state = {
  config: null, api: null, bundle: null, layout: null, store: null,
  boardId: null, range: R.relative('6h'),
  offset: 0, overview: null, deploys: [], cases: new Map(),
  selected: null, unit: null, series: null,
  live: 'connecting', link: { status: 'connected' }, versionOk: false,
  editing: false, signature: '', manual: false,
};
// What panels keep between renders and between boards: open tables, chosen comparisons.
const ui = { tableOpen: false, keyOpen: false, showAll: false, compare: {} };
let chrome = null, board = null, focus = null, alertBar = null, painted = false, focusPushed = false, savedScroll = 0;

const nowMs = () => estimateNow(state.offset, Date.now());
const currentBoard = () => L.boardById(state.layout, state.boardId);

// ---- reading ------------------------------------------------------------------

let running = false, again = false;
async function refresh() {
  if (running) { again = true; return; }
  running = true;
  try {
    if (!state.versionOk) { await checkVersion(); state.versionOk = true; }
    // Alerts do not follow the board's time range: a regression from yesterday is still open
    // today. They read the whole history in one light call (summaries, no evidence).
    const [overview, deploys, wide] = await Promise.all([
      state.api.overview(), state.api.deploys(R.query(state.range)),
      state.api.deploys({ from: `now-${R.MAX_DAYS}d` }).catch(() => null),
    ]);
    state.offset = clockOffset(overview.generatedAt, Date.now());
    state.overview = overview;
    state.deploys = deploys;
    if (alertBar) alertBar.update({ deploys: wide || deploys, watches: overview.watches || [], complete: !!wide });
    const ids = deploys.slice(0, 30).map((d) => d.id);
    // A case opened by its address may be older than the range; it is still read.
    if (state.selected && !ids.includes(state.selected)) ids.push(state.selected);
    const cases = await Promise.all(ids.map((id) => state.api.deploy(id).catch(() => null)));
    state.cases = new Map(cases.filter(Boolean).map((c) => [c.id, c]));
    if (!state.selected || (!state.cases.has(state.selected) && !deploys.some((d) => d.id === state.selected))) {
      state.selected = defaultSelection(deploys);
      state.unit = null;
    }
    await loadSeries();
    refreshSeriesCache();
    showError(null);
  } catch (e) {
    // Waiting for the port-forward is not an error to alarm anyone with: the status line says it.
    if (e.status === 503) showError(null); else showError(e.message);
  } finally {
    running = false;
    // A live view that redraws when nothing changed would drop what the person
    // was doing (a hover, an open table); only changed data repaints.
    const sig = signature();
    if (sig !== state.signature) { state.signature = sig; render(); }
    if (again) { again = false; refresh(); }
  }
}

function signature() {
  const { generatedAt, ...overview } = state.overview || {};
  void generatedAt;
  return JSON.stringify([overview, state.deploys, [...state.cases.values()], state.series, state.selected, state.unit, state.live, state.link.status, state.range]);
}

// The panel option that says which query a chart opens on, if the board has one.
function unitPolicy() {
  const b = currentBoard();
  const p = b && b.panels.find((x) => x.type === 'canary-chart');
  return (p && p.options && p.options.unit) || 'first';
}

async function loadSeries() {
  const c = state.cases.get(state.selected);
  const last = c && c.history && c.history[c.history.length - 1];
  const units = (last && last.units) || [];
  if (!units.some((u) => u.key === state.unit)) { state.unit = defaultUnit(units, unitPolicy()); state.manual = false; }
  state.series = null;
  if (c && state.unit) {
    // A query the person did not pick may have no recorded series; then the next one
    // that does is shown, rather than an empty chart.
    const order = state.manual ? [state.unit] : [state.unit, ...units.map((u) => u.key).filter((k) => k !== state.unit)].slice(0, 6);
    for (const k of order) {
      try { state.series = await state.api.series(c.id, k); state.unit = k; break; } catch (e) { if (e.status !== 404) break; }
    }
  }
}

// Series for a deploy and query the board is not itself drawing: a panel whose query
// names its own deploy or database query asks for them here. Read once, kept, and
// read again when the data moves, so a live panel stays live.
const seriesCache = new Map(); // "deploy|query" -> {series} | {missing: true} | 'loading'
function seriesFor(id, key) {
  const k = `${id}|${key}`, hit = seriesCache.get(k);
  if (hit && hit !== 'loading') return hit.missing ? { series: null, state: 'missing' } : { series: hit.series, state: 'ready' };
  if (!hit) fetchSeries(k, id, key, false);
  return { series: null, state: 'loading' };
}
async function fetchSeries(k, id, key, quiet) {
  if (!seriesCache.has(k)) seriesCache.set(k, 'loading');
  let next;
  try { next = { series: await state.api.series(id, key) }; } catch (e) { next = e.status === 404 ? { missing: true } : null; }
  if (!next) { if (seriesCache.get(k) === 'loading') seriesCache.delete(k); return; }
  const before = seriesCache.get(k);
  seriesCache.set(k, next);
  if (!quiet || JSON.stringify(before) !== JSON.stringify(next)) rerender();
}
function refreshSeriesCache() {
  for (const k of [...seriesCache.keys()].slice(0, 24)) { const i = k.indexOf('|'); fetchSeries(k, k.slice(0, i), k.slice(i + 1), true); }
}

async function rerender() { state.signature = signature(); render(); }

async function select(id) {
  state.selected = id; state.unit = null; state.manual = false; ui.showAll = false;
  if (state.boardId === 'case' || location.hash.startsWith('#/case')) history.replaceState(null, '', L.hashFor({ board: 'case', deploy: id, view: focus && focus.panelId() }));
  if (!state.cases.has(id)) await refresh(); else { await loadSeries(); await rerender(); }
}
async function pickUnit(key) { state.unit = key; state.manual = true; await loadSeries(); await rerender(); }

// ---- drawing ------------------------------------------------------------------

function showError(msg, problems) {
  const el = $('error');
  el.hidden = !msg;
  if (!msg) return;
  const kids = [h('strong', { text: `${msg} ` })];
  if (problems) {
    kids.push(h('ul', { class: 'problems' }, problems.map((p) => h('li', null, h('code', { text: p.line ? `${p.line}:${p.col}` : 'file' }), ` ${p.message}`))),
      h('p', { class: 'muted', text: 'Fix the file and reload this page; pg-regression-radar dashboard config validate FILE checks it from the terminal.' }));
  } else {
    kids.push(h('button', { class: 'btn', type: 'button', onclick: refresh, text: 'Try again' }));
  }
  el.replaceChildren(...kids);
}

function view() {
  const sel = state.cases.get(state.selected) || null;
  return {
    now: nowMs(), config: state.config, overview: state.overview, deploys: state.deploys, cases: state.cases,
    selected: state.selected, kase: sel, unit: state.unit, series: state.series, link: state.link, range: state.range,
    // A unit that was asked for and has no series: the chart says so instead of staying blank.
    seriesMissing: !!state.unit && !state.series,
    seriesFor,
  };
}

function liveText() {
  if (state.link.status === 'incompatible') return ['Incompatible', 'live-bad'];
  if (state.link.status === 'reconnecting') return ['Reconnecting', 'live-retry'];
  if (state.link.status === 'connecting') return ['Connecting', ''];
  if (state.live === 'live') return ['Live', 'live-on'];
  if (state.live === 'reconnecting') return ['Reconnecting', 'live-retry'];
  return ['Connecting', ''];
}

function render() {
  const live = $('live');
  const [text, cls] = liveText();
  live.className = `live ${cls}`;
  $('live-text').textContent = text;
  if (!state.overview) return;
  // The board underneath is not drawn while a panel is in focus; it catches up when focus ends.
  if (focus.isOpen()) focus.update(view()); else board.update(view());
  chrome.setRange(state.range, R.historyNote(state.range, nowMs(), state.overview.history));
  if (!painted) {
    painted = true;
    // The boot sequence runs once; the class is gone before anything live can replay it.
    setTimeout(() => document.body.classList.remove('boot'), 1800);
  }
}

// Relative times move on their own; nothing else is redrawn.
function tick() {
  const now = nowMs();
  for (const el of document.querySelectorAll('[data-rel]')) {
    const t = Number(el.dataset.rel), r = relative(t, now);
    el.textContent = el.dataset.fmt === 'deployed' ? `deployed ${r}` : el.dataset.fmt === 'next' ? `next check ${r}` : r;
  }
}

// ---- boards, ranges, editing --------------------------------------------------

function route() {
  const r = L.parseHash(location.hash);
  const want = r.board && L.boardById(state.layout, r.board) ? r.board : state.layout.boards[0].id;
  const changed = want !== state.boardId;
  if (r.deploy && r.deploy !== state.selected) { state.selected = r.deploy; state.unit = null; }
  if (changed) { leaveFocusNow(); showBoard(want); } else if (r.deploy) refresh();
  syncFocus(r.view);
}

// ---- the focus view -------------------------------------------------------------

// The address of what is on screen: the board, the deploy on the case board, and the focused panel.
const addr = (viewId) => L.hashFor({ board: state.boardId, deploy: state.boardId === 'case' ? state.selected : null, view: viewId });

function openFocus(id) {
  if (state.editing || focus.isOpen()) return;
  history.pushState(null, '', addr(id));
  focusPushed = true;
  route();
}

// Back to the board: undo the step that opened the view if this page took it, else just drop it from the address.
function leaveFocus() {
  if (focusPushed) { focusPushed = false; history.back(); return; }
  history.replaceState(null, '', addr(null));
  route();
}

function leaveFocusNow() {
  if (!focus.isOpen()) return;
  focus.close();
  $('board').hidden = false;
}

function syncFocus(viewId) {
  let p = viewId && board.panel(viewId);
  if (viewId && !p) { history.replaceState(null, '', addr(null)); viewId = null; }
  if (!p) {
    focusPushed = false;
    if (focus.isOpen()) { focus.close(); showGrid(); }
    return;
  }
  if (focus.isOpen() && focus.panelId() === p.id) return;
  if (state.editing) setEditing(false);
  closeInfoTip();
  savedScroll = window.scrollY;
  $('board').hidden = true;
  focus.open(p, state.overview ? view() : null);
  window.scrollTo(0, 0);
}

function showGrid() {
  $('board').hidden = false;
  board.replace(currentBoard().panels); // takes in what was changed in focus
  if (state.overview) board.update(view());
  window.scrollTo(0, savedScroll);
}

// A setting changed in the focus view: kept like any other edit of the layout.
function onFocusChange(p) {
  const b = currentBoard();
  const before = b.panels.find((x) => x.id === p.id);
  onBoardEdit(L.replacePanel(b.panels, p));
  // Which query the chart opens on is a setting of the chart; a new choice applies now.
  if (p.type === 'canary-chart' && ((before && before.options) || {}).unit !== (p.options || {}).unit) {
    state.unit = null; state.manual = false;
    loadSeries().then(rerender);
  }
}

function showBoard(id) {
  state.boardId = id;
  const b = currentBoard();
  board.show(b);
  chrome.setBoards(state.layout.boards, id);
  state.range = state.store.loadRange(state.bundle.source, id) || R.fromBoard(b.range);
  document.title = `${b.title} · Deploy radar`;
  state.signature = '';
  refresh();
}

function setRange(r) {
  if (R.same(r, state.range)) return;
  state.range = r;
  state.store.saveRange(state.bundle.source, state.boardId, r);
  state.selected = null; state.unit = null;
  refresh();
}

function setEditing(on) {
  if (on && window.matchMedia('(max-width: 900px)').matches) {
    chrome.notice('Editing the layout needs a wider window: on a narrow screen the panels stack in one column.', 'info');
    return;
  }
  if (on && focus.isOpen()) { focusPushed = false; history.replaceState(null, '', addr(null)); leaveFocusNow(); showGrid(); }
  state.editing = on;
  board.setEditing(on);
  chrome.setEditing(on);
}

function onBoardEdit(panels) {
  state.layout = L.withBoard(state.layout, state.boardId, (b) => ({ ...b, panels }));
  const ok = state.store.saveLayout(state.bundle.source, state.bundle.layout, state.layout);
  chrome.setEdited(L.fingerprint(state.layout) !== L.fingerprint(state.bundle.layout));
  if (!ok) chrome.notice('This browser would not keep the change, so it will be gone after a reload. Export the YAML to keep it.', 'warn');
}

// The profile could not be written (disk full, directory gone): say so, once, and
// clear it when a later save works.
let saveNotice = false;
function onSaveProblem(e) {
  if (!e) { if (saveNotice) { saveNotice = false; chrome.notice(null); } return; }
  saveNotice = true;
  chrome.notice(`Your edits could not be saved to ${state.store.where} (${e.message}). They stay in this window; export the YAML to keep them.`, 'warn');
}

function resetEdits() {
  state.store.resetLayout(state.bundle.source);
  state.layout = state.bundle.layout;
  chrome.setEdited(false);
  chrome.setBoards(state.layout.boards, state.boardId);
  board.replace(currentBoard().panels);
  chrome.notice(null);
}

// ---- theme --------------------------------------------------------------------

const SUN = () => svgEl('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.8, 'stroke-linecap': 'round', 'aria-hidden': 'true' },
  svgEl('circle', { cx: 12, cy: 12, r: 4 }), svgEl('path', { d: 'M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6l1.4 1.4M17 17l1.4 1.4M5.6 18.4L7 17M17 7l1.4-1.4' }));
const MOON = () => svgEl('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.8, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' },
  svgEl('path', { d: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z' }));

function initTheme() {
  const btn = $('theme'), root = document.documentElement;
  const paint = () => {
    const light = root.getAttribute('data-theme') === 'light';
    btn.replaceChildren(light ? MOON() : SUN());
    const next = light ? 'dark' : 'light';
    btn.setAttribute('aria-label', `Switch to the ${next} theme`);
    btn.title = `Switch to the ${next} theme`;
  };
  btn.addEventListener('click', () => {
    const next = root.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
    root.classList.add('theming'); // colours cross-fade for a moment
    root.setAttribute('data-theme', next);
    try { localStorage.setItem('pgrr-theme', next); } catch (_) { /* storage may be blocked */ }
    paint();
    setTimeout(() => root.classList.remove('theming'), 450);
  });
  paint();
}

// ---- the link to the manager (cluster only) ---------------------------------------

let linkTimer = null;
async function pollLink() {
  try {
    const l = await linkStatus();
    const was = state.link.status;
    state.link = l;
    if (l.status !== was) {
      if (l.status === 'connected') { state.versionOk = false; refresh(); }
      state.signature = signature();
      render();
    }
    if (l.status === 'incompatible') showError(`This dashboard cannot talk to that manager. ${l.error || ''}`);
    else if (l.status !== 'connected') showError(null);
  } catch (_) { /* the dashboard process itself is gone; nothing to say that the page can show */ }
  linkTimer = setTimeout(pollLink, state.link.status === 'connected' ? 5000 : 1500);
}

// ---- start --------------------------------------------------------------------

async function start() {
  initTheme();
  try {
    state.config = await getConfig();
    state.api = client(state.config);
    state.bundle = await layoutBundle();
  } catch (e) {
    showError(e.message, e.problems);
    return;
  }
  const storage = (() => { try { return window.localStorage; } catch (_) { return { getItem: () => null, setItem() { throw new Error('blocked'); }, removeItem() {} }; } })();
  const profile = await getProfile();
  state.store = createStore(storage, profile.saved ? { ...profile, put: putProfile, forget: forgetProfile } : null, onSaveProblem);
  const loaded = state.store.loadLayout(state.bundle.source, state.bundle.layout, state.bundle.catalog);
  state.layout = loaded.layout;

  board = createBoard($('board'), {
    catalog: state.bundle.catalog, grid: state.bundle.grid,
    env: { ui, actions: { select, pickUnit, rerender } },
    onEdit: onBoardEdit,
    onFocus: openFocus,
  });
  focus = createFocus({ el: $('focus'), catalog: state.bundle.catalog, queryFields: state.bundle.queryFields || [], env: { ui, actions: { select, pickUnit, rerender } }, getBoardId: () => state.boardId, onChange: onFocusChange, onBack: leaveFocus });
  chrome = createChrome({
    catalog: state.bundle.catalog, now: nowMs,
    onRange: setRange,
    onEdit: setEditing,
    onDone: () => setEditing(false),
    onAdd: (type) => board.add(type),
    onReset: resetEdits,
    onExport: () => chrome.showExport(L.toYAML(state.layout, state.bundle.catalog), state.bundle.source),
  });
  alertBar = createAlertBar({
    el: $('alerts'), alerts: createAlerts({ storage, docsHref }),
    hrefFor: (id) => L.hashFor({ board: state.layout.boards.some((b) => b.id === 'case') ? 'case' : state.boardId, deploy: id }),
  });
  chrome.setSaveNote(state.store.saved ? `Your edits are saved in ${state.store.where}.` : 'Your edits are kept in this browser only.');
  chrome.setEdited(loaded.edited);
  if (profile.problem) chrome.notice(`${profile.problem}. The next change you make replaces it.`, 'warn');
  const leave = () => state.store.flush(true);
  window.addEventListener('pagehide', leave);
  document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') leave(); });
  if (loaded.discarded) chrome.notice('The dashboard file changed since you edited the layout here, so your edits were dropped.', 'info');

  window.addEventListener('hashchange', route);
  // V opens the panel under the pointer (or holding the keyboard focus) large, as in Grafana.
  document.addEventListener('keydown', (e) => {
    if ((e.key !== 'v' && e.key !== 'V') || e.ctrlKey || e.metaKey || e.altKey || e.shiftKey || e.defaultPrevented) return;
    const t = e.target;
    if (t instanceof Element && t.matches('input, textarea, select, [contenteditable]')) return;
    const id = !focus.isOpen() && !state.editing && board.current();
    if (id) { e.preventDefault(); openFocus(id); }
  });
  route();

  if (state.config.origin === 'cluster') pollLink();
  let timer = null;
  events({
    onState: (v) => { state.live = v; state.signature = signature(); render(); },
    onChange: () => {
      replay($('live'), 'ping'); // a ring leaves the dot: the stream delivered something
      clearTimeout(timer); timer = setTimeout(refresh, 400);
    },
  });
  setInterval(tick, 1000);
  setInterval(refresh, 30000); // the stream says what changed; this only covers a missed beat
}

start();
