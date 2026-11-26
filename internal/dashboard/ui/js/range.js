// The time range of a board: a preset ("last 6 hours") or an absolute from/to.
// Pure, so the rules (what is allowed, what the API is asked) are tested.

export const PRESETS = [
  { id: '15m', label: 'Last 15 minutes' },
  { id: '1h', label: 'Last hour' },
  { id: '6h', label: 'Last 6 hours' },
  { id: '24h', label: 'Last 24 hours' },
  { id: '7d', label: 'Last 7 days' },
  { id: '30d', label: 'Last 30 days' },
];

export const MAX_DAYS = 90;
const UNIT_MS = { m: 60e3, h: 3600e3, d: 86400e3 };

// "90m", "6h", "7d" -> milliseconds; NaN when it is not that.
export function idMs(id) {
  const m = /^([1-9][0-9]{0,3})([mhd])$/.exec(id || '');
  return m ? Number(m[1]) * UNIT_MS[m[2]] : NaN;
}

export const relative = (id) => ({ kind: 'rel', id });
export const absolute = (from, to) => ({ kind: 'abs', from, to });
export const same = (a, b) => a.kind === b.kind && (a.kind === 'rel' ? a.id === b.id : a.from === b.from && a.to === b.to);

// A board's default, from the layout file: anything that is not a valid range falls back.
export function fromBoard(rangeId) {
  return Number.isFinite(idMs(rangeId)) ? relative(rangeId) : relative('6h');
}

// What the Observe API is asked: it understands now-<n><unit> and RFC 3339.
export function query(r) {
  if (r.kind === 'rel') return { from: `now-${r.id}` };
  return { from: new Date(r.from).toISOString(), to: new Date(r.to).toISOString() };
}

// The window in milliseconds, given the current time.
export function bounds(r, nowMs) {
  return r.kind === 'rel' ? { from: nowMs - idMs(r.id), to: nowMs } : { from: r.from, to: r.to };
}

export function validateCustom(fromMs, toMs, nowMs) {
  if (!Number.isFinite(fromMs) || !Number.isFinite(toMs)) return 'Give both a start and an end.';
  if (toMs <= fromMs) return 'The end has to be after the start.';
  if (fromMs > nowMs) return 'The start is in the future.';
  if (toMs - fromMs > MAX_DAYS * 86400e3) return `The radar looks back at most ${MAX_DAYS} days at a time.`;
  return null;
}

const p2 = (n) => String(n).padStart(2, '0');

// <input type="datetime-local"> speaks local time without a zone.
export const toLocalInput = (ms) => {
  const d = new Date(ms);
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}T${p2(d.getHours())}:${p2(d.getMinutes())}`;
};
export function parseLocalInput(v) {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(v || '');
  return m ? new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]).getTime() : NaN;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const stamp = (ms) => { const d = new Date(ms); return `${d.getDate()} ${MONTHS[d.getMonth()]} ${p2(d.getHours())}:${p2(d.getMinutes())}`; };

export function label(r) {
  if (r.kind === 'abs') return `${stamp(r.from)} to ${stamp(r.to)}`;
  return (PRESETS.find((p) => p.id === r.id) || { label: `Last ${r.id}` }).label;
}

// Says so when the range reaches back further than the history does, so a short
// list is not taken for a complete one.
export function historyNote(r, nowMs, history) {
  if (!history || !history.since) return null;
  const since = Date.parse(history.since);
  if (!Number.isFinite(since)) return null;
  const { from } = bounds(r, nowMs);
  if (from >= since) return null;
  return `The radar's history starts ${stamp(since)}${history.store === 'memory' ? ' (kept in memory; it starts over when the manager restarts)' : ''}. Earlier deploys are not in this range.`;
}
