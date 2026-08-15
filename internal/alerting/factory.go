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
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
// actually detected. For non-PagerDuty formats, an empty URL disables
// alert delivery and returns a notifier whose Notify method is a no-op.
func BuildNotifier(cfg BuildConfig, logger *slog.Logger, reg prometheus.Registerer) (*WebhookNotifier, error) {
	format := cfg.Format
	if format == "" {
		format = "slack"
	}

	url := cfg.URL
	var formatter Formatter
	disabled := format != "pagerduty" && url == ""

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
		if disabled {
			break
		}
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

	if disabled {
		return NewWebhookNotifier(WebhookConfig{
			URL:         "",
			Timeout:     cfg.Timeout,
			ClusterName: cfg.ClusterName,
			Formatter:   formatter,
			Registerer:  reg,
		}, logger), nil
	}

	// pagerduty's url is always the fixed pagerDutyEventsURL constant above,
	// never operator input, so there's nothing to validate for it. Every
	// other format sends cfg.URL -- a value that ultimately comes from a
	// --alert-url/--slack-url flag or a PostgresWatch's
	// spec.alerting.url/spec.slackWebhookUrl, i.e. something anyone able to
	// create or edit a PostgresWatch controls. Validate it here so both real
	// call sites (internal/cli.RunOperator's --dry-run and startup path, and
	// PostgresWatchReconciler's reconcile) get the same check for free.
	if format != "pagerduty" {
		if err := validateWebhookURL(url); err != nil {
			return nil, err
		}
	}

	return NewWebhookNotifier(WebhookConfig{
		URL:         url,
		Timeout:     cfg.Timeout,
		ClusterName: cfg.ClusterName,
		Formatter:   formatter,
		Registerer:  reg,
	}, logger), nil
}

// blockedAlertDestinationHosts lists well-known cloud instance-metadata
// hostnames -- the single highest-value SSRF target, since a request that
// reaches one of these from inside a cloud VM/pod can return the node's own
// cloud credentials. Matched case-insensitively against the URL's hostname
// (not the IP it happens to resolve to).
var blockedAlertDestinationHosts = map[string]bool{
	"metadata.google.internal": true, // GCP
	"metadata.internal":        true, // GCP (short form some images alias)
	"metadata.azure.com":       true, // Azure IMDS is normally reached via 169.254.169.254 directly, but block the hostname too
}

// validateWebhookURL rejects the alert-destination shapes that have no
// legitimate use as a Slack/Teams/custom webhook target and are the classic
// SSRF payloads: non-HTTP(S) schemes, loopback/link-local literal IPs (which
// covers the 169.254.169.254 cloud metadata address shared by AWS/GCP/Azure/
// DigitalOcean/Alibaba), and known cloud metadata hostnames.
//
// This is a deliberately narrow, static check, not a complete SSRF defence:
// it does not resolve hostnames (so it cannot catch DNS rebinding, where a
// hostname that resolves to a public IP at validation time is repointed at
// an internal IP before the actual request is sent) and it does not block
// ordinary RFC1918 private ranges (10.0.0.0/8, 172.16.0.0/12,
// 192.168.0.0/16), because those are exactly where a self-hosted, in-cluster
// webhook receiver (an internal Alertmanager, a homegrown relay, etc.)
// legitimately lives -- blocking them by default would break that
// deployment shape rather than just an attack. Installations that need a
// stricter policy (only alert to a pre-approved allowlist of hosts, or via a
// referenced Secret) should enforce that with an admission policy in front
// of PostgresWatch/DeploySource, the same scoping this project uses for the
// Secret-consent-label and kubeconfig-restriction controls in
// docs/multi-cluster.md.
func validateWebhookURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("alerting: webhook url is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("alerting: invalid webhook url %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("alerting: webhook url %q uses scheme %q; only http and https are allowed", rawURL, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("alerting: webhook url %q has no host", rawURL)
	}
	if blockedAlertDestinationHosts[strings.ToLower(host)] {
		return fmt.Errorf("alerting: webhook url %q targets a well-known cloud metadata hostname, which is never a valid alert destination", rawURL)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("alerting: webhook url %q targets a loopback or link-local address (this includes the 169.254.169.254 cloud metadata address), which is never a valid alert destination", rawURL)
		}
	}
	return nil
}
