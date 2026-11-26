import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import * as R from '../ui/js/range.js';
import { createStore } from '../ui/js/store.js';

const bundle = JSON.parse(readFileSync(new URL('../layout/testdata/default.bundle.json', import.meta.url), 'utf8'));
const { catalog, layout } = bundle;
const mem = () => { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), m }; };

test('ranges parse the way the layout file and the API write them', () => {
  assert.equal(R.idMs('90m'), 90 * 60e3);
  assert.equal(R.idMs('6h'), 6 * 3600e3);
  assert.equal(R.idMs('7d'), 7 * 86400e3);
  for (const bad of ['', '0h', '6', 'h', '6w', '-1h', '1.5h', null, undefined]) assert.ok(Number.isNaN(R.idMs(bad)), String(bad));
  assert.deepEqual(R.fromBoard('24h'), { kind: 'rel', id: '24h' });
  assert.deepEqual(R.fromBoard('whenever'), { kind: 'rel', id: '6h' });
});

test('a preset asks the API for now-<range>; an absolute range sends RFC 3339', () => {
  assert.deepEqual(R.query(R.relative('6h')), { from: 'now-6h' });
  const q = R.query(R.absolute(Date.parse('2026-03-10T10:00:00Z'), Date.parse('2026-03-10T11:30:00Z')));
  assert.deepEqual(q, { from: '2026-03-10T10:00:00.000Z', to: '2026-03-10T11:30:00.000Z' });
});

test('a custom range has to be sane', () => {
  const now = Date.parse('2026-03-10T12:00:00Z');
  const h = 3600e3;
  assert.equal(R.validateCustom(now - 2 * h, now - h, now), null);
  assert.match(R.validateCustom(NaN, now, now), /both/);
  assert.match(R.validateCustom(now, now - h, now), /after the start/);
  assert.match(R.validateCustom(now + h, now + 2 * h, now), /future/);
  assert.match(R.validateCustom(now - 100 * 86400e3, now, now), /90 days/);
});

test('datetime-local values round-trip in local time', () => {
  const ms = new Date(2026, 2, 10, 9, 5).getTime();
  assert.equal(R.toLocalInput(ms), '2026-03-10T09:05');
  assert.equal(R.parseLocalInput('2026-03-10T09:05'), ms);
  assert.ok(Number.isNaN(R.parseLocalInput('yesterday')));
  assert.ok(Number.isNaN(R.parseLocalInput('')));
});

test('a range that reaches before the history says so', () => {
  const now = Date.parse('2026-03-10T12:00:00Z');
  const hist = { store: 'memory', since: '2026-03-10T09:00:00Z' };
  assert.match(R.historyNote(R.relative('24h'), now, hist), /starts .*memory/);
  assert.equal(R.historyNote(R.relative('1h'), now, hist), null);
  assert.equal(R.historyNote(R.relative('24h'), now, { store: 'postgres' }), null, 'an empty store has nothing to warn about');
  assert.match(R.historyNote(R.relative('24h'), now, { store: 'postgres', since: hist.since }), /^The radar's history starts [^(]*\. Earlier/);
});

test('labels read as words', () => {
  assert.equal(R.label(R.relative('24h')), 'Last 24 hours');
  assert.equal(R.label(R.relative('90m')), 'Last 90m');
  assert.match(R.label(R.absolute(new Date(2026, 2, 10, 9, 0).getTime(), new Date(2026, 2, 10, 11, 30).getTime())), /^10 Mar 09:00 to 10 Mar 11:30$/);
});

test('edits to a layout are remembered while the served layout is the one they were made on', () => {
  const st = createStore(mem());
  const edited = structuredClone(layout);
  edited.boards[0].panels[0].at = [0, 0, 8, 4];
  assert.equal(st.saveLayout('default', layout, edited), true);
  const got = st.loadLayout('default', layout, catalog);
  assert.equal(got.edited, true);
  assert.deepEqual(got.layout.boards[0].panels[0].at, [0, 0, 8, 4]);
});

test('when the dashboard file changes, old edits are dropped and the person is told', () => {
  const store = mem();
  const st = createStore(store);
  const edited = structuredClone(layout);
  edited.boards[0].panels[0].at = [0, 0, 8, 4];
  st.saveLayout('/etc/d.yaml', layout, edited);
  const changed = structuredClone(layout);
  changed.boards[1].title = 'Cases';
  const got = st.loadLayout('/etc/d.yaml', changed, catalog);
  assert.deepEqual([got.edited, got.discarded], [false, true]);
  assert.deepEqual(got.layout, changed);
  assert.equal(store.m.size, 0, 'the stale edit is removed');
});

test('saving the served layout again forgets the edit, and reset does too', () => {
  const store = mem();
  const st = createStore(store);
  const edited = structuredClone(layout);
  edited.boards[0].panels[0].at = [0, 0, 8, 4];
  st.saveLayout('d', layout, edited);
  assert.equal(store.m.size, 1);
  st.saveLayout('d', layout, layout);
  assert.equal(store.m.size, 0);
  st.saveLayout('d', layout, edited);
  st.resetLayout('d');
  assert.equal(store.m.size, 0);
});

test('layouts saved for different sources do not mix', () => {
  const st = createStore(mem());
  const edited = structuredClone(layout);
  edited.boards[0].panels[0].at = [0, 0, 8, 4];
  st.saveLayout('a.yaml', layout, edited);
  assert.equal(st.loadLayout('b.yaml', layout, catalog).edited, false);
});

test('garbage in storage is ignored, and blocked storage does not break anything', () => {
  const store = mem();
  store.setItem('pgrr-layout:v1:default', '{not json');
  assert.equal(createStore(store).loadLayout('default', layout, catalog).edited, false);
  store.setItem('pgrr-layout:v1:default', JSON.stringify({ v: 1, base: 'x', layout: { boards: 3 } }));
  assert.equal(createStore(store).loadLayout('default', layout, catalog).edited, false);

  const blocked = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); }, removeItem() { throw new Error('blocked'); } };
  const st = createStore(blocked);
  assert.equal(st.loadLayout('default', layout, catalog).edited, false);
  assert.equal(st.saveLayout('default', layout, structuredClone(layout)), true);
  const edited = structuredClone(layout); edited.boards[0].panels[0].at = [0, 0, 8, 4];
  assert.equal(st.saveLayout('default', layout, edited), false, 'says it could not keep it');
  assert.equal(st.loadRange('default', 'now'), null);
});

test('a board range is remembered per board', () => {
  const st = createStore(mem());
  st.saveRange('default', 'now', R.relative('1h'));
  st.saveRange('default', 'case', R.absolute(1000, 2000));
  assert.deepEqual(st.loadRange('default', 'now'), { kind: 'rel', id: '1h' });
  assert.deepEqual(st.loadRange('default', 'case'), { kind: 'abs', from: 1000, to: 2000 });
  assert.equal(st.loadRange('default', 'health'), null);
});
