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
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/httpserver"
	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// RunIngester implements the standalone deploy-event webhook receiver mode:
// it accepts webhook payloads from ArgoCD, Argo Rollouts, and Flux and
// stores normalised DeployEvents which the Correlation Engine can query.
func RunIngester(args []string) int {
	fs := flag.NewFlagSet("ingester", flag.ContinueOnError)
	listen := fs.String("listen", ":8080", "HTTP listen address for webhook endpoint")
	sourceType := fs.String("source-type", "generic", "Webhook source type: argocd, argo-rollouts, flux, generic")
	sourceName := fs.String("source-name", "default", "Unique name for this DeploySource")
	postgresWatchRef := fs.String("postgres-watch-ref", "", "PostgresWatch to associate events with")
	appName := fs.String("app-name", "", "Filter events to a specific application name (empty = all)")
	clusterName := fs.String("cluster-name", "", "Kubernetes cluster identity to stamp on DeployEvents when the webhook payload doesn't carry one (e.g. Argo Rollouts, Flux without eventMetadata)")
	webhookSecret := fs.String("webhook-secret", "", "Shared secret for webhook authentication; when set, every POST to /webhook must include this value in the X-Webhook-Token header (401 otherwise). Recommended for internet-facing deployments. Prefer passing this via an environment variable reference rather than a CLI flag to avoid exposure in process listings.")
	versionFlag := fs.Bool("version", false, "Print version information and exit")
	dryRun := fs.Bool("dry-run", false, "Validate configuration, then exit without starting the webhook server")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *versionFlag {
		fmt.Println(buildinfo.String("ingester"))
		return 0
	}

	if *dryRun {
		if !ingester.ValidSourceTypes[*sourceType] {
			slog.Error("--dry-run: unknown --source-type", "value", *sourceType)
			return 1
		}
		addr, err := net.ResolveTCPAddr("tcp", *listen)
		if err != nil {
			slog.Error("--dry-run: invalid --listen address", "value", *listen, "err", err)
			return 1
		}
		slog.Info("--dry-run: configuration OK", "version", buildinfo.String("ingester"), "listen", addr.String())
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	store := ingester.NewStore()
	source := v1alpha1.DeploySource{
		Name:             *sourceName,
		SourceType:       *sourceType,
		PostgresWatchRef: *postgresWatchRef,
		AppName:          *appName,
		ClusterName:      *clusterName,
		WebhookSecret:    *webhookSecret,
	}

	mux := newIngesterMux(store, source, *webhookSecret, logger)

	// httpserver.New sets conservative timeouts on every server in the
	// project, preventing slow or stalled clients from holding connections
	// open indefinitely (Slowloris-style exhaustion).
	srv := httpserver.New(*listen, mux)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	logger.Info("ingester: listening", "addr", *listen, "source_type", *sourceType)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "err", err)
		return 1
	}
	return 0
}

// newIngesterMux builds the HTTP mux for the ingester. It is separated from
// RunIngester so that tests can construct a mux and exercise its routes with
// httptest without starting a real listener.
//
// /events is only registered when webhookSecret is non-empty. Without a
// secret the route does not exist (404), preventing accidental exposure of
// the full event history when no authentication is in place.
func newIngesterMux(store *ingester.Store, source v1alpha1.DeploySource, webhookSecret string, logger *slog.Logger) *http.ServeMux {
	if logger == nil {
		logger = slog.Default()
	}
	handler := ingester.NewHandler(store, source, logger)

	mux := http.NewServeMux()
	mux.Handle("/webhook", handler)
	if webhookSecret != "" {
		mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get("X-Webhook-Token")
			if subtle.ConstantTimeCompare([]byte(got), []byte(webhookSecret)) != 1 {
				logger.Warn("ingester: rejected /events request with invalid or missing token", "remote_addr", r.RemoteAddr)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(store.All()); err != nil {
				logger.Error("events handler: encode", "err", err)
			}
		})
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}
