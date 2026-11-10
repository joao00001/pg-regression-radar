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
};

function applyScenario(sc) {
  state.scenario = sc;
  state.custom = false;
  state.params = { ...DEFAULTS, deploySql: sc.deploySql, callsPerScrape: sc.callsPerScrape ?? DEFAULTS.callsPerScrape, resetAfterDeployScrapes: sc.resetAfterDeployScrapes ?? 0 };
}
applyScenario(SCENARIOS[0]);

// ---- rendering ----------------------------------------------------------

const els = {};

function field(label, input, hint) {
  return h('label', { class: 'pgrr-field' }, h('span', { class: 'pgrr-field-label' }, label), input, hint && h('span', { class: 'pgrr-hint' }, hint));
}

function numberInput(key, { min, max, step = 1 }) {
  return h('input', {
    type: 'number', min, max, step, value: state.params[key],
    onchange: (e) => { const v = Number(e.target.value); if (Number.isFinite(v)) state.params[key] = v; },
  });
}

function sqlArea(key, label, rows) {
  return h('label', { class: 'pgrr-field pgrr-wide' },
    h('span', { class: 'pgrr-field-label' }, label),
    h('textarea', {
      rows, spellcheck: 'false', value: state.params[key],
      oninput: (e) => { state.params[key] = e.target.value; state.custom = true; markCustom(); },
    }, state.params[key]));
}

function markCustom() {
  els.scenarioNote.textContent = state.custom ? 'Custom SQL: the verdict is whatever the engine decides.' : state.scenario.blurb;
  for (const b of els.scenarioButtons) b.setAttribute('aria-pressed', String(!state.custom && b.dataset.id === state.scenario.id));
}

function renderForm() {
  els.scenarioButtons = SCENARIOS.map((sc) => h('button', {
    type: 'button', class: 'pgrr-chip', 'data-id': sc.id, 'aria-pressed': String(sc.id === state.scenario.id),
    onclick: () => { applyScenario(sc); renderForm(); },
  }, sc.title));
  els.scenarioNote = h('p', { class: 'pgrr-note' }, state.scenario.blurb);

  els.run = h('button', { type: 'button', class: 'pgrr-run', onclick: run }, 'Run simulation');
  els.status = h('p', { class: 'pgrr-status', role: 'status', 'aria-live': 'polite' });
  els.bar = h('progress', { class: 'pgrr-progress', max: 1, value: 0, hidden: true });

  const form = h('div', { class: 'pgrr-form' },
    h('div', { class: 'pgrr-chips', role: 'group', 'aria-label': 'Scenario' }, els.scenarioButtons),
    els.scenarioNote,
    h('div', { class: 'pgrr-grid' },
      field('Calls per query, per 30 s scrape', numberInput('callsPerScrape', { min: 1, max: 100 }), 'Traffic'),
      field('Analysis window (min)', numberInput('windowMinutes', { min: 2, max: 30 }), 'Each side of the deploy'),
      field('Min samples', numberInput('minSamples', { min: 2, max: 100 }), '--min-samples'),
      field('Min executions', numberInput('minExecutions', { min: 1, max: 100000 }), '--min-executions'),
      field('Latency change threshold', numberInput('latencyChangeThreshold', { min: 0.01, max: 5, step: 0.01 }), 'Fraction, 0.2 = 20%'),
      field('p-value threshold', numberInput('pValueThreshold', { min: 0.001, max: 0.5, step: 0.001 }), 'Welch t-test')),
    h('details', {},
      h('summary', {}, 'Edit the SQL'),
      h('p', { class: 'pgrr-hint' }, 'Runs in a real PostgreSQL in your browser tab. :id becomes a random id on each call. One workload statement per line.'),
      sqlArea('setupSql', 'Schema and data', 14),
      sqlArea('workloadSql', 'Workload (one statement per line)', 5),
      sqlArea('deploySql', 'The deploy (runs halfway through)', 6)),
    h('div', { class: 'pgrr-actions' }, els.run, els.bar, els.status));
  els.form.replaceChildren(form);
}

function badge(status) {
  return h('span', { class: `pgrr-badge pgrr-${status}` }, status);
}

const fmtMs = (v) => (v >= 100 ? v.toFixed(0) : v >= 1 ? v.toFixed(2) : v.toFixed(3));

function renderResults() {
  const r = state.result;
  if (!r) { els.results.replaceChildren(); return; }
  const { response, request } = r;
  if (response.error) {
    els.results.replaceChildren(h('p', { class: 'pgrr-error' }, `Engine error: ${response.error}`));
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
  h('td', { class: 'pgrr-num' }, res.status === 'InsufficientData' ? '-' : `${fmtMs(res.meanLatencyBefore)} → ${fmtMs(res.meanLatencyAfter)} ms`),
  h('td', { class: 'pgrr-num' }, res.status === 'InsufficientData' ? '-' : `×${res.latencyChangeFactor.toFixed(2)}`),
  h('td', { class: 'pgrr-num' }, res.status === 'Detected' ? res.confidenceScore.toFixed(3) : '-')));

  const table = h('table', { class: 'pgrr-table' },
    h('thead', {}, h('tr', {}, ...['Verdict', 'Query', 'Mean before → after', 'Change', 'Confidence'].map((t) => h('th', {}, t)))),
    h('tbody', {}, rows));

  const detected = response.results.filter((x) => x.status === 'Detected').length;
  const summary = h('p', { class: 'pgrr-summary' }, detected
    ? `${detected} of ${response.results.length} queries flagged. Select a row to see its series.`
    : `No regression reported across ${response.results.length} queries. Select a row to see its series.`);

  const chartBox = h('div', { class: 'pgrr-chart' });
  els.chartBox = chartBox;

  const lastRows = request.snapshots[request.snapshots.length - 1].rows;
  const download = h('a', {
    class: 'pgrr-download', download: 'pg_stat_statements-snapshots.json',
    href: URL.createObjectURL(new Blob([JSON.stringify(request, null, 2)], { type: 'application/json' })),
  }, 'Download the snapshots (JSON)');
  const raw = h('details', {},
    h('summary', {}, 'What the engine received'),
    h('p', { class: 'pgrr-hint' }, `${request.snapshots.length} snapshots of pg_stat_statements, one every 30 s of virtual time. The last one:`),
    h('pre', {}, JSON.stringify(lastRows, null, 2)),
    download);

  els.results.replaceChildren(summary, table, chartBox, raw);
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
    h('li', {}, h('i', { class: 'pgrr-sw pgrr-sw-int' }), 'Mean of the calls since the previous scrape (what the radar analyses)'),
    h('li', {}, h('i', { class: 'pgrr-sw pgrr-sw-cum' }), 'pg_stat_statements.mean_exec_time (cumulative since the last reset)'),
    series.points.some((p) => p.reset) && h('li', {}, h('i', { class: 'pgrr-sw pgrr-sw-reset' }), 'Counter reset detected'));
  els.chartBox.replaceChildren(h('code', { class: 'pgrr-chart-title' }, res.query), g, legend);
}

// ---- run ----------------------------------------------------------------

function setStatus(text, busy = false) {
  els.status.textContent = text;
  els.status.classList.toggle('pgrr-busy', busy);
}

async function run() {
  if (state.running) return;
  state.running = true;
  els.run.disabled = true;
  els.bar.hidden = false;
  els.bar.removeAttribute('value'); // indeterminate while loading
  try {
    setStatus('Loading PostgreSQL and the detection engine (first run only)…', true);
    const [pg] = await Promise.all([loadPostgres(), loadEngine()]);

    const request = await simulate(pg, { ...state.params }, ({ done, total, phase }) => {
      els.bar.max = total; els.bar.value = done;
      setStatus(`${phase}: scrape ${done} of ${total}`, true);
    });
    setStatus('Analysing with the correlation engine…', true);
    const response = JSON.parse(globalThis.pgrrAnalyse(JSON.stringify(request)));
    state.result = { request, response };
    state.selected = (response.results?.find((x) => x.status === 'Detected') ?? response.results?.[0])?.queryid ?? null;
    renderResults();
    const expected = !state.custom && state.scenario.expected;
    setStatus(expected ? `Done. This scenario is expected to report ${expected}.` : 'Done.');
  } catch (err) {
    console.error(err);
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
  els.form = h('div');
  els.results = h('div', { class: 'pgrr-results', 'aria-live': 'polite' });
  ROOT.append(els.form, els.results);
  renderForm();
}
init();
