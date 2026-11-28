// A board on screen: its panels placed on the grid, and the editing of that grid.
// The positions come from layout.js; this file only measures the pointer, applies
// the result as CSS grid lines, and hands panels their data.
import { h } from './dom.js';
import { instantiate } from './panels/instance.js';
import { infoButton, closeInfoTip } from './infotip.js';
import { s as svgEl } from './dom.js';
import * as L from './layout.js';

const px = (n) => `${n}px`;


// What a click must not open the focus view from: anything that does something of
// its own, the panel's own buttons, and a text selection being made.
const OWN_JOB = 'a, button, input, select, textarea, summary, label, [role="button"], .p-info, .p-expand, .p-grip, .p-resize';

function wantsFocus(e, rec) {
  if (e.button !== 0 || e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return false;
  const t = e.target;
  if (!(t instanceof Element) || !t.isConnected || t.closest(OWN_JOB)) return false;
  const root = rec.el.parentElement;
  if (root && root.classList.contains('editing')) return false;
  const sel = window.getSelection();
  return !(sel && !sel.isCollapsed && rec.el.contains(sel.anchorNode));
}

// The button that opens a panel in the focus view; the click anywhere else on the panel does the same.
function expandButton(title, open) {
  return h('button', { class: 'p-expand', type: 'button', 'aria-label': `View ${title} large`, title: 'View large (V)', onclick: open },
    svgEl('svg', { viewBox: '0 0 16 16', width: '14', height: '14', 'aria-hidden': 'true', focusable: 'false', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.4' },
      svgEl('path', { d: 'M10 2.5h3.5V6M6 13.5H2.5V10M13.5 2.5L9.5 6.5M2.5 13.5l4-4' })));
}

// Tells the stylesheet which edges of a scrolling panel have more behind them
// (data-more="start", "end" or "start end"), so the content fades out there
// instead of ending in a cut line that reads like an open card.
function watchEdges(body) {
  let queued = false;
  const measure = () => {
    queued = false;
    const slack = 2; // sub-pixel rounding is not "more"
    const more = [];
    if (body.scrollTop > slack) more.push('start');
    if (body.scrollHeight - body.clientHeight - body.scrollTop > slack) more.push('end');
    const v = more.join(' ');
    if ((body.dataset.more || '') === v) return;
    if (v) body.dataset.more = v; else delete body.dataset.more;
  };
  const soon = () => { if (!queued) { queued = true; requestAnimationFrame(measure); } };
  body.addEventListener('scroll', soon, { passive: true });
  if (typeof ResizeObserver === 'function') new ResizeObserver(soon).observe(body);
  if (typeof MutationObserver === 'function') new MutationObserver(soon).observe(body, { childList: true, subtree: true, characterData: true });
  soon();
}

export function createBoard(root, { catalog, grid, env, onEdit, onFocus }) {
  const types = new Map(catalog.map((c) => [c.type, c]));
  const minOf = (p) => { const t = types.get(p.type); return t ? { w: t.minW, h: t.minH } : { w: 1, h: 1 }; };
  let board = null, panels = [], editing = false, view = null, holeSig = '';
  const inst = new Map(); // panel id -> { el, body, panel, api, ctx }
  root.classList.add('grid');
  root.style.setProperty('--cols', String(grid.columns));
  root.style.setProperty('--row', px(grid.rowHeight));

  // ---- placing --------------------------------------------------------------

  // Puts every panel where `list` says, on screen only; `commit` is what makes a
  // list the board's own.
  // A DOM move would drop the pointer capture of a drag in progress, so the reading
  // order is only restored when asked to (on release), never while dragging.
  function place(list, reorder = true) {
    for (const p of list) {
      const i = inst.get(p.id);
      if (!i) continue;
      const [x, y, w, hh] = p.at;
      i.el.style.setProperty('--x', String(x + 1));
      i.el.style.setProperty('--y', String(y + 1));
      i.el.style.setProperty('--w', String(w));
      i.el.style.setProperty('--h', String(hh));
      i.el.classList.toggle('edge-r', x + w >= grid.columns);
    }
    root.style.setProperty('--rows', String(Math.max(L.bottom(list), 1)));
    if (reorder) {
      // The DOM order is the reading order, which is also the order on a narrow screen.
      // The empty cells (below) come after the panels.
      const ordered = [...list].sort(L.byPosition).map((p) => inst.get(p.id)).filter(Boolean);
      ordered.forEach((i, n) => { if (root.children[n] !== i.el) root.insertBefore(i.el, root.children[n] || null); });
    }
    drawHoles(list);
  }

  // What a column that ends above its neighbour leaves empty gets its edges drawn
  // like a panel's, so the sheet has no cell with an open side.
  function drawHoles(list) {
    const want = L.holes(list, grid.columns);
    const sig = JSON.stringify(want);
    if (sig === holeSig) return;
    holeSig = sig;
    for (const el of root.querySelectorAll(':scope > .hole')) el.remove();
    for (const [x, y, w, hh, sealed] of want) {
      const el = h('div', { class: 'hole', 'aria-hidden': 'true' });
      el.classList.toggle('open-below', !sealed);
      el.style.setProperty('--x', String(x + 1));
      el.style.setProperty('--y', String(y + 1));
      el.style.setProperty('--w', String(w));
      el.style.setProperty('--h', String(hh));
      el.classList.toggle('edge-r', x + w >= grid.columns);
      root.append(el);
    }
  }

  function commit(list) {
    panels = list;
    for (const p of list) { const i = inst.get(p.id); if (i) i.panel = p; }
    place(list);
  }

  function mountPanel(p) {
    const pt = types.get(p.type);
    const title = p.title || (pt && pt.title) || p.type;
    const el = h('section', { class: `panel type-${p.type}`, 'data-panel': p.id, 'aria-label': title });
    const body = h('div', { class: 'p-body' });
    const mover = h('button', { class: 'p-move', type: 'button', tabindex: '-1', 'aria-label': `Move ${title}. Arrow keys move it, Shift with arrow keys resizes it.`, title: 'Drag to move' },
      h('span', { class: 'p-dots', 'aria-hidden': 'true' }), h('span', { class: 'p-grip-title', text: title }));
    const grip = h('div', { class: 'p-grip', role: 'group', 'aria-label': `${title}: layout controls` }, mover,
      h('button', { class: 'p-x', type: 'button', 'aria-label': `Hide ${title}`, title: 'Hide this panel', onclick: () => edit((ps) => L.remove(ps, p.id)), text: '×' }));
    const handle = h('span', { class: 'p-resize', 'aria-hidden': 'true' });
    const rec = { el, body, panel: p, sig: L.panelSignature(p), api: null, ctx: null };
    // What the info button says: the file's own description, else the type's help.
    const about = () => {
      const q = rec.panel, t = types.get(q.type);
      const base = q.description || (t && (t.help || t.doc)) || '';
      return { title: q.title || (t && t.title) || q.type, text: q.query ? `${base} This panel is limited by its query: ${q.query}.` : base };
    };
    el.append(body, grip, handle);
    watchEdges(body);
    const tools = [];
    if (about().text) tools.push(infoButton(about));
    // A plain panel (the settings) has no expanded view: what it holds is already all there is.
    if (!(pt && pt.plain)) tools.push(expandButton(title, () => onFocus && onFocus(p.id)));
    el.append(...tools);
    Object.assign(rec, instantiate(p, body, { types, boardId: board.id, env }));
    if (!(pt && pt.plain)) el.addEventListener('click', (e) => { if (wantsFocus(e, rec)) onFocus && onFocus(rec.panel.id); });
    inst.set(p.id, rec);
    wireDrag(rec, mover, 'move');
    wireDrag(rec, handle, 'resize');
    wireKeys(rec, mover);
    return rec;
  }

  // ---- editing -----------------------------------------------------------------

  // A change to the layout of this board: computed, applied, reported.
  function edit(fn) {
    const next = fn(panels);
    syncPanels(next);
    onEdit(next);
  }

  // Adds or removes instances so they match the list, then places them.
  function syncPanels(next) {
    const ids = new Set(next.map((p) => p.id));
    for (const [id, i] of inst) if (!ids.has(id)) { i.el.remove(); inst.delete(id); }
    for (const p of next) {
      const cur = inst.get(p.id);
      if (!cur) root.append(mountPanel(p).el);
      // A panel whose title, description or settings changed is drawn again from scratch.
      else if (cur.sig !== L.panelSignature(p)) cur.el.replaceWith(mountPanel(p).el);
    }
    commit(next);
    if (view) for (const i of inst.values()) i.api.render(view);
  }

  const cell = () => { const r = root.getBoundingClientRect(); return { w: r.width / grid.columns, h: grid.rowHeight }; };

  function wireDrag(rec, handle, mode) {
    handle.addEventListener('pointerdown', (e) => {
      if (!editing || e.button !== 0) return;
      e.preventDefault();
      handle.setPointerCapture(e.pointerId);
      const start = { x: e.clientX, y: e.clientY }, at0 = [...rec.panel.at], c = cell();
      const base = panels;
      let working = base;
      rec.el.classList.add('dragging');
      root.classList.add('is-dragging');
      const move = (ev) => {
        const dx = Math.round((ev.clientX - start.x) / c.w), dy = Math.round((ev.clientY - start.y) / c.h);
        const at = mode === 'move' ? [at0[0] + dx, at0[1] + dy, at0[2], at0[3]] : [at0[0], at0[1], at0[2] + dx, at0[3] + dy];
        working = L.place(base, rec.panel.id, at, minOf);
        place(working, false); // a preview: the board's own list is not touched until the pointer is released
      };
      const end = (ev) => {
        handle.removeEventListener('pointermove', move);
        handle.removeEventListener('pointerup', end);
        handle.removeEventListener('pointercancel', end);
        rec.el.classList.remove('dragging');
        root.classList.remove('is-dragging');
        if (ev.type === 'pointercancel') { place(base); return; }
        commit(working);
        onEdit(working);
      };
      handle.addEventListener('pointermove', move);
      handle.addEventListener('pointerup', end);
      handle.addEventListener('pointercancel', end);
    });
  }

  function wireKeys(rec, handle) {
    handle.addEventListener('keydown', (e) => {
      if (!editing) return;
      const d = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }[e.key];
      if (!d) return;
      e.preventDefault();
      const [x, y, w, hh] = rec.panel.at;
      const at = e.shiftKey ? [x, y, w + d[0], hh + d[1]] : [x + d[0], y + d[1], w, hh];
      const next = L.place(panels, rec.panel.id, at, minOf);
      commit(next);
      onEdit(next);
      handle.focus();
    });
  }

  // ---- public ------------------------------------------------------------------------

  return {
    show(b) {
      board = b;
      for (const i of inst.values()) i.el.remove();
      inst.clear();
      root.replaceChildren();
      holeSig = '';
      const list = b.panels.map((p) => ({ ...p, at: [...p.at] }));
      panels = list;
      for (const p of list) root.append(mountPanel(p).el);
      commit(list);
      root.classList.toggle('editing', editing);
      if (view) for (const i of inst.values()) i.api.render(view);
    },
    update(v) {
      view = v;
      for (const i of inst.values()) i.api.render(v);
    },
    setEditing(on) {
      editing = on;
      if (on) closeInfoTip();
      root.classList.toggle('editing', on);
      for (const i of inst.values()) i.el.querySelector('.p-move').tabIndex = on ? 0 : -1;
    },
    // Replaces the board's panels (reset, undo, a layout that arrived from elsewhere).
    replace(list) { syncPanels(list.map((p) => ({ ...p, at: [...p.at] }))); },
    add(type) {
      const pt = types.get(type);
      if (!pt) return;
      edit((ps) => L.add(ps, pt));
      const added = panels[panels.length - 1];
      const rec = inst.get(added.id);
      if (rec) rec.el.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
    },
    panels: () => panels,
    panel: (id) => panels.find((p) => p.id === id) || null,
    // The panel the pointer is over or the keyboard is in, for the V shortcut.
    current() {
      const a = document.activeElement && document.activeElement.closest && document.activeElement.closest('.panel');
      const el = root.querySelector('.panel:hover') || a;
      return el && root.contains(el) ? el.dataset.panel : null;
    },
  };
}
