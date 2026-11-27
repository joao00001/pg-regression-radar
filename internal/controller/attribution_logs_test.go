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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	"github.com/joao00001/pg-regression-radar/internal/attribution/sim"
	"github.com/joao00001/pg-regression-radar/internal/collector"
	"github.com/joao00001/pg-regression-radar/internal/reason"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// logCapture is a logger whose lines can be read back while another goroutine
// still writes to it.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *logCapture) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (c *logCapture) lines() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if l == "" {
			continue
		}
		m := map[string]any{}
		if err := json.Unmarshal([]byte(l), &m); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// find returns the first line carrying this reason code.
func (c *logCapture) find(code reason.Code) (map[string]any, bool) {
	for _, m := range c.lines() {
		if m["reason"] == string(code) {
			return m, true
		}
	}
	return nil, false
}

func (c *logCapture) mustFind(t *testing.T, code reason.Code) map[string]any {
	t.Helper()
	m, ok := c.find(code)
	if !ok {
		t.Fatalf("no log line with reason=%s; got %v", code, c.lines())
	}
	return m
}

func TestDeployRegisteredIsLoggedWithItsEvaluationWindow(t *testing.T) {
	cap := &logCapture{}
	tr := newShadowTracker(shadow.New(&replaySource{}, collector.FingerprintQuery, attribution.Config{}), cap.logger(), prometheus.NewRegistry(), "c")
	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	tr.add(dto.DeployEvent{ID: "d1", Namespace: "shop", App: "checkout", Revision: "abc", Source: "rollouts", Timestamp: at})

	m := cap.mustFind(t, reason.DeployRegistered)
	for k, want := range map[string]string{
		"deploy": "d1", "namespace": "shop", "app": "checkout", "revision": "abc",
		"firstEvaluation": "2026-11-27T12:10:00Z", "lastEvaluation": "2026-11-27T12:45:00Z",
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %s", k, m[k], want)
		}
	}
}

func TestEvaluationWithoutSamplesIsNotSilent(t *testing.T) {
	cap := &logCapture{}
	tr := newShadowTracker(shadow.New(&replaySource{}, collector.FingerprintQuery, attribution.Config{}), cap.logger(), prometheus.NewRegistry(), "c")
	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	tr.add(dto.DeployEvent{ID: "d1", Namespace: "shop", App: "checkout", Timestamp: at})
	tr.tick(at.Add(11 * time.Minute))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cap.find(reason.NoSamples); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	m := cap.mustFind(t, reason.NoSamples)
	if m["level"] != "WARN" || m["deploy"] != "d1" {
		t.Fatalf("got %v", m)
	}
}

func TestActuationPathsCarryTheirReason(t *testing.T) {
	now := time.Unix(1000, 0)
	run := func(t *testing.T, setup func(*shadowTracker, *fakeActuator), act attribution.Action) *logCapture {
		cap := &logCapture{}
		f := &fakeActuator{}
		tr := actuateTracker(f, true)
		tr.logger = cap.logger()
		if setup != nil {
			setup(tr, f)
		}
		tr.maybeActuate(rolloutDeploy, rec(act, true), now)
		return cap
	}

	t.Run("acted", func(t *testing.T) {
		m := run(t, nil, attribution.ActionHold).mustFind(t, reason.Acted)
		if m["deploy"] != "d1" || m["action"] != "pause" {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("namespace not allowed", func(t *testing.T) {
		m := run(t, func(tr *shadowTracker, _ *fakeActuator) { tr.act.Namespaces = []string{"other"} }, attribution.ActionRollback).
			mustFind(t, reason.NamespaceNotAllowed)
		if m["namespace"] != "shop" {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("paused by annotation", func(t *testing.T) {
		m := run(t, func(tr *shadowTracker, _ *fakeActuator) { tr.act.Paused = func(context.Context) bool { return true } }, attribution.ActionRollback).
			mustFind(t, reason.ActuationPaused)
		if m["level"] != "WARN" {
			t.Fatalf("a paused actuation is worth a warning: %v", m)
		}
	})
	t.Run("not an argo rollout", func(t *testing.T) {
		m := run(t, func(tr *shadowTracker, _ *fakeActuator) {
			tr.act.IsRollout = func(context.Context, dto.DeployEvent) bool { return false }
		}, attribution.ActionHold).mustFind(t, reason.NotArgoRollout)
		if m["source"] != "rollouts" {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("failed", func(t *testing.T) {
		m := run(t, func(_ *shadowTracker, f *fakeActuator) { f.pauseErr = errors.New("forbidden") }, attribution.ActionHold).
			mustFind(t, reason.ActuationFailed)
		if m["level"] != "ERROR" || m["err"] != "forbidden" {
			t.Fatalf("got %v", m)
		}
	})
}

func TestDecisionLinesSayWhyNothingWasExecuted(t *testing.T) {
	for _, c := range []struct {
		name    string
		enforce bool
		want    reason.Code
	}{
		{"shadow", false, reason.ShadowMode},
		{"enforce but below the acting threshold", true, reason.BelowActingThreshold},
	} {
		t.Run(c.name, func(t *testing.T) {
			units, _, dep, _ := sim.World{Seed: 3, Queries: 40}.Generate() // no regression: the decision is "none"
			cap := &logCapture{}
			ev := shadow.NewWithPolicy(replayOf(units), collector.FingerprintQuery, attribution.Config{}, attribution.PolicyConfig{Enforce: c.enforce})
			tr := newShadowTracker(ev, cap.logger(), prometheus.NewRegistry(), "c")
			tr.add(dto.DeployEvent{ID: dep.ID, Timestamp: dep.At})
			tr.tick(dep.At.Add(11 * time.Minute))
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) {
				if _, ok := cap.find(c.want); ok {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatalf("no line with reason=%s; got %v", c.want, cap.lines())
		})
	}
}

// A check after an abort, on a simulated world: with the regression gone the
// abort recovered; with it still there the abort did not help, and the line that
// says so is the breaker's once it opens.
func TestAbortVerificationLinesCarryTheirReason(t *testing.T) {
	verify := func(t *testing.T, factor float64, limit int) *logCapture {
		t.Helper()
		units, _, dep, _ := sim.World{Seed: 3, Queries: 60, Deploy: sim.Effect{Fraction: 0.3, Factor: factor}}.Generate()
		cap := &logCapture{}
		ev := shadow.NewWithPolicy(replayOf(units), collector.FingerprintQuery, attribution.Config{},
			attribution.PolicyConfig{Enforce: true, BreakerLimit: limit})
		tr := newShadowTracker(ev, cap.logger(), prometheus.NewRegistry(), "c")
		job := verifyJob{deploy: shadow.Deploy{ID: dep.ID, At: dep.At}, event: dto.DeployEvent{ID: dep.ID}, acted: dep.At.Add(20 * time.Minute)}
		tr.verifyAbort(job, dep.At.Add(20*time.Minute).Add(verifyAfter))
		return cap
	}

	t.Run("recovered", func(t *testing.T) {
		m := verify(t, 1.0, 2).mustFind(t, reason.AbortRecovered)
		if m["result"] != "recovered" || m["detail"] == "" {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("persisted, breaker still closed", func(t *testing.T) {
		m := verify(t, 3.0, 2).mustFind(t, reason.AbortPersisted)
		if m["result"] != "persisted" {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("persisted and the breaker opens", func(t *testing.T) {
		m := verify(t, 3.0, 1).mustFind(t, reason.BreakerOpen)
		if m["level"] != "WARN" || m["result"] != "persisted" {
			t.Fatalf("got %v", m)
		}
	})
	t.Run("too little data", func(t *testing.T) {
		cap := &logCapture{}
		tr := newShadowTracker(shadow.New(&replaySource{}, collector.FingerprintQuery, attribution.Config{}), cap.logger(), prometheus.NewRegistry(), "c")
		tr.verifyAbort(verifyJob{deploy: shadow.Deploy{ID: "d"}, acted: time.Unix(0, 0)}, time.Unix(3600, 0))
		cap.mustFind(t, reason.AbortUnverifiable)
	})
}
