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

// Package cli holds the actual flag-parsing and wiring logic behind each of
// pg-regression-radar's four run modes (operator, manager, collector,
// ingester). It exists so that logic is written exactly once but reachable
// from two kinds of binaries:
//
//   - the single, unified `pg-regression-radar` CLI (cmd/pg-regression-radar),
//     which dispatches to these Run* functions based on its first
//     argument — this is what `go install
//     .../cmd/pg-regression-radar@latest` gives you, the simplest way to
//     install the whole project as one command;
//   - the four standalone binaries (cmd/operator, cmd/manager,
//     cmd/collector, cmd/ingester), kept as thin wrappers around the same
//     Run* functions specifically so the existing Dockerfile targets and
//     Helm chart image references (which each expect a single-purpose
//     binary as PID 1 of its own container) don't have to change.
//
// Each Run* function owns its own flag.FlagSet rather than the package-level
// flag.CommandLine, so that (a) it behaves identically whether invoked as
// `pg-regression-radar operator ...` or as the standalone `operator ...`,
// and (b) nothing here quietly depends on being the only flag parser in the
// process.
package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/httpserver"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/internal/planner"
	"github.com/joao00001/pg-regression-radar/internal/storage"
	"github.com/joao00001/pg-regression-radar/internal/storage/postgres"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RunOperator implements the pg-regression-radar all-in-one mode: it wires
// together the Collector, Deploy Event Ingester, Correlation Engine, and
// Alerting components into a single process suitable for running in
// Kubernetes as a Deployment. See docs/getting-started.md for full usage.
func RunOperator(args []string) int {
	return runOperator(args, os.Stdout)
}

func runOperator(args []string, logOutput io.Writer) int {
	fs := flag.NewFlagSet("operator", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (required)")
	clusterName := fs.String("cluster-name", "default", "CloudNativePG cluster name label")
	namespace := fs.String("namespace", "default", "Kubernetes namespace label")
	scrapeInterval := fs.Duration("scrape-interval", 60*time.Second, "Collector scrape interval")
	webhookListen := fs.String("webhook-listen", ":8080", "HTTP listen address for deploy webhooks")
	metricsListen := fs.String("metrics-listen", ":9090", "HTTP listen address for Prometheus metrics")
	slackURL := fs.String("slack-url", "", "Slack incoming-webhook URL for notifications (alias of --alert-url with --alert-format=slack, the default)")
	alertFormat := fs.String("alert-format", "slack", "Notification payload format: slack, teams, pagerduty, or custom — see docs/alerting.md")
	alertURL := fs.String("alert-url", "", "Webhook URL for --alert-format=slack/teams/custom; ignored for pagerduty. Falls back to --slack-url when unset")
	pagerdutyRoutingKey := fs.String("pagerduty-routing-key", "", "PagerDuty Events API v2 routing key; required when --alert-format=pagerduty")
	alertTemplate := fs.String("alert-template", "", "Go text/template source (inline) for --alert-format=custom — see docs/alerting.md#custom-format. Alternative to --alert-template-file; takes precedence when both are set")
	alertTemplateFile := fs.String("alert-template-file", "", "Path to a Go text/template file for --alert-format=custom, when passing the source inline via --alert-template isn't convenient")
	sourceType := fs.String("source-type", "generic", "Deploy source type: argocd, argo-rollouts, flux, generic")
	webhookSecret := fs.String("webhook-secret", "", "Shared secret for webhook authentication; when set, every POST to /webhook must include this value in the X-Webhook-Token header (401 otherwise). Recommended for internet-facing deployments. Prefer passing this via an environment variable reference rather than a CLI flag to avoid exposure in process listings.")
	windowMinutes := fs.Int("window-minutes", 30, "Analysis window (minutes before/after deploy)")
	minExecutions := fs.Int64("min-executions", 10, "Minimum query executions per window")
	latencyThreshold := fs.Float64("latency-threshold", 0.20, "Minimum relative latency increase to flag")
	changePointTolerance := fs.Duration("changepoint-tolerance", 0, "Max distance between the E-divisive change point and the deploy timestamp still attributed to that deploy (0 = auto: 20% of window, floor 2m)")
	maxQueryTextLen := fs.Int("max-query-text-len", 200, "Max query text length (characters) stored per sample before truncation for alerting/fingerprinting")
	retentionMinutes := fs.Int("retention-minutes", 180, "How long (minutes) the collector retains in-memory query samples before pruning them; should stay well above 2x --window-minutes")
	capturePlans := fs.Bool("capture-plans", false, "Capture periodic EXPLAIN (GENERIC_PLAN) plan snapshots for tracked queries and attach a plan-diff summary to detected regressions; requires PostgreSQL 16+ (no-op, logged once, on older servers) and adds one extra planner invocation per tracked query per scrape cycle — see docs/detection-algorithm.md")
	// ---- Periodic (deploy-independent) detection — see docs/periodic-detection.md ----
	// Off by default: this is a materially different, less deploy-anchored
	// kind of detection than the rest of this project's default behaviour,
	// with a real false-positive risk from ordinary traffic variation that
	// hasn't been validated against production traffic yet — see ADR-0001
	// (docs/adr/0001-deploy-independent-regression-detection.md).
	periodicDetection := fs.Bool("periodic-detection", false, "Also run regression detection on a rolling schedule, independent of any tracked deploy — see docs/periodic-detection.md")
	periodicWindowMinutes := fs.Int("periodic-window-minutes", 60, "Rolling window --periodic-detection splits in half (recent vs. previous) to look for a regression with no deploy to anchor to")
	periodicIntervalMinutes := fs.Int("periodic-interval-minutes", 15, "How often a full --periodic-detection pass runs over every tracked query")
	// ---- Persistence (see internal/storage) ----
	// "memory" preserves today's behaviour exactly: samples/events live only
	// in the Collector/Ingester in-process maps and are lost on restart.
	// "postgres" additionally persists them so history survives pod restarts
	// and can be shared across replicas (NOT by itself safe for multiple
	// *active* replicas — see internal/storage's package doc on leader
	// election). Kept additive and defaulted to "memory" so the documented
	// quick-start keeps working unchanged.
	stateBackend := fs.String("state-backend", "memory", "State persistence backend: memory (default) or postgres")
	stateDSN := fs.String("state-dsn", "", "Postgres DSN for the state backend (default: reuse --dsn, i.e. store state in the same monitored Postgres; set this to point state at a separate Postgres instance instead)")
	stateRetention := fs.Duration("state-retention", 7*24*time.Hour, "How long to retain samples/events in the postgres state backend before pruning")
	statePruneInterval := fs.Duration("state-prune-interval", 15*time.Minute, "How often to sweep the postgres state backend for records older than --state-retention")
	versionFlag := fs.Bool("version", false, "Print version information and exit")
	dryRun := fs.Bool("dry-run", false, "Validate configuration and Postgres connectivity, then exit without starting any servers")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *versionFlag {
		fmt.Println(buildinfo.String("operator"))
		return 0
	}

	if *dsn == "" {
		slog.Error("--dsn is required")
		return 1
	}
	if *maxQueryTextLen <= 0 {
		slog.Error("--max-query-text-len must be positive", "value", *maxQueryTextLen)
		return 1
	}

	logger := slog.New(slog.NewJSONHandler(logOutput, nil))
	reg := prometheus.NewRegistry()

	if err := validatePostgresDSN(*dsn); err != nil {
		logger.Error("invalid --dsn", "err", err)
		return 1
	}
	if *stateBackend == "postgres" && *stateDSN != "" {
		if err := validatePostgresDSN(*stateDSN); err != nil {
			logger.Error("invalid --state-dsn", "err", err)
			return 1
		}
	}

	// ---- Alerting config (validated below, in --dry-run and for real) ----
	// --alert-url falls back to the older --slack-url when unset, so
	// existing --slack-url-only invocations keep working unchanged with the
	// default --alert-format=slack.
	resolvedAlertURL := *alertURL
	if resolvedAlertURL == "" {
		resolvedAlertURL = *slackURL
	}
	customAlertTemplate := *alertTemplate
	if customAlertTemplate == "" && *alertTemplateFile != "" {
		b, err := os.ReadFile(*alertTemplateFile)
		if err != nil {
			logger.Error("failed to read --alert-template-file", "err", err)
			return 1
		}
		customAlertTemplate = string(b)
	}
	alertCfg := alerting.BuildConfig{
		Format:              *alertFormat,
		URL:                 resolvedAlertURL,
		PagerDutyRoutingKey: *pagerdutyRoutingKey,
		CustomTemplate:      customAlertTemplate,
		ClusterName:         *clusterName,
	}

	// ---- Collector ----
	col, err := collector.New(collector.Config{
		DSN:               *dsn,
		ScrapeInterval:    *scrapeInterval,
		ClusterName:       *clusterName,
		Namespace:         *namespace,
		MaxQueryTextLen:   *maxQueryTextLen,
		RetentionDuration: time.Duration(*retentionMinutes) * time.Minute,
		CapturePlans:      *capturePlans,
	}, logger, reg)
	if err != nil {
		logger.Error("failed to create collector", "err", err)
		return 1
	}

	if *dryRun {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if !ingester.ValidSourceTypes[*sourceType] {
			logger.Error("--dry-run: unknown --source-type", "value", *sourceType)
			return 1
		}
		if _, err := alerting.BuildNotifier(alertCfg, logger, reg); err != nil {
			logger.Error("--dry-run: invalid alerting configuration", "err", err)
			return 1
		}
		if *periodicDetection {
			if *periodicWindowMinutes <= 0 {
				logger.Error("--dry-run: --periodic-window-minutes must be positive", "value", *periodicWindowMinutes)
				return 1
			}
			if *periodicIntervalMinutes <= 0 {
				logger.Error("--dry-run: --periodic-interval-minutes must be positive", "value", *periodicIntervalMinutes)
				return 1
			}
		}
		if err := col.Ping(ctx); err != nil {
			logger.Error("--dry-run: collector ping failed", "err", err)
			return 1
		}
		if *stateBackend == "postgres" {
			stateConnDSN := *stateDSN
			if stateConnDSN == "" {
				stateConnDSN = *dsn
			}
			stateDB, err := postgres.Open(ctx, stateConnDSN)
			if err != nil {
				logger.Error("--dry-run: postgres state backend connection failed", "err", err)
				return 1
			}
			_ = stateDB.Close()
		} else if *stateBackend != "" && *stateBackend != "memory" {
			logger.Error("--dry-run: unknown --state-backend (want memory or postgres)", "value", *stateBackend)
			return 1
		}
		logger.Info("--dry-run: configuration and connectivity OK", "version", buildinfo.String("operator"))
		return 0
	}

	// ---- Ingester ----
	store := &ingester.Store{}
	source := v1alpha1.DeploySource{
		Name:          "operator-default",
		SourceType:    *sourceType,
		ClusterName:   *clusterName,
		WebhookSecret: *webhookSecret,
	}
	webhookHandler := ingester.NewHandler(store, source, logger)

	// ---- Correlation Engine ----
	engine := correlation.New(correlation.Config{
		Namespace:              *namespace,
		WindowMinutes:          *windowMinutes,
		MinExecutions:          *minExecutions,
		LatencyChangeThreshold: *latencyThreshold,
		ChangePointTolerance:   *changePointTolerance,
		PeriodicWindowMinutes:  *periodicWindowMinutes,
	}, col, logger)

	// ---- Alerting ----
	notifier, err := alerting.BuildNotifier(alertCfg, logger, reg)
	if err != nil {
		logger.Error("failed to configure alerting", "err", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ---- State persistence backend ----
	// sampleStore/eventStore stay nil for the "memory" default: the Collector
	// and Ingester already keep equivalent state in process memory, so there
	// is nothing extra to wire up and zero behaviour change from before this
	// flag existed. They're only populated for "postgres", in which case a
	// couple of small bridge goroutines below copy freshly observed
	// samples/events into the durable store (Collector/Ingester themselves
	// are out of scope for this change — see internal/storage's package doc).
	var (
		sampleStore storage.SampleStore
		eventStore  storage.EventStore
		stateDB     *sql.DB
	)
	// initialCursor seeds the deploy-event poll loop's DrainSince cursor below.
	// It stays 0 (today's behaviour) unless the postgres backend backfills
	// history into `store`, in which case it's advanced past the backfilled
	// events — see the Backfill call below for why that matters.
	initialCursor := 0
	switch *stateBackend {
	case "", "memory":
		// Nothing to do — see comment above.
	case "postgres":
		stateConnDSN := *stateDSN
		if stateConnDSN == "" {
			stateConnDSN = *dsn // reuse the monitored Postgres DSN by default
		}
		db, err := postgres.Open(ctx, stateConnDSN)
		if err != nil {
			logger.Error("failed to initialise postgres state backend", "err", err)
			return 1
		}
		stateDB = db
		pgSamples := postgres.NewSampleStore(db)
		pgEvents := postgres.NewEventStore(db)
		sampleStore, eventStore = pgSamples, pgEvents

		// ---- Backfill: seed the in-memory Collector/Ingester history from
		// the durable store before anything else starts, so a restarted
		// process doesn't present a cold in-memory view to the correlation
		// engine while its Postgres history is actually intact — see
		// docs/persistence.md ("Backfill on startup").
		since := time.Now().UTC().Add(-time.Duration(*retentionMinutes) * time.Minute)
		backfillCtx, backfillCancel := context.WithTimeout(ctx, 30*time.Second)
		qids, err := pgSamples.AllQueryIDs(backfillCtx)
		if err != nil {
			logger.Warn("operator: backfill: list query ids failed, starting with cold sample history", "err", err)
		} else {
			var samples []collector.QuerySample
			for _, qid := range qids {
				s, err := pgSamples.SamplesInRange(backfillCtx, qid, since, time.Now().UTC())
				if err != nil {
					logger.Warn("operator: backfill: load samples failed", "query_id", qid, "err", err)
					continue
				}
				samples = append(samples, s...)
			}
			col.Backfill(samples)
			logger.Info("operator: backfilled collector sample history", "samples", len(samples), "query_ids", len(qids))
		}

		events, err := pgEvents.EventsInRange(backfillCtx, since, time.Now().UTC())
		if err != nil {
			logger.Warn("operator: backfill: load events failed, starting with cold event history", "err", err)
			events = nil
		}
		backfillCancel()

		// Advancing the cursor past the backfilled events is not optional:
		// DrainSince treats anything past its cursor as newly-arrived work to
		// analyse and potentially alert on (see the poll loop below). Without
		// this, the very first DrainSince(0) call would treat every
		// backfilled (already-handled-in-a-previous-process-lifetime) deploy
		// event as brand new, re-running correlation and potentially
		// re-sending duplicate Slack alerts for regressions already reported
		// before the restart.
		initialCursor = store.Backfill(events)
		logger.Info("operator: backfilled event history", "events", len(events), "cursor", initialCursor)

		go storage.RunPruneLoop(ctx, "query_samples", pgSamples, *statePruneInterval, *stateRetention, logger)
		go storage.RunPruneLoop(ctx, "deploy_events", pgEvents, *statePruneInterval, *stateRetention, logger)

		logger.Info("operator: postgres state backend enabled",
			"schema", postgres.SchemaName,
			"retention", *stateRetention,
			"prune_interval", *statePruneInterval,
			"separate_state_dsn", *stateDSN != "")
	default:
		logger.Error("unknown --state-backend (want memory or postgres)", "value", *stateBackend)
		return 1
	}
	if stateDB != nil {
		defer func() { _ = stateDB.Close() }()
	}

	// ---- HTTP servers ----
	webhookMux := http.NewServeMux()
	webhookMux.Handle("/webhook", webhookHandler)
	webhookMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	// httpserver.New sets conservative timeouts on every server in the
	// project, preventing slow or stalled clients from holding connections
	// open indefinitely (Slowloris-style exhaustion).
	webhookSrv := httpserver.New(*webhookListen, webhookMux)
	metricsSrv := httpserver.New(*metricsListen, metricsMux)

	go func() {
		logger.Info("operator: webhook server listening", "addr", *webhookListen)
		if err := webhookSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("webhook server error", "err", err)
		}
	}()
	go func() {
		logger.Info("operator: metrics server listening", "addr", *metricsListen)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = webhookSrv.Shutdown(shutdownCtx)
		_ = metricsSrv.Shutdown(shutdownCtx)
	}()

	// If a durable SampleStore is configured, mirror newly scraped samples
	// into it. The Collector keeps scraping/serving the correlation engine
	// out of its own in-memory map exactly as before; this just gives those
	// same observations a copy that survives a restart. Only exported
	// Collector methods are used here, so this needs no changes to
	// internal/collector.
	if sampleStore != nil {
		go func() {
			lastSync := time.Now().UTC()
			ticker := time.NewTicker(*scrapeInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					now := time.Now().UTC()
					for _, qid := range col.AllQueryIDs() {
						for _, s := range col.SamplesInRange(qid, lastSync, now) {
							if err := sampleStore.Append(ctx, s); err != nil {
								logger.Error("operator: persist sample failed", "err", err, "query_id", qid)
							}
						}
					}
					lastSync = now
				}
			}
		}()
	}

	// Poll the ingester store every 5 s; a channel-based push would require
	// locking changes across packages, so polling keeps the coupling minimal.
	// DrainSince uses a cursor (the count of already-processed events) so
	// each tick copies only newly arrived events and releases the lock
	// immediately, avoiding repeated full-slice copies and lock contention.
	//
	// A newly-arrived event is registered with pending rather than analysed
	// once and discarded: a real deploy webhook fires the moment the deploy
	// completes, well before --scrape-interval/--min-executions could have
	// accumulated enough post-deploy samples, so an immediate one-shot
	// Analyse call almost always sees StatusInsufficientData and, with no
	// retry, a real regression could go unreported entirely. pending.Tick
	// keeps re-running Analyse for every event still inside its analysis
	// window each tick, so a regression that only becomes statistically
	// visible several minutes into the post-deploy window is still caught —
	// see internal/correlation.PendingSet's doc comment for how this gap was
	// actually found (running this binary for real, not via the test suite).
	pending := correlation.NewPendingSet(engine, logger)
	// Exposes PendingSet.Len() as a gauge so a long-running operator's memory
	// use here is directly observable rather than just "trusted by design":
	// PendingSet retires every deploy event once its analysis window
	// elapses (see PendingSet's doc comment), so this should track deploy
	// frequency × --window-minutes, not grow without bound over days/weeks
	// of uptime — if it ever does, that's a real bug this metric would
	// actually surface.
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: "pg_regression_radar",
		Name:      "pending_deploy_events",
		Help:      "Deploy events still under active retry, waiting for their analysis window to close.",
		ConstLabels: prometheus.Labels{
			"cluster":   *clusterName,
			"namespace": *namespace,
		},
	}, func() float64 { return float64(pending.Len()) }))
	go func() {
		cursor := initialCursor
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var evs []v1alpha1.DeployEvent
				evs, cursor = store.DrainSince(cursor)
				for _, ev := range evs {
					// Mirror into the durable EventStore, if configured (see
					// the SampleStore bridge above for the same rationale).
					if eventStore != nil {
						if err := eventStore.Add(ctx, ev); err != nil {
							logger.Error("operator: persist event failed", "err", err, "event_id", ev.ID)
						}
					}
					pending.Add(ev)
				}

				for _, tr := range pending.Tick() {
					r := tr.Regression
					// Attach a plan-diff hint, if plan capture is enabled:
					// PlansAround returns nil/nil when CapturePlans is off,
					// the server is below PostgreSQL 16, or capture hasn't
					// produced anything for this queryid yet, in which case
					// planner.Diff's nil-safe handling leaves
					// PlanDiffSummary at its empty default.
					if *capturePlans {
						before, after := col.PlansAround(r.QueryID, r.DetectedChangeAt)
						r.PlanDiffSummary = planner.Diff(before, after)
					}
					notifier.ObserveDetectedRegression(r)
					if err := notifier.Notify(ctx, r); err != nil {
						logger.Error("operator: notify failed", "err", err, "regression", r.Name)
					}
				}
			}
		}
	}()

	// ---- Periodic (deploy-independent) detection — see docs/periodic-detection.md ----
	// A separate goroutine and ticker from the deploy-triggered poll loop
	// above: this one runs on its own --periodic-interval-minutes cadence,
	// entirely independent of whether (or how often) deploy events arrive,
	// and calls AnalysePeriodic (via PeriodicTracker) rather than Analyse.
	if *periodicDetection {
		tracker := correlation.NewPeriodicTracker(engine, logger)
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace: "pg_regression_radar",
			Name:      "periodic_regressions_in_progress",
			Help:      "Queries currently suppressed under an already-reported, still-ongoing periodic regression episode.",
			ConstLabels: prometheus.Labels{
				"cluster":   *clusterName,
				"namespace": *namespace,
			},
		}, func() float64 { return float64(tracker.Len()) }))

		go func() {
			ticker := time.NewTicker(time.Duration(*periodicIntervalMinutes) * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					for _, r := range tracker.Tick(time.Now().UTC()) {
						if *capturePlans {
							before, after := col.PlansAround(r.QueryID, r.DetectedChangeAt)
							r.PlanDiffSummary = planner.Diff(before, after)
						}
						notifier.ObserveDetectedRegression(r)
						if err := notifier.Notify(ctx, r); err != nil {
							logger.Error("operator: periodic notify failed", "err", err, "regression", r.Name)
						}
					}
				}
			}
		}()
	}

	// ---- Collector (blocking) ----
	if err := col.Run(ctx); err != nil {
		logger.Error("collector exited with error", "err", err)
		return 1
	}
	return 0
}
