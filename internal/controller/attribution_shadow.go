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
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/revert"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// shadowEvaluations are the offsets after a deploy at which the attribution
// engine judges it. Measured on simulated workloads, the engine cannot convict
// before about 15 minutes (its persistence check needs a late window), so the
// first evaluations only establish that nothing is wrong yet; from 15 minutes on,
// each one can confirm the last, which the policy needs before it would act on
// anything above an alert. The verdict endpoint serves the latest of them.
var shadowEvaluations = []time.Duration{
	10 * time.Minute, 15 * time.Minute, 20 * time.Minute, 25 * time.Minute,
	30 * time.Minute, 35 * time.Minute, 40 * time.Minute, 45 * time.Minute,
}

// shadowTracker schedules the attribution evaluations of every drained deploy
// and runs them off the poll goroutine, since one assessment can take a while on
// a large workload. It logs, counts, and keeps the result for the verdict
// endpoint. It writes to the cluster only through the optional ActuateRuntime
// (pause or abort an Argo Rollouts rollout), and to GitHub only through the
// optional revert proposer; with neither it is read-only.
type shadowTracker struct {
	ev     *shadow.Evaluator
	logger *slog.Logger
	count  *prometheus.CounterVec

	// Optional draft-revert proposals. rv is nil when none is configured.
	rv             *revert.Proposer
	enforce        bool
	includeQueries bool
	revertCount    *prometheus.CounterVec

	// Optional pause/abort of the rollout behind a convicted deploy. act is nil
	// when none is configured.
	act      *ActuateRuntime
	actCount *prometheus.CounterVec
	verCount *prometheus.CounterVec

	mu   sync.Mutex
	jobs []*shadowJob
	busy bool
	// acted is the strongest action already taken on a deploy, so a repeated
	// evaluation never patches the same Rollout twice; skipped marks deploys
	// that are not an Argo Rollouts rollout, so they are not looked up again.
	acted   map[string]attribution.Action
	skipped map[string]bool
	// verify are the checks owed after an abort: did the regression go away?
	verify []verifyJob
}

// verifyJob is a check of a deploy at a time, after the Rollout was aborted.
type verifyJob struct {
	deploy shadow.Deploy
	event  dto.DeployEvent
	at     time.Time
}

// verifyAfter is how long after an abort the engine checks whether the
// regression is gone. The canary's traffic is back on the stable version by
// then, and the engine's persistence check has a late window to read.
const verifyAfter = 15 * time.Minute

type shadowJob struct {
	deploy shadow.Deploy
	event  dto.DeployEvent
	due    []time.Time
}

func newShadowTracker(ev *shadow.Evaluator, logger *slog.Logger, reg prometheus.Registerer, cluster string) *shadowTracker {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "pg_regression_radar",
		Name:        "attribution_shadow_decisions_total",
		Help:        "Decisions the attribution engine would have taken, in shadow mode (never executed).",
		ConstLabels: prometheus.Labels{"cluster": cluster},
	}, []string{"action", "grade"})
	reg.MustRegister(c)
	rc := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "pg_regression_radar",
		Name:        "attribution_revert_proposals_total",
		Help:        "Draft revert pull requests the attribution engine opened, or declined to open, by result.",
		ConstLabels: prometheus.Labels{"cluster": cluster},
	}, []string{"result"})
	reg.MustRegister(rc)
	ac := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "pg_regression_radar",
		Name:        "attribution_actions_total",
		Help:        "Pauses and aborts of an Argo Rollouts rollout taken on an attribution decision, by action and result.",
		ConstLabels: prometheus.Labels{"cluster": cluster},
	}, []string{"action", "result"})
	reg.MustRegister(ac)
	vc := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace:   "pg_regression_radar",
		Name:        "attribution_verifications_total",
		Help:        "Checks made after an abort of whether the regression went away: recovered, or persisted (the action did not help).",
		ConstLabels: prometheus.Labels{"cluster": cluster},
	}, []string{"result"})
	reg.MustRegister(vc)
	if ev != nil {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Namespace:   "pg_regression_radar",
			Name:        "attribution_breaker_open",
			Help:        "1 when the attribution circuit breaker is open and the engine only alerts, because earlier actions did not help.",
			ConstLabels: prometheus.Labels{"cluster": cluster},
		}, func() float64 {
			if ev.BreakerOpen() {
				return 1
			}
			return 0
		}))
	}
	return &shadowTracker{ev: ev, logger: logger, count: c, revertCount: rc, actCount: ac, verCount: vc,
		acted: map[string]attribution.Action{}, skipped: map[string]bool{}}
}

// withActuation lets the tracker pause or abort the Argo Rollouts rollout of a
// deploy the engine has held or rolled back. It only ever acts on a decision
// the policy marked as enforced.
func (s *shadowTracker) withActuation(a *ActuateRuntime) { s.act = a }

// withRevert enables draft revert proposals. In enforce mode a convicted deploy
// gets a pull request; otherwise the pull request that would be opened is only
// logged.
func (s *shadowTracker) withRevert(p *revert.Proposer, enforce, includeQueries bool) {
	s.rv, s.enforce, s.includeQueries = p, enforce, includeQueries
}

// add schedules the evaluations of a newly drained deploy.
func (s *shadowTracker) add(e dto.DeployEvent) {
	d := shadow.Deploy{ID: e.ID, Namespace: e.Namespace, App: e.App, Revision: e.Revision, At: e.Timestamp,
		HasRollbackTarget: s.ev.HasPrior(e.Namespace, e.App, e.Timestamp)}
	s.ev.Register(d)
	j := &shadowJob{deploy: d, event: e}
	for _, o := range shadowEvaluations {
		j.due = append(j.due, e.Timestamp.Add(o))
	}
	s.mu.Lock()
	s.jobs = append(s.jobs, j)
	s.mu.Unlock()
}

// tick runs every evaluation and post-action check that has come due, in the
// background.
func (s *shadowTracker) tick(now time.Time) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return
	}
	type work struct {
		d  shadow.Deploy
		ev dto.DeployEvent
	}
	var todo []work
	live := s.jobs[:0]
	for _, j := range s.jobs {
		for len(j.due) > 0 && !now.Before(j.due[0]) {
			j.due = j.due[1:]
			todo = append(todo, work{j.deploy, j.event})
		}
		if len(j.due) > 0 {
			live = append(live, j)
		}
	}
	s.jobs = live
	var checks []verifyJob
	pending := s.verify[:0]
	for _, v := range s.verify {
		if now.Before(v.at) {
			pending = append(pending, v)
		} else {
			checks = append(checks, v)
		}
	}
	s.verify = pending
	if len(todo) == 0 && len(checks) == 0 {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()

	go func() {
		defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
		for _, w := range todo {
			rec, ok := s.ev.Evaluate(now, w.d)
			if !ok {
				continue
			}
			d := rec.Decision
			s.count.WithLabelValues(string(d.Action), string(d.Grade)).Inc()
			msg := "attribution (shadow): decision, not executed"
			if d.Enforced {
				msg = "attribution (enforce): verdict served to the rollout controller"
			}
			s.logger.Info(msg,
				"deploy", w.d.ID, "health", string(rec.Assessment.Health),
				"action", string(d.Action), "verdict", string(d.Verdict()), "wanted", string(d.Wanted), "grade", string(d.Grade),
				"impact", d.Impact, "attribution", rec.Assessment.Attribution, "reasons", d.Reasons)
			s.maybeActuate(w.ev, rec, now)
			s.maybePropose(w.d, rec)
		}
		for _, c := range checks {
			s.verifyAbort(c, now)
		}
	}()
}

// verifyAbort checks, after an abort, whether the regression is gone and tells
// the policy, which counts an action that did not help and, after enough of them,
// opens the circuit breaker so the engine goes back to alerting only.
func (s *shadowTracker) verifyAbort(c verifyJob, now time.Time) {
	v, ok := s.ev.Verify(now, c.deploy)
	if !ok {
		return
	}
	result := "recovered"
	if !v.Effective {
		result = "persisted"
	}
	s.verCount.WithLabelValues(result).Inc()
	s.record(c.event, "abort", result, v.Reason, now)
	if s.ev.BreakerOpen() {
		s.logger.Warn("attribution: circuit breaker open, the engine will only alert until it is reset",
			"deploy", c.deploy.ID, "last", v.Reason)
	} else {
		s.logger.Info("attribution: checked the abort", "deploy", c.deploy.ID, "result", result, "reason", v.Reason)
	}
}

// record adds an entry to the evaluator's action ledger.
func (s *shadowTracker) record(ev dto.DeployEvent, action, result, detail string, at time.Time) {
	if s.ev == nil {
		return
	}
	s.ev.RecordAction(shadow.ActionRecord{At: at, DeployID: ev.ID, Namespace: ev.Namespace, App: ev.App,
		Action: action, Result: result, Detail: detail})
}

// maybePropose opens a draft revert pull request for a deploy the policy has
// convicted at hold or above. It runs on every such evaluation, which is safe:
// the proposer acts at most once per deploy, and a failed attempt is retried at
// the next evaluation.
func (s *shadowTracker) maybePropose(d shadow.Deploy, rec shadow.Record) {
	if s.rv == nil || rec.Decision.Action.Rank() < attribution.ActionHold.Rank() {
		return
	}
	in := revert.Input{
		DeployID: d.ID,
		Revision: d.Revision,
		Body: revert.Diagnosis(revert.DiagnosisInput{
			App: d.App, Namespace: d.Namespace, Revision: d.Revision,
			Assessment: rec.Assessment, Decision: rec.Decision,
			PriorDeploySeen: d.HasRollbackTarget, IncludeQueries: s.includeQueries,
		}),
	}
	if !s.enforce || !rec.Decision.Enforced {
		s.logger.Info("attribution (shadow): would open a draft revert pull request, not opened",
			"deploy", d.ID, "revision", d.Revision, "grade", string(rec.Decision.Grade))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res := s.rv.Propose(ctx, in)
	s.revertCount.WithLabelValues(string(res.Outcome)).Inc()
	s.logger.Info("attribution: draft revert pull request",
		"deploy", d.ID, "outcome", string(res.Outcome), "reason", res.Reason, "url", res.URL)
}

// maybeActuate pauses or aborts the Argo Rollouts rollout behind a deploy the
// policy has held or rolled back, when the watch is allowed to. A hold pauses
// the rollout (spec.paused), a rollback aborts it (status.abort); nothing is
// resumed, rolled back to a revision, or deleted, so a person always decides
// what happens next. It runs on every evaluation, which is safe: each action is
// taken at most once per deploy, a later and stronger decision still escalates
// from a pause to an abort, and a failed call is retried at the next evaluation.
func (s *shadowTracker) maybeActuate(ev dto.DeployEvent, rec shadow.Record, now time.Time) {
	if s.act == nil || !rec.Decision.Enforced {
		return
	}
	act := rec.Decision.Action
	if act != attribution.ActionHold && act != attribution.ActionRollback {
		return
	}
	s.mu.Lock()
	done, skip := s.acted[ev.ID], s.skipped[ev.ID]
	s.mu.Unlock()
	if skip || done.Rank() >= act.Rank() {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if len(s.act.Namespaces) > 0 && !contains(s.act.Namespaces, ev.Namespace) {
		s.mu.Lock()
		s.skipped[ev.ID] = true
		s.mu.Unlock()
		s.actCount.WithLabelValues(actionName(act), "skipped").Inc()
		s.record(ev, actionName(act), "skipped", "namespace not in attribution.actuate.namespaces", now)
		s.logger.Info("attribution: namespace not allowed for actuation, nothing paused or aborted",
			"deploy", ev.ID, "namespace", ev.Namespace, "action", string(act))
		return
	}
	if s.act.Paused != nil && s.act.Paused(ctx) {
		// Not remembered: lifting the annotation lets the next evaluation act.
		s.actCount.WithLabelValues(actionName(act), "skipped").Inc()
		s.record(ev, actionName(act), "skipped", "actuation paused by annotation", now)
		s.logger.Warn("attribution: actuation paused by annotation, nothing paused or aborted",
			"deploy", ev.ID, "action", string(act))
		return
	}
	if s.act.IsRollout != nil && !s.act.IsRollout(ctx, ev) {
		s.mu.Lock()
		s.skipped[ev.ID] = true
		s.mu.Unlock()
		s.actCount.WithLabelValues(actionName(act), "skipped").Inc()
		s.record(ev, actionName(act), "skipped", "not an Argo Rollouts deploy", now)
		s.logger.Info("attribution: not an Argo Rollouts deploy, nothing to pause or abort",
			"deploy", ev.ID, "source", ev.Source, "action", string(act))
		return
	}

	var err error
	switch act {
	case attribution.ActionHold:
		if s.act.Pauser == nil {
			return
		}
		err = s.act.Pauser.Pause(ctx, ev.Namespace, ev.App)
	case attribution.ActionRollback:
		if s.act.Aborter == nil {
			return
		}
		err = s.act.Aborter.Abort(ctx, ev.Namespace, ev.App)
	}
	if err != nil {
		s.actCount.WithLabelValues(actionName(act), "failed").Inc()
		s.record(ev, actionName(act), "failed", err.Error(), now)
		s.logger.Error("attribution: could not act on the rollout, will retry at the next evaluation",
			"deploy", ev.ID, "rollout", ev.Namespace+"/"+ev.App, "action", actionName(act), "err", err)
		return
	}
	s.mu.Lock()
	s.acted[ev.ID] = act
	if act == attribution.ActionRollback {
		s.verify = append(s.verify, verifyJob{deploy: s.deployOf(ev), event: ev, at: now.Add(verifyAfter)})
	}
	s.mu.Unlock()
	s.actCount.WithLabelValues(actionName(act), "done").Inc()
	s.record(ev, actionName(act), "done", strings.Join(rec.Decision.Reasons, "; "), now)
	s.logger.Info("attribution: acted on the rollout",
		"deploy", ev.ID, "rollout", ev.Namespace+"/"+ev.App, "action", actionName(act),
		"grade", string(rec.Decision.Grade), "impact", rec.Decision.Impact, "reasons", rec.Decision.Reasons)
}

// actionName is the rollout operation an engine action stands for.
func actionName(a attribution.Action) string {
	if a == attribution.ActionRollback {
		return "abort"
	}
	return "pause"
}

// deployOf is the shadow.Deploy the tracker registered for ev, found among its
// jobs; the event itself is used when the job has already run out of evaluations.
func (s *shadowTracker) deployOf(ev dto.DeployEvent) shadow.Deploy {
	for _, j := range s.jobs {
		if j.deploy.ID == ev.ID {
			return j.deploy
		}
	}
	return shadow.Deploy{ID: ev.ID, Namespace: ev.Namespace, App: ev.App, Revision: ev.Revision, At: ev.Timestamp}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
