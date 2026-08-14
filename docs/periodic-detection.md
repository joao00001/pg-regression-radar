# Periodic (Deploy-Independent) Detection

*Regression detection that doesn't need a deploy at all.*

## Overview

By default, this project only ever analyses a query around a tracked `DeployEvent`: `Engine.Analyse` builds a window around the deploy's timestamp and only confirms a regression whose change point falls close enough to it. That means a real, significant latency regression with no deploy behind it — autovacuum falling behind, index/table bloat, stale planner statistics, organic data growth, a connection-pool or traffic-pattern change — goes completely undetected, and using this feature at all requires wiring up a [DeploySource](webhooks.md), even for clusters that don't want deploy correlation specifically.

Periodic detection is a second, independent trigger path: `Engine.AnalysePeriodic` runs the same E-divisive-change-point-plus-Welch's-t-test machinery on a rolling schedule, per tracked query, with no `DeployEvent` involved at all. It is **additive, not a replacement** — deploy-triggered detection keeps working exactly as before, unaffected by whether periodic detection is enabled. A `PostgresWatch` (or the CLI) can have either, both, or neither active. See [ADR-0001](adr/0001-deploy-independent-regression-detection.md) for the full design rationale.

This is opt-in, on purpose:

- Disabled by default, both on the CLI (`--periodic-detection`) and the CRD (`spec.periodicDetection.enabled`).
- Per-watch/per-process. Enabling it for one `PostgresWatch`, or one `cmd/operator` process, doesn't affect any other.
- Available in both run modes at once (see [Architecture Overview](architecture.md)) — this was a deliberate scope decision (see ADR-0001), not staged.

## How the window works

Each tick, the engine looks at a window of `2 * windowMinutes` ending now, and splits it in half: the *older* half is the baseline, the *newer* half is what gets compared against it. There is no deploy timestamp to anchor the split on, so `windowMinutes` defaults to `60` — double the deploy-triggered default of `30` — since a shorter window would be noisier with nothing narrowing down where to look.

This is a genuinely simpler baseline than deploy-triggered detection's: it's vulnerable to ordinary daily/weekly traffic-pattern shifts producing a "detected" result with nothing actually wrong (a start-of-business-hours load ramp is the obvious failure case). It is mitigated, not eliminated, by a longer default window and by the existing `latencyChangeThreshold`/`pValueThreshold` significance bar — the same statistical test deploy-triggered detection uses, just without a deploy to check the change point against. Treat the defaults as a starting point to tune against your own traffic, not a validated answer, especially in the first few weeks of using this.

## Suppression: re-arm, not cooldown

A query that trips periodic detection stays flagged internally until it recovers — it will not re-fire on every subsequent tick just because the same underlying condition is still present. Once a tick shows the query's latency has come back down within `latencyChangeThreshold` of a fresh baseline, it re-arms, so a later, genuinely new episode of regression fires again. This was a deliberate choice over a fixed time-based cooldown, which either re-alerts on a schedule with nothing new to say, or goes silent well past the point someone fixed and then re-broke the same thing.

This suppression state is in-memory, matching the rest of this project's default runtime state (the Collector's samples, `PendingSet`'s dedup) — it resets on restart. A regression already in progress at restart time will fire one more alert rather than staying silently suppressed forever.

## Configuration

### `cmd/operator` (CLI mode)

```
--periodic-detection
--periodic-window-minutes=60
--periodic-interval-minutes=15
```

| Flag | Default | Description |
|---|---|---|
| `--periodic-detection` | `false` | Turns on periodic detection for this process. |
| `--periodic-window-minutes` | `60` | Split-window size; the engine compares the most recent half of `2 * this value` minutes against the older half. |
| `--periodic-interval-minutes` | `15` | How often a full pass over every tracked query runs. |

### `PostgresWatch` (manager/CRD mode)

```yaml
apiVersion: radar.pgregressionradar.io/v1alpha1
kind: PostgresWatch
metadata:
  name: prod-db
spec:
  clusterName: prod
  dsnSecretRef: {name: prod-db-dsn, key: dsn}
  periodicDetection:
    enabled: true
    windowMinutes: 60      # optional; this is the default
    intervalMinutes: 15    # optional; this is the default
```

| Field | Default | Description |
|---|---|---|
| `enabled` | `false` | Turns on periodic detection for this watch. |
| `windowMinutes` | `60` | Same meaning as `--periodic-window-minutes`. |
| `intervalMinutes` | `15` | Same meaning as `--periodic-interval-minutes`. |

## Telling periodic and deploy-triggered regressions apart

Every `PerformanceRegression` now carries `triggerType`: `deploy` (the default, matching every regression created before this field existed) or `periodic`. A periodic regression's `deployEventId` is always empty — there is no deploy event to reference. Alerts (Slack/Teams/PagerDuty/custom) and `kubectl get performanceregressions` both surface `triggerType`, so it's always clear which path fired.

Unlike deploy-triggered regressions, which are named after the triggering deploy event so each deploy is its own record, a periodic regression uses one stable name per query (`periodic-q<queryID>`). Repeated episodes for the same query update that same object rather than accumulating a new `PerformanceRegression` on every tick — periodic detection has no natural "this episode is over" boundary the way a deploy's fixed analysis window does.

## See also

- [ADR-0001: Deploy-independent regression detection](adr/0001-deploy-independent-regression-detection.md) — the design decisions and trade-offs behind this feature.
- [Detection Algorithm](detection-algorithm.md) — the E-divisive/Welch's-t-test core both trigger paths share.
- [Configuration Reference](configuration.md) — the full flag/field reference across every binary and CRD.
- [Auto-Abort (Argo Rollouts)](auto-abort.md) — auto-abort only ever acts on deploy-triggered regressions; a periodic-triggered regression is never auto-aborted, since there is no deploy to abort.
