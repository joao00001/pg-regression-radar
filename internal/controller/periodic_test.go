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
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	radarv1alpha1 "github.com/joao00001/pg-regression-radar/api/v1alpha1"
	dto "github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// TestRegressionResourceName_PeriodicIsStablePerQuery is the regression test
// for the naming decision recorded in docs/periodic-detection.md: unlike a
// deploy-triggered regression (one CR per deploy episode), a periodic
// regression has no natural "episode is over" boundary, so it must always
// resolve to the same object name for a given query, regardless of the
// engine-generated dto.PerformanceRegression.Name (which embeds a timestamp
// and therefore differs across ticks).
func TestRegressionResourceName_PeriodicIsStablePerQuery(t *testing.T) {
	first := regressionResourceName(dto.TriggerTypePeriodic, "", 42)
	second := regressionResourceName(dto.TriggerTypePeriodic, "", 42)
	if first != second {
		t.Fatalf("expected a stable name across calls, got %q and %q", first, second)
	}
	if want := "periodic-q42"; first != want {
		t.Fatalf("expected periodic name %q, got %q", want, first)
	}

	// A non-empty deployEventID must be ignored entirely for a periodic
	// regression -- there is no deploy event to name it after.
	withEventID := regressionResourceName(dto.TriggerTypePeriodic, "some-deploy-id", 42)
	if withEventID != first {
		t.Fatalf("expected deployEventID to be ignored for periodic naming, got %q", withEventID)
	}
}

// TestRegressionResourceName_DeployUnchanged guards the pre-existing,
// deploy-triggered naming behavior against regressing while adding the
// periodic branch above.
func TestRegressionResourceName_DeployUnchanged(t *testing.T) {
	got := regressionResourceName(dto.TriggerTypeDeploy, "Deploy_ABC/123", 7)
	want := "deploy-abc-123-q7"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// TestRecordRegression_PeriodicUpdatesSameCRAcrossEpisodes proves
// recordRegression's periodic path is idempotent per-query the way
// docs/periodic-detection.md promises: two separate periodic ticks for the
// same query, each with the engine's own distinct Name, must update one
// PerformanceRegression object rather than creating a second one.
func TestRecordRegression_PeriodicUpdatesSameCRAcrossEpisodes(t *testing.T) {
	watch := samplePostgresWatch("watch-periodic", "default")
	r, c := newTestReconciler(t, watch)

	watchKey := types.NamespacedName{Name: "watch-periodic", Namespace: "default"}

	firstTick := dto.PerformanceRegression{
		Name:        "periodic-q99-1000",
		QueryID:     99,
		QueryText:   "SELECT 1",
		Status:      dto.StatusDetected,
		TriggerType: dto.TriggerTypePeriodic,
		CreatedAt:   time.Now(),
	}
	if err := r.recordRegression(context.Background(), watchKey, firstTick); err != nil {
		t.Fatalf("first recordRegression: %v", err)
	}

	secondTick := firstTick
	secondTick.Name = "periodic-q99-2000" // a later episode, engine gives it a fresh Name
	secondTick.ConfidenceScore = 0.99
	if err := r.recordRegression(context.Background(), watchKey, secondTick); err != nil {
		t.Fatalf("second recordRegression: %v", err)
	}

	var list radarv1alpha1.PerformanceRegressionList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatalf("list performanceregressions: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected exactly 1 PerformanceRegression across two periodic ticks for the same query, got %d", len(list.Items))
	}

	obj := list.Items[0]
	if want := "periodic-q99"; obj.Name != want {
		t.Errorf("expected stable object name %q, got %q", want, obj.Name)
	}
	if obj.Spec.TriggerType != radarv1alpha1.RegressionTriggerTypePeriodic {
		t.Errorf("expected spec.triggerType=periodic, got %q", obj.Spec.TriggerType)
	}
	if obj.Spec.DeployEventID != "" {
		t.Errorf("expected empty spec.deployEventId for a periodic regression, got %q", obj.Spec.DeployEventID)
	}
	if obj.Status.ConfidenceScore != fmt.Sprintf("%.4f", secondTick.ConfidenceScore) {
		t.Errorf("expected status to reflect the second tick's confidence, got %q", obj.Status.ConfidenceScore)
	}
}

// TestRecordRegression_DeploySetsTriggerType guards the deploy-triggered
// path: even though every pre-existing regression predates the TriggerType
// field, every newly created one must now have it explicitly set to
// "deploy", not left empty, so the CRD's own documented "empty means deploy"
// fallback is a compatibility affordance for old data, not something new
// code silently relies on.
func TestRecordRegression_DeploySetsTriggerType(t *testing.T) {
	watch := samplePostgresWatch("watch-deploy-trigger", "default")
	r, c := newTestReconciler(t, watch)

	watchKey := types.NamespacedName{Name: "watch-deploy-trigger", Namespace: "default"}

	res := dto.PerformanceRegression{
		Name:          "dep-1-q5",
		DeployEventID: "dep-1",
		QueryID:       5,
		Status:        dto.StatusDetected,
		TriggerType:   dto.TriggerTypeDeploy,
		CreatedAt:     time.Now(),
	}
	if err := r.recordRegression(context.Background(), watchKey, res); err != nil {
		t.Fatalf("recordRegression: %v", err)
	}

	var obj radarv1alpha1.PerformanceRegression
	if err := c.Get(context.Background(), types.NamespacedName{Name: "dep-1-q5", Namespace: "default"}, &obj); err != nil {
		t.Fatalf("get performanceregression: %v", err)
	}
	if obj.Spec.TriggerType != radarv1alpha1.RegressionTriggerTypeDeploy {
		t.Errorf("expected spec.triggerType=deploy, got %q", obj.Spec.TriggerType)
	}
}
