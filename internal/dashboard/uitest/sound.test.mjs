import test from 'node:test';
import assert from 'node:assert/strict';
import { createSound, RECIPES, PRIORITY, strongest, duration } from '../ui/js/sound.js';
import { createSettings } from '../ui/js/settings.js';

const mem = () => { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k) }; };

// A stand-in for the browser's audio: it keeps what was scheduled, and can be asleep or awake.
function fakeAudio(state = 'running') {
  const made = { ctxs: [], oscs: [] };
  class Ctx {
    constructor() { this.state = state; this.currentTime = 10; this.destination = {}; made.ctxs.push(this); }
    resume() { this.state = 'running'; return Promise.resolve(); }
    createGain() { return { gain: { value: 1, setValueAtTime() {}, exponentialRampToValueAtTime() {} }, connect() {} }; }
    createBiquadFilter() { return { type: '', frequency: { value: 0 }, connect() {} }; }
    createOscillator() { const o = { type: '', frequency: { value: 0 }, connect() {}, start(t) { o.at = t; }, stop() {} }; made.oscs.push(o); return o; }
  }
  return { Ctx, made };
}
const fakeWin = () => { const heard = []; return { heard, addEventListener() {}, removeEventListener() {}, dispatchEvent: (e) => { heard.push(e.detail); return true; } }; };
const setup = ({ state, on = true } = {}) => {
  const settings = createSettings({ storage: mem() });
  if (on) settings.set({ sound: { on: true } });
  const audio = fakeAudio(state), win = fakeWin();
  const sound = createSound({ settings, win, AudioCtx: audio.Ctx });
  return { settings, audio, win, sound };
};
const later = (ms = 80) => new Promise((r) => setTimeout(r, ms));

test('each sound is a few notes, in order, and critical outranks the rest', () => {
  assert.deepEqual(Object.keys(RECIPES).sort(), [...PRIORITY].sort());
  for (const k of PRIORITY) {
    assert.ok(RECIPES[k].notes.length >= 2);
    assert.ok(RECIPES[k].notes.every((n) => n.f > 100 && n.f < 2000 && n.d > 0 && n.t >= 0));
    assert.ok(duration(k) < 1.5, 'short enough not to nag');
  }
  assert.equal(strongest('healthy', 'critical'), 'critical');
  assert.equal(strongest('alert', 'healthy'), 'alert');
  assert.equal(strongest(null, 'healthy'), 'healthy');
});

test('nothing plays while the sound is off, or that event is switched off', async () => {
  const off = setup({ on: false });
  off.sound.play('alert'); await later();
  assert.equal(off.audio.made.oscs.length, 0);
  assert.equal(off.sound.status(), 'off');
  const some = setup();
  some.settings.set({ sound: { events: { healthy: false } } });
  some.sound.play('healthy'); await later();
  assert.equal(some.audio.made.oscs.length, 0, 'healthy is switched off');
  some.sound.play('alert'); await later();
  assert.equal(some.audio.made.oscs.length, RECIPES.alert.notes.length);
});

test('sounds asked for together become the most important one', async () => {
  const { sound, win, audio } = setup();
  sound.play('healthy'); sound.play('alert'); sound.play('critical'); sound.play('alert');
  await later();
  assert.deepEqual(win.heard.map((d) => d.kind), ['critical']);
  assert.equal(audio.made.oscs.length, RECIPES.critical.notes.length);
  assert.ok(audio.made.oscs.every((o, i) => o.at > 10 && o.frequency.value === RECIPES.critical.notes[i].f));
});

test('the volume follows the setting', async () => {
  const { sound, settings, win } = setup();
  settings.set({ sound: { volume: 0.25 } });
  sound.play('alert'); await later();
  assert.equal(win.heard[0].volume, 0.25);
});

test('a page the browser has not woken up stays silent and queues nothing', async () => {
  const { sound, audio, win } = setup({ state: 'suspended' });
  // resume() is refused until a click: model it as staying asleep
  audio.made.ctxs.length = 0;
  const settings = createSettings({ storage: mem() }); settings.set({ sound: { on: true } });
  const asleep = createSound({ settings, win, AudioCtx: class extends audio.Ctx { resume() { return Promise.reject(new Error('needs a gesture')); } } });
  asleep.play('critical'); await later();
  assert.equal(audio.made.oscs.length, 0, 'nothing was scheduled');
  assert.equal(asleep.status(), 'locked', 'so the settings can say why');
  assert.deepEqual(win.heard.map((d) => d.kind), ['critical'], 'the choice was still announced');
  void sound;
});

test('the preview buttons play at once, whatever the switches say', () => {
  const { sound, audio, settings } = setup();
  settings.set({ sound: { events: { alert: false } } });
  assert.equal(sound.preview('alert'), true);
  assert.equal(audio.made.oscs.length, RECIPES.alert.notes.length);
});

test('no audio in the browser at all is not an error', async () => {
  const settings = createSettings({ storage: mem() }); settings.set({ sound: { on: true } });
  const sound = createSound({ settings, win: fakeWin(), AudioCtx: null });
  assert.doesNotThrow(() => sound.play('alert'));
  await later();
  assert.equal(sound.status(), 'locked');
});
