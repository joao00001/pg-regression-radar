import test from 'node:test';
import assert from 'node:assert/strict';
import { createSettings, normalize, resolveTheme, DEFAULTS, KEY, THEME_KEY } from '../ui/js/settings.js';

const mem = (init = {}) => { const m = new Map(Object.entries(init)); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k), raw: m }; };

test('the defaults: system theme, alerts say they are healthy and then go, sound off', () => {
  const s = createSettings({ storage: mem() }).get();
  assert.equal(s.theme, 'system');
  assert.equal(s.resolved, 'say');
  assert.equal(s.level, 'all');
  assert.equal(s.max, 3);
  assert.equal(s.dismissal, 'persist');
  assert.equal(s.sound.on, false, 'no sound until the person asks for it');
});

test('a choice is checked, kept, and told to everyone listening', () => {
  const storage = mem();
  const s = createSettings({ storage });
  const seen = [];
  s.subscribe((now, before) => seen.push([now.level, before.level]));
  s.set({ level: 'critical' });
  s.set({ level: 'critical' });
  assert.deepEqual(seen, [['critical', 'all']], 'setting the same value again says nothing');
  assert.equal(createSettings({ storage }).get().level, 'critical', 'kept for the next visit');
});

test('values that are not allowed fall back, whatever was stored or asked', () => {
  const n = normalize({ theme: 'purple', resolved: 7, level: null, max: 4, dismissal: 'x', sound: { on: 'yes', volume: 9, events: { alert: 'no', critical: false } } });
  assert.equal(n.theme, 'system');
  assert.equal(n.resolved, 'say');
  assert.equal(n.max, 3, '4 is not one of the choices');
  assert.equal(n.sound.on, false);
  assert.equal(n.sound.volume, 1, 'volume stays between 0 and 1');
  assert.equal(n.sound.events.alert, true, 'a damaged switch keeps its default');
  assert.equal(n.sound.events.critical, false);
  assert.equal(normalize({ sound: { volume: -3 } }).sound.volume, 0);
  assert.equal(normalize({ sound: { volume: 'loud' } }).sound.volume, DEFAULTS.sound.volume);
  assert.equal(normalize({ max: '5' }).max, 5, 'a number that came as text still counts');
});

test('sound switches can be changed one at a time without losing the others', () => {
  const s = createSettings({ storage: mem() });
  s.set({ sound: { events: { alert: false } } });
  s.set({ sound: { on: true } });
  const v = s.get().sound;
  assert.deepEqual(v.events, { alert: false, critical: true, healthy: true });
  assert.equal(v.on, true);
  assert.equal(v.volume, 0.6);
});

test('the theme keeps its own key, and "system" removes it', () => {
  const storage = mem();
  const s = createSettings({ storage });
  s.set({ theme: 'light' });
  assert.equal(storage.getItem(THEME_KEY), 'light', 'the script that runs before the first paint reads this');
  assert.ok(!JSON.parse(storage.getItem(KEY)).theme, 'and it is not kept twice');
  s.set({ theme: 'system' });
  assert.equal(storage.getItem(THEME_KEY), null);
  assert.equal(createSettings({ storage: mem({ [THEME_KEY]: 'dark' }) }).get().theme, 'dark', 'a theme chosen before the settings existed is kept');
});

test('storage that is blocked, full or damaged never breaks it', () => {
  const blocked = { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('full'); }, removeItem() { throw new Error('blocked'); } };
  const s = createSettings({ storage: blocked });
  assert.doesNotThrow(() => s.set({ level: 'critical' }));
  assert.equal(s.get().level, 'critical', 'it holds for this page even though it cannot be kept');
  assert.equal(createSettings({ storage: mem({ [KEY]: '{not json' }) }).get().max, 3);
  assert.equal(createSettings({ storage: mem({ [KEY]: '[1,2]' }) }).get().resolved, 'say');
});

test('a listener that fails does not stop the others', () => {
  const s = createSettings({ storage: mem() });
  let heard = 0;
  s.subscribe(() => { throw new Error('boom'); });
  s.subscribe(() => { heard++; });
  s.set({ max: 5 });
  assert.equal(heard, 1);
});

test('the theme to wear: the system\'s unless one was chosen', () => {
  assert.equal(resolveTheme('system', true), 'light');
  assert.equal(resolveTheme('system', false), 'dark');
  assert.equal(resolveTheme('dark', true), 'dark');
  assert.equal(resolveTheme('light', false), 'light');
});
