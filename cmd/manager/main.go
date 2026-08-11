// manager is the highly-available, Kubernetes-native entrypoint for
// pg-regression-radar: it runs a controller-runtime Manager that reconciles
// PostgresWatch, DeploySource, and PerformanceRegression CRDs, starting one
// Collector+Correlation Engine worker per PostgresWatch and one webhook
// route per DeploySource — the same engine packages cmd/operator/main.go
// wires up directly, but now driven by Kubernetes objects instead of CLI
// flags, and with leader election so multiple replicas can run for HA
// (only the elected leader does any work; standbys take over via a
// coordination.k8s.io Lease on failover).
//
// cmd/operator remains the simple, CRD-free, single-process/single-DSN
// mode documented in the README; cmd/manager is the additional,
// recommended-for-production mode. See README.md, section "Two ways to
// run pg-regression-radar".
//
// Usage:
//
//	manager \
//	  --metrics-bind-address=:9090 \
//	  --health-probe-bind-address=:8081 \
//	  --pg-metrics-bind-address=:9091 \
//	  --webhook-bind-address=:8080 \
//	  --leader-elect=true \
//	  --leader-election-namespace=pg-regression-radar
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(radarv1alpha1.AddToScheme(scheme))
}

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func main() {
	var metricsAddr string
	var probeAddr string
	var pgMetricsAddr string
	var webhookAddr string
	var enableLeaderElection bool
	var leaderElectionNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0",
		"Address the controller-runtime metrics endpoint binds to (reconcile/workqueue metrics). "+
			"Use :8443 for HTTPS, :8080 for HTTP, or 0 to disable.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"Address the liveness/readiness probe endpoint binds to.")
	flag.StringVar(&pgMetricsAddr, "pg-metrics-bind-address", ":9090",
		"Address the aggregated Postgres query metrics endpoint (one Collector's worth per active "+
			"PostgresWatch) binds to. Served only by the leader.")
	flag.StringVar(&webhookAddr, "webhook-bind-address", ":8080",
		"Address the deploy-event webhook listener (one route per DeploySource CR) binds to. "+
			"Served only by the leader.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. Enabling this ensures there is only one "+
			"active set of PostgresWatch/DeploySource workers across all replicas, with automatic "+
			"failover via a coordination.k8s.io Lease. Disable only for local development.")
	flag.StringVar(&leaderElectionNamespace, "leader-election-namespace", "",
		"Namespace the leader election Lease is created in. Defaults to the manager's own "+
			"namespace when running in-cluster (via the downward API / in-cluster config).")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,

		// Leader election: exactly one replica reconciles at a time. On
		// that replica's failure, the Lease expires after LeaseDuration
		// and a standby acquires it, so PostgresWatch/DeploySource workers
		// resume automatically without operator intervention. See
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/manager
		// and https://book.kubebuilder.io/cronjob-tutorial/empty-main.html
		// for the option semantics used here.
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "pg-regression-radar-manager.radar.pgregressionradar.io",
		LeaderElectionNamespace: leaderElectionNamespace,
		// The default resource lock is "leases" (coordination.k8s.io),
		// which is the only lock kind still supported by client-go; we
		// rely on that default rather than pinning
		// LeaderElectionResourceLock explicitly.
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	registry := controller.NewRegistry()
	mux := controller.NewDynamicMux()

	if err := (&controller.PostgresWatchReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Registry: registry,
		Logger:   logger,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PostgresWatch")
		os.Exit(1)
	}

	if err := (&controller.DeploySourceReconciler{
		Client:   mgr.GetClient(),
		Registry: registry,
		Mux:      mux,
		Logger:   logger,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DeploySource")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// The webhook listener and the aggregated Postgres metrics listener
	// both depend entirely on state (Registry, Mux) that only the leader
	// populates, so both are gated behind leader election via
	// NeedLeaderElection() — controller-runtime starts them only once this
	// replica becomes leader, and stops them (by cancelling their
	// context) on step-down. Non-leader replicas stay idle and ready to
	// take over.
	if err := mgr.Add(&httpRunnable{
		addr:    webhookAddr,
		handler: mux,
		name:    "webhook",
		logger:  logger,
	}); err != nil {
		setupLog.Error(err, "unable to add webhook server")
		os.Exit(1)
	}

	pgMetricsMux := http.NewServeMux()
	pgMetricsMux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if err := mgr.Add(&httpRunnable{
		addr:    pgMetricsAddr,
		handler: pgMetricsMux,
		name:    "pg-metrics",
		logger:  logger,
	}); err != nil {
		setupLog.Error(err, "unable to add pg-metrics server")
		os.Exit(1)
	}

	setupLog.Info("starting manager", "leaderElection", enableLeaderElection)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// httpRunnable adapts a plain net/http.Handler into a
// manager.Runnable + manager.LeaderElectionRunnable so controller-runtime
// starts it exactly like it starts the built-in controllers: only once
// this process is leader (or immediately, if leader election is disabled),
// and shut down cleanly when the manager's context is cancelled.
type httpRunnable struct {
	addr    string
	handler http.Handler
	name    string
	logger  *slog.Logger
}

// Start implements manager.Runnable.
func (h *httpRunnable) Start(ctx context.Context) error {
	srv := &http.Server{Addr: h.addr, Handler: h.handler}

	errCh := make(chan error, 1)
	go func() {
		h.logger.Info("http server listening", "server", h.name, "addr", h.addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("%s server: %w", h.name, err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("%s server shutdown: %w", h.name, err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: both the
// webhook listener and the pg-metrics listener only have anything useful
// to serve on the leader (Registry/Mux are only populated by the
// reconcilers, which only run on the leader), so gate them the same way.
func (h *httpRunnable) NeedLeaderElection() bool {
	return true
}
