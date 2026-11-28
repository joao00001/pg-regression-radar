# Where to look

*Which signal answers which question: the status of an object, a condition, a log line, a metric, an API, or the dashboard.*

## Overview

The radar tells you what it is doing in six places. Each is the right place for a different kind of question, and starting in the right one saves the most time. Read the first column as your question and the second as where the answer is.

All commands below assume the manager runs in `<ns>`. In the lab that is `pgrr-lab` and the Deployment is `pgrr-manager`; with the Helm chart the Deployment is `<release>-manager`.

## Start here

| You want to know | Look at | Command |
|---|---|---|
| Is the radar reading my database? | the `Collecting` condition of the PostgresWatch | `kubectl get pgw <name> -n <ns> -o jsonpath='{.status.conditions}'` |
| Can it tell a canary from the stable pods? | the `ArmsLog` condition | same command |
| May it pause or abort Rollouts right now, and if not, why? | the `Actuating` condition | same command |
| What is wrong with this watch or source? | `kubectl describe`, then the manager log | `kubectl describe pgw <name> -n <ns>` |
| Did my deploy reach the radar? | the manager log for `deploy event stored` | [A deploy never shows up](deploy-sources.md#a-deploy-never-shows-up) |
| Why did the engine not act on a deploy? | the decision line and the action ledger | [The engine only alerts and does not act](enforce.md#the-engine-only-alerts-and-does-not-act) |
| Is something failing now, or has it been for hours? | metrics | [Metrics](metrics.md) |
| What does this log line mean? | the catalog | [Log catalog](log-catalog.md) |
| What does this `reason` mean, and how do I fix it? | the reason codes | [Reason codes](reasons.md) |

## The six places

### 1. Status and conditions

`kubectl get` and `kubectl describe` show the state the manager last wrote. It is refreshed every 30 seconds. This is the first place to look because it needs no access to the manager pod and keeps the answer next to the object.

```bash
kubectl get pgw,pgds,pgreg -n <ns>
kubectl get pgw <name> -n <ns> -o json | jq '.status.conditions[] | {type, status, reason, message}'
```

| Object | Short name | What it tells you |
|---|---|---|
| `PostgresWatch` | `pgw` | `Phase` (`Pending`, `Running`, `Failed`), `Tracked Queries`, `status.lastScrapeTime`, and the conditions `Ready`, `Collecting`, `ArmsLog`, `Actuating` |
| `DeploySource` | `pgds` | `Phase`, the webhook `Path` it listens on, and the `Ready` condition |
| `PerformanceRegression` | `pgreg` | one detected regression: query, p-value, confidence and change |
| `PostgresRadarCluster` | `pgrc` | `Ready` for a remote cluster the radar reaches through a kubeconfig Secret |

A condition that is not healthy has a `reason` from the [reason codes](reasons.md) and a `message` that ends with the link to its entry.

!!! note "Conditions only exist while they apply"
    `Collecting` is absent when the sample source is not the built-in collector, `ArmsLog` when `attribution.arms` is off, `Actuating` when the attribution engine is off. A condition that is missing is not a failure.

### 2. The manager log

The log says what happened and when, and is the only place that keeps the order of events. Every line is one JSON object, so it can be filtered:

```bash
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --tail=500
kubectl logs -n <ns> deploy/<manager> --since=1h | jq -c 'select(.level == "WARN" or .level == "ERROR")'
```

With more than one replica, only the **leader** runs the watches. Followers log little. Find the leader from its `postgreswatch worker started` lines, or from the Lease:

```bash
kubectl get lease -n <ns> pg-regression-radar-manager.radar.pgregressionradar.io -o jsonpath='{.spec.holderIdentity}'
```

[Logs](logs.md) describes the format, the levels and the fields. The [log catalog](log-catalog.md) lists every message.

### 3. Reason codes

A reason code is the stable name of a situation. It appears in the `reason` field of log lines and as the `reason` of a condition, and it has a page entry with causes, how to confirm each and the fix. Search for what your system emits:

```bash
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --tail=2000 \
  | jq -r 'select(.reason) | .reason' | sort | uniq -c | sort -rn
```

### 4. Metrics

Metrics are for trends and for alerting. The manager serves the per-watch metrics on `--pg-metrics-bind-address` (`:9090`); see [Metrics](metrics.md).

### 5. The manager's HTTP endpoints

| Endpoint | Port flag | What it answers |
|---|---|---|
| `/healthz`, `/readyz` | `--health-probe-bind-address` (`:8081`) | is the process alive and ready |
| `/metrics` | `--pg-metrics-bind-address` (`:9090`) | [metrics](metrics.md) |
| `GET /api/v1/attribution/verdict` | `--attribution-verdict-addr` (off by default) | what the engine says about a deploy now |
| `GET /api/v1/attribution/actions` | same | the ledger of pauses and aborts, and `breakerOpen` |
| `/api/v1/regressions`, `/api/v1/watches` | `--dashboard-addr` (off by default) | regressions and watches as JSON, from every manager pod |
| the Observe API | `--observe-addr` (`127.0.0.1:8083`) | what the dashboard reads, from the leader only |
| the deploy webhook | `--webhook-bind-address` (`:8080`) | receives deploy events |

Reach one without exposing it:

```bash
kubectl -n <ns> port-forward deploy/<manager> 8082:8082
curl -s 'localhost:8082/api/v1/attribution/verdict?namespace=shop&app=checkout' | jq
curl -s 'localhost:8082/api/v1/attribution/actions' | jq
```

### 6. The dashboard

`pg-regression-radar dashboard` shows deploys, the judgement of each, and the query curves around it, with history. Use it when the question is "what did the engine see around this deploy", which no single log line answers. Regressions and a stopped radar stay on screen as [alerts](../dashboard.md#alerts) until dealt with. Its Source health and Safety panels and its action list show the same reason codes as the conditions and the logs, each linked to [Reason codes](reasons.md). See [Dashboard](../dashboard.md).

## Which place for which problem

| The problem looks like | Start at | Then |
|---|---|---|
| Nothing is detected | the `Collecting` condition | [Nothing is detected](detection.md) |
| The database side fails (extension, permissions, connection) | the `Ready` condition message and `collector: scrape failed` | [Setup and the database](setup.md) |
| A deploy is missing | `ingester: deploy event stored` in the log | [Deploy sources](deploy-sources.md) |
| A regression was detected and nobody was told | `postgreswatch: alert notify failed` | [Alerts and the dashboard](alerts-and-dashboard.md) |
| It judged a deploy and did not pause or abort it | the `Actuating` condition | [Enforce mode](enforce.md) |
| The manager does not start or restarts | `kubectl logs --previous` | [The manager does not start](setup.md#the-manager-does-not-start) |
| You need to report a bug | | [Collect a report](report.md) |

## If you only run the one-binary operator

The `operator` command has no custom resources, so there are no conditions. Everything it says is in its log and its `/metrics`, and `--dry-run` checks the configuration and the connection before it starts. The log lines are the same ones as in the [catalog](log-catalog.md), under *Operator (all-in-one mode)*.

## See also

- [Logs](logs.md), [Log catalog](log-catalog.md), [Reason codes](reasons.md) and [Metrics](metrics.md).
- [Troubleshooting toolkit](../kubernetes/troubleshooting.md) for the Kubernetes side: pods, events and RBAC.
