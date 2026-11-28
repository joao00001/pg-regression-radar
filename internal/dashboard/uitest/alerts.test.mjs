import test from 'node:test';
import assert from 'node:assert/strict';
import { createAlerts, stageOf, recovered, dismissKey, DISMISS_KEY } from '../ui/js/alerts.js';

const mem = () => { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), raw: m }; };
const dep = (id, extra = {}) => {
  const [namespace, app] = id.split('/');
  return { id, namespace, app, at: '2026-03-10T09:00:00Z', phase: 'decided', health: 'Regressed', sentence: `${app} regressed`, latest: { at: '2026-03-10T09:12:00Z' }, ...extra };
};
const acted = (id, result = 'done', action = 'abort', extra = {}) => dep(id, {
  phase: result === 'done' ? 'acted' : 'verified',
  lastAction: { at: '2026-03-10T09:15:00Z', action, result, reason: result === 'persisted' ? 'AbortPersisted' : 'Acted', docs: 'https://x/#r' },
  ...extra,
});
const ids = (a) => a.visible().map((x) => x.id);

test('a regression raises an alert and it stays across polls until it is dismissed', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [dep('shop/orders/3')] });
  assert.deepEqual(ids(a), ['deploy:shop/orders/3']);
  assert.equal(a.visible()[0].title, 'Regression detected');
  assert.equal(a.visible()[0].level, 'alert');
  for (let i = 0; i < 5; i++) a.update({ deploys: [dep('shop/orders/3')] });
  assert.equal(a.visible().length, 1, 'polling again does not add or drop it');
  a.dismiss(dismissKey(a.visible()[0]));
  assert.deepEqual(ids(a), []);
  a.update({ deploys: [dep('shop/orders/3')] });
  assert.deepEqual(ids(a), [], 'a dismissed alert does not come back on the next poll');
});

test('acted on, then healthy again: the alert goes away by itself', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [dep('shop/orders/3')] });
  a.update({ deploys: [acted('shop/orders/3', 'done', 'abort')] });
  assert.equal(a.visible()[0].title, 'Regressed, rollout aborted', 'the one alert follows the deploy');
  assert.equal(a.visible().length, 1);
  const { resolved } = a.update({ deploys: [dep('shop/orders/3', { phase: 'verified', health: 'Healthy', lastAction: { at: '2026-03-10T09:30:00Z', action: 'abort', result: 'recovered' } })] });
  assert.deepEqual(ids(a), []);
  assert.equal(resolved.length, 1);
  assert.equal(resolved[0].app, 'orders', 'the caller is told what was resolved, to say so');
});

test('a pause that is lifted and a later clean evaluation also clear it', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [acted('shop/cart/4', 'done', 'pause')] });
  assert.equal(a.visible()[0].title, 'Regressed, rollout paused');
  a.update({ deploys: [dep('shop/cart/4', { phase: 'acted', health: 'Healthy', lastAction: { at: '2026-03-10T09:20:00Z', action: 'pause', result: 'done' } })] });
  assert.deepEqual(ids(a), []);
});

test('still regressed after the abort is its own, worse alert, and it clears when the deploy recovers', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [acted('payments/checkout/7', 'done')] });
  const first = a.visible()[0];
  a.dismiss(dismissKey(first));
  assert.deepEqual(ids(a), [], 'the person dismissed the first level of trouble');
  a.update({ deploys: [acted('payments/checkout/7', 'persisted')] });
  const worse = a.visible();
  assert.equal(worse.length, 1, 'worse trouble is news even after a dismissal');
  assert.equal(worse[0].level, 'critical');
  assert.equal(worse[0].title, 'Still regressed after the abort');
  assert.equal(worse[0].reason, 'AbortPersisted');
  // Dismissing the critical one does not hide a later, different problem on another deploy.
  a.dismiss(dismissKey(worse[0]));
  a.update({ deploys: [acted('payments/checkout/7', 'persisted'), dep('shop/cart/4')] });
  assert.deepEqual(ids(a), ['deploy:shop/cart/4']);
  // Later the deploy is healthy: nothing left, and the dismissals are forgotten.
  a.update({ deploys: [dep('payments/checkout/7', { phase: 'verified', health: 'Healthy', lastAction: { at: '2026-03-10T10:00:00Z', action: 'abort', result: 'recovered' } }), dep('shop/cart/4')] });
  assert.deepEqual(ids(a), ['deploy:shop/cart/4']);
  a.update({ deploys: [dep('payments/checkout/7'), dep('shop/cart/4')] });
  assert.ok(ids(a).includes('deploy:payments/checkout/7'), 'a relapse after recovery alerts again');
});

test('a failed pause or abort is critical', () => {
  const s = stageOf(acted('shop/orders/3', 'failed', 'pause'));
  assert.equal(s.level, 'critical');
  assert.equal(s.title, 'Could not pause the rollout');
  assert.equal(stageOf(acted('shop/orders/3', 'failed', 'abort')).title, 'Could not abort the rollout');
});

test('an inconclusive or unfinished reading is not an all-clear', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [dep('shop/orders/3')] });
  a.update({ deploys: [dep('shop/orders/3', { health: 'Inconclusive' })] });
  assert.deepEqual(ids(a), ['deploy:shop/orders/3']);
  a.update({ deploys: [dep('shop/orders/3', { health: '', phase: 'observing' })] });
  assert.deepEqual(ids(a), ['deploy:shop/orders/3']);
  assert.equal(recovered(dep('x/y/1', { health: 'Inconclusive' })), false);
  assert.equal(recovered(dep('x/y/1', { health: 'Healthy' })), true);
  assert.equal(recovered(dep('x/y/1', { health: 'Regressed', lastAction: { result: 'recovered' } })), false, 'a recovered abort check does not outvote a regressed reading');
});

test('a dismissal survives a reload, and is forgotten after a recovery', () => {
  const storage = mem();
  const a = createAlerts({ storage });
  a.update({ deploys: [dep('shop/orders/3')] });
  a.dismiss(dismissKey(a.visible()[0]));
  const b = createAlerts({ storage });
  b.update({ deploys: [dep('shop/orders/3')] });
  assert.deepEqual(ids(b), [], 'still dismissed after the page was reloaded');
  b.update({ deploys: [dep('shop/orders/3', { health: 'Healthy' })] });
  assert.equal(Object.keys(JSON.parse(storage.getItem(DISMISS_KEY))).length, 0);
});

test('a deploy that is no longer reported keeps its alert, marked, unless the list was partial', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [dep('shop/orders/3')] });
  a.update({ deploys: [], complete: false });
  assert.equal(a.visible()[0].missing, false, 'a partial list says nothing about a deploy that is not in it');
  a.update({ deploys: [] });
  assert.equal(a.visible().length, 1, 'silence is not an all-clear');
  assert.equal(a.visible()[0].missing, true);
  a.update({ deploys: [dep('shop/orders/3')] });
  assert.equal(a.visible()[0].missing, false, 'reported again, so no longer marked');
});

test('an open breaker is an alert per watch until it is closed', () => {
  const a = createAlerts({ storage: mem() });
  const w = (open, extra = {}) => ({ name: 'checkout-db', namespace: 'radar', breakerOpen: open, conditions: open ? [{ type: 'Actuating', status: 'False', reason: 'BreakerOpen', message: 'Earlier aborts did not help. Close it with: kubectl annotate postgreswatch checkout-db See https://x/#breakeropen', docs: 'https://x/#breakeropen' }] : [], ...extra });
  a.update({ watches: [w(true)] });
  const x = a.visible()[0];
  assert.equal(x.kind, 'breaker');
  assert.equal(x.level, 'critical');
  assert.equal(x.reason, 'BreakerOpen');
  assert.ok(!x.text.includes('See https'), 'the docs link is shown as a link, not in the sentence');
  assert.ok(x.text.includes('kubectl annotate'), 'the command to close it is kept');
  a.update({ watches: [w(false)] });
  assert.deepEqual(ids(a), []);
});

test('an older manager that sends no conditions still gets a breaker alert', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ watches: [{ name: 'w', namespace: 'radar', breakerOpen: true }] });
  assert.equal(a.visible().length, 1);
  assert.ok(a.visible()[0].text.length > 20);
});

test('the worst come first, then the newest', () => {
  const a = createAlerts({ storage: mem() });
  a.update({
    deploys: [
      dep('shop/a/1', { latest: { at: '2026-03-10T09:10:00Z' } }),
      dep('shop/b/1', { latest: { at: '2026-03-10T09:40:00Z' } }),
      acted('shop/c/1', 'persisted', 'abort', { lastAction: { at: '2026-03-10T08:00:00Z', action: 'abort', result: 'persisted' } }),
    ],
    watches: [{ name: 'w', namespace: 'radar', breakerOpen: true }],
  });
  assert.deepEqual(ids(a), ['deploy:shop/c/1', 'watch:radar/w', 'deploy:shop/b/1', 'deploy:shop/a/1']);
  const v = a.visible();
  assert.equal(v[0].level, 'critical');
  assert.equal(v[1].level, 'critical');
  assert.equal(v[2].id, 'deploy:shop/b/1');
  assert.equal(v[3].id, 'deploy:shop/a/1');
});

test('dismiss all hides what is shown, and only that', () => {
  const a = createAlerts({ storage: mem() });
  a.update({ deploys: [dep('shop/a/1'), dep('shop/b/1')] });
  a.dismissAll();
  assert.deepEqual(ids(a), []);
  a.update({ deploys: [dep('shop/a/1'), dep('shop/b/1'), dep('shop/c/1')] });
  assert.deepEqual(ids(a), ['deploy:shop/c/1']);
});

test('blocked, full or corrupt storage never breaks it, and old dismissals are dropped', () => {
  const blocked = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('full'); } };
  const a = createAlerts({ storage: blocked });
  a.update({ deploys: [dep('shop/a/1')] });
  a.dismiss(dismissKey(a.visible()[0]));
  assert.deepEqual(ids(a), [], 'dismissed for this page even though it cannot be kept');
  const corrupt = mem(); corrupt.setItem(DISMISS_KEY, '{not json');
  assert.doesNotThrow(() => createAlerts({ storage: corrupt }));
  const old = mem(); const t = 1_800_000_000_000;
  old.setItem(DISMISS_KEY, JSON.stringify({ 'deploy:shop/a/1#alert': t - 40 * 24 * 3600e3, 'deploy:shop/b/1#alert': t - 1000 }));
  const b = createAlerts({ storage: old, now: () => t });
  b.update({ deploys: [dep('shop/a/1'), dep('shop/b/1')] });
  assert.deepEqual(ids(b), ['deploy:shop/a/1'], 'a month-old dismissal has expired, a fresh one holds');
});

test('dismissals that are not to outlive the page are not kept, and switching it on or off follows', () => {
  const storage = mem();
  let remember = false;
  const a = createAlerts({ storage, remember: () => remember });
  a.update({ deploys: [dep('shop/a/1')] });
  a.dismiss(dismissKey(a.visible()[0]));
  assert.deepEqual(ids(a), [], 'dismissed for this page');
  assert.equal(storage.getItem(DISMISS_KEY), null, 'but not kept for the next one');
  assert.deepEqual(ids(createAlerts({ storage, remember: () => false })), [], 'a new page starts with none dismissed');
  remember = true; a.remembering(true);
  assert.equal(Object.keys(JSON.parse(storage.getItem(DISMISS_KEY))).length, 1, 'what was dismissed is kept from now on');
  remember = false; a.remembering(false);
  assert.equal(storage.getItem(DISMISS_KEY), null, 'and let go of when the person turns it off');
  const stored = mem(); stored.setItem(DISMISS_KEY, JSON.stringify({ 'deploy:shop/a/1#alert': Date.now() }));
  const b = createAlerts({ storage: stored, remember: () => false });
  b.update({ deploys: [dep('shop/a/1')] });
  assert.deepEqual(ids(b), ['deploy:shop/a/1'], 'old dismissals are not read when they are not to be remembered');
});

test('a resolved card is teal in every theme that recolours severity', async () => {
  const { readFileSync } = await import('node:fs');
  const css = readFileSync(new URL('../ui/app.css', import.meta.url), 'utf8');
  const themed = [...css.matchAll(/^:root\[data-theme="(\w+)"\] \.alert-(?:warn|critical) \{[^}]*--hue:/gm)].map((m) => m[1]);
  assert.ok(themed.length > 0, 'the light theme recolours warn and critical');
  for (const theme of new Set(themed)) {
    const rule = new RegExp(`^:root\\[data-theme="${theme}"\\] \\.alert\\[data-tone="ok"\\] \\{[^}]*--hue: var\\(--teal\\)`, 'm');
    assert.match(css, rule, `a resolved card keeps the severity colour in the ${theme} theme`);
  }
});
