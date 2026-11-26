// Run with: node --test internal/dashboard/uitest/*.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import * as m from '../ui/js/model.js';
import * as c from '../ui/js/chartmath.js';

const dep = (over) => ({ id: 'a', app: 'orders', namespace: 'shop', at: '2026-03-10T12:00:00Z', phase: 'decided', evaluations: 2, health: 'Regressed', latest: { grade: 'B', action: 'hold' }, ...over });

test('relative time reads from the server clock, past and future', () => {
  const now = Date.parse('2026-03-10T12:36:00Z');
  assert.equal(m.relative(Date.parse('2026-03-10T12:00:00Z'), now), '36m ago');
  assert.equal(m.relative(Date.parse('2026-03-10T12:40:00Z'), now), 'in 4m');
  assert.equal(m.relative(now - 1000, now), 'just now');
  assert.equal(m.relative(now - 3 * 3600e3, now), '3h ago');
  assert.equal(m.relative(now - 50 * 3600e3, now), '2d ago');
});

test('the clock follows the server, even for data recorded in the past', () => {
  const off = m.clockOffset('2026-03-10T12:36:00Z', 1_000_000);
  assert.equal(m.estimateNow(off, 1_000_000), Date.parse('2026-03-10T12:36:00Z'));
  assert.equal(m.estimateNow(off, 1_060_000), Date.parse('2026-03-10T12:37:00Z'));
  assert.equal(m.clockOffset('not a date', 5), 0);
});

test('a deploy has one state, and only state', () => {
  assert.equal(m.tone(dep({ phase: 'acted' })), 'acted');
  assert.equal(m.tone(dep({ phase: 'verified' })), 'acted');
  assert.equal(m.tone(dep({})), 'regressed');
  assert.equal(m.tone(dep({ health: 'Healthy' })), 'ok');
  assert.equal(m.tone(dep({ phase: 'observing', latest: null, health: '' })), 'watching');
  assert.equal(m.tone(dep({ health: 'Inconclusive' })), 'quiet');
  assert.equal(m.stateLabel(dep({ phase: 'verified' })), 'Acted and checked');
});

test('the first card is the one that needs a person', () => {
  const list = [dep({ id: 'ok', health: 'Healthy', at: '2026-03-10T13:00:00Z' }), dep({ id: 'bad', at: '2026-03-10T11:00:00Z' }), dep({ id: 'acted', phase: 'acted', at: '2026-03-10T10:00:00Z' })];
  assert.equal(m.defaultSelection(list), 'acted');
  assert.equal(m.defaultSelection(list.slice(0, 2)), 'bad');
  assert.equal(m.defaultSelection([]), null);
});

const ov = (w, extra) => ({ scope: { all: true }, watches: [{ name: 'w', namespace: 'r', mode: 'shadow', actuation: { enabled: false, paused: false }, breakerOpen: false, collector: { staleSeconds: 20 }, ...w }], ...extra });
const get = (items, id) => items.find((i) => i.id === id);

test('the status strip says first whether the data can be trusted', () => {
  assert.equal(get(m.statusItems(ov({}), {}, 0), 'fresh').tone, 'ok');
  assert.equal(get(m.statusItems(ov({ collector: { staleSeconds: 600 } }), {}, 0), 'fresh').tone, 'bad');
  assert.equal(get(m.statusItems(ov({ collector: { staleSeconds: -1 } }), {}, 0), 'fresh').tone, 'warn');
  assert.equal(m.statusItems(ov({}), {}, 0)[0].id, 'fresh');
});

test('the strip names what may act, what is stopped and what is unusable', () => {
  const items = m.statusItems(ov({ mode: 'enforce', actuation: { enabled: true, paused: false }, breakerOpen: true, arms: { usable: false, why: 'No log line has arrived yet.' } }), {}, 0);
  assert.equal(get(items, 'mode').value, 'enforce');
  assert.equal(get(items, 'actuation').value, 'on');
  assert.equal(get(items, 'breaker').tone, 'bad');
  assert.equal(get(items, 'arms').tone, 'warn');
  assert.match(get(items, 'arms').detail, /No log line/);
  assert.equal(get(m.statusItems(ov({ actuation: { enabled: true, paused: true } }), {}, 0), 'actuation').value, 'switched off');
});

test('scope is named so a missing deploy is explained', () => {
  assert.equal(get(m.statusItems(ov({}, { scope: { all: false, namespaces: ['billing', 'shop'] } }), {}, 0), 'scope').value, 'billing, shop');
  assert.equal(get(m.statusItems(ov({}), {}, 0), 'scope').value, 'all namespaces');
});

test('recorded data is never mistaken for a live system', () => {
  const first = m.statusItems(ov({}), { origin: 'fixture', note: 'sample' }, 0)[0];
  assert.equal(first.id, 'origin');
  assert.match(first.value, /Recorded/);
  assert.equal(m.statusItems({ watches: [], scope: { all: true } }, {}, 0)[0].tone, 'warn');
});

test('the feed is newest first and says what happened in words', () => {
  const cases = [{
    id: 'a', app: 'orders', namespace: 'shop',
    history: [
      { at: '2026-03-10T12:30:00Z', health: 'Regressed', decision: { grade: 'B', impact: 0.13, action: 'hold', enforced: true } },
      { at: '2026-03-10T12:25:00Z', health: 'Healthy', decision: { grade: '', impact: 0, action: 'none' } },
    ],
    actions: [{ at: '2026-03-10T12:30:00Z', action: 'pause', result: 'done' }, { at: '2026-03-10T12:45:00Z', action: 'abort', result: 'persisted' }],
  }];
  const f = m.feedEntries(cases);
  assert.deepEqual(f.map((e) => e.kind), ['action', 'action', 'evaluation', 'evaluation']);
  assert.equal(f[0].text, 'Checked the abort: the regression is still there');
  assert.equal(f[0].bad, true);
  assert.equal(f[1].text, 'Paused the rollout');
  assert.match(f[2].text, /grade B, 13% added database time, a pause/);
  assert.equal(f[3].text, 'Judged: nothing regressed beyond the margin');
});

test('numbers are rounded the way a person reads them', () => {
  assert.equal(m.percent(0.1326), '13%');
  assert.equal(m.percent(0.042), '4.2%');
  assert.equal(m.percent(0), '0%');
  assert.equal(m.percent(NaN), '–');
  assert.equal(m.times(1.5515), '1.55×');
  assert.equal(m.times(0), '–');
  assert.equal(m.ms(3.14159), '3.14 ms');
  assert.equal(m.ms(87.65), '87.7 ms');
});

test('chart arithmetic: scales, ticks, gaps, baseline, nearest', () => {
  const x = c.linear(0, 10, 100, 200);
  assert.equal(x(0), 100); assert.equal(x(5), 150); assert.equal(x(10), 200);
  assert.deepEqual(c.niceTicks(0, 100, 4), [0, 20, 40, 60, 80, 100]);
  assert.deepEqual(c.niceTicks(0, 87, 4), [0, 20, 40, 60, 80]);
  assert.equal(c.pathD([[0, 1], [10, 2], [100, 3]], 20), 'M0.0,1.0L10.0,2.0M100.0,3.0'); // the gap is not bridged
  assert.equal(c.medianGap([0, 60, 120, 180, 1000]), 60);
  const pts = [{ at: 1, meanMs: 10, n: 1 }, { at: 2, meanMs: 20, n: 3 }, { at: 9, meanMs: 99, n: 1 }];
  assert.equal(c.baselineBefore(pts, 5), (10 * 1 + 20 * 3) / 4); // weighted by calls
  assert.ok(Number.isNaN(c.baselineBefore(pts, 0)));
  assert.equal(c.nearest([0, 10, 20, 30], 14), 1);
  assert.equal(c.nearest([0, 10, 20, 30], 99), 3);
  assert.equal(c.nearest([], 1), -1);
  assert.deepEqual(c.extent([]), [0, 1]);
});

test('the hero answers "is anything wrong?" in one sentence', () => {
  const bad = dep({ id: 'b' }), acted = dep({ id: 'a', phase: 'acted' }), ok = dep({ id: 'o', health: 'Healthy' });
  const watching = dep({ id: 'w', phase: 'observing', latest: null, health: '' });
  let s = m.summary([ok, bad, acted, watching]);
  assert.equal(s.lead, '2 of 4 deploys regressed.');
  assert.equal(s.detail, 'The radar acted on 1. 1 still being judged.');
  assert.deepEqual(s.counts, { regressed: 1, acted: 1, watching: 1, ok: 1, quiet: 0 });
  assert.equal(m.summary([bad]).lead, '1 of 1 deploy regressed.');
  assert.equal(m.summary([ok, watching]).lead, 'Nothing has regressed so far.');
  s = m.summary([ok, ok]);
  assert.equal(s.lead, 'Nothing regressed.');
  assert.equal(s.detail, '2 deploys judged healthy.');
  assert.match(m.summary([]).lead, /No deploys in this range/);
});

test('the hero bar has a segment per deploy, worst first, and groups past thirty', () => {
  const few = m.summary([dep({ id: 'o', health: 'Healthy' }), dep({ id: 'b' })]);
  assert.deepEqual(few.segs, [{ tone: 'regressed', n: 1 }, { tone: 'ok', n: 1 }]);
  const many = m.summary(Array.from({ length: 40 }, (_, i) => dep({ id: String(i), health: i < 3 ? 'Regressed' : 'Healthy' })));
  assert.deepEqual(many.segs, [{ tone: 'regressed', n: 3 }, { tone: 'ok', n: 37 }]);
});

test('a grade is a strength out of four', () => {
  assert.deepEqual(['A', 'B', 'C', 'D', '', undefined].map(m.gradeLevel), [4, 3, 2, 1, 0, 0]);
});
