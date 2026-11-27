// Pure functions: everything the UI decides about what it was given. No DOM, so
// it runs under `node --test`.

// ---- time -------------------------------------------------------------------

// The UI's idea of "now" is the server's clock plus the time since it was read,
// so "next check in 3m" is right even when this machine's clock is off, and for
// recorded data whose clock is frozen in the past.
export function clockOffset(serverNowIso, localNowMs) {
  const t = Date.parse(serverNowIso);
  return Number.isFinite(t) ? t - localNowMs : 0;
}
export const estimateNow = (offset, localNowMs) => localNowMs + offset;

export function duration(ms) {
  const a = Math.abs(ms);
  if (a < 45e3) return `${Math.max(1, Math.round(a / 1e3))}s`;
  if (a < 90 * 60e3) return `${Math.round(a / 60e3)}m`;
  if (a < 36 * 3600e3) return `${Math.round(a / 3600e3)}h`;
  return `${Math.round(a / 86400e3)}d`;
}

// "3m ago" or "in 3m".
export function relative(thenMs, nowMs) {
  const d = thenMs - nowMs;
  if (Math.abs(d) < 5e3) return 'just now';
  return d < 0 ? `${duration(d)} ago` : `in ${duration(d)}`;
}

export function clock(ms, withSeconds = false) {
  const d = new Date(ms);
  const p = (n) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}${withSeconds ? ':' + p(d.getSeconds()) : ''}`;
}

// ---- numbers ----------------------------------------------------------------

export const percent = (x) => {
  if (!Number.isFinite(x)) return '–';
  const v = x * 100;
  return (v >= 10 || v === 0 ? v.toFixed(0) : v.toFixed(1)) + '%';
};
export const times = (r) => (Number.isFinite(r) && r > 0 ? r.toFixed(2) + '×' : '–');
export const ms = (v) => (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + ' ms';

// ---- state of a deploy ------------------------------------------------------

// One tone per state, and only state: watching, ok, regressed, acted, quiet.
export function tone(d) {
  if (d.phase === 'acted' || d.phase === 'verified') return 'acted';
  if (d.health === 'Regressed') return 'regressed';
  if (d.phase === 'observing' || !d.latest) return 'watching';
  if (d.health === 'Healthy') return 'ok';
  return 'quiet';
}

export const TONE_LABEL = {
  watching: 'Watching',
  ok: 'Healthy',
  regressed: 'Regressed',
  acted: 'Acted',
  quiet: 'Inconclusive',
};

// The phase says how far the radar got; the label says it in the user's words.
export function stateLabel(d) {
  if (d.phase === 'verified') return 'Acted and checked';
  if (d.phase === 'acted') return 'Acted';
  return TONE_LABEL[tone(d)];
}

// Evidence strength as a count out of four: A is 4 and D is 1, none is 0. It
// drives the segmented bar next to a grade, so a person sees "how much" at a glance.
export const GRADES = ['A', 'B', 'C', 'D'];
export const gradeLevel = (g) => { const i = GRADES.indexOf(g); return i < 0 ? 0 : GRADES.length - i; };

// The query a chart opens on when the person has not picked one: with 'first' the
// engine's own order (the ones that explain the verdict come first), with 'worst'
// the flagged query that moved furthest from 1.0.
export function defaultUnit(units, policy = 'first') {
  if (!units || !units.length) return null;
  if (policy === 'worst') {
    const flagged = units.filter((u) => u.rejected && u.ratio > 0);
    if (flagged.length) return flagged.reduce((a, b) => (Math.abs(Math.log(b.ratio)) > Math.abs(Math.log(a.ratio)) ? b : a)).key;
  }
  return units[0].key;
}

// What a deploy with no grade shows in the badge.
export const gradeOf = (d) => (d.latest && d.latest.grade) || '';

// The card to open first: something that needs a person, else the newest.
export function defaultSelection(deploys) {
  if (!deploys.length) return null;
  const rank = { acted: 0, regressed: 1, watching: 2, quiet: 3, ok: 4 };
  return [...deploys].sort((a, b) => rank[tone(a)] - rank[tone(b)] || Date.parse(b.at) - Date.parse(a.at))[0].id;
}

// ---- the top of the page ----------------------------------------------------

const ORDER = ['regressed', 'acted', 'watching', 'quiet', 'ok'];
const MAX_SEGMENTS = 30;

// What the hero says about the deploys in view: a count per state, the one-line
// answer to "is anything wrong?", and the segments of the bar (one per deploy,
// or one per state with a weight when there are too many to draw).
export function summary(deploys) {
  const counts = { regressed: 0, acted: 0, watching: 0, ok: 0, quiet: 0 };
  for (const d of deploys) counts[tone(d)]++;
  const total = deploys.length;
  const noun = (n) => (n === 1 ? 'deploy' : 'deploys');
  // Acting means the radar judged a regression, so both count as "regressed".
  const bad = counts.regressed + counts.acted;

  let lead, rest = [];
  if (!total) {
    lead = 'No deploys in this range.';
    rest.push('The radar judges a deploy about ten minutes after it registers.');
  } else if (bad) {
    lead = `${bad} of ${total} ${noun(total)} regressed.`;
    if (counts.acted) rest.push(`The radar acted on ${counts.acted}.`);
    if (counts.watching) rest.push(`${counts.watching} still being judged.`);
  } else if (counts.watching) {
    lead = 'Nothing has regressed so far.';
    rest.push(`${counts.watching} still being judged.`);
  } else {
    lead = 'Nothing regressed.';
    rest.push(`${counts.ok} ${noun(counts.ok)} judged healthy.`);
    if (counts.quiet) rest.push(`${counts.quiet} inconclusive.`);
  }

  let segs;
  if (total <= MAX_SEGMENTS) {
    segs = deploys.map((d) => tone(d)).sort((a, b) => ORDER.indexOf(a) - ORDER.indexOf(b)).map((t) => ({ tone: t, n: 1 }));
  } else {
    segs = ORDER.filter((t) => counts[t]).map((t) => ({ tone: t, n: counts[t] }));
  }
  return { total, counts, lead, detail: rest.join(' '), segs };
}

// ---- what a panel's settings ask it to show ------------------------------------------------

// The deploys a list panel shows: filtered by state, ordered, cut to `limit`.
// `matching` counts what passed the filter before the cut, `total` everything.
export function pickDeploys(deploys, { show = 'all', sort = 'newest', limit = 12 } = {}) {
  const keep = {
    all: () => true,
    problems: (d) => tone(d) === 'regressed' || tone(d) === 'acted',
    watching: (d) => tone(d) === 'watching',
    healthy: (d) => tone(d) === 'ok',
  }[show] || (() => true);
  const list = (deploys || []).filter(keep);
  const at = (d) => Date.parse(d.at) || 0;
  if (sort === 'worst') list.sort((a, b) => ORDER.indexOf(tone(a)) - ORDER.indexOf(tone(b)) || at(b) - at(a));
  else list.sort((a, b) => at(b) - at(a));
  return { list: list.slice(0, limit), matching: list.length, total: (deploys || []).length };
}

// The queries of an evaluation as the evidence table lists them: filtered, then ordered.
export function evidenceRows(units, { show = 'all', sort = 'engine' } = {}) {
  const names = labelUnits(units);
  const num = (v) => (Number.isFinite(v) ? v : -Infinity);
  const by = {
    impact: (a, b) => num(b.impactShare) - num(a.impactShare),
    slowdown: (a, b) => num(b.ratio) - num(a.ratio),
    name: (a, b) => names.get(a.key).localeCompare(names.get(b.key)),
  }[sort];
  const rows = (units || []).filter((u) => show !== 'flagged' || u.rejected);
  return by ? rows.sort(by) : rows;
}

// ---- status strip -----------------------------------------------------------

// Each item: {id, label, value, tone: ok|warn|bad|info, detail}. The order is the
// order a person needs it: is the data fresh, may it act, is anything stopped.
export function statusItems(overview, config, nowMs) {
  const items = [];
  const watches = (overview && overview.watches) || [];
  if (config && config.origin === 'fixture') {
    items.push({ id: 'origin', label: 'Data', value: 'Recorded sample', tone: 'info', detail: config.note || 'Not a live cluster.' });
  }
  if (!watches.length) {
    items.push({ id: 'watches', label: 'Watches', value: 'none', tone: 'warn', detail: 'No PostgresWatch is running on this manager.' });
    return items;
  }

  const stale = Math.max(...watches.map((w) => w.collector.staleSeconds));
  const never = watches.some((w) => w.collector.staleSeconds < 0);
  if (never) items.push({ id: 'fresh', label: 'Data', value: 'no scrape yet', tone: 'warn', detail: 'A watch has not read pg_stat_statements yet.' });
  else if (stale > 180) items.push({ id: 'fresh', label: 'Data', value: `${duration(stale * 1e3)} old`, tone: 'bad', detail: 'The newest scrape is old; every verdict below rests on it.' });
  else items.push({ id: 'fresh', label: 'Data', value: 'fresh', tone: 'ok', detail: `Newest scrape ${duration(stale * 1e3)} ago.` });

  const enforcing = watches.filter((w) => w.mode === 'enforce').length;
  items.push({
    id: 'mode', label: 'Mode',
    value: enforcing === 0 ? 'shadow' : enforcing === watches.length ? 'enforce' : `${enforcing}/${watches.length} enforce`,
    tone: 'info',
    detail: enforcing === 0 ? 'Judging and logging only; nothing is paused or aborted.' : 'Decisions can pause or abort a rollout.',
  });

  const acting = watches.filter((w) => w.actuation.enabled && !w.actuation.paused).length;
  const paused = watches.some((w) => w.actuation.paused);
  items.push({
    id: 'actuation', label: 'Acting',
    value: paused ? 'switched off' : acting ? 'on' : 'off',
    tone: paused ? 'warn' : 'info',
    detail: paused ? 'The kill-switch annotation is set: no action will be taken.' : acting ? 'May pause or abort Argo Rollouts rollouts.' : 'Will not touch any rollout.',
  });

  const open = watches.filter((w) => w.breakerOpen);
  items.push({
    id: 'breaker', label: 'Breaker',
    value: open.length ? 'open' : 'closed',
    tone: open.length ? 'bad' : 'ok',
    detail: open.length ? 'Earlier actions did not help, so the radar only alerts until it is reset.' : 'Automatic actions are allowed.',
  });

  const withArms = watches.filter((w) => w.arms);
  if (withArms.length) {
    const bad = withArms.filter((w) => !w.arms.usable);
    items.push({
      id: 'arms', label: 'Log source',
      value: bad.length ? 'not usable' : 'flowing',
      tone: bad.length ? 'warn' : 'ok',
      detail: bad.length ? bad[0].arms.why || 'The Postgres log cannot be used.' : 'Canary and stable pods can be told apart (grade A is possible).',
    });
  } else {
    items.push({ id: 'arms', label: 'Log source', value: 'off', tone: 'info', detail: 'Without it the strongest decision is to pause (grade B at most).' });
  }

  const sc = overview.scope || {};
  items.push({
    id: 'scope', label: 'Scope',
    value: sc.all ? 'all namespaces' : (sc.namespaces || []).join(', ') || 'nothing',
    tone: 'info',
    detail: sc.all ? 'No allowlist is set.' : 'Only deploys in these namespaces are shown.',
  });
  return items;
}

// ---- decision feed ----------------------------------------------------------

export function actionText(a) {
  const what = a.action === 'abort' ? 'the abort' : 'the pause';
  switch (a.result) {
    case 'done': return a.action === 'abort' ? 'Aborted the rollout' : 'Paused the rollout';
    case 'failed': return `Could not apply ${what}`;
    case 'skipped': return `Skipped ${what}`;
    case 'recovered': return 'Checked the abort: the regression went away';
    case 'persisted': return 'Checked the abort: the regression is still there';
    default: return `${a.action} ${a.result}`;
  }
}

export function evaluationText(e) {
  const d = e.decision;
  if (e.health === 'Healthy') return 'Judged: nothing regressed beyond the margin';
  if (e.health !== 'Regressed') return `Judged: ${e.health.toLowerCase()}`;
  const what = d.action === 'none' ? 'no action' : d.action === 'hold' ? 'a pause' : d.action === 'rollback' ? 'an abort' : d.action;
  return `Judged: grade ${d.grade || '–'}, ${percent(d.impact)} added database time, ${what}${d.enforced ? '' : ' (not enforced)'}`;
}

// Everything that happened, newest first, from the cases the UI holds.
export function feedEntries(cases, limit = 60, show = 'all') {
  const out = [];
  for (const c of cases) {
    for (const e of c.history || []) {
      out.push({ at: Date.parse(e.at), kind: 'evaluation', deployId: c.id, app: c.app, namespace: c.namespace, text: evaluationText(e), bad: e.health === 'Regressed' });
    }
    for (const a of c.actions || []) {
      out.push({ at: Date.parse(a.at), kind: 'action', deployId: c.id, app: c.app, namespace: c.namespace, text: actionText(a), bad: a.result === 'failed' || a.result === 'persisted', detail: a.detail });
    }
  }
  const keep = show === 'problems' ? (e) => e.bad : show === 'actions' ? (e) => e.kind === 'action' : () => true;
  return out.filter(keep).sort((x, y) => y.at - x.at || (x.kind === 'action' ? -1 : 1)).slice(0, limit);
}

// ---- naming queries -------------------------------------------------------------------

// The engine names a query by its statement and the tables it touches, "SELECT:orders".
// Said in words that is "SELECT on orders", which is what a person can recognise
// without the SQL text (hidden unless asked for).
export function queryLabel(family, key = '') {
  if (!family) return key ? key.slice(0, 8) : 'query';
  const i = family.indexOf(':');
  return i > 0 && i < family.length - 1 ? `${family.slice(0, i)} on ${family.slice(i + 1)}` : family;
}

// A label for each query of a list, by key. Queries that would read the same ("SELECT
// on orders" three times) are numbered in list order, so two rows are never alike.
export function labelUnits(...lists) {
  const seen = new Map(); // key -> family
  for (const l of lists) for (const u of l || []) if (!seen.has(u.key)) seen.set(u.key, u.family || '');
  const base = new Map([...seen].map(([k, f]) => [k, queryLabel(f, k)]));
  const total = new Map();
  for (const b of base.values()) total.set(b, (total.get(b) || 0) + 1);
  const nth = new Map(), out = new Map();
  for (const [k, b] of base) {
    if (total.get(b) === 1) { out.set(k, b); continue; }
    nth.set(b, (nth.get(b) || 0) + 1);
    out.set(k, `${b} #${nth.get(b)}`);
  }
  return out;
}

// ---- ratio chart (forest plot) ------------------------------------------------------

// One row per query that has a usable ratio, the ones that regressed first and,
// within a group, the furthest from 1.0 on top. A ratio of 1 means nothing changed.
export function ratioRows(units, limit = 25, show = 'all') {
  const ok = (v) => Number.isFinite(v) && v > 0;
  const names = labelUnits(units);
  const rows = (units || [])
    .filter((u) => ok(u.ratio) && ok(u.ratioLow) && ok(u.ratioHigh) && (show !== 'flagged' || u.rejected))
    .map((u) => ({
      key: u.key, label: names.get(u.key), ratio: u.ratio, low: u.ratioLow, high: u.ratioHigh,
      rejected: !!u.rejected, grade: u.grade || '', kind: u.kind,
      // The interval clears 1.0 on one side: the query really moved.
      moved: u.ratioLow > 1 || u.ratioHigh < 1,
    }));
  rows.sort((a, b) => (b.rejected - a.rejected) || (Math.abs(Math.log(b.ratio)) - Math.abs(Math.log(a.ratio))));
  return rows.slice(0, limit);
}

// A log axis that always contains 1.0 and every interval, from a short list of
// round ratios, so the picture reads "twice as slow" or "half as slow" evenly.
const RATIO_STEPS = [0.125, 0.25, 0.5, 1, 2, 4, 8, 16, 32, 64];
export function ratioAxis(rows) {
  let lo = 1, hi = 1;
  for (const r of rows) { lo = Math.min(lo, r.low, r.ratio); hi = Math.max(hi, r.high, r.ratio); }
  const min = [...RATIO_STEPS].reverse().find((t) => t <= lo) ?? RATIO_STEPS[0];
  const max = RATIO_STEPS.find((t) => t >= hi) ?? RATIO_STEPS[RATIO_STEPS.length - 1];
  const lo2 = Math.min(min, 0.5), hi2 = Math.max(max, 2); // never so tight that 1.0 hugs an edge
  return { min: lo2, max: hi2, ticks: RATIO_STEPS.filter((t) => t >= lo2 && t <= hi2) };
}

// Position of a ratio on the log axis, 0 to 1. Values past the axis are clamped,
// and `clipped` says so, so the picture never pretends an interval ends where the axis does.
export function ratioPos(v, axis) {
  const p = (Math.log(v) - Math.log(axis.min)) / (Math.log(axis.max) - Math.log(axis.min));
  return { pos: Math.min(1, Math.max(0, p)), clipped: p < 0 || p > 1 };
}

// ---- evaluations ----------------------------------------------------------------------------

// The judgements of one deploy as a timeline, oldest first.
export function timeline(history) {
  return (history || []).map((e, i) => ({
    index: i, at: Date.parse(e.at), health: e.health, grade: (e.decision && e.decision.grade) || '',
    action: e.decision && e.decision.action, wanted: e.decision && e.decision.wanted,
    impact: e.decision ? e.decision.impact : 0, regressed: e.counts.regressed, tested: e.counts.tested,
    coverage: e.coverage, units: e.units || [],
  }));
}

// What changed between two evaluations. An older evaluation keeps only the queries
// that were flagged, so a query missing from it is "not recorded", never "was fine".
export function compareEvaluations(a, b) {
  const byKey = (e) => new Map((e.units || []).map((u) => [u.key, u]));
  const ma = byKey(a), mb = byKey(b);
  const keys = [...new Set([...ma.keys(), ...mb.keys()])];
  const names = labelUnits(a.units, b.units);
  const pick = (u) => (u ? { ratio: u.ratio, low: u.ratioLow, high: u.ratioHigh, rejected: !!u.rejected, grade: u.grade } : null);
  const units = keys.map((k) => {
    const ua = ma.get(k), ub = mb.get(k);
    const ra = ua && ua.ratio > 0 ? ua.ratio : NaN, rb = ub && ub.ratio > 0 ? ub.ratio : NaN;
    return { key: k, family: (ub || ua).family || k, label: names.get(k), a: pick(ua), b: pick(ub), delta: Number.isFinite(ra) && Number.isFinite(rb) ? rb / ra : NaN };
  });
  // The furthest movement first, then the ones present on only one side.
  units.sort((x, y) => (Number.isFinite(y.delta) ? Math.abs(Math.log(y.delta)) : -1) - (Number.isFinite(x.delta) ? Math.abs(Math.log(x.delta)) : -1));
  return {
    grade: [a.decision.grade || '', b.decision.grade || ''],
    impact: [a.decision.impact, b.decision.impact],
    regressed: [a.counts.regressed, b.counts.regressed],
    tested: [a.counts.tested, b.counts.tested],
    coverage: [a.coverage, b.coverage],
    action: [a.decision.action, b.decision.action],
    units,
  };
}

// ---- source health and safety -------------------------------------------------------------

// The health condition of a watch by type: the same Collecting, ArmsLog and
// Actuating that `kubectl describe postgreswatch` shows. A manager that predates
// them sends none, and the rows below fall back to the raw numbers.
export function conditionOf(w, type) {
  return ((w && w.conditions) || []).find((c) => c.type === type) || null;
}

// A link to the docs is only followed when it is https: the page does not trust
// what a manager sends to be a safe URL.
export function docsHref(url) {
  return typeof url === 'string' && url.startsWith('https://') ? url : null;
}

// What a not-healthy condition adds to a row: its reason code and the page that
// explains it. A healthy one adds nothing.
function why(cond) {
  return cond && cond.status === 'False' ? { reason: cond.reason, docs: docsHref(cond.docs), message: cond.message || '' } : { reason: '', docs: null, message: '' };
}

// One row per source the radar reads: is it answering, how fresh, and what is missing.
export function sourceRows(overview, nowMs) {
  const rows = [];
  for (const w of (overview && overview.watches) || []) {
    const name = `${w.namespace}/${w.name}`;
    const never = w.collector.staleSeconds < 0;
    const stale = w.collector.staleSeconds;
    rows.push({
      id: `scrape:${name}`, source: 'Query statistics', watch: name,
      tone: never ? 'warn' : stale > 180 ? 'bad' : 'ok',
      value: never ? 'no scrape yet' : stale > 180 ? `${duration(stale * 1e3)} old` : 'fresh',
      detail: never ? 'Nothing has been read from pg_stat_statements yet.' : `Last read ${w.collector.lastScrape ? relative(Date.parse(w.collector.lastScrape), nowMs) : `${duration(stale * 1e3)} ago`}. Every verdict rests on this.`,
      at: w.collector.lastScrape ? Date.parse(w.collector.lastScrape) : null,
      ...why(conditionOf(w, 'Collecting')),
    });
    if (w.arms) {
      const a = w.arms;
      rows.push({
        id: `log:${name}`, source: 'Postgres log', watch: name,
        tone: a.usable ? 'ok' : 'warn', value: a.usable ? 'flowing' : 'not usable',
        detail: a.usable
          ? `${a.records.toLocaleString('en-US')} records read${a.dropped ? `, ${a.dropped.toLocaleString('en-US')} dropped` : ''}. Canary and stable pods can be told apart.`
          : a.why || 'The log cannot be used, so grade A is not possible.',
        at: a.newest ? Date.parse(a.newest) : null,
        ...why(conditionOf(w, 'ArmsLog')),
      });
    } else {
      rows.push({ id: `log:${name}`, source: 'Postgres log', watch: name, tone: 'info', value: 'off', detail: 'Not configured. Without it the strongest decision is a pause (grade B at most).', at: null });
    }
  }
  const h = overview && overview.history;
  if (h) {
    rows.push({
      id: 'history', source: 'History', watch: '',
      tone: h.recording ? 'ok' : 'bad', value: h.recording ? h.store : `${h.store}, not recording`,
      detail: h.recording
        ? (h.note || `Kept in ${h.store}${h.retentionSeconds ? ` for ${duration(h.retentionSeconds * 1e3)}` : ''}.`)
        : (h.note || 'The store cannot be written to: the radar keeps acting, but this stretch of history will have a gap.'),
      at: h.since ? Date.parse(h.since) : null,
    });
  }
  return rows;
}

// What the radar may do here, per watch.
export function safetyRows(overview) {
  const rows = [];
  for (const w of (overview && overview.watches) || []) {
    const act = w.actuation;
    const state = act.paused ? 'switched off' : act.enabled ? 'allowed' : 'off';
    rows.push({
      id: `${w.namespace}/${w.name}`, name: w.name, namespace: w.namespace, cluster: w.cluster,
      mode: w.mode,
      acting: state, actingTone: act.paused ? 'warn' : 'info',
      actingDetail: act.paused ? 'The kill-switch annotation is set: no action will be taken.' : act.enabled ? 'May pause or abort Argo Rollouts rollouts.' : 'Will not touch any rollout.',
      breaker: w.breakerOpen ? 'open' : 'closed', breakerTone: w.breakerOpen ? 'bad' : 'ok',
      breakerDetail: w.breakerOpen ? 'Earlier aborts did not make the regression go away, so the radar only alerts until it is reset.' : 'Automatic actions are allowed.',
      namespaces: act.namespaces || [],
      ...(() => {
        // One condition says whether it may act, and why not. Its reason is the
        // breaker's when the breaker is what stops it, which has its own cell.
        const c = why(conditionOf(w, 'Actuating'));
        return w.breakerOpen ? { breakerReason: c.reason, breakerDocs: c.docs, breakerMessage: c.message, actingReason: '', actingDocs: null, actingMessage: '' }
          : { actingReason: c.reason, actingDocs: c.docs, actingMessage: c.message, breakerReason: '', breakerDocs: null, breakerMessage: '' };
      })(),
    });
  }
  return rows;
}
