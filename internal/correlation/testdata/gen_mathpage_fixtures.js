// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Regenerates mathpage_fixtures.json, the series that
// mathpage_test.go feeds to the real engine and compares with what the
// "Our math" page computes (docs/assets/math/math-core.js):
//
//   node internal/correlation/testdata/gen_mathpage_fixtures.js
//
// Only needed when math-core.js changes on purpose; the test fails if the two
// implementations ever disagree.
const fs = require("fs");
const path = require("path");
const M = require(path.join(__dirname, "../../../docs/assets/math/math-core.js"));

const r9 = (x) => Math.round(x * 1e9) / 1e9;
const rng = M.mulberry32(12345);
const pick = (a, b) => a + (b - a) * rng();
const fixtures = [];
for (let i = 0; i < 48; i++) {
  const o = {
    windowMin: [10, 20, 30][i % 3],
    scrapeSec: [30, 60][i % 2],
    base: 10,
    size: [0, 0.15, 0.3, 0.6, 1.5, 3, 0.5][i % 7],
    lagMin: pick(-8, 10),
    cv: pick(0.3, 2.5),
    callsPerScrape: [20, 40, 200, 8, 100][i % 5],
    phi: [0, 0, 0.5, 0.8][i % 4],
    seed: 1000 + i,
  };
  const samples = M.generate(o).map((s) => ({ t: r9(s.t), calls: s.calls, totalMs: r9(s.totalMs) }));
  const cfg = {
    latencyThreshold: 0.2,
    pThreshold: [0.05, 0.01][i % 2],
    minSamples: 10,
    minExecutions: [100, 100, 500][i % 3],
    toleranceMin: Math.max(2, o.windowMin * 0.2),
  };
  const r = M.analyse(samples, cfg);
  fixtures.push({
    windowMin: o.windowMin,
    cfg,
    samples,
    expected: {
      verdict: r.verdict,
      factor: r.factor,
      p: r.welch ? r.welch.p : null,
      changeAtMin: r.changeAtMin,
    },
  });
}
const tgrid = [];
for (const df of [2.5, 5, 9.7, 30, 118.4, 400]) for (const t of [0.3, 1, 2, 2.6, 4, 9]) tgrid.push({ df, t, p: M.tDistPValue(t, df) });
fs.writeFileSync(path.join(__dirname, "mathpage_fixtures.json"), JSON.stringify({ fixtures, tgrid }));
const n = {};
fixtures.forEach((f) => (n[f.expected.verdict] = (n[f.expected.verdict] || 0) + 1));
console.log(n);
