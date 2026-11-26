// Panel type -> the function that builds it. The catalog (sizes, options, docs)
// comes from the Go side; this is only the drawing. A type the catalog has and
// this table lacks shows a placeholder, so a newer manager's layout still loads.
import { summaryPanel } from './summary.js';
import { radarStatePanel } from './strip.js';
import { deployCardsPanel } from './cards.js';
import { deployHeadPanel, figuresPanel, whyPanel, evidencePanel, ledgerPanel } from './detail.js';
import { canaryChartPanel } from './canary.js';
import { ratioChartPanel } from './ratio.js';
import { evaluationsPanel } from './evaluations.js';
import { decisionFeedPanel } from './feed.js';
import { safetyPanel, sourceHealthPanel } from './health.js';
import { h } from '../dom.js';

export const PANELS = {
  'summary': summaryPanel,
  'radar-state': radarStatePanel,
  'deploy-cards': deployCardsPanel,
  'deploy-head': deployHeadPanel,
  'canary-chart': canaryChartPanel,
  'figures': figuresPanel,
  'why': whyPanel,
  'evidence-table': evidencePanel,
  'ratio-chart': ratioChartPanel,
  'evaluations': evaluationsPanel,
  'ledger': ledgerPanel,
  'decision-feed': decisionFeedPanel,
  'safety': safetyPanel,
  'source-health': sourceHealthPanel,
};

export function build(type, host, ctx) {
  const make = PANELS[type];
  if (make) return make(host, ctx);
  host.append(h('p', { class: 'empty', text: `This dashboard has a "${type}" panel this version cannot draw. Use the dashboard command of the same release as the manager.` }));
  return { render() {} };
}
