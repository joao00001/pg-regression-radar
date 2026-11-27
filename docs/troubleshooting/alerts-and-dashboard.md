# Alerts and the dashboard

*A regression was found and nobody was told, or the dashboard does not show what you expect.*

## Overview

Alerts leave the radar for a destination you configured; the dashboard reads the manager. Both are ways to see what the radar found, and each has a few failure modes of its own. A detected regression is always written first as a `PerformanceRegression`, so if that object exists the detection worked and the problem is delivery.

```bash
kubectl get pgreg -n <ns>
```

## An alert did not arrive

Find out which of three things happened: it was never sent, it was sent and failed, or it was delivered and you did not see it.

```bash
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --since=2h \
  | jq -c 'select(.msg | test("alert|notification|notify"))'
```

| You see | It means | Fix |
|---|---|---|
| nothing, and `pgreg` is empty | nothing was detected | [Nothing is detected](detection.md) |
| `alerting: notification skipped; webhook url is not configured` | the watch has no alert URL | set `spec.alerting.url` (or the legacy `spec.slackWebhookUrl`) |
| `postgreswatch: alert notify failed` | delivery failed | [`AlertNotifyFailed`](reasons.md#alertnotifyfailed): `err` has the status or the network error |
| `postgreswatch: failed to persist PerformanceRegression CR` | the alert went out and the object could not be saved | [`RegressionPersistFailed`](reasons.md#regressionpersistfailed) |
| `alerting: notification sent` with a `status_code` | the destination accepted it | look in the destination: the right channel, a filter, a rate limit |
| the watch is `Failed` with `build alert notifier` | the alert URL or policy is rejected when the watch starts | [`WorkerStartFailed`](reasons.md#workerstartfailed) |

A rejected URL is refused before any request is made: a scheme other than `http` or `https`, a literal loopback or link-local address, and the cloud metadata hostnames. A `3xx` from the destination counts as a failure, because the radar never follows redirects. See [Destination validation](../alerting.md#destination-validation-ssrf-hardening) and [Destination policies](../alerting.md#destination-policies).

Counts over time:

- `pg_regression_radar_regressions_detected_total` rises when a regression is found.
- `pg_regression_radar_notifications_total{status="success"}` and `{status="error"}` count delivery attempts by outcome. Detections that rise with no `success` mean alerts are being lost.

For regressions exported as OTLP logs, `pg_regression_radar_otlp_export_total{result}` and the lines `otlpexport: queue full`, `otlpexport: delivery failed, will retry` and `otlpexport: giving up on regression` say what happened; see [OTLP export](../otlp-export.md).

## The dashboard

```bash
pg-regression-radar dashboard --namespace <manager-namespace> --open
```

| The page says | Cause | Fix |
|---|---|---|
| *Reconnecting* | the leader changed, or the port-forward dropped | wait; the command finds the new leader. The terminal logs `dashboard: cannot reach the manager`, then `dashboard: connected to the manager` |
| *Incompatible* | the manager speaks an Observe API version this dashboard does not | upgrade the CLI to the manager's release; the page names the versions it supports |
| *No deploys in this time range* | no deploy has been registered, or it is not 10 minutes old yet | widen the range; check [A deploy never shows up](deploy-sources.md#a-deploy-never-shows-up) |
| history is not being recorded | the history store is down | [`HistoryNotRecording`](reasons.md#historynotrecording). Decisions are unaffected |
| history is lost after a restart | `--observe-history=memory`, the default | set it to `postgres` |
| the command cannot find the manager | your user cannot read the Lease or port-forward | needs `get` on `leases` and `pods`, `create` on `pods/portforward` |
| query text is missing | by design | `--show-queries` on the dashboard **and** `--observe-allow-queries` on the manager |

The dashboard reads the leader only. If the manager has more than one pod, a port-forward to a standby shows nothing, which is why the command resolves the leader from the Lease itself. See [Dashboard](../dashboard.md).

## See also

- [Alerting](../alerting.md) and [OTLP export](../otlp-export.md)
- [Where to look](where-to-look.md)
