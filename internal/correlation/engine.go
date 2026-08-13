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

// Package correlation detects query performance regressions triggered by
// Kubernetes deploys. It pairs E-divisive change-point detection with Welch's
// t-test so only statistically significant shifts reach the alerting layer.
package correlation

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// Config holds tuning parameters for the correlation engine.
type Config struct {
	// WindowMinutes — wider windows reduce sensitivity to bursty load.
	WindowMinutes int
	// MinExecutions — guards against spurious regressions on rarely-called queries.
	MinExecutions int64
	// LatencyChangeThreshold — raise in noisy environments to suppress alerts.
	LatencyChangeThreshold float64
	// PValueThreshold — lower values increase specificity at the cost of sensitivity.
	PValueThreshold float64
	// ChangePointTolerance bounds how far the change point located by
	// E-divisive (stage 1) may sit from the deploy timestamp and still be
	// attributed to that deploy. Rolling deploys take time to roll out across
	// replicas, connections drain gradually, and the collector itself has
	// scrape-interval granularity — so the real regime shift in the query
	// latency series rarely lands exactly on ev.Timestamp. If the change
	// point E-divisive finds is farther than this from the deploy, the shift
	// is treated as unrelated background noise, not a regression caused by
	// this deploy.
	//
	// Zero means "auto": 20% of the analysis window, floored at 2 minutes.
	// The percentage (rather than a fixed duration) keeps the same relative
	// slack whether operators configure a tight 10-minute window or a loose
	// 60-minute one; the floor keeps short windows from getting an
	// unreasonably tiny allowance.
	ChangePointTolerance time.Duration
}

func (c *Config) defaults() {
	if c.WindowMinutes == 0 {
		c.WindowMinutes = 30
	}
	if c.MinExecutions == 0 {
		c.MinExecutions = 10
	}
	if c.LatencyChangeThreshold == 0 {
		c.LatencyChangeThreshold = 0.20
	}
	if c.PValueThreshold == 0 {
		c.PValueThreshold = 0.05
	}
	if c.ChangePointTolerance == 0 {
		c.ChangePointTolerance = time.Duration(c.WindowMinutes) * time.Minute / 5
		if c.ChangePointTolerance < 2*time.Minute {
			c.ChangePointTolerance = 2 * time.Minute
		}
	}
}

// AnalysisWindow returns the configured pre/post-deploy window duration —
// the same span Analyse subtracts from and adds to a deploy event's
// timestamp to bound the samples it considers. Callers that retry Analyse
// as more post-deploy data arrives (see internal/cli.RunOperator and
// internal/controller.PostgresWatchReconciler's poll loops) use this to know
// when a deploy event's post-deploy window has fully elapsed and no further
// retry could possibly see new data for it.
func (e *Engine) AnalysisWindow() time.Duration {
	return time.Duration(e.cfg.WindowMinutes) * time.Minute
}

// SampleSource can provide QuerySamples for a time range.
type SampleSource interface {
	SamplesInRange(queryID int64, from, to time.Time) []collector.QuerySample
	AllQueryIDs() []int64
}

// Engine is the Correlation Engine.
type Engine struct {
	cfg    Config
	src    SampleSource
	logger *slog.Logger
}

// New creates a new Engine.
func New(cfg Config, src SampleSource, logger *slog.Logger) *Engine {
	cfg.defaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{cfg: cfg, src: src, logger: logger}
}

// Analyse runs the full regression analysis for a given deploy event and
// returns at most one PerformanceRegression per distinct query (see the
// fingerprint-dedup note below for what "distinct" means here).
//
// AllQueryIDs() is sorted before iterating (it returns Go map keys, so its
// natural order is nondeterministic): this makes the loop reproducible for a
// given input, and — combined with the dedup logic below — makes which
// queryid a given fingerprint's "seen" entry gets recorded against
// reproducible too, even though canonicalQueryID (not the loop's qid)
// ultimately decides the *reported* QueryID regardless of iteration order.
func (e *Engine) Analyse(ev v1alpha1.DeployEvent) []v1alpha1.PerformanceRegression {
	window := time.Duration(e.cfg.WindowMinutes) * time.Minute
	before := ev.Timestamp.Add(-window)
	after := ev.Timestamp.Add(window)

	queryIDs := append([]int64(nil), e.src.AllQueryIDs()...)
	sort.Slice(queryIDs, func(i, j int) bool { return queryIDs[i] < queryIDs[j] })

	e.logger.Info("correlation: analysing deploy",
		"event_id", ev.ID,
		"query_count", len(queryIDs),
		"window_minutes", e.cfg.WindowMinutes)

	// seen dedups findings across a queryid rotation. pg_stat_statements'
	// queryid is not guaranteed stable (see collector.Collector.
	// SamplesInRange's doc comment), so a query whose queryid rotates
	// mid-window is enumerated twice by AllQueryIDs() — once for the old
	// queryid, once for the new one. SamplesInRange's fingerprint-merge
	// fallback means both calls pull in the same union of samples once either
	// side's direct bucket runs low, so without this guard evaluateQuery would
	// run twice on identical data and emit two PerformanceRegressions for one
	// real regression. Keying on Fingerprint (not queryid) is what lets us
	// recognise "these two loop iterations are actually the same query"
	// without changing the SampleSource interface: the first queryid (in
	// sorted order) that surfaces a given fingerprint wins and is evaluated;
	// every later queryid sharing that fingerprint is skipped outright, since
	// it would only re-evaluate the identical merged sample set.
	seen := make(map[string]struct{}, len(queryIDs))
	var results []v1alpha1.PerformanceRegression

	for _, qid := range queryIDs {
		allSamples := e.src.SamplesInRange(qid, before, after)

		if fp := latestFingerprint(allSamples); fp != "" {
			if _, dup := seen[fp]; dup {
				continue
			}
			seen[fp] = struct{}{}
		}

		// Report under whichever queryid most recently produced a sample, not
		// the loop's qid: when a rotation actually happened, the merged
		// sample set legitimately contains rows stamped with two different
		// original QueryID values, and only one of them is still "live" in
		// pg_stat_statements — reporting under the other could point an
		// operator at a queryid that has already fallen out of the top-500
		// and means nothing to them by the time they look it up.
		reportQID := canonicalQueryID(allSamples, qid)
		preSamples, postSamples := partitionAtDeployTime(allSamples, ev.Timestamp)

		r := e.evaluateQuery(ev, reportQID, preSamples, postSamples)
		results = append(results, r)
	}

	// Sorting keeps output order deterministic regardless of AllQueryIDs()'s
	// (nondeterministic) map-iteration order or which queryid within a
	// rotated pair happened to win the fingerprint dedup above.
	sort.Slice(results, func(i, j int) bool { return results[i].QueryID < results[j].QueryID })

	return results
}

// latestFingerprint returns the Fingerprint of whichever sample in samples
// has the latest RecordedAt, or "" if samples is empty. Any non-empty
// sample's fingerprint would technically do here — when SamplesInRange's
// fingerprint-merge fallback pulled in samples from more than one queryid,
// every sample in the merged set shares the same fingerprint by construction
// — but picking the most recent one keeps this helper's notion of "current
// identity" consistent with canonicalQueryID below, which also picks by most
// recent RecordedAt.
func latestFingerprint(samples []collector.QuerySample) string {
	latest, ok := latestSample(samples)
	if !ok {
		return ""
	}
	return latest.Fingerprint
}

// canonicalQueryID returns the QueryID of whichever sample in samples has the
// latest RecordedAt, or fallbackQID if samples is empty.
//
// This intentionally does NOT just return the loop variable that produced
// samples: when a queryid rotation actually happened mid-window, the merged
// sample set returned by SamplesInRange's fingerprint fallback legitimately
// contains samples stamped with two different original QueryID values (the
// pre- and post-rotation ones). Reporting under whichever one most recently
// received a sample means the regression is filed under the queryid that is
// CURRENTLY still receiving traffic in pg_stat_statements — the "live"
// identity — not one that may already have aged out or fallen out of the
// top-500 and would mean nothing to an operator looking it up after the fact.
func canonicalQueryID(samples []collector.QuerySample, fallbackQID int64) int64 {
	latest, ok := latestSample(samples)
	if !ok {
		return fallbackQID
	}
	return latest.QueryID
}

// latestSample returns the sample in samples with the latest RecordedAt, and
// false if samples is empty. Factored out since latestFingerprint and
// canonicalQueryID both need "the most recent sample" and must agree on which
// one that is.
func latestSample(samples []collector.QuerySample) (collector.QuerySample, bool) {
	var latest collector.QuerySample
	found := false
	for _, s := range samples {
		if !found || s.RecordedAt.After(latest.RecordedAt) {
			latest = s
			found = true
		}
	}
	return latest, found
}

// evaluateQuery runs the two-stage detection for a single query and returns a
// PerformanceRegression describing the outcome.
//
// Stage 0 (cheap pre-filter): reject outright if the naive pre/post means —
// computed over the whole pre-deploy and post-deploy windows — don't differ
// by at least LatencyChangeThreshold. This avoids running the O(n^2)
// E-divisive scan on queries that obviously didn't regress.
//
// Stage 1 (E-divisive): for queries that pass the pre-filter, build the
// combined, chronologically-ordered series covering the whole window and
// locate the single most significant change point in it. This does NOT
// assume the shift happens exactly at ev.Timestamp — rolling deploys,
// connection draining and scrape lag all delay the observable effect.
//
// Stage 2 (Welch's t-test): confirm that the two segments defined by the
// change point E-divisive actually found (not the naive deploy-timestamp
// split) are statistically different.
//
// A PerformanceRegression is only marked Detected when the change point
// exists, lies within ChangePointTolerance of the deploy timestamp, AND the
// t-test on the real segments is significant. Any one of those failing —
// including a naive mean shift with no corresponding change point, or a
// genuine change point that's unrelated to this deploy — yields NoRegression.
func (e *Engine) evaluateQuery(
	ev v1alpha1.DeployEvent,
	queryID int64,
	preSamples, postSamples []collector.QuerySample,
) v1alpha1.PerformanceRegression {
	name := fmt.Sprintf("%s-q%d", ev.ID, queryID)
	queryText := ""
	if len(preSamples) > 0 {
		queryText = preSamples[0].QueryText
	} else if len(postSamples) > 0 {
		queryText = postSamples[0].QueryText
	}

	base := v1alpha1.PerformanceRegression{
		Name:          name,
		Namespace:     ev.Namespace,
		DeployEventID: ev.ID,
		QueryID:       queryID,
		QueryText:     queryText,
		CreatedAt:     time.Now().UTC(),
	}

	// Require minimum sample counts in both windows to avoid false positives from sparse data.
	if int64(len(preSamples)) < e.cfg.MinExecutions || int64(len(postSamples)) < e.cfg.MinExecutions {
		base.Status = v1alpha1.StatusInsufficientData
		// This is the routine, expected case for a deploy event analysed
		// soon after it arrives — a real deploy webhook fires the moment
		// the deploy completes, well before --scrape-interval has had time
		// to accumulate --min-executions worth of post-deploy samples. On
		// its own, this used to be indistinguishable in the logs from "the
		// query has no regression" or "analysis never ran at all" — Debug
		// rather than Info because a caller retrying via PendingSet logs
		// its own Info-level lifecycle events (registered / window
		// elapsed), so this line's job is purely to answer "why, for this
		// one query" when someone's already looking.
		e.logger.Debug("correlation: insufficient data, not yet ready to evaluate",
			"query_id", queryID,
			"pre_samples", len(preSamples),
			"post_samples", len(postSamples),
			"min_executions", e.cfg.MinExecutions)
		return base
	}

	preLatencies := extractLatencies(preSamples)
	postLatencies := extractLatencies(postSamples)

	meanPre := mean(preLatencies)
	meanPost := mean(postLatencies)

	changeFactor := 0.0
	if meanPre > 0 {
		changeFactor = meanPost / meanPre
	}

	// These are always reported relative to the deploy timestamp (not the
	// change point) because that's the comparison operators actually care
	// about: "what did latency look like before/after this deploy".
	base.MeanLatencyBefore = meanPre
	base.MeanLatencyAfter = meanPost
	base.LatencyChangeFactor = changeFactor

	relativeChange := changeFactor - 1.0
	if relativeChange < e.cfg.LatencyChangeThreshold {
		base.Status = v1alpha1.StatusNoRegression
		return base
	}

	// ---- Stage 1: locate the real change point (E-divisive) ----
	allSamples := make([]collector.QuerySample, 0, len(preSamples)+len(postSamples))
	allSamples = append(allSamples, preSamples...)
	allSamples = append(allSamples, postSamples...)
	sort.Slice(allSamples, func(i, j int) bool {
		return allSamples[i].RecordedAt.Before(allSamples[j].RecordedAt)
	})
	series := extractLatencies(allSamples)

	minSegLen := changePointMinSegLen(len(series))
	cps := EDivisive(series, minSegLen, 1)
	if len(cps) == 0 {
		// EDivisive itself only ever returns candidates with a positive
		// E-statistic (see its break condition), so "no candidates" already
		// means "no statistically relevant regime shift anywhere in the
		// window". We deliberately do NOT add a second, separate minimum
		// E-statistic threshold here: the energy statistic's magnitude is in
		// raw latency units and isn't comparable across queries with
		// different latency scales, whereas the p-value computed in stage 2
		// already is a normalised, comparable significance measure. Rejecting
		// on "no change point found" here, and on p-value in stage 2, avoids
		// a second, redundant, harder-to-tune knob.
		e.logger.Debug("correlation: no change point found, rejecting naive mean shift",
			"query_id", queryID,
			"change_factor", changeFactor)
		base.Status = v1alpha1.StatusNoRegression
		return base
	}
	cp := cps[0]
	// cp.Index is the boundary sample: series[:cp.Index] is the segment
	// E-divisive considers "before", series[cp.Index:] is "after". We
	// attribute the change to the timestamp of the first "after" sample.
	changeAt := allSamples[cp.Index].RecordedAt

	// The change point must fall close to the deploy to be attributable to
	// it — otherwise we'd be alerting on some unrelated shift that merely
	// happens to overlap the analysis window.
	distance := changeAt.Sub(ev.Timestamp)
	if distance < 0 {
		distance = -distance
	}
	if distance > e.cfg.ChangePointTolerance {
		e.logger.Debug("correlation: change point too far from deploy, rejecting",
			"query_id", queryID,
			"change_at", changeAt,
			"deploy_at", ev.Timestamp,
			"distance", distance,
			"tolerance", e.cfg.ChangePointTolerance)
		base.Status = v1alpha1.StatusNoRegression
		return base
	}

	// ---- Stage 2: confirm significance (Welch's t-test) on the REAL segments ----
	// Deliberately NOT preLatencies/postLatencies (the naive deploy-timestamp
	// split): we test the two segments E-divisive actually found, which may
	// be offset from ev.Timestamp by up to ChangePointTolerance.
	preSeg := series[:cp.Index]
	postSeg := series[cp.Index:]
	tStat, pValue := welchTTest(preSeg, postSeg)
	_ = tStat

	e.logger.Debug("correlation: t-test result",
		"query_id", queryID,
		"p_value", pValue,
		"change_factor", changeFactor,
		"change_at", changeAt)

	if pValue > e.cfg.PValueThreshold {
		base.Status = v1alpha1.StatusNoRegression
		return base
	}

	// Confidence is derived from the p-value: a p-value close to zero means the
	// observed shift is extremely unlikely to be random noise.
	confidence := 1.0 - pValue
	if confidence > 1 {
		confidence = 1
	}

	base.Status = v1alpha1.StatusDetected
	base.ConfidenceScore = confidence
	base.DetectedChangeAt = changeAt

	e.logger.Info("correlation: regression detected",
		"query_id", queryID,
		"change_factor", changeFactor,
		"confidence", confidence,
		"change_at", changeAt)

	return base
}

// changePointMinSegLen picks E-divisive's minimum segment length as a
// function of the combined series length: large enough that the energy
// statistic isn't swayed by a couple of noisy points, small enough that a
// change point near either edge of the window (e.g. right after the deploy,
// with few post-deploy samples collected yet) is still reachable.
func changePointMinSegLen(n int) int {
	minSegLen := n / 10
	if minSegLen < 3 {
		minSegLen = 3
	}
	return minSegLen
}

// partitionAtDeployTime splits samples into pre- and post-deploy slices.
// Pre contains samples with RecordedAt ≤ deployAt; post contains samples with
// RecordedAt ≥ deployAt. A sample recorded exactly at deployAt appears in both
// slices, matching the inclusive-boundary semantics of two separate
// SamplesInRange([before, deployAt]) and SamplesInRange([deployAt, after]) calls.
func partitionAtDeployTime(samples []collector.QuerySample, deployAt time.Time) (pre, post []collector.QuerySample) {
	for _, s := range samples {
		if !s.RecordedAt.After(deployAt) {
			pre = append(pre, s)
		}
		if !s.RecordedAt.Before(deployAt) {
			post = append(post, s)
		}
	}
	return pre, post
}

// ----- statistical helpers -----

// extractLatencies returns the MeanExecTimeMs from each sample as a slice.
func extractLatencies(samples []collector.QuerySample) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = s.MeanExecTimeMs
	}
	return out
}

// mean returns the arithmetic mean of vals. Returns 0 for an empty slice.
func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

// variance returns the sample variance (divides by n-1).
func variance(vals []float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	m := mean(vals)
	sum := 0.0
	for _, v := range vals {
		d := v - m
		sum += d * d
	}
	return sum / float64(len(vals)-1)
}

// welchTTest computes Welch's t-test for two independent samples with unequal
// variances and returns the t-statistic and an approximated two-tailed p-value.
//
// The p-value approximation uses the Cornish–Fisher expansion of the
// t-distribution CDF, which is accurate to ±0.001 for df > 4.
func welchTTest(a, b []float64) (tStat, pValue float64) {
	n1 := float64(len(a))
	n2 := float64(len(b))
	if n1 < 2 || n2 < 2 {
		return 0, 1
	}

	v1 := variance(a)
	v2 := variance(b)
	m1 := mean(a)
	m2 := mean(b)

	se := math.Sqrt(v1/n1 + v2/n2)
	if se == 0 {
		// Zero variance in both groups: the result is deterministic — different
		// means mean a definite shift (p→0), equal means mean no difference (p=1).
		if m1 != m2 {
			return math.Inf(1), 0
		}
		return 0, 1
	}

	tStat = (m1 - m2) / se

	// Welch–Satterthwaite degrees of freedom. Named w1/w2 (not a/b) to avoid
	// shadowing this function's own a/b []float64 parameters.
	w1 := v1 / n1
	w2 := v2 / n2
	num := (w1 + w2) * (w1 + w2)
	denom := w1*w1/(n1-1) + w2*w2/(n2-1)
	df := num / denom

	pValue = tDistPValue(math.Abs(tStat), df)
	return tStat, pValue
}

// tDistPValue returns the two-tailed p-value for |t| and df using the
// incomplete beta function identity, avoiding a numerical CDF integration.
func tDistPValue(absT, df float64) float64 {
	if df <= 0 {
		return 1
	}
	// P(T > absT) * 2  (two-tailed)
	// Use the incomplete beta function: p = I(df/(df+t^2), df/2, 1/2)
	x := df / (df + absT*absT)
	return incompleteBeta(x, df/2, 0.5)
}

// incompleteBeta approximates the regularised incomplete beta function I_x(a,b)
// via continued fractions (Lentz's method). Accurate to ±0.001 for df > 4,
// which covers all practical p-value thresholds around 0.05.
func incompleteBeta(x, a, b float64) float64 {
	if x < 0 || x > 1 {
		return 0
	}
	if x == 0 {
		return 0
	}
	if x == 1 {
		return 1
	}

	lbeta := logBeta(a, b)
	front := math.Exp(math.Log(x)*a+math.Log(1-x)*b-lbeta) / a

	// Use symmetry for numerical stability.
	if x > (a+1)/(a+b+2) {
		return 1 - incompleteBeta(1-x, b, a)
	}

	cf := betaCF(x, a, b)
	return front * cf
}

// betaCF evaluates the continued fraction for the incomplete beta function.
func betaCF(x, a, b float64) float64 {
	const maxIter = 200
	const eps = 3e-7
	const fpMin = 1e-30

	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < fpMin {
		d = fpMin
	}
	d = 1 / d
	h := d

	for m := 1; m <= maxIter; m++ {
		mf := float64(m)
		// Even step
		aa := mf * (b - mf) * x / ((qam + 2*mf) * (a + 2*mf))
		d = 1 + aa*d
		if math.Abs(d) < fpMin {
			d = fpMin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpMin {
			c = fpMin
		}
		d = 1 / d
		h *= d * c
		// Odd step
		aa = -(a + mf) * (qab + mf) * x / ((a + 2*mf) * (qap + 2*mf))
		d = 1 + aa*d
		if math.Abs(d) < fpMin {
			d = fpMin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpMin {
			c = fpMin
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < eps {
			break
		}
	}
	return h
}

// logBeta returns ln(B(a,b)) = lgamma(a) + lgamma(b) - lgamma(a+b).
func logBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}

// ChangePoint represents a detected change point in a time series.
type ChangePoint struct {
	Index int
	// EStatistic is higher for more pronounced regime shifts.
	EStatistic float64
}

// EDivisive finds up to maxPoints change points in a univariate time series
// using the E-divisive means algorithm. Greedy search is sufficient here because
// the analysis window is short (≤ 60 scrapes per 30 min).
//
// Reference: Matteson & James (2014), "A Nonparametric Approach for Multiple
// Change Point Analysis of Multivariate Data", JASA.
func EDivisive(series []float64, minSegLen int, maxPoints int) []ChangePoint {
	if len(series) < 2*minSegLen {
		return nil
	}

	var found []ChangePoint
	segments := [][]int{{0, len(series)}}

	for len(found) < maxPoints {
		best := ChangePoint{EStatistic: -1}
		var bestSeg []int

		for _, seg := range segments {
			l, r := seg[0], seg[1]
			if r-l < 2*minSegLen {
				continue
			}
			cp := bestChangePoint(series[l:r], minSegLen)
			if cp.EStatistic > best.EStatistic {
				best = ChangePoint{Index: l + cp.Index, EStatistic: cp.EStatistic}
				bestSeg = seg
			}
		}

		if best.EStatistic <= 0 || bestSeg == nil {
			break
		}

		found = append(found, best)

		// Split the segment at the change point.
		newSegs := make([][]int, 0, len(segments)+1)
		for _, seg := range segments {
			if seg[0] == bestSeg[0] && seg[1] == bestSeg[1] {
				newSegs = append(newSegs, []int{bestSeg[0], best.Index})
				newSegs = append(newSegs, []int{best.Index, bestSeg[1]})
			} else {
				newSegs = append(newSegs, seg)
			}
		}
		segments = newSegs
	}

	sort.Slice(found, func(i, j int) bool { return found[i].Index < found[j].Index })
	return found
}

// bestChangePoint finds the index within series that maximises the energy
// statistic (i.e. the most likely single change point).
//
// The implementation uses an incremental update strategy so that the overall
// complexity is O(n²) rather than O(n³):
//
//   - sumAB tracks Σ|a[i]-b[j]| and is updated in O(n) per step by adding the
//     contributions of the element that moves from b into a.
//   - sumAA and sumBB (the within-segment sums) are updated similarly in O(n).
func bestChangePoint(series []float64, minSegLen int) ChangePoint {
	n := len(series)
	best := ChangePoint{EStatistic: -1}

	// Seed with the initial split at k = minSegLen.
	// a = series[:minSegLen], b = series[minSegLen:]
	sumAB := 0.0
	for i := 0; i < minSegLen; i++ {
		for j := minSegLen; j < n; j++ {
			sumAB += math.Abs(series[i] - series[j])
		}
	}

	sumAA := 0.0
	for i := 0; i < minSegLen; i++ {
		for j := i + 1; j < minSegLen; j++ {
			sumAA += math.Abs(series[i] - series[j])
		}
	}

	sumBB := 0.0
	for i := minSegLen; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sumBB += math.Abs(series[i] - series[j])
		}
	}

	evalAndUpdate := func(k int) {
		na := float64(k)
		nb := float64(n - k)
		// meanAbsDiff between a and b
		ab := sumAB / (na * nb)
		// meanAbsDiffSelf for a: sum over upper triangle → multiply by 2 for
		// the n*(n-1) denominator used in the original formula.
		aa := (2 * sumAA) / (na * (na - 1))
		bb := (2 * sumBB) / (nb * (nb - 1))
		if na < 2 {
			aa = 0
		}
		if nb < 2 {
			bb = 0
		}
		e := (2 * na * nb / (na + nb)) * (ab - aa/2 - bb/2)
		if e > best.EStatistic {
			best = ChangePoint{Index: k, EStatistic: e}
		}
	}

	evalAndUpdate(minSegLen)

	// Slide the split point from minSegLen to n-minSegLen, updating the running
	// sums incrementally each time series[k-1] moves from b into a.
	for k := minSegLen + 1; k <= n-minSegLen; k++ {
		moved := series[k-1] // element transitioning: b → a

		// sumAB: the pairs (i, k-1) for i<k-1 that were cross-pairs are gone;
		// the new cross-pairs (k-1, j) for j>=k are added.
		for i := 0; i < k-1; i++ {
			sumAB -= math.Abs(series[i] - moved)
		}
		for j := k; j < n; j++ {
			sumAB += math.Abs(moved - series[j])
		}

		// sumBB: remove pairs that included the moved element (it's now in a).
		for j := k; j < n; j++ {
			sumBB -= math.Abs(moved - series[j])
		}

		// sumAA: add pairs between the moved element and all existing a members.
		for i := 0; i < k-1; i++ {
			sumAA += math.Abs(series[i] - moved)
		}

		evalAndUpdate(k)
	}

	return best
}
