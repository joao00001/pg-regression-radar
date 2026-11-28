/*
 * Diagram theme. Material renders each Mermaid block in a closed shadow root,
 * so page CSS cannot reach it; this wraps `mermaid.initialize` (Material calls
 * it with its own theme) to append the project's diagram classes. Colors are
 * CSS variables, so light/dark switching needs no re-render. In a diagram,
 * only class names are used (`A["..."]:::radar`): `classDef ...var(--x)` is
 * not possible because Mermaid reads `--` as an arrow.
 *
 *   radar  a pg-regression-radar component        red border
 *   ext    a system outside the radar             dashed, no fill
 *   k8s    a Kubernetes object                    blue border
 *   gate   a decision or a neutral step           gray border
 *   out    a result (alert, PerformanceRegression) soft red fill
 *
 * Loaded by overrides/main.html only on pages with a diagram.
 */
(function () {
  "use strict";
  if (!window.mermaid || !window.mermaid.initialize) return;

  var shapes = ["rect", "polygon", "circle", "ellipse", "path"];
  function box(name, rule) {
    return shapes.map(function (s) { return ".node." + name + " " + s; }).join(",") + "{" + rule + "}";
  }

  var CSS =
    ".node .label,.nodeLabel{font-size:14px}.edgeLabel{font-size:12px}" +
    ".cluster rect{fill:transparent!important;stroke:var(--pgrr-dg-line)!important;rx:10;ry:10}" +
    ".cluster .nodeLabel,.cluster-label span{color:var(--md-default-fg-color--light)!important;font-size:12px;letter-spacing:.04em}" +
    box("radar", "fill:var(--pgrr-dg-card);stroke:var(--pgrr-accent);stroke-width:1.5px") +
    box("ext", "fill:transparent;stroke:var(--md-default-fg-color--light);stroke-dasharray:4 3") +
    box("k8s", "fill:var(--pgrr-dg-k8s-bg);stroke:var(--pgrr-dg-k8s);stroke-width:1.5px") +
    box("gate", "fill:transparent;stroke:var(--md-default-fg-color--light)") +
    box("out", "fill:var(--pgrr-accent-bg);stroke:var(--pgrr-accent-border)") +
    ".actor-line,.messageLine0,.messageLine1{stroke:var(--md-default-fg-color--lighter)}" +
    "rect.actor{fill:var(--pgrr-dg-card);stroke:var(--pgrr-dg-line)}" +
    "text.actor>tspan{fill:var(--md-default-fg-color)}" +
    ".statediagram-state rect,.node.statediagram-state rect{fill:var(--pgrr-dg-card);stroke:var(--pgrr-dg-line)}";

  var init = window.mermaid.initialize.bind(window.mermaid);
  window.mermaid.initialize = function (cfg) {
    cfg = cfg || {};
    cfg.themeCSS = (cfg.themeCSS || "") + CSS;
    cfg.flowchart = { nodeSpacing: 28, rankSpacing: 44, padding: 10, curve: "basis", htmlLabels: true };
    return init(cfg);
  };
})();
