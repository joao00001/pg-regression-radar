# Alerting

*How a detected regression gets reported, and to what.*

## Overview

When the Correlation Engine detects a regression, `internal/alerting.WebhookNotifier` POSTs it to a webhook endpoint. What that payload actually looks like is decided by a `Formatter` — this project ships four:

| Format | Destination | Notes |
|---|---|---|
| `slack` (default) | Slack (or Slack-compatible, e.g. Mattermost) incoming webhook | This project's original, and still default, format. |
| `teams` | Microsoft Teams Incoming Webhook connector | Classic MessageCard payload — the same shape Teams' own webhook connector documents today. |
| `pagerduty` | PagerDuty Events API v2 | Opens (or updates, on a repeat notification for the same regression) an incident; always posts to PagerDuty's fixed Events API endpoint, authenticated by a routing key rather than a per-integration URL. |
| `custom` | Anything | A Go `text/template` you supply renders the body — see [Custom format](#custom-format). |

Every format reports the same underlying fields (deploy event, query ID/text, latency before/after, change factor, confidence, change point, and — when `--capture-plans`/`spec.capturePlans` is enabled — the plan-diff summary), just laid out however each destination expects.

## Configuration

**CLI (`cmd/operator`, or the unified `pg-regression-radar operator` subcommand):**

```bash
pg-regression-radar operator \
  --dsn "$DSN" \
  --alert-format teams \
  --alert-url "https://example.webhook.office.com/webhookb2/..." \
  ...
```

| Flag | Default | Description |
|---|---|---|
| `--alert-format` | `slack` | `slack`, `teams`, `pagerduty`, or `custom` |
| `--alert-url` | `` | Webhook URL for `slack`/`teams`/`custom`; ignored for `pagerduty`. Falls back to `--slack-url` when unset |
| `--pagerduty-routing-key` | `` | Required when `--alert-format=pagerduty` |
| `--alert-template` | `` | Go `text/template` source, inline; required (or use `--alert-template-file`) when `--alert-format=custom` |
| `--alert-template-file` | `` | Path to a template file — alternative to `--alert-template` |

`--dry-run` validates the alerting configuration (unknown format, missing routing key, unparseable template) before touching Postgres, so a typo'd `--alert-format` fails immediately instead of on the first detected regression.

**CRD (`PostgresWatch.spec.alerting`, `cmd/manager`):**

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: prod-db
spec:
  clusterName: prod
  dsnSecretRef: {name: prod-db-dsn, key: dsn}
  alerting:
    format: pagerduty
    pagerDutyRoutingKey: R0UTE1NG-KEY
```

| Field | Default | Description |
|---|---|---|
| `format` | `""` (→ `slack`) | `slack`, `teams`, `pagerduty`, or `custom` |
| `url` | `""` | Webhook URL for `slack`/`teams`/`custom`; ignored for `pagerduty` |
| `pagerDutyRoutingKey` | `""` | Required when `format` is `pagerduty` |
| `customTemplate` | `""` | Required when `format` is `custom` — inline, since a CR spec has no local filesystem to reference |

`spec.alerting` supersedes `spec.slackWebhookUrl` entirely when set — there is no field-by-field merge between the two. Leave `alerting` unset to keep using `slackWebhookUrl` (or no alerting at all, if that's empty too) exactly as before this field existed.

**Helm chart:** the `alerting.*` values (`format`, `url`, `pagerDutyRoutingKey`, `customTemplate`, alongside the pre-existing `slackWebhookUrl`) feed both `mode: operator` (as CLI flags, via a Secret) and `mode: manager` (as `spec.alerting` on the default `PostgresWatch`) — see [Installation](installation.md).

## Destination validation (SSRF hardening)

`--alert-url`/`spec.alerting.url` (and the deprecated `--slack-url`/`spec.slackWebhookUrl`) are free-text: whoever can pass the flag or edit the `PostgresWatch` controls where every detected regression's payload gets sent. Since that payload can include query text and, with `--capture-plans`/`spec.capturePlans` on, plan-diff content, an unvalidated destination is both a spoofing/notification-injection risk and a potential exfiltration/SSRF path. `BuildNotifier` (the one place both the CLI and the `manager` controller construct a notifier from that configuration) rejects a configured URL outright, before ever making a request, when it:

- uses a scheme other than `http`/`https` (rules out `file://`, `ftp://`, `gopher://`, and similar);
- is a literal loopback or link-local address — this covers `127.0.0.1`/`::1` and, notably, `169.254.169.254`, the cloud instance-metadata address shared by AWS, GCP, Azure, DigitalOcean, and Alibaba, which can hand back the node's own cloud credentials to whatever reaches it; or
- targets a well-known cloud metadata hostname (`metadata.google.internal`, etc.).

Separately, `WebhookNotifier` never follows HTTP redirects — a `3xx` response from the configured endpoint is treated as a delivery failure rather than a hop to a second, unvalidated destination, closing the usual way around an up-front check like the one above.

This check is intentionally narrow, not a complete SSRF defence: it validates the URL's literal host, not whatever address it eventually resolves to (so it doesn't defend against DNS rebinding), and it does **not** block ordinary private (RFC1918) addresses, since a self-hosted, in-cluster webhook receiver — an internal Alertmanager, a homegrown relay — legitimately lives at exactly one of those addresses, and blocking them by default would break that deployment shape rather than an attack. If your environment needs a stricter guarantee than this (e.g. only alerting to a pre-approved allowlist of hosts), enforce that with an admission policy in front of `PostgresWatch`/the operator's flags — the same scoping this project uses for the Secret-consent-label and kubeconfig restrictions documented in [Multi-Cluster (Fleet) Mode](multi-cluster.md).

`pagerduty` is exempt from all of the above: its destination is always PagerDuty's own fixed Events API endpoint, never the configured URL.

## Custom format

`--alert-format=custom` / `spec.alerting.format: custom` renders the notification body from a Go [`text/template`](https://pkg.go.dev/text/template) you supply, for any destination that isn't Slack/Teams/PagerDuty-shaped — a generic HTTP endpoint, an internal on-call tool, an n8n/Zapier relay, or anything else with its own expected JSON (or non-JSON) body.

The template is executed against these fields:

| Field | Type | Example |
|---|---|---|
| `.ClusterName` | string | `prod-east` |
| `.DeployEventID` | string | `deploy-abc123` |
| `.QueryID` | int64 | `987654321` |
| `.QueryText` | string | `SELECT * FROM orders WHERE customer_id = $1` |
| `.ConfidenceScore` | float64 | `0.97` |
| `.ConfidencePercent` | string (pre-formatted) | `97%` |
| `.MeanLatencyBeforeMs` | string (pre-formatted) | `12.50` |
| `.MeanLatencyAfterMs` | string (pre-formatted) | `84.20` |
| `.LatencyChangeFactor` | string (pre-formatted) | `6.74x` |
| `.ChangePointRFC3339` | string | `2026-08-12T10:00:00Z` |
| `.ExternalCauseSuspected` | bool | `false` |
| `.PlanDiffSummary` | string | `` (empty unless `--capture-plans`/`spec.capturePlans` is on) |

The pre-formatted fields (`ConfidencePercent`, `MeanLatencyBeforeMs`, etc.) match exactly what the built-in Slack/Teams/PagerDuty formatters render, so a template author gets the same numbers without reimplementing that formatting.

Example — a minimal generic JSON body:

```gotemplate
{
  "cluster": "{{.ClusterName}}",
  "query_id": {{.QueryID}},
  "confidence": "{{.ConfidencePercent}}",
  "summary": "query {{.QueryID}} latency changed {{.LatencyChangeFactor}} on {{.ClusterName}}"
}
```

The rendered body is sent with `Content-Type: application/json` by default; a non-JSON template (plain text, form-encoded, etc.) can pass a different content type — the CRD/CLI surface doesn't expose this today (it defaults to `application/json`), since every destination worth templating for accepts JSON.

A broken template (bad `text/template` syntax) is rejected at construction time — `--dry-run`, or the first reconcile of a `PostgresWatch` with a bad `customTemplate` — rather than only failing the first time a regression is actually detected.
