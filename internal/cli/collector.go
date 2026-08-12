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

package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RunCollector implements the standalone collector mode: it scrapes
// pg_stat_statements from a Postgres cluster and exposes the data as
// Prometheus metrics, without the correlation/alerting/webhook pieces.
func RunCollector(args []string) {
	fs := flag.NewFlagSet("collector", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (required)")
	scrapeInterval := fs.Duration("scrape-interval", 60*time.Second, "Scrape interval")
	clusterName := fs.String("cluster-name", "default", "CloudNativePG cluster name")
	namespace := fs.String("namespace", "default", "Kubernetes namespace")
	listen := fs.String("listen", ":9090", "Prometheus metrics listen address")
	retentionMinutes := fs.Int("retention-minutes", 180, "How long (minutes) to retain in-memory query samples before pruning them; should stay well above the correlation window(s) analysed against this data")
	versionFlag := fs.Bool("version", false, "Print version information and exit")
	dryRun := fs.Bool("dry-run", false, "Validate configuration and Postgres connectivity, then exit without starting any servers")
	_ = fs.Parse(args)

	if *versionFlag {
		fmt.Println(buildinfo.String("collector"))
		return
	}

	if *dsn == "" {
		slog.Error("--dsn is required")
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	reg := prometheus.NewRegistry()

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
		if err := col.Ping(ctx); err != nil {
			logger.Error("--dry-run: collector ping failed", "err", err)
			os.Exit(1)
		}
		logger.Info("--dry-run: configuration and connectivity OK", "version", buildinfo.String("collector"))
		return
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
