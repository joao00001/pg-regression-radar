// The focus view: one panel shown large, with a settings pane beside it, as in
// Grafana's "view panel". The settings are not typed in: the pane is drawn from
// the catalog the server sends (each option has a label, a group and a help
// sentence), so a new option on the Go side shows up here with no UI change.
//
// A change applies at once and is kept like any other edit of the layout; the
// view keeps the panel as it was when it opened, so "Discard changes" can go back.
import { h, s } from './dom.js';
import { instantiate } from './panels/instance.js';
import { closeInfoTip } from './infotip.js';
import { clock } from './model.js';
import * as L from './layout.js';
import * as Q from './query.js';

const GROUP_ORDER = ['Visualization', 'Data', 'Display'];
// The panel's own name and description come after what it can draw, as in Grafana.

// A small picture for each way of drawing a panel, so the choice can be seen.
const GLYPHS = {
  cards: () => [s('rect', { x: 3, y: 3, width: 22, height: 6 }), s('rect', { x: 3, y: 11, width: 22, height: 6 })],
  list: () => [3, 8, 13, 18].map((y) => s('path', { d: `M3 ${y}h22` })),
  chart: () => [s('path', { d: 'M3 17l6-7 5 4 6-9 5 6' }), s('path', { class: 'dim', d: 'M3 19h22' })],
  table: () => [s('rect', { x: 3, y: 3, width: 22, height: 15 }), s('path', { d: 'M3 8h22M3 13h22M11 3v15' })],
  feed: () => [3, 9, 15].flatMap((y) => [s('circle', { cx: 5, cy: y + 1.5, r: 1.4 }), s('path', { d: `M10 ${y + 1.5}h15` })]),
  ratio: () => [s('path', { class: 'dim', d: 'M14 2v17' }), s('path', { d: 'M14 5h9M14 10h5M14 15h-8' })],
};
const glyph = (name) => s('svg', { class: 'viz-glyph', viewBox: '0 0 28 21', 'aria-hidden': 'true', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.4' }, (GLYPHS[name] || GLYPHS.table)());

const chevron = () => s('svg', { viewBox: '0 0 16 16', width: '14', height: '14', 'aria-hidden': 'true', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.5' }, s('path', { d: 'M10 3L5 8l5 5' }));

export function createFocus({ el, catalog, queryFields = [], env, getBoardId, onChange, onBack }) {
  const types = new Map(catalog.map((c) => [c.type, c]));
  let panel = null, start = null, inst = null, mountedSig = '', lastView = null, opener = null;
  let parts = null, syncers = [], debounce = null, deploySig = '', qbar = null;

  const titleOf = (p) => p.title || (types.get(p.type) && types.get(p.type).title) || p.type;
  const isOpen = () => panel !== null;

  // ---- the stage -----------------------------------------------------------------

  function mountStage() {
    mountedSig = L.panelSignature(panel);
    parts.stageBody.replaceChildren();
    inst = instantiate(panel, parts.stageBody, { types, boardId: getBoardId(), env, focus: true });
    parts.stage.setAttribute('aria-label', titleOf(panel));
    parts.title.textContent = titleOf(panel);
    parts.pane.setAttribute('aria-label', `Settings for ${titleOf(panel)}`);
    if (lastView) inst.api.render(lastView);
  }

  // ---- applying a change -----------------------------------------------------------

  function apply(next, from) {
    if (!panel || !parts) return; // a late blur or timer after the view closed
    if (L.panelSignature(next) === L.panelSignature(panel)) return;
    panel = next;
    onChange(next);
    mountStage();
    for (const sync of syncers) if (sync !== from) sync();
    applyWhen();
    deployPicker(lastView);
    parts.discard.disabled = L.panelSignature(panel) === L.panelSignature(start);
  }

  const setOpt = (name, value, from) => apply(L.setOption(panel, types.get(panel.type), name, value), from);

  // ---- the pane ------------------------------------------------------------------------

  let nextId = 0;
  const fid = (k) => `fv-${k}-${++nextId}`;

  // A row: label, control, the option's help sentence, and a way back to the default.
  function field({ label, doc, control, id, modified, resetTo, kind = 'field' }) {
    const docId = fid('doc');
    const reset = h('button', { class: 'field-reset', type: 'button', hidden: true, 'aria-label': `Reset ${label} to the default`, title: 'Back to the default', onclick: resetTo, text: 'Reset' });
    const row = h('div', { class: `field ${kind}`, 'data-search': `${label} ${doc}`.toLowerCase() },
      h('div', { class: 'field-head' }, kind === 'group' ? h('span', { class: 'field-label', id, text: label }) : h('label', { class: 'field-label', for: id, text: label }), reset),
      control, doc ? h('p', { class: 'field-doc', id: docId, text: doc }) : null);
    const ctl = control.matches('input, select, textarea') ? control : control.querySelector('input, select, textarea');
    if (ctl && doc) ctl.setAttribute('aria-describedby', docId);
    return { row, mark: (on) => { reset.hidden = !on; row.classList.toggle('modified', on); } };
  }

  function textField(name, label, doc, max, multiline, placeholder) {
    const id = fid(name);
    const ctl = h(multiline ? 'textarea' : 'input', { id, class: 'txt', maxlength: String(max), placeholder, rows: multiline ? '4' : null, type: multiline ? null : 'text', autocomplete: 'off', spellcheck: 'false' });
    const count = h('span', { class: 'count', 'aria-hidden': 'true' });
    const f = field({ label, doc, control: h('div', { class: 'txt-wrap' }, ctl, multiline ? count : null), id, resetTo: () => { ctl.value = ''; push(true); } });
    const push = (now) => {
      clearTimeout(debounce);
      const run = () => apply(L.setText(panel, name, ctl.value), sync);
      if (now) run(); else debounce = setTimeout(run, 250);
      count.textContent = `${ctl.value.length} / ${max}`;
    };
    // The control is its own source while it is being typed in.
    const sync = () => {
      if (document.activeElement !== ctl) ctl.value = panel[name] || '';
      count.textContent = `${ctl.value.length} / ${max}`;
      f.mark(!!panel[name]);
    };
    ctl.addEventListener('input', () => push(false));
    ctl.addEventListener('blur', () => { clearTimeout(debounce); if (panel) apply(L.setText(panel, name, ctl.value), sync); });
    syncers.push(sync);
    sync();
    return f.row;
  }

  function vizPicker(o) {
    const name = fid('viz');
    const labelOf = (v) => (o.labels && o.labels[o.values.indexOf(v)]) || v;
    const radios = o.values.map((v) => h('label', { class: 'viz-opt' },
      h('input', { type: 'radio', name, value: v, onchange: () => setOpt(o.name, v, sync) }),
      h('span', { class: 'viz-tile' }, glyph(v), h('span', { class: 'viz-name', text: labelOf(v) }))));
    const set = h('fieldset', { class: 'viz-set' }, h('legend', { class: 'sr-only', text: o.label }), radios);
    const f = field({ label: o.label, doc: o.doc, control: set, id: fid('viz-l'), kind: 'group', resetTo: () => setOpt(o.name, o.default, sync) });
    const sync = () => {
      const v = L.effectiveOptions(panel, types.get(panel.type))[o.name];
      for (const r of radios) r.querySelector('input').checked = r.querySelector('input').value === v;
      f.mark(v !== o.default);
    };
    syncers.push(sync); sync();
    return f.row;
  }

  function optionField(o) {
    if (o.name === 'viz' && o.kind === 'enum') return vizPicker(o);
    const id = fid(o.name);
    const cur = () => L.effectiveOptions(panel, types.get(panel.type))[o.name];
    let ctl, read, write;
    if (o.kind === 'bool') {
      ctl = h('input', { type: 'checkbox', role: 'switch', id, class: 'switch', onchange: () => setOpt(o.name, ctl.checked, sync) });
      read = () => ctl.checked; write = (v) => { ctl.checked = v; };
    } else if (o.kind === 'int') {
      ctl = h('input', { type: 'number', id, class: 'num', min: String(o.min), max: String(o.max), step: '1', inputmode: 'numeric' });
      const commit = () => {
        const n = Number(ctl.value);
        if (ctl.value !== '' && Number.isInteger(n)) setOpt(o.name, Math.min(o.max, Math.max(o.min, n)), sync);
      };
      ctl.addEventListener('change', () => { commit(); ctl.value = String(cur()); });
      ctl.addEventListener('input', () => { const n = Number(ctl.value); if (ctl.value !== '' && Number.isInteger(n) && n >= o.min && n <= o.max) setOpt(o.name, n, sync); });
      write = (v) => { if (document.activeElement !== ctl) ctl.value = String(v); };
    } else {
      ctl = h('select', { id, onchange: () => setOpt(o.name, ctl.value, sync) }, o.values.map((v, i) => h('option', { value: v, text: (o.labels && o.labels[i]) || v })));
      write = (v) => { ctl.value = v; };
    }
    const doc = o.kind === 'int' ? `${o.doc} From ${o.min} to ${o.max}.` : o.doc;
    const f = field({ label: o.label, doc, control: o.kind === 'bool' ? h('label', { class: 'switch-wrap', for: id }, ctl, h('span', { class: 'switch-track', 'aria-hidden': 'true' })) : ctl, id, kind: o.kind === 'bool' ? 'switchrow' : 'field', resetTo: () => setOpt(o.name, o.default, sync) });
    if (o.kind === 'bool') f.row.querySelector('.field-head label').setAttribute('for', id);
    const sync = () => { write(cur()); f.mark(cur() !== o.default); };
    void read;
    if (o.when) f.row.dataset.when = o.when;
    syncers.push(sync); sync();
    return f.row;
  }

  // A section of the pane. Open or closed is remembered for the page, so a person who
  // closed one does not have to close it again for the next panel.
  function group(name, rows, open = true) {
    const was = env.ui.groups && env.ui.groups[name];
    const d = h('details', { class: 'grp', open: (was === undefined ? open : was) ? '' : null, 'data-group': name }, h('summary', null, h('span', { text: name })), h('div', { class: 'grp-body' }, rows));
    d.addEventListener('toggle', () => { env.ui.groups = { ...(env.ui.groups || {}), [name]: d.open }; });
    return d;
  }

  // An option that only applies to one way of drawing the panel is shown only then;
  // a section left with nothing to show goes away.
  function applyWhen() {
    if (!parts) return;
    const cur = L.effectiveOptions(panel, types.get(panel.type));
    for (const row of parts.pane.querySelectorAll('.field[data-when]')) {
      const [name, val] = row.dataset.when.split('=');
      row.classList.toggle('na', cur[name] !== val);
    }
    for (const g of parts.pane.querySelectorAll('details.grp')) {
      const rows = g.querySelectorAll('.field');
      g.classList.toggle('na', rows.length > 0 && !g.querySelector('.field:not(.na)'));
    }
  }

  function buildPane() {
    syncers = [];
    const pt = types.get(panel.type);
    const about = h('p', { class: 'pane-about', text: (pt && (pt.help || pt.doc)) || '' });
    const panelGroup = group('Panel', [
      about,
      textField('title', 'Title', 'Shown above the panel. Empty uses the built-in name.', L.MAX_TITLE, false, (pt && pt.title) || panel.type),
      textField('description', 'Description', 'What the info button says. Empty uses the built-in text.', L.MAX_DESCRIPTION, true, 'Built-in text'),
    ], false);
    const byGroup = new Map();
    for (const o of (pt && pt.options) || []) {
      if (!byGroup.has(o.group)) byGroup.set(o.group, []);
      byGroup.get(o.group).push(o);
    }
    const names = [...byGroup.keys()].sort((a, b) => (GROUP_ORDER.indexOf(a) + 1 || 99) - (GROUP_ORDER.indexOf(b) + 1 || 99));
    const groups = names.map((g) => group(g, byGroup.get(g).map(optionField)));
    const none = h('p', { class: 'pane-none', hidden: true, role: 'status', text: 'No setting matches.' });
    const ordered = [...groups.filter((g) => g.dataset.group === 'Visualization'), panelGroup, ...groups.filter((g) => g.dataset.group !== 'Visualization')];
    const search = h('input', { type: 'search', class: 'pane-search', placeholder: 'Search settings', 'aria-label': 'Search settings', autocomplete: 'off' });
    search.addEventListener('input', () => {
      const q = search.value.trim().toLowerCase();
      let any = false;
      for (const g of ordered) {
        const rows = [...g.querySelectorAll('.field:not(.na)')];
        let hits = 0;
        for (const r of rows) { const ok = !q || r.dataset.search.includes(q); r.hidden = !ok; if (ok) hits++; }
        const aboutEl = g.querySelector('.pane-about');
        if (aboutEl) aboutEl.hidden = !!q;
        g.hidden = !!q && hits === 0;
        if (q && hits) g.open = true;
        any = any || hits > 0;
      }
      none.hidden = any || !q;
    });
    const reset = h('button', { class: 'btn', type: 'button', text: 'Reset to defaults', onclick: () => apply(L.resetPanel(panel)) });
    const copy = h('button', { class: 'btn', type: 'button', text: 'Copy as YAML', onclick: copyYAML });
    const manual = h('textarea', { class: 'yaml-out', readonly: '', rows: '8', 'aria-label': 'Panel as YAML', hidden: true });
    parts.manual = manual;
    parts.status = h('p', { class: 'pane-status', role: 'status' });
    return [h('div', { class: 'pane-search-row' }, search), h('div', { class: 'pane-scroll' }, ordered, none),
      h('div', { class: 'pane-foot' }, h('div', { class: 'pane-actions' }, reset, copy), parts.status, manual)];
  }

  function say(text) { if (parts) parts.status.textContent = text; }

  async function copyYAML() {
    const text = L.panelYAML(panel, catalog);
    try {
      await navigator.clipboard.writeText(text);
      say('Copied. Paste it under panels: in your dashboard file.');
    } catch (_) {
      parts.manual.value = text; parts.manual.hidden = false; parts.manual.select();
      say('Could not copy by itself. The text is selected below; copy it from there.');
    }
  }


  // ---- the query bar -------------------------------------------------------------------------

  // Under the panel: the query it uses, which the person can change. It is not a place to
  // write SQL: it says which of the data the dashboard already has the panel shows, and
  // suggests what exists right now (apps, namespaces, states, database queries) as it is typed.
  function buildQueryBar() {
    const pt = types.get(panel.type);
    if (!pt || !(pt.query && pt.query.length)) return null;
    const id = fid('q');
    const listId = `${id}-list`;
    const input = h('input', { id, class: 'qinput', type: 'text', role: 'combobox', 'aria-autocomplete': 'list', 'aria-expanded': 'false', 'aria-controls': listId,
      autocomplete: 'off', spellcheck: 'false', autocapitalize: 'off', maxlength: String(Q.MAX_QUERY), placeholder: pt.followsDeploy ? 'The deploy picked on the board' : 'All deploys' });
    const list = h('ul', { class: 'qlist', id: listId, role: 'listbox', 'aria-label': 'Suggestions', hidden: '' });
    const status = h('p', { class: 'qstatus', role: 'status' });
    const hint = h('p', { class: 'qhint' });
    const clear = h('button', { class: 'btn qclear', type: 'button', text: 'Clear', hidden: '', onclick: () => { input.value = ''; changed(true); input.focus(); } });
    let active = -1, items = [], range = [0, 0], timer = null;

    const terms = () => Q.parse(input.value).terms;
    const example = () => {
      const app = lastView && lastView.deploys && lastView.deploys[0] && lastView.deploys[0].app;
      const a = app ? `app:${Q.quote(app)}` : 'app:checkout';
      return pt.query.includes('query') ? `${a} statement:UPDATE` : `${a} state:regressed`;
    };

    function close() { list.hidden = true; input.setAttribute('aria-expanded', 'false'); input.removeAttribute('aria-activedescendant'); active = -1; }
    function open() {
      if (!lastView || document.activeElement !== input) return;
      const r = Q.suggest(input.value, input.selectionStart ?? input.value.length, { pt, fields: queryFields, view: lastView, terms: terms() });
      items = r.items; range = [r.from, r.to]; active = -1;
      if (!items.length) { close(); return; }
      list.replaceChildren(...items.map((it, i) => h('li', { id: `${id}-o${i}`, role: 'option', 'aria-selected': 'false', class: 'qopt',
        onmousedown: (e) => { e.preventDefault(); accept(i); } },
      h('span', { class: 'qopt-label', text: it.label }), it.detail ? h('span', { class: 'qopt-detail', text: it.detail }) : null)));
      list.hidden = false;
      input.setAttribute('aria-expanded', 'true');
    }
    function mark(i) {
      active = i;
      [...list.children].forEach((li, k) => { li.setAttribute('aria-selected', String(k === i)); li.classList.toggle('on', k === i); });
      if (i >= 0) { input.setAttribute('aria-activedescendant', `${id}-o${i}`); list.children[i].scrollIntoView({ block: 'nearest' }); } else input.removeAttribute('aria-activedescendant');
    }
    function accept(i) {
      const it = items[i];
      if (!it) return;
      const chars = Array.from(input.value);
      const done = !it.insert.endsWith(':');
      const before = chars.slice(0, range[0]).join(''), after = chars.slice(range[1]).join('');
      const next = `${before}${it.insert}${done && !after.startsWith(' ') ? ' ' : ''}${after}`;
      input.value = next;
      const caret = (before + it.insert).length + (done && !after.startsWith(' ') ? 1 : 0);
      input.setSelectionRange(caret, caret);
      changed(true);
      open();
    }

    // What the text says: an error to fix, or what it comes to now.
    function judge() {
      const text = input.value, parsed = Q.parse(text);
      let problem = parsed.error ? parsed.error.message : Q.validate(pt, queryFields, parsed.terms)[0] || '';
      input.setAttribute('aria-invalid', String(!!problem));
      input.classList.toggle('bad', !!problem);
      clear.hidden = !text;
      if (problem) { status.textContent = problem; status.className = 'qstatus bad'; return false; }
      const o = lastView ? Q.outcome(pt, lastView, parsed.terms) : { text: '' };
      status.textContent = o.text ? `Shows ${o.text}.` : '';
      status.className = `qstatus${o.bad ? ' warn' : ''}`;
      if (o.bad) status.textContent = o.text;
      return true;
    }
    function changed(now) {
      clearTimeout(timer);
      const ok = judge();
      if (!ok) return;
      const run = () => { if (panel) apply(L.setQuery(panel, input.value), sync); };
      if (now) run(); else timer = setTimeout(run, 300);
    }
    const sync = () => {
      if (document.activeElement !== input) input.value = panel.query || '';
      hint.textContent = `Fields: ${pt.query.join(', ')}. Separate terms with spaces; * matches any text. For example ${example()}.`;
      judge();
    };

    input.addEventListener('input', () => { changed(false); open(); });
    input.addEventListener('focus', () => open());
    input.addEventListener('click', () => open());
    input.addEventListener('blur', () => { close(); clearTimeout(timer); if (panel && judge()) apply(L.setQuery(panel, input.value), sync); });
    input.addEventListener('keydown', (e) => {
      const isOpen = !list.hidden;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        if (!isOpen) { open(); return; }
        mark((active + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length);
      } else if ((e.key === 'Enter' || e.key === 'Tab') && isOpen && active >= 0) {
        e.preventDefault();
        accept(active);
      } else if (e.key === 'Enter') {
        e.preventDefault();
        changed(true);
      } else if (e.key === 'Escape' && isOpen) {
        e.preventDefault();
        e.stopPropagation();
        close();
      }
    });
    syncers.push(sync);
    sync();
    const bar = h('div', { class: 'qbar' },
      h('label', { class: 'qlabel', for: id, text: 'Query' }),
      h('div', { class: 'qfield' }, input, list, clear),
      status, hint);
    // The suggestions follow the data: they are drawn again when it moves.
    return { el: bar, stop() { clearTimeout(timer); }, refresh() { judge(); if (!list.hidden) open(); } };
  }

  // ---- the toolbar ---------------------------------------------------------------------

  function deployPicker(view) {
    const pt = types.get(panel.type);
    if (!pt || !pt.followsDeploy) { parts.deployWrap.hidden = true; return; }
    parts.deployWrap.hidden = false;
    const list = view ? [...view.deploys] : [];
    if (view && view.kase && !list.some((d) => d.id === view.kase.id)) list.unshift(view.kase);
    const sig = JSON.stringify([list.map((d) => d.id), view && view.selected]);
    if (sig !== deploySig) {
      deploySig = sig;
      parts.deploy.replaceChildren(...(list.length ? list : [{ id: '', app: 'No deploys in this range', namespace: '', at: null }]).map((d) =>
        h('option', { value: d.id, text: d.id ? `${d.app}${d.namespace ? ` · ${d.namespace}` : ''}${d.at ? ` · ${clock(Date.parse(d.at))}` : ''}` : d.app })));
    }
    if (view && view.selected) parts.deploy.value = view.selected;
    // A panel whose query chooses the deploy does not follow the board's.
    const own = Q.parse(panel.query).terms.some((t) => ['deploy', 'app', 'namespace', 'state', 'grade'].includes(t.field));
    parts.deploy.disabled = own || !list.length;
    parts.deploy.title = own ? "This panel's query chooses the deploy" : '';
  }

  function togglePane(on) {
    env.ui.focusPane = on;
    parts.main.classList.toggle('no-pane', !on);
    parts.toggle.setAttribute('aria-expanded', String(on));
    parts.toggle.textContent = on ? 'Hide settings' : 'Show settings';
  }

  function onKey(e) {
    if (e.key !== 'Escape' || e.defaultPrevented) return;
    const tip = document.getElementById('infotip'), pop = document.getElementById('range-pop');
    if ((tip && !tip.hidden) || (pop && !pop.hidden)) return;
    const t = e.target;
    // The first Escape leaves a field; the next one leaves the view.
    if (t instanceof Element && t.matches('input, textarea, select') && !t.matches('input[type="search"]:placeholder-shown')) { t.blur(); e.preventDefault(); return; }
    e.preventDefault();
    onBack();
  }

  // ---- public ------------------------------------------------------------------------------

  return {
    isOpen,
    panelId: () => (panel ? panel.id : null),
    open(p, view) {
      if (panel) this.close();
      closeInfoTip();
      panel = { ...p, at: [...p.at] };
      start = panel;
      lastView = view;
      opener = document.activeElement;
      const title = h('h2', { class: 'focus-title', tabindex: '-1' });
      const back = h('button', { class: 'btn focus-back', type: 'button', onclick: onBack }, chevron(), h('span', { text: 'Back to the board' }));
      const deploy = h('select', { id: 'focus-deploy', onchange: (e) => e.target.value && env.actions.select(e.target.value) });
      const deployWrap = h('label', { class: 'focus-deploy', for: 'focus-deploy' }, h('span', { text: 'Deploy' }), deploy);
      const discard = h('button', { class: 'btn', type: 'button', disabled: '', text: 'Discard changes', onclick: () => { apply(start); } });
      const toggle = h('button', { class: 'btn', type: 'button', 'aria-controls': 'focus-pane' });
      const bar = h('div', { class: 'focus-bar' }, back, title, h('div', { class: 'focus-tools' }, deployWrap, discard, toggle));
      const stageBody = h('div', { class: 'p-body' });
      const stage = h('section', { class: `panel focus-panel type-${p.type}`, 'data-panel': p.id }, stageBody);
      const pane = h('aside', { class: 'focus-pane', id: 'focus-pane' });
      parts = { bar, title, back, deploy, deployWrap, discard, toggle, stage, stageBody, pane, main: null, manual: null, status: null };
      pane.replaceChildren(...buildPane());
      qbar = buildQueryBar();
      const col = h('div', { class: 'focus-stagecol' }, stage, qbar ? qbar.el : null);
      const main = h('div', { class: 'focus-main' }, col, pane);
      parts.main = main;
      applyWhen();
      toggle.addEventListener('click', () => togglePane(!(env.ui.focusPane !== false)));
      togglePane(env.ui.focusPane !== false);
      deploySig = '';
      deployPicker(view);
      mountStage();
      el.replaceChildren(bar, main);
      el.hidden = false;
      document.addEventListener('keydown', onKey);
      title.focus({ preventScroll: true });
    },
    update(view) {
      lastView = view;
      if (!panel) return;
      deployPicker(view);
      if (inst) inst.api.render(view);
      if (qbar) qbar.refresh();
    },
    close() {
      if (!panel) return;
      clearTimeout(debounce);
      if (qbar) qbar.stop();
      document.removeEventListener('keydown', onKey);
      const id = panel.id;
      panel = null; start = null; inst = null; parts = null; syncers = []; qbar = null;
      el.hidden = true;
      el.replaceChildren();
      const o = opener;
      opener = null;
      // Focus goes back to where it was, or to the panel's own button if that was rebuilt.
      queueMicrotask(() => {
        if (o && o.isConnected && o !== document.body) { o.focus({ preventScroll: true }); return; }
        const b = document.querySelector(`[data-panel="${CSS.escape(id)}"] .p-expand`);
        if (b) b.focus({ preventScroll: true });
      });
    },
  };
}
