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
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// shadowEvaluations are the offsets after a deploy at which the attribution
// engine judges it. The first is when the post-deploy window has filled; the
// following ones confirm the verdict, which the policy needs before it would
// act on anything above an alert.
var shadowEvaluations = []time.Duration{30 * time.Minute, 35 * time.Minute, 40 * time.Minute}

// shadowTracker schedules the shadow evaluations of every drained deploy and
// runs them off the poll goroutine, since one assessment can take a while on a
// large workload. It only ever logs and counts; nothing it concludes is acted on.
type shadowTracker struct {
	ev     *shadow.Evaluator
	logger *slog.Logger
	count  *prometheus.CounterVec

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
	return &shadowTracker{ev: ev, logger: logger, count: c}
}

// add schedules the evaluations of a newly drained deploy.
func (s *shadowTracker) add(e dto.DeployEvent) {
	s.ev.NoteDeploy(e.ID, e.Timestamp)
	j := &shadowJob{deploy: shadow.Deploy{ID: e.ID, At: e.Timestamp, HasRollbackTarget: true}}
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
			s.logger.Info("attribution (shadow): decision, not executed",
				"deploy", w.d.ID, "health", string(rec.Assessment.Health),
				"action", string(d.Action), "wanted", string(d.Wanted), "grade", string(d.Grade),
				"impact", d.Impact, "attribution", rec.Assessment.Attribution, "reasons", d.Reasons)
		}
	}()
}
