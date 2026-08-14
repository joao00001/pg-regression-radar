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

// teamsMessageCard is the legacy "Office 365 Connector" MessageCard schema
// (https://learn.microsoft.com/en-us/outlook/actionable-messages/message-card-reference).
// It is still what Microsoft Teams' Incoming Webhook connector (and every
// third-party Teams webhook relay this project's users are likely to sit
// behind) documents and accepts today, unlike the newer, considerably more
// verbose Adaptive Card schema aimed at Teams Workflows — this is the same
// "one flat card, one list of facts" shape as Slack's attachment/fields,
// just under Microsoft's field names.
type teamsMessageCard struct {
	Type       string         `json:"@type"`
	Context    string         `json:"@context"`
	ThemeColor string         `json:"themeColor"`
	Summary    string         `json:"summary"`
	Sections   []teamsSection `json:"sections"`
}

type teamsSection struct {
	ActivityTitle string      `json:"activityTitle"`
	Facts         []teamsFact `json:"facts"`
	Markdown      bool        `json:"markdown"`
}

type teamsFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const (
	teamsColorDanger  = "D93F3F" // hex, no leading '#' — MessageCard convention
	teamsColorWarning = "E8A33D"
)

// TeamsFormatter renders a Microsoft Teams Incoming Webhook MessageCard.
// Mirrors SlackFormatter field-for-field so the same regression looks the
// same regardless of which chat tool receives it.
type TeamsFormatter struct{}

// Format implements Formatter.
func (TeamsFormatter) Format(r v1alpha1.PerformanceRegression, clusterName string) ([]byte, string, error) {
	facts := []teamsFact{
		{Name: "Deploy Event", Value: r.DeployEventID},
		{Name: "Query ID", Value: fmt.Sprintf("%d", r.QueryID)},
		{Name: "Query (excerpt)", Value: r.QueryText},
		{Name: "Latency Before", Value: fmt.Sprintf("%.2f ms", r.MeanLatencyBefore)},
		{Name: "Latency After", Value: fmt.Sprintf("%.2f ms", r.MeanLatencyAfter)},
		{Name: "Change Factor", Value: fmt.Sprintf("%.2fx", r.LatencyChangeFactor)},
		{Name: "Confidence", Value: fmt.Sprintf("%.0f%%", r.ConfidenceScore*100)},
		{Name: "Change Point", Value: r.DetectedChangeAt.Format(time.RFC3339)},
	}

	color := teamsColorDanger
	if r.ExternalCauseSuspected {
		color = teamsColorWarning
		facts = append(facts, teamsFact{Name: "⚠️ Note", Value: "External cause suspected (CPU/IO also changed)."})
	}
	if r.PlanDiffSummary != "" {
		facts = append(facts, teamsFact{Name: "Plan Diff", Value: r.PlanDiffSummary})
	}

	card := teamsMessageCard{
		Type:       "MessageCard",
		Context:    "http://schema.org/extensions",
		ThemeColor: color,
		Summary:    fmt.Sprintf("pg-regression-radar — performance regression detected on cluster %s", clusterName),
		Sections: []teamsSection{
			{
				ActivityTitle: fmt.Sprintf("🚨 **pg-regression-radar** — performance regression detected on cluster **%s**", clusterName),
				Facts:         facts,
				Markdown:      true,
			},
		},
	}

	body, err := json.Marshal(card)
	if err != nil {
		return nil, "", fmt.Errorf("alerting: marshal teams payload: %w", err)
	}
	return body, "application/json", nil
}
