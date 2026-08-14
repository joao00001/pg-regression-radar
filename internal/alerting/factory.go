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
	"fmt"
	"log/slog"
	"time"
)

// BuildConfig collects every alerting-related knob from either CLI flags
// (internal/cli.RunOperator) or a PostgresWatch's spec.alerting
// (internal/controller.PostgresWatchReconciler.startWatch) before a
// Formatter/WebhookNotifier is actually constructed. It exists so both call
// sites share exactly one place that decides which Formatter to use and
// validates the format-specific required fields, instead of each
// reimplementing (and potentially disagreeing on) that decision.
type BuildConfig struct {
	// Format selects the layout: "slack" (default), "teams", "pagerduty", or
	// "custom". Empty is treated as "slack".
	Format string

	// URL is the webhook endpoint for the slack, teams, and custom formats.
	// Ignored (and overridden with pagerDutyEventsURL) for pagerduty, which
	// has no per-integration URL — see PagerDutyFormatter.
	URL string

	// PagerDutyRoutingKey is required when Format == "pagerduty".
	PagerDutyRoutingKey string

	// CustomTemplate is the Go text/template source required when
	// Format == "custom". See CustomTemplateData for the fields it can
	// reference.
	CustomTemplate string
	// CustomContentType overrides the Content-Type header used for
	// Format == "custom" (defaults to application/json).
	CustomContentType string

	ClusterName string
	Timeout     time.Duration
}

// BuildNotifier validates cfg and returns a ready-to-use WebhookNotifier, or
// an error describing exactly which required field is missing/invalid —
// surfaced at startup (operator flag parsing, or the first reconcile of a
// PostgresWatch) rather than silently failing the first time a regression is
// actually detected.
func BuildNotifier(cfg BuildConfig, logger *slog.Logger) (*WebhookNotifier, error) {
	format := cfg.Format
	if format == "" {
		format = "slack"
	}

	url := cfg.URL
	var formatter Formatter

	switch format {
	case "slack":
		formatter = SlackFormatter{}
	case "teams":
		formatter = TeamsFormatter{}
	case "pagerduty":
		if cfg.PagerDutyRoutingKey == "" {
			return nil, fmt.Errorf("alerting: format=pagerduty requires a routing key (--pagerduty-routing-key, or spec.alerting.pagerDutyRoutingKey)")
		}
		formatter = NewPagerDutyFormatter(cfg.PagerDutyRoutingKey)
		url = pagerDutyEventsURL
	case "custom":
		if cfg.CustomTemplate == "" {
			return nil, fmt.Errorf("alerting: format=custom requires a template (--alert-template-file, or spec.alerting.customTemplate)")
		}
		cf, err := NewCustomFormatter(cfg.CustomTemplate, cfg.CustomContentType)
		if err != nil {
			return nil, err
		}
		formatter = cf
	default:
		return nil, fmt.Errorf("alerting: unknown format %q (want slack, teams, pagerduty, or custom)", format)
	}

	return NewWebhookNotifier(WebhookConfig{
		URL:         url,
		Timeout:     cfg.Timeout,
		ClusterName: cfg.ClusterName,
		Formatter:   formatter,
	}, logger), nil
}
