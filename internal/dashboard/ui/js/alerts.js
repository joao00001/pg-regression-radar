// Alerts: the few situations that deserve to stay on screen until somebody has
// dealt with them. Pure, so the rules are tested without a browser.
//
// The rules, in one place:
//
//   - A deploy the engine reads as Regressed raises one alert, whatever the time
//     range the board is showing. The alert stays until the person dismisses it,
//     or until the situation is over.
//   - It is over when the engine reads the deploy as Healthy again (an abort
//     that worked, a pause that was lifted, a later evaluation that came back
//     clean), or the abort check says "recovered". The alert then goes away by
//     itself. An inconclusive reading is not an all-clear, so it stays.
//   - One alert per deploy, whose wording follows how far the radar got:
//     regression found, paused or aborted and being checked, and the two worse
//     ones, "still regressed after the abort" and "could not act".
//   - A person who dismissed an alert is not told again about that same level
//     of trouble. Trouble that gets worse (persisted, failed) is a new alert.
//     The dismissal is forgotten when the deploy recovers, so a later relapse
//     alerts again.
//   - An open circuit breaker (the radar stopped acting by itself) is an alert
//     of its own, per watch, until the breaker is closed.
//   - If a deploy or watch stops being reported (the manager restarted and its
//     in-memory history is gone), its alert stays, marked as no longer reported:
//     silence is not an all-clear.

export const DISMISS_KEY = 'pgrr-dashboard-dismissed-v1';
const KEEP_MS = 30 * 24 * 3600e3;
const KEEP_MAX = 300;

const ts = (iso) => { const t = Date.parse(iso); return Number.isFinite(t) ? t : 0; };

// How far the radar got with a regressed deploy, as one stage.
export function stageOf(d) {
  const a = d.lastAction;
  if (a && a.result === 'persisted') {
    return { level: 'critical', stage: 'persisted', title: 'Still regressed after the abort', at: ts(a.at), reason: a.reason, docs: a.docs };
  }
  if (a && a.result === 'failed') {
    const what = a.action === 'abort' ? 'abort' : 'pause';
    return { level: 'critical', stage: 'failed', title: `Could not ${what} the rollout`, at: ts(a.at), reason: a.reason, docs: a.docs };
  }
  if (d.phase === 'acted' || d.phase === 'verified') {
    const abort = a && a.action === 'abort';
    return {
      level: 'alert', stage: 'acted',
      title: d.phase === 'verified' ? 'Regressed, abort checked' : abort ? 'Regressed, rollout aborted' : 'Regressed, rollout paused',
      at: ts(a && a.at), reason: a && a.reason, docs: a && a.docs,
    };
  }
  return { level: 'alert', stage: 'regressed', title: 'Regression detected', at: ts(d.latest && d.latest.at) || ts(d.at) };
}

// Is the trouble over? Only a clear reading says so.
export function recovered(d) {
  if (d.health === 'Healthy') return true;
  const a = d.lastAction;
  return !!(a && a.result === 'recovered' && d.health !== 'Regressed');
}

function deployAlert(d) {
  const s = stageOf(d);
  return {
    id: `deploy:${d.id}`, kind: 'deploy', deployId: d.id, app: d.app, namespace: d.namespace,
    level: s.level, stage: s.stage, title: s.title, text: d.sentence || '', at: s.at || ts(d.at),
    reason: s.reason || '', docs: s.docs || '', missing: false,
  };
}

function breakerAlert(w, docsHref) {
  const c = (w.conditions || []).find((x) => x.type === 'Actuating' && x.reason === 'BreakerOpen');
  return {
    id: `watch:${w.namespace}/${w.name}`, kind: 'breaker', deployId: '', app: w.name, namespace: w.namespace,
    level: 'critical', stage: 'breaker', title: 'The radar stopped acting by itself',
    text: c && c.message
      ? c.message.replace(/\s*See https?:\/\/\S+$/, '')
      : 'Earlier aborts did not make the regression go away, so it only alerts until the breaker is closed.',
    at: 0, reason: c ? c.reason : 'BreakerOpen', docs: c ? docsHref(c.docs) || '' : '', missing: false,
  };
}

// The key a dismissal is remembered under: the alert and how bad it is, so that
// getting worse is news and staying the same is not.
export const dismissKey = (a) => `${a.id}#${a.level}`;

// remember: whether a dismissal outlives the page (the person's choice, and it can change
// while the page is open). When it does not, dismissals are kept in memory only.
export function createAlerts({ storage, now = Date.now, docsHref = (u) => u, remember = () => true } = {}) {
  const active = new Map();   // alert id -> alert, in the order it was first raised
  let dismissed = remember() ? load() : {};

  function load() {
    try {
      const raw = JSON.parse(storage.getItem(DISMISS_KEY) || '{}');
      const t = now();
      const keep = Object.entries(raw && typeof raw === 'object' ? raw : {})
        .filter(([, v]) => Number.isFinite(v) && t - v < KEEP_MS)
        .sort((x, y) => y[1] - x[1]).slice(0, KEEP_MAX);
      return Object.fromEntries(keep);
    } catch (_) { return {}; }
  }
  function save() {
    if (!remember()) return;
    try { storage.setItem(DISMISS_KEY, JSON.stringify(dismissed)); } catch (_) { /* the page still works; it just forgets */ }
  }
  function forget(id) {
    let changed = false;
    for (const k of Object.keys(dismissed)) if (k.startsWith(`${id}#`)) { delete dismissed[k]; changed = true; }
    if (changed) save();
  }

  // Read the newest state. Returns what just went away because it was resolved.
  // A deploy that is in the list but not in `active` has nothing to resolve.
  function update({ deploys = [], watches = [], complete = true }) {
    const resolved = [];
    const seenDeploys = new Set(), seenWatches = new Set();

    for (const d of deploys) {
      const id = `deploy:${d.id}`;
      seenDeploys.add(id);
      if (d.health === 'Regressed') {
        const next = deployAlert(d);
        active.set(id, next);
      } else if (recovered(d)) {
        const was = active.get(id);
        if (was) { resolved.push(was); active.delete(id); }
        forget(id);
      } else {
        const was = active.get(id);
        if (was) was.missing = false; // still reported, just not clearly better yet
      }
    }
    for (const w of watches) {
      const id = `watch:${w.namespace}/${w.name}`;
      seenWatches.add(id);
      if (w.breakerOpen) {
        active.set(id, breakerAlert(w, docsHref));
      } else {
        const was = active.get(id);
        if (was) { resolved.push(was); active.delete(id); }
        forget(id);
      }
    }
    // `complete` is false when the list is only part of the history (the wide read
    // failed and the board's own range stands in): then absence says nothing.
    if (complete) {
      for (const [id, a] of active) {
        const seen = a.kind === 'deploy' ? seenDeploys : seenWatches;
        if (!seen.has(id)) a.missing = true;
      }
    }
    return { resolved };
  }

  // What to show: not dismissed, the worst first, then the newest.
  function visible() {
    return [...active.values()]
      .filter((a) => !dismissed[dismissKey(a)])
      .sort((x, y) => (y.level === 'critical') - (x.level === 'critical') || y.at - x.at || (x.id < y.id ? -1 : 1));
  }

  function dismiss(key) {
    dismissed[key] = now();
    save();
  }
  function dismissAll() {
    for (const a of visible()) dismissed[dismissKey(a)] = now();
    save();
  }

  // The person changed whether dismissals outlive the page: keep them from now on, or
  // let go of what was kept (what is dismissed on this page stays dismissed until it is closed).
  function remembering(on) {
    if (on) save();
    else { try { storage.removeItem(DISMISS_KEY); } catch (_) { /* nothing to forget */ } }
  }

  return { update, visible, dismiss, dismissAll, remembering, size: () => active.size };
}
