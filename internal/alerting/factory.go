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
	// In relay-only mode this field is also ignored; RelayUrl is used instead.
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

	// AllowedDestinations enables a stricter, opt-in destination allowlist:
	// when non-empty, the webhook URL's host must match one of these exact
	// hostnames/IPs or fall inside one of these CIDRs. This is intended for
	// multi-tenant clusters where only a fixed set of alert receivers should be
	// reachable from PostgresWatch-driven alerting.
	AllowedDestinations []string

	// DestinationPolicy selects the destination-validation strategy. Leave
	// empty (or set to "permissive") for the original SSRF-blocklist-only
	// behaviour. See the DestinationPolicy constants for available values and
	// docs/alerting.md#destination-policies.
	DestinationPolicy DestinationPolicy

	// RelayUrl is the fixed relay endpoint used when DestinationPolicy is
	// "relay-only". Ignored for all other policies.
	RelayUrl string

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
	if err := ValidateAllowedDestinations(cfg.AllowedDestinations); err != nil {
		return nil, err
	}

	// Resolve the effective URL based on the destination policy.  This must
	// happen before format-specific validation so the correct URL is checked
	// and used throughout.
	effectiveURL, err := resolveDestinationURL(cfg)
	if err != nil {
		return nil, err
	}
	// Use the policy-resolved URL for all subsequent logic.
	cfg.URL = effectiveURL

	format := cfg.Format
	if format == "" {
		format = "slack"
	}

	url := cfg.URL
	if format != "pagerduty" && url == "" {
		switch format {
		case "slack", "teams", "custom":
			return NewWebhookNotifier(WebhookConfig{
				URL:         "",
				Timeout:     cfg.Timeout,
				ClusterName: cfg.ClusterName,
				Formatter:   noopFormatter{},
				Registerer:  reg,
			}, logger), nil
		default:
			return nil, fmt.Errorf("alerting: unknown format %q (want slack, teams, pagerduty, or custom)", format)
		}
	}

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

	// pagerduty's url is always the fixed pagerDutyEventsURL constant above,
	// never operator input, so there's nothing to validate for it. Every
	// other format sends cfg.URL -- a value that ultimately comes from a
	// --alert-url/--slack-url flag or a PostgresWatch's
	// spec.alerting.url/spec.slackWebhookUrl, i.e. something anyone able to
	// create or edit a PostgresWatch controls. Validate it here so both real
	// call sites (internal/cli.RunOperator's --dry-run and startup path, and
	// PostgresWatchReconciler's reconcile) get the same check for free.
	if format != "pagerduty" {
		if err := validateWebhookURL(url, cfg.AllowedDestinations); err != nil {
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

// resolveDestinationURL applies the DestinationPolicy in cfg and returns the
// effective URL that BuildNotifier should use.  It enforces policy-specific
// constraints before any format-level validation runs:
//
//   - permissive (default): returns cfg.URL unchanged; the SSRF blocklist
//     applied later by validateWebhookURL is the only guard.
//   - allowlist: returns cfg.URL unchanged but guarantees AllowedDestinations
//     is non-empty; if the list is empty the caller's
//     --alerting-allowed-destinations was missing, which is a misconfiguration.
//   - relay-only: returns cfg.RelayUrl, ignoring cfg.URL entirely; returns an
//     error when RelayUrl is empty because the policy is meaningless without it.
func resolveDestinationURL(cfg BuildConfig) (string, error) {
	switch cfg.DestinationPolicy {
	case DestinationPolicyRelayOnly:
		if cfg.RelayUrl == "" {
			return "", fmt.Errorf("alerting: destination policy %q requires --alerting-destination-policy-relay-url to be set", DestinationPolicyRelayOnly)
		}
		return cfg.RelayUrl, nil
	case DestinationPolicyAllowlist:
		if len(cfg.AllowedDestinations) == 0 {
			return "", fmt.Errorf("alerting: destination policy %q requires --alerting-allowed-destinations to be non-empty", DestinationPolicyAllowlist)
		}
		return cfg.URL, nil
	case DestinationPolicyPermissive, "":
		return cfg.URL, nil
	default:
		return "", fmt.Errorf("alerting: unknown destination policy %q (want permissive, allowlist, or relay-only)", cfg.DestinationPolicy)
	}
}

// ValidateDestinationPolicy checks that the combination of policy and
// relayUrl is coherent at startup — before any PostgresWatch is reconciled.
// This is analogous to ValidateAllowedDestinations: callers (CLI flag
// parsing in internal/cli) can surface misconfiguration immediately rather
// than discovering it at the first reconcile.
//
// Specifically:
//   - "relay-only" without a relayUrl is rejected with a clear message.
//   - Unknown policy strings are rejected.
//   - The relay URL itself is NOT validated here (validateWebhookURL is
//     called later inside BuildNotifier, where the policy and URL are both
//     available at the same time).
func ValidateDestinationPolicy(policy DestinationPolicy, relayUrl string) error {
	switch policy {
	case DestinationPolicyRelayOnly:
		if relayUrl == "" {
			return fmt.Errorf("alerting: destination policy %q requires --alerting-destination-policy-relay-url to be set", policy)
		}
		return nil
	case DestinationPolicyAllowlist, DestinationPolicyPermissive, "":
		return nil
	default:
		return fmt.Errorf("alerting: unknown destination policy %q (want permissive, allowlist, or relay-only)", policy)
	}
}


// hostnames -- the single highest-value SSRF target, since a request that
// reaches one of these from inside a cloud VM/pod can return the node's own
// cloud credentials. Matched case-insensitively against the URL's hostname
// (not the IP it happens to resolve to).
var blockedAlertDestinationHosts = map[string]bool{
	"metadata.google.internal": true, // GCP
	"metadata.internal":        true, // GCP (short form some images alias)
	"metadata.azure.com":       true, // Azure IMDS is normally reached via 169.254.169.254 directly, but block the hostname too
	"localhost":                true, // RFC 6761: resolves to loopback
}

// validateWebhookURL rejects the alert-destination shapes that have no
// legitimate use as a Slack/Teams/custom webhook target and are the classic
// SSRF payloads: non-HTTP(S) schemes, loopback/link-local literal IPs (which
// covers the 169.254.169.254 cloud metadata address shared by AWS/GCP/Azure/
// DigitalOcean/Alibaba), and known cloud metadata hostnames. When
// allowedDestinations is non-empty, it additionally requires the webhook
// target's host to match an explicit hostname/IP/CIDR allowlist.
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
func validateWebhookURL(rawURL string, allowedDestinations []string) error {
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
	if isBlockedAlertDestinationHost(host) {
		return fmt.Errorf("alerting: webhook url %q targets a blocked metadata or loopback hostname, which is never a valid alert destination", rawURL)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("alerting: webhook url %q targets a loopback or link-local address (this includes the 169.254.169.254 cloud metadata address), which is never a valid alert destination", rawURL)
		}
	}
	if len(allowedDestinations) > 0 && !matchesAllowedDestination(host, allowedDestinations) {
		return fmt.Errorf("alerting: webhook url %q host %q is not in the configured alerting destination allowlist", rawURL, host)
	}
	return nil
}

// ValidateAllowedDestinations checks the syntax of the stricter, opt-in alert
// destination allowlist used by BuildNotifier and the operator/manager CLI
// flags. Entries must be exact hostnames, exact IP addresses, or CIDRs.
func ValidateAllowedDestinations(allowedDestinations []string) error {
	for _, raw := range allowedDestinations {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err == nil {
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			continue
		}
		if !isValidAllowedHostname(entry) {
			return fmt.Errorf("alerting: invalid allowed destination %q (want an exact hostname, IP, or CIDR)", raw)
		}
	}
	return nil
}

func matchesAllowedDestination(host string, allowedDestinations []string) bool {
	hostIP := net.ParseIP(strings.Trim(host, "[]"))
	for _, raw := range allowedDestinations {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if hostIP != nil && cidr.Contains(hostIP) {
				return true
			}
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			if hostIP != nil && hostIP.Equal(ip) {
				return true
			}
			continue
		}
		if strings.EqualFold(host, entry) {
			return true
		}
	}
	return false
}

func isValidAllowedHostname(host string) bool {
	if host == "" || strings.Contains(host, "://") || strings.ContainsAny(host, "/[]") {
		return false
	}
	// Bare names are allowed intentionally: in-cluster alert receivers are
	// often addressed as a short Service DNS name ("alertmanager") rather than
	// an FQDN. Safety-sensitive single-label names like "localhost" are still
	// rejected by the ordinary URL blocklist above.
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func isBlockedAlertDestinationHost(host string) bool {
	lowerHost := strings.ToLower(host)
	return blockedAlertDestinationHosts[lowerHost] || strings.HasSuffix(lowerHost, ".localhost")
}
