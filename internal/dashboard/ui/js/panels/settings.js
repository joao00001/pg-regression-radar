// The Settings page: a board like the others, made of panels, that is not part of the
// dashboard file because what it holds belongs to the person looking, not to the layout
// everyone gets. The rules are in settings.js; these panels draw what it holds and write
// back what is changed.
import { h, s } from '../dom.js';
import { heading, mount } from './common.js';
import { DEFAULTS } from '../settings.js';
import { ZONES, zoneName, offsetLabel, clock } from '../time.js';

// The gear in the header, which opens this page.
export const gearIcon = () => s('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.8, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' },
  s('circle', { cx: 12, cy: 12, r: 3 }),
  s('path', { d: 'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z' }));

let settings = null, sound = null;
export function bindSettings(s, snd) { settings = s; sound = snd; }

// The catalog entries the board needs (the real catalog comes from the manager and has
// no idea these exist). `plain` panels have no expanded view and cannot be edited.
export const SETTINGS_CATALOG = [
  { type: 'settings-appearance', title: 'Appearance', minW: 3, minH: 3, w: 6, h: 5, plain: true, options: [],
    doc: 'How this page looks.', help: 'How this page looks. Kept in this browser only.' },
  { type: 'settings-time', title: 'Time', minW: 3, minH: 3, w: 6, h: 5, plain: true, options: [],
    doc: 'The time zone of every time on the page.', help: 'Every clock time on the page is shown in this zone: on the cards, in the charts, in alerts and in the time range. Times such as "3m ago" do not depend on it. Kept in this browser only.' },
  { type: 'settings-alerts', title: 'Alerts', minW: 4, minH: 4, w: 7, h: 15, plain: true, options: [],
    doc: 'Which alerts stay on screen, and how they are kept tidy.', help: 'Alerts are the few situations that stay on screen until somebody has dealt with them: a regression, one that survived an abort, an open circuit breaker. These choices decide how many you see and what happens to them. Kept in this browser only.' },
  { type: 'settings-sound', title: 'Sound', minW: 3, minH: 4, w: 5, h: 15, plain: true, options: [],
    doc: 'A sound with each alert.', help: 'A short sound that goes with an alert appearing and with an alert clearing. Browsers keep a page silent until you have clicked on it once. Kept in this browser only.' },
];

export const SETTINGS_BOARD = Object.freeze({
  id: 'settings', title: 'Settings', range: '6h',
  panels: [
    { id: 'appearance', type: 'settings-appearance', at: [0, 0, 6, 5] },
    { id: 'time', type: 'settings-time', at: [6, 0, 6, 5] },
    { id: 'sound', type: 'settings-sound', at: [7, 5, 5, 15] },
    { id: 'alerts', type: 'settings-alerts', at: [0, 5, 7, 15] },
  ],
});

// ---- the pieces ---------------------------------------------------------------------
// The same parts as the panel settings in the expanded view: a field with its label, a
// sentence saying what it does and a way back to the default; tiles to pick one of a few; a
// switch; selects. A field that is not at its default carries the accent bar and "Reset".

let uid = 0;
const fid = (k) => `st-${k}-${++uid}`;

function field({ label, doc, control, id, kind = 'field', changed = false, reset, aside }) {
  const docId = fid('doc');
  const head = h('div', { class: 'field-head' },
    kind === 'group' ? h('span', { class: 'field-label', id, text: label }) : h('label', { class: 'field-label', for: id, text: label }),
    aside || null,
    reset ? h('button', { class: 'field-reset', type: 'button', hidden: !changed, 'aria-label': `Reset ${label} to the default`, title: 'Back to the default', onclick: reset, text: 'Reset' }) : null);
  const row = h('div', { class: `field ${kind}${changed ? ' modified' : ''}` }, head, control, doc ? h('p', { class: 'field-doc', id: docId, text: doc }) : null);
  const ctl = control && control.matches && (control.matches('input, select') ? control : control.querySelector('input, select'));
  if (ctl && doc) ctl.setAttribute('aria-describedby', docId);
  return row;
}

// Pick one of a few, as tiles. The radios are the real controls; the tile is what is drawn.
function tiles({ name, label, doc, options, current, pick, cols, fallback, reset }) {
  const group = fid(name);
  const shown = options.find(([v]) => v === current);
  const set = h('fieldset', { class: 'viz-set plain', 'data-cols': String(cols || options.length) },
    h('legend', { class: 'sr-only', text: label }),
    options.map(([value, text]) => {
      const input = h('input', { type: 'radio', name: group, value: String(value), 'data-id': `${name}-${value}`, onchange: () => pick(value) });
      input.checked = value === current;
      return h('label', { class: 'viz-opt' }, input, h('span', { class: 'viz-tile' }, h('span', { class: 'viz-name', text })));
    }));
  return field({ label, doc: (shown && shown[2]) || doc, control: set, id: fid(`${name}-l`), kind: 'group', changed: current !== fallback, reset });
}

// A real checkbox under a drawn track, so it is a switch to the keyboard and to a screen reader.
function switchField({ id, label, doc, on, pick, extra, changed, reset }) {
  const dom = fid(id);
  const input = h('input', { type: 'checkbox', role: 'switch', id: dom, class: 'switch', 'data-id': id, onchange: (e) => pick(e.target.checked) });
  input.checked = !!on;
  const ctl = h('div', { class: 'ctl-group' }, extra || null, h('label', { class: 'switch-wrap', for: dom }, input, h('span', { class: 'switch-track', 'aria-hidden': 'true' })));
  const f = field({ label, doc, control: ctl, id: dom, kind: 'switchrow', changed, reset });
  return f;
}

// Pick one of many, from a list.
function selectField({ name, label, doc, options, current, pick, fallback, reset }) {
  const id = fid(name);
  const sel = h('select', { id, 'data-id': name, onchange: (e) => pick(e.target.value) },
    options.map(([value, text]) => { const o = h('option', { value, text }); return o; }));
  sel.value = current;
  return field({ label, doc, control: sel, id, changed: current !== fallback, reset });
}

const fields = (...kids) => h('div', { class: 'fields' }, kids);

// A panel that draws itself again whenever a setting changes (and stops when it is gone).
function live(host, draw, skip = () => false) {
  let off = () => {};
  off = settings.subscribe(() => { if (!host.isConnected) { off(); return; } if (!skip()) draw(); });
  draw();
}

// Redrawing replaces the controls, so the keyboard focus is put back on the one that had it.
function keepFocus(host, fn) {
  const had = document.activeElement && host.contains(document.activeElement) ? document.activeElement.dataset.id : null;
  fn();
  if (had) { const el = host.querySelector(`[data-id="${had}"]`); if (el) el.focus(); }
}

const D = DEFAULTS;
const back = (patch) => () => settings.set(patch);

// ---- the panels ---------------------------------------------------------------------

export function appearancePanel(host, ctx) {
  host.classList.add('p-settings');
  live(host, () => keepFocus(host, () => {
    const v = settings.get();
    mount(host, heading(ctx, 'Appearance'), fields(
      tiles({ name: 'theme', label: 'Theme', doc: 'Follow the system, or always light or always dark.',
        options: [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']], current: v.theme, fallback: D.theme,
        pick: (x) => settings.set({ theme: x }), reset: back({ theme: D.theme }) })));
  }));
  return { render() {} };
}

export function timePanel(host, ctx) {
  host.classList.add('p-settings');
  live(host, () => keepFocus(host, () => {
    const v = settings.get(), now = Date.now();
    const options = ZONES.map(([id]) => [id, id === 'local' ? zoneName(id) : `${zoneName(id)} (${offsetLabel(now, id)})`]);
    // Browser time is whatever the browser says; its offset is shown so it can be told from the others.
    options[0][1] = `${zoneName('local')}, ${offsetLabel(now, 'local')}`;
    mount(host, heading(ctx, 'Time'), fields(
      selectField({ name: 'timezone', label: 'Time zone', options, current: v.timezone, fallback: D.timezone,
        doc: `Cards, charts, alerts and the time range all use it. It is ${clock(now, false, v.timezone)} there now.`,
        pick: (x) => settings.set({ timezone: x }), reset: back({ timezone: D.timezone }) })));
  }));
  return { render() {} };
}

export function alertsPanel(host, ctx) {
  host.classList.add('p-settings');
  live(host, () => keepFocus(host, () => {
    const v = settings.get();
    mount(host, heading(ctx, 'Alerts'), fields(
      tiles({ name: 'resolved', label: 'When an alert clears by itself', doc: 'The deploy is healthy again, or the breaker is closed.',
        options: [
          ['say', 'Say so, then remove it', 'The card turns to healthy, says so for a moment, and goes.'],
          ['quiet', 'Remove it quietly', 'The card just goes.'],
          ['keep', 'Keep it until I dismiss it', 'The card turns to healthy and stays, so you can see what cleared while you were away.'],
        ], current: v.resolved, fallback: D.resolved, pick: (x) => settings.set({ resolved: x }), reset: back({ resolved: D.resolved }) }),
      tiles({ name: 'level', label: 'Alerts to show', doc: 'Critical means the radar could not act, the regression survived an abort, or its breaker is open.',
        options: [['all', 'All'], ['critical', 'Critical only']], current: v.level, fallback: D.level,
        pick: (x) => settings.set({ level: x }), reset: back({ level: D.level }) }),
      tiles({ name: 'max', label: 'Most at once', doc: 'The rest wait behind a "more" button. A phone always shows one.',
        options: [1, 2, 3, 5].map((n) => [n, String(n)]), current: v.max, fallback: D.max,
        pick: (x) => settings.set({ max: x }), reset: back({ max: D.max }) }),
      tiles({ name: 'dismissal', label: 'An alert you dismissed', doc: 'Whichever you choose, one that gets worse is shown again.',
        options: [
          ['persist', 'Stays hidden', 'Until it gets worse, or the deploy recovers and regresses again. Remembered for 30 days in this browser.'],
          ['session', 'Comes back on reload', 'Nothing is remembered between visits.'],
        ], current: v.dismissal, fallback: D.dismissal, pick: (x) => settings.set({ dismissal: x }), reset: back({ dismissal: D.dismissal }) })));
  }));
  return { render() {} };
}

const SOUNDS = [
  ['alert', 'New alert', 'Two notes going up.'],
  ['critical', 'Critical alert', 'Two falling notes, twice. Also when an alert gets worse.'],
  ['healthy', 'Healthy again', 'Three rising notes, as the card turns healthy.'],
];

export function soundPanel(host, ctx) {
  host.classList.add('p-settings');
  let dragging = false; // the panel is not redrawn while the slider is dragged: it would be replaced under the finger
  live(host, () => keepFocus(host, () => {
    const snd = settings.get().sound, status = sound.status();
    const kids = [
      switchField({ id: 'sound-on', label: 'Play a sound with alerts', doc: 'Off until you turn it on.', on: snd.on,
        pick: (on) => { settings.set({ sound: { on } }); if (on) sound.preview('healthy'); },
        changed: snd.on !== D.sound.on, reset: back({ sound: { on: D.sound.on } }) }),
    ];
    if (snd.on) {
      const pct = Math.round(snd.volume * 100);
      const out = h('span', { class: 'field-value', 'data-id': 'volume-value', text: `${pct}%` });
      const range = h('input', {
        type: 'range', min: 0, max: 100, step: 5, id: fid('volume'), class: 'range', 'data-id': 'volume',
        oninput: (e) => { dragging = true; out.textContent = `${e.target.value}%`; settings.set({ sound: { volume: Number(e.target.value) / 100 } }); dragging = false; },
        onchange: () => sound.preview('healthy'),
      });
      range.value = String(pct);
      kids.push(field({ label: 'Volume', doc: null, control: range, id: range.id, aside: out,
        changed: snd.volume !== D.sound.volume, reset: back({ sound: { volume: D.sound.volume } }) }));
      for (const [kind, name, why] of SOUNDS) {
        kids.push(switchField({
          id: `sound-${kind}`, label: name, doc: why, on: snd.events[kind],
          pick: (on) => settings.set({ sound: { events: { [kind]: on } } }),
          extra: h('button', { type: 'button', class: 'btn', 'data-id': `play-${kind}`, text: 'Play', 'aria-label': `Play the sound for: ${name}`, onclick: () => sound.preview(kind) }),
          changed: snd.events[kind] !== D.sound.events[kind], reset: back({ sound: { events: { [kind]: D.sound.events[kind] } } }),
        }));
      }
      if (status === 'locked') kids.push(h('p', { class: 'pane-about locked', text: 'Your browser plays sound only after you have clicked on the page once.' }));
    }
    mount(host, heading(ctx, 'Sound'), fields(...kids));
  }), () => dragging);
  return { render() {} };
}
