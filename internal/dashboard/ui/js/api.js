// The only file that talks to the network. Everything it returns is the Observe
// API's own JSON; nothing is cached here, so what is on screen is what was asked.

async function getJSON(path) {
  const r = await fetch(path, { headers: { Accept: 'application/json' }, cache: 'no-store' });
  if (!r.ok) {
    let why = r.statusText, body = null;
    try { body = await r.json(); why = body.error || why; if (body.detail) why += ` (${body.detail})`; } catch (_) { /* not JSON */ }
    const e = new Error(`${path}: ${r.status} ${why}`);
    e.status = r.status;
    e.body = body;
    throw e;
  }
  return r.json();
}

const SUPPORTED = ['v1'];

export const getConfig = () => getJSON('/dashboard/config');

// Both sides must speak the same API version; guessing would show wrong numbers.
export async function checkVersion() {
  const v = await getJSON('/observe/version');
  const common = (v.supported || []).find((x) => SUPPORTED.includes(x));
  if (!common) {
    throw new Error(`This dashboard understands API ${SUPPORTED.join(', ')}; the manager offers ${(v.supported || []).join(', ') || 'none'}. Use a dashboard of the same release as the manager.`);
  }
  return common;
}

// The boards to show: the layout, where it came from, the panel catalog and the
// grid geometry. A dashboard file with problems is answered 422 with the problems,
// each with its line, so the page can show them instead of a blank board.
export async function layoutBundle() {
  try {
    return await getJSON('/dashboard/layout');
  } catch (e) {
    if (e.status === 422 && e.body && Array.isArray(e.body.problems)) { e.problems = e.body.problems; e.message = 'The dashboard file has problems.'; }
    throw e;
  }
}

// How the connection to the manager stands (only meaningful for a cluster).
export const linkStatus = () => getJSON('/dashboard/status');

export function client(config) {
  const q = config.showQueries ? 'queries=1' : '';
  const join = (path, ...parts) => {
    const qs = parts.filter(Boolean).join('&');
    return qs ? `${path}?${qs}` : path;
  };
  const id = (x) => encodeURIComponent(x);
  return {
    overview: () => getJSON('/observe/v1/overview'),
    // range is {from, to?} as range.js's query() gives it.
    deploys: (range) => getJSON(join('/observe/v1/deploys', range && range.from && `from=${encodeURIComponent(range.from)}`, range && range.to && `to=${encodeURIComponent(range.to)}`)).then((r) => r.deploys),
    deploy: (x) => getJSON(join(`/observe/v1/deploys/${id(x)}`, q)),
    series: (x, unit) => getJSON(join(`/observe/v1/deploys/${id(x)}/series`, `unit=${encodeURIComponent(unit)}`)),
  };
}

// Server-Sent Events. A gap in the sequence, or a reconnection, means events may
// have been missed, so the caller is told to read everything again: the stream
// carries no evidence, only "something changed here".
export function events({ onChange, onState }) {
  let es, last = 0, closed = false;
  const types = ['deploy.registered', 'evaluation', 'action', 'health'];
  const open = () => {
    es = new EventSource('/observe/v1/events');
    es.addEventListener('hello', () => { last = 0; onState('live'); onChange({ resync: true }); });
    for (const t of types) {
      es.addEventListener(t, (m) => {
        const e = JSON.parse(m.data);
        const gap = last !== 0 && e.seq !== last + 1;
        last = e.seq;
        onChange({ event: e, resync: gap });
      });
    }
    es.onerror = () => { onState('reconnecting'); };
  };
  open();
  return () => { closed = true; if (es) es.close(); void closed; };
}

// What the command keeps for this person between runs. `saved` is false when the
// command was started with --no-save or could not use its data directory; then the
// page keeps edits in the browser instead.
export async function getProfile() {
  try {
    const p = await getJSON('/dashboard/profile');
    return p && p.saved ? p : { saved: false };
  } catch (_) {
    return { saved: false };
  }
}

// The one thing the page ever writes, and only to its own server. The marker header
// is how the server knows the request is this page's (a page on another site cannot
// send it without the browser asking the server first, which it never allows).
async function writeProfile(method, body, leaving) {
  const r = await fetch('/dashboard/profile', {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Requested-By': 'pgrr-dashboard' },
    body: body === undefined ? undefined : JSON.stringify(body),
    keepalive: !!leaving, // lets the last save finish while the page is closing
  });
  if (!r.ok) {
    let why = r.statusText;
    try { const j = await r.json(); why = j.error || why; } catch (_) { /* not JSON */ }
    throw new Error(`${r.status} ${why}`);
  }
}
export const putProfile = (profile, leaving) => writeProfile('PUT', profile, leaving);
export const forgetProfile = (leaving) => writeProfile('DELETE', undefined, leaving);
