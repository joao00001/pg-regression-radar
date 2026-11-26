import test from 'node:test';
import assert from 'node:assert/strict';
import * as m from '../ui/js/model.js';

const u = (key, ratio, low, high, extra = {}) => ({ key, family: `SELECT:${key}`, ratio, ratioLow: low, ratioHigh: high, rejected: false, grade: 'A', kind: 'Healthy', ...extra });

test('ratio rows: regressed first, then furthest from 1.0, and unusable ratios are left out', () => {
  const rows = m.ratioRows([
    u('calm', 1.02, 0.95, 1.1),
    u('slow', 1.6, 1.3, 2.0, { rejected: true }),
    u('fast', 0.4, 0.3, 0.5),
    u('nan', NaN, 1, 1), u('zero', 0, 0, 0), u('inf', Infinity, 1, 2),
  ]);
  assert.deepEqual(rows.map((r) => r.key), ['slow', 'fast', 'calm']);
  assert.equal(rows[0].moved, true);
  assert.equal(rows[2].moved, false, 'an interval that spans 1.0 has not moved');
  assert.equal(m.ratioRows([u('a', 2, 1.5, 3), u('b', 3, 2, 4)], 1).length, 1);
  assert.deepEqual(m.ratioRows(null), []);
});

test('the ratio axis always contains 1.0 and every interval', () => {
  const ax = m.ratioAxis([{ ratio: 1.6, low: 1.3, high: 2.0 }, { ratio: 0.4, low: 0.3, high: 0.5 }]);
  assert.equal(ax.min, 0.25);
  assert.equal(ax.max, 2);
  assert.ok(ax.ticks.includes(1));
  const wide = m.ratioAxis([{ ratio: 9, low: 5, high: 30 }]);
  assert.equal(wide.max, 32);
  const none = m.ratioAxis([]);
  assert.deepEqual([none.min, none.max], [0.5, 2], '1.0 never hugs an edge');
});

test('positions on the log axis put 1.0 in the middle of a symmetric axis, and say when clamped', () => {
  const ax = { min: 0.5, max: 2, ticks: [0.5, 1, 2] };
  assert.equal(m.ratioPos(1, ax).pos, 0.5);
  assert.equal(m.ratioPos(0.5, ax).pos, 0);
  assert.equal(m.ratioPos(2, ax).pos, 1);
  assert.deepEqual(m.ratioPos(10, ax), { pos: 1, clipped: true });
  assert.deepEqual(m.ratioPos(0.01, ax), { pos: 0, clipped: true });
});

const ev = (at, grade, impact, regressed, units = []) => ({
  at, health: 'Regressed', coverage: 0.8, decision: { grade, impact, action: 'hold', wanted: 'hold' },
  counts: { tested: 20, regressed }, units,
});

test('the timeline lists the judgements oldest first with what each decided', () => {
  const t = m.timeline([ev('2026-03-10T12:10:00Z', 'B', 0.2, 2), ev('2026-03-10T12:20:00Z', 'A', 0.3, 3)]);
  assert.deepEqual(t.map((x) => [x.index, x.grade, x.regressed]), [[0, 'B', 2], [1, 'A', 3]]);
  assert.equal(t[1].at, Date.parse('2026-03-10T12:20:00Z'));
  assert.deepEqual(m.timeline(undefined), []);
});

test('comparing two evaluations: change per query, furthest movement first, one-sided queries marked', () => {
  const a = ev('2026-03-10T12:10:00Z', 'B', 0.2, 1, [u('x', 1.2, 1.1, 1.3, { rejected: true }), u('gone', 1.5, 1.3, 1.7, { rejected: true })]);
  const b = ev('2026-03-10T12:20:00Z', 'A', 0.4, 2, [u('x', 1.8, 1.5, 2.1, { rejected: true }), u('new', 1.4, 1.2, 1.6, { rejected: true })]);
  const c = m.compareEvaluations(a, b);
  assert.deepEqual(c.grade, ['B', 'A']);
  assert.deepEqual(c.impact, [0.2, 0.4]);
  assert.equal(c.units[0].key, 'x');
  assert.ok(Math.abs(c.units[0].delta - 1.5) < 1e-9);
  const rest = c.units.slice(1);
  assert.deepEqual(rest.map((x) => x.key).sort(), ['gone', 'new']);
  assert.ok(rest.every((x) => Number.isNaN(x.delta)));
  assert.equal(rest.find((x) => x.key === 'gone').b, null);
  assert.equal(rest.find((x) => x.key === 'new').a, null);
});

const watch = (extra = {}) => ({
  name: 'orders', namespace: 'shop', cluster: 'pg', mode: 'shadow',
  actuation: { enabled: true, paused: false, namespaces: ['shop'] }, breakerOpen: false,
  collector: { staleSeconds: 20, lastScrape: '2026-03-10T11:59:40Z' }, ...extra,
});
const NOW = Date.parse('2026-03-10T12:00:00Z');

test('source rows say whether each source is answering, in words', () => {
  const rows = m.sourceRows({ watches: [watch({ arms: { usable: true, records: 12000, dropped: 3, newest: '2026-03-10T11:59:59Z' } })], history: { store: 'memory', recording: true, since: '2026-03-10T10:00:00Z' } }, NOW);
  assert.deepEqual(rows.map((r) => [r.source, r.tone, r.value]), [['Query statistics', 'ok', 'fresh'], ['Postgres log', 'ok', 'flowing'], ['History', 'ok', 'memory']]);
  assert.match(rows[1].detail, /12,000 records read, 3 dropped/);
});

test('a stale scrape is a problem, no scrape yet and an unusable log are warnings', () => {
  const stale = m.sourceRows({ watches: [watch({ collector: { staleSeconds: 600, lastScrape: '2026-03-10T11:50:00Z' } })] }, NOW);
  assert.deepEqual([stale[0].tone, stale[0].value], ['bad', '10m old']);
  const never = m.sourceRows({ watches: [watch({ collector: { staleSeconds: -1 } })] }, NOW);
  assert.deepEqual([never[0].tone, never[0].value], ['warn', 'no scrape yet']);
  const bad = m.sourceRows({ watches: [watch({ arms: { usable: false, records: 0, dropped: 0, why: 'log_line_prefix is missing %a' } })] }, NOW);
  assert.deepEqual([bad[1].tone, bad[1].detail], ['warn', 'log_line_prefix is missing %a']);
  const off = m.sourceRows({ watches: [watch()] }, NOW);
  assert.equal(off[1].value, 'off');
});

test('a history store that cannot be written to is a problem, said plainly', () => {
  const r = m.sourceRows({ watches: [], history: { store: 'postgres', recording: false } }, NOW);
  assert.deepEqual([r[0].tone, r[0].value], ['bad', 'postgres, not recording']);
  assert.match(r[0].detail, /gap/);
  assert.deepEqual(m.sourceRows(null, NOW), []);
});

test('safety rows say what the radar may do, and why it may not', () => {
  const ok = m.safetyRows({ watches: [watch()] })[0];
  assert.deepEqual([ok.mode, ok.acting, ok.breaker, ok.namespaces], ['shadow', 'allowed', 'closed', ['shop']]);
  const off = m.safetyRows({ watches: [watch({ actuation: { enabled: true, paused: true }, breakerOpen: true })] })[0];
  assert.deepEqual([off.acting, off.actingTone, off.breaker, off.breakerTone], ['switched off', 'warn', 'open', 'bad']);
  assert.match(off.breakerDetail, /only alerts/);
  assert.deepEqual(m.safetyRows(null), []);
});

test('which query a chart opens on: the engine\'s first, or the flagged one that moved furthest', () => {
  const units = [u('a', 1.2, 1.1, 1.3, { rejected: true }), u('b', 3, 2, 4, { rejected: true }), u('c', 0.2, 0.1, 0.3, { rejected: true }), u('d', 9, 5, 12)];
  assert.equal(m.defaultUnit(units), 'a');
  assert.equal(m.defaultUnit(units, 'first'), 'a');
  assert.equal(m.defaultUnit(units, 'worst'), 'c', 'a ratio of 0.2 is five times away from 1, further than 3');
  assert.equal(m.defaultUnit([u('x', 1, 0.9, 1.1)], 'worst'), 'x', 'nothing flagged: the first');
  assert.equal(m.defaultUnit([]), null);
  assert.equal(m.defaultUnit(null), null);
});

test('queries are named by statement and table, and repeats are numbered', () => {
  assert.equal(m.queryLabel('SELECT:orders'), 'SELECT on orders');
  assert.equal(m.queryLabel('UPDATE:orders,items'), 'UPDATE on orders,items');
  assert.equal(m.queryLabel('other'), 'other');
  assert.equal(m.queryLabel('', 'abcdef123456'), 'abcdef12');
  const names = m.labelUnits([{ key: 'k1', family: 'SELECT:orders' }, { key: 'k2', family: 'SELECT:orders' }, { key: 'k3', family: 'SELECT:items' }]);
  assert.deepEqual([...names.values()], ['SELECT on orders #1', 'SELECT on orders #2', 'SELECT on items']);
  // The same query keeps its name when it is looked up through another list.
  assert.equal(m.labelUnits([{ key: 'k3', family: 'SELECT:items' }]).get('k3'), 'SELECT on items');
  const rows = m.ratioRows([u('a', 2, 1.5, 2.5), u('b', 1.1, 0.9, 1.3)]);
  assert.equal(rows[0].label, 'SELECT on a');
});

// ---- what a panel's settings ask it to show --------------------------------------------------

const D = (id, state, at, extra = {}) => ({ id, app: id, namespace: 'ns', at, state, sentence: '', ...extra });

test('deploys are filtered by state, ordered, and cut after counting', () => {
  const t = (h) => new Date(2026, 0, 1, h).toISOString();
  const ds = [
    { id: 'old-ok', at: t(8), phase: 'judged', health: 'Healthy', latest: {} },
    { id: 'reg', at: t(9), phase: 'judged', health: 'Regressed', latest: {} },
    { id: 'wait', at: t(10), phase: 'observing', latest: null },
    { id: 'acted', at: t(11), phase: 'acted', health: 'Regressed', latest: {} },
    { id: 'new-ok', at: t(12), phase: 'judged', health: 'Healthy', latest: {} },
  ];
  const ids = (o) => m.pickDeploys(ds, o).list.map((d) => d.id);
  assert.deepEqual(ids({ sort: 'newest', limit: 10 }), ['new-ok', 'acted', 'wait', 'reg', 'old-ok'], 'newest first');
  assert.deepEqual(ids({ sort: 'worst', limit: 10 }), ['reg', 'acted', 'wait', 'new-ok', 'old-ok'], 'regressed, then acted on, then being judged, then healthy; newest first within a state');
  assert.deepEqual(ids({ show: 'problems', limit: 10 }).sort(), ['acted', 'reg']);
  assert.deepEqual(ids({ show: 'watching', limit: 10 }), ['wait']);
  assert.deepEqual(ids({ show: 'healthy', limit: 10 }), ['new-ok', 'old-ok']);
  const cut = m.pickDeploys(ds, { show: 'healthy', limit: 1 });
  assert.deepEqual([cut.list.length, cut.matching, cut.total], [1, 2, 5], 'counted before the cut');
  assert.deepEqual(ds.map((d) => d.id), ['old-ok', 'reg', 'wait', 'acted', 'new-ok'], 'the input is not reordered');
  assert.deepEqual(m.pickDeploys(null, {}), { list: [], matching: 0, total: 0 });
});

test('the evidence table can list only flagged queries and order them four ways', () => {
  const us = [
    u('b', 1.2, 1.1, 1.3, { impactShare: 0.1, rejected: false, family: 'SELECT:b' }),
    u('a', 3, 2, 4, { impactShare: 0.02, rejected: true, family: 'SELECT:a' }),
    u('c', 1.5, 1.2, 1.9, { impactShare: 0.5, rejected: true, family: 'UPDATE:c' }),
  ];
  const ks = (o) => m.evidenceRows(us, o).map((x) => x.key);
  assert.deepEqual(ks({}), ['b', 'a', 'c'], "the radar's own order is kept");
  assert.deepEqual(ks({ show: 'flagged' }), ['a', 'c']);
  assert.deepEqual(ks({ sort: 'impact' }), ['c', 'b', 'a']);
  assert.deepEqual(ks({ sort: 'slowdown' }), ['a', 'c', 'b']);
  assert.deepEqual(ks({ sort: 'name' }), ['a', 'b', 'c']);
  assert.deepEqual(us.map((x) => x.key), ['b', 'a', 'c'], 'the input is not reordered');
  assert.deepEqual(m.evidenceRows(null), []);
});

test('the ratio chart can show only flagged queries', () => {
  const us = [u('x', 1.6, 1.3, 2, { rejected: true }), u('y', 1.1, 0.9, 1.3)];
  assert.deepEqual(m.ratioRows(us, 10, 'flagged').map((r) => r.key), ['x']);
  assert.deepEqual(m.ratioRows(us, 10).map((r) => r.key), ['x', 'y']);
});

test('the activity feed can show only problems or only actions', () => {
  const c = {
    id: 'd', app: 'a', namespace: 'n',
    history: [{ at: '2026-01-01T10:00:00Z', health: 'Regressed', decision: { grade: 'A' }, counts: {} }, { at: '2026-01-01T10:05:00Z', health: 'Healthy', decision: { grade: '' }, counts: {} }],
    actions: [{ at: '2026-01-01T10:06:00Z', action: 'rollback', result: 'done' }, { at: '2026-01-01T10:07:00Z', action: 'rollback', result: 'failed' }],
  };
  const kinds = (show) => m.feedEntries([c], 20, show).map((e) => `${e.kind}${e.bad ? '!' : ''}`);
  assert.equal(kinds('all').length, 4);
  assert.deepEqual(kinds('actions'), ['action!', 'action']);
  assert.deepEqual(kinds('problems').sort(), ['action!', 'evaluation!']);
  assert.equal(m.feedEntries([c], 1, 'all').length, 1, 'the limit applies after the filter');
});
