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
	"fmt"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// pagerDutyEventsURL is PagerDuty's Events API v2 endpoint. It is fixed —
// unlike Slack/Teams, a PagerDuty integration is identified by the
// routing_key inside the request body, not by a per-integration URL — so
// BuildNotifier always sends pagerduty-formatted payloads here regardless
// of any configured --alert-url/spec.alerting.url.
const pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

// pagerDutyEvent is the Events API v2 "trigger" request body
// (https://developer.pagerduty.com/api-reference/9d0b4b12e36f9-send-an-event-to-pager-duty).
// This project only ever triggers new incidents — it never acknowledges or
// resolves them, since neither concept has an equivalent in
// v1alpha1.PerformanceRegression today; resolution is left to whatever
// on-call process PagerDuty itself drives from here.
type pagerDutyEvent struct {
	RoutingKey  string             `json:"routing_key"`
	EventAction string             `json:"event_action"`
	DedupKey    string             `json:"dedup_key"`
	Payload     pagerDutyEventBody `json:"payload"`
}

type pagerDutyEventBody struct {
	Summary       string                 `json:"summary"`
	Source        string                 `json:"source"`
	Severity      string                 `json:"severity"`
	Timestamp     string                 `json:"timestamp"`
	CustomDetails map[string]interface{} `json:"custom_details"`
}

// PagerDutyFormatter renders a PagerDuty Events API v2 "trigger" payload.
// Unlike SlackFormatter/TeamsFormatter (stateless, safe as a zero value),
// PagerDutyFormatter needs a routing key baked in at construction time —
// see NewPagerDutyFormatter.
type PagerDutyFormatter struct {
	routingKey string
}

// NewPagerDutyFormatter creates a PagerDutyFormatter for the given Events
// API v2 integration/routing key. Required (see BuildNotifier) because,
// unlike a webhook URL, PagerDuty has no per-integration endpoint to POST
// to — every integration shares pagerDutyEventsURL and is disambiguated
// entirely by this key.
func NewPagerDutyFormatter(routingKey string) *PagerDutyFormatter {
	return &PagerDutyFormatter{routingKey: routingKey}
}

// Format implements Formatter.
func (f *PagerDutyFormatter) Format(r v1alpha1.PerformanceRegression, clusterName string) ([]byte, string, error) {
	severity := "critical"
	if r.ExternalCauseSuspected {
		severity = "warning"
	}

	// dedup_key mirrors this project's own idempotency key (see
	// internal/controller.regressionResourceName): the same (deploy event,
	// query) pair re-notifying (e.g. on a controller restart re-processing
	// a not-yet-retired PendingSet entry) updates the same PagerDuty
	// incident instead of opening a duplicate one.
	dedupKey := fmt.Sprintf("%s-q%d", r.DeployEventID, r.QueryID)

	details := map[string]interface{}{
		"query_id":            r.QueryID,
		"query_text":          r.QueryText,
		"latency_before_ms":   fmt.Sprintf("%.2f", r.MeanLatencyBefore),
		"latency_after_ms":    fmt.Sprintf("%.2f", r.MeanLatencyAfter),
		"latency_change":      fmt.Sprintf("%.2fx", r.LatencyChangeFactor),
		"confidence":          fmt.Sprintf("%.0f%%", r.ConfidenceScore*100),
		"change_point":        r.DetectedChangeAt.Format(time.RFC3339),
		"external_cause_note": r.ExternalCauseSuspected,
	}
	if r.PlanDiffSummary != "" {
		details["plan_diff"] = r.PlanDiffSummary
	}

	event := pagerDutyEvent{
		RoutingKey:  f.routingKey,
		EventAction: "trigger",
		DedupKey:    dedupKey,
		Payload: pagerDutyEventBody{
			Summary:       fmt.Sprintf("pg-regression-radar: query %d latency changed %.2fx on cluster %s", r.QueryID, r.LatencyChangeFactor, clusterName),
			Source:        clusterName,
			Severity:      severity,
			Timestamp:     r.DetectedChangeAt.Format(time.RFC3339),
			CustomDetails: details,
		},
	}

	body, err := json.Marshal(event)
	if err != nil {
		return nil, "", fmt.Errorf("alerting: marshal pagerduty payload: %w", err)
	}
	return body, "application/json", nil
}
