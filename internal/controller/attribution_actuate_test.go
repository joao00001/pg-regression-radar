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
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/internal/attribution"
	"github.com/joao00001/pg-regression-radar/internal/attribution/shadow"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

type fakeActuator struct {
	pauses, aborts []string
	pauseErr       error
}

func (f *fakeActuator) Pause(_ context.Context, ns, name string) error {
	f.pauses = append(f.pauses, ns+"/"+name)
	return f.pauseErr
}

func (f *fakeActuator) Abort(_ context.Context, ns, name string) error {
	f.aborts = append(f.aborts, ns+"/"+name)
	return nil
}

func actuateTracker(f *fakeActuator, isRollout bool) *shadowTracker {
	tr := newShadowTracker(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry(), "c")
	tr.withActuation(&ActuateRuntime{
		Aborter:   f,
		Pauser:    f,
		IsRollout: func(context.Context, dto.DeployEvent) bool { return isRollout },
	})
	return tr
}

func rec(action attribution.Action, enforced bool) shadow.Record {
	return shadow.Record{Decision: attribution.Decision{Action: action, Enforced: enforced, Grade: attribution.GradeB}}
}

var rolloutDeploy = dto.DeployEvent{ID: "d1", Namespace: "shop", App: "checkout", Source: "rollouts"}

func TestActuateHoldPausesAndRollbackAborts(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)

	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	if len(f.pauses) != 1 || f.pauses[0] != "shop/checkout" || len(f.aborts) != 0 {
		t.Fatalf("hold: pauses=%v aborts=%v, want one pause and no abort", f.pauses, f.aborts)
	}
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0))
	if len(f.aborts) != 1 || f.aborts[0] != "shop/checkout" {
		t.Fatalf("rollback: aborts=%v, want one abort (a pause escalates to an abort)", f.aborts)
	}
}

func TestActuateActsOncePerAction(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	for i := 0; i < 4; i++ {
		tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	}
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0))
	// A weaker later decision must not undo or repeat anything.
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	if len(f.pauses) != 1 || len(f.aborts) != 1 {
		t.Fatalf("pauses=%d aborts=%d, want exactly one of each", len(f.pauses), len(f.aborts))
	}
}

func TestActuateNeverActsOnAnUnenforcedOrMildDecision(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, false), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, false), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionAlert, true), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionNone, true), time.Unix(0, 0))
	if len(f.pauses)+len(f.aborts) != 0 {
		t.Fatalf("acted on a shadow, alert or none decision: pauses=%v aborts=%v", f.pauses, f.aborts)
	}
}

func TestActuateLeavesNonRolloutDeploysAlone(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, false)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0))
	if len(f.pauses)+len(f.aborts) != 0 {
		t.Fatalf("acted on a deploy that is not an Argo Rollouts rollout: %v %v", f.pauses, f.aborts)
	}
}

func TestActuateRetriesAFailedCall(t *testing.T) {
	f := &fakeActuator{pauseErr: errors.New("forbidden")}
	tr := actuateTracker(f, true)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	f.pauseErr = nil
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	if len(f.pauses) != 2 {
		t.Fatalf("pause calls = %d, want 2 (one failure, one retry that succeeds, then no more)", len(f.pauses))
	}
}

func TestActuateDisabledWithoutRuntime(t *testing.T) {
	tr := newShadowTracker(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry(), "c")
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0)) // must not panic
}

func TestActuateHonoursTheNamespaceAllowlist(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	tr.act.Namespaces = []string{"payments"}
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), time.Unix(0, 0)) // shop is not allowed
	other := rolloutDeploy
	other.ID, other.Namespace = "d2", "payments"
	tr.maybeActuate(other, rec(attribution.ActionRollback, true), time.Unix(0, 0))
	if len(f.aborts) != 1 || f.aborts[0] != "payments/checkout" {
		t.Fatalf("aborts = %v, want only payments/checkout", f.aborts)
	}
}

func TestActuateStopsWhilePausedAndResumesAfter(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	paused := true
	tr.act.Paused = func(context.Context) bool { return paused }
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	if len(f.pauses) != 0 {
		t.Fatalf("paused %d times while the kill switch was on", len(f.pauses))
	}
	paused = false
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), time.Unix(0, 0))
	if len(f.pauses) != 1 {
		t.Fatalf("pauses = %d after lifting the switch, want 1", len(f.pauses))
	}
}

func TestAbortSchedulesACheckButPauseDoesNot(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	now := time.Unix(1000, 0)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true), now)
	if len(tr.verify) != 0 {
		t.Fatalf("a pause scheduled %d checks: a held canary still serves traffic, so its regression persists by design", len(tr.verify))
	}
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true), now)
	if len(tr.verify) != 1 || !tr.verify[0].at.Equal(now.Add(verifyAfter)) {
		t.Fatalf("verify = %+v, want one check %s after the abort", tr.verify, verifyAfter)
	}
}
