// The grid, as pure functions: no DOM, so it runs under `node --test`.
//
// A layout is { boards: [{ id, title, range, panels: [{ id, type, at: [x, y, w, h], title?, description?, options? }] }] }
// on a grid of COLS columns and rows of fixed height. Moving or resizing one
// panel pushes the ones in its way down, and every panel then rises as far as it
// can, as in Grafana: a layout is always gap-free upward and never overlaps, so
// there is nothing to "fix up" by hand.

import { parse as parseQuery } from './query.js';

export const COLS = 12;
export const MAX_ROWS = 60;

const X = 0, Y = 1, W = 2, H = 3;

export const hit = (a, b) => a[X] < b[X] + b[W] && b[X] < a[X] + a[W] && a[Y] < b[Y] + b[H] && b[Y] < a[Y] + a[H];

export const byPosition = (a, b) => a.at[Y] - b.at[Y] || a.at[X] - b.at[X] || (a.id < b.id ? -1 : 1);

// Every panel as high as it can go without touching one placed before it.
export function compact(panels) {
  const placed = [];
  const out = new Map();
  for (const p of [...panels].sort(byPosition)) {
    const at = [...p.at];
    while (at[Y] > 0 && !placed.some((q) => hit([at[X], at[Y] - 1, at[W], at[H]], q.at))) at[Y]--;
    const np = { ...p, at };
    placed.push(np);
    out.set(p.id, np);
  }
  return panels.map((p) => out.get(p.id));
}

// Keeps a size and position inside the grid and above the type's minimum.
export function clamp(at, min = { w: 1, h: 1 }, cols = COLS) {
  let [x, y, w, h] = at.map((v) => Math.round(v));
  w = Math.min(cols, Math.max(min.w, w));
  h = Math.min(MAX_ROWS, Math.max(min.h, h));
  x = Math.min(cols - w, Math.max(0, x));
  y = Math.min(MAX_ROWS - h, Math.max(0, y));
  return [x, y, w, h];
}

// Puts panel `id` at `at` (clamped), pushes whatever is in its way below it and
// lets everything settle upward. `minOf(panel)` gives {w, h}.
export function place(panels, id, at, minOf = () => ({ w: 1, h: 1 })) {
  const moving = panels.find((p) => p.id === id);
  if (!moving) return panels;
  const target = { ...moving, at: clamp(at, minOf(moving)) };
  const fixed = [target];
  const pushed = new Map([[id, target]]);
  for (const p of [...panels].filter((q) => q.id !== id).sort(byPosition)) {
    const np = { ...p, at: [...p.at] };
    // Below the lowest thing it touches, repeatedly, until it touches nothing.
    for (let again = true; again;) {
      again = false;
      for (const q of fixed) {
        if (hit(np.at, q.at)) { np.at[Y] = q.at[Y] + q.at[H]; again = true; }
      }
    }
    fixed.push(np);
    pushed.set(p.id, np);
  }
  return compact(panels.map((p) => pushed.get(p.id)));
}

// The bottom edge of a layout, in rows.
export const bottom = (panels) => panels.reduce((m, p) => Math.max(m, p.at[Y] + p.at[H]), 0);

// The empty cells of a layout, as rectangles [x, y, w, h, sealed]: what a column that
// ends above its neighbour leaves under it. The board draws their edges so that every
// cell of the sheet is closed, and so that what is empty reads as empty rather than as
// a card that was cut off. `sealed` says whether the rectangle needs a line along its
// bottom: it does at the bottom of the layout and over a panel, and does not over
// another empty cell, where the line would cut one empty area in two. Only rows above
// the layout's bottom are looked at.
export function holes(panels, cols = COLS) {
  const rows = bottom(panels);
  const full = Array.from({ length: rows }, () => new Array(cols).fill(false));
  for (const p of panels) {
    for (let y = p.at[Y]; y < Math.min(p.at[Y] + p.at[H], rows); y++) {
      for (let x = p.at[X]; x < Math.min(p.at[X] + p.at[W], cols); x++) full[y][x] = true;
    }
  }
  const out = [];
  let open = new Map(); // "x:w" -> the rectangle still growing downwards
  for (let y = 0; y < rows; y++) {
    const next = new Map();
    for (let x = 0; x < cols;) {
      if (full[y][x]) { x++; continue; }
      let w = 0;
      while (x + w < cols && !full[y][x + w]) w++;
      const key = `${x}:${w}`;
      const r = open.get(key);
      if (r) { r[H]++; next.set(key, r); } else { const n = [x, y, w, 1, true]; out.push(n); next.set(key, n); }
      x += w;
    }
    open = next;
  }
  for (const r of out) {
    const below = r[Y] + r[H];
    r[4] = below >= rows || full[below].slice(r[X], r[X] + r[W]).some(Boolean);
  }
  return out;
}

// A fresh id for a type on a board: the type's name, then name-2, name-3.
export function freshId(panels, type) {
  const taken = new Set(panels.map((p) => p.id));
  if (!taken.has(type)) return type;
  for (let i = 2; ; i++) if (!taken.has(`${type}-${i}`)) return `${type}-${i}`;
}

// Adds a panel of a catalog type at the bottom, full to its default width.
export function add(panels, pt) {
  const w = Math.min(COLS, pt.w);
  return [...panels, { id: freshId(panels, pt.type), type: pt.type, at: [0, bottom(panels), w, pt.h] }];
}

export const remove = (panels, id) => compact(panels.filter((p) => p.id !== id));

// ---- reading back what was saved ------------------------------------------------

// A saved or received layout, made safe: only known panel types survive, each at
// a size that fits the grid and its type. Anything else is dropped rather than
// repaired, because a half-valid layout would be shown as if it were whole.
export function sanitize(layout, catalog) {
  const types = new Map(catalog.map((c) => [c.type, c]));
  if (!layout || !Array.isArray(layout.boards)) return null;
  const seen = new Set();
  const boards = [];
  for (const b of layout.boards) {
    if (!b || typeof b.id !== 'string' || seen.has(b.id) || !Array.isArray(b.panels)) return null;
    seen.add(b.id);
    const ids = new Set();
    const panels = [];
    for (const p of b.panels) {
      const pt = p && types.get(p.type);
      if (!pt || typeof p.id !== 'string' || ids.has(p.id) || !Array.isArray(p.at) || p.at.length !== 4 || !p.at.every(Number.isFinite)) return null;
      ids.add(p.id);
      const q = { id: p.id, type: p.type, at: clamp(p.at, { w: pt.minW, h: pt.minH }) };
      if (typeof p.title === 'string' && p.title) q.title = p.title;
      if (typeof p.description === 'string' && p.description) q.description = p.description;
      if (typeof p.query === 'string' && queryFits(p.query, pt)) q.query = p.query;
      const opts = cleanOptions(p.options, pt);
      if (Object.keys(opts).length) q.options = opts;
      panels.push(q);
    }
    boards.push({ id: b.id, title: String(b.title || b.id), range: String(b.range || '6h'), panels: compact(panels) });
  }
  return boards.length ? { apiVersion: layout.apiVersion, kind: layout.kind, boards } : null;
}

// ---- a panel's own settings --------------------------------------------------------

export const MAX_TITLE = 80;
export const MAX_DESCRIPTION = 300;

export const optionDef = (pt, name) => ((pt && pt.options) || []).find((o) => o.name === name) || null;

// Whether `v` is something option `o` accepts: the same rule the Go validator applies.
export function validOption(o, v) {
  if (!o) return false;
  if (o.kind === 'bool') return typeof v === 'boolean';
  if (o.kind === 'int') return Number.isInteger(v) && v >= o.min && v <= o.max;
  if (o.kind === 'enum') return typeof v === 'string' && o.values.includes(v);
  return false;
}

// What a panel was given, with unknown or invalid settings dropped.
export function cleanOptions(opts, pt) {
  const out = {};
  if (!opts || typeof opts !== 'object') return out;
  for (const o of (pt && pt.options) || []) if (o.name in opts && validOption(o, opts[o.name])) out[o.name] = opts[o.name];
  return out;
}

// Every setting of a panel with the type's default where the panel says nothing.
export function effectiveOptions(p, pt) {
  const out = {};
  for (const o of (pt && pt.options) || []) out[o.name] = o.default;
  return Object.assign(out, cleanOptions(p.options, pt));
}

// The panel with one setting changed. A value equal to the default is not stored,
// so a panel that was put back looks untouched and the file stays short.
export function setOption(p, pt, name, value) {
  const o = optionDef(pt, name);
  if (!validOption(o, value)) return p;
  const options = { ...(p.options || {}) };
  if (value === o.default) delete options[name]; else options[name] = value;
  const { options: _drop, ...rest } = p;
  void _drop;
  return Object.keys(options).length ? { ...rest, options } : rest;
}

// The panel with its title and/or description changed; empty text removes it.
export function setText(p, field, text) {
  const max = field === 'title' ? MAX_TITLE : MAX_DESCRIPTION;
  const v = String(text || '').replace(/\s+$/g, '').slice(0, max);
  const { [field]: _drop, ...rest } = p;
  void _drop;
  return v.trim() ? { ...rest, [field]: v } : rest;
}

// A panel with its own title, description, query and settings cleared (position stays).
export function resetPanel(p) {
  const { title, description, query, options, ...rest } = p;
  void title; void description; void query; void options;
  return rest;
}

// Whether a query reads and uses only fields its panel type takes (the file's own
// validator is stricter about values; a layout the server served already passed it).
export function queryFits(text, pt) {
  const { terms, error } = parseQuery(text);
  const takes = (pt && pt.query) || [];
  return !error && terms.length > 0 && terms.every((t) => takes.includes(t.field));
}

// The panel with its query changed; empty removes it.
export function setQuery(p, text) {
  const v = String(text || '').trim();
  const { query: _drop, ...rest } = p;
  void _drop;
  return v ? { ...rest, query: v } : rest;
}

// Changes when something a panel draws from changes, so the board knows to rebuild it.
export const panelSignature = (p) => JSON.stringify([p.type, p.title || '', p.description || '', p.query || '', p.options || {}]);

export const replacePanel = (panels, next) => panels.map((p) => (p.id === next.id ? next : p));

// ---- the address -------------------------------------------------------------------

// `#/board`, `#/case/<deploy>`, either with `?view=<panel>` when one panel is in focus.
export function parseHash(hash) {
  const [path, query = ''] = String(hash || '').split('?');
  const m = /^#\/([a-z][a-z0-9-]*)(?:\/(.+))?$/.exec(path);
  const view = new URLSearchParams(query).get('view');
  let deploy = null;
  if (m && m[2]) { try { deploy = decodeURIComponent(m[2]); } catch (_) { deploy = null; } }
  return { board: m ? m[1] : null, deploy, view: view && /^[A-Za-z0-9_-]+$/.test(view) ? view : null };
}

export function hashFor({ board, deploy, view }) {
  let h = `#/${board}`;
  if (deploy) h += `/${encodeURIComponent(deploy)}`;
  if (view) h += `?view=${encodeURIComponent(view)}`;
  return h;
}

// ---- the dashboard file ------------------------------------------------------------

const PLAIN = /^[A-Za-z][A-Za-z0-9 _./()-]*$/;
const RESERVED = /^(true|false|yes|no|on|off|null|y|n|~)$/i;
const scalar = (s) => (PLAIN.test(s) && !RESERVED.test(s) && s === s.trim() ? s : JSON.stringify(s));

// The layout as the YAML file `dashboard --config` reads, written the way
// `dashboard config dump` writes it, so what is exported and what is shipped
// are the same shape and diff cleanly.
export function toYAML(layout, catalog = []) {
  const types = new Map(catalog.map((c) => [c.type, c]));
  const out = [`apiVersion: ${layout.apiVersion || 'pgrr.io/v1'}`, `kind: ${layout.kind || 'Dashboard'}`, 'boards:'];
  for (const b of layout.boards) {
    out.push(`  - id: ${b.id}`, `    title: ${scalar(b.title)}`, `    range: ${b.range}`, '    panels:');
    for (const p of b.panels) out.push(...panelLines(p, types.get(p.type)).map((l) => `      ${l}`));
  }
  return out.join('\n') + '\n';
}

// One panel as the lines of a `panels:` list item, so it can be pasted into a file.
function panelLines(p, pt) {
  const out = [`- id: ${p.id}`, `  type: ${p.type}`, `  at: [${p.at.join(', ')}]`];
  if (p.title) out.push(`  title: ${scalar(p.title)}`);
  if (p.description) out.push(`  description: ${scalar(p.description)}`);
  if (p.query) out.push(`  query: ${scalar(p.query)}`);
  const opts = optionLines(p, pt);
  if (opts.length) out.push('  options:', ...opts.map((l) => `    ${l}`));
  return out;
}

export const panelYAML = (p, catalog = []) => panelLines(p, catalog.find((c) => c.type === p.type)).join('\n') + '\n';

function optionLines(p, pt) {
  if (!p.options) return [];
  const order = pt ? pt.options.map((o) => o.name) : Object.keys(p.options).sort();
  return order.filter((k) => k in p.options).map((k) => {
    const v = p.options[k];
    return `${k}: ${typeof v === 'string' ? scalar(v) : String(v)}`;
  });
}

// A short stable fingerprint of a layout, to tell "the file changed" from "you edited".
export function fingerprint(layout) {
  const s = JSON.stringify(layout);
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 0x01000193) >>> 0; }
  return h.toString(16).padStart(8, '0');
}

// ---- boards ----------------------------------------------------------------------------

export const boardById = (layout, id) => layout.boards.find((b) => b.id === id) || null;

export function withBoard(layout, id, fn) {
  return { ...layout, boards: layout.boards.map((b) => (b.id === id ? fn(b) : b)) };
}
