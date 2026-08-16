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
			if n.cfg.Formatter == nil {
				t.Fatal("expected disabled notifier to keep a non-nil formatter")
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
		"http://localhost/steal",
		"http://relay.localhost/steal",
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

func TestBuildNotifier_StrictAllowlist(t *testing.T) {
	t.Run("accepts exact hostnames and cidrs", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			url   string
			allow []string
		}{
			{
				name:  "hostname",
				url:   "https://hooks.slack.example.com/services/T000/B000/XXX",
				allow: []string{"hooks.slack.example.com"},
			},
			{
				name:  "cidr",
				url:   "http://10.0.0.5:9094/webhook",
				allow: []string{"10.0.0.0/8"},
			},
			{
				name:  "literal ip",
				url:   "https://203.0.113.10/webhook",
				allow: []string{"203.0.113.10"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := BuildNotifier(BuildConfig{
					Format:              "slack",
					URL:                 tc.url,
					AllowedDestinations: tc.allow,
				}, nil, prometheus.NewRegistry()); err != nil {
					t.Fatalf("expected %q to be accepted by allowlist %v, got %v", tc.url, tc.allow, err)
				}
			})
		}
	})

	t.Run("rejects destinations outside allowlist", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:              "slack",
			URL:                 "https://hooks.slack.example.com/services/T000/B000/XXX",
			AllowedDestinations: []string{"pagerduty.com", "203.0.113.0/24"},
		}, nil, prometheus.NewRegistry())
		if err == nil {
			t.Fatal("expected non-allowlisted destination to be rejected, got nil")
		}
	})

	t.Run("does not override existing SSRF blocklist", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:              "slack",
			URL:                 "http://127.0.0.1/steal",
			AllowedDestinations: []string{"127.0.0.1", "127.0.0.0/8"},
		}, nil, prometheus.NewRegistry())
		if err == nil {
			t.Fatal("expected loopback destination to stay blocked even if allowlisted, got nil")
		}
	})
}

// TestValidateWebhookURL_RejectsEmpty verifies validateWebhookURL only accepts
// concrete destinations; the empty-string/no-alerting-configured case is
// handled earlier by BuildNotifier as an explicit disabled path.
func TestValidateWebhookURL_RejectsEmpty(t *testing.T) {
	if err := validateWebhookURL("", nil); err == nil {
		t.Fatal("expected an empty URL to be rejected, got nil")
	}
}

func TestValidateAllowedDestinations(t *testing.T) {
	t.Run("accepts hostnames ips and cidrs", func(t *testing.T) {
		if err := ValidateAllowedDestinations([]string{
			"alertmanager",
			"hooks.slack.example.com",
			"10.0.0.0/8",
			"203.0.113.10",
		}); err != nil {
			t.Fatalf("expected allowlist to validate, got %v", err)
		}
	})

	t.Run("rejects malformed entries", func(t *testing.T) {
		if err := ValidateAllowedDestinations([]string{"https://example.com/webhook"}); err == nil {
			t.Fatal("expected malformed allowlist entry to be rejected, got nil")
		}
	})
}

// TestDestinationPolicy_Permissive verifies that the default permissive
// policy keeps backward-compatible behaviour: any URL that passes the SSRF
// blocklist is accepted, without any further allowlist check.
func TestDestinationPolicy_Permissive(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want bool
	}{
		{"ordinary external url", "https://hooks.slack.example.com/T000/B000/X", true},
		{"private range url (in-cluster receiver)", "http://10.0.0.5:9094/webhook", true},
		{"loopback rejected", "http://127.0.0.1/steal", false},
		{"link-local rejected", "http://169.254.169.254/latest/meta-data/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildNotifier(BuildConfig{
				Format:            "slack",
				URL:               tc.url,
				DestinationPolicy: DestinationPolicyPermissive,
			}, nil, prometheus.NewRegistry())
			if tc.want && err != nil {
				t.Errorf("expected url to be accepted with permissive policy, got error: %v", err)
			}
			if !tc.want && err == nil {
				t.Error("expected url to be rejected with permissive policy, got nil")
			}
		})
	}
}

// TestDestinationPolicy_Allowlist verifies that the allowlist policy rejects
// URLs whose host is not in AllowedDestinations, and that it requires the
// list to be non-empty at all.
func TestDestinationPolicy_Allowlist(t *testing.T) {
	t.Run("accepts URL matching allowlist", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:              "slack",
			URL:                 "https://hooks.slack.example.com/services/T000/B000/XXX",
			DestinationPolicy:   DestinationPolicyAllowlist,
			AllowedDestinations: []string{"hooks.slack.example.com"},
		}, nil, prometheus.NewRegistry())
		if err != nil {
			t.Fatalf("expected allowlist policy to accept matching URL, got error: %v", err)
		}
	})

	t.Run("rejects URL not in allowlist", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:              "slack",
			URL:                 "https://other.example.com/hook",
			DestinationPolicy:   DestinationPolicyAllowlist,
			AllowedDestinations: []string{"hooks.slack.example.com"},
		}, nil, prometheus.NewRegistry())
		if err == nil {
			t.Fatal("expected allowlist policy to reject URL outside allowlist, got nil")
		}
	})

	t.Run("fails when allowlist is empty", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:            "slack",
			URL:               "https://hooks.slack.example.com/hook",
			DestinationPolicy: DestinationPolicyAllowlist,
		}, nil, prometheus.NewRegistry())
		if err == nil {
			t.Fatal("expected allowlist policy to fail when AllowedDestinations is empty, got nil")
		}
	})
}

// TestDestinationPolicy_RelayOnly verifies that relay-only ignores the
// CRD-level URL entirely and always uses RelayURL, and rejects empty relay.
func TestDestinationPolicy_RelayOnly(t *testing.T) {
	const relay = "https://relay.example.com/webhook"

	t.Run("uses relay url, ignores CRD url", func(t *testing.T) {
		n, err := BuildNotifier(BuildConfig{
			Format:            "slack",
			URL:               "https://crd-url-should-be-ignored.invalid/hook",
			DestinationPolicy: DestinationPolicyRelayOnly,
			RelayURL:          relay,
		}, nil, prometheus.NewRegistry())
		if err != nil {
			t.Fatalf("expected relay-only policy to succeed, got error: %v", err)
		}
		if n.cfg.URL != relay {
			t.Errorf("expected notifier URL to be the relay %q, got %q", relay, n.cfg.URL)
		}
	})

	t.Run("rejects empty relay url", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:            "slack",
			URL:               "https://some-url.example.com/hook",
			DestinationPolicy: DestinationPolicyRelayOnly,
		}, nil, prometheus.NewRegistry())
		if err == nil {
			t.Fatal("expected relay-only policy to fail when RelayURL is empty, got nil")
		}
	})

	t.Run("relay url must pass SSRF blocklist", func(t *testing.T) {
		_, err := BuildNotifier(BuildConfig{
			Format:            "slack",
			DestinationPolicy: DestinationPolicyRelayOnly,
			RelayURL:          "http://169.254.169.254/latest/meta-data/",
		}, nil, prometheus.NewRegistry())
		if err == nil {
			t.Fatal("expected SSRF-blocked relay url to be rejected, got nil")
		}
	})
}

// TestValidateDestinationPolicy verifies startup-time validation of
// policy + relay-url combinations, independent of BuildNotifier.
func TestValidateDestinationPolicy(t *testing.T) {
	t.Run("permissive requires no relay url", func(t *testing.T) {
		if err := ValidateDestinationPolicy(DestinationPolicyPermissive, ""); err != nil {
			t.Fatalf("expected permissive+empty relay to be valid, got: %v", err)
		}
	})

	t.Run("allowlist requires no relay url", func(t *testing.T) {
		if err := ValidateDestinationPolicy(DestinationPolicyAllowlist, ""); err != nil {
			t.Fatalf("expected allowlist+empty relay to be valid, got: %v", err)
		}
	})

	t.Run("relay-only requires relay url", func(t *testing.T) {
		if err := ValidateDestinationPolicy(DestinationPolicyRelayOnly, ""); err == nil {
			t.Fatal("expected relay-only+empty relay to be invalid, got nil")
		}
	})

	t.Run("relay-only with relay url is valid", func(t *testing.T) {
		if err := ValidateDestinationPolicy(DestinationPolicyRelayOnly, "https://relay.example.com/hook"); err != nil {
			t.Fatalf("expected relay-only+relay url to be valid, got: %v", err)
		}
	})

	t.Run("unknown policy is rejected", func(t *testing.T) {
		if err := ValidateDestinationPolicy(DestinationPolicy("unknown"), ""); err == nil {
			t.Fatal("expected unknown policy to be rejected, got nil")
		}
	})

	t.Run("empty policy is treated as permissive", func(t *testing.T) {
		if err := ValidateDestinationPolicy("", ""); err != nil {
			t.Fatalf("expected empty policy to be accepted (treated as permissive), got: %v", err)
		}
	})
}
