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
	"flag"
	"fmt"
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
	"github.com/joao00001/pg-regression-radar/internal/ingester"
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
func RunOperator(args []string) {
	fs := flag.NewFlagSet("operator", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (required)")
	clusterName := fs.String("cluster-name", "default", "CloudNativePG cluster name label")
	namespace := fs.String("namespace", "default", "Kubernetes namespace label")
	scrapeInterval := fs.Duration("scrape-interval", 60*time.Second, "Collector scrape interval")
	webhookListen := fs.String("webhook-listen", ":8080", "HTTP listen address for deploy webhooks")
	metricsListen := fs.String("metrics-listen", ":9090", "HTTP listen address for Prometheus metrics")
	slackURL := fs.String("slack-url", "", "Slack incoming-webhook URL for notifications")
	sourceType := fs.String("source-type", "generic", "Deploy source type: argocd, argo-rollouts, flux, generic")
	windowMinutes := fs.Int("window-minutes", 30, "Analysis window (minutes before/after deploy)")
	minExecutions := fs.Int64("min-executions", 10, "Minimum query executions per window")
	latencyThreshold := fs.Float64("latency-threshold", 0.20, "Minimum relative latency increase to flag")
	changePointTolerance := fs.Duration("changepoint-tolerance", 0, "Max distance between the E-divisive change point and the deploy timestamp still attributed to that deploy (0 = auto: 20% of window, floor 2m)")
	retentionMinutes := fs.Int("retention-minutes", 180, "How long (minutes) the collector retains in-memory query samples before pruning them; should stay well above 2x --window-minutes")
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
	_ = fs.Parse(args)

	if *versionFlag {
		fmt.Println(buildinfo.String("operator"))
		return
	}

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	reg := prometheus.NewRegistry()

	// ---- Collector ----
	col, err := collector.New(collector.Config{
		DSN:               *dsn,
		ScrapeInterval:    *scrapeInterval,
		ClusterName:       *clusterName,
		Namespace:         *namespace,
		RetentionDuration: time.Duration(*retentionMinutes) * time.Minute,
	}, logger, reg)
	if err != nil {
		logger.Error("failed to create collector", "err", err)
		os.Exit(1)
	}

	if *dryRun {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if !ingester.ValidSourceTypes[*sourceType] {
			logger.Error("--dry-run: unknown --source-type", "value", *sourceType)
			os.Exit(1)
		}
		if err := col.Ping(ctx); err != nil {
			logger.Error("--dry-run: collector ping failed", "err", err)
			os.Exit(1)
		}
		if *stateBackend == "postgres" {
			stateConnDSN := *stateDSN
			if stateConnDSN == "" {
				stateConnDSN = *dsn
			}
			stateDB, err := postgres.Open(ctx, stateConnDSN)
			if err != nil {
				logger.Error("--dry-run: postgres state backend connection failed", "err", err)
				os.Exit(1)
			}
			stateDB.Close()
		} else if *stateBackend != "" && *stateBackend != "memory" {
			logger.Error("--dry-run: unknown --state-backend (want memory or postgres)", "value", *stateBackend)
			os.Exit(1)
		}
		logger.Info("--dry-run: configuration and connectivity OK", "version", buildinfo.String("operator"))
		return
	}

	// ---- Ingester ----
	store := &ingester.Store{}
	source := v1alpha1.DeploySource{
		Name:        "operator-default",
		SourceType:  *sourceType,
		ClusterName: *clusterName,
	}
	webhookHandler := ingester.NewHandler(store, source, logger)

	// ---- Correlation Engine ----
	engine := correlation.New(correlation.Config{
		WindowMinutes:          *windowMinutes,
		MinExecutions:          *minExecutions,
		LatencyChangeThreshold: *latencyThreshold,
		ChangePointTolerance:   *changePointTolerance,
	}, col, logger)

	// ---- Alerting ----
	notifier := alerting.NewWebhookNotifier(alerting.WebhookConfig{
		URL:         *slackURL,
		ClusterName: *clusterName,
	}, logger)

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
			os.Exit(1)
		}
		stateDB = db
		pgSamples := postgres.NewSampleStore(db)
		pgEvents := postgres.NewEventStore(db)
		sampleStore, eventStore = pgSamples, pgEvents

		go storage.RunPruneLoop(ctx, "query_samples", pgSamples, *statePruneInterval, *stateRetention, logger)
		go storage.RunPruneLoop(ctx, "deploy_events", pgEvents, *statePruneInterval, *stateRetention, logger)

		logger.Info("operator: postgres state backend enabled",
			"schema", postgres.SchemaName,
			"retention", *stateRetention,
			"prune_interval", *statePruneInterval,
			"separate_state_dsn", *stateDSN != "")
	default:
		logger.Error("unknown --state-backend (want memory or postgres)", "value", *stateBackend)
		os.Exit(1)
	}
	if stateDB != nil {
		defer stateDB.Close()
	}

	// ---- HTTP servers ----
	webhookMux := http.NewServeMux()
	webhookMux.Handle("/webhook", webhookHandler)
	webhookMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	go func() {
		logger.Info("operator: webhook server listening", "addr", *webhookListen)
		if err := http.ListenAndServe(*webhookListen, webhookMux); err != nil && err != http.ErrServerClosed {
			logger.Error("webhook server error", "err", err)
		}
	}()
	go func() {
		logger.Info("operator: metrics server listening", "addr", *metricsListen)
		if err := http.ListenAndServe(*metricsListen, metricsMux); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
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
	go func() {
		var cursor int
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
					results := engine.Analyse(ev)
					for _, r := range results {
						if r.Status == v1alpha1.StatusDetected {
							if err := notifier.Notify(ctx, r); err != nil {
								logger.Error("operator: notify failed", "err", err, "regression", r.Name)
							}
						}
					}
				}
			}
		}
	}()

	// ---- Collector (blocking) ----
	if err := col.Run(ctx); err != nil {
		logger.Error("collector exited with error", "err", err)
		os.Exit(1)
	}
}
