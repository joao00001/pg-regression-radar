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
	"testing"
)

// TestBuildNotifier_DefaultsToSlack verifies an empty Format behaves
// exactly like this package's original, Slack-only API — the backward
// compatibility BuildConfig's doc comment promises.
func TestBuildNotifier_DefaultsToSlack(t *testing.T) {
	n, err := BuildNotifier(BuildConfig{URL: "http://example.invalid", ClusterName: "test"}, nil)
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
	n, err := BuildNotifier(BuildConfig{Format: "teams", URL: "http://example.invalid"}, nil)
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

// TestBuildNotifier_PagerDuty_RequiresRoutingKey verifies the one genuinely
// new required field this refactor introduces is actually enforced at
// construction time, with an actionable error message.
func TestBuildNotifier_PagerDuty_RequiresRoutingKey(t *testing.T) {
	if _, err := BuildNotifier(BuildConfig{Format: "pagerduty"}, nil); err == nil {
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
	}, nil)
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
	if _, err := BuildNotifier(BuildConfig{Format: "custom", URL: "http://example.invalid"}, nil); err == nil {
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
	}, nil)
	if err == nil {
		t.Fatal("expected an error for an unparseable custom template, got nil")
	}
}

// TestBuildNotifier_UnknownFormat verifies a typo'd/unsupported format is
// rejected clearly rather than silently falling back to Slack.
func TestBuildNotifier_UnknownFormat(t *testing.T) {
	if _, err := BuildNotifier(BuildConfig{Format: "webex"}, nil); err == nil {
		t.Fatal("expected an error for an unknown format, got nil")
	}
}
