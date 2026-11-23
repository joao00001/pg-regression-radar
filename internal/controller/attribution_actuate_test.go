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

	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true))
	if len(f.pauses) != 1 || f.pauses[0] != "shop/checkout" || len(f.aborts) != 0 {
		t.Fatalf("hold: pauses=%v aborts=%v, want one pause and no abort", f.pauses, f.aborts)
	}
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true))
	if len(f.aborts) != 1 || f.aborts[0] != "shop/checkout" {
		t.Fatalf("rollback: aborts=%v, want one abort (a pause escalates to an abort)", f.aborts)
	}
}

func TestActuateActsOncePerAction(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	for i := 0; i < 4; i++ {
		tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true))
	}
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true))
	// A weaker later decision must not undo or repeat anything.
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true))
	if len(f.pauses) != 1 || len(f.aborts) != 1 {
		t.Fatalf("pauses=%d aborts=%d, want exactly one of each", len(f.pauses), len(f.aborts))
	}
}

func TestActuateNeverActsOnAnUnenforcedOrMildDecision(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, true)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, false))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, false))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionAlert, true))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionNone, true))
	if len(f.pauses)+len(f.aborts) != 0 {
		t.Fatalf("acted on a shadow, alert or none decision: pauses=%v aborts=%v", f.pauses, f.aborts)
	}
}

func TestActuateLeavesNonRolloutDeploysAlone(t *testing.T) {
	f := &fakeActuator{}
	tr := actuateTracker(f, false)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true))
	if len(f.pauses)+len(f.aborts) != 0 {
		t.Fatalf("acted on a deploy that is not an Argo Rollouts rollout: %v %v", f.pauses, f.aborts)
	}
}

func TestActuateRetriesAFailedCall(t *testing.T) {
	f := &fakeActuator{pauseErr: errors.New("forbidden")}
	tr := actuateTracker(f, true)
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true))
	f.pauseErr = nil
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true))
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionHold, true))
	if len(f.pauses) != 2 {
		t.Fatalf("pause calls = %d, want 2 (one failure, one retry that succeeds, then no more)", len(f.pauses))
	}
}

func TestActuateDisabledWithoutRuntime(t *testing.T) {
	tr := newShadowTracker(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), prometheus.NewRegistry(), "c")
	tr.maybeActuate(rolloutDeploy, rec(attribution.ActionRollback, true)) // must not panic
}
