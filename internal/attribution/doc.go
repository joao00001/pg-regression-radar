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

// Package attribution decides whether a deploy caused a performance
// regression, and how much evidence backs that claim, before anything acts on
// it.
//
// The correlation engine (internal/correlation) answers "did this query's
// latency change around the deploy?". That is necessary for a rollback but not
// sufficient: a latency shift can come from fresh statistics, a load change, a
// query that was rewritten, another deploy, or the deploy itself, and rolling
// back only helps in the last case. This package answers the harder question,
// "would the latency have stayed where it was without this deploy?", with one
// mechanism instead of one rule per false-positive class:
//
//   - Counterfactual. Every unit (a query, identified by a stable fingerprint)
//     gets a prediction of its own latency without the deploy, from the
//     strongest source available: a concurrent control arm (canary against
//     stable), a synthetic control built from units the deploy did not touch,
//     or its own history. Whatever the covariates explain (load, cache
//     warm-up, fresh statistics that move sibling queries too) is removed by
//     construction; there is no list of exceptions to maintain.
//   - Sequential inference. The deviation from the counterfactual is tested
//     with an e-process, so it can be re-evaluated on every scrape without
//     inflating the false-alarm rate, and across hundreds of queries with the
//     e-BH procedure, which holds under arbitrary dependence.
//   - Evidence grades. Each verdict carries the grade of the counterfactual
//     behind it (A concurrent control, B synthetic control, C history only,
//     D none). The action policy reads the grade: only A and B can justify
//     more than an alert, only A can justify a rollback, and D never acts.
//   - Closed loop. After an action, the same machinery checks whether the
//     metric recovered. If it did not, the deploy was not the cause, the
//     attribution is counted as wrong and a circuit breaker stops automatic
//     actions until a human looks.
//
// The package depends only on the standard library, like internal/pgstat, so
// it can run in the operator, the CLI and the WebAssembly playground alike.
//
// # References
//
// The design follows published methods rather than invented ones:
//
//   - K. H. Brodersen et al., "Inferring causal impact using Bayesian
//     structural time-series models", Annals of Applied Statistics 9(1), 2015:
//     the counterfactual forecast from covariates plus a local level.
//   - A. Abadie, A. Diamond, J. Hainmueller, "Synthetic control methods for
//     comparative case studies", JASA 105(490), 2010: donors the intervention
//     did not touch, and placebo (permutation) inference across donors.
//   - J. Durbin and S. J. Koopman, "Time Series Analysis by State Space
//     Methods", 2nd ed., 2012, section 6.2.4: regression and intervention
//     effects estimated inside the Kalman filter, which is what makes the
//     sequential test below exact under the model. G. E. P. Box and G. C. Tiao,
//     "Intervention analysis with applications to economic and environmental
//     problems", JASA 70(349), 1975, for the step intervention.
//   - R. Johari, L. Pekelis, D. J. Walsh, "Always valid inference: bringing
//     sequential analysis to A/B testing", arXiv:1512.04922 (mixture sequential
//     probability ratio test), and M. Lindon, C. Sanden, V. Shirikian, "Rapid
//     regression detection in software deployments through sequential testing",
//     KDD 2022 (the same idea applied to canary analysis at Netflix).
//   - A. Ramdas, P. Grünwald, V. Vovk, G. Shafer, "Game-theoretic statistics and
//     safe anytime-valid inference", Statistical Science 38(4), 2023: e-values
//     and e-processes. S. R. Howard, A. Ramdas, J. McAuliffe, J. Sekhon,
//     "Time-uniform, nonparametric, nonasymptotic confidence sequences", Annals
//     of Statistics 49(2), 2021: the confidence sequence reported with each
//     effect.
//   - R. Wang and A. Ramdas, "False discovery rate control with e-values",
//     JRSS-B 84(3), 2022: the e-BH procedure used across queries.
//   - D. Daly et al., "The use of change point detection to identify software
//     performance regressions in a continuous integration system", ICPE 2020,
//     and H. Fleming et al., "Hunter", ICPE 2023: change-point detection as the
//     weakest, history-only fallback (internal/correlation).
//   - Y. Li et al., "Gandalf: an intelligent, end-to-end analytics service for
//     safe deployment in large-scale cloud infrastructure", NSDI 2020:
//     attributing a fault to one of several concurrent rollouts.
//   - Microsoft SQL Server automatic plan correction ("force last good plan"):
//     act, verify against the same metric, revert the action if it did not
//     help.
package attribution
