// operator is the pg-regression-radar all-in-one binary.
// It wires together the Collector, Deploy Event Ingester, Correlation Engine,
// and Alerting components into a single process suitable for running in
// Kubernetes as a Deployment.
//
// Usage:
//
//	operator \
//	  --dsn "host:5432/dbname?sslmode=disable" \
//	  --cluster-name my-cluster \
//	  --namespace production \
//	  --webhook-listen :8080 \
//	  --metrics-listen :9090 \
//	  --slack-url https://hooks.slack.com/... \
//	  --window-minutes 30 \
//	  --min-executions 10 \
//	  --latency-threshold 0.20 \
//	  --changepoint-tolerance 6m \
//	  --retention-minutes 180 \
//	  --state-backend postgres \
//	  --state-dsn "host:5432/dbname?sslmode=disable"
package main

import (
	"context"
	"database/sql"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/alerting"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/correlation"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/internal/storage"
	"github.com/joao00001/pg-regression-radar/internal/storage/postgres"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	dsn := flag.String("dsn", "", "Postgres DSN (required)")
	clusterName := flag.String("cluster-name", "default", "CloudNativePG cluster name label")
	namespace := flag.String("namespace", "default", "Kubernetes namespace label")
	scrapeInterval := flag.Duration("scrape-interval", 60*time.Second, "Collector scrape interval")
	webhookListen := flag.String("webhook-listen", ":8080", "HTTP listen address for deploy webhooks")
	metricsListen := flag.String("metrics-listen", ":9090", "HTTP listen address for Prometheus metrics")
	slackURL := flag.String("slack-url", "", "Slack incoming-webhook URL for notifications")
	sourceType := flag.String("source-type", "generic", "Deploy source type: argocd, argo-rollouts, flux, generic")
	windowMinutes := flag.Int("window-minutes", 30, "Analysis window (minutes before/after deploy)")
	minExecutions := flag.Int64("min-executions", 10, "Minimum query executions per window")
	latencyThreshold := flag.Float64("latency-threshold", 0.20, "Minimum relative latency increase to flag")
	changePointTolerance := flag.Duration("changepoint-tolerance", 0, "Max distance between the E-divisive change point and the deploy timestamp still attributed to that deploy (0 = auto: 20% of window, floor 2m)")
	retentionMinutes := flag.Int("retention-minutes", 180, "How long (minutes) the collector retains in-memory query samples before pruning them; should stay well above 2x --window-minutes")
	// ---- Persistence (see internal/storage) ----
	// "memory" preserves today's behaviour exactly: samples/events live only
	// in the Collector/Ingester in-process maps and are lost on restart.
	// "postgres" additionally persists them so history survives pod restarts
	// and can be shared across replicas (NOT by itself safe for multiple
	// *active* replicas — see internal/storage's package doc on leader
	// election). Kept additive and defaulted to "memory" so the documented
	// quick-start keeps working unchanged.
	stateBackend := flag.String("state-backend", "memory", "State persistence backend: memory (default) or postgres")
	stateDSN := flag.String("state-dsn", "", "Postgres DSN for the state backend (default: reuse --dsn, i.e. store state in the same monitored Postgres; set this to point state at a separate Postgres instance instead)")
	stateRetention := flag.Duration("state-retention", 7*24*time.Hour, "How long to retain samples/events in the postgres state backend before pruning")
	statePruneInterval := flag.Duration("state-prune-interval", 15*time.Minute, "How often to sweep the postgres state backend for records older than --state-retention")
	flag.Parse()

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
	go func() {
		seen := map[string]struct{}{}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, ev := range store.All() {
					if _, ok := seen[ev.ID]; ok {
						continue
					}
					seen[ev.ID] = struct{}{}
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
