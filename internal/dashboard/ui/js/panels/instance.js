// One live panel: the context a panel type reads, and the drawing built from it.
// The grid and the focus view both make panels here, so a panel is the same
// thing wherever it is shown.
import { build } from './registry.js';
import { uid } from './common.js';
import { effectiveOptions } from '../layout.js';
import { parse, scope } from '../query.js';

// `types` is the catalog by type name. `focus` is true for the one panel shown
// large in the focus view, so a type may use the room (it never has to).
export function instantiate(p, host, { types, boardId, env, focus = false }) {
  const pt = types.get(p.type);
  const options = effectiveOptions(p, pt);
  const terms = parse(p.query).terms;
  // A panel that chooses its own query keeps its own choice of database query too.
  const own = { pin: null };
  const chooses = !!pt && pt.followsDeploy && terms.some((t) => ['deploy', 'app', 'namespace', 'state', 'grade', 'query', 'statement', 'table'].includes(t.field));
  const actions = chooses ? { ...env.actions, pickUnit: (key) => { own.pin = key; env.actions.rerender(); } } : env.actions;
  const ctx = { uid: uid(), id: p.id, type: p.type, board: boardId, explicitTitle: p.title || '', options, ui: env.ui, actions, catalog: pt, focus, query: terms };
  const api = build(p.type, host, ctx);
  if (!terms.length) return { ctx, api };
  const follows = !!pt && !!pt.followsDeploy;
  return { ctx, api: { ...api, render: (view) => api.render(scope(view, terms, { pin: own.pin, policy: options.unit || 'first', follows, wantsSeries: p.type === 'canary-chart' })) } };
}
