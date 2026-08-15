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
	"errors"
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
	"github.com/joao00001/pg-regression-radar/internal/httpserver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RunCollector implements the standalone collector mode: it scrapes
// pg_stat_statements from a Postgres cluster and exposes the data as
// Prometheus metrics, without the correlation/alerting/webhook pieces.
func RunCollector(args []string) int {
	fs := flag.NewFlagSet("collector", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (required)")
	scrapeInterval := fs.Duration("scrape-interval", 60*time.Second, "Scrape interval")
	clusterName := fs.String("cluster-name", "default", "CloudNativePG cluster name")
	namespace := fs.String("namespace", "default", "Kubernetes namespace")
	listen := fs.String("listen", ":9090", "Prometheus metrics listen address")
	maxQueryTextLen := fs.Int("max-query-text-len", 200, "Max query text length (characters) stored per sample before truncation for alerting/fingerprinting")
	retentionMinutes := fs.Int("retention-minutes", 180, "How long (minutes) to retain in-memory query samples before pruning them; should stay well above the correlation window(s) analysed against this data")
	versionFlag := fs.Bool("version", false, "Print version information and exit")
	dryRun := fs.Bool("dry-run", false, "Validate configuration and Postgres connectivity, then exit without starting any servers")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *versionFlag {
		fmt.Println(buildinfo.String("collector"))
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

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	reg := prometheus.NewRegistry()

	col, err := collector.New(collector.Config{
		DSN:               *dsn,
		ScrapeInterval:    *scrapeInterval,
		ClusterName:       *clusterName,
		Namespace:         *namespace,
		MaxQueryTextLen:   *maxQueryTextLen,
		RetentionDuration: time.Duration(*retentionMinutes) * time.Minute,
	}, logger, reg)
	if err != nil {
		logger.Error("failed to create collector", "err", err)
		return 1
	}

	if *dryRun {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := col.Ping(ctx); err != nil {
			logger.Error("--dry-run: collector ping failed", "err", err)
			return 1
		}
		logger.Info("--dry-run: configuration and connectivity OK", "version", buildinfo.String("collector"))
		return 0
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		srv := httpserver.New(*listen, mux)
		logger.Info("collector: metrics server listening", "addr", *listen)
		errCh := make(chan error, 1)
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				logger.Error("metrics server shutdown error", "err", err)
			}
		case err := <-errCh:
			if err != nil {
				logger.Error("metrics server error", "err", err)
			}
		}
	}()

	if err := col.Run(ctx); err != nil {
		logger.Error("collector exited with error", "err", err)
		return 1
	}
	return 0
}
