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

package attribution

import (
	"math"
	"sort"
)

// StepOptions configures StepTest.
type StepOptions struct {
	// Tau is the scale of the mixing prior, on the log scale. A good choice is
	// the smallest effect worth acting on, which makes the test most powerful
	// where it matters. Zero means 0.25.
	Tau float64
	// Alpha is the level of the reported confidence sequence. Zero means 0.05.
	Alpha float64
	// MinPre is the number of observed pre-intervention ticks required to fit
	// the model. Zero means 10.
	MinPre int
	// Grace is the number of ticks after the intervention that StepResult.Late
	// leaves out: a rollout in progress, a cache warming up and connections
	// draining all move latency for a while without being the steady state. The
	// main evidence (Shift, Path) uses every tick, because with a drifting level
	// almost all the information about a step sits in the ticks right after it;
	// Late is the evidence from the ticks after Grace only, the check that the
	// shift is still there once the transient is over. The increments of the
	// score after a fixed tick form a martingale of their own, so Late is a valid
	// e-process too.
	Grace int
}

func (o *StepOptions) defaults() {
	if o.Tau == 0 {
		o.Tau = 0.25
	}
	if o.Alpha == 0 {
		o.Alpha = 0.05
	}
	if o.MinPre == 0 {
		o.MinPre = 10
	}
}

// StepResult is the outcome of fitting the counterfactual model on the
// pre-intervention ticks and testing for a persistent level shift after them.
type StepResult struct {
	Ok     bool
	Reason string // why the model could not be fitted, when !Ok

	// Shift carries the sufficient statistic of the effect; Theta and SE are its
	// maximum-likelihood estimate and standard error (log scale).
	Shift Shift
	Theta float64
	SE    float64

	PreN, PostN int     // observed ticks on each side
	LatePostN   int     // observed post-intervention ticks after the grace period
	Q           float64 // level-drift variance as a multiple of the observation variance
	Sigma2      float64 // observation variance per unit weight, after inflation
	Rho1        float64 // lag-1 autocorrelation of the standardised pre-period innovations
	Inflation   float64 // variance inflation applied for model misfit and estimation
	Beta        []float64
	DF          float64 // effective degrees of freedom of the covariate regression

	// Path is the Shift after each post-intervention tick (len = len(y) − T),
	// so a caller can ask what the evidence was at any earlier tick, or re-test
	// as ticks arrive.
	Path []Shift
	// Late is the Shift accumulated over the ticks after the grace period only.
	// Info is 0 when there are none.
	Late Shift
}

// StepTest fits a counterfactual for the series y on the ticks before T and
// tests whether the series shifted by a persistent amount from tick T on.
//
// The model is a local level plus regression, the intervention-analysis form of
// Box and Tiao (1975) and the core of Brodersen et al. (2015):
//
//	y_t = μ_t + x_t·β + θ·1[t ≥ T] + ε_t,   μ_t = μ_{t-1} + η_t
//
// y is the log of the interval mean latency; w is the number of calls behind
// each observation (a mean of w calls has variance proportional to 1/w), NaN or
// non-positive where the query did not run; cov are columns of covariates that
// the intervention did not affect (the same length as y, no NaN). β is fitted by
// ridge regression on first differences of the pre-intervention data, which
// removes the level so that drift cannot masquerade as a coefficient. The
// variances of ε and η are fitted by maximum likelihood on the pre-intervention
// data only.
//
// θ is then estimated inside the Kalman filter run over the whole series
// (Durbin and Koopman 2012, 6.2.4): the filter's innovation at each tick is the
// innovation under θ = 0 minus θ times the innovation of the step regressor
// pushed through the same gains. That makes the log-likelihood of θ exactly
// quadratic, with score Σ a_t ν_t/F_t and information Σ a_t²/F_t, and under no
// effect the score is a martingale with that quadratic variation. The pair is
// returned as a Shift, to which the e-process machinery applies exactly.
//
// The consequence of using a random-walk level is intended: information about θ
// accrues from the ticks around T and then from however slowly the level is
// allowed to move. A series whose level wanders (large Q) cannot support a
// verdict, and the test says so by its width instead of by a rule.
func StepTest(y, w []float64, cov [][]float64, T int, opt StepOptions) StepResult {
	opt.defaults()
	n := len(y)
	res := StepResult{Theta: nan, SE: nan, Inflation: 1}
	if T <= 0 || T >= n {
		res.Reason = "intervention tick outside the series"
		return res
	}
	if len(w) != n {
		res.Reason = "weights and series differ in length"
		return res
	}
	for t := 0; t < n; t++ {
		if !isNaN(y[t]) {
			if t < T {
				res.PreN++
			} else {
				res.PostN++
				if t >= T+opt.Grace {
					res.LatePostN++
				}
			}
		}
	}
	if res.PreN < opt.MinPre {
		res.Reason = "too few pre-intervention observations"
		return res
	}
	if res.PostN < 1 {
		res.Reason = "no post-intervention observations"
		return res
	}

	// Observation weights relative to the pre-period median, clipped so that one
	// huge interval cannot dominate and one tiny one cannot be ignored.
	pw := make([]float64, 0, T)
	for t := 0; t < T; t++ {
		if !isNaN(y[t]) && w[t] > 0 {
			pw = append(pw, w[t])
		}
	}
	med := medianOf(pw)
	if isNaN(med) || med <= 0 {
		med = 1
	}
	h := make([]float64, n)
	for t := 0; t < n; t++ {
		rel := 1.0
		if w[t] > 0 {
			rel = math.Min(20, math.Max(0.05, w[t]/med))
		}
		h[t] = 1 / rel
	}

	// Covariates → residual series.
	r := append([]float64(nil), y...)
	var beta []float64
	var sigmaBeta [][]float64
	if len(cov) > 0 {
		var df float64
		beta, df, sigmaBeta = fitRidge(y, h, cov, T)
		res.Beta, res.DF = beta, df
		for t := 0; t < n; t++ {
			if isNaN(r[t]) {
				continue
			}
			for k, b := range beta {
				r[t] -= b * cov[k][t]
			}
		}
	}

	// Fit the drift ratio q on the pre-period.
	crit := func(lnq float64) float64 {
		out := runFilter(r, h, nil, math.Exp(lnq), T)
		if out.nObs < 3 {
			return math.Inf(1)
		}
		return out.crit
	}
	lnq := goldenMin(crit, math.Log(1e-5), math.Log(1e2), 60)
	// the objective can be flat at the lower edge; prefer the edge to a wander
	if crit(math.Log(1e-5)) <= crit(lnq)+1e-9 {
		lnq = math.Log(1e-5)
	}
	q := math.Exp(lnq)
	res.Q = q

	s := make([]float64, n)
	for t := T; t < n; t++ {
		s[t] = 1
	}
	pre := runFilter(r, h, nil, q, T)
	if pre.nObs < 3 || pre.sigma2 <= 0 {
		res.Reason = "degenerate pre-intervention series"
		return res
	}
	full := runFilter(r, h, s, q, n)

	// Diagnostics: serial correlation left in the standardised pre-period
	// innovations means the local level did not capture the dynamics; widen the
	// variance rather than trust an overconfident test.
	z := make([]float64, 0, T)
	for t := 0; t < T; t++ {
		if !isNaN(full.v[t]) && t > full.first {
			z = append(z, full.v[t]/math.Sqrt(pre.sigma2*full.f[t]))
		}
	}
	res.Rho1 = lag1(z)
	infl := 1.0
	if res.Rho1 > 0.15 {
		rho := math.Min(res.Rho1, 0.8)
		infl = (1 + rho) / (1 - rho)
	}
	// The variance was estimated from PreN points: use an upper bound one
	// standard error above the estimate (relative s.e. √(2/(n−1))), plus the
	// degrees of freedom the covariate regression used up.
	infl *= 1 + math.Sqrt(2/float64(max(res.PreN-1, 1))) + res.DF/float64(res.PreN)
	res.Inflation = infl
	res.Sigma2 = pre.sigma2 * infl

	res.Path = make([]Shift, 0, n-T)
	// Mean of each covariate over the last ticks before T: the local reference
	// the level absorbs. The post-period mean minus this is the covariate shift
	// the counterfactual has to extrapolate over.
	var ref []float64
	if len(cov) > 0 {
		ref = make([]float64, len(cov))
		for k := range cov {
			var sum float64
			c := 0
			for t := T - 1; t >= 0 && c < 15; t-- {
				if !isNaN(y[t]) {
					sum += cov[k][t]
					c++
				}
			}
			if c > 0 {
				ref[k] = sum / float64(c)
			}
		}
	}
	// adjust folds the uncertainty of the covariate coefficients into an effect
	// estimate: they were fitted with error, and the post-period covariates may
	// sit outside the range the pre-period taught them. That error is a common
	// offset on the effect, independent of the innovations, so its variance adds
	// to the effect's.
	adjust := func(sh Shift, postSum []float64, postN int) Shift {
		if len(cov) == 0 || postN == 0 || sh.Info <= 0 {
			return sh
		}
		d := make([]float64, len(cov))
		for k := range cov {
			d[k] = postSum[k]/float64(postN) - ref[k]
		}
		var vb float64
		for i := range d {
			for j := range d {
				vb += d[i] * sigmaBeta[i][j] * d[j]
			}
		}
		vb *= infl
		if vb <= 0 {
			return sh
		}
		theta := sh.Score / sh.Info
		info := 1 / (1/sh.Info + vb)
		return Shift{Score: theta * info, Info: info}
	}
	postSum := make([]float64, len(cov))
	lateSum := make([]float64, len(cov))
	postN, lateN := 0, 0
	var raw Shift
	for t := T; t < n; t++ {
		isLate := t >= T+opt.Grace
		if !isNaN(full.v[t]) {
			raw.Add(full.vx[t], full.v[t], res.Sigma2*full.f[t])
		}
		if !isNaN(y[t]) {
			postN++
			if isLate {
				lateN++
			}
			for k := range cov {
				postSum[k] += cov[k][t]
				if isLate {
					lateSum[k] += cov[k][t]
				}
			}
		}
		res.Path = append(res.Path, adjust(raw, postSum, postN))
	}
	res.Late = adjust(lateForecast(r, h, pre, q, res.Sigma2, T, T+opt.Grace, n), lateSum, lateN)
	if len(res.Path) > 0 {
		res.Shift = res.Path[len(res.Path)-1]
	}
	res.Theta, res.SE = res.Shift.Estimate()
	res.Ok = true
	return res
}

// lateForecast is the evidence about the level of the series after the grace
// period, taken the way Brodersen et al. take it: the counterfactual is the
// forecast from the end of the pre-intervention period, which is not updated by
// the post-intervention data, and the effect at each late tick is the observation
// minus that forecast. The forecast errors share the uncertainty of the level at
// the end of the pre-period and of its drift since, so their covariance is
//
//	Σ_ij = σ² (P + q (min(t_i, t_j) − t_last) + h_i·[i = j])
//
// and the effect that stayed, a constant over the late ticks, is estimated by
// generalised least squares with score 1'Σ⁻¹e and information 1'Σ⁻¹1. Unlike
// the filtered innovations, which adapt to a shift and keep little information
// about it after the first few ticks, this keeps the full late-window evidence
// of a permanent change and shows a transient (a warm-up that decayed) as the
// absence of one. It is a fixed-sample statistic at the current tick, used to
// confirm persistence, not as a sequential test.
func lateForecast(r, h []float64, pre kfOut, q, sigma2 float64, T, from, n int) Shift {
	if pre.last < 0 {
		return Shift{}
	}
	var ticks []int
	for t := from; t < n; t++ {
		if !isNaN(r[t]) {
			ticks = append(ticks, t)
		}
	}
	m := len(ticks)
	if m == 0 {
		return Shift{}
	}
	cov := make([][]float64, m)
	e := make([]float64, m)
	one := make([]float64, m)
	for i, ti := range ticks {
		cov[i] = make([]float64, m)
		e[i] = r[ti] - pre.a
		one[i] = 1
		for j := 0; j <= i; j++ {
			tj := ticks[j]
			c := sigma2 * (pre.p + q*float64(tj-pre.last)) // min(t_i,t_j) = t_j for j ≤ i
			if i == j {
				c += sigma2 * h[ti]
			}
			cov[i][j] = c
		}
		for j := i + 1; j < m; j++ {
			cov[i][j] = 0
		}
	}
	for i := 0; i < m; i++ {
		for j := i + 1; j < m; j++ {
			cov[i][j] = cov[j][i]
		}
	}
	c1 := make([][]float64, m)
	for i := range c1 {
		c1[i] = append([]float64(nil), cov[i]...)
	}
	x, ok1 := solveSPD(c1, one)
	c2 := make([][]float64, m)
	for i := range c2 {
		c2[i] = append([]float64(nil), cov[i]...)
	}
	y, ok2 := solveSPD(c2, e)
	if !ok1 || !ok2 {
		return Shift{}
	}
	var info, score float64
	for i := 0; i < m; i++ {
		info += x[i]
		score += y[i] // 1'Σ⁻¹e
	}
	return Shift{Score: score, Info: info}
}

// kfOut is the output of one Kalman-filter pass, in units where the
// observation variance of a unit-weight observation is 1.
type kfOut struct {
	v, f, vx []float64 // innovation, its variance, regressor innovation (NaN when not observed)
	sigma2   float64   // concentrated ML estimate of the observation variance scale
	crit     float64   // −2·(concentrated log-likelihood) up to a constant
	nObs     int       // observations entering the likelihood (the first is absorbed)
	first    int       // index of the first observation
	a, p     float64   // filtered level and its variance after the last observed tick
	last     int       // tick of the last observation (-1 if none)
}

// runFilter runs the local-level Kalman filter over r[0:upto] with observation
// variances h[t] and level-innovation variance q (both in units of the unknown
// scale σ²). The first observation initialises the level (exact diffuse prior).
// If s is non-nil the same gains are applied to the regressor s, giving the
// innovations vx used for the intervention effect.
func runFilter(r, h, s []float64, q float64, upto int) kfOut {
	out := kfOut{
		v:     make([]float64, upto),
		f:     make([]float64, upto),
		vx:    make([]float64, upto),
		first: -1,
		last:  -1,
	}
	for t := range out.v {
		out.v[t], out.f[t], out.vx[t] = nan, nan, nan
	}
	var a, ax, p float64
	var sumQ, sumLnF float64
	for t := 0; t < upto; t++ {
		if out.first >= 0 {
			p += q
		}
		if isNaN(r[t]) {
			continue
		}
		if out.first < 0 {
			out.first = t
			out.last = t
			a, p = r[t], h[t]
			out.a, out.p = a, p
			if s != nil {
				ax = s[t]
			}
			continue
		}
		f := p + h[t]
		v := r[t] - a
		k := p / f
		out.v[t], out.f[t] = v, f
		if s != nil {
			vx := s[t] - ax
			out.vx[t] = vx
			ax += k * vx
		}
		a += k * v
		p = p * (1 - k)
		sumQ += v * v / f
		sumLnF += math.Log(f)
		out.nObs++
		out.last = t
		out.a, out.p = a, p
	}
	if out.nObs > 0 {
		out.sigma2 = sumQ / float64(out.nObs)
		if out.sigma2 > 0 {
			out.crit = float64(out.nObs)*math.Log(out.sigma2) + sumLnF
		} else {
			out.crit = math.Inf(-1)
		}
	}
	return out
}

// fitRidge fits the covariate coefficients by weighted ridge regression of the
// first differences of y on the first differences of the covariates, over the
// pre-intervention ticks, choosing the penalty by blocked cross-validation.
// It returns the coefficients on the original scale of the covariates and the
// effective degrees of freedom. With more covariates than the data can support
// it keeps the most correlated ones, by correlation with y computed on the
// pre-period only.
func fitRidge(y, h []float64, cov [][]float64, T int) (beta []float64, df float64, sigmaBeta [][]float64) {
	p := len(cov)
	beta = make([]float64, p)
	sigmaBeta = make([][]float64, p)
	for k := range sigmaBeta {
		sigmaBeta[k] = make([]float64, p)
	}

	// difference pairs of consecutive observed pre-period ticks
	type pair struct {
		i, j int
		wt   float64
	}
	var pairs []pair
	last := -1
	for t := 0; t < T; t++ {
		if isNaN(y[t]) {
			continue
		}
		if last >= 0 && t-last <= 3 {
			pairs = append(pairs, pair{last, t, 1 / (h[last] + h[t])})
		}
		last = t
	}
	m := len(pairs)
	if m < 8 {
		return beta, 0, sigmaBeta
	}
	dy := make([]float64, m)
	dx := make([][]float64, p)
	wt := make([]float64, m)
	for k := range dx {
		dx[k] = make([]float64, m)
	}
	var wsum float64
	for r, pr := range pairs {
		dy[r] = y[pr.j] - y[pr.i]
		wt[r] = pr.wt
		wsum += pr.wt
		for k := 0; k < p; k++ {
			dx[k][r] = cov[k][pr.j] - cov[k][pr.i]
		}
	}
	for r := range wt {
		wt[r] *= float64(m) / wsum
	}

	// keep the covariates most correlated with dy, at most m/4 of them
	keepN := p
	if lim := m / 4; keepN > lim {
		keepN = lim
	}
	if keepN < 1 {
		keepN = 1
	}
	scale := make([]float64, p)
	corr := make([]float64, p)
	for k := 0; k < p; k++ {
		var sxx, sxy, syy float64
		for r := 0; r < m; r++ {
			sxx += wt[r] * dx[k][r] * dx[k][r]
			sxy += wt[r] * dx[k][r] * dy[r]
			syy += wt[r] * dy[r] * dy[r]
		}
		scale[k] = math.Sqrt(sxx / float64(m))
		if sxx > 0 && syy > 0 {
			corr[k] = math.Abs(sxy) / math.Sqrt(sxx*syy)
		}
	}
	order := make([]int, p)
	for k := range order {
		order[k] = k
	}
	sort.SliceStable(order, func(a, b int) bool { return corr[order[a]] > corr[order[b]] })
	sel := order[:keepN]
	sort.Ints(sel)
	live := sel[:0:0]
	for _, k := range sel {
		if scale[k] > 1e-12 {
			live = append(live, k)
		}
	}
	if len(live) == 0 {
		return beta, 0, sigmaBeta
	}
	q := len(live)

	// Standardised design, stored once.
	xs := make([][]float64, m)
	for r := 0; r < m; r++ {
		xs[r] = make([]float64, q)
		for i := 0; i < q; i++ {
			xs[r][i] = dx[live[i]][r] / scale[live[i]]
		}
	}

	// Per-fold normal equations, computed once: a fold's held-out error for any
	// penalty is then a quadratic form in the coefficients, with no pass over the
	// rows.
	const folds = 5
	type acc struct {
		g   [][]float64
		b   []float64
		syy float64
	}
	mk := func() *acc {
		a := &acc{g: make([][]float64, q), b: make([]float64, q)}
		for i := range a.g {
			a.g[i] = make([]float64, q)
		}
		return a
	}
	fold := make([]*acc, folds)
	total := mk()
	for f := 0; f < folds; f++ {
		fold[f] = mk()
		lo, hi := f*m/folds, (f+1)*m/folds
		for r := lo; r < hi; r++ {
			fold[f].syy += wt[r] * dy[r] * dy[r]
			for i := 0; i < q; i++ {
				xi := wt[r] * xs[r][i]
				fold[f].b[i] += xi * dy[r]
				for j := 0; j <= i; j++ {
					fold[f].g[i][j] += xi * xs[r][j]
				}
			}
		}
		for i := 0; i < q; i++ {
			total.b[i] += fold[f].b[i]
			for j := 0; j <= i; j++ {
				total.g[i][j] += fold[f].g[i][j]
			}
		}
		total.syy += fold[f].syy
	}
	for _, a := range append([]*acc{total}, fold...) {
		for i := 0; i < q; i++ {
			for j := i + 1; j < q; j++ {
				a.g[i][j] = a.g[j][i]
			}
		}
	}
	solveWith := func(g [][]float64, b []float64, lambda float64) ([]float64, bool) {
		a := make([][]float64, q)
		for i := range a {
			a[i] = append([]float64(nil), g[i]...)
			a[i][i] += lambda
		}
		return solveSPD(a, append([]float64(nil), b...))
	}

	grid := []float64{0.01, 0.1, 1, 3, 10, 30, 100, 300, 1000}
	bestLam, bestErr := grid[len(grid)-1]*float64(m)/10, math.Inf(1)
	for _, lam := range grid {
		lam *= float64(m) / 10
		var errSum float64
		okAll := true
		for f := 0; f < folds && okAll; f++ {
			g := make([][]float64, q)
			b := make([]float64, q)
			for i := 0; i < q; i++ {
				g[i] = make([]float64, q)
				b[i] = total.b[i] - fold[f].b[i]
				for j := 0; j < q; j++ {
					g[i][j] = total.g[i][j] - fold[f].g[i][j]
				}
			}
			beta, ok := solveWith(g, b, lam)
			if !ok {
				okAll = false
				break
			}
			// held-out weighted squared error: Syy − 2β'b + β'Gβ on the fold
			e := fold[f].syy
			for i := 0; i < q; i++ {
				e -= 2 * beta[i] * fold[f].b[i]
				for j := 0; j < q; j++ {
					e += beta[i] * fold[f].g[i][j] * beta[j]
				}
			}
			errSum += e
		}
		if okAll && errSum < bestErr {
			bestErr, bestLam = errSum, lam
		}
	}
	bs, ok := solveWith(total.g, total.b, bestLam)
	if !ok {
		return beta, 0, sigmaBeta
	}
	for i, k := range live {
		beta[k] = bs[i] / scale[k]
	}
	gram := total.g
	all := make([]int, m)
	for r := range all {
		all[r] = r
	}

	// effective degrees of freedom: tr((X'WX + λI)^{-1} X'WX), and the sampling
	// covariance of the coefficients, σ²_Δ · M⁻¹ G M⁻¹ with M = G + λI
	// (standardised units, then rescaled).
	minv := make([][]float64, q) // columns of M⁻¹
	for i := 0; i < q; i++ {
		a := make([][]float64, q)
		e := make([]float64, q)
		e[i] = 1
		for u := 0; u < q; u++ {
			a[u] = append([]float64(nil), gram[u]...)
			a[u][u] += bestLam
		}
		col, ok := solveSPD(a, e)
		if !ok {
			return beta, 0, sigmaBeta
		}
		minv[i] = col
	}
	for i := 0; i < q; i++ {
		for u := 0; u < q; u++ {
			df += minv[i][u] * gram[u][i]
		}
	}
	var rss float64
	for r := 0; r < m; r++ {
		pred := 0.0
		for i := 0; i < q; i++ {
			pred += bs[i] * xs[r][i]
		}
		e := dy[r] - pred
		rss += wt[r] * e * e
	}
	sig2 := rss / math.Max(1, float64(m)-df)
	for i := 0; i < q; i++ {
		for j := 0; j < q; j++ {
			// (M⁻¹ G M⁻¹)_ij
			var v float64
			for u := 0; u < q; u++ {
				for w := 0; w < q; w++ {
					v += minv[i][u] * gram[u][w] * minv[w][j]
				}
			}
			sigmaBeta[live[i]][live[j]] = sig2 * v / (scale[live[i]] * scale[live[j]])
		}
	}
	return beta, df, sigmaBeta
}
