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

package correlation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// MultipleTesting selects how an analysis pass controls the false-discovery
// rate across the queries it tests.
//
// A pass evaluates every tracked query (up to the top 500 that
// pg_stat_statements keeps), and periodic detection repeats that on every
// tick. Each query is judged against PValueThreshold on its own, so even when
// every individual test is honest, the chance that at least one of hundreds of
// unchanged queries is flagged grows with the number of queries. The modes
// below bound the expected share of false detections among the queries a pass
// reports instead.
type MultipleTesting string

const (
	// MultipleTestingNone judges every query on its own p-value against
	// PValueThreshold. It is the default and the behaviour before the other
	// modes existed.
	MultipleTestingNone MultipleTesting = "none"
	// MultipleTestingBH applies the Benjamini-Hochberg step-up procedure
	// across the queries of one pass at level PValueThreshold. It controls the
	// false-discovery rate when the p-values are independent or positively
	// dependent, which is the usual situation for queries sharing one server's
	// load (Benjamini and Yekutieli, 2001).
	MultipleTestingBH MultipleTesting = "bh"
	// MultipleTestingBY applies the Benjamini-Yekutieli procedure: BH with the
	// level divided by the harmonic sum 1 + 1/2 + ... + 1/m. It controls the
	// false-discovery rate under any dependence between the p-values, at a
	// real cost in sensitivity.
	MultipleTestingBY MultipleTesting = "by"
)

// ParseMultipleTesting maps a flag or spec value to a MultipleTesting mode.
// The empty string is MultipleTestingNone.
func ParseMultipleTesting(s string) (MultipleTesting, error) {
	switch m := MultipleTesting(strings.ToLower(strings.TrimSpace(s))); m {
	case "", MultipleTestingNone:
		return MultipleTestingNone, nil
	case MultipleTestingBH, MultipleTestingBY:
		return m, nil
	default:
		return "", fmt.Errorf("unknown multiple-testing mode %q (want none, bh or by)", s)
	}
}

// benjaminiHochberg returns, for each p-value, whether the step-up procedure
// at level q rejects it: the largest k with p(k) <= q*k/m is found and the k
// smallest p-values are rejected. m is the number of hypotheses the level is
// divided over, which may exceed len(p) when some hypotheses are already
// known not to be rejected and have been left out.
func benjaminiHochberg(p []float64, q float64, m int) []bool {
	order := make([]int, len(p))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return p[order[a]] < p[order[b]] })
	k := 0
	for rank, i := range order {
		if p[i] <= q*float64(rank+1)/float64(m) {
			k = rank + 1
		}
	}
	rejected := make([]bool, len(p))
	for _, i := range order[:k] {
		rejected[i] = true
	}
	return rejected
}

// harmonic returns 1 + 1/2 + ... + 1/m.
func harmonic(m int) float64 {
	var h float64
	for i := 1; i <= m; i++ {
		h += 1 / float64(i)
	}
	return h
}

// controlFalseDiscoveries applies the configured multiple-testing procedure
// to one pass's results and returns them with the detections that do not
// survive it demoted to NoRegression.
//
// The family is every query the pass could judge, that is, every result that
// is not InsufficientData: a query that failed a gate (too small a change, no
// change point, a change point away from the deploy) is a test that did not
// reject, and counts toward m. Only the queries already marked Detected can be
// rejected, and the others enter the step-up with p = 1, so the procedure can
// only remove detections, never add one. Counting the unrejected queries in m
// is what makes the correction a statement about the whole pass, and it is
// also what makes it strict: with 500 queries the smallest p-value must be
// below q/500 to survive.
func (e *Engine) controlFalseDiscoveries(results []v1alpha1.PerformanceRegression) []v1alpha1.PerformanceRegression {
	mode := e.cfg.MultipleTesting
	if mode != MultipleTestingBH && mode != MultipleTestingBY {
		return results
	}

	m, detected := 0, 0
	for _, r := range results {
		if r.Status == v1alpha1.StatusInsufficientData {
			continue
		}
		m++
		if r.Status == v1alpha1.StatusDetected {
			detected++
		}
	}
	if detected == 0 {
		return results
	}

	q := e.cfg.PValueThreshold
	if mode == MultipleTestingBY {
		q /= harmonic(m)
	}

	idx := make([]int, 0, detected)
	ps := make([]float64, 0, detected)
	for i, r := range results {
		if r.Status != v1alpha1.StatusDetected {
			continue
		}
		p := 1.0
		if r.PValue != nil {
			p = *r.PValue
		}
		idx = append(idx, i)
		ps = append(ps, p)
	}

	demoted := 0
	for j, keep := range benjaminiHochberg(ps, q, m) {
		if keep {
			continue
		}
		r := &results[idx[j]]
		e.logger.Debug("correlation: detection did not survive multiple-testing control",
			"query_id", r.QueryID, "p_value", ps[j], "mode", string(mode), "queries_tested", m)
		r.Status = v1alpha1.StatusNoRegression
		r.ConfidenceScore = 0
		r.DetectedChangeAt = time.Time{}
		demoted++
	}
	if demoted > 0 {
		e.logger.Info("correlation: multiple-testing control removed detections",
			"mode", string(mode), "queries_tested", m, "detected_before", detected, "removed", demoted, "level", q)
	}
	return results
}
