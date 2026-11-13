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
// endpoint; this manager never writes to the cluster because of it.
type shadowTracker struct {
	ev     *shadow.Evaluator
	logger *slog.Logger
	count  *prometheus.CounterVec

	// Optional draft-revert proposals. rv is nil when none is configured.
	rv             *revert.Proposer
	enforce        bool
	includeQueries bool
	revertCount    *prometheus.CounterVec

	mu   sync.Mutex
	jobs []*shadowJob
	busy bool
}

type shadowJob struct {
	deploy shadow.Deploy
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
	return &shadowTracker{ev: ev, logger: logger, count: c, revertCount: rc}
}

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
	j := &shadowJob{deploy: d}
	for _, o := range shadowEvaluations {
		j.due = append(j.due, e.Timestamp.Add(o))
	}
	s.mu.Lock()
	s.jobs = append(s.jobs, j)
	s.mu.Unlock()
}

// tick runs every evaluation that has come due, in the background.
func (s *shadowTracker) tick(now time.Time) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return
	}
	type work struct {
		d shadow.Deploy
	}
	var todo []work
	live := s.jobs[:0]
	for _, j := range s.jobs {
		for len(j.due) > 0 && !now.Before(j.due[0]) {
			j.due = j.due[1:]
			todo = append(todo, work{j.deploy})
		}
		if len(j.due) > 0 {
			live = append(live, j)
		}
	}
	s.jobs = live
	if len(todo) == 0 {
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
			s.maybePropose(w.d, rec)
		}
	}()
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
