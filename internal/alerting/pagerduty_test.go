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

package alerting

import (
	"encoding/json"
	"testing"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// TestPagerDutyFormatter_Format verifies the Events API v2 "trigger" body:
// routing key propagation, the dedup_key derived from (deploy event, query)
// so repeat notifications update rather than duplicate an incident, and
// that the regression's own fields make it into custom_details.
func TestPagerDutyFormatter_Format(t *testing.T) {
	reg := sampleRegression(v1alpha1.StatusDetected)

	f := NewPagerDutyFormatter("R0UT1NG-KEY")
	body, contentType, err := f.Format(reg, "prod-east")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("expected application/json, got %q", contentType)
	}

	var event pagerDutyEvent
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if event.RoutingKey != "R0UT1NG-KEY" {
		t.Errorf("expected routing_key to be propagated, got %q", event.RoutingKey)
	}
	if event.EventAction != "trigger" {
		t.Errorf("expected event_action=trigger, got %q", event.EventAction)
	}
	wantDedup := "deploy-123-q987654321"
	if event.DedupKey != wantDedup {
		t.Errorf("expected dedup_key=%q, got %q", wantDedup, event.DedupKey)
	}
	if event.Payload.Severity != "critical" {
		t.Errorf("expected severity=critical for a plain detected regression, got %q", event.Payload.Severity)
	}
	if event.Payload.Source != "prod-east" {
		t.Errorf("expected source to be the cluster name, got %q", event.Payload.Source)
	}
	if event.Payload.CustomDetails["query_text"] != reg.QueryText {
		t.Errorf("expected custom_details.query_text=%q, got %v", reg.QueryText, event.Payload.CustomDetails["query_text"])
	}
}

// TestPagerDutyFormatter_ExternalCauseSuspected verifies severity is
// downgraded to "warning" — mirroring Slack's color and Teams' themeColor
// downgrade — when the deploy might not be the sole cause.
func TestPagerDutyFormatter_ExternalCauseSuspected(t *testing.T) {
	reg := sampleRegression(v1alpha1.StatusDetected)
	reg.ExternalCauseSuspected = true

	f := NewPagerDutyFormatter("R0UT1NG-KEY")
	body, _, err := f.Format(reg, "test-cluster")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	var event pagerDutyEvent
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if event.Payload.Severity != "warning" {
		t.Errorf("expected severity=warning when ExternalCauseSuspected, got %q", event.Payload.Severity)
	}
}

// TestPagerDutyFormatter_PlanDiffSummary verifies the same opt-in
// plan-diff field Slack/Teams surface also reaches PagerDuty's
// custom_details, only when non-empty.
func TestPagerDutyFormatter_PlanDiffSummary(t *testing.T) {
	f := NewPagerDutyFormatter("R0UT1NG-KEY")

	withDiff := sampleRegression(v1alpha1.StatusDetected)
	withDiff.PlanDiffSummary = "root plan node changed from Index Scan to Seq Scan"
	body, _, err := f.Format(withDiff, "test-cluster")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	var event pagerDutyEvent
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if event.Payload.CustomDetails["plan_diff"] != withDiff.PlanDiffSummary {
		t.Errorf("expected custom_details.plan_diff=%q, got %v", withDiff.PlanDiffSummary, event.Payload.CustomDetails["plan_diff"])
	}

	withoutDiff := sampleRegression(v1alpha1.StatusDetected)
	body, _, err = f.Format(withoutDiff, "test-cluster")
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	// A fresh decode target: json.Unmarshal reuses (rather than clears) an
	// already-populated map field, so decoding into the same `event` var
	// used above would leave the first call's "plan_diff" entry behind
	// regardless of whether this second payload actually sent one.
	var event2 pagerDutyEvent
	if err := json.Unmarshal(body, &event2); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if _, ok := event2.Payload.CustomDetails["plan_diff"]; ok {
		t.Error("did not expect a plan_diff key when PlanDiffSummary is empty")
	}
}
