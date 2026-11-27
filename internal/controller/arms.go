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

package controller

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	"github.com/joao00001/pg-regression-radar/internal/attribution/adapter"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/pglog"
	"github.com/joao00001/pg-regression-radar/internal/reason"
)

// RolloutHashes reads which pod-template hash is the stable version of a Rollout
// and which is the one being rolled out. *actuation.ArgoRolloutsAborter
// satisfies it.
type RolloutHashes interface {
	PodHashes(ctx context.Context, namespace, name string) (stable, current string, err error)
}

const (
	// armsRetention is how much of the log is kept: the pre-deploy window, the
	// post-deploy window and a margin.
	armsRetention = 4 * time.Hour
	// armsRevalidate is how often the server's settings are read again, so a
	// change to them (or a failover to a differently configured server) is seen.
	armsRevalidate = 5 * time.Minute
	// armsStale is how old the newest log line may be before the log is
	// considered not flowing: the engine then ignores it rather than compare
	// arms on a log that stopped.
	armsStale = 5 * time.Minute
)

// ArmsRuntime reads a Postgres log for one watch and tells the evaluator which
// pods ran which version of a deploy.
type ArmsRuntime struct {
	Store  *pglog.Store
	Hashes RolloutHashes
	Logger *slog.Logger

	usable atomic.Bool // the settings check passed
	now    func() time.Time
}

func (a *ArmsRuntime) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// Usable reports whether the log can be trusted right now: the server's
// settings pass and lines are arriving.
func (a *ArmsRuntime) Usable() bool {
	if !a.usable.Load() {
		return false
	}
	newest := a.Store.Stats().Newest
	return !newest.IsZero() && a.clock().Sub(newest) <= armsStale
}

// ArmsFor is the shadow.Evaluator hook. It returns false, and the engine falls
// back to what pg_stat_statements alone can support, when the log is unusable or
// the deploy has no canary to tell apart: not a Rollout, or already promoted
// (the stable and current hashes are equal).
func (a *ArmsRuntime) ArmsFor(d shadow.Deploy) (adapter.ArmSpec, bool) {
	if !a.Usable() || a.Hashes == nil || d.Namespace == "" || d.App == "" {
		return adapter.ArmSpec{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stable, current, err := a.Hashes.PodHashes(ctx, d.Namespace, d.App)
	if err != nil {
		a.Logger.Warn("attribution: cannot tell the arms of a deploy apart", "reason", reason.ArmsUndistinguished, "deploy", d.ID, "err", err)
		return adapter.ArmSpec{}, false
	}
	if stable == "" || current == "" || stable == current {
		return adapter.ArmSpec{}, false
	}
	return PodArms(d.App, stable, current), true
}

// PodArms tells a canary's pods from the stable ones by name. The pods of an
// Argo Rollouts Rollout are named <rollout>-<pod-template-hash>-<suffix>, and
// the workload is expected to report that name as its application_name
// (PGAPPNAME from the pod's own name).
func PodArms(rollout, stableHash, currentHash string) adapter.ArmSpec {
	return adapter.ArmSpec{
		Treatment: func(app string) bool { return strings.HasPrefix(app, rollout+"-"+currentHash+"-") },
		Control:   func(app string) bool { return strings.HasPrefix(app, rollout+"-"+stableHash+"-") },
	}
}

// startArms starts reading the log for a watch that asked for attribution.arms
// and hooks it into the evaluator. It returns nil, and says why, when the
// configuration cannot work; the engine then runs as it does without it.
func (r *PostgresWatchReconciler) startArms(ctx context.Context, key fmt.Stringer, watch *radarv1alpha1.PostgresWatch, rt *WatchRuntime, scrape time.Duration) *ArmsRuntime {
	cfg := watch.Spec.Attribution.Arms
	if cfg == nil || !cfg.Enabled || rt.Shadow == nil {
		return nil
	}
	fail := func(why string) *ArmsRuntime {
		r.Logger.Error("postgreswatch: attribution.arms disabled, "+why, "reason", reason.ArmsDisabled, "watch", key.String())
		return nil
	}
	if rt.Collector == nil {
		return fail("it needs the direct database connection to check the server's settings, which sampleSource prometheus does not have")
	}
	if (cfg.PodLogs == nil) == (cfg.File == "") {
		return fail("set exactly one of podLogs and file")
	}
	if cfg.File != "" {
		if err := pglog.ValidFilePath(cfg.File); err != nil {
			return fail(err.Error())
		}
	}
	hashes, ok := r.Aborter.(RolloutHashes)
	if !ok || r.Aborter == nil {
		return fail("the manager cannot read Rollouts (enable manager.autoAbort.rbacEnabled)")
	}
	if cfg.PodLogs != nil && r.KubeClient == nil {
		return fail("the manager has no Kubernetes client to read pod logs")
	}

	a := &ArmsRuntime{Store: pglog.NewStore(scrape, armsRetention, 0), Hashes: hashes, Logger: r.Logger}
	rt.Shadow.Arms, rt.Shadow.ArmsFor = a.Store, a.ArmsFor
	a.metrics(rt)

	sink := func(rec pglog.Record) { a.Store.Add(rec) }
	logf := func(format string, args ...any) { r.Logger.Warn(fmt.Sprintf(format, args...), "watch", key.String()) }
	switch {
	case cfg.PodLogs != nil:
		pl := cfg.PodLogs
		container := pl.Container
		if container == "" {
			container = "postgres"
		}
		go pglog.FollowPods(ctx, pl.Namespace, container,
			pglog.KubePodLister(r.KubeClient, pl.Namespace, pl.Selector),
			pglog.KubePodLogs{Client: r.KubeClient}, 15*time.Second, sink, logf)
	default:
		go func() {
			if err := pglog.FollowFile(ctx, cfg.File, 0, sink); err != nil {
				logf("pglog: follow %s: %v", cfg.File, err)
			}
		}()
	}
	go a.maintain(ctx, key.String(), rt.Collector)
	return a
}

// maintain keeps the store pruned and the settings check current.
func (a *ArmsRuntime) maintain(ctx context.Context, watch string, col interface{ DB() *sql.DB }) {
	prune := time.NewTicker(time.Minute)
	defer prune.Stop()
	wait := time.Duration(0)
	var lastOK, seen bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			a.Store.Prune()
		case <-time.After(wait):
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			chk, err := pglog.Validate(cctx, col.DB())
			cancel()
			if err != nil {
				wait = 30 * time.Second
				a.usable.Store(false)
				a.Logger.Warn("attribution.arms: cannot read the server's settings", "reason", reason.ArmsSettingsUnread, "watch", watch, "err", err)
				continue
			}
			wait = armsRevalidate
			a.Store.SetSampling(chk.Sampling)
			a.usable.Store(chk.OK())
			if !seen || chk.OK() != lastOK {
				seen, lastOK = true, chk.OK()
				if chk.OK() {
					a.Logger.Info("attribution.arms: the Postgres log can serve as an arm-labelled source",
						"reason", reason.ArmsUsable, "watch", watch, "sampleRate", chk.Sampling.Rate)
				}
				for _, i := range chk.Issues {
					msg, code := "attribution.arms: the Postgres log cannot be used yet", reason.ArmsSettingsRejected
					if i.Warning {
						msg, code = "attribution.arms: the Postgres log setting costs more than it needs to", ""
					}
					attrs := []any{"watch", watch}
					if code != "" {
						attrs = append(attrs, "reason", code)
					}
					a.Logger.Warn(msg, append(attrs,
						"setting", i.Setting, "value", i.Got, "problem", i.Problem, "fix", i.Fix)...)
				}
			}
		}
	}
}

func (a *ArmsRuntime) metrics(rt *WatchRuntime) {
	reg := rt.PromRegistry
	if reg == nil {
		return
	}
	lbl := prometheus.Labels{"cluster": rt.ClusterName}
	reg.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "pg_regression_radar", Name: "attribution_arms_usable", ConstLabels: lbl,
			Help: "1 when the Postgres log is flowing and its settings allow arm-labelled evidence (grade A)."}, func() float64 {
			if a.Usable() {
				return 1
			}
			return 0
		}),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "pg_regression_radar", Name: "attribution_arms_records_total", ConstLabels: lbl,
			Help: "Statement executions read from the Postgres log."}, func() float64 { return float64(a.Store.Stats().Records) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "pg_regression_radar", Name: "attribution_arms_dropped_total", ConstLabels: lbl,
			Help: "Statement executions dropped because too many distinct application_name and query pairs were in the log."}, func() float64 { return float64(a.Store.Stats().Dropped) }),
	)
}
