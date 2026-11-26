import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createStore } from '../ui/js/store.js';

const { catalog, layout } = JSON.parse(readFileSync(new URL('../layout/testdata/default.bundle.json', import.meta.url), 'utf8'));
const mem = () => { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), m }; };

// A stand-in for /dashboard/profile: what was last put, and what a restart would read.
function disk(profile = null) {
  const d = { profile, puts: 0, forgets: 0, fail: null };
  d.remote = () => ({
    saved: true, where: '/data/dashboard-default.json', profile: d.profile,
    put: async (p) => { if (d.fail) throw d.fail; d.puts++; d.profile = JSON.parse(JSON.stringify(p)); },
    forget: async () => { if (d.fail) throw d.fail; d.forgets++; d.profile = null; },
  });
  return d;
}
const edit = () => { const e = structuredClone(layout); e.boards[0].panels[0].query = 'app:checkout'; e.boards[0].panels[1].at = [0, 2, 12, 3]; return e; };
const settle = (ms = 420) => new Promise((r) => setTimeout(r, ms));

test('edits and ranges are written to the data directory a moment after the change, once', async () => {
  const d = disk();
  const st = createStore(mem(), d.remote());
  assert.equal(st.saved, true);
  assert.equal(st.where, '/data/dashboard-default.json');
  st.saveLayout('default', layout, edit());
  st.saveRange('default', 'now', { kind: 'rel', id: '24h' });
  st.saveLayout('default', layout, edit());
  assert.equal(d.puts, 0, 'not on every keystroke');
  await settle();
  assert.equal(d.puts, 1, 'one write for the burst');
  assert.equal(d.profile.v, 1);
  assert.equal(d.profile.layout.boards[0].panels[0].query, 'app:checkout');
  assert.deepEqual(d.profile.ranges.now, { kind: 'rel', id: '24h' });
});

test('a new run finds the edits and the range again', async () => {
  const d = disk();
  const first = createStore(mem(), d.remote());
  first.saveLayout('default', layout, edit());
  first.saveRange('default', 'now', { kind: 'rel', id: '24h' });
  await first.flush();
  const second = createStore(mem(), d.remote()); // another browser, another profile of the browser: the disk is what counts
  const got = second.loadLayout('default', layout, catalog);
  assert.equal(got.edited, true);
  assert.equal(got.layout.boards[0].panels[0].query, 'app:checkout');
  assert.deepEqual(second.loadRange('default', 'now'), { kind: 'rel', id: '24h' });
  assert.equal(second.loadRange('default', 'case'), null);
});

test('undoing the edits removes them from disk', async () => {
  const d = disk();
  const st = createStore(mem(), d.remote());
  st.saveLayout('default', layout, edit());
  await st.flush();
  st.resetLayout('default');
  await st.flush();
  assert.equal(d.profile, null, 'nothing left to keep');
  assert.equal(d.forgets, 1);
  st.saveLayout('default', layout, edit());
  st.saveLayout('default', layout, layout); // edited back to the served one
  await st.flush();
  assert.equal(d.profile, null);
});

test('when the dashboard file changed, the saved edits are dropped from disk and the person is told', async () => {
  const d = disk();
  const a = createStore(mem(), d.remote());
  a.saveLayout('f', layout, edit());
  await a.flush();
  const changed = structuredClone(layout);
  changed.boards[1].title = 'Cases';
  const b = createStore(mem(), d.remote());
  const got = b.loadLayout('f', changed, catalog);
  assert.deepEqual([got.edited, got.discarded], [false, true]);
  await b.flush();
  assert.equal(d.profile, null, 'the stale edit is gone');
});

test('a failing disk is reported once, keeps the change to retry, and recovers', async () => {
  const d = disk();
  const problems = [];
  const st = createStore(mem(), d.remote(), (e) => problems.push(e ? e.message : null));
  d.fail = new Error('500 disk full');
  st.saveLayout('default', layout, edit());
  await st.flush();
  st.saveRange('default', 'now', { kind: 'rel', id: '1h' });
  await st.flush();
  assert.deepEqual(problems, ['500 disk full'], 'said once');
  d.fail = null;
  await st.flush();
  assert.deepEqual(problems, ['500 disk full', null], 'cleared when a save works');
  assert.equal(d.profile.layout.boards[0].panels[0].query, 'app:checkout', 'the change that failed was written later');
});

test('edits made in this browser before the profile moved to disk move there once', async () => {
  const d = disk();
  const browser = mem();
  const old = createStore(browser); // no disk: the previous behaviour
  old.saveLayout('default', layout, edit());
  assert.equal(browser.m.size, 1);
  const st = createStore(browser, d.remote());
  const got = st.loadLayout('default', layout, catalog);
  assert.equal(got.edited, true);
  assert.equal(got.layout.boards[0].panels[0].query, 'app:checkout');
  await st.flush();
  assert.equal(d.profile.layout.boards[0].panels[0].query, 'app:checkout');
  assert.equal(browser.m.size, 0, 'the browser copy is removed so the two cannot disagree');
});

test('a profile that is not this version is ignored', () => {
  const d = disk({ v: 7, layout: edit(), base: 'x' });
  const st = createStore(mem(), d.remote());
  assert.equal(st.loadLayout('default', layout, catalog).edited, false);
});

test('without a data directory the browser keeps them, as before', () => {
  const b = mem();
  const st = createStore(b, { saved: false });
  assert.equal(st.saved, false);
  assert.equal(st.where, '');
  st.saveLayout('default', layout, edit());
  assert.equal(b.m.size, 1);
  return st.flush(); // nothing to write, and no error
});
