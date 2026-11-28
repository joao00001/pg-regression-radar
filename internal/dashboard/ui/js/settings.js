// What the person chose for this page: the theme, how alerts are kept tidy, and the
// sound. Pure apart from the storage it is given, so the rules are tested without a
// browser. Everything is the person's, in this browser; none of it is sent anywhere.

import { ZONE_IDS } from './time.js';

export const KEY = 'pgrr-dashboard-settings-v1';
// The theme has its own key because a script that runs before the first paint reads it.
export const THEME_KEY = 'pgrr-theme';

export const CHOICES = {
  theme: ['system', 'light', 'dark'],
  // What happens to an alert when the trouble clears by itself.
  resolved: ['say', 'quiet', 'keep'],
  // Which alerts are shown at all.
  level: ['all', 'critical'],
  // The most cards on screen at once (a phone always shows one).
  max: [1, 2, 3, 5],
  // Does a dismissed alert stay dismissed after the page is reloaded?
  dismissal: ['persist', 'session'],
  // The zone every time on the page is shown in.
  timezone: ZONE_IDS,
};

export const SOUND_EVENTS = ['alert', 'critical', 'healthy'];

export const DEFAULTS = Object.freeze({
  theme: 'system',
  resolved: 'say',
  level: 'all',
  max: 3,
  dismissal: 'persist',
  // What the page showed before there was a choice: the browser's own zone.
  timezone: 'local',
  // Off until the person turns it on: a dashboard left open in a room should not beep by surprise.
  sound: { on: false, volume: 0.6, events: { alert: true, critical: true, healthy: true } },
});

const pick = (list, v, fallback) => (list.includes(v) ? v : fallback);

// Anything that is not a valid value falls back to the default, so a damaged or old
// entry can never leave the page in a state nobody chose.
export function normalize(raw, base = DEFAULTS) {
  const r = raw && typeof raw === 'object' ? raw : {};
  const s = r.sound && typeof r.sound === 'object' ? r.sound : {};
  const e = s.events && typeof s.events === 'object' ? s.events : {};
  const vol = Number(s.volume);
  return {
    theme: pick(CHOICES.theme, r.theme, base.theme),
    resolved: pick(CHOICES.resolved, r.resolved, base.resolved),
    level: pick(CHOICES.level, r.level, base.level),
    max: pick(CHOICES.max, Number(r.max), base.max),
    dismissal: pick(CHOICES.dismissal, r.dismissal, base.dismissal),
    timezone: pick(CHOICES.timezone, r.timezone, base.timezone),
    sound: {
      on: typeof s.on === 'boolean' ? s.on : base.sound.on,
      volume: Number.isFinite(vol) && s.volume !== null && s.volume !== '' ? Math.min(1, Math.max(0, vol)) : base.sound.volume,
      events: Object.fromEntries(SOUND_EVENTS.map((k) => [k, typeof e[k] === 'boolean' ? e[k] : base.sound.events[k]])),
    },
  };
}

const merge = (cur, patch) => ({
  ...cur, ...patch,
  sound: { ...cur.sound, ...(patch.sound || {}), events: { ...cur.sound.events, ...((patch.sound && patch.sound.events) || {}) } },
});

export function createSettings({ storage } = {}) {
  const read = (k) => { try { return storage.getItem(k); } catch (_) { return null; } };
  const write = (k, v) => { try { if (v === null) storage.removeItem(k); else storage.setItem(k, v); } catch (_) { /* the page works; it just forgets */ } };

  let raw = {};
  try { raw = JSON.parse(read(KEY) || '{}'); } catch (_) { raw = {}; }
  const t = read(THEME_KEY);
  let cur = normalize({ ...raw, theme: t === 'light' || t === 'dark' ? t : 'system' });
  const listeners = new Set();

  return {
    get: () => cur,
    // Change some of it; the new value is checked, kept, and everyone listening is told.
    set(patch) {
      const next = normalize(merge(cur, patch));
      if (JSON.stringify(next) === JSON.stringify(cur)) return cur;
      const before = cur;
      cur = next;
      const { theme, ...rest } = next;
      write(KEY, JSON.stringify(rest));
      write(THEME_KEY, theme === 'system' ? null : theme);
      listeners.forEach((fn) => { try { fn(cur, before); } catch (_) { /* one listener cannot stop the others */ } });
      return cur;
    },
    subscribe(fn) { listeners.add(fn); return () => listeners.delete(fn); },
  };
}

// The theme the page should wear now.
export const resolveTheme = (theme, prefersLight) => (theme === 'system' ? (prefersLight ? 'light' : 'dark') : theme);
