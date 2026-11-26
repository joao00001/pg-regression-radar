// The page's frame: board tabs, the time range picker, the editing toolbar and
// the export dialog. It draws and reports what the person did; main.js decides
// what that means.
import { h } from './dom.js';
import * as R from './range.js';

export function createChrome({ catalog, onRange, onEdit, onAdd, onReset, onExport, onDone, now }) {
  const $ = (id) => document.getElementById(id);
  const tabs = $('boards'), tools = $('tools'), bar = $('editbar'), dialog = $('export'), notice = $('notice');
  let range = R.relative('6h'), history = null, editing = false, edited = false, popOpen = false;

  // ---- range picker -----------------------------------------------------------------
  const rangeBtn = h('button', { class: 'btn range-btn', type: 'button', 'aria-haspopup': 'dialog', 'aria-expanded': 'false', id: 'range-btn' });
  const pop = h('div', { class: 'popover', role: 'dialog', 'aria-label': 'Time range', hidden: true, id: 'range-pop' });
  const histNote = h('span', { class: 'hist-note', hidden: true });
  const rangeWrap = h('div', { class: 'range' }, rangeBtn, pop);
  const editBtn = h('button', { class: 'btn', type: 'button', id: 'edit-btn', 'aria-pressed': 'false', text: 'Edit layout', onclick: () => onEdit(!editing) });
  tools.append(histNote, rangeWrap, editBtn);

  function closePop() { popOpen = false; pop.hidden = true; rangeBtn.setAttribute('aria-expanded', 'false'); }
  function openPop() {
    popOpen = true;
    drawPop();
    pop.hidden = false;
    rangeBtn.setAttribute('aria-expanded', 'true');
  }
  rangeBtn.addEventListener('click', () => (popOpen ? closePop() : openPop()));
  document.addEventListener('click', (e) => { if (popOpen && !rangeWrap.contains(e.target)) closePop(); });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && popOpen) { closePop(); rangeBtn.focus(); } });

  function drawPop() {
    const t = now();
    const from = h('input', { type: 'datetime-local', id: 'range-from', value: R.toLocalInput(range.kind === 'abs' ? range.from : t - 3600e3) });
    const to = h('input', { type: 'datetime-local', id: 'range-to', value: R.toLocalInput(range.kind === 'abs' ? range.to : t) });
    const err = h('p', { class: 'form-error', role: 'alert', hidden: true });
    const apply = () => {
      const f = R.parseLocalInput(from.value), u = R.parseLocalInput(to.value);
      const msg = R.validateCustom(f, u, now());
      if (msg) { err.textContent = msg; err.hidden = false; return; }
      closePop();
      onRange(R.absolute(f, u));
    };
    pop.replaceChildren(
      h('ul', { class: 'presets' }, R.PRESETS.map((p) => h('li', null, h('button', {
        type: 'button', class: 'preset', 'aria-pressed': String(range.kind === 'rel' && range.id === p.id), text: p.label,
        onclick: () => { closePop(); onRange(R.relative(p.id)); },
      })))),
      h('form', { class: 'custom', onsubmit: (e) => { e.preventDefault(); apply(); } },
        h('p', { class: 'label', text: 'Custom range' }),
        h('label', null, h('span', { text: 'From' }), from),
        h('label', null, h('span', { text: 'To' }), to),
        err,
        h('button', { class: 'btn', type: 'submit', text: 'Apply' })));
  }

  // ---- editing toolbar -----------------------------------------------------------------
  const addSel = h('select', { id: 'add-panel', 'aria-label': 'Add a panel' },
    h('option', { value: '', text: 'Add a panel…' }), catalog.map((c) => h('option', { value: c.type, title: c.doc, text: c.title })));
  addSel.addEventListener('change', () => { if (addSel.value) { onAdd(addSel.value); addSel.value = ''; } });
  let saveNote = '';
  const resetBtn = h('button', { class: 'btn', type: 'button', id: 'reset-layout', text: 'Undo my edits', onclick: () => onReset() });
  const exportBtn = h('button', { class: 'btn', type: 'button', id: 'export-yaml', text: 'Export YAML', onclick: () => onExport() });
  const doneBtn = h('button', { class: 'btn primary', type: 'button', id: 'edit-done', text: 'Done', onclick: () => onDone() });
  const hint = h('span', { class: 'hint', text: 'Drag a panel by its bar, resize from the corner. With the keyboard: arrows move, Shift and arrows resize.' });
  bar.append(hint, h('span', { class: 'spacer' }), addSel, resetBtn, exportBtn, doneBtn);

  // ---- export dialog ---------------------------------------------------------------------
  function showExport(yaml, source) {
    const ta = h('textarea', { readonly: true, rows: 18, spellcheck: 'false', 'aria-label': 'Dashboard file', text: yaml });
    const copied = h('span', { class: 'muted', role: 'status' });
    dialog.replaceChildren(h('form', { method: 'dialog' },
      h('h2', { id: 'export-title', text: 'Dashboard file' }),
      h('p', { class: 'muted', text: `Save this as a .yaml file and start the dashboard with --config FILE to make it the layout everyone gets${source && source !== 'default' ? ` (it replaces ${source})` : ''}. Check it first with: pg-regression-radar dashboard config validate FILE` }),
      ta,
      h('div', { class: 'dialog-actions' }, copied,
        h('button', { class: 'btn', type: 'button', text: 'Copy', onclick: async () => {
          try { await navigator.clipboard.writeText(yaml); copied.textContent = 'Copied.'; } catch (_) { ta.select(); copied.textContent = 'Press Ctrl+C to copy.'; }
        } }),
        h('button', { class: 'btn', type: 'button', text: 'Download', onclick: () => {
          const a = h('a', { href: URL.createObjectURL(new Blob([yaml], { type: 'text/yaml' })), download: 'dashboard.yaml' });
          document.body.append(a); a.click(); a.remove();
        } }),
        h('button', { class: 'btn primary', value: 'close', text: 'Close' }))));
    if (typeof dialog.showModal === 'function') dialog.showModal(); else dialog.setAttribute('open', '');
  }

  return {
    setBoards(boards, active) {
      tabs.replaceChildren(...boards.map((b) => h('a', { class: 'tab', href: `#/${b.id}`, 'aria-current': b.id === active ? 'page' : null, text: b.title })));
    },
    // note says when the range reaches back past what the history holds; it stays a
    // small label by the picker (the sentence is its tooltip), not a banner.
    setRange(r, note) {
      range = r; history = note;
      histNote.hidden = !note;
      histNote.textContent = note ? 'History is shorter than this range' : '';
      histNote.title = note || '';
      rangeBtn.textContent = R.label(r);
      rangeBtn.title = 'Change the time range';
      if (popOpen) drawPop();
    },
    setEditing(on) {
      editing = on;
      bar.hidden = !on;
      editBtn.setAttribute('aria-pressed', String(on));
      editBtn.textContent = on ? 'Editing layout' : 'Edit layout';
      document.body.classList.toggle('editing', on);
    },
    setSaveNote(text) { saveNote = text || ''; resetBtn.title = saveNote; },
    setEdited(on) { edited = on; resetBtn.disabled = !on; },
    notice(msg, kind) {
      notice.hidden = !msg;
      notice.className = `notice${kind ? ` ${kind}` : ''}`;
      if (msg) notice.replaceChildren(h('span', { text: msg }), ' ', h('button', { class: 'link', type: 'button', text: 'Dismiss', onclick: () => { notice.hidden = true; } }));
    },
    showExport,
    get history() { return history; },
    get edited() { return edited; },
  };
}
