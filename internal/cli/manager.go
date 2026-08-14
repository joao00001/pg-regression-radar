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
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/actuation"
	"github.com/joao00001/pg-regression-radar/internal/buildinfo"
	"github.com/joao00001/pg-regression-radar/internal/controller"
)

var (
	managerScheme   = runtime.NewScheme()
	managerSetupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(managerScheme))
	utilruntime.Must(corev1.AddToScheme(managerScheme))
	utilruntime.Must(radarv1alpha1.AddToScheme(managerScheme))
}

// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// RunManager implements the highly-available, Kubernetes-native mode for
// pg-regression-radar: it runs a controller-runtime Manager that reconciles
// PostgresWatch, DeploySource, and PerformanceRegression CRDs, starting one
// Collector+Correlation Engine worker per PostgresWatch and one webhook
// route per DeploySource — the same engine packages RunOperator wires up
// directly, but now driven by Kubernetes objects instead of CLI flags, and
// with leader election so multiple replicas can run for HA (only the
// elected leader does any work; standbys take over via a
// coordination.k8s.io Lease on failover).
func RunManager(args []string) int {
	fs := flag.NewFlagSet("manager", flag.ContinueOnError)
	var metricsAddr string
	var probeAddr string
	var pgMetricsAddr string
	var webhookAddr string
	var enableLeaderElection bool
	var leaderElectionNamespace string
	var versionFlag bool
	var dryRun bool

	fs.StringVar(&metricsAddr, "metrics-bind-address", "0",
		"Address the controller-runtime metrics endpoint binds to (reconcile/workqueue metrics). "+
			"Use :8443 for HTTPS, :8080 for HTTP, or 0 to disable.")
	fs.StringVar(&probeAddr, "health-probe-bind-address", ":8081",
		"Address the liveness/readiness probe endpoint binds to.")
	fs.StringVar(&pgMetricsAddr, "pg-metrics-bind-address", ":9090",
		"Address the aggregated Postgres query metrics endpoint (one Collector's worth per active "+
			"PostgresWatch) binds to. Served only by the leader.")
	fs.StringVar(&webhookAddr, "webhook-bind-address", ":8080",
		"Address the deploy-event webhook listener (one route per DeploySource CR) binds to. "+
			"Served only by the leader.")
	fs.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. Enabling this ensures there is only one "+
			"active set of PostgresWatch/DeploySource workers across all replicas, with automatic "+
			"failover via a coordination.k8s.io Lease. Disable only for local development.")
	fs.StringVar(&leaderElectionNamespace, "leader-election-namespace", "",
		"Namespace the leader election Lease is created in. Defaults to the manager's own "+
			"namespace when running in-cluster (via the downward API / in-cluster config).")
	fs.BoolVar(&versionFlag, "version", false, "Print version information and exit")
	fs.BoolVar(&dryRun, "dry-run", false,
		"Validate that a Kubernetes API server config can be resolved (in-cluster or via "+
			"kubeconfig), then exit without starting the manager.")
	opts := zap.Options{Development: true}
	opts.BindFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if versionFlag {
		fmt.Println(buildinfo.String("manager"))
		return 0
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := ctrl.GetConfig()
	if err != nil {
		managerSetupLog.Error(err, "unable to resolve Kubernetes API server config")
		return 1
	}

	if dryRun {
		logger.Info("--dry-run: configuration OK", "version", buildinfo.String("manager"), "apiServerHost", cfg.Host)
		return 0
	}

	statusClient, err := client.New(cfg, client.Options{Scheme: managerScheme})
	if err != nil {
		managerSetupLog.Error(err, "unable to build direct Kubernetes client for startup failure reporting")
		statusClient = nil
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: managerScheme,
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
		managerSetupLog.Error(err, "unable to start manager")
		return 1
	}

	registry := controller.NewRegistry()
	mux := controller.NewDynamicMux()

	// Backs auto-abort (PostgresWatch.spec.autoAbort — see
	// docs/auto-abort.md): a plain dynamic client is enough since this
	// manager only ever needs to set one status field on a Rollout, not
	// vendor Argo Rollouts' own Go types. A failure here is not fatal to
	// startup — it only means auto-abort silently has nothing to call
	// (PostgresWatchReconciler.Aborter stays nil, and startWatch already
	// treats a nil Aborter as "auto-abort unavailable" rather than
	// panicking) — everything else this manager does (detection, alerting,
	// the webhook/native-watch ingestion paths) is unaffected.
	var aborter controller.RolloutAborter
	if dynClient, err := dynamic.NewForConfig(cfg); err != nil {
		managerSetupLog.Error(err, "unable to build dynamic client; PostgresWatch.spec.autoAbort will have no effect")
	} else {
		aborter = actuation.NewArgoRolloutsAborter(dynClient)
	}

	if err := (&controller.PostgresWatchReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Registry: registry,
		Logger:   logger,
		Aborter:  aborter,
	}).SetupWithManager(mgr); err != nil {
		managerSetupLog.Error(err, "unable to create controller", "controller", "PostgresWatch")
		return 1
	}

	if err := (&controller.DeploySourceReconciler{
		Client:   mgr.GetClient(),
		Registry: registry,
		Mux:      mux,
		Logger:   logger,
	}).SetupWithManager(mgr); err != nil {
		managerSetupLog.Error(err, "unable to create controller", "controller", "DeploySource")
		return 1
	}

	if err := (&controller.WorkloadWatchReconciler{
		Client:   mgr.GetClient(),
		Registry: registry,
		Logger:   logger,
	}).SetupWithManager(mgr); err != nil {
		managerSetupLog.Error(err, "unable to create controller", "controller", "WorkloadWatch")
		return 1
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		managerSetupLog.Error(err, "unable to set up health check")
		return 1
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		managerSetupLog.Error(err, "unable to set up ready check")
		return 1
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
		managerSetupLog.Error(err, "unable to add webhook server")
		return 1
	}

	pgMetricsMux := http.NewServeMux()
	pgMetricsMux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if err := mgr.Add(&httpRunnable{
		addr:    pgMetricsAddr,
		handler: pgMetricsMux,
		name:    "pg-metrics",
		logger:  logger,
	}); err != nil {
		managerSetupLog.Error(err, "unable to add pg-metrics server")
		return 1
	}

	managerSetupLog.Info("starting manager", "leaderElection", enableLeaderElection)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		if statusClient != nil {
			statusCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if markErr := reportManagerStartupFailureStatus(statusCtx, statusClient, err); markErr != nil {
				managerSetupLog.Error(markErr, "unable to report manager startup failure in custom resource status")
			}
		}
		managerSetupLog.Error(err, "problem running manager")
		return 1
	}
	return 0
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
