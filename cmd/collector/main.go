// collector is the pg-regression-radar metric scraper binary.
// It scrapes pg_stat_statements from a Postgres cluster and exposes the data as
// Prometheus metrics.
//
// Usage:
//
//	collector \
//	  --dsn "******host:5432/dbname?sslmode=disable" \
//	  --scrape-interval 60s \
//	  --cluster-name my-cluster \
//	  --namespace production \
//	  --listen :9090
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

	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	dsn := flag.String("dsn", "", "Postgres DSN (required)")
	scrapeInterval := flag.Duration("scrape-interval", 60*time.Second, "Scrape interval")
	clusterName := flag.String("cluster-name", "default", "CloudNativePG cluster name")
	namespace := flag.String("namespace", "default", "Kubernetes namespace")
	listen := flag.String("listen", ":9090", "Prometheus metrics listen address")
	flag.Parse()

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	reg := prometheus.NewRegistry()

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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		logger.Info("collector: metrics server listening", "addr", *listen)
		if err := http.ListenAndServe(*listen, mux); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
	}()

	if err := col.Run(ctx); err != nil {
		logger.Error("collector exited with error", "err", err)
		os.Exit(1)
	}
}
