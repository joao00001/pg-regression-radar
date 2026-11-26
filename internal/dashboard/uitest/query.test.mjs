// Run with: node --test internal/dashboard/uitest/*.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import * as Q from '../ui/js/query.js';

const cases = JSON.parse(readFileSync(new URL('../layout/testdata/query-cases.json', import.meta.url), 'utf8'));
const bundle = JSON.parse(readFileSync(new URL('../layout/testdata/default.bundle.json', import.meta.url), 'utf8'));
const type = (n) => bundle.catalog.find((c) => c.type === n);
const fields = bundle.queryFields;

test('the grammar agrees with the Go parser on every shared case', () => {
  for (const c of cases) {
    const r = Q.parse(c.in);
    if (c.error) {
      assert.deepEqual(r.error && { code: r.error.code, col: r.error.col }, c.error, JSON.stringify(c.in));
    } else {
      assert.equal(r.error, null, JSON.stringify(c.in));
      assert.deepEqual(r.terms.map((t) => [t.field, t.value, t.col]), c.terms, JSON.stringify(c.in));
    }
  }
});

test('the length limit is the same as Go', () => {
  assert.equal(Q.parse('app:' + 'x'.repeat(197)).error.code, 'too-long');
  assert.equal(Q.parse('app:' + 'x'.repeat(196)).error, null);
});

test('a value is quoted only when it has to be, and quoting round-trips', () => {
  assert.equal(Q.quote('checkout'), 'checkout');
  assert.equal(Q.quote('SELECT on orders'), '"SELECT on orders"');
  for (const v of ['a b', 'say "hi"', 'back\\slash', 'x*', 'é à']) assert.equal(Q.parse(`query:${Q.quote(v)}`).terms[0].value, v);
});

test('a query is checked against what the panel takes, as Go does', () => {
  const check = (t, q) => Q.validate(type(t), fields, Q.parse(q).terms).join('; ');
  assert.equal(check('deploy-cards', 'app:checkout state:regressed grade:a'), '');
  assert.match(check('deploy-cards', 'state:broken'), /state is one of regressed, acted, watching, healthy, inconclusive/);
  assert.match(check('deploy-cards', 'state:reg*'), /exact values/);
  assert.match(check('deploy-cards', 'query:x'), /no "query" field/);
  assert.match(check('canary-chart', 'deploy:a deploy:b'), /give deploy: once/);
  assert.match(check('radar-state', 'app:x'), /takes no query/);
  assert.equal(check('safety', ''), '');
});

// ---- data ---------------------------------------------------------------------------

const t = (h) => new Date(2026, 0, 1, h).toISOString();
const D = (id, app, ns, h, extra = {}) => ({ id, app, namespace: ns, at: t(h), phase: 'judged', health: 'Healthy', latest: { grade: '' }, ...extra });
const deploys = [
  D('a1', 'checkout', 'payments', 9, { health: 'Regressed', latest: { grade: 'A' } }),
  D('a2', 'checkout', 'payments', 11, { health: 'Regressed', phase: 'acted', latest: { grade: 'A' } }),
  D('b1', 'orders', 'shop', 10, { health: 'Regressed', latest: { grade: 'B' } }),
  D('c1', 'cart', 'shop', 8),
  D('w1', 'search', 'shop', 12, { phase: 'observing', latest: null }),
];
const U = (key, family, rejected = false) => ({ key, family, rejected, ratio: 1.5, ratioLow: 1.2, ratioHigh: 1.8 });
const units = [U('k1aaaaaa', 'SELECT:orders', true), U('k2bbbbbb', 'SELECT:orders'), U('k3cccccc', 'UPDATE:orders,items', true), U('k4dddddd', 'SELECT:users')];
const kase = (d) => ({ ...d, history: [{ units }], actions: [] });
const cases2 = new Map(deploys.map((d) => [d.id, kase(d)]));
const view = { deploys, cases: cases2, selected: 'c1', kase: cases2.get('c1'), unit: 'k4dddddd', series: { s: 1 }, seriesMissing: false, now: Date.now() };
const terms = (q) => Q.parse(q).terms;

test('deploys are filtered by app, namespace, state and grade; the same field means either', () => {
  const ids = (q) => Q.selectDeploys(deploys, terms(q)).map((d) => d.id);
  assert.deepEqual(ids(''), ['a1', 'a2', 'b1', 'c1', 'w1']);
  assert.deepEqual(ids('app:checkout'), ['a1', 'a2']);
  assert.deepEqual(ids('app:checkout app:cart'), ['a1', 'a2', 'c1'], 'the same field: either');
  assert.deepEqual(ids('namespace:shop state:regressed'), ['b1'], 'different fields: all');
  assert.deepEqual(ids('state:acted'), ['a2']);
  assert.deepEqual(ids('state:WATCHING'), ['w1'], 'case is ignored');
  assert.deepEqual(ids('state:healthy'), ['c1']);
  assert.deepEqual(ids('grade:a'), ['a1', 'a2']);
  assert.deepEqual(ids('app:ch* app:*art'), ['a1', 'a2', 'c1'], '* matches any characters');
  assert.deepEqual(ids('app:nothing'), []);
});

test('database queries are matched by name, statement and table, with sets by wildcard', () => {
  const keys = (q) => Q.selectUnits(units, terms(q)).map((u) => u.key);
  assert.deepEqual(keys('query:"SELECT on orders #1"'), ['k1aaaaaa']);
  assert.deepEqual(keys('query:"SELECT on users"'), ['k4dddddd'], 'the plain name when it is not numbered');
  assert.deepEqual(keys('query:k2bbbb*'), ['k2bbbbbb'], 'or the start of its fingerprint');
  assert.deepEqual(keys('statement:select'), ['k1aaaaaa', 'k2bbbbbb', 'k4dddddd']);
  assert.deepEqual(keys('table:items'), ['k3cccccc'], 'a query that touches two tables matches either');
  assert.deepEqual(keys('statement:SELECT table:orders'), ['k1aaaaaa', 'k2bbbbbb']);
  assert.deepEqual(keys('table:o*'), ['k1aaaaaa', 'k2bbbbbb', 'k3cccccc']);
  assert.deepEqual(keys('app:x'), ['k1aaaaaa', 'k2bbbbbb', 'k3cccccc', 'k4dddddd'], 'a deploy field says nothing about queries');
});

test('which deploy a panel shows: the one named, the newest match, or the one picked', () => {
  const r = (q) => Q.resolveDeploy(view, terms(q));
  assert.equal(r('').kase.id, 'c1');
  assert.equal(r('').source, 'picked');
  assert.deepEqual([r('deploy:b1').kase.id, r('deploy:b1').source], ['b1', 'named']);
  assert.match(r('deploy:zzz').note, /No deploy "zzz"/);
  assert.deepEqual([r('app:checkout').kase.id, r('app:checkout').source], ['a2', 'matched'], 'newest first');
  assert.equal(r('state:regressed namespace:shop').kase.id, 'b1');
  assert.match(r('app:nobody').note, /matches the query of this panel/);
});

test('a panel on one deploy is given that deploy, its own query and its own series', () => {
  const asked = [];
  const v = { ...view, seriesFor: (id, key) => { asked.push([id, key]); return { series: { for: `${id}/${key}` }, state: 'ready' }; } };
  // Following the board changes nothing.
  assert.equal(Q.scope(v, [], { follows: true }), v);
  // The newest checkout deploy, opened on its first query: not the board's, so its own series.
  const a = Q.scope(v, terms('app:checkout'), { follows: true, wantsSeries: true });
  assert.equal(a.kase.id, 'a2');
  assert.equal(a.unit, 'k1aaaaaa');
  assert.deepEqual(a.series, { for: 'a2/k1aaaaaa' });
  assert.deepEqual(asked, [['a2', 'k1aaaaaa']]);
  // A named query on the board's own deploy: the board's series is reused when it is the same one.
  const b = Q.scope(v, terms('query:"SELECT on users"'), { follows: true, wantsSeries: true });
  assert.equal(b.kase.id, 'c1');
  assert.equal(b.unit, 'k4dddddd');
  assert.deepEqual(b.series, { s: 1 }, 'no second read for what the board already has');
  assert.equal(asked.length, 1);
  // The panel's own pick survives while the query lets it through, and is dropped when it does not.
  assert.equal(Q.scope(v, terms('app:checkout statement:SELECT'), { follows: true, pin: 'k2bbbbbb' }).unit, 'k2bbbbbb');
  assert.equal(Q.scope(v, terms('app:checkout statement:SELECT'), { follows: true, pin: 'k3cccccc' }).unit, 'k1aaaaaa');
  // Only the queries the query describes remain in the evaluations the panel reads.
  const c = Q.scope(v, terms('statement:UPDATE'), { follows: true });
  assert.deepEqual(c.kase.history[0].units.map((u) => u.key), ['k3cccccc']);
  assert.equal(view.kase.history[0].units.length, 4, 'the board\'s own case is not touched');
  // Nothing matches: the panel says so rather than showing the board's deploy.
  const d = Q.scope(v, terms('app:nobody'), { follows: true });
  assert.equal(d.kase, null);
  assert.match(d.scopeNote, /matches the query/);
});

test('a list of deploys is filtered and does not move the board\'s pick', () => {
  const v = Q.scope(view, terms('state:regressed'), { follows: false });
  assert.deepEqual(v.deploys.map((d) => d.id), ['a1', 'b1']);
  assert.equal(v.kase, view.kase);
  assert.equal(v.selected, 'c1');
  assert.deepEqual([...v.cases.keys()], ['a1', 'b1'], 'the activity feed reads only these');
});

test('what the query came to is said in words', () => {
  assert.equal(Q.outcome(type('deploy-cards'), view, terms('state:regressed')).text, '2 of 5 deploys');
  assert.equal(Q.outcome(type('deploy-cards'), view, []).text, 'all 5 deploys');
  assert.match(Q.outcome(type('canary-chart'), view, []).text, /checkout|cart/);
  assert.match(Q.outcome(type('canary-chart'), view, []).text, /the deploy picked on the board/);
  assert.match(Q.outcome(type('canary-chart'), view, terms('app:checkout statement:UPDATE')).text, /newest deploy that matches; 1 of 4 queries/);
  assert.equal(Q.outcome(type('canary-chart'), view, terms('app:nobody')).bad, true);
  assert.equal(Q.outcome(type('radar-state'), view, []).empty, true);
});

// ---- suggestions, live ----------------------------------------------------------------

const sug = (text, caret = text.length, ts = terms(text), pt = type('canary-chart')) => Q.suggest(text, caret, { pt, fields, view, terms: ts });
const labels = (r) => r.items.map((i) => i.label);

test('field names are suggested while a term is being started, and only those the panel takes', () => {
  assert.deepEqual(labels(sug('')), ['app', 'namespace', 'state', 'grade', 'deploy', 'query', 'statement', 'table']);
  assert.deepEqual(labels(sug('st', 2, [])), ['state', 'statement']);
  assert.deepEqual(labels(sug('', 0, [], type('deploy-cards'))), ['app', 'namespace', 'state', 'grade']);
  assert.deepEqual(sug('', 0, [], type('radar-state')).items, []);
  assert.equal(sug('st', 2, []).items[0].insert, 'state:');
});

test('values are suggested from the data as it is now, with counts', () => {
  const apps = sug('app:', 4, []);
  assert.deepEqual(labels(apps).sort(), ['cart', 'checkout', 'orders', 'search']);
  assert.equal(apps.items.find((i) => i.label === 'checkout').detail, '2 deploys');
  assert.equal(apps.items.find((i) => i.label === 'cart').detail, '1 deploy');
  assert.deepEqual(labels(sug('app:ch', 6, [])), ['checkout', 'search'], 'a start first, then anywhere in the name');
  assert.deepEqual(labels(sug('state:', 6, [])), Q.STATES);
  assert.equal(sug('state:', 6, []).items[0].detail, '2 now');
  assert.equal(sug('grade:', 6, []).items.find((i) => i.label === 'A').detail, '2 now');
  assert.equal(sug('deploy:', 7, []).items.length, 5);
  assert.equal(sug('deploy:', 7, []).items[0].insert.startsWith('deploy:'), true);
});

test('database queries are suggested from the deploy the other terms choose', () => {
  assert.deepEqual(labels(sug('query:', 6, [])), ['SELECT on orders #1', 'SELECT on orders #2', 'UPDATE on orders,items', 'SELECT on users']);
  assert.equal(sug('query:SELECT on', 15, []).items.length, 0, 'an unquoted space ends the term');
  assert.equal(sug('query:"SELECT on', 16, []).items.length, 3);
  assert.equal(sug('query:', 6, []).items[0].insert, 'query:"SELECT on orders #1"', 'quoted when it has to be');
  assert.deepEqual(labels(sug('statement:', 10, [])), ['SELECT', 'UPDATE']);
  assert.deepEqual(labels(sug('table:', 6, [])).sort(), ['items', 'orders', 'users']);
  // Another deploy has other queries.
  const v2 = { ...view, cases: new Map([...cases2, ['a2', { ...cases2.get('a2'), history: [{ units: [U('zz', 'DELETE:audit')] }] }]]) };
  assert.deepEqual(Q.suggest('statement:', 10, { pt: type('canary-chart'), fields, view: v2, terms: terms('app:checkout') }).items.map((i) => i.label), ['DELETE']);
});

test('the caret decides which term is completed, and a whole term is replaced', () => {
  const text = 'app:ch state:regr';
  const r = sug(text, 6, terms(text));
  assert.deepEqual([r.from, r.to], [0, 6]);
  const s = sug(text, text.length, terms(text));
  assert.deepEqual([s.from, s.to], [7, text.length]);
  assert.deepEqual(labels(s), ['regressed']);
  const mid = sug('app:ch state:x', 5, []);
  assert.deepEqual([mid.from, mid.to], [0, 6], 'the term runs on past the caret');
  const q = Q.locate('query:"a b" app:', 16);
  assert.deepEqual([q.field, q.prefix], ['app', '']);
  assert.deepEqual(sug('app:é', 5, []).items, []);
});
