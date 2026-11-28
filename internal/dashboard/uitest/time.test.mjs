import test from 'node:test';
import assert from 'node:assert/strict';
import { ZONES, ZONE_IDS, setZone, getZone, clock, parts, offsetMinutes, offsetLabel, zoneName, fromWall } from '../ui/js/time.js';
import { toLocalInput, parseLocalInput, label, absolute } from '../ui/js/range.js';
import { normalize, createSettings } from '../ui/js/settings.js';

const at = Date.UTC(2026, 10, 27, 12, 5, 9); // 27 Nov 2026, 12:05:09 UTC
test.afterEach(() => setZone('local'));

test('the same moment reads differently in each zone', () => {
  assert.equal(clock(at, false, 'UTC'), '12:05');
  assert.equal(clock(at, true, 'UTC'), '12:05:09');
  assert.equal(clock(at, false, 'Asia/Tokyo'), '21:05');
  assert.equal(clock(at, false, 'America/Sao_Paulo'), '09:05');
  assert.equal(clock(at, false, 'Asia/Kolkata'), '17:35', 'a half hour offset');
  assert.equal(clock(at, false, 'Pacific/Auckland'), '01:05', 'past midnight is the next day, and 00-23');
});

test('midnight is 00:xx, not 24:xx', () => {
  assert.equal(clock(Date.UTC(2026, 10, 27, 0, 30), false, 'UTC'), '00:30');
});

test('the zone in use is the one the page was told about, and an unknown one is refused', () => {
  setZone('Asia/Tokyo');
  assert.equal(getZone(), 'Asia/Tokyo');
  assert.equal(clock(at), '21:05');
  setZone('Mars/Olympus');
  assert.equal(getZone(), 'local', 'falls back to the browser');
});

test('summer time is followed, not a fixed offset', () => {
  assert.equal(clock(Date.UTC(2026, 6, 1, 12), false, 'America/New_York'), '08:00');
  assert.equal(clock(Date.UTC(2026, 11, 1, 12), false, 'America/New_York'), '07:00');
  assert.equal(offsetLabel(Date.UTC(2026, 6, 1), 'Europe/London'), 'UTC+01:00');
  assert.equal(offsetLabel(Date.UTC(2026, 11, 1), 'Europe/London'), 'UTC+00:00');
});

test('offsets are labelled the way they are said', () => {
  assert.equal(offsetLabel(at, 'UTC'), 'UTC');
  assert.equal(offsetLabel(at, 'Asia/Kolkata'), 'UTC+05:30');
  assert.equal(offsetLabel(at, 'America/Sao_Paulo'), 'UTC−03:00');
  assert.equal(offsetMinutes(at, 'Asia/Tokyo'), 540);
});

test('there are fifteen zones to choose from besides the browser, UTC among them', () => {
  assert.equal(ZONES.length, 16);
  assert.equal(new Set(ZONE_IDS).size, 16);
  assert.ok(ZONE_IDS.includes('UTC') && ZONE_IDS.includes('local'));
  for (const id of ZONE_IDS) assert.ok(zoneName(id).length > 0);
  for (const id of ZONE_IDS) assert.match(clock(at, true, id), /^\d\d:\d\d:\d\d$/, id);
});

test('a wall clock time goes back to the moment it came from, in every zone', () => {
  for (const id of ZONE_IDS) {
    for (const t of [at, Date.UTC(2026, 6, 1, 3, 45), Date.UTC(2027, 0, 15, 23, 59)]) {
      const w = parts(t, id);
      assert.equal(fromWall(w.y, w.mo, w.d, w.h, w.mi, id), Math.floor(t / 60000) * 60000, `${id} ${new Date(t).toISOString()}`);
    }
  }
});

test('a time the clocks skip, or repeat, still gives a moment', () => {
  assert.ok(Number.isFinite(fromWall(2026, 3, 8, 2, 30, 'America/New_York')), 'skipped');
  assert.ok(Number.isFinite(fromWall(2026, 11, 1, 1, 30, 'America/New_York')), 'repeated');
});

test('the custom range is typed and read in the zone the page shows', () => {
  setZone('Asia/Tokyo');
  assert.equal(toLocalInput(at), '2026-11-27T21:05');
  assert.equal(parseLocalInput('2026-11-27T21:05'), Math.floor(at / 60000) * 60000);
  setZone('UTC');
  assert.equal(toLocalInput(at), '2026-11-27T12:05');
  const r = absolute(Date.UTC(2026, 10, 26, 23, 0), at);
  assert.equal(label(r), '26 Nov 23:00 to 27 Nov 12:05');
  setZone('America/Sao_Paulo');
  assert.equal(label(r), '26 Nov 20:00 to 27 Nov 09:05');
});

test('the setting: the browser by default, only a listed zone allowed', () => {
  assert.equal(normalize({}).timezone, 'local');
  assert.equal(normalize({ timezone: 'UTC' }).timezone, 'UTC');
  assert.equal(normalize({ timezone: 'Asia/Tokyo' }).timezone, 'Asia/Tokyo');
  assert.equal(normalize({ timezone: 'Mars/Olympus' }).timezone, 'local');
  assert.equal(normalize({ timezone: 7 }).timezone, 'local');
  const m = new Map();
  const storage = { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k) };
  createSettings({ storage }).set({ timezone: 'Europe/Berlin' });
  assert.equal(createSettings({ storage }).get().timezone, 'Europe/Berlin', 'kept for the next visit');
});
