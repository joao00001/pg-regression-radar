// Docs playground UI. Loads lazily: nothing heavy is fetched until the reader
// presses "Run simulation" (PGlite is about 5 MB gzipped, the engine about 1 MB).
import { SCENARIOS, DEFAULTS, simulate } from './playground-core.js';

const HERE = new URL('.', import.meta.url);
const ROOT = document.getElementById('pgrr-playground');
// Pinned: the extension's tarball and the wasm must come from the same release.
const PGLITE_BASE = ROOT?.dataset.pgliteBase
  || 'https://cdn.jsdelivr.net/npm/@electric-sql/pglite@0.5.16/dist';

const SVG_NS = 'http://www.w3.org/2000/svg';

function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === false || v == null) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

function svg(tag, attrs = {}, ...kids) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  for (const kid of kids.flat()) if (kid != null) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  return el;
}

// ---- lazy loaders -------------------------------------------------------

let enginePromise;
function loadEngine() {
  enginePromise ??= (async () => {
    await new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = new URL('wasm_exec.js', HERE);
      s.onload = resolve;
      s.onerror = () => reject(new Error('Could not load wasm_exec.js'));
      document.head.append(s);
    });
    const res = await fetch(new URL('pgrr.wasm.gz', HERE));
    if (!res.ok) throw new Error(`Could not load the engine (HTTP ${res.status})`);
    let bytes = new Uint8Array(await res.arrayBuffer());
    // Gzipped by us; some servers also decode it for us, so sniff the magic.
    if (bytes[0] === 0x1f && bytes[1] === 0x8b) {
      if (typeof DecompressionStream === 'undefined') throw new Error('This browser cannot decompress the engine (DecompressionStream is missing).');
      const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream('gzip'));
      bytes = new Uint8Array(await new Response(stream).arrayBuffer());
    }
    const go = new globalThis.Go();
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    go.run(instance); // runs until the page closes; exports pgrrAnalyse
    for (let i = 0; i < 100 && !globalThis.pgrrReady; i++) await new Promise((r) => setTimeout(r, 10));
    if (!globalThis.pgrrReady) throw new Error('The engine did not start.');
  })();
  enginePromise.catch(() => { enginePromise = undefined; });
  return enginePromise;
}

let pgPromise;
function loadPostgres() {
  pgPromise ??= (async () => {
    const [core, ext] = await Promise.all([
      import(/* @vite-ignore */ `${PGLITE_BASE}/index.js`),
      import(/* @vite-ignore */ `${PGLITE_BASE}/contrib/pg_stat_statements.js`),
    ]);
    return { PGlite: core.PGlite, pg_stat_statements: ext.pg_stat_statements };
  })();
  pgPromise.catch(() => { pgPromise = undefined; });
  return pgPromise;
}

// ---- state --------------------------------------------------------------

const state = {
  scenario: SCENARIOS[0],
  params: { ...DEFAULTS },
  custom: false,
  running: false,
  result: null, // { request, response }
  selected: null,
  sqlOpen: false,
};

function applyScenario(sc) {
  state.scenario = sc;
  state.custom = false;
  state.params = { ...DEFAULTS, deploySql: sc.deploySql, callsPerScrape: sc.callsPerScrape ?? DEFAULTS.callsPerScrape, resetAfterDeployScrapes: sc.resetAfterDeployScrapes ?? 0 };
}
applyScenario(SCENARIOS[0]);

// ---- rendering ----------------------------------------------------------
//
// The whole UI is drawn as terminal sessions in the docs' code-block cards
// (`.pgrr-term`, sharing extra.css's card, header row and colour tokens).

const els = {};

const OPTIONS = [
  { key: 'callsPerScrape', flag: '--calls-per-scrape', min: 1, max: 100, step: 1, note: 'calls/query/scrape (playground)' },
  { key: 'windowMinutes', flag: '--window-minutes', min: 2, max: 30, step: 1, note: 'each side of the deploy' },
  { key: 'minSamples', flag: '--min-samples', min: 2, max: 100, step: 1, note: 'observations per window' },
  { key: 'minExecutions', flag: '--min-executions', min: 1, max: 100000, step: 1, note: 'executions per window' },
  { key: 'latencyChangeThreshold', flag: '--latency-threshold', min: 0.01, max: 5, step: 0.01, note: '0.2 = 20% slower' },
  { key: 'pValueThreshold', flag: '--p-value-threshold', min: 0.001, max: 0.5, step: 0.001, note: 'Welch t-test (playground)' },
];

const FILES = [
  { key: 'setupSql', name: 'schema.sql', label: 'Schema and data', rows: 14, hint: 'runs once, before the workload' },
  { key: 'workloadSql', name: 'workload.sql', label: 'Workload (one statement per line)', rows: 5, hint: 'one statement per line; :id becomes a random id on every call' },
  { key: 'deploySql', name: 'deploy.sql', label: 'The deploy (runs halfway through)', rows: 6, hint: 'runs once, halfway through the timeline' },
];

function term(title, ...screen) {
  return h('section', { class: 'pgrr-term' },
    h('div', { class: 'filename' }, title),
    h('div', { class: 'pgrr-screen' }, ...screen));
}

const prompt = (...kids) => h('div', { class: 'pgrr-line' }, h('span', { class: 'pgrr-ps' }, '$'), ' ', ...kids);

function optionRow(opt, last) {
  const input = h('input', {
    type: 'number', min: opt.min, max: opt.max, step: opt.step, value: state.params[opt.key],
    'aria-label': `${opt.flag} (${opt.note})`,
    style: `width:${String(opt.step < 1 ? 5 : 6)}ch`,
    onchange: (e) => { const v = Number(e.target.value); if (Number.isFinite(v)) state.params[opt.key] = v; },
  });
  return h('div', { class: `pgrr-opt${last ? ' pgrr-last' : ''}` },
    h('span', { class: 'pgrr-optmain' },
      h('span', { class: 'pgrr-flag' }, opt.flag), '=', input,
      h('span', { class: 'pgrr-bs', 'aria-hidden': 'true' }, last ? '' : ' \\')),
    h('span', { class: 'pgrr-comment-inline' }, opt.note));
}

function fileCard(f) {
  return term(f.name,
    h('p', { class: 'pgrr-comment' }, f.hint),
    h('textarea', {
      rows: f.rows, spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off', 'aria-label': f.label,
      oninput: (e) => { state.params[f.key] = e.target.value; state.custom = true; markCustom(); },
    }, state.params[f.key]));
}

function markCustom() {
  els.scenarioNote.textContent = state.custom ? 'custom SQL: the verdict is whatever the engine decides' : state.scenario.blurb;
  els.cmdName.textContent = state.custom ? 'custom' : state.scenario.id;
  for (const b of els.scenarioButtons) b.setAttribute('aria-pressed', String(!state.custom && b.dataset.id === state.scenario.id));
}

function setSqlOpen(open) {
  state.sqlOpen = open;
  els.sqlToggle.setAttribute('aria-expanded', String(open));
  els.sqlToggle.querySelector('.pgrr-chev').textContent = open ? '▾' : '▸';
  els.files.hidden = !open;
}

function renderForm() {
  els.scenarioButtons = SCENARIOS.map((sc) => h('button', {
    type: 'button', class: 'pgrr-scn', 'data-id': sc.id, 'aria-pressed': String(sc.id === state.scenario.id),
    onclick: () => { applyScenario(sc); renderForm(); },
  }, h('span', { class: 'pgrr-mark-sel', 'aria-hidden': 'true' }, '❯'), h('span', { class: 'pgrr-scn-id' }, sc.id), h('span', { class: 'pgrr-scn-title' }, sc.title)));
  els.scenarioNote = h('p', { class: 'pgrr-comment' }, state.scenario.blurb);
  els.cmdName = h('span', { class: 'pgrr-arg' }, state.scenario.id);

  els.run = h('button', { type: 'button', class: 'pgrr-run', onclick: run }, '⏎ Run simulation');
  els.status = h('p', { class: 'pgrr-log', role: 'status', 'aria-live': 'polite' });
  els.bar = h('span', { class: 'pgrr-bar', role: 'progressbar', 'aria-label': 'Simulation progress', 'aria-valuemin': '0', hidden: true });

  els.sqlToggle = h('button', { type: 'button', class: 'pgrr-toggle', 'aria-expanded': 'false', 'aria-controls': 'pgrr-files', onclick: () => setSqlOpen(!state.sqlOpen) },
    h('span', { class: 'pgrr-ps' }, '$'), ' playground edit ', h('span', { class: 'pgrr-flag' }, '--sql'), ' ', h('span', { class: 'pgrr-chev', 'aria-hidden': 'true' }, '▸'),
    h('span', { class: 'pgrr-comment-inline' }, 'schema, workload and deploy'));
  els.files = h('div', { id: 'pgrr-files', class: 'pgrr-files', hidden: true }, FILES.map(fileCard));

  const main = term('Playground',
    prompt('playground ', h('span', { class: 'pgrr-cmd' }, 'scenarios')),
    h('div', { class: 'pgrr-scenarios', role: 'group', 'aria-label': 'Scenario' }, els.scenarioButtons),
    els.scenarioNote,
    prompt('playground ', h('span', { class: 'pgrr-cmd' }, 'run'), ' ', els.cmdName, ' \\'),
    h('div', { class: 'pgrr-opts' }, OPTIONS.map((o, i) => optionRow(o, i === OPTIONS.length - 1))),
    els.sqlToggle,
    h('div', { class: 'pgrr-runrow' }, els.run, els.bar),
    els.status);

  els.form.replaceChildren(main, els.files);
  setSqlOpen(state.sqlOpen);
}

const STATUS_MARK = { Detected: '✖', NoRegression: '✔', InsufficientData: '?' };

function badge(status) {
  return h('span', { class: `pgrr-badge pgrr-${status}` }, h('span', { 'aria-hidden': 'true' }, `${STATUS_MARK[status] ?? '•'} `), status);
}

const fmtMs = (v) => (v >= 100 ? v.toFixed(0) : v >= 1 ? v.toFixed(2) : v.toFixed(3));

// A tiny JSON highlighter with the code blocks' token colours.
function highlightJson(text) {
  const esc = text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  return esc.replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(-?\d+(?:\.\d+)?(?:e[+-]?\d+)?)\b|\b(true|false|null)\b/gi,
    (m, str, colon, num, kw) => {
      if (str) return colon ? `<span class="pgrr-t-key">${str}</span>${colon}` : `<span class="pgrr-t-str">${str}</span>`;
      if (num !== undefined) return `<span class="pgrr-t-num">${num}</span>`;
      return `<span class="pgrr-t-kw">${kw}</span>`;
    });
}

function renderResults() {
  const r = state.result;
  if (!r) { els.results.replaceChildren(); return; }
  const { response, request } = r;
  if (response.error) {
    els.results.replaceChildren(term('Output', h('p', { class: 'pgrr-error' }, `error: ${response.error}`)));
    return;
  }
  const rows = response.results.map((res) => h('tr', {
    class: res.queryid === state.selected ? 'pgrr-selected' : '',
    tabindex: 0, 'aria-selected': String(res.queryid === state.selected),
    onclick: () => select(res.queryid),
    onkeydown: (e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); select(res.queryid); } },
  },
  h('td', {}, badge(res.status)),
  h('td', { class: 'pgrr-q' }, h('code', {}, res.query)),
  h('td', { class: 'pgrr-num', 'data-label': 'mean' }, res.status === 'InsufficientData' ? '-' : `${fmtMs(res.meanLatencyBefore)} → ${fmtMs(res.meanLatencyAfter)} ms`),
  h('td', { class: 'pgrr-num', 'data-label': 'change' }, res.status === 'InsufficientData' ? '-' : `×${res.latencyChangeFactor.toFixed(2)}`),
  h('td', { class: 'pgrr-num', 'data-label': 'confidence' }, res.status === 'Detected' ? res.confidenceScore.toFixed(3) : '-')));

  const table = h('table', { class: 'pgrr-table' },
    h('thead', {}, h('tr', {}, ...['Verdict', 'Query', 'Before → after', 'Change', 'Confidence'].map((t) => h('th', {}, t)))),
    h('tbody', {}, rows));

  const detected = response.results.filter((x) => x.status === 'Detected').length;
  const summary = h('p', { class: `pgrr-summary${detected ? ' pgrr-flagged' : ''}` }, detected
    ? `${detected} of ${response.results.length} queries flagged. Select a row to see its series.`
    : `No regression reported across ${response.results.length} queries. Select a row to see its series.`);

  els.chartBox = h('div', { class: 'pgrr-chart' });

  const lastRows = request.snapshots[request.snapshots.length - 1].rows;
  const pre = h('pre', { class: 'pgrr-json', hidden: true, id: 'pgrr-json' });
  pre.innerHTML = highlightJson(JSON.stringify(lastRows, null, 2));
  const download = h('a', {
    class: 'pgrr-download', hidden: true, download: 'pg_stat_statements-snapshots.json',
    href: URL.createObjectURL(new Blob([JSON.stringify(request, null, 2)], { type: 'application/json' })),
  }, '$ download pg_stat_statements-snapshots.json');
  const rawComment = h('p', { class: 'pgrr-comment', hidden: true }, `${request.snapshots.length} snapshots of pg_stat_statements, one every 30 s of virtual time; the last one:`);
  const rawToggle = h('button', { type: 'button', class: 'pgrr-toggle', 'aria-expanded': 'false', 'aria-controls': 'pgrr-json',
    onclick: () => {
      const open = rawToggle.getAttribute('aria-expanded') !== 'true';
      rawToggle.setAttribute('aria-expanded', String(open));
      rawToggle.querySelector('.pgrr-chev').textContent = open ? '▾' : '▸';
      pre.hidden = download.hidden = rawComment.hidden = !open;
    } },
  h('span', { class: 'pgrr-ps' }, '$'), ' cat ', h('span', { class: 'pgrr-arg' }, 'snapshots.json'), ' ', h('span', { class: 'pgrr-chev', 'aria-hidden': 'true' }, '▸'),
  h('span', { class: 'pgrr-comment-inline' }, 'what the engine received'));

  els.results.replaceChildren(term('Output',
    prompt('playground ', h('span', { class: 'pgrr-cmd' }, 'report'), ' ', h('span', { class: 'pgrr-arg' }, state.custom ? 'custom' : state.scenario.id)),
    summary, table, els.chartBox, rawToggle, rawComment, pre, download));
  drawChart();
}

function select(queryid) {
  state.selected = queryid;
  for (const tr of els.results.querySelectorAll('tbody tr')) {
    const on = tr.querySelector('code').textContent === state.result.response.results.find((x) => x.queryid === queryid)?.query;
    tr.classList.toggle('pgrr-selected', on);
    tr.setAttribute('aria-selected', String(on));
  }
  drawChart();
}

// ---- chart --------------------------------------------------------------

function niceMax(v) {
  const pow = 10 ** Math.floor(Math.log10(v || 1));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * pow >= v) return m * pow;
  return 10 * pow;
}

function drawChart() {
  const { response, request } = state.result;
  const res = response.results.find((x) => x.queryid === state.selected);
  const series = response.series.find((x) => x.queryid === state.selected);
  if (!res || !series) { els.chartBox.replaceChildren(); return; }

  const W = 720, H = 300, m = { l: 56, r: 30, t: 28, b: 44 };
  const t0 = series.points[0].at, t1 = series.points[series.points.length - 1].at;
  const xs = (t) => m.l + ((t - t0) / (t1 - t0)) * (W - m.l - m.r);
  const interval = series.points.filter((p) => p.intervalKnown && p.intervalCalls > 0);
  const cumulative = series.points.filter((p) => p.cumulativeMeanMs > 0);
  const ymax = niceMax(Math.max(...interval.map((p) => p.intervalMeanMs), ...cumulative.map((p) => p.cumulativeMeanMs), 0.001) * 1.08);
  const ys = (v) => H - m.b - (v / ymax) * (H - m.t - m.b);
  const path = (pts, key) => pts.map((p, i) => `${i ? 'L' : 'M'}${xs(p.at).toFixed(1)} ${ys(p[key]).toFixed(1)}`).join(' ');

  const g = svg('svg', { viewBox: `0 0 ${W} ${H}`, role: 'img', class: 'pgrr-svg', 'aria-label': `Latency of ${res.query} over time, cumulative mean versus per-interval mean, with the deploy marked` });
  for (let i = 0; i <= 4; i++) {
    const v = (ymax * i) / 4;
    g.append(svg('line', { x1: m.l, x2: W - m.r, y1: ys(v), y2: ys(v), class: 'pgrr-grid-line' }));
    g.append(svg('text', { x: m.l - 8, y: ys(v) + 4, class: 'pgrr-axis', 'text-anchor': 'end' }, fmtMs(v)));
  }
  g.append(svg('text', { x: 14, y: H / 2, class: 'pgrr-axis', transform: `rotate(-90 14 ${H / 2})`, 'text-anchor': 'middle' }, 'mean latency (ms)'));
  const mins = Math.round((t1 - t0) / 60000);
  for (let mnt = 0; mnt <= mins; mnt += 2) {
    const x = xs(t0 + mnt * 60000);
    const rel = Math.round((t0 + mnt * 60000 - request.deployAt) / 60000);
    g.append(svg('text', { x, y: H - m.b + 18, class: 'pgrr-axis', 'text-anchor': 'middle' }, rel > 0 ? `+${rel}` : String(rel)));
  }
  g.append(svg('text', { x: (m.l + W - m.r) / 2, y: H - 6, class: 'pgrr-axis', 'text-anchor': 'middle' }, 'minutes from the deploy'));

  const vline = (t, cls, label, dy) => {
    g.append(svg('line', { x1: xs(t), x2: xs(t), y1: m.t - 6, y2: H - m.b, class: cls }));
    g.append(svg('text', { x: xs(t) + 5, y: m.t + dy, class: 'pgrr-mark' }, label));
  };
  vline(request.deployAt, 'pgrr-deploy-line', 'deploy', 0);
  if (res.detectedChangeAt) vline(res.detectedChangeAt, 'pgrr-detect-line', 'change point found', 14);

  g.append(svg('path', { d: path(cumulative, 'cumulativeMeanMs'), class: 'pgrr-line-cum' }));
  g.append(svg('path', { d: path(interval, 'intervalMeanMs'), class: 'pgrr-line-int' }));
  for (const p of series.points.filter((q) => q.reset)) {
    const x = xs(p.at);
    g.append(svg('path', { d: `M${x} ${H - m.b - 2} l-6 -11 h12 z`, class: 'pgrr-reset' }, svg('title', {}, 'pg_stat_statements was reset here')));
  }

  const legend = h('ul', { class: 'pgrr-legend' },
    h('li', {}, h('i', { class: 'pgrr-sw pgrr-sw-int' }), 'mean of the calls since the previous scrape (what the radar analyses)'),
    h('li', {}, h('i', { class: 'pgrr-sw pgrr-sw-cum' }), 'pg_stat_statements.mean_exec_time (cumulative since the last reset)'),
    series.points.some((p) => p.reset) && h('li', {}, h('i', { class: 'pgrr-sw pgrr-sw-reset' }), 'counter reset detected'));
  els.chartBox.replaceChildren(h('p', { class: 'pgrr-comment pgrr-chart-title' }, res.query), g, legend);
}

// ---- run ----------------------------------------------------------------

const BAR_CELLS = 24;
const barText = (done, total) => {
  const filled = Math.round((done / total) * BAR_CELLS);
  return `${'█'.repeat(filled)}${'░'.repeat(BAR_CELLS - filled)} ${done}/${total}`;
};

const SPIN = ['⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'];
let spinTimer;
function spin(on) {
  clearInterval(spinTimer);
  if (!on) return;
  const still = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  let i = 0;
  els.bar.textContent = still ? '…' : SPIN[0];
  if (!still) spinTimer = setInterval(() => { els.bar.textContent = SPIN[(i = (i + 1) % SPIN.length)]; }, 90);
}

function setStatus(text) {
  els.status.textContent = text ? `> ${text}` : '';
}

async function run() {
  if (state.running) return;
  state.running = true;
  els.run.disabled = true;
  els.bar.hidden = false;
  els.bar.removeAttribute('aria-valuenow');
  els.bar.removeAttribute('aria-valuemax');
  try {
    setStatus('Loading PostgreSQL and the detection engine (first run only)…');
    spin(true);
    const [pg] = await Promise.all([loadPostgres(), loadEngine()]);
    spin(false);

    let lastPhase = '';
    const request = await simulate(pg, { ...state.params }, ({ done, total, phase }) => {
      els.bar.setAttribute('aria-valuemax', String(total));
      els.bar.setAttribute('aria-valuenow', String(done));
      els.bar.textContent = barText(done, total);
      if (phase !== lastPhase) { lastPhase = phase; setStatus(phase); }
    });
    setStatus('Analysing with the correlation engine…');
    const response = JSON.parse(globalThis.pgrrAnalyse(JSON.stringify(request)));
    state.result = { request, response };
    state.selected = (response.results?.find((x) => x.status === 'Detected') ?? response.results?.[0])?.queryid ?? null;
    renderResults();
    const expected = !state.custom && state.scenario.expected;
    setStatus(expected ? `Done. This scenario is expected to report ${expected}.` : 'Done.');
  } catch (err) {
    console.error(err);
    spin(false);
    setStatus(`Something went wrong: ${err.message || err}`);
  } finally {
    state.running = false;
    els.run.disabled = false;
    els.bar.hidden = true;
  }
}

function init() {
  if (!ROOT) return;
  ROOT.replaceChildren();
  els.form = h('div', { class: 'pgrr-form' });
  els.results = h('div', { class: 'pgrr-results', 'aria-live': 'polite' });
  ROOT.append(els.form, els.results);
  renderForm();
}
init();
