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
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/joao00001/pg-regression-radar/pkg/apis/v1alpha1"
)

// TestBuildNotifier_DefaultsToSlack verifies an empty Format behaves
// exactly like this package's original, Slack-only API — the backward
// compatibility BuildConfig's doc comment promises.
func TestBuildNotifier_DefaultsToSlack(t *testing.T) {
	n, err := BuildNotifier(BuildConfig{URL: "http://example.invalid", ClusterName: "test"}, nil, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	if _, ok := n.cfg.Formatter.(SlackFormatter); !ok {
		t.Errorf("expected SlackFormatter for an empty Format, got %T", n.cfg.Formatter)
	}
	if n.cfg.URL != "http://example.invalid" {
		t.Errorf("expected URL to be passed through, got %q", n.cfg.URL)
	}
}

// TestBuildNotifier_Teams verifies format=teams selects TeamsFormatter and
// keeps the configured URL (unlike pagerduty, teams has no fixed endpoint).
func TestBuildNotifier_Teams(t *testing.T) {
	n, err := BuildNotifier(BuildConfig{Format: "teams", URL: "http://example.invalid"}, nil, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	if _, ok := n.cfg.Formatter.(TeamsFormatter); !ok {
		t.Errorf("expected TeamsFormatter, got %T", n.cfg.Formatter)
	}
	if n.cfg.URL != "http://example.invalid" {
		t.Errorf("expected URL to be passed through for teams, got %q", n.cfg.URL)
	}
}

// TestBuildNotifier_EmptyURLDisablesAlerting verifies the documented
// "no URL = no alerting configured" contract is enforced at construction
// time for URL-backed formats, so Notify becomes a no-op instead of failing
// later on an empty destination string.
func TestBuildNotifier_EmptyURLDisablesAlerting(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  BuildConfig
	}{
		{name: "default-slack", cfg: BuildConfig{ClusterName: "test"}},
		{name: "teams", cfg: BuildConfig{Format: "teams", ClusterName: "test"}},
		{name: "custom", cfg: BuildConfig{Format: "custom", ClusterName: "test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, err := BuildNotifier(tc.cfg, nil, prometheus.NewRegistry())
			if err != nil {
				t.Fatalf("BuildNotifier: %v", err)
			}
			if n == nil {
				t.Fatal("expected a disabled notifier, got nil")
			}
			if n.cfg.URL != "" {
				t.Fatalf("expected disabled notifier to keep an empty URL, got %q", n.cfg.URL)
			}
			if err := n.Notify(context.Background(), v1alpha1.PerformanceRegression{
				Name:   "regression-1",
				Status: v1alpha1.StatusDetected,
			}); err != nil {
				t.Fatalf("expected disabled notifier to no-op, got error: %v", err)
			}
		})
	}
}

// TestBuildNotifier_PagerDuty_RequiresRoutingKey verifies the one genuinely
// new required field this refactor introduces is actually enforced at
// construction time, with an actionable error message.
func TestBuildNotifier_PagerDuty_RequiresRoutingKey(t *testing.T) {
	if _, err := BuildNotifier(BuildConfig{Format: "pagerduty"}, nil, prometheus.NewRegistry()); err == nil {
		t.Fatal("expected an error when format=pagerduty has no routing key, got nil")
	}
}

// TestBuildNotifier_PagerDuty_OverridesURL verifies PagerDuty always posts
// to its fixed Events API v2 endpoint, ignoring any configured URL — a
// PagerDuty integration is identified by routing_key, not by a
// per-integration webhook URL.
func TestBuildNotifier_PagerDuty_OverridesURL(t *testing.T) {
	n, err := BuildNotifier(BuildConfig{
		Format:              "pagerduty",
		URL:                 "http://this-should-be-ignored.invalid",
		PagerDutyRoutingKey: "R0UTE",
	}, nil, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("BuildNotifier: %v", err)
	}
	if n.cfg.URL != pagerDutyEventsURL {
		t.Errorf("expected URL to be overridden to %q, got %q", pagerDutyEventsURL, n.cfg.URL)
	}
	if _, ok := n.cfg.Formatter.(*PagerDutyFormatter); !ok {
		t.Errorf("expected *PagerDutyFormatter, got %T", n.cfg.Formatter)
	}
}

// TestBuildNotifier_Custom_RequiresTemplate mirrors the pagerduty
// requiredness test for format=custom's required template.
func TestBuildNotifier_Custom_RequiresTemplate(t *testing.T) {
	if _, err := BuildNotifier(BuildConfig{Format: "custom", URL: "http://example.invalid"}, nil, prometheus.NewRegistry()); err == nil {
		t.Fatal("expected an error when format=custom has no template, got nil")
	}
}

// TestBuildNotifier_Custom_RejectsInvalidTemplate verifies a broken
// template surfaces through BuildNotifier, not just NewCustomFormatter
// directly, since BuildNotifier is what both real call sites actually use.
func TestBuildNotifier_Custom_RejectsInvalidTemplate(t *testing.T) {
	_, err := BuildNotifier(BuildConfig{
		Format:         "custom",
		URL:            "http://example.invalid",
		CustomTemplate: "{{ .Unclosed",
	}, nil, prometheus.NewRegistry())
	if err == nil {
		t.Fatal("expected an error for an unparseable custom template, got nil")
	}
}

// TestBuildNotifier_UnknownFormat verifies a typo'd/unsupported format is
// rejected clearly rather than silently falling back to Slack.
func TestBuildNotifier_UnknownFormat(t *testing.T) {
	if _, err := BuildNotifier(BuildConfig{Format: "webex"}, nil, prometheus.NewRegistry()); err == nil {
		t.Fatal("expected an error for an unknown format, got nil")
	}
}

// TestBuildNotifier_RejectsSSRFDestinations verifies the SSRF guard in
// validateWebhookURL is actually wired into BuildNotifier — the one place
// both real call sites (--dry-run/startup in internal/cli, and
// PostgresWatchReconciler) go through — for every format that sends
// cfg.URL as configured (pagerduty is exempt: see
// TestBuildNotifier_PagerDuty_IgnoresInvalidURL below).
func TestBuildNotifier_RejectsSSRFDestinations(t *testing.T) {
	badURLs := []string{
		"http://127.0.0.1/steal",
		"http://[::1]/steal",
		"http://169.254.169.254/latest/meta-data/",
		"http://metadata.google.internal/computeMetadata/v1/",
		"ftp://example.invalid/",
		"file:///etc/passwd",
		"not-a-url-at-all",
	}
	for _, format := range []string{"slack", "teams"} {
		for _, u := range badURLs {
			t.Run(format+"/"+u, func(t *testing.T) {
				if _, err := BuildNotifier(BuildConfig{Format: format, URL: u}, nil, prometheus.NewRegistry()); err == nil {
					t.Fatalf("expected BuildNotifier to reject url %q, got nil error", u)
				}
			})
		}
	}
}

// TestBuildNotifier_AllowsOrdinaryDestinations verifies the SSRF guard
// doesn't collaterally reject the ordinary case: a public-looking hostname,
// and (deliberately) an RFC1918 private address, since a self-hosted
// in-cluster webhook receiver is a legitimate, common destination — see
// validateWebhookURL's doc comment for why private ranges are out of scope
// for this check.
func TestBuildNotifier_AllowsOrdinaryDestinations(t *testing.T) {
	for _, u := range []string{
		"https://hooks.slack.example.com/services/T000/B000/XXX",
		"http://10.0.0.5:9094/webhook",
		"http://alertmanager.monitoring.svc.cluster.local:9093/webhook",
	} {
		t.Run(u, func(t *testing.T) {
			if _, err := BuildNotifier(BuildConfig{Format: "slack", URL: u}, nil, prometheus.NewRegistry()); err != nil {
				t.Errorf("expected url %q to be accepted, got error: %v", u, err)
			}
		})
	}
}

// TestBuildNotifier_PagerDuty_IgnoresInvalidURL verifies the SSRF guard is
// skipped for format=pagerduty, since its URL is always overridden to the
// fixed pagerDutyEventsURL constant and never comes from operator input —
// validating a value that's about to be discarded would only produce a
// confusing error for URLs that are already ignored.
func TestBuildNotifier_PagerDuty_IgnoresInvalidURL(t *testing.T) {
	_, err := BuildNotifier(BuildConfig{
		Format:              "pagerduty",
		URL:                 "http://169.254.169.254/latest/meta-data/",
		PagerDutyRoutingKey: "R0UTE",
	}, nil, prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("expected pagerduty to ignore its (bogus) URL rather than validate it, got error: %v", err)
	}
}

// TestValidateWebhookURL_RejectsEmpty verifies validateWebhookURL only accepts
// concrete destinations; the empty-string/no-alerting-configured case is
// handled earlier by BuildNotifier as an explicit disabled path.
func TestValidateWebhookURL_RejectsEmpty(t *testing.T) {
	if err := validateWebhookURL(""); err == nil {
		t.Fatal("expected an empty URL to be rejected, got nil")
	}
}
