// A panel's query: which of the data the Observe API already serves the panel
// shows. A few `field:value` terms, read by the same grammar as the Go side
// (layout/query.go; testdata/query-cases.json is the contract both are tested
// against). Nothing here fetches or reaches anything the API does not serve.
//
//   app:checkout state:regressed          deploys of one app that regressed
//   deploy:abc query:"SELECT on orders"   one deploy, one database query
//   statement:UPDATE table:orders*        a set of queries
import { tone, gradeOf, labelUnits, queryLabel, defaultUnit, clock } from './model.js';

export const MAX_QUERY = 200;
export const MAX_TERMS = 12;

const MESSAGES = {
  'bad-term': (c) => `column ${c}: expected field:value, such as app:checkout`,
  'empty-value': (c) => `column ${c}: this field has no value`,
  'unterminated-quote': (c) => `column ${c}: this quote is never closed`,
  'stray-quote': (c) => `column ${c}: a value with a quote in it must be wholly in quotes`,
  'bad-escape': (c) => `column ${c}: inside quotes only \\" and \\\\ can follow a backslash`,
  'too-long': () => `a query is at most ${MAX_QUERY} characters`,
  'too-many': () => `a query has at most ${MAX_TERMS} terms`,
};

// Reads the grammar: {terms: [{field, value, col}], error: null | {code, col, message}}.
// Columns count characters, not UTF-16 units, as the Go side does.
export function parse(text) {
  const r = Array.from(String(text || ''));
  const fail = (code, col) => ({ terms: [], error: { code, col, message: MESSAGES[code](col) } });
  if (r.length > MAX_QUERY) return fail('too-long', 1);
  const space = (c) => c === ' ' || c === '\t' || c === '\n' || c === '\r';
  const terms = [];
  let i = 0;
  for (;;) {
    while (i < r.length && space(r[i])) i++;
    if (i >= r.length) break;
    const start = i;
    while (i < r.length && r[i] >= 'a' && r[i] <= 'z') i++;
    if (i === start || i >= r.length || r[i] !== ':') return fail('bad-term', start + 1);
    const field = r.slice(start, i).join('');
    i++;
    const valStart = i;
    const val = [];
    if (r[i] === '"') {
      i++;
      let closed = false;
      while (i < r.length) {
        const c = r[i];
        if (c === '\\') {
          if (i + 1 >= r.length || (r[i + 1] !== '"' && r[i + 1] !== '\\')) return fail('bad-escape', i + 1);
          val.push(r[i + 1]);
          i += 2;
          continue;
        }
        if (c === '"') { closed = true; i++; break; }
        val.push(c);
        i++;
      }
      if (!closed) return fail('unterminated-quote', valStart + 1);
      if (i < r.length && !space(r[i])) return fail('bad-term', i + 1);
    } else {
      while (i < r.length && !space(r[i])) {
        if (r[i] === '"') return fail('stray-quote', i + 1);
        val.push(r[i]);
        i++;
      }
    }
    if (!val.length) return fail('empty-value', start + 1);
    terms.push({ field, value: val.join(''), col: start + 1 });
    if (terms.length > MAX_TERMS) return fail('too-many', start + 1);
  }
  return { terms, error: null };
}

// A value as it is written in a query: bare when it can be, quoted otherwise.
export const quote = (v) => (/^[^\s"\\]+$/.test(v) ? v : `"${v.replace(/[\\"]/g, '\\$&')}"`);

// What is wrong with a parsed query for a panel type, as messages ([] if nothing).
export function validate(pt, fields, terms) {
  const takes = (pt && pt.query) || [];
  if (!takes.length) return terms.length ? [`column ${terms[0].col}: a ${pt ? pt.type : 'this'} panel takes no query`] : [];
  const out = [];
  let deploys = 0;
  for (const t of terms) {
    const f = fields.find((x) => x.name === t.field);
    if (!f || !takes.includes(t.field)) { out.push(`column ${t.col}: a ${pt.type} panel has no "${t.field}" field; it takes ${takes.join(', ')}`); continue; }
    if (f.values && !f.values.some((v) => v.toLowerCase() === t.value.toLowerCase())) out.push(`column ${t.col}: ${f.name} is one of ${f.values.join(', ')}, not "${t.value}"`);
    if (f.values && t.value.includes('*')) out.push(`column ${t.col}: ${f.name} takes exact values; * works only in free text`);
    if (f.name === 'deploy') {
      if (++deploys > 1) out.push(`column ${t.col}: a panel shows one deploy; give deploy: once`);
      if (t.value.includes('*')) out.push(`column ${t.col}: deploy takes an exact id`);
    }
  }
  return out;
}

// ---- matching ----------------------------------------------------------------------

// Case-blind equality where * stands for any characters.
function matcher(value) {
  const v = value.toLowerCase();
  if (!v.includes('*')) return (s) => String(s).toLowerCase() === v;
  const re = new RegExp(`^${v.split('*').map((p) => p.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('.*')}$`);
  return (s) => re.test(String(s).toLowerCase());
}

// Same field: either value; different fields: all of them.
function byField(terms) {
  const m = new Map();
  for (const t of terms) { if (!m.has(t.field)) m.set(t.field, []); m.get(t.field).push(matcher(t.value)); }
  return m;
}
const holds = (ms, ...candidates) => ms.some((f) => candidates.some((c) => f(c)));

export const STATES = ['regressed', 'acted', 'watching', 'healthy', 'inconclusive'];
const STATE_OF = { regressed: 'regressed', acted: 'acted', watching: 'watching', ok: 'healthy', quiet: 'inconclusive' };
export const stateName = (d) => STATE_OF[tone(d)];

const DEPLOY_FIELDS = ['app', 'namespace', 'state', 'grade'];
export const filtersDeploys = (terms) => terms.some((t) => DEPLOY_FIELDS.includes(t.field));
export const filtersUnits = (terms) => terms.some((t) => ['query', 'statement', 'table'].includes(t.field));

// The deploys the query describes.
export function selectDeploys(deploys, terms) {
  const by = byField(terms.filter((t) => DEPLOY_FIELDS.includes(t.field)));
  if (!by.size) return deploys || [];
  return (deploys || []).filter((d) =>
    (!by.has('app') || holds(by.get('app'), d.app)) && (!by.has('namespace') || holds(by.get('namespace'), d.namespace))
    && (!by.has('state') || holds(by.get('state'), stateName(d))) && (!by.has('grade') || holds(by.get('grade'), gradeOf(d))));
}

const statementOf = (u) => String(u.family || '').split(':')[0];
const tablesOf = (u) => (String(u.family || '').includes(':') ? u.family.slice(u.family.indexOf(':') + 1).split(/[,\s]+/).filter(Boolean) : []);

// The queries (units) of a list that the query describes.
export function selectUnits(units, terms) {
  const by = byField(terms.filter((t) => ['query', 'statement', 'table'].includes(t.field)));
  if (!by.size) return units || [];
  const names = labelUnits(units);
  return (units || []).filter((u) =>
    (!by.has('query') || holds(by.get('query'), names.get(u.key), queryLabel(u.family, u.key), u.key.slice(0, 8), u.key))
    && (!by.has('statement') || holds(by.get('statement'), statementOf(u)))
    && (!by.has('table') || holds(by.get('table'), ...tablesOf(u))));
}

const lastOf = (c) => c && c.history && c.history[c.history.length - 1];

// Which deploy a panel shows: the one named, else the newest that matches the
// filters, else the one picked on the board. `note` says why there is none.
export function resolveDeploy(view, terms) {
  const named = terms.find((t) => t.field === 'deploy');
  if (named) {
    const c = view.cases.get(named.value);
    return c ? { kase: c, source: 'named', note: '' } : { kase: null, source: 'named', note: `No deploy "${named.value}" in this time range.` };
  }
  if (filtersDeploys(terms)) {
    const list = selectDeploys(view.deploys, terms).slice().sort((a, b) => (Date.parse(b.at) || 0) - (Date.parse(a.at) || 0));
    const hit = list.map((d) => view.cases.get(d.id)).find(Boolean);
    return hit ? { kase: hit, source: 'matched', note: '' } : { kase: null, source: 'matched', note: 'No deploy in this time range matches the query of this panel.' };
  }
  return { kase: view.kase, source: 'picked', note: '' };
}

// The same case with only the queries the query describes in each evaluation.
function narrow(c, terms) {
  if (!c || !filtersUnits(terms)) return c;
  return { ...c, history: (c.history || []).map((e) => ({ ...e, units: selectUnits(e.units, terms) })) };
}

// The view a panel is drawn from: what it was given, cut down to what its query says.
// `follows` is true for a panel on one deploy. `pin` is the person's own choice of
// query in this panel, kept while the query still lets it through. `viewSeries(id, key)` gives {series, state} for a query the
// board is not already drawing.
export function scope(view, terms, { pin = null, policy = 'first', follows = false, wantsSeries = false } = {}) {
  if (!terms.length) return view;
  const out = { ...view };
  out.deploys = selectDeploys(view.deploys, terms);
  const keep = new Set(out.deploys.map((d) => d.id));
  if (filtersDeploys(terms)) out.cases = new Map([...view.cases].filter(([id]) => keep.has(id)));
  // A list of deploys is only filtered; it does not change which deploy the board has picked.
  if (!follows) return out;
  const r = resolveDeploy(view, terms);
  out.scopeNote = r.note;
  out.kase = narrow(r.kase, terms);
  if (r.source !== 'picked') out.selected = r.kase ? r.kase.id : null;
  const last = lastOf(out.kase);
  const units = (last && last.units) || [];
  if (r.source === 'picked' && !filtersUnits(terms)) return out;
  // Not the board's deploy or query: this panel chooses its own.
  let unit = pin && units.some((u) => u.key === pin) ? pin : null;
  if (!unit && r.source === 'picked' && units.some((u) => u.key === view.unit)) unit = view.unit;
  if (!unit) unit = defaultUnit(units, policy);
  out.unit = unit;
  if (wantsSeries && out.kase && unit) {
    if (out.kase.id === view.kase?.id && unit === view.unit) { out.series = view.series; out.seriesMissing = view.seriesMissing; }
    else { const s = view.seriesFor ? view.seriesFor(out.kase.id, unit) : { series: null, state: 'missing' }; out.series = s.series; out.seriesMissing = s.state === 'missing'; }
  } else if (!unit) { out.series = null; out.seriesMissing = false; }
  return out;
}

// ---- what the query came to, in words -----------------------------------------------------

export function outcome(pt, view, terms) {
  const takes = (pt && pt.query) || [];
  if (!takes.length) return { text: '', empty: true };
  const listy = !pt.followsDeploy;
  if (listy) {
    const n = selectDeploys(view.deploys, terms).length;
    const all = (view.deploys || []).length;
    return { text: terms.length ? `${n} of ${all} ${all === 1 ? 'deploy' : 'deploys'}` : `all ${all} ${all === 1 ? 'deploy' : 'deploys'}`, empty: false };
  }
  const r = resolveDeploy(view, terms);
  if (!r.kase) return { text: r.note, empty: false, bad: true };
  const c = r.kase;
  const who = `${c.app} · ${c.namespace} · ${clock(Date.parse(c.at))}`;
  const how = { picked: 'the deploy picked on the board', named: 'the deploy named', matched: 'the newest deploy that matches' }[r.source];
  let text = `${who}, ${how}`;
  if (filtersUnits(terms)) {
    const all = (lastOf(c) && lastOf(c).units) || [];
    text += `; ${selectUnits(all, terms).length} of ${all.length} queries`;
  }
  return { text, empty: false };
}

// ---- suggestions, from the data as it is now --------------------------------------------------

const countBy = (items, f) => { const m = new Map(); for (const x of items) { const k = f(x); if (k) m.set(k, (m.get(k) || 0) + 1); } return m; };

// Where the caret is in the text: the term being typed and, if past the colon, its value so far.
export function locate(text, caret) {
  const r = Array.from(text);
  let inQuote = false, start = 0, i = 0;
  const upTo = Array.from(text.slice(0, caret)).length;
  for (; i < upTo; i++) {
    const c = r[i];
    if (inQuote) { if (c === '\\') i++; else if (c === '"') inQuote = false; continue; }
    if (c === '"') inQuote = true;
    else if (c === ' ' || c === '\t' || c === '\n') start = i + 1;
  }
  // The term runs on past the caret to the next space outside quotes.
  let end = upTo, q = inQuote;
  for (; end < r.length; end++) {
    const c = r[end];
    if (q) { if (c === '\\') end++; else if (c === '"') q = false; continue; }
    if (c === '"') q = true;
    else if (c === ' ' || c === '\t' || c === '\n') break;
  }
  const token = r.slice(start, upTo).join('');
  const colon = token.indexOf(':');
  return { start, end, token, field: colon > 0 ? token.slice(0, colon) : null, prefix: (colon > 0 ? token.slice(colon + 1) : token).replace(/^"/, ''), chars: r };
}

// Suggestions at the caret: field names, or the values a field has right now.
// Each is {insert, label, detail}; `from`/`to` are character offsets to replace.
export function suggest(text, caret, { pt, fields, view, terms }) {
  const takes = (pt && pt.query) || [];
  const at = locate(text, caret);
  const base = { from: at.start, to: at.end, items: [] };
  const lower = at.prefix.toLowerCase();
  if (!at.field) {
    base.items = fields.filter((f) => takes.includes(f.name) && f.name.startsWith(lower)).map((f) => ({ insert: `${f.name}:`, label: f.name, detail: f.doc }));
    return base;
  }
  const f = fields.find((x) => x.name === at.field);
  if (!f || !takes.includes(f.name)) return base;
  const deploys = (view && view.deploys) || [];
  const others = (terms || []).filter((t) => !['query', 'statement', 'table'].includes(t.field));
  const c = view ? resolveDeploy({ ...view }, others).kase : null;
  const units = (lastOf(c) && lastOf(c).units) || [];
  let rows = [];
  if (f.name === 'app') rows = [...countBy(deploys, (d) => d.app)].map(([v, n]) => ({ v, detail: `${n} ${n === 1 ? 'deploy' : 'deploys'}` }));
  else if (f.name === 'namespace') rows = [...countBy(deploys, (d) => d.namespace)].map(([v, n]) => ({ v, detail: `${n} ${n === 1 ? 'deploy' : 'deploys'}` }));
  else if (f.name === 'state') { const m = countBy(deploys, stateName); rows = STATES.map((v) => ({ v, detail: `${m.get(v) || 0} now` })); }
  else if (f.name === 'grade') { const m = countBy(deploys, gradeOf); rows = f.values.map((v) => ({ v, detail: `${m.get(v) || 0} now` })); }
  else if (f.name === 'deploy') rows = deploys.map((d) => ({ v: d.id, label: `${d.app} · ${d.namespace}`, detail: clock(Date.parse(d.at)) }));
  else if (f.name === 'query') { const names = labelUnits(units); rows = units.map((u) => ({ v: names.get(u.key), detail: u.rejected ? 'flagged' : '' })); }
  else if (f.name === 'statement') rows = [...countBy(units, statementOf)].map(([v, n]) => ({ v, detail: `${n} ${n === 1 ? 'query' : 'queries'}` }));
  else if (f.name === 'table') rows = [...countBy(units.flatMap(tablesOf).map((t) => ({ t })), (x) => x.t)].map(([v, n]) => ({ v, detail: `${n} ${n === 1 ? 'query' : 'queries'}` }));
  const seen = new Set();
  base.items = rows.filter((r) => r.v && !seen.has(r.v) && seen.add(r.v) && r.v.toLowerCase().includes(lower))
    .sort((a, b) => (a.v.toLowerCase().startsWith(lower) ? 0 : 1) - (b.v.toLowerCase().startsWith(lower) ? 0 : 1))
    .slice(0, 12).map((r) => ({ insert: `${f.name}:${quote(r.v)}`, label: r.label || r.v, detail: r.detail || '' }));
  return base;
}
