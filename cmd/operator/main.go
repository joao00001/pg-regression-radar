// operator is the DeployLens operator binary.
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
//	  --latency-threshold 0.20
package main

import (
	"context"
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
	flag.Parse()

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	reg := prometheus.NewRegistry()

	// ---- Collector ----
	col, err := collector.New(collector.Config{
		DSN:            *dsn,
		ScrapeInterval: *scrapeInterval,
		ClusterName:    *clusterName,
		Namespace:      *namespace,
	}, logger, reg)
	if err != nil {
		logger.Error("failed to create collector", "err", err)
		os.Exit(1)
	}

	// ---- Ingester ----
	store := &ingester.Store{}
	source := v1alpha1.DeploySource{
		Name:       "operator-default",
		SourceType: *sourceType,
	}
	webhookHandler := ingester.NewHandler(store, source, logger)

	// ---- Correlation Engine ----
	engine := correlation.New(correlation.Config{
		WindowMinutes:          *windowMinutes,
		MinExecutions:          *minExecutions,
		LatencyChangeThreshold: *latencyThreshold,
	}, col, logger)

	// ---- Alerting ----
	notifier := alerting.NewWebhookNotifier(alerting.WebhookConfig{
		URL:         *slackURL,
		ClusterName: *clusterName,
	}, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	// ---- Deploy event processor ----
	// Poll the ingester store for new events and run correlation analysis.
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
