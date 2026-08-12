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

// Package alerting fires notifications when a PerformanceRegression is detected.
// The initial implementation targets Slack's incoming-webhook format because
// it is the lowest-friction integration for on-call workflows.
package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// WebhookConfig holds configuration for the generic/Slack webhook notifier.
type WebhookConfig struct {
	URL string
	// Timeout guards against Slack outages blocking the operator goroutine.
	Timeout     time.Duration
	ClusterName string
}

func (c *WebhookConfig) defaults() {
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
}

// WebhookNotifier sends Slack-compatible webhook payloads.
type WebhookNotifier struct {
	cfg    WebhookConfig
	client *http.Client
	logger *slog.Logger
}

// NewWebhookNotifier creates a new WebhookNotifier.
func NewWebhookNotifier(cfg WebhookConfig, logger *slog.Logger) *WebhookNotifier {
	cfg.defaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		logger: logger,
	}
}

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

// Notify sends a webhook notification for a detected regression.
// It is a no-op (returns nil) when r.Status != StatusDetected.
func (n *WebhookNotifier) Notify(ctx context.Context, r v1alpha1.PerformanceRegression) error {
	if r.Status != v1alpha1.StatusDetected {
		return nil
	}

	payload := slackPayload{
		Text: fmt.Sprintf(":rotating_light: *pg-regression-radar* — performance regression detected on cluster *%s*", n.cfg.ClusterName),
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
		return fmt.Errorf("alerting: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("alerting: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("alerting: send webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("alerting: webhook returned status %d", resp.StatusCode)
	}

	n.logger.Info("alerting: notification sent",
		"regression", r.Name,
		"status_code", resp.StatusCode)

	return nil
}
