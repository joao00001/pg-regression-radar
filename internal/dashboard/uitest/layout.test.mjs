// Run with: node --test internal/dashboard/uitest/*.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import * as L from '../ui/js/layout.js';

const golden = (n) => readFileSync(new URL(`../layout/testdata/${n}`, import.meta.url), 'utf8');
const bundle = JSON.parse(golden('default.bundle.json'));
const catalog = bundle.catalog;
const minOf = (p) => { const c = catalog.find((x) => x.type === p.type); return { w: c.minW, h: c.minH }; };
const P = (id, x, y, w, h, type = 'summary') => ({ id, type, at: [x, y, w, h] });
const overlaps = (ps) => ps.some((a, i) => ps.slice(i + 1).some((b) => L.hit(a.at, b.at)));

test('what the UI writes is what the Go side writes: the default exports byte for byte', () => {
  assert.equal(L.toYAML(bundle.layout, catalog), golden('default.yaml'));
});

test('the layout Go serves passes the UI sanitizer unchanged', () => {
  const s = L.sanitize(bundle.layout, catalog);
  assert.deepEqual(s.boards.map((b) => b.panels.map((p) => [p.id, p.at])), bundle.layout.boards.map((b) => b.panels.map((p) => [p.id, p.at])));
});

test('the default layout has no overlaps and nothing floating', () => {
  for (const b of bundle.layout.boards) {
    assert.equal(overlaps(b.panels), false, b.id);
    assert.deepEqual(L.compact(b.panels).map((p) => p.at), b.panels.map((p) => p.at), `${b.id} is already compact`);
  }
});

test('panels rise to the first free row', () => {
  const out = L.compact([P('a', 0, 0, 6, 2), P('b', 0, 9, 6, 2), P('c', 6, 5, 6, 2)]);
  assert.deepEqual(out.map((p) => p.at), [[0, 0, 6, 2], [0, 2, 6, 2], [6, 0, 6, 2]]);
});

test('compaction keeps the order of the list it was given', () => {
  const ps = [P('z', 0, 4, 3, 1), P('a', 0, 0, 3, 1)];
  assert.deepEqual(L.compact(ps).map((p) => p.id), ['z', 'a']);
});

test('clamp keeps a panel on the grid and above its minimum', () => {
  assert.deepEqual(L.clamp([-3, -1, 20, 0], { w: 4, h: 2 }), [0, 0, 12, 2]);
  assert.deepEqual(L.clamp([10, 5, 6, 3], { w: 4, h: 2 }), [6, 5, 6, 3]);
  assert.deepEqual(L.clamp([1.4, 2.6, 4, 2], { w: 1, h: 1 }), [1, 3, 4, 2]);
  assert.deepEqual(L.clamp([0, 0, 1, 1], { w: 4, h: 3 }), [0, 0, 4, 3]);
});

test('moving a panel onto another pushes it below, and nothing overlaps', () => {
  const ps = [P('a', 0, 0, 6, 3), P('b', 6, 0, 6, 3), P('c', 0, 3, 12, 2)];
  const out = L.place(ps, 'b', [0, 0, 6, 3]);
  const at = Object.fromEntries(out.map((p) => [p.id, p.at]));
  assert.deepEqual(at.b, [0, 0, 6, 3]);
  assert.equal(overlaps(out), false);
  assert.ok(at.a[1] >= 3, 'a was pushed below b');
});

test('a push cascades through everything underneath', () => {
  const ps = [P('a', 0, 0, 12, 2), P('b', 0, 2, 12, 2), P('c', 0, 4, 12, 2), P('d', 0, 6, 12, 2)];
  const out = L.place(ps, 'd', [0, 0, 12, 2]);
  assert.deepEqual(out.map((p) => [p.id, p.at[1]]), [['a', 2], ['b', 4], ['c', 6], ['d', 0]]);
});

test('dropping a panel into empty space below makes it rise, not float', () => {
  const ps = [P('a', 0, 0, 6, 3), P('b', 6, 0, 6, 3)];
  const out = L.place(ps, 'b', [6, 20, 6, 3]);
  assert.deepEqual(out.find((p) => p.id === 'b').at, [6, 0, 6, 3]);
});

test('resizing respects the type minimum and the grid', () => {
  const ps = [P('s', 0, 0, 12, 4, 'summary')];
  assert.deepEqual(L.place(ps, 's', [0, 0, 1, 1], minOf)[0].at, [0, 0, 4, 3]);
  assert.deepEqual(L.place(ps, 's', [0, 0, 40, 4], minOf)[0].at, [0, 0, 12, 4]);
});

test('placing an unknown id changes nothing', () => {
  const ps = [P('a', 0, 0, 6, 3)];
  assert.equal(L.place(ps, 'nope', [0, 0, 1, 1]), ps);
});

test('holes are the empty cells under a column that ends above its neighbour', () => {
  assert.deepEqual(L.holes([P('a', 0, 0, 12, 3), P('b', 0, 3, 4, 5), P('c', 4, 3, 8, 5)]), []);
  // the last column ends early: the gap is closed along the bottom of the layout
  assert.deepEqual(L.holes([P('a', 0, 0, 4, 10), P('b', 4, 0, 8, 6)]), [[4, 6, 8, 4, true]]);
  // two gaps, one of them against the right edge
  assert.deepEqual(L.holes([P('a', 0, 0, 4, 10), P('b', 4, 0, 6, 4), P('c', 10, 0, 2, 6)]), [[4, 4, 6, 2, false], [4, 6, 8, 4, true]]);
  // a gap over a panel is closed along its bottom
  assert.deepEqual(L.holes([P('a', 0, 0, 4, 10), P('b', 4, 0, 8, 4), P('c', 4, 6, 8, 4)]), [[4, 4, 8, 2, true]]);
  assert.deepEqual(L.holes([]), []);
});

test('holes and panels together cover every cell above the bottom once', () => {
  let seed = 11;
  const rnd = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
  let ps = bundle.layout.boards[0].panels;
  for (let i = 0; i < 200; i++) {
    const p = ps[rnd(ps.length)];
    ps = L.place(ps, p.id, [rnd(14) - 1, rnd(45) - 2, rnd(13) + 1, rnd(12) + 1], minOf);
    const rows = L.bottom(ps);
    const seen = Array.from({ length: rows }, () => new Array(L.COLS).fill(0));
    for (const at of [...ps.map((q) => q.at), ...L.holes(ps).map((r) => r.slice(0, 4))]) {
      for (let y = at[1]; y < at[1] + at[3]; y++) for (let x = at[0]; x < at[0] + at[2]; x++) seen[y][x]++;
    }
    assert.ok(seen.every((r) => r.every((n) => n === 1)), `cells covered once after move ${i}`);
  }
});

test('the default boards have no empty cells', () => {
  for (const b of bundle.layout.boards) assert.deepEqual(L.holes(b.panels), [], b.id);
});

test('any sequence of moves keeps a layout overlap-free, in the grid and compact', () => {
  let seed = 7;
  const rnd = (n) => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed % n; };
  let ps = bundle.layout.boards[0].panels;
  for (let i = 0; i < 400; i++) {
    const p = ps[rnd(ps.length)];
    const at = [rnd(14) - 1, rnd(45) - 2, rnd(13) + 1, rnd(12) + 1];
    ps = L.place(ps, p.id, at, minOf);
    assert.equal(overlaps(ps), false, `overlap after move ${i}`);
    for (const q of ps) {
      const m = minOf(q);
      assert.ok(q.at[0] >= 0 && q.at[0] + q.at[2] <= L.COLS && q.at[1] >= 0, `${q.id} off the grid: ${q.at}`);
      assert.ok(q.at[2] >= m.w && q.at[3] >= m.h, `${q.id} below its minimum`);
    }
    assert.deepEqual(L.compact(ps).map((q) => q.at), ps.map((q) => q.at), `not compact after move ${i}`);
  }
});

test('adding a panel puts it at the bottom with its default size and a fresh id', () => {
  const ps = [P('summary', 0, 0, 12, 4)];
  const pt = catalog.find((c) => c.type === 'summary');
  let out = L.add(ps, pt);
  assert.deepEqual(out[1], { id: 'summary-2', type: 'summary', at: [0, 4, pt.w, pt.h] });
  out = L.add(out, pt);
  assert.equal(out[2].id, 'summary-3');
  assert.equal(L.add([], catalog.find((c) => c.type === 'deploy-cards'))[0].id, 'deploy-cards');
});

test('removing a panel lets the ones below rise', () => {
  const ps = [P('a', 0, 0, 12, 3), P('b', 0, 3, 12, 3), P('c', 0, 6, 12, 3)];
  assert.deepEqual(L.remove(ps, 'b').map((p) => [p.id, p.at[1]]), [['a', 0], ['c', 3]]);
});

test('sanitize drops what it cannot trust instead of repairing it', () => {
  const ok = { apiVersion: 'pgrr.io/v1', kind: 'Dashboard', boards: [{ id: 'a', title: 'A', range: '6h', panels: [P('s', 0, 0, 12, 4)] }] };
  assert.ok(L.sanitize(ok, catalog));
  const bad = (f) => { const c = structuredClone(ok); f(c); return L.sanitize(c, catalog); };
  assert.equal(bad((c) => { c.boards[0].panels[0].type = 'nope'; }), null);
  assert.equal(bad((c) => { c.boards[0].panels[0].at = [0, 0, 'x', 1]; }), null);
  assert.equal(bad((c) => { c.boards[0].panels.push(P('s', 0, 4, 3, 3)); }), null, 'duplicate id');
  assert.equal(bad((c) => { c.boards.push({ ...c.boards[0] }); }), null, 'duplicate board');
  assert.equal(bad((c) => { c.boards = []; }), null);
  assert.equal(L.sanitize(null, catalog), null);
  // sizes that do not fit are clamped, not rejected
  const small = bad((c) => { c.boards[0].panels[0].at = [0, 0, 1, 1]; });
  assert.deepEqual(small.boards[0].panels[0].at, [0, 0, 4, 3]);
});

test('YAML export quotes only what needs it, and keeps options in the catalog order', () => {
  const layout = {
    apiVersion: 'pgrr.io/v1', kind: 'Dashboard',
    boards: [{ id: 'now', title: 'Now: "live"', range: '6h', panels: [
      { id: 'd', type: 'deploy-cards', at: [0, 0, 3, 4], title: 'yes', options: { limit: 7 } },
      { id: 'c', type: 'canary-chart', at: [3, 0, 6, 7], options: { unit: 'first' } },
    ] }],
  };
  const y = L.toYAML(layout, catalog);
  assert.match(y, /title: "Now: \\"live\\""/);
  assert.match(y, /title: "yes"/, 'a bare yes would be read as a boolean');
  assert.match(y, /options:\n {10}limit: 7/);
  assert.match(y, /unit: first/);
});

test('fingerprints tell layouts apart and are stable', () => {
  const a = L.fingerprint(bundle.layout);
  assert.equal(a, L.fingerprint(structuredClone(bundle.layout)));
  const b = structuredClone(bundle.layout);
  b.boards[0].panels[0].at[2] = 11;
  assert.notEqual(a, L.fingerprint(b));
  assert.match(a, /^[0-9a-f]{8}$/);
});

test('the export writes titles and descriptions exactly as `config dump` does', () => {
  const cases = JSON.parse(golden('text-cases.json'));
  assert.ok(cases.length > 10);
  for (const c of cases) {
    assert.equal(L.toYAML(c.layout, catalog), c.yaml, JSON.stringify(c.text));
  }
});

test('a panel description survives the sanitizer, the export and a reload of that export', () => {
  const lay = JSON.parse(JSON.stringify(bundle.layout));
  lay.boards[0].panels[0].description = 'Shown by the info button: what this panel is.';
  const clean = L.sanitize(lay, catalog);
  assert.equal(clean.boards[0].panels[0].description, 'Shown by the info button: what this panel is.');
  assert.match(L.toYAML(clean, catalog), /description: ["']?Shown by the info button/);
  // A move keeps it.
  const moved = L.place(clean.boards[0].panels, clean.boards[0].panels[0].id, [0, 0, 12, 4], () => ({ w: 1, h: 1 }));
  assert.equal(moved.find((p) => p.id === clean.boards[0].panels[0].id).description, 'Shown by the info button: what this panel is.');
});

// ---- a panel's own settings ---------------------------------------------------------------

const type = (name) => catalog.find((c) => c.type === name);
const cards = () => ({ id: 'deploys', type: 'deploy-cards', at: [0, 0, 3, 8] });

test('every catalog option is described for the settings pane, with a valid default', () => {
  for (const pt of catalog) for (const o of pt.options || []) {
    assert.ok(o.label && o.group && o.doc, `${pt.type}.${o.name} has a label, a group and help`);
    assert.ok(L.validOption(o, o.default), `${pt.type}.${o.name}: default ${o.default} is valid`);
    if (o.when) {
      const [name, val] = o.when.split('=');
      assert.ok(pt.options.find((x) => x.name === name).values.includes(val), `${pt.type}.${o.name}: when ${o.when}`);
    }
  }
});

test('an option is checked the way the Go validator checks it', () => {
  const limit = L.optionDef(type('deploy-cards'), 'limit');
  assert.equal(L.validOption(limit, 12), true);
  assert.equal(L.validOption(limit, 0), false);
  assert.equal(L.validOption(limit, 51), false);
  assert.equal(L.validOption(limit, 2.5), false);
  assert.equal(L.validOption(limit, '12'), false);
  const viz = L.optionDef(type('deploy-cards'), 'viz');
  assert.equal(L.validOption(viz, 'list'), true);
  assert.equal(L.validOption(viz, 'pie'), false);
  assert.equal(L.validOption(L.optionDef(type('summary'), 'bar'), 'yes'), false);
  assert.equal(L.validOption(null, 1), false);
});

test('a setting equal to the default is not stored, and a panel put back looks untouched', () => {
  const pt = type('deploy-cards');
  let p = L.setOption(cards(), pt, 'viz', 'list');
  assert.deepEqual(p.options, { viz: 'list' });
  p = L.setOption(p, pt, 'limit', 5);
  assert.deepEqual(p.options, { viz: 'list', limit: 5 });
  p = L.setOption(L.setOption(p, pt, 'viz', 'cards'), pt, 'limit', 12);
  assert.equal('options' in p, false, 'no empty options left behind');
  assert.deepEqual(p, cards());
  assert.deepEqual(L.setOption(cards(), pt, 'viz', 'pie'), cards(), 'an invalid value changes nothing');
  assert.deepEqual(L.setOption(cards(), pt, 'nope', 1), cards(), 'an unknown option changes nothing');
});

test('effective options fill in the defaults and drop what is invalid', () => {
  const pt = type('deploy-cards');
  assert.deepEqual(L.effectiveOptions({ options: { limit: 3, viz: 'pie', x: 1 } }, pt), { viz: 'cards', show: 'all', sort: 'newest', limit: 3 });
  assert.deepEqual(L.cleanOptions({ limit: 3, viz: 'pie', x: 1 }, pt), { limit: 3 });
  assert.deepEqual(L.cleanOptions(null, pt), {});
});

test('a saved layout keeps valid settings and loses invalid ones', () => {
  const layout = { apiVersion: 'pgrr.io/v1', kind: 'Dashboard', boards: [{ id: 'now', title: 'Now', range: '6h', panels: [{ ...cards(), options: { viz: 'list', limit: 999, junk: true } }] }] };
  assert.deepEqual(L.sanitize(layout, catalog).boards[0].panels[0].options, { viz: 'list' });
});

test('title and description are trimmed to their limits, and empty text removes them', () => {
  let p = L.setText(cards(), 'title', 'x'.repeat(200));
  assert.equal(p.title.length, L.MAX_TITLE);
  p = L.setText(p, 'description', 'y'.repeat(500));
  assert.equal(p.description.length, L.MAX_DESCRIPTION);
  assert.equal('title' in L.setText(p, 'title', '   '), false);
  assert.equal(L.setText(cards(), 'title', 'Deploys  ').title, 'Deploys', 'trailing space is dropped');
  const r = L.resetPanel({ ...cards(), title: 'a', description: 'b', options: { limit: 2 } });
  assert.deepEqual(r, cards(), 'reset keeps the place and clears the rest');
});

test('a panel is rebuilt when what it draws from changes, not when it only moves', () => {
  const a = L.setOption(cards(), type('deploy-cards'), 'viz', 'list');
  assert.notEqual(L.panelSignature(a), L.panelSignature(cards()));
  assert.equal(L.panelSignature({ ...a, at: [3, 3, 6, 9] }), L.panelSignature(a));
  assert.notEqual(L.panelSignature(L.setText(cards(), 'title', 'x')), L.panelSignature(cards()));
});

test('one panel exports as the lines the file holds for it', () => {
  const p = { ...L.setOption(L.setOption(cards(), type('deploy-cards'), 'limit', 5), type('deploy-cards'), 'viz', 'list'), title: 'Every deploy', description: 'All of them: 100%' };
  const one = L.panelYAML(p, catalog);
  assert.equal(one, [
    '- id: deploys', '  type: deploy-cards', '  at: [0, 0, 3, 8]', '  title: Every deploy', '  description: "All of them: 100%"',
    '  options:', '    viz: list', '    limit: 5', '',
  ].join('\n'));
  const whole = L.toYAML({ boards: [{ id: 'now', title: 'Now', range: '6h', panels: [p] }] }, catalog);
  assert.ok(whole.includes(one.split('\n').map((l) => (l ? `      ${l}` : l)).join('\n')), 'the same lines sit inside the full file');
});

test('the address names the board, the deploy of a case and the panel in focus', () => {
  assert.deepEqual(L.parseHash('#/now'), { board: 'now', deploy: null, view: null });
  assert.deepEqual(L.parseHash('#/now?view=canary'), { board: 'now', deploy: null, view: 'canary' });
  assert.deepEqual(L.parseHash('#/case/a%2Fb%20c?view=evidence-2'), { board: 'case', deploy: 'a/b c', view: 'evidence-2' });
  assert.deepEqual(L.parseHash('#/now?view=<script>'), { board: 'now', deploy: null, view: null }, 'a panel id is only letters, digits, - and _');
  assert.deepEqual(L.parseHash(''), { board: null, deploy: null, view: null });
  assert.deepEqual(L.parseHash('#/case/%E0%A4%A'), { board: 'case', deploy: null, view: null }, 'a broken escape is not a deploy');
  for (const a of [{ board: 'now' }, { board: 'case', deploy: 'x/y z', view: 'ledger' }, { board: 'now', view: 'canary' }]) {
    const r = L.parseHash(L.hashFor(a));
    assert.deepEqual(r, { board: a.board, deploy: a.deploy || null, view: a.view || null });
  }
});
