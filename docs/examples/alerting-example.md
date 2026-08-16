# Alerting example

*Concrete webhook alerting example that shows how to configure delivery, receive a regression alert, and identify the deployment that caused it.*

## Overview

This page demonstrates a practical alerting setup for regression notifications using a Slack incoming webhook. The same pattern works with other webhook-compatible destinations: configure alerting in your run mode, trigger a known regression scenario, and verify that the delivered alert includes enough deploy metadata to identify the faulty release.

Use this together with [Example regression scenario](regression-scenario.md) so your alert test is repeatable.

## 1) Configure alert destination

Use a redacted placeholder webhook URL in docs/scripts:

```text
https://hooks.slack.com/services/XXX/YYY/ZZZ
```

### operator

```bash
go run ./cmd/operator \
  --dsn "******localhost:5432/mydb?sslmode=disable" \
  --cluster-name demo-cluster \
  --source-type generic \
  --alert-format slack \
  --alert-url https://hooks.slack.com/services/XXX/YYY/ZZZ
```

### manager (`PostgresWatch.spec.alerting`)

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: demo-db
  namespace: demo
spec:
  clusterName: demo-cluster
  dsnSecretRef:
    name: demo-db-dsn
    key: dsn
  alerting:
    format: slack
    url: https://hooks.slack.com/services/XXX/YYY/ZZZ
```

### Helm

```bash
helm upgrade --install pg-regression-radar ./deploy/helm/deploylens \
  --set postgres.dsn="******localhost:5432/mydb?sslmode=disable" \
  --set alerting.format=slack \
  --set alerting.url=https://hooks.slack.com/services/XXX/YYY/ZZZ
```

## 2) Trigger a known regression

Run the reproducible flow in [Example regression scenario](regression-scenario.md), including:

1. baseline deploy event (`revision=v1`),
2. query slowdown,
3. second deploy event (`revision=v2`),
4. continued query executions.

## 3) Confirm alert delivery

When the regression is detected, Slack receives a message similar to:

```text
🚨 PostgreSQL Regression Detected
Cluster: demo-cluster
App: checkout-api
Namespace: demo
Revision: v2
Query ID: 123456
Latency: 3.1ms -> 48.7ms (15.7x)
Confidence: 98%
```

If you are testing without a real Slack workspace, use a temporary webhook sink (for example, an internal relay or test endpoint) and confirm the same fields appear in the payload body.

## 4) Interpret which deployment is at fault

The key correlation fields are:

- **App + namespace**: workload identity.
- **Revision / image tag**: specific deployment candidate.
- **Regression timing + before/after latency**: evidence that degradation aligns with that deployment window.

In this example, `revision=v2` is the deployment to investigate/rollback first.

## 5) Common alerting validation issues

- **No alert arrives:** verify webhook URL, egress/network policy, and destination allowlist settings.
- **Detection happened but no destination accepted:** check `--alert-format`/`spec.alerting.format` matches destination type.
- **Payload missing deployment info:** confirm deploy events include app/namespace/revision fields.

## See also

- [Alerting](../alerting.md) — full formatter and destination-policy reference.
- [Deploy Sources & Webhooks](../webhooks.md) — deploy event source wiring and webhook auth.
- [Example regression scenario](regression-scenario.md) — reproducible data-plane + deploy-event test flow.
- [Quickstart Validation](../quickstart-validation.md) — complete external-user validation checklist.
