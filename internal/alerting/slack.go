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

// slackPayload is a minimal Slack incoming-webhook message.
type slackPayload struct {
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

type slackAttachment struct {
	Color  string       `json:"color"`
	Fields []slackField `json:"fields"`
}

type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

// SlackFormatter renders a Slack (or Slack-compatible, e.g. Mattermost)
// incoming-webhook payload. It is this package's original (and still
// default) format — see BuildNotifier and Format's "slack" case.
type SlackFormatter struct{}

// Format implements Formatter.
func (SlackFormatter) Format(r v1alpha1.PerformanceRegression, clusterName string) ([]byte, string, error) {
	payload := slackPayload{
		Text: fmt.Sprintf(":rotating_light: *pg-regression-radar* — performance regression detected on cluster *%s*", clusterName),
		Attachments: []slackAttachment{
			{
				Color: "danger",
				Fields: []slackField{
					{Title: "Deploy Event", Value: r.DeployEventID, Short: true},
					{Title: "Query ID", Value: fmt.Sprintf("%d", r.QueryID), Short: true},
					{Title: "Query (excerpt)", Value: r.QueryText, Short: false},
					{Title: "Latency Before", Value: fmt.Sprintf("%.2f ms", r.MeanLatencyBefore), Short: true},
					{Title: "Latency After", Value: fmt.Sprintf("%.2f ms", r.MeanLatencyAfter), Short: true},
					{Title: "Change Factor", Value: fmt.Sprintf("%.2fx", r.LatencyChangeFactor), Short: true},
					{Title: "Confidence", Value: fmt.Sprintf("%.0f%%", r.ConfidenceScore*100), Short: true},
					{Title: "Change Point", Value: r.DetectedChangeAt.Format(time.RFC3339), Short: true},
				},
			},
		},
	}

	if r.ExternalCauseSuspected {
		payload.Attachments[0].Color = "warning"
		payload.Attachments[0].Fields = append(payload.Attachments[0].Fields,
			slackField{Title: "⚠️ Note", Value: "External cause suspected (CPU/IO also changed).", Short: false})
	}

	// PlanDiffSummary is only populated when --capture-plans is enabled and
	// the server is PostgreSQL 16+ (see internal/planner and
	// docs/detection-algorithm.md); most deployments won't have it, so it's
	// appended as its own field rather than reserving space for it above.
	if r.PlanDiffSummary != "" {
		payload.Attachments[0].Fields = append(payload.Attachments[0].Fields,
			slackField{Title: "Plan Diff", Value: r.PlanDiffSummary, Short: false})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("alerting: marshal slack payload: %w", err)
	}
	return body, "application/json", nil
}
