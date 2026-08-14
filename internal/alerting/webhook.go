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

// Package alerting fires notifications when a PerformanceRegression is
// detected. WebhookNotifier owns the HTTP transport (POST, timeout,
// status-code handling); the actual payload layout is delegated to a
// Formatter (see formatter.go) — SlackFormatter (this package's original,
// and still default, format), TeamsFormatter, PagerDutyFormatter, or a
// user-supplied CustomFormatter. See BuildNotifier for how one is picked
// from a --alert-format flag or a PostgresWatch's spec.alerting.format.
package alerting

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// WebhookConfig holds configuration for the webhook notifier.
type WebhookConfig struct {
	URL string
	// Timeout guards against a slow/unreachable destination blocking the
	// operator/manager goroutine that calls Notify.
	Timeout     time.Duration
	ClusterName string
	// Formatter builds the notification body for each detected regression.
	// Defaults to SlackFormatter{} when nil, preserving this package's
	// original (and still most common) behaviour — most callers should
	// prefer BuildNotifier, which sets this from a --alert-format flag or
	// spec.alerting.format instead of leaving it to this zero-value default.
	Formatter Formatter
}

func (c *WebhookConfig) defaults() {
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.Formatter == nil {
		c.Formatter = SlackFormatter{}
	}
}

// WebhookNotifier POSTs a Formatter-rendered payload to a webhook endpoint.
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

// Notify sends a webhook notification for a detected regression.
// It is a no-op (returns nil) when r.Status != StatusDetected.
func (n *WebhookNotifier) Notify(ctx context.Context, r v1alpha1.PerformanceRegression) error {
	if r.Status != v1alpha1.StatusDetected {
		return nil
	}

	body, contentType, err := n.cfg.Formatter.Format(r, n.cfg.ClusterName)
	if err != nil {
		return fmt.Errorf("alerting: format payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("alerting: create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

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
