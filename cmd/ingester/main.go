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
//	  --app-name my-app
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/joao00001/pg-regression-radar/internal/ingester"
	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address for webhook endpoint")
	sourceType := flag.String("source-type", "generic", "Webhook source type: argocd, argo-rollouts, flux, generic")
	sourceName := flag.String("source-name", "default", "Unique name for this DeploySource")
	postgresWatchRef := flag.String("postgres-watch-ref", "", "PostgresWatch to associate events with")
	appName := flag.String("app-name", "", "Filter events to a specific application name (empty = all)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	store := &ingester.Store{}
	source := v1alpha1.DeploySource{
		Name:             *sourceName,
		SourceType:       *sourceType,
		PostgresWatchRef: *postgresWatchRef,
		AppName:          *appName,
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
