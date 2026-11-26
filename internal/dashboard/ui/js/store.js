// What is kept for this one person between runs: edits to a board's layout and the
// time range of each board.
//
// With a `remote` (the command's data directory, behind /dashboard/profile) the
// profile lives there and the page writes through to it a moment after each change.
// Without one (--no-save, or a directory that cannot be written) it lives in the
// browser's storage. Either way every call here is synchronous and never throws:
// storage and the network can fail, and the page works without them.
import { fingerprint, sanitize } from './layout.js';

const VERSION = 1;

export function createStore(storage, remote = null, onProblem = () => {}) {
  const read = (k) => { try { return JSON.parse(storage.getItem(k)); } catch (_) { return null; } };
  const write = (k, v) => { try { storage.setItem(k, JSON.stringify(v)); return true; } catch (_) { return false; } };
  const drop = (k) => { try { storage.removeItem(k); } catch (_) { /* nothing to do */ } };
  const lkey = (source) => `pgrr-layout:v${VERSION}:${source}`;
  const rkey = (source, board) => `pgrr-range:v${VERSION}:${source}:${board}`;

  const disk = !!(remote && remote.saved);
  const given = disk && remote.profile && typeof remote.profile === 'object' && remote.profile.v === VERSION ? remote.profile : {};
  // The profile as it is (to be) saved: { v, base?, layout?, ranges? }.
  const mem = { v: VERSION, ...(given.base ? { base: given.base } : {}), ...(given.layout ? { layout: given.layout } : {}), ranges: { ...(given.ranges || {}) } };
  let timer = null, dirty = false, failing = false;

  function flush(leaving = false) {
    clearTimeout(timer);
    if (!disk || !dirty) return Promise.resolve();
    dirty = false;
    const body = { v: VERSION, ...(mem.base ? { base: mem.base } : {}), ...(mem.layout ? { layout: mem.layout } : {}), ranges: mem.ranges };
    const empty = !mem.layout && !Object.keys(mem.ranges).length;
    return Promise.resolve(empty ? remote.forget(leaving) : remote.put(body, leaving)).then(
      () => { if (failing) { failing = false; onProblem(null); } },
      (e) => { dirty = true; if (!failing) { failing = true; onProblem(e); } },
    );
  }
  function changed() {
    if (!disk) return;
    dirty = true;
    clearTimeout(timer);
    timer = setTimeout(flush, 350);
  }

  // A saved layout still counts only on the layout it was made on.
  function usable(saved, served, catalog) {
    const base = fingerprint(served);
    if (!saved) return { layout: served, edited: false, discarded: false };
    if (saved.v !== VERSION || saved.base !== base) return { layout: served, edited: false, discarded: true };
    const layout = sanitize(saved.layout, catalog);
    return layout ? { layout, edited: true, discarded: false } : { layout: served, edited: false, discarded: true };
  }

  return {
    // Whether edits reach the command's data directory, and where.
    get saved() { return disk; },
    get where() { return disk ? remote.where : ''; },
    // Writes anything not yet written; the page calls it as it is closed.
    flush,

    // The layout to show: what this person edited on top of what the server serves.
    // Edits are kept only while the served layout is the one they were made on, so
    // changing the dashboard file always wins over an old edit (and says so).
    loadLayout(source, served, catalog) {
      if (disk) {
        let saved = mem.layout ? { v: VERSION, base: mem.base, layout: mem.layout } : null;
        if (!saved) {
          // Edits made before the dashboard kept a profile on disk move there once.
          const old = read(lkey(source));
          if (old) { saved = old; mem.base = old.base; mem.layout = old.layout; changed(); drop(lkey(source)); }
        }
        const r = usable(saved, served, catalog);
        if (!r.edited && saved) { delete mem.base; delete mem.layout; changed(); }
        return r;
      }
      const r = usable(read(lkey(source)), served, catalog);
      if (!r.edited && (r.discarded)) drop(lkey(source));
      return r;
    },
    saveLayout(source, served, layout) {
      const same = fingerprint(layout) === fingerprint(served);
      if (disk) {
        if (same) { delete mem.base; delete mem.layout; } else { mem.base = fingerprint(served); mem.layout = layout; }
        changed();
        return true;
      }
      if (same) { drop(lkey(source)); return true; }
      return write(lkey(source), { v: VERSION, base: fingerprint(served), layout });
    },
    resetLayout(source) {
      if (disk) { delete mem.base; delete mem.layout; changed(); return; }
      drop(lkey(source));
    },

    loadRange(source, board) {
      const r = disk ? (mem.ranges[board] || read(rkey(source, board))) : read(rkey(source, board));
      if (r && r.kind === 'rel' && typeof r.id === 'string') return r;
      if (r && r.kind === 'abs' && Number.isFinite(r.from) && Number.isFinite(r.to)) return r;
      return null;
    },
    saveRange(source, board, range) {
      if (disk) { mem.ranges[board] = range; changed(); return; }
      write(rkey(source, board), range);
    },
  };
}
