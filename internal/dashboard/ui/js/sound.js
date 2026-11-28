// The sound of an alert, made in the browser with the Web Audio API: no file to load, so
// nothing for the page's content policy to object to. Three sounds, each a few soft notes:
//   alert     two notes going up, short                     a new alert
//   critical  two falling notes, twice                      a critical one, or one that got worse
//   healthy   three rising notes, a major chord, let ring    the trouble is over
// The notes are plain data (RECIPES) so they are tested, and so the same sounds can be
// rendered outside the browser.

export const RECIPES = Object.freeze({
  alert: { wave: 'sine', gain: 0.5, notes: [{ f: 659.25, t: 0, d: 0.22 }, { f: 880, t: 0.1, d: 0.3 }] },
  critical: { wave: 'triangle', gain: 0.55, notes: [{ f: 587.33, t: 0, d: 0.2 }, { f: 440, t: 0.14, d: 0.34 }, { f: 587.33, t: 0.5, d: 0.2 }, { f: 440, t: 0.64, d: 0.34 }] },
  healthy: { wave: 'sine', gain: 0.42, notes: [{ f: 523.25, t: 0, d: 0.5 }, { f: 659.25, t: 0.09, d: 0.5 }, { f: 783.99, t: 0.18, d: 0.7 }] },
});
// When several sounds are asked for at once, only the most important is heard.
export const PRIORITY = ['critical', 'alert', 'healthy'];
export const LOWPASS = 4200;
export const HEADROOM = 0.5;
export const duration = (kind) => Math.max(...RECIPES[kind].notes.map((n) => n.t + n.d));
export const strongest = (a, b) => (!a ? b : !b ? a : PRIORITY.indexOf(a) <= PRIORITY.indexOf(b) ? a : b);

// settings: from settings.js. win: the window, which also hears a 'pgrr:sound' event each
// time a sound is chosen (a hook for tests and for anything that wants to follow along).
export function createSound({ settings, win = typeof window !== 'undefined' ? window : null, AudioCtx = win && (win.AudioContext || win.webkitAudioContext) } = {}) {
  let ctx = null, pending = null, timer = null, locked = false;

  function ensure() {
    if (ctx || !AudioCtx) return ctx;
    try { ctx = new AudioCtx(); } catch (_) { ctx = null; }
    return ctx;
  }

  // Browsers keep a page silent until the person has clicked or pressed a key on it.
  // The first such gesture wakes the sound up, so later alerts can be heard.
  function unlock() {
    const c = ensure();
    if (c && c.state === 'suspended') c.resume().catch(() => {});
  }
  if (win && win.addEventListener) {
    const once = () => { unlock(); ['pointerdown', 'keydown', 'touchstart'].forEach((e) => win.removeEventListener(e, once, true)); };
    ['pointerdown', 'keydown', 'touchstart'].forEach((e) => win.addEventListener(e, once, { capture: true, passive: true }));
  }

  function schedule(c, kind, volume) {
    const r = RECIPES[kind], t0 = c.currentTime + 0.02;
    const master = c.createGain();
    master.gain.value = volume * HEADROOM;
    const lp = c.createBiquadFilter();
    lp.type = 'lowpass'; lp.frequency.value = LOWPASS;
    master.connect(lp); lp.connect(c.destination);
    for (const n of r.notes) {
      const osc = c.createOscillator(), g = c.createGain();
      osc.type = r.wave; osc.frequency.value = n.f;
      g.gain.setValueAtTime(0.0001, t0 + n.t);
      g.gain.exponentialRampToValueAtTime(r.gain, t0 + n.t + 0.012);
      g.gain.exponentialRampToValueAtTime(0.0001, t0 + n.t + n.d);
      osc.connect(g); g.connect(master);
      osc.start(t0 + n.t); osc.stop(t0 + n.t + n.d + 0.05);
    }
  }

  function fire(kind, volume) {
    if (win && win.dispatchEvent && typeof CustomEvent === 'function') win.dispatchEvent(new CustomEvent('pgrr:sound', { detail: { kind, volume } }));
    const c = ensure();
    if (!c) return false;
    if (c.state === 'suspended') c.resume().catch(() => {});
    locked = c.state !== 'running';
    if (locked) return false; // nothing is queued: a burst of old alerts must not play when the page wakes up
    schedule(c, kind, volume);
    return true;
  }

  return {
    // Ask for a sound. Honours the person's choices; sounds asked for within 60 ms of each
    // other become one, the most important.
    play(kind) {
      const s = settings.get().sound;
      if (!RECIPES[kind] || !s.on || !s.events[kind]) return;
      pending = strongest(pending, kind);
      if (timer) return;
      timer = setTimeout(() => { const k = pending; pending = null; timer = null; fire(k, settings.get().sound.volume); }, 60);
    },
    // For the buttons in the settings: plays whatever the switches say, now.
    preview(kind) { unlock(); return fire(kind, settings.get().sound.volume); },
    // 'off', 'locked' (the browser is waiting for a click), or 'ready'.
    status() {
      if (!settings.get().sound.on) return 'off';
      const c = ensure();
      return c && c.state === 'running' ? 'ready' : 'locked';
    },
    get locked() { return locked; },
  };
}
