// The time zone the page shows times in, and everything that depends on it. Pure apart from
// the one zone it is told to use, so the rules are tested without a browser.
//
// Every time on the page goes through here: the cards, the charts, the alerts, the time range.
// Relative times ("3m ago") do not depend on a zone and do not come here.

// The zones on offer, west to east. 'local' is whatever the browser says it is, and is
// what the page used before there was a choice.
export const ZONES = Object.freeze([
  ['local', null],
  ['UTC', 'UTC'],
  ['America/Los_Angeles', 'Los Angeles'],
  ['America/Denver', 'Denver'],
  ['America/Chicago', 'Chicago'],
  ['America/New_York', 'New York'],
  ['America/Sao_Paulo', 'São Paulo'],
  ['Europe/London', 'London'],
  ['Europe/Berlin', 'Berlin'],
  ['Africa/Johannesburg', 'Johannesburg'],
  ['Asia/Dubai', 'Dubai'],
  ['Asia/Kolkata', 'Kolkata'],
  ['Asia/Singapore', 'Singapore'],
  ['Asia/Tokyo', 'Tokyo'],
  ['Australia/Sydney', 'Sydney'],
  ['Pacific/Auckland', 'Auckland'],
]);
export const ZONE_IDS = ZONES.map(([id]) => id);

const p2 = (n) => String(n).padStart(2, '0');
let zone = 'local';
const formats = new Map();

function format(z) {
  let f = formats.get(z);
  if (!f) {
    const timeZone = z === 'local' ? undefined : z;
    try {
      f = new Intl.DateTimeFormat('en-GB', { timeZone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' });
    } catch (_) { f = z === 'local' ? null : format('local'); } // a browser that does not know the zone shows its own
    formats.set(z, f);
  }
  return f;
}

export function setZone(z) {
  zone = ZONE_IDS.includes(z) ? z : 'local';
  return zone;
}
export const getZone = () => zone;

// The wall clock in a zone, for a moment.
export function parts(ms, z = zone) {
  const f = format(z);
  if (!f) { const d = new Date(ms); return { y: d.getFullYear(), mo: d.getMonth() + 1, d: d.getDate(), h: d.getHours(), mi: d.getMinutes(), s: d.getSeconds() }; }
  const o = {};
  for (const x of f.formatToParts(new Date(ms))) if (x.type !== 'literal') o[x.type] = Number(x.value);
  return { y: o.year, mo: o.month, d: o.day, h: o.hour % 24, mi: o.minute, s: o.second };
}

export function clock(ms, withSeconds = false, z = zone) {
  const t = parts(ms, z);
  return `${p2(t.h)}:${p2(t.mi)}${withSeconds ? ':' + p2(t.s) : ''}`;
}

// How far the zone is from UTC at a moment, in minutes (east is positive).
export function offsetMinutes(ms, z = zone) {
  const t = parts(ms, z);
  return Math.round((Date.UTC(t.y, t.mo - 1, t.d, t.h, t.mi, t.s) - Math.floor(ms / 1000) * 1000) / 60000);
}

// "UTC", or "UTC+05:30" / "UTC−03:00".
export function offsetLabel(ms = Date.now(), z = zone) {
  if (z === 'UTC') return 'UTC';
  const m = offsetMinutes(ms, z), a = Math.abs(m);
  return `UTC${m < 0 ? '−' : '+'}${p2(Math.floor(a / 60))}:${p2(a % 60)}`;
}

// The name of a zone as a person would say it.
export function zoneName(z = zone) {
  if (z === 'local') {
    let tz = '';
    try { tz = Intl.DateTimeFormat().resolvedOptions().timeZone || ''; } catch (_) { /* unknown */ }
    return tz ? `Browser time (${tz})` : 'Browser time';
  }
  const hit = ZONES.find(([id]) => id === z);
  return hit && hit[1] ? hit[1] : z;
}

// The other way round: the wall clock in a zone to the moment it is. A time that does not
// exist (the clocks went forward) or that happens twice (they went back) gets one of its
// two readings rather than none.
export function fromWall(y, mo, d, h, mi, z = zone) {
  if (z === 'local') return new Date(y, mo - 1, d, h, mi).getTime();
  const guess = Date.UTC(y, mo - 1, d, h, mi);
  const first = guess - offsetMinutes(guess, z) * 60000;
  return guess - offsetMinutes(first, z) * 60000;
}
