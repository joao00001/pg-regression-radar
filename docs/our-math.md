---
hide:
  - navigation
  - toc
---

<div class="pgrr-home pgrr-math" markdown="1">

<header class="pgrr-paper">
<div class="pgrr-paper-main">
<p class="pgrr-paper-kick">Technical note</p>
<h1>Our <em>math</em></h1>
<div class="pgrr-abstract">
<b>Abstract</b>
<p>A <code>PerformanceRegression</code> is a verdict reached in seven steps from two cumulative counters. This note derives each step, states the assumptions it needs and measures where they fail. The figures run the engine's own arithmetic, in your browser.</p>
</div>
<div class="pgrr-rules">
<math display="block"><mrow><mi>f</mi><mo>≥</mo><mn>1</mn><mo>+</mo><mi>θ</mi></mrow></math>
<math display="block"><mrow><mrow><mo>|</mo><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover><mo>−</mo><msub><mi>t</mi><mi>d</mi></msub><mo>|</mo></mrow><mo>≤</mo><mi>τ</mi></mrow></math>
<math display="block"><mrow><mi>p</mi><mo>≤</mo><mi>α</mi></mrow></math>
</div>
</div>
<figure class="pgrr-paper-fig" data-w="hero">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 420 190" role="img" aria-label="The energy statistic scanning a latency series for its change point"></svg>
<svg class="pgrr-plot" data-r="curve" viewBox="0 0 420 90" role="img" aria-hidden="true"></svg>
<figcaption><b>Fig. 0</b> The split slides along the series; Q(k) is drawn as it goes, and the engine keeps the maximum.</figcaption>
</figure>
<nav class="pgrr-toc" aria-label="Contents">
<a href="#lab"><span>Lab</span>Whole engine</a>
<a href="#samples"><span>§1</span>Samples</a>
<a href="#gates"><span>§2</span>Gates</a>
<a href="#energy"><span>§3</span>Energy</a>
<a href="#tolerance"><span>§4</span>Tolerance</a>
<a href="#welch"><span>§5</span>Welch</a>
<a href="#confidence"><span>§6</span>Confidence</a>
<a href="#selection"><span>§7</span>Selection</a>
<a href="#power"><span>§8</span>Power</a>
<a href="#attribution"><span>§9</span>Attribution</a>
</nav>
</header>

## <span class="pgrr-kick">Overview</span>Seven steps from a counter to a *verdict*<span class="pgrr-sub">Each stage can stop the analysis. The lab below plays a new query every few seconds; the stage that stops it turns red.</span> { #pipeline .pgrr-title data-toc-label="Pipeline" }

<div class="pgrr-pipe is-idle" data-w="pipe">
<div class="pgrr-stage" style="--i:0" data-s="samples"><span class="pgrr-n">01</span><b>Samples</b><math><mrow><msub><mi>x</mi><mi>t</mi></msub><mo>=</mo><mfrac><mrow><mi>Δ</mi><mi>T</mi></mrow><mrow><mi>Δ</mi><mi>C</mi></mrow></mfrac></mrow></math><small>Interval means from the cumulative counters.</small></div>
<div class="pgrr-stage" style="--i:1" data-s="floors"><span class="pgrr-n">02</span><b>Floors</b><math><mrow><mi>n</mi><mo>≥</mo><mn>10</mn><mo>,</mo><mi>N</mi><mo>≥</mo><mn>100</mn></mrow></math><small>Enough points and calls to estimate a spread.</small></div>
<div class="pgrr-stage" style="--i:2" data-s="prefilter"><span class="pgrr-n">03</span><b>Pre-filter</b><math><mrow><mi>f</mi><mo>−</mo><mn>1</mn><mo>≥</mo><mi>θ</mi></mrow></math><small>Cheap rejection of small shifts.</small></div>
<div class="pgrr-stage" style="--i:3" data-s="edivisive"><span class="pgrr-n">04</span><b>E-divisive</b><math><mrow><mi>argmax</mi><mi>Q</mi><mo>(</mo><mi>k</mi><mo>)</mo></mrow></math><small>Where did the distribution change?</small></div>
<div class="pgrr-stage" style="--i:4" data-s="tolerance"><span class="pgrr-n">05</span><b>Tolerance</b><math><mrow><mo>|</mo><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover><mo>−</mo><msub><mi>t</mi><mi>d</mi></msub><mo>|</mo><mo>≤</mo><mi>τ</mi></mrow></math><small>Was it this deploy?</small></div>
<div class="pgrr-stage" style="--i:5" data-s="welch"><span class="pgrr-n">06</span><b>Welch</b><math><mrow><mi>p</mi><mo>≤</mo><mi>α</mi></mrow></math><small>Is the difference more than noise?</small></div>
<div class="pgrr-stage" style="--i:6" data-s="verdict"><span class="pgrr-n">07</span><b>Verdict</b><math><mrow><mi>c</mi><mo>=</mo><mn>1</mn><mo>−</mo><mi>p</mi></mrow></math><small>Detected, with a score to rank it.</small></div>
</div>

## <span class="pgrr-kick">Lab</span>Run the *whole engine*<span class="pgrr-sub">The Go correlation engine's own arithmetic, on a new synthetic query every few seconds. Watch which stage stops it.</span> { #lab .pgrr-title data-toc-label="Lab" }

<div class="pgrr-sec pgrr-sec--wide">
<div class="pgrr-lab pgrr-lab--full" data-w="lab">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 720 300" role="img" aria-label="Interval-mean latency around the deploy, with the detected change point and its tolerance band"></svg>
<svg class="pgrr-plot" data-r="curve" viewBox="0 0 720 96" role="img" aria-label="Energy statistic at every candidate split"></svg>
<div class="pgrr-legend"><span class="lg-c"><i></i>deploy <math><msub><mi>t</mi><mi>d</mi></msub></math></span><span class="lg-a"><i></i>change point <math><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover></math></span><span class="lg-b lg-dash"><i></i>window means</span><span>shaded band: ±τ tolerance</span></div>
<dl class="pgrr-params">
<div><dt>Regression size</dt><dd data-o="size"></dd></div>
<div><dt>Noise, per-call CV</dt><dd data-o="cv"></dd></div>
<div><dt>Calls per scrape</dt><dd data-o="calls"></dd></div>
<div><dt>Lands after deploy</dt><dd data-o="lag"></dd></div>
<div><dt>Autocorrelation φ</dt><dd data-o="phi"></dd></div>
<div><dt>Scrape interval</dt><dd data-o="scrape"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
<div class="pgrr-verdict" data-r="verdict" data-v="NoRegression" role="status"><b>—</b><span></span></div>
<p class="pgrr-cap">Window 30 min each side · θ = 0.20 · α = 0.05 · floors 10 samples and 100 calls · tolerance max(2, 0.2 × 30) = 6 min.</p>
</div>
</div>

## <span class="pgrr-kick">Derivation</span>From counters to *samples*<span class="pgrr-sub">The number PostgreSQL shows is not the number we analyse.</span> { #samples .pgrr-title data-toc-label="Samples" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§1</span>Interval means</h3>
<p><code>pg_stat_statements</code> exposes two monotone counters per query: the calls <math><mi>C</mi></math> and the total execution time <math><mi>T</mi></math> in milliseconds, both accumulated since the last reset. Its <code>mean_exec_time</code> is their ratio, which is a mean over the whole history. We scrape both counters and analyse the mean of the calls made <em>since the previous scrape</em>:</p>
<div class="pgrr-eq"><math display="block"><mrow><msub><mi>x</mi><mi>t</mi></msub><mo>=</mo><mfrac><mrow><msub><mi>T</mi><mi>t</mi></msub><mo>−</mo><msub><mi>T</mi><mrow><mi>t</mi><mo>−</mo><mn>1</mn></mrow></msub></mrow><mrow><msub><mi>C</mi><mi>t</mi></msub><mo>−</mo><msub><mi>C</mi><mrow><mi>t</mi><mo>−</mo><mn>1</mn></mrow></msub></mrow></mfrac><mo>=</mo><mfrac><mrow><mi>Δ</mi><msub><mi>T</mi><mi>t</mi></msub></mrow><mrow><mi>Δ</mi><msub><mi>C</mi><mi>t</mi></msub></mrow></mfrac></mrow></math></div>
<p>The cumulative mean is slow for a precise reason. If a query ran <math><msub><mi>n</mi><mtext>pre</mtext></msub></math> calls at mean <math><mi>μ</mi></math> and then <math><msub><mi>n</mi><mtext>post</mtext></msub></math> calls at <math><mrow><mi>μ</mi><mo>+</mo><mi>δ</mi></mrow></math>, the counter reports</p>
<div class="pgrr-eq"><math display="block"><mrow><mfrac><mi>T</mi><mi>C</mi></mfrac><mo>=</mo><mi>μ</mi><mo>+</mo><mi>δ</mi><mo>·</mo><mfrac><msub><mi>n</mi><mtext>post</mtext></msub><mrow><msub><mi>n</mi><mtext>pre</mtext></msub><mo>+</mo><msub><mi>n</mi><mtext>post</mtext></msub></mrow></mfrac></mrow></math></div>
<p>so after a deploy it moves only by the share of calls made since. A step of +100% needs as many calls after the deploy as before it just to read +50%. The interval mean reads the step at the next scrape.</p>
<div class="pgrr-note"><b>Resets</b><p>A counter that goes down (a restart, <code>pg_stat_statements_reset()</code>, a change in <code>stats_since</code>) makes <math><mrow><mi>Δ</mi><mi>C</mi></mrow></math> negative. That interval is discarded, not clamped, because its calls belong to no window. The cumulative mean jumps at a reset; the interval means do not.</p></div>
</div>
<div class="pgrr-lab" data-w="counters">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 280" role="img" aria-label="Cumulative mean versus interval mean after a step change in latency"></svg>
<div class="pgrr-legend"><span class="lg-a"><i></i>interval mean <math><msub><mi>x</mi><mi>t</mi></msub></math></span><span class="lg-m lg-dash"><i></i>cumulative mean <math><mrow><mi>T</mi><mo>/</mo><mi>C</mi></mrow></math></span></div>
<dl class="pgrr-params">
<div><dt>Step</dt><dd data-o="size"></dd></div>
<div><dt>Step lands at</dt><dd data-o="at"></dd></div>
<div><dt>Counter reset</dt><dd data-o="reset"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Derivation</span>Gates before *statistics*<span class="pgrr-sub">Floors decide when we are allowed to answer, not how often the answer is wrong.</span> { #gates .pgrr-title data-toc-label="Gates" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§2</span>Floors and the pre-filter</h3>
<p>Both windows (before and after the deploy) must clear two floors. <strong>Samples</strong> protect the machinery: the t-test and the energy statistic estimate a spread from the data, so they need points, <math><mrow><mi>n</mi><mo>≥</mo><msub><mi>n</mi><mtext>min</mtext></msub><mo>=</mo><mn>10</mn></mrow></math>. <strong>Executions</strong> bound the precision of the window's mean: the standard error of a mean of <math><mi>N</mi></math> calls with per-call coefficient of variation <math><mtext>CV</mtext></math> is</p>
<div class="pgrr-eq"><math display="block"><mrow><mtext>SE</mtext><mo>(</mo><mover accent="true"><mi>x</mi><mo>¯</mo></mover><mo>)</mo><mo>/</mo><mi>μ</mi><mo>=</mo><mfrac><mtext>CV</mtext><msqrt><mi>N</mi></msqrt></mfrac></mrow></math></div>
<p>With <math><mrow><mi>N</mi><mo>=</mo><mn>100</mn></mrow></math> and <math><mrow><mtext>CV</mtext><mo>=</mo><mn>1</mn></mrow></math>, a window mean is good to about ±10% at one standard error. Windows with fewer calls return <code>InsufficientData</code>, which is different from <code>NoRegression</code>: the first says "we could not tell".</p>
<p>The pre-filter compares the call-weighted window means, <math><mrow><mover accent="true"><mi>x</mi><mo>¯</mo></mover><mo>=</mo><mfrac><mrow><mo>∑</mo><mi>Δ</mi><mi>T</mi></mrow><mrow><mo>∑</mo><mi>Δ</mi><mi>C</mi></mrow></mfrac></mrow></math>, and stops when</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>f</mi><mo>−</mo><mn>1</mn><mo>&lt;</mo><mi>θ</mi><mo>=</mo><mn>0.20</mn><mo>⟹</mo><mtext>NoRegression</mtext></mrow></math></div>
<p>It is an optimisation and a guard: the expensive stages run only on shifts big enough to matter.</p>
</div>
<div class="pgrr-lab" data-w="gates">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 220" role="img" aria-label="Standard error of a window mean as the number of calls grows"></svg>
<dl class="pgrr-params">
<div><dt>Per-call CV</dt><dd data-o="cv"></dd></div>
<div><dt>Calls in the window</dt><dd data-o="n"></dd></div>
<div><dt>Scrapes in the window</dt><dd data-o="s"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Stage 1</span>Finding the change with *energy*<span class="pgrr-sub">E-divisive means asks whether two stretches of the series come from the same distribution, not only whether their means differ.</span> { #energy .pgrr-title data-toc-label="Energy statistic" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§3</span>The energy statistic</h3>
<p>For two samples <math><mrow><mi>A</mi><mo>=</mo><msub><mi>a</mi><mn>1</mn></msub><mo>…</mo><msub><mi>a</mi><msub><mi>n</mi><mi>a</mi></msub></msub></mrow></math> and <math><mrow><mi>B</mi><mo>=</mo><msub><mi>b</mi><mn>1</mn></msub><mo>…</mo><msub><mi>b</mi><msub><mi>n</mi><mi>b</mi></msub></msub></mrow></math> let <math><mrow><mi>D</mi><mo>(</mo><mi>A</mi><mo>,</mo><mi>B</mi><mo>)</mo></mrow></math> be the mean absolute distance across them. The energy distance of Székely and Rizzo <a href="#ref-1">[1]</a> compares the cross distance with the distances inside each sample:</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>ℰ</mi><mo>=</mo><mn>2</mn><msub><mi>D</mi><mrow><mi>a</mi><mi>b</mi></mrow></msub><mo>−</mo><msub><mi>D</mi><mrow><mi>a</mi><mi>a</mi></mrow></msub><mo>−</mo><msub><mi>D</mi><mrow><mi>b</mi><mi>b</mi></mrow></msub><mspace width="1em"></mspace><msub><mi>D</mi><mrow><mi>a</mi><mi>b</mi></mrow></msub><mo>=</mo><mfrac><mn>1</mn><mrow><msub><mi>n</mi><mi>a</mi></msub><msub><mi>n</mi><mi>b</mi></msub></mrow></mfrac><munder><mo>∑</mo><mrow><mi>i</mi><mo>,</mo><mi>j</mi></mrow></munder><mrow><mo>|</mo><msub><mi>a</mi><mi>i</mi></msub><mo>−</mo><msub><mi>b</mi><mi>j</mi></msub><mo>|</mo></mrow></mrow></math></div>
<p>It is zero exactly when the two samples have the same distribution (in the limit) and grows with any difference: location, spread or shape. Scaled by the sample sizes it becomes the statistic the engine evaluates at every admissible split <math><mi>k</mi></math> of the series, with within-sample distances averaged over unordered pairs <a href="#ref-2">[2]</a>:</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>Q</mi><mo>(</mo><mi>k</mi><mo>)</mo><mo>=</mo><mfrac><mrow><mn>2</mn><msub><mi>n</mi><mi>a</mi></msub><msub><mi>n</mi><mi>b</mi></msub></mrow><mrow><msub><mi>n</mi><mi>a</mi></msub><mo>+</mo><msub><mi>n</mi><mi>b</mi></msub></mrow></mfrac><mrow><mo>(</mo><msub><mi>D</mi><mrow><mi>a</mi><mi>b</mi></mrow></msub><mo>−</mo><mfrac><msub><mi>D</mi><mrow><mi>a</mi><mi>a</mi></mrow></msub><mn>2</mn></mfrac><mo>−</mo><mfrac><msub><mi>D</mi><mrow><mi>b</mi><mi>b</mi></mrow></msub><mn>2</mn></mfrac><mo>)</mo></mrow><mspace width="1em"></mspace><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover><mo>=</mo><munder><mi>argmax</mi><mrow><mi>m</mi><mo>≤</mo><mi>k</mi><mo>≤</mo><mi>n</mi><mo>−</mo><mi>m</mi></mrow></munder><mi>Q</mi><mo>(</mo><mi>k</mi><mo>)</mo></mrow></math></div>
<p>Here <math><mrow><msub><mi>n</mi><mi>a</mi></msub><mo>=</mo><mi>k</mi></mrow></math>, <math><mrow><msub><mi>n</mi><mi>b</mi></msub><mo>=</mo><mi>n</mi><mo>−</mo><mi>k</mi></mrow></math>, and <math><mrow><mi>m</mi><mo>=</mo><mo>max</mo><mo>(</mo><mn>3</mn><mo>,</mo><mo>⌊</mo><mi>n</mi><mo>/</mo><mn>10</mn><mo>⌋</mo><mo>)</mo></mrow></math> keeps a change from being "found" in the first or last few points. A split is kept only if <math><mrow><mi>Q</mi><mo>&gt;</mo><mn>0</mn></mrow></math>. Evaluating all splits costs <math><mrow><mi>O</mi><mo>(</mo><msup><mi>n</mi><mn>2</mn></msup><mo>)</mo></mrow></math> because moving the split one step changes each sum by one row and one column.</p>
<div class="pgrr-note"><b>Why not just split at the deploy?</b><p>Rolling deploys and connection draining spread the shift over minutes, and the collector adds scrape granularity. Letting the data place the split gives the shift's real start, and the next stage asks whether that start is close enough to the deploy to blame it.</p></div>
</div>
<div class="pgrr-lab" data-w="energy">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 220" role="img" aria-label="A latency series with the split sliding along it"></svg>
<svg class="pgrr-plot" data-r="curve" viewBox="0 0 520 120" role="img" aria-label="Q at every split"></svg>
<div class="pgrr-legend"><span class="lg-b"><i></i>left of the split</span><span class="lg-a"><i></i>right of the split</span><span class="lg-c"><i></i>argmax Q</span></div>
<dl class="pgrr-params">
<div><dt>Shift in the middle</dt><dd data-o="size"></dd></div>
<div><dt>Noise</dt><dd data-o="cv"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Stage 1b</span>Was it *this* deploy?<span class="pgrr-sub">A change point is only evidence about a deploy if it is close to it.</span> { #tolerance .pgrr-title data-toc-label="Tolerance" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§4</span>The tolerance window</h3>
<p>Let <math><msub><mi>t</mi><mi>d</mi></msub></math> be the deploy time and <math><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover></math> the located change. The change is attributed to the deploy only if</p>
<div class="pgrr-eq"><math display="block"><mrow><mrow><mo>|</mo><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover><mo>−</mo><msub><mi>t</mi><mi>d</mi></msub><mo>|</mo></mrow><mo>≤</mo><mi>τ</mi><mo>=</mo><mo>max</mo><mo>(</mo><mn>2</mn><mtext> min</mtext><mo>,</mo><mn>0.2</mn><mi>W</mi><mo>)</mo></mrow></math></div>
<p>where <math><mi>W</mi></math> is the analysis window. Making the allowance a fraction of the window keeps the same relative slack for a tight 10-minute window and a loose 60-minute one; the floor keeps short windows from shrinking it to nothing.</p>
<p>The order matters. The engine checks the tolerance <em>before</em> the significance test, so a shift that is very significant but starts twenty minutes before the deploy is reported as unrelated background change, never as a regression of this deploy. This is the step that turns a change detector into an attribution tool.</p>
<div class="pgrr-note"><b>A trade-off, not a free parameter</b><p>A wide tolerance catches slow rollouts and attributes more unrelated shifts to the deploy. A narrow one does the opposite. Under a Poisson process of unrelated shifts the expected number of coincidences grows linearly in <math><mi>τ</mi></math>.</p></div>
</div>
<div class="pgrr-lab" data-w="tol">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 170" role="img" aria-label="Timeline with the deploy, the tolerance band and the located change point"></svg>
<dl class="pgrr-params">
<div><dt>Window W</dt><dd data-o="w"></dd></div>
<div><dt>Located change <math><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover></math></dt><dd data-o="cp"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Stage 2</span>Is it more than *noise*?<span class="pgrr-sub">Welch's t-test on the two segments the data itself found.</span> { #welch .pgrr-title data-toc-label="Welch" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§5</span>Welch's t-test</h3>
<p>The two segments on either side of <math><mover accent="true"><mi>τ</mi><mo>ˆ</mo></mover></math> are compared without assuming equal variances. A latency regression often changes the spread as well as the mean, so the pooled-variance Student test would be the wrong tool <a href="#ref-3">[3]</a>:</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>t</mi><mo>=</mo><mfrac><mrow><msub><mover accent="true"><mi>x</mi><mo>¯</mo></mover><mi>b</mi></msub><mo>−</mo><msub><mover accent="true"><mi>x</mi><mo>¯</mo></mover><mi>a</mi></msub></mrow><msqrt><mrow><mfrac><msubsup><mi>s</mi><mi>a</mi><mn>2</mn></msubsup><msub><mi>n</mi><mi>a</mi></msub></mfrac><mo>+</mo><mfrac><msubsup><mi>s</mi><mi>b</mi><mn>2</mn></msubsup><msub><mi>n</mi><mi>b</mi></msub></mfrac></mrow></msqrt></mfrac></mrow></math></div>
<p>The reference distribution is Student's <math><mi>t</mi></math> with the Welch–Satterthwaite degrees of freedom,</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>ν</mi><mo>=</mo><mfrac><msup><mrow><mo>(</mo><mfrac><msubsup><mi>s</mi><mi>a</mi><mn>2</mn></msubsup><msub><mi>n</mi><mi>a</mi></msub></mfrac><mo>+</mo><mfrac><msubsup><mi>s</mi><mi>b</mi><mn>2</mn></msubsup><msub><mi>n</mi><mi>b</mi></msub></mfrac><mo>)</mo></mrow><mn>2</mn></msup><mrow><mfrac><msup><mrow><mo>(</mo><msubsup><mi>s</mi><mi>a</mi><mn>2</mn></msubsup><mo>/</mo><msub><mi>n</mi><mi>a</mi></msub><mo>)</mo></mrow><mn>2</mn></msup><mrow><msub><mi>n</mi><mi>a</mi></msub><mo>−</mo><mn>1</mn></mrow></mfrac><mo>+</mo><mfrac><msup><mrow><mo>(</mo><msubsup><mi>s</mi><mi>b</mi><mn>2</mn></msubsup><mo>/</mo><msub><mi>n</mi><mi>b</mi></msub><mo>)</mo></mrow><mn>2</mn></msup><mrow><msub><mi>n</mi><mi>b</mi></msub><mo>−</mo><mn>1</mn></mrow></mfrac></mrow></mfrac></mrow></math></div>
<p>and the two-sided p-value has a closed form through the regularised incomplete beta function, which is what the engine computes with a continued fraction <a href="#ref-4">[4]</a>:</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>p</mi><mo>=</mo><mi>P</mi><mo>(</mo><mo>|</mo><msub><mi>T</mi><mi>ν</mi></msub><mo>|</mo><mo>≥</mo><mo>|</mo><mi>t</mi><mo>|</mo><mo>)</mo><mo>=</mo><msub><mi>I</mi><mrow><mi>ν</mi><mo>/</mo><mo>(</mo><mi>ν</mi><mo>+</mo><msup><mi>t</mi><mn>2</mn></msup><mo>)</mo></mrow></msub><mo>(</mo><mfrac><mi>ν</mi><mn>2</mn></mfrac><mo>,</mo><mfrac><mn>1</mn><mn>2</mn></mfrac><mo>)</mo></mrow></math></div>
<p>The test is applied to the per-interval means <math><msub><mi>x</mi><mi>t</mi></msub></math>, not to individual calls. By the central limit theorem those are much closer to normal than the heavy-tailed per-call latencies, which is why a test that assumes normality is defensible here at all.</p>
</div>
<div class="pgrr-lab" data-w="welch">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 240" role="img" aria-label="Student t density with the two tails beyond the observed t shaded"></svg>
<dl class="pgrr-params">
<div><dt>Change in the mean</dt><dd data-o="d"></dd></div>
<div><dt>Interval-mean CV</dt><dd data-o="cv"></dd></div>
<div><dt>Points before</dt><dd data-o="na"></dd></div>
<div><dt>Points after</dt><dd data-o="nb"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Interpretation</span>What the score *means*<span class="pgrr-sub">confidenceScore is 1 − p. It ranks detections. It is not the probability that the regression is real.</span> { #confidence .pgrr-title data-toc-label="Confidence" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§6</span>A p-value is not a posterior</h3>
<p>The p-value answers "if nothing changed, how often would noise alone look at least this extreme?". Reading <math><mrow><mn>1</mn><mo>−</mo><mi>p</mi></mrow></math> as "the chance the change is real" inverts the conditional. How far apart the two can be is bounded, without any assumption about the prior, by the calibration of Sellke, Bayarri and Berger <a href="#ref-5">[5]</a>. For <math><mrow><mi>p</mi><mo>&lt;</mo><mn>1</mn><mo>/</mo><mi>e</mi></mrow></math> the Bayes factor in favour of "no change" is at least <math><mrow><mi>B</mi><mo>(</mo><mi>p</mi><mo>)</mo><mo>=</mo><mo>−</mo><mi>e</mi><mi>p</mi><mi>ln</mi><mi>p</mi></mrow></math>, so with even prior odds</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>P</mi><mo>(</mo><msub><mi>H</mi><mn>0</mn></msub><mo>∣</mo><mtext>data</mtext><mo>)</mo><mo>≥</mo><msup><mrow><mo>(</mo><mn>1</mn><mo>+</mo><mfrac><mn>1</mn><mrow><mo>−</mo><mi>e</mi><mi>p</mi><mi>ln</mi><mi>p</mi></mrow></mfrac><mo>)</mo></mrow><mrow><mo>−</mo><mn>1</mn></mrow></msup></mrow></math></div>
<div class="pgrr-tblwrap"><table class="pgrr-tbl"><thead><tr><th>p</th><th>score 1 − p</th><th>P(no change) ≥</th></tr></thead><tbody>
<tr><td>0.05</td><td>0.95</td><td>0.29</td></tr>
<tr><td>0.01</td><td>0.99</td><td>0.11</td></tr>
<tr><td>0.001</td><td>0.999</td><td>0.018</td></tr>
<tr><td>0.0001</td><td>0.9999</td><td>0.0025</td></tr>
</tbody></table></div>
<p>A score of 0.95 can leave a 29% chance that nothing changed. That is why the score is documented as a ranking and a threshold input, and why it should not be quoted as a probability.</p>
</div>
<div class="pgrr-lab" data-w="sbb">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 250" role="img" aria-label="Lower bound on the probability of no change as a function of the p-value"></svg>
<dl class="pgrr-params">
<div><dt>p-value</dt><dd data-o="p"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Caveat</span>The *search* inflates significance<span class="pgrr-sub">Choosing the split from the data, then testing it as if it had been chosen in advance, is optimistic.</span> { #selection .pgrr-title data-toc-label="Selection bias" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§7</span>Maximally selected statistics</h3>
<p>Stage 1 picks the split with the largest <math><mi>Q</mi></math>. Stage 2 then tests <em>that</em> split with a t-test whose reference distribution assumes the split was fixed beforehand. Under "no change" a t-statistic at a fixed split has <math><mrow><mi>p</mi><mo>∼</mo><mtext>Uniform</mtext><mo>(</mo><mn>0</mn><mo>,</mo><mn>1</mn><mo>)</mo></mrow></math>, but the split <math><msup><mi>k</mi><mo>∗</mo></msup></math> that maximises a separation statistic is by construction the one where the two sides look most different, so the p-value <math><msub><mi>p</mi><mrow><mi>k</mi><mo>∗</mo></mrow></msub></math> at that split is too small:</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>P</mi><mrow><mo>(</mo><msub><mi>p</mi><mrow><mi>k</mi><mo>∗</mo></mrow></msub><mo>≤</mo><mi>α</mi><mo>∣</mo><msub><mi>H</mi><mn>0</mn></msub><mo>)</mo></mrow><mo>&gt;</mo><mi>α</mi></mrow></math></div>
<p>The statistical literature calls this the problem of maximally selected statistics and corrects for it with a p-value that accounts for the search <a href="#ref-6">[6]</a>. E-divisive means itself does so with a permutation test over the whole search <a href="#ref-1">[1]</a>.</p>
<p>The figure draws series with <strong>no change at all</strong>, applies stages 1 and 2 exactly as the engine does, and histograms the p-values. A correct test would be flat. With independent noise the lab typically puts 35–39% of them below 0.05; with autocorrelated noise, far more.</p>
<div class="pgrr-note"><b>What protects production</b><p>Three other gates sit around this one: the pre-filter (a real +20% is required), the tolerance (the located change must sit near the deploy) and the floors. Through the whole pipeline the measured share of no-change analyses reported as <code>Detected</code> is much lower than the raw p-value rate; the <a href="https://joao00001.github.io/pg-regression-radar/roadmap/">roadmap</a> tracks the calibration work.</p></div>
</div>
<div class="pgrr-lab" data-w="selection">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 250" role="img" aria-label="Histogram of p-values at the split E-divisive chose, on series with no change"></svg>
<dl class="pgrr-params">
<div><dt>Points per series</dt><dd data-o="n"></dd></div>
<div><dt>Autocorrelation φ</dt><dd data-o="phi"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
<p class="pgrr-cap">Dashed line: what a correctly calibrated test would give.</p>
</div>
</div>

## <span class="pgrr-kick">Design</span>How many calls you *need*<span class="pgrr-sub">Sensitivity is bought with executions. This is the price list.</span> { #power .pgrr-title data-toc-label="Power" }

<div class="pgrr-sec">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§8</span>Sample size for a target effect</h3>
<p>To detect a relative change <math><mi>δ</mi></math> in the mean at size <math><mrow><mi>α</mi><mo>=</mo><mn>0.05</mn></mrow></math> (two-sided) with power <math><mrow><mn>1</mn><mo>−</mo><mi>β</mi><mo>=</mo><mn>0.8</mn></mrow></math>, a two-sample comparison needs, per window,</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>N</mi><mo>≈</mo><mfrac><mrow><mn>2</mn><msup><mrow><mo>(</mo><msub><mi>z</mi><mrow><mi>α</mi><mo>/</mo><mn>2</mn></mrow></msub><mo>+</mo><msub><mi>z</mi><mi>β</mi></msub><mo>)</mo></mrow><mn>2</mn></msup><mspace width="0.2em"></mspace><msup><mtext>CV</mtext><mn>2</mn></msup></mrow><msup><mi>δ</mi><mn>2</mn></msup></mfrac><mo>=</mo><mfrac><mrow><mn>15.7</mn><mspace width="0.2em"></mspace><msup><mtext>CV</mtext><mn>2</mn></msup></mrow><msup><mi>δ</mi><mn>2</mn></msup></mfrac></mrow></math></div>
<p>using <math><mrow><msub><mi>z</mi><mrow><mi>α</mi><mo>/</mo><mn>2</mn></mrow></msub><mo>=</mo><mn>1.96</mn></mrow></math> and <math><mrow><msub><mi>z</mi><mi>β</mi></msub><mo>=</mo><mn>0.84</mn></mrow></math>. The cost is quadratic in both noise and ambition: halving the smallest change you care about quadruples the calls you need. A query with CV = 1 needs about 390 calls per window to see +20% reliably, and about 1,570 to see +10%.</p>
<p>This is the asymptotic normal approximation for means, and it ignores the search of §7, so it is optimistic for small windows. Treat it as a lower bound when choosing <code>--min-executions</code> and the window length.</p>
</div>
<div class="pgrr-lab" data-w="power">
<svg class="pgrr-plot" data-r="chart" viewBox="0 0 520 250" role="img" aria-label="Calls needed per window as a function of the relative change to detect"></svg>
<dl class="pgrr-params">
<div><dt>Per-call CV</dt><dd data-o="cv"></dd></div>
<div><dt>Smallest change δ</dt><dd data-o="d"></dd></div>
</dl>
<dl class="pgrr-kv" data-r="kv"></dl>
</div>
</div>

## <span class="pgrr-kick">Extension</span>From "it changed" to *"it caused"*<span class="pgrr-sub">The seven steps say a query moved near a deploy. Attribution asks what it would have done without it.</span> { #attribution .pgrr-title data-toc-label="Attribution" }

<div class="pgrr-sec pgrr-sec--wide">
<div class="pgrr-text">
<h3><span class="pgrr-sn">§9</span>The counterfactual, and evidence that can be read at any time</h3>
<p>The opt-in <a href="https://joao00001.github.io/pg-regression-radar/latest/attribution-engine/">attribution engine</a> models the log of each query's mean latency as a local level plus a regression on control series (queries the deploy did not touch, or the same query on a canary's stable arm) plus a step at the deploy time <math><msub><mi>t</mi><mi>d</mi></msub></math>:</p>
<div class="pgrr-eq"><math display="block"><mrow><msub><mi>y</mi><mi>t</mi></msub><mo>=</mo><msub><mi>μ</mi><mi>t</mi></msub><mo>+</mo><msup><mi>β</mi><mo>′</mo></msup><msub><mi>x</mi><mi>t</mi></msub><mo>+</mo><mi>θ</mi><mspace width="0.2em"></mspace><mn>1</mn><mo>[</mo><mi>t</mi><mo>≥</mo><msub><mi>t</mi><mi>d</mi></msub><mo>]</mo><mo>+</mo><msub><mi>ε</mi><mi>t</mi></msub><mo>,</mo><mspace width="1em"></mspace><msub><mi>μ</mi><mrow><mi>t</mi><mo>+</mo><mn>1</mn></mrow></msub><mo>=</mo><msub><mi>μ</mi><mi>t</mi></msub><mo>+</mo><msub><mi>η</mi><mi>t</mi></msub></mrow></math></div>
<p>The step <math><mi>θ</mi></math> is estimated inside the Kalman filter, and the filter's innovations accumulate a score <math><mi>S</mi></math> and an information <math><mi>I</mi></math> about it. What the engine tests is not <math><mrow><mi>θ</mi><mo>=</mo><mn>0</mn></mrow></math> but the composite null <math><mrow><mi>θ</mi><mo>≤</mo><mi>δ</mi></mrow></math>, with <math><mrow><mi>δ</mi><mo>=</mo><mi>ln</mi><mn>1.2</mn></mrow></math> (a 20% margin), so a small real change is not an alarm. A half-normal mixture over the effect gives an e-process in closed form, with <math><mrow><mi>a</mi><mo>=</mo><mi>S</mi><mo>−</mo><mi>δ</mi><mi>I</mi></mrow></math> and <math><mrow><mi>b</mi><mo>=</mo><mi>I</mi><mo>+</mo><mn>1</mn><mo>/</mo><msup><mi>τ</mi><mn>2</mn></msup></mrow></math>:</p>
<div class="pgrr-eq"><math display="block"><mrow><mi>Λ</mi><mo>=</mo><mfrac><mn>2</mn><mrow><mi>τ</mi><msqrt><mi>b</mi></msqrt></mrow></mfrac><mspace width="0.2em"></mspace><mi>exp</mi><mo>(</mo><mfrac><msup><mi>a</mi><mn>2</mn></msup><mrow><mn>2</mn><mi>b</mi></mrow></mfrac><mo>)</mo><mspace width="0.2em"></mspace><mi>Φ</mi><mo>(</mo><mfrac><mi>a</mi><msqrt><mi>b</mi></msqrt></mfrac><mo>)</mo></mrow></math></div>
<p>Because <math><mi>Λ</mi></math> is a nonnegative supermartingale under the null, Ville's inequality gives <math><mrow><mi>P</mi><mo>(</mo><msub><mi>sup</mi><mi>t</mi></msub><msub><mi>Λ</mi><mi>t</mi></msub><mo>≥</mo><mn>1</mn><mo>/</mo><mi>α</mi><mo>)</mo><mo>≤</mo><mi>α</mi></mrow></math>: the evidence can be read after every scrape without the optional-stopping inflation that §7 describes for the split search. Across <math><mi>m</mi></math> queries, e-BH rejects the <math><msup><mi>k</mi><mo>*</mo></msup></math> largest e-values, where <math><msup><mi>k</mi><mo>*</mo></msup></math> is the largest <math><mi>k</mi></math> with <math><mrow><msub><mi>e</mi><mrow><mo>(</mo><mi>k</mi><mo>)</mo></mrow></msub><mo>≥</mo><mfrac><mi>m</mi><mrow><mi>α</mi><mi>k</mi></mrow></mfrac></mrow></math>. That controls the false discovery rate at <math><mi>α</mi></math> under arbitrary dependence between queries, which matters because queries share load.</p>
<p>Where the control comes from sets the <em>grade</em>: A for a concurrent arm with parallel pre-trends, B for a synthetic control validated by in-time placebos, C for the query's own history, D for nothing. A rollback is only ever called for at grade A. A cold cache is told from a regression by a persistence test that compares the late post-deploy window with the forecast from the end of the pre-period, which keeps the evidence of a permanent change and shows a decayed transient as none.</p>
<p>On simulated workloads (120 queries; load shocks, statistics refreshes, cold caches, rewrites, concurrent deploys) no scenario with a null deploy produced a grade A or B conviction, and with a control arm about 98% of the affected database time was found. These are simulations, not production traces, and this section has no live widget yet: the engine's tests, not this page, run its arithmetic.</p>
</div>
</div>

## <span class="pgrr-kick">Honesty</span>Where the math *stops*<span class="pgrr-sub">The two assumptions that matter most, and what happens when they fail.</span> { #limits .pgrr-title data-toc-label="Limits" }

<div class="pgrr-limits">
<div><b>Independent intervals</b><p>The t-test and the permutation argument assume the interval means are independent. Serially correlated noise (a slow checkpoint, a cache warming) inflates the false-positive rate, as the selection lab shows with φ &gt; 0.</p></div>
<div><b>Controls, not magic</b><p>Attribution is only as good as its control. <code>pg_stat_statements</code> cannot split a canary from the stable replicas, so without arm-labelled data the best evidence is a synthetic control, and a shift shared by every query is capped at an alert.</p></div>
<div><b>Counters, not calls</b><p>The mean of an interval hides its composition: a shift in the mix of parameter values looks the same as a slower plan. That is why a detection carries a plan diff and an <code>externalCauseSuspected</code> flag.</p></div>
</div>

## <span class="pgrr-kick">Reproducibility</span>The page runs the *engine's* math<span class="pgrr-sub">Not a re-implementation we hope is close.</span> { #reproduce .pgrr-title data-toc-label="Reproduce" }

<div class="pgrr-sec pgrr-sec--wide">
<div class="pgrr-text">
<p>Every widget above calls one dependency-free module, <code>docs/assets/math/math-core.js</code>, a line-by-line port of <code>internal/correlation/engine.go</code>: the interval means, the minimum segment length, the incremental energy statistic, the Lanczos log-gamma, the incomplete beta continued fraction and the decision order of <code>evaluateQuery</code>. A Go test (<code>internal/correlation/mathpage_test.go</code>) feeds fixed series, generated by that module, to the real engine and fails if the verdict, the change point, <math><mi>t</mi></math>, <math><mi>ν</mi></math> or <math><mi>p</mi></math> differ, so the page cannot drift from the code without CI noticing. Noise in the lab is a seeded log-normal per call (so the mean of <math><mi>c</mi></math> calls has coefficient of variation <math><mrow><mtext>CV</mtext><mo>/</mo><msqrt><mi>c</mi></msqrt></mrow></math>), with optional AR(1) correlation, from a fixed seed so every figure is reproducible.</p>
<p>For the full operational description of each stage, flag and CRD field, see the <a href="https://joao00001.github.io/pg-regression-radar/latest/detection-algorithm/">Detection Algorithm</a> and the <a href="https://joao00001.github.io/pg-regression-radar/latest/configuration/">Configuration Reference</a>. To run the engine itself on a workload, open the <a href="https://joao00001.github.io/pg-regression-radar/playground/">Playground</a>.</p>
</div>
</div>

## References { #references }

<ul class="pgrr-refs">
<li id="ref-1"><span class="pgrr-rn">[1]</span>D. S. Matteson and N. A. James, “A nonparametric approach for multiple change point analysis of multivariate data”, <em>JASA</em> 109(505), 2014. <a href="https://doi.org/10.1080/01621459.2013.849605">doi:10.1080/01621459.2013.849605</a></li>
<li id="ref-2"><span class="pgrr-rn">[2]</span>G. J. Székely and M. L. Rizzo, “Energy statistics: a class of statistics based on distances”, <em>Journal of Statistical Planning and Inference</em> 143(8), 2013.</li>
<li id="ref-3"><span class="pgrr-rn">[3]</span>B. L. Welch, “The generalization of ‘Student's’ problem when several different population variances are involved”, <em>Biometrika</em> 34, 1947.</li>
<li id="ref-4"><span class="pgrr-rn">[4]</span>W. H. Press et al., <em>Numerical Recipes</em>, §6.4 “Incomplete beta function” (continued fraction by the modified Lentz method).</li>
<li id="ref-5"><span class="pgrr-rn">[5]</span>T. Sellke, M. J. Bayarri and J. O. Berger, “Calibration of p values for testing precise null hypotheses”, <em>The American Statistician</em> 55(1), 2001.</li>
<li id="ref-6"><span class="pgrr-rn">[6]</span>R. Miller and D. Siegmund, “Maximally selected chi square statistics”, <em>Biometrics</em> 38, 1982; B. Lausen and M. Schumacher, “Maximally selected rank statistics”, <em>Biometrics</em> 48, 1992.</li>
<li id="ref-7"><span class="pgrr-rn">[7]</span>D. Daly et al., “The use of change point detection to identify software performance regressions in a continuous integration system”, <em>ICPE</em> 2020. <a href="https://arxiv.org/abs/2003.00584">arXiv:2003.00584</a></li>
<li id="ref-8"><span class="pgrr-rn">[8]</span>K. H. Brodersen et al., “Inferring causal impact using Bayesian structural time-series models”, <em>Annals of Applied Statistics</em> 9(1), 2015.</li>
<li id="ref-9"><span class="pgrr-rn">[9]</span>A. Abadie, A. Diamond and J. Hainmueller, “Synthetic control methods for comparative case studies”, <em>JASA</em> 105(490), 2010.</li>
<li id="ref-10"><span class="pgrr-rn">[10]</span>A. Ramdas, P. Grünwald, V. Vovk and G. Shafer, “Game-theoretic statistics and safe anytime-valid inference”, <em>Statistical Science</em> 38(4), 2023.</li>
<li id="ref-11"><span class="pgrr-rn">[11]</span>R. Wang and A. Ramdas, “False discovery rate control with e-values”, <em>JRSS B</em> 84(3), 2022.</li>
<li id="ref-12"><span class="pgrr-rn">[12]</span>J. Durbin and S. J. Koopman, <em>Time Series Analysis by State Space Methods</em>, 2nd ed., Oxford University Press, 2012, §6.2.4.</li>
</ul>


</div>
