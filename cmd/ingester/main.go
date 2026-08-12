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

// ingester is the pg-regression-radar deploy-event webhook receiver binary.
// It accepts webhook payloads from ArgoCD, Argo Rollouts, and Flux and stores
// normalised DeployEvents which the Correlation Engine can query.
//
// Usage:
//
//	ingester \
//	  --listen :8080 \
//	  --source-type argocd \
//	  --source-name prod-argocd \
//	  --postgres-watch-ref prod-watch \
//	  --app-name my-app \
//	  --cluster-name prod-cluster-1
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address for webhook endpoint")
	sourceType := flag.String("source-type", "generic", "Webhook source type: argocd, argo-rollouts, flux, generic")
	sourceName := flag.String("source-name", "default", "Unique name for this DeploySource")
	postgresWatchRef := flag.String("postgres-watch-ref", "", "PostgresWatch to associate events with")
	appName := flag.String("app-name", "", "Filter events to a specific application name (empty = all)")
	clusterName := flag.String("cluster-name", "", "Kubernetes cluster identity to stamp on DeployEvents when the webhook payload doesn't carry one (e.g. Argo Rollouts, Flux without eventMetadata)")
	versionFlag := flag.Bool("version", false, "Print version information and exit")
	dryRun := flag.Bool("dry-run", false, "Validate configuration, then exit without starting the webhook server")
	flag.Parse()

	if *versionFlag {
		fmt.Println(buildinfo.String("ingester"))
		return
	}

	if *dryRun {
		if !ingester.ValidSourceTypes[*sourceType] {
			slog.Error("--dry-run: unknown --source-type", "value", *sourceType)
			os.Exit(1)
		}
		addr, err := net.ResolveTCPAddr("tcp", *listen)
		if err != nil {
			slog.Error("--dry-run: invalid --listen address", "value", *listen, "err", err)
			os.Exit(1)
		}
		slog.Info("--dry-run: configuration OK", "version", buildinfo.String("ingester"), "listen", addr.String())
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	store := &ingester.Store{}
	source := v1alpha1.DeploySource{
		Name:             *sourceName,
		SourceType:       *sourceType,
		PostgresWatchRef: *postgresWatchRef,
		AppName:          *appName,
		ClusterName:      *clusterName,
	}

	handler := ingester.NewHandler(store, source, logger)

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(store.All()); err != nil {
			logger.Error("events handler: encode", "err", err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: *listen, Handler: mux}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	logger.Info("ingester: listening", "addr", *listen, "source_type", *sourceType)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}
