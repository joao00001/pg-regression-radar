# Enforce mode

*Why the engine judged a deploy and did not pause or abort it, what to check before you let it act, and how to stop and restart it.*

## Overview

The attribution engine works in steps, and each one is a place it can stop. Knowing which step a deploy reached is most of the diagnosis.

```text
registered ─▶ judged ─▶ decision ─▶ verdict served ─▶ manager acts ─▶ abort checked
 (10 min)     (every 5)   none/alert    (enforce)      (actuate)       (15 min later)
                          hold/rollback
```

- **Shadow mode** (`attribution.mode: shadow`, the default) judges and logs. The verdict is always `pass`, and nothing is written to a Rollout.
- **Enforce** serves a verdict for a rollout controller to poll, and a decision is acted on only if the controller asks.
- **Enforce with `actuate.enabled`** lets the manager itself pause (`hold`) or abort (`rollback`) the Rollout.

A deploy cannot be convicted before about 15 minutes after it. Everything below assumes the Rollout waits that long.

## Read the three conditions first

The status of the watch already says which step is blocked. `Actuating` is the one that answers "may it act right now".

```bash
kubectl get pgw <name> -n <ns> -o json | jq '.status.conditions[] | select(.type=="Actuating" or .type=="ArmsLog" or .type=="Collecting") | {type, status, reason, message}'
```

`Actuating` has a single reason at a time, and they are checked in this order:

| `Actuating` is | Reason | What it means | Fix |
|---|---|---|---|
| `False` | [`BreakerOpen`](reasons.md#breakeropen) | earlier aborts did not help, so the engine only alerts | [close the breaker](#the-circuit-breaker) after finding out why |
| `False` | [`ShadowMode`](reasons.md#shadowmode) | the watch is in shadow mode | set `attribution.mode: enforce` once the shadow decisions look right |
| `False` | [`ActuationNotAsked`](reasons.md#actuationnotasked) | enforce, without `attribution.actuate.enabled` | enable `actuate`, or let the rollout controller act on the verdict |
| `False` | [`NoActuator`](reasons.md#noactuator) | `actuate` is on and the manager cannot patch Rollouts | set `manager.autoAbort.rbacEnabled: true` in the chart |
| `False` | [`ActuationPaused`](reasons.md#actuationpaused) | the kill switch annotation is set | remove it |
| `True` | [`ActuationReady`](reasons.md#actuationready) | the manager may pause and abort Rollouts | nothing: if a deploy was not acted on, see below |

## Before you turn enforce on

Run this once, in order. Each line is a way a first real regression has gone unhandled.

1. **Run shadow mode first, and compare.** Leave the watch in shadow for a few deploys. For each, compare the decision line's `action` with what happened. The verdict endpoint's `wouldBe` shows what enforce would have said. Turn enforce on when you would have agreed with the engine.

    ```bash
    kubectl logs -n <ns> -l app.kubernetes.io/component=manager --since=24h \
      | jq -r 'select(.msg | startswith("attribution (shadow)")) | "\(.time) \(.deploy) \(.action) grade=\(.grade) \(.reasons)"'
    ```

2. **The source is `argo-rollouts`.** Only a deploy that an `argo-rollouts` DeploySource reported can be acted on, and the Rollout must carry the app's name, in the deploy's namespace. See [Deploy sources](deploy-sources.md#an-argo-rollouts-deploy-is-judged-but-never-paused).
3. **The manager may patch Rollouts.** `manager.autoAbort.rbacEnabled: true` grants `patch` on `rollouts` and `rollouts/status`. Check it:

    ```bash
    kubectl auth can-i patch rollouts.argoproj.io -n <app-ns> --as system:serviceaccount:<ns>:<manager-service-account>
    kubectl auth can-i patch rollouts.argoproj.io/status -n <app-ns> --as system:serviceaccount:<ns>:<manager-service-account>
    ```

4. **The Rollout waits long enough.** It needs a pause step of at least 20 minutes, or the verdict arrives after the release is finished and can only alert.
5. **If you want aborts, grade A must be available.** An abort needs grade A evidence, which needs the Postgres log: `attribution.arms`, the `ArmsLog` condition `True`, `manager.arms.rbacEnabled` for pod logs, and every pod reporting an `application_name` that starts with its own name. Without it the strongest action is a pause. See [Postgres log (grade A)](#grade-a-needs-the-postgres-log).
6. **`actuate.namespaces` is what you mean.** Empty means every namespace. A deploy outside the list is judged and logged and recorded as `skipped`.
7. **You know the kill switch.** `kubectl annotate postgreswatch <name> -n <ns> pg-regression-radar.io/actuation-paused=true` stops all pausing and aborting at once, with no restart. Remove it to resume. If the watch cannot be read at that moment, the manager assumes it is paused.
8. **You know what a restart forgets.** The action ledger (the last 200 attempts) and the breaker's count live in memory. The metrics and the log are the durable record.
9. **Someone reads the alerts.** The first real action is a pause, which one click resumes. A pause with nobody watching holds the release.

## The engine only alerts and does not act

Start with the decision line and its `reasons`, in the manager log (`attribution (...)`) and in the verdict at `/api/v1/attribution/verdict`. Three cases cover almost everything.

1. **"evidence is capped at grade C by: ..."** The engine saw a regression but could not prove it was the deploy, so it only alerts. The flags say what held it back:
    - `few-donors`: fewer than 5 untouched queries with enough history to build a control. A control query needs data for at least half of the 90-minute pre-deploy window, so right after the manager starts or restarts the engine stays at alert for about 45 minutes. Wait, or check that the watch's collector sees enough distinct queries.
    - `global-shift`: most untouched queries moved the same way at the deploy, so the cause may be load, not the code. The engine cannot separate them without a control arm.
    - `table-level`: most queries on one table moved together and the deploy did not declare that table.
    - `warming-up`, `transient`: the shift is too recent, or was fading, so it is not yet confirmed. Wait for the next evaluation (every 5 minutes).
2. **"seen in 1 of 2 consecutive evaluations required"** and **"cooldown after the previous action"** are guards, not problems: a pause needs the same evidence twice, and a second action waits 10 minutes.
3. **Nothing is paused although the engine wants a hold.** The `Actuating` condition says whether the manager may act at all. If it is `True`, look at the action ledger (`GET /api/v1/attribution/actions`) and at the `reason` of the line after the decision:

    | Ledger `result` | Log `reason` | Meaning |
    |---|---|---|
    | `skipped` | [`NamespaceNotAllowed`](reasons.md#namespacenotallowed) | the namespace is not in `actuate.namespaces` |
    | `skipped` | [`ActuationPaused`](reasons.md#actuationpaused) | the kill-switch annotation is set |
    | `skipped` | [`NotArgoRollout`](reasons.md#notargorollout) | the deploy did not come from an `argo-rollouts` DeploySource |
    | `failed` | [`ActuationFailed`](reasons.md#actuationfailed) | the call to the Rollout was refused, usually because `manager.autoAbort.rbacEnabled` is off, or the Rollout has another name |
    | `done` | [`Acted`](reasons.md#acted) | it acted |

4. **The deploy was the first of its app.** With no history to compare against the engine says it cannot tell and never convicts it, and there is no earlier revision to roll back to.

The engine cannot convict a deploy before about 15 minutes after it. A Rollout needs a pause step of at least 20 minutes for the action to land in time. See [Attribution engine](../attribution-engine.md).

### A line for each step

| You see | The deploy reached | Next |
|---|---|---|
| [`DeployRegistered`](reasons.md#deployregistered) only | the engine, not yet judged | wait for `firstEvaluation` |
| [`NoSamples`](reasons.md#nosamples) | judged and skipped: nothing to read | [Nothing is detected](detection.md#1-is-it-reading-the-database) |
| [`ShadowMode`](reasons.md#shadowmode) | a decision, not executed | the watch is in shadow mode |
| [`BelowActingThreshold`](reasons.md#belowactingthreshold) | a decision that never acts (`none`, `alert`) | the first list above |
| [`VerdictServed`](reasons.md#verdictserved) | a `hold` or `rollback`, enforced | the rollout controller or `actuate` acts |
| [`Acted`](reasons.md#acted) | the Rollout was patched | wait for the abort check |
| [`AbortRecovered`](reasons.md#abortrecovered) / [`AbortPersisted`](reasons.md#abortpersisted) | the abort was checked 15 minutes later | persisted twice opens the breaker |

## Grade A needs the Postgres log

A canary and the stable pods can be told apart only by the Postgres log, which records each statement's `application_name`. The `ArmsLog` condition says whether that works:

| `ArmsLog` | Reason | Fix |
|---|---|---|
| `True` | [`ArmsUsable`](reasons.md#armsusable) | nothing |
| `False` | [`ArmsSettingsRejected`](reasons.md#armssettingsrejected) | a server setting is wrong; the manager log line names the `setting`, the `problem` and the `fix` |
| `False` | [`ArmsNoLogYet`](reasons.md#armsnologyet) | no line has arrived since the manager started; check the log destination and the selector |
| `False` | [`ArmsLogStale`](reasons.md#armslogstale) | the log stopped flowing for five minutes |
| absent | [`ArmsDisabled`](reasons.md#armsdisabled) is in the log | `attribution.arms` is set and cannot work (no pod-log RBAC, for instance) |

When a deploy's pods cannot be told apart you see [`ArmsUndistinguished`](reasons.md#armsundistinguished) and the evidence is B at best. A pause is still possible. See [Telling a canary from the stable pods](../attribution-engine.md#telling-a-canary-from-the-stable-pods-grade-a).

## The circuit breaker

After an abort the engine checks, 15 minutes later, whether the regression went away. If it did not, twice in a row, the breaker opens and the engine only alerts. It is the radar noticing that it is acting on something it cannot fix.

1. Find out why the aborts did not help: [`AbortPersisted`](reasons.md#abortpersisted). The deploy may not have been the cause.
2. Close the breaker:

    ```bash
    kubectl annotate postgreswatch <name> -n <ns> pg-regression-radar.io/reset-breaker="$(date +%s)" --overwrite
    ```

    A new value closes it. A value that was on the object when the manager started does nothing, and the same value twice resets once. Restarting the manager also closes it, and also empties the ledger.
3. Watch for `attribution: circuit breaker closed by the reset-breaker annotation` ([`BreakerReset`](reasons.md#breakerreset)) and for `Actuating` turning `True`.

`pg_regression_radar_attribution_breaker_open` is `1` while it is open: it is a good metric to [alert on](metrics.md#useful-alerts), because it needs a person.

## See also

- [Attribution engine](../attribution-engine.md) and [Auto-abort](../auto-abort.md)
- [Example: pausing a bad rollout](../examples/attribution-rollout.md)
- [Reason codes](reasons.md#the-attribution-engine)
