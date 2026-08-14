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
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

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
	// Registerer receives the alerting metrics for this notifier. Defaults to
	// prometheus.DefaultRegisterer when nil.
	Registerer prometheus.Registerer
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
	cfg                      WebhookConfig
	client                   *http.Client
	logger                   *slog.Logger
	notificationsTotal       *prometheus.CounterVec
	regressionsDetectedTotal *prometheus.CounterVec
}

// NewWebhookNotifier creates a new WebhookNotifier.
func NewWebhookNotifier(cfg WebhookConfig, logger *slog.Logger) *WebhookNotifier {
	cfg.defaults()
	if logger == nil {
		logger = slog.Default()
	}
	reg := cfg.Registerer
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	return &WebhookNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
		logger: logger,
		notificationsTotal: registerCounterVec(reg, prometheus.CounterOpts{
			Name: "pg_regression_radar_notifications_total",
			Help: "Total notification attempts, labeled by final status and configured payload format.",
		}, []string{"status", "format"}),
		regressionsDetectedTotal: registerCounterVec(reg, prometheus.CounterOpts{
			Name: "pg_regression_radar_regressions_detected_total",
			Help: "Total regressions that reached StatusDetected, labeled by trigger type and cluster.",
		}, []string{"trigger", "cluster"}),
	}
}

// ObserveDetectedRegression records that a regression reached StatusDetected.
// Callers should invoke this once for each newly detected regression before
// attempting delivery.
func (n *WebhookNotifier) ObserveDetectedRegression(r v1alpha1.PerformanceRegression) {
	if r.Status != v1alpha1.StatusDetected {
		return
	}
	n.regressionsDetectedTotal.WithLabelValues(triggerLabel(r.TriggerType), n.cfg.ClusterName).Inc()
}

// Notify sends a webhook notification for a detected regression.
// It is a no-op (returns nil) when r.Status != StatusDetected.
func (n *WebhookNotifier) Notify(ctx context.Context, r v1alpha1.PerformanceRegression) error {
	if r.Status != v1alpha1.StatusDetected {
		return nil
	}

	body, contentType, err := n.cfg.Formatter.Format(r, n.cfg.ClusterName)
	if err != nil {
		n.notificationsTotal.WithLabelValues("error", formatLabel(n.cfg.Formatter)).Inc()
		return fmt.Errorf("alerting: format payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.URL, bytes.NewReader(body))
	if err != nil {
		n.notificationsTotal.WithLabelValues("error", formatLabel(n.cfg.Formatter)).Inc()
		return fmt.Errorf("alerting: create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := n.client.Do(req)
	if err != nil {
		n.notificationsTotal.WithLabelValues("error", formatLabel(n.cfg.Formatter)).Inc()
		return fmt.Errorf("alerting: send webhook: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 300 {
		n.notificationsTotal.WithLabelValues("error", formatLabel(n.cfg.Formatter)).Inc()
		return fmt.Errorf("alerting: webhook returned status %d", resp.StatusCode)
	}
	n.notificationsTotal.WithLabelValues("success", formatLabel(n.cfg.Formatter)).Inc()

	n.logger.Info("alerting: notification sent",
		"regression", r.Name,
		"status_code", resp.StatusCode)

	return nil
}

func registerCounterVec(reg prometheus.Registerer, opts prometheus.CounterOpts, labelNames []string) *prometheus.CounterVec {
	cv := prometheus.NewCounterVec(opts, labelNames)
	if err := reg.Register(cv); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
				return existing
			}
			panic(fmt.Sprintf("alerting: metric %q already registered with unexpected collector type %T", opts.Name, are.ExistingCollector))
		}
		panic(fmt.Sprintf("alerting: register metric %q: %v", opts.Name, err))
	}
	return cv
}

func formatLabel(f Formatter) string {
	switch f.(type) {
	case SlackFormatter, *SlackFormatter:
		return "slack"
	case TeamsFormatter, *TeamsFormatter:
		return "teams"
	case *PagerDutyFormatter:
		return "pagerduty"
	case *CustomFormatter:
		return "custom"
	default:
		return "unknown"
	}
}

func triggerLabel(trigger v1alpha1.TriggerType) string {
	switch trigger {
	case v1alpha1.TriggerTypePeriodic:
		return string(v1alpha1.TriggerTypePeriodic)
	case "", v1alpha1.TriggerTypeDeploy:
		return string(v1alpha1.TriggerTypeDeploy)
	default:
		return "unknown"
	}
}
