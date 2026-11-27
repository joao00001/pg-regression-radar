# Reason codes

*The stable names the radar gives to what it decided or what went wrong, with the causes of each, how to confirm which one you have, and the fix.*

## Overview

A log message is a sentence and may be reworded. A **reason code** is an identifier that stays the same. It appears as the `reason` field of a log line and as the `reason` of a status condition on a `PostgresWatch` or a `DeploySource`, so you can search for it, alert on it, and find it on this page. The message of an unhealthy condition ends with the link to its entry here.

Find the codes your system is emitting:

```bash
# in the manager's log (JSON): every line that carries a reason, grouped
kubectl logs -n <ns> -l app.kubernetes.io/component=manager --tail=2000 \
  | jq -r 'select(.reason) | "\(.level) \(.reason)"' | sort | uniq -c | sort -rn

# the conditions of a watch
kubectl get postgreswatch <watch> -n <ns> -o json | jq '.status.conditions[] | {type, status, reason, message}'
```

Each entry says where you see the code, and then either what it means (a normal event) or its possible causes, each with how to confirm it and how to fix it. The index says which codes are also the reason of a status condition.

## Index

| Reason | Level | Condition | In short |
|---|---|---|---|
| [`DeployRegistered`](#deployregistered) | info |  | A deploy was registered and will be judged between 10 and 45 minutes after it. |
| [`NoSamples`](#nosamples) | warn |  | A deploy could not be judged because there are no query samples to read. |
| [`ShadowMode`](#shadowmode) | info | `Actuating` | The watch is in shadow mode: decisions are logged and nothing is paused or aborted. |
| [`BelowActingThreshold`](#belowactingthreshold) | info |  | The watch is in enforce mode, but this decision is not strong enough to act on. |
| [`VerdictServed`](#verdictserved) | info |  | An enforced decision was served to the rollout controller. |
| [`NamespaceNotAllowed`](#namespacenotallowed) | info |  | The radar decided to act but the Rollout's namespace is not in attribution.actuate.namespaces. |
| [`ActuationPaused`](#actuationpaused) | warn | `Actuating` | The radar decided to act but the actuation-paused annotation is set on the PostgresWatch. |
| [`NotArgoRollout`](#notargorollout) | info |  | The radar decided to act but the deploy did not come from an argo-rollouts DeploySource. |
| [`ActuationFailed`](#actuationfailed) | error |  | The call to pause or abort the Rollout was refused or failed. |
| [`Acted`](#acted) | info |  | The radar paused or aborted the Rollout. |
| [`AbortRecovered`](#abortrecovered) | info |  | After an abort, the regression went away. |
| [`AbortPersisted`](#abortpersisted) | info |  | After an abort, the regression was still there: the action did not help. |
| [`AbortUnverifiable`](#abortunverifiable) | info |  | An abort could not be checked: too little data came after it. |
| [`BreakerOpen`](#breakeropen) | warn | `Actuating` | Earlier aborts did not help, so the engine only alerts until the breaker is closed. |
| [`BreakerReset`](#breakerreset) | warn |  | An operator closed the circuit breaker with the reset-breaker annotation, so the engine may act again. |
| [`ActuationReady`](#actuationready) | info | `Actuating` | The watch is in enforce mode and may pause and abort Rollouts. |
| [`ActuationNotAsked`](#actuationnotasked) | info | `Actuating` | The watch is in enforce mode but attribution.actuate is not enabled, so this manager does not pause or abort. |
| [`NoActuator`](#noactuator) | info | `Actuating` | Actuation was asked for but this manager has no way to pause or abort a Rollout. |
| [`ArmsDisabled`](#armsdisabled) | error |  | attribution.arms is configured but cannot work, so grade A evidence is off. |
| [`ArmsSettingsUnread`](#armssettingsunread) | warn |  | The radar could not read the Postgres server's logging settings. |
| [`ArmsSettingsRejected`](#armssettingsrejected) | warn | `ArmsLog` | The Postgres logging settings do not allow arm-labelled evidence. |
| [`ArmsUsable`](#armsusable) | info | `ArmsLog` | The Postgres log can serve as an arm-labelled source. |
| [`ArmsNoLogYet`](#armsnologyet) | info | `ArmsLog` | No Postgres log line has arrived yet. |
| [`ArmsLogStale`](#armslogstale) | warn | `ArmsLog` | The Postgres log has stopped flowing. |
| [`ArmsUndistinguished`](#armsundistinguished) | warn |  | The pods of a deploy's canary and stable arms could not be told apart. |
| [`WorkerStarted`](#workerstarted) | info | PostgresWatch `Ready` | The collector and the correlation engine are running. |
| [`WorkerStartFailed`](#workerstartfailed) | error | PostgresWatch `Ready` | The watch's worker could not be started. |
| [`DSNUnresolved`](#dsnunresolved) | error |  | The watch's database connection string could not be resolved. |
| [`SecretsUnresolved`](#secretsunresolved) | error |  | A Secret the watch refers to could not be read. |
| [`CollectorExited`](#collectorexited) | error |  | The collector stopped on its own. |
| [`ScrapeFresh`](#scrapefresh) | info | `Collecting` | The collector scraped pg_stat_statements recently. |
| [`NoScrapeYet`](#noscrapeyet) | info | `Collecting` | The collector has not completed a scrape yet. |
| [`ScrapeStale`](#scrapestale) | warn | `Collecting` | The collector has not scraped for longer than it should. |
| [`AlertNotifyFailed`](#alertnotifyfailed) | error |  | An alert could not be delivered. |
| [`RegressionPersistFailed`](#regressionpersistfailed) | error |  | A detected regression could not be saved as a PerformanceRegression. |
| [`RevertDisabled`](#revertdisabled) | error |  | attribution.revert is configured wrongly, so draft revert pull requests are off. |
| [`AutoAbortIgnored`](#autoabortignored) | info |  | spec.autoAbort stands down because attribution.actuate decides. |
| [`AutoAbortSkipped`](#autoabortskipped) | info |  | The p-value auto-abort found no DeploySource for the deploy and did nothing. |
| [`AutoAbortFailed`](#autoabortfailed) | error |  | The p-value auto-abort tried to abort a Rollout and failed. |
| [`AutoAbortTriggered`](#autoaborttriggered) | info |  | The p-value auto-abort aborted a Rollout. |
| [`MultipleTestingUnknown`](#multipletestingunknown) | warn |  | spec.multipleTesting has a value the radar does not know and was ignored. |
| [`RouteRegistered`](#routeregistered) | info | DeploySource `Ready` | The webhook route of the DeploySource is registered. |
| [`NativeWatchActive`](#nativewatchactive) | info | DeploySource `Ready` | The DeploySource is watched through the Kubernetes API, with no webhook route. |
| [`InvalidSpec`](#invalidspec) | warn | DeploySource `Ready` | The DeploySource's spec is invalid, so no route is registered. |
| [`PostgresWatchNotReady`](#postgreswatchnotready) | info | DeploySource `Ready` | The PostgresWatch the DeploySource points to does not exist or is not running yet. |
| [`DeployEventSynthesised`](#deployeventsynthesised) | info |  | A deploy event was made from a native Rollout or Deployment change. |
| [`HistoryNotRecording`](#historynotrecording) | warn |  | The dashboard's history could not be written; it will have a gap. |
| [`ClusterHealthCheckFailed`](#clusterhealthcheckfailed) | warn |  | The health check of a PostgresRadarCluster failed. |

## The attribution engine

Decisions about a deploy, acting on a Rollout, and the circuit breaker.

### `DeployRegistered`

A deploy was registered and will be judged between 10 and 45 minutes after it.

**Where you see it.** Log, INFO `attribution: deploy registered, it will be judged from 10 to 45 minutes after it`, with `deploy`, `namespace`, `app`, `revision`, `source`, `firstEvaluation` and `lastEvaluation`.

**What it means.** The attribution engine now tracks this deploy. It judges it eight times: 10 minutes after it, then every 5 minutes from 15 to 45. Each judgment is a `decision` line with the same `deploy`. If you expected this line and it is not there, the deploy never reached the watch: see [A deploy never shows up](deploy-sources.md#a-deploy-never-shows-up).

### `NoSamples`

A deploy could not be judged because there are no query samples to read.

**Where you see it.** Log, WARN `attribution: no query samples to judge the deploy by, evaluation skipped`, once per skipped evaluation, with `deploy`, `namespace` and `app`. No `decision` line follows it, and the verdict endpoint has nothing for the deploy.

**Causes.**

1. **The collector has read nothing.** The watch scraped no query at all in the window the engine reads (from 90 minutes before the deploy to now).

    *Confirm:* `kubectl get postgreswatch -n <ns>`: `Tracked Queries` is `0`, or the `Collecting` condition is `False` (`NoScrapeYet`, `ScrapeStale`).

    *Fix:* Fix the collector first: see [`NoScrapeYet`](#noscrapeyet) and [`ScrapeStale`](#scrapestale).

2. **The manager restarted after the deploy.** The samples live in memory. A manager that restarted between the deploy and its evaluation starts with an empty history, so a deploy that is already 10 minutes old has no samples from before it.

    *Confirm:* The manager's pod `RESTARTS` or `AGE` is newer than the deploy (`kubectl get pods -n <ns> -l app.kubernetes.io/component=manager`); the `deploy` in the line is older than that.

    *Fix:* Nothing to repair for this deploy: it cannot be judged. For the next ones, keep the manager from restarting at deploy time (leader election, a `PodDisruptionBudget`, no rolling the manager with the app).

3. **The sample source is Prometheus and its queries fail.** With `spec.sampleSource.type: prometheus` the samples come from your Prometheus. A failing query leaves the engine with none.

    *Confirm:* The log has `promsource: query failed; the engine will see no samples until this is fixed` with the `operation` and `err`.

    *Fix:* Fix the Prometheus URL, credentials or the metric names the source expects; see [OTel / Prometheus source](../otel-prometheus-source.md).

### `ShadowMode`

The watch is in shadow mode: decisions are logged and nothing is paused or aborted.

**Where you see it.** Log, INFO `attribution (shadow): decision, not executed` on every evaluation, and `postgreswatch: attribution.actuate ignored, it only acts in enforce mode` when the watch also asks for actuation. Condition `Actuating` is `False` with this reason.

**What it means.** This is the default, and it is safe: the engine judges every deploy and logs what it would have done, and nothing is paused or aborted. To let it act, set `spec.attribution.mode: enforce` and `spec.attribution.actuate.enabled: true`, only after running in shadow mode long enough to trust its decisions; see [Attribution and enforce](enforce.md#before-you-turn-enforce-on).

### `BelowActingThreshold`

The watch is in enforce mode, but this decision is not strong enough to act on.

**Where you see it.** Log, INFO `attribution (enforce): decision below the acting threshold, nothing to execute`. It is the normal line for most deploys in enforce mode.

**What it means.** The watch is in enforce mode and the engine judged the deploy, but the decision is `none` or `alert`, which never acts. The `reasons` field of the same line says why it did not go further, for instance `evidence is capped at grade C by: few-donors`. [Why the engine only alerts](enforce.md#the-engine-only-alerts-and-does-not-act) explains each one.

### `VerdictServed`

An enforced decision was served to the rollout controller.

**Where you see it.** Log, INFO `attribution (enforce): verdict served to the rollout controller`, with `action`, `verdict`, `grade` and `impact`.

**What it means.** The engine reached a decision strong enough to act on (`hold` or `rollback`) and it is enforced. The verdict is now what `GET /api/v1/attribution/verdict` returns for this deploy, for a rollout controller's analysis to poll. If `attribution.actuate` is on, the manager acts too: the next line is [`Acted`](#acted), or one of the reasons it did not.

### `NamespaceNotAllowed`

The radar decided to act but the Rollout's namespace is not in attribution.actuate.namespaces.

**Where you see it.** Log, INFO `attribution: namespace not allowed for actuation, nothing paused or aborted`, with `namespace` and `action`. The action ledger records the attempt as `skipped`.

**Causes.**

1. **`attribution.actuate.namespaces` does not list the Rollout's namespace.** The list is an allowlist. A deploy elsewhere is still judged, logged and served as a verdict; only the pause or abort is never done.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.spec.attribution.actuate.namespaces}'` and compare with the `namespace` field of the log line.

    *Fix:* Add the namespace to the list, or leave it out on purpose: this line is the intended behavior for a namespace you did not opt in.

### `ActuationPaused`

The radar decided to act but the actuation-paused annotation is set on the PostgresWatch.

**Where you see it.** Log, WARN `attribution: actuation paused by annotation, nothing paused or aborted`. Condition `Actuating` is `False` with this reason. The action ledger records `skipped`.

**Causes.**

1. **The kill switch is on.** The annotation `pg-regression-radar.io/actuation-paused` is `"true"` on the PostgresWatch. Somebody set it, on purpose.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.metadata.annotations}'`.

    *Fix:* Remove it to resume: `kubectl annotate postgreswatch <watch> -n <ns> pg-regression-radar.io/actuation-paused-`. It takes effect at the next evaluation; a deploy that was skipped is acted on if it is still being judged.

2. **The manager could not read the PostgresWatch.** When it cannot read the object at the moment of acting, the manager assumes it is paused, because acting on a guess is the wrong way to fail.

    *Confirm:* The annotation is not set, but the line appears. `kubectl auth can-i get postgreswatches.radar.pgregressionradar.io --as system:serviceaccount:<ns>:<manager-service-account> -n <ns>`.

    *Fix:* Restore the manager's `get` on `postgreswatches` (the chart's ClusterRole has it); look for API server errors in the manager log around the same time.

### `NotArgoRollout`

The radar decided to act but the deploy did not come from an argo-rollouts DeploySource.

**Where you see it.** Log, INFO `attribution: not an Argo Rollouts deploy, nothing to pause or abort`, with `source` and `action`. The ledger records `skipped`.

**Causes.**

1. **The deploy was reported by another kind of DeploySource.** The manager only pauses or aborts an Argo Rollouts `Rollout`, and only when the deploy came from a DeploySource of `sourceType: argo-rollouts`. Deploys from `kubernetes`, `argocd`, `flux` or `generic` sources are judged and served, never acted on.

    *Confirm:* `kubectl get deploysource -n <ns> -o custom-columns=NAME:.metadata.name,TYPE:.spec.sourceType,APP:.spec.appName`: the one named in the line's `source` is not `argo-rollouts`.

    *Fix:* If the app runs as a Rollout, add a DeploySource of type `argo-rollouts` for it. If it is a plain Deployment, this is expected: the radar alerts and serves the verdict, and a person acts.

### `ActuationFailed`

The call to pause or abort the Rollout was refused or failed.

**Where you see it.** Log, ERROR `attribution: could not act on the rollout, will retry at the next evaluation`, with `rollout` (`<namespace>/<app>`), `action` (`pause` or `abort`) and `err`. The ledger records `failed`, and `pg_regression_radar_attribution_actions_total{result="failed"}` counts it.

**Causes.**

1. **The manager may not patch Rollouts.** `err` says `forbidden`. The optional Argo Rollouts RBAC is off, or was removed.

    *Confirm:* `kubectl auth can-i patch rollouts.argoproj.io --as system:serviceaccount:<ns>:<manager-service-account> -n <rollout-ns>` prints `no`.

    *Fix:* Install with `manager.autoAbort.rbacEnabled: true`. Without it the condition is [`NoActuator`](#noactuator) and this line never appears.

2. **There is no Rollout with that name.** The manager looks for a `Rollout` named after the deploy's `app`, in its namespace. `err` says `not found`.

    *Confirm:* `kubectl get rollout <app> -n <namespace>`. The `app` in the line is what the DeploySource reports (`spec.appName`), which must equal the Rollout's name.

    *Fix:* Make the DeploySource's `appName` equal to the Rollout's name.

3. **The API server or an admission webhook refused the patch.** `err` carries the server's message.

    *Confirm:* Read `err`; `kubectl describe rollout <app> -n <namespace>` for admission or finalizer problems.

    *Fix:* Fix what `err` names. The manager retries at every evaluation until 45 minutes after the deploy, so a transient refusal heals itself.

### `Acted`

The radar paused or aborted the Rollout.

**Where you see it.** Log, INFO `attribution: acted on the rollout`, with `rollout`, `action`, `grade`, `impact` and `reasons`. The ledger records `done`; `pg_regression_radar_attribution_actions_total{result="done"}` counts it. For an abort, the check after it is logged 15 minutes later: [`AbortRecovered`](#abortrecovered) or [`AbortPersisted`](#abortpersisted).

**What it means.** The radar set `spec.paused: true` on the Rollout (`pause`, grade B evidence) or `status.abort: true` (`abort`, grade A). It does nothing else: it does not resume, roll back to a revision, or delete anything. A person decides next: `kubectl argo rollouts get rollout <app> -n <ns>` shows the state; `kubectl argo rollouts promote <app> -n <ns>` resumes a pause; `kubectl argo rollouts retry rollout <app> -n <ns>` tries an aborted rollout again.

### `AbortRecovered`

After an abort, the regression went away.

**Where you see it.** Log, INFO `attribution: checked the abort`, with `result=recovered` and a `detail`. `pg_regression_radar_attribution_verifications_total{result="recovered"}` counts it.

**What it means.** Fifteen minutes after an abort the engine read the deploy again, leaving out the time until the abort and three more minutes for the canary's pods to drain, and the regression was gone. The abort helped. A recovered check also clears the count of consecutive failures that leads to the breaker.

### `AbortPersisted`

After an abort, the regression was still there: the action did not help.

**Where you see it.** Log, INFO `attribution: checked the abort`, with `result=persisted` and a `detail` such as `regression still supported after the action (grade A, 31.2%)`. `pg_regression_radar_attribution_verifications_total{result="persisted"}` counts it.

**Causes.**

1. **The deploy was not the cause.** Something else regressed the queries at about the same time: a load change, a dropped index, a plan change after statistics refreshed, another deploy. Aborting the canary changed nothing because the canary was not the problem.

    *Confirm:* The regression is also on queries the canary does not touch; the dashboard's deploy card shows it as a shift of the whole workload. Check `pg_stat_user_indexes` / recent migrations and other deploys in the same window.

    *Fix:* Find and fix the real cause. Two persisted checks in a row open the breaker so the radar stops acting on a problem it cannot fix: see [`BreakerOpen`](#breakeropen).

2. **The abort did not take.** Pods of the canary are still serving: the Rollout did not scale the canary down in time (`abortScaleDownDelaySeconds`), another Deployment or ReplicaSet runs the same image and reports the same `application_name` prefix, or the Rollout was promoted again.

    *Confirm:* `kubectl argo rollouts get rollout <app> -n <ns>`: the canary ReplicaSet still has pods. `kubectl get pods -n <ns> -l app=<app>` shows pods with the canary's `pod-template-hash`.

    *Fix:* Make the canary actually stop: check `abortScaleDownDelaySeconds` and that nothing else owns pods with the same name prefix.

### `AbortUnverifiable`

An abort could not be checked: too little data came after it.

**Where you see it.** Log, INFO `attribution: could not check the abort, too little data after it`.

**Causes.**

1. **Too few samples came in after the abort.** The check needs enough scrapes after the abort (and the three minutes of draining). If the collector stalled, the manager restarted, or the database was unreachable after the abort, there is nothing to judge by.

    *Confirm:* The watch's `Collecting` condition is or was `False` after the abort; look for `collector: scrape failed` in the log after the `Acted` line.

    *Fix:* Nothing to repair for this abort. It is not a failure and does not count towards the breaker; fix the collector so the next one can be checked.

### `BreakerOpen`

Earlier aborts did not help, so the engine only alerts until the breaker is closed.

**Where you see it.** Log, WARN `attribution: circuit breaker open, the engine will only alert until it is reset`, with `result`, `detail` and `howToReset`. Condition `Actuating` is `False` with this reason and the command to close it. Metric `pg_regression_radar_attribution_breaker_open` is `1`. The dashboard's Source health panel shows it. `GET /api/v1/attribution/actions` and the verdict API report `breakerOpen: true`.

**Causes.**

1. **Two aborts in a row did not help.** After each abort the engine checks it. Two `persisted` results in a row (`BreakerLimit`) mean the radar is acting on something it cannot fix, so it stops acting and only alerts. Judging and logging continue.

    *Confirm:* Two [`AbortPersisted`](#abortpersisted) lines for the same watch, in a row. `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.status.conditions[?(@.type=="Actuating")]}'`.

    *Fix:* Find out why the aborts did not help (see [`AbortPersisted`](#abortpersisted)). Then close the breaker: `kubectl annotate postgreswatch <watch> -n <ns> pg-regression-radar.io/reset-breaker="$(date +%s)" --overwrite`. It reopens if the next aborts do not help either. Restarting the manager also closes it, and also empties the action ledger.

### `BreakerReset`

An operator closed the circuit breaker with the reset-breaker annotation, so the engine may act again.

**Where you see it.** Log, WARN `attribution: circuit breaker closed by the reset-breaker annotation, the engine may act again`, with `watch` and `token`. If the breaker was already closed, an INFO line says so and nothing changes.

**Causes.**

1. **An operator set the reset-breaker annotation.** The annotation `pg-regression-radar.io/reset-breaker` got a value it did not have. Only a change counts: a value that was already on the object when the manager started does nothing, and the same value twice resets once.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.metadata.annotations}'`.

    *Fix:* Nothing to fix: this is the intended way to close the breaker. If it should not have happened, set `pg-regression-radar.io/actuation-paused="true"` while you investigate.

### `ActuationReady`

The watch is in enforce mode and may pause and abort Rollouts.

**Where you see it.** Condition `Actuating` is `True` with this reason: `kubectl describe postgreswatch <watch> -n <ns>`.

**What it means.** The watch is in enforce mode, `attribution.actuate.enabled` is on, the manager can patch Rollouts, the kill switch is off and the breaker is closed. A decision of `hold` or `rollback` will pause or abort the Rollout (in the namespaces listed, if any). It is not a promise that anything will happen: most deploys are never held.

### `ActuationNotAsked`

The watch is in enforce mode but attribution.actuate is not enabled, so this manager does not pause or abort.

**Where you see it.** Condition `Actuating` is `False` with this reason.

**What it means.** The watch is in `enforce` mode, but `spec.attribution.actuate.enabled` is not on. That is a valid setup: the engine serves verdicts at `GET /api/v1/attribution/verdict` and your rollout controller's analysis decides. This manager never pauses or aborts. Turn on `attribution.actuate.enabled` only if you want the manager to act itself.

### `NoActuator`

Actuation was asked for but this manager has no way to pause or abort a Rollout.

**Where you see it.** Log, INFO `postgreswatch: attribution.actuate ignored, no Argo Rollouts actuator is available (enable manager.autoAbort.rbacEnabled)`. Condition `Actuating` is `False` with this reason.

**Causes.**

1. **The manager has no Argo Rollouts RBAC.** `attribution.actuate` is on and the watch is in enforce mode, but the manager was installed without `manager.autoAbort.rbacEnabled`, so it cannot patch or read Rollouts and the engine runs as if actuation had not been asked for.

    *Confirm:* `kubectl auth can-i patch rollouts.argoproj.io --as system:serviceaccount:<ns>:<manager-service-account> -n <rollout-ns>` prints `no`.

    *Fix:* `helm upgrade` with `--set manager.autoAbort.rbacEnabled=true`. This grants `patch` on `rollouts` and `rollouts/status`, and nothing else.

## The Postgres log (arms)

What lets the engine tell a canary from the stable pods, which grade A evidence and an abort depend on.

### `ArmsDisabled`

attribution.arms is configured but cannot work, so grade A evidence is off.

**Where you see it.** Log, ERROR `postgreswatch: attribution.arms disabled, <why>`. The engine keeps running without the log: it falls back to `pg_stat_statements`, whose best grade is B, so it can pause and never abort.

**Causes.**

1. **The sample source is Prometheus.** Why: `it needs the direct database connection to check the server's settings, which sampleSource prometheus does not have`.

    *Confirm:* `spec.sampleSource.type` is `prometheus`.

    *Fix:* Use the built-in collector for a watch that needs grade A, or leave `attribution.arms` off.

2. **Both or neither of `podLogs` and `file` are set.** Why: `set exactly one of podLogs and file`.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.spec.attribution.arms}'`.

    *Fix:* Set exactly one.

3. **The `file` path is not accepted.** Why: the message is the validator's own, for instance a relative path or a path with `..`.

    *Confirm:* The `<why>` of the line.

    *Fix:* Give an absolute path to the Postgres JSON log the manager can read.

4. **The manager cannot read Rollouts.** Why: `the manager cannot read Rollouts (enable manager.autoAbort.rbacEnabled)`.

    *Confirm:* `kubectl auth can-i get rollouts.argoproj.io --as system:serviceaccount:<ns>:<manager-service-account> -n <rollout-ns>`.

    *Fix:* Install with `manager.autoAbort.rbacEnabled: true`.

5. **The manager has no Kubernetes client for `podLogs`.** Why: `the manager has no Kubernetes client to read pod logs`. It also logs `unable to build Kubernetes client; attribution.arms.podLogs will have no effect` at start.

    *Confirm:* The start-up log line above.

    *Fix:* Fix the manager's in-cluster configuration, and install with `manager.arms.rbacEnabled: true` (get and list on pods, get on `pods/log`).

### `ArmsSettingsUnread`

The radar could not read the Postgres server's logging settings.

**Where you see it.** Log, WARN `attribution.arms: cannot read the server's settings`, with `watch` and `err`. Retried every 30 seconds. Condition `ArmsLog` is `False` (`ArmsSettingsRejected`) until it succeeds.

**Causes.**

1. **The query on `pg_settings` fails.** The connection dropped, or the database role cannot run it. `err` says which.

    *Confirm:* Read `err`. `psql "$DSN" -c "select name, setting from pg_settings where name like 'log_%'"` from a pod in the namespace.

    *Fix:* Restore the connection (see [Setup](setup.md)); `pg_settings` is readable by any role, so a permission error means something else is wrong with the DSN.

### `ArmsSettingsRejected`

The Postgres logging settings do not allow arm-labelled evidence.

**Where you see it.** Log, WARN `attribution.arms: the Postgres log cannot be used yet`, with `setting`, `value`, `problem` and `fix`, once for each setting that is wrong and again when the state changes. Condition `ArmsLog` is `False` with this reason. `pg_regression_radar_attribution_arms_usable` is `0`.

**Causes.**

1. **`server_version_num` is below 150000.** The JSON log needs PostgreSQL 15 or newer.

    *Confirm:* `select version()`.

    *Fix:* Upgrade, or leave `attribution.arms` off.

2. **`compute_query_id` is off, or `auto` without `pg_stat_statements` preloaded.** The log lines carry no `query_id`, so they cannot be joined to the collected queries.

    *Confirm:* `show compute_query_id; show shared_preload_libraries;`.

    *Fix:* `compute_query_id = on`.

3. **`log_destination` has no `jsonlog` (or `csvlog`).** `application_name` and `query_id` cannot be read from plain text.

    *Confirm:* `show log_destination;`.

    *Fix:* `log_destination = 'jsonlog'` (or `'stderr,jsonlog'`).

4. **`log_statement_sample_rate` is 0 or less.** No statement is sampled, so the log has nothing in it.

    *Confirm:* `show log_statement_sample_rate;`.

    *Fix:* Set a small rate, for example `0.01`, and raise it on a quiet server.

5. **`log_min_duration_sample` is negative or above 0.** Negative turns sampling off. Above 0, only statements slower than that are sampled, so the fast ones that make most of the traffic are missing.

    *Confirm:* `show log_min_duration_sample;`.

    *Fix:* `log_min_duration_sample = 0`.

### `ArmsUsable`

The Postgres log can serve as an arm-labelled source.

**Where you see it.** Log, INFO `attribution.arms: the Postgres log can serve as an arm-labelled source`, with `watch` and `sampleRate`. Condition `ArmsLog` is `True`. `pg_regression_radar_attribution_arms_usable` is `1`.

**What it means.** The server's settings pass the check and lines are arriving. The engine can tell the canary's pods from the stable ones, which makes grade A evidence, and therefore an abort, possible. A `the Postgres log setting costs more than it needs to` warning next to it means `log_statement_sample_rate` is above 0.25: it works, and costs the database and the log pipeline more than it should.

### `ArmsNoLogYet`

No Postgres log line has arrived yet.

**Where you see it.** Condition `ArmsLog` is `False` with this reason. `pg_regression_radar_attribution_arms_records_total` is `0`.

**Causes.**

1. **The `podLogs` selector matches no pod, or names the wrong container.** The manager follows the pods matching `selector` in `namespace`, container `postgres` unless `container` says otherwise.

    *Confirm:* `kubectl get pods -n <pg-ns> -l <selector>` is empty, or `kubectl logs -n <pg-ns> -l <selector> -c <container> --tail=5` fails.

    *Fix:* Correct `attribution.arms.podLogs.selector`, `namespace` or `container`. Install with `manager.arms.rbacEnabled: true` so the manager may read `pods/log`.

2. **The `file` is not being written, or not readable by the manager.** With `file` the manager tails a path on its own filesystem.

    *Confirm:* `kubectl exec` into the manager pod and `tail` the file.

    *Fix:* Mount the log into the manager pod, or use `podLogs`.

3. **The database has no traffic, or sampling is too sparse.** Settings pass but nothing is logged: no statement was sampled yet.

    *Confirm:* Is the app sending queries? `show log_statement_sample_rate;`.

    *Fix:* Wait, or raise the sample rate.

### `ArmsLogStale`

The Postgres log has stopped flowing.

**Where you see it.** Condition `ArmsLog` is `False` with this reason and the age of the newest line. `pg_regression_radar_attribution_arms_usable` is `0` again after it was `1`.

**Causes.**

1. **The log stopped flowing.** No line for five minutes. The Postgres pod was replaced and the follower has not found the new one yet (it looks again every 15 seconds), the log rotated, or the app stopped sending traffic.

    *Confirm:* `kubectl logs -n <pg-ns> -l <selector> -c <container> --tail=5`; look for `pglog:` lines in the manager log.

    *Fix:* Restore the log, or wait for the follower. Until it flows again the engine falls back to `pg_stat_statements`, so it can pause and cannot abort.

### `ArmsUndistinguished`

The pods of a deploy's canary and stable arms could not be told apart.

**Where you see it.** Log, WARN `attribution: cannot tell the arms of a deploy apart`, with `deploy` and `err`. That evaluation uses `pg_stat_statements` alone (grade B at best).

**Causes.**

1. **The manager cannot read the Rollout's ReplicaSets.** To tell canary pods from stable ones it reads the Rollout's two pod-template hashes. `err` says why it could not: `forbidden`, or `not found`.

    *Confirm:* `kubectl auth can-i get rollouts.argoproj.io --as system:serviceaccount:<ns>:<manager-service-account> -n <rollout-ns>`; `kubectl get rollout <app> -n <rollout-ns>`.

    *Fix:* Grant the Argo Rollouts RBAC (`manager.autoAbort.rbacEnabled: true`) and make `appName` equal the Rollout's name.

## PostgresWatch

Starting a watch, the collector, alerts and saved regressions.

### `WorkerStarted`

The collector and the correlation engine are running.

**Where you see it.** Log, INFO `postgreswatch worker started`, with `cluster`. Condition `Ready` is `True` on the PostgresWatch.

**What it means.** The collector and the correlation engine are running for this watch. It happens at the first reconcile and again whenever the spec changes (the worker is replaced, which also empties the in-memory sample history and the action ledger of that watch).

### `WorkerStartFailed`

The watch's worker could not be started.

**Where you see it.** Log, ERROR `unable to start watch worker`, with the error. `kubectl get postgreswatch` shows `Phase: Failed`; condition `Ready` is `False` with this reason and the message. The reconciler retries with backoff.

**Causes.**

1. **The spec combines options that cannot work together.** For example `capturePlans: true` with `sampleSource.type: prometheus`: plan capture needs the direct database connection that source does not have.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.status.message}'`.

    *Fix:* Change the spec as the message says.

2. **The DSN is malformed.** The message starts `create collector: collector: open db`. A malformed DSN is rejected at once; a DSN that is well formed but points nowhere is not, and shows up as a failing scrape instead (see [`NoScrapeYet`](#noscrapeyet)).

    *Confirm:* The `message` on the watch.

    *Fix:* Correct the connection string in the Secret.

3. **The Prometheus sample source cannot be built.** The message starts `create prometheus sample source`.

    *Confirm:* The `message` on the watch.

    *Fix:* Correct `spec.sampleSource.prometheus`; see [OTel / Prometheus source](../otel-prometheus-source.md).

4. **The alerting configuration is invalid.** The message starts `build alert notifier`: the URL is malformed or the destination policy rejects it.

    *Confirm:* The `message` on the watch.

    *Fix:* Correct `spec.alerting`; see [Alerting](../alerting.md#destination-policies).

### `DSNUnresolved`

The watch's database connection string could not be resolved.

**Where you see it.** Log, ERROR `unable to resolve DSN`, with the error. `Phase: Failed`; condition `Ready` is `False` (`WorkerStartFailed`) with the same message.

**Causes.**

1. **The Secret does not carry the consent label.** The manager only reads a Secret labeled `pg-regression-radar.io/allow-postgreswatch-access=true`.

    *Confirm:* The error mentions the label. `kubectl get secret <name> -n <ns> --show-labels`.

    *Fix:* `kubectl label secret <name> -n <ns> pg-regression-radar.io/allow-postgreswatch-access=true`.

2. **`dsnSecretRef` points at the wrong Secret or key.** A name or key that does not exist, or a Secret in another namespace (it must be in the watch's own).

    *Confirm:* `kubectl get secret <name> -n <ns> -o jsonpath='{.data}'` and compare with `spec.dsnSecretRef.name` and `.key`.

    *Fix:* Correct the reference.

3. **The manager may not read Secrets in that namespace.** The error says `forbidden`.

    *Confirm:* `kubectl auth can-i get secrets --as system:serviceaccount:<ns>:<manager-service-account> -n <ns>`.

    *Fix:* Grant the manager read access to that Secret.

### `SecretsUnresolved`

A Secret the watch refers to could not be read.

**Where you see it.** Log, ERROR `unable to resolve referenced secrets`, with the error. `Phase: Failed`; condition `Ready` is `False` (`WorkerStartFailed`).

**Causes.**

1. **A Secret the watch refers to is missing or lacks the consent label.** Besides the DSN, a watch can refer to Secrets for a Prometheus bearer token and for OTLP export headers. Each needs the consent label.

    *Confirm:* The error names the Secret and the key. `kubectl get secret <name> -n <ns> --show-labels`.

    *Fix:* Create it, fix the key, or label it `pg-regression-radar.io/allow-postgreswatch-access=true`.

### `CollectorExited`

The collector stopped on its own.

**Where you see it.** Log, ERROR `postgreswatch: collector exited unexpectedly`, with `watch` and `err`. After it the watch stops scraping; `Collecting` turns `False` (`ScrapeStale`) after three intervals.

**Causes.**

1. **The collector hit an error it does not retry.** `err` says what: a lost connection that did not recover, or a failed first query.

    *Confirm:* Read `err`; `kubectl get postgreswatch -n <ns>` shows `Tracked Queries` stuck.

    *Fix:* Fix what `err` names. The status refresh does not restart the worker; changing the spec or restarting the manager does.

### `ScrapeFresh`

The collector scraped pg_stat_statements recently.

**Where you see it.** Condition `Collecting` is `True` with this reason.

**What it means.** The collector scraped `pg_stat_statements` within the last three scrape intervals. One missed scrape is normal: a slow query on the database can delay it.

### `NoScrapeYet`

The collector has not completed a scrape yet.

**Where you see it.** Condition `Collecting` is `False` with this reason. `status.lastScrapeTime` is empty and `Tracked Queries` is `0`.

**Causes.**

1. **The worker just started.** The first scrape comes one scrape interval after the start (60 seconds by default).

    *Confirm:* `kubectl get postgreswatch -n <ns>` a minute later.

    *Fix:* Wait. The condition clears by itself.

2. **The collector cannot scrape.** `pg_stat_statements` is missing or empty, the role cannot read it, or the connection fails. The log has `collector: scrape failed` with the `err`.

    *Confirm:* `kubectl logs -n <ns> -l app.kubernetes.io/component=manager --tail=500 | grep 'scrape failed'`.

    *Fix:* See [`pg_stat_statements` is not available](setup.md#pg_stat_statements-is-not-available) and [Permission denied](setup.md#permission-denied-42501).

### `ScrapeStale`

The collector has not scraped for longer than it should.

**Where you see it.** Condition `Collecting` is `False` with this reason and the age of the last scrape.

**Causes.**

1. **The database is slow or unreachable.** Scrapes fail, or take longer than three intervals. `collector: scrape failed` lines say why.

    *Confirm:* `kubectl logs -n <ns> -l app.kubernetes.io/component=manager --tail=500 | grep 'scrape failed'`.

    *Fix:* Fix the connection or the database; see [Setup](setup.md).

2. **The collector exited.** See [`CollectorExited`](#collectorexited).

    *Confirm:* An ERROR `postgreswatch: collector exited unexpectedly` earlier in the log.

    *Fix:* As there.

### `AlertNotifyFailed`

An alert could not be delivered.

**Where you see it.** Log, ERROR `postgreswatch: alert notify failed` (and `periodic alert notify failed`), with `watch`, `regression` and `err`. `pg_regression_radar_notifications_total{status="error"}` counts it.

**Causes.**

1. **The destination refused or is unreachable.** A wrong URL, a webhook that was revoked, DNS, or an egress `NetworkPolicy`.

    *Confirm:* `err` has the HTTP status or the network error.

    *Fix:* Fix the URL or allow the egress; see [Alerting](../alerting.md).

2. **The destination policy rejected it.** The policy is `allowlist` (`spec.alerting.destinationPolicy` or `--alerting-destination-policy`) and the host is not in `--alerting-allowed-destinations`, or the address is on the SSRF blocklist.

    *Confirm:* `err` mentions the policy.

    *Fix:* Add the host to the allowlist, or change the policy; see [Alerting](../alerting.md#destination-policies).

### `RegressionPersistFailed`

A detected regression could not be saved as a PerformanceRegression.

**Where you see it.** Log, ERROR `postgreswatch: failed to persist PerformanceRegression CR` (and the `periodic` variant), with `watch`, `regression` and `err`. The alert was still sent.

**Causes.**

1. **The manager may not write PerformanceRegression objects.** `err` says `forbidden`.

    *Confirm:* `kubectl auth can-i create performanceregressions.radar.pgregressionradar.io --as system:serviceaccount:<ns>:<manager-service-account> -n <ns>`.

    *Fix:* Restore the chart's RBAC.

2. **The API server rejected the object.** A quota (`exceeded quota`), an admission webhook, or an invalid name.

    *Confirm:* `err`; `kubectl describe resourcequota -n <ns>`.

    *Fix:* Fix what `err` names.

### `RevertDisabled`

attribution.revert is configured wrongly, so draft revert pull requests are off.

**Where you see it.** Log, ERROR `postgreswatch: attribution revert disabled by invalid configuration`, with `watch` and `err`. The engine and its verdict keep working without draft revert pull requests.

**Causes.**

1. **`attribution.revert.repository` is not `owner/name`.** `err` is `revert: repository "..." must be owner/name`.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.spec.attribution.revert}'`.

    *Fix:* Write it as `owner/name`.

2. **`attribution.revert.apiURL` is invalid or not https.** The token is sent to that URL, so anything but https is refused: `revert: invalid api url` or `revert: api url must be https, the token is sent to it`.

    *Confirm:* As above.

    *Fix:* Use an https URL, or leave `apiURL` empty for github.com.

### `AutoAbortIgnored`

spec.autoAbort stands down because attribution.actuate decides.

**Where you see it.** Log, INFO `postgreswatch: spec.autoAbort ignored, attribution.actuate decides instead`.

**What it means.** The watch has both the older `spec.autoAbort` (p-value based) and `attribution.actuate`. One regression must not be acted on by two detectors, so the older one stands down and the engine decides.

### `AutoAbortSkipped`

The p-value auto-abort found no DeploySource for the deploy and did nothing.

**Where you see it.** Log, INFO `postgreswatch: auto-abort skipped, deploysource not found`, with `source` and `err`.

**What it means.** `spec.autoAbort` found a regression but the deploy's `source` is not a DeploySource the manager can read, so it did nothing. This is normal for deploys from sources that have no abort action. If you expected an abort, check that the DeploySource named in `source` exists in the watch's namespace.

### `AutoAbortFailed`

The p-value auto-abort tried to abort a Rollout and failed.

**Where you see it.** Log, ERROR `postgreswatch: auto-abort failed`, with `rollout`, `confidence` and `err`. The regression's `status.autoAbortError` carries the same text.

**Causes.**

1. **The manager may not patch the Rollout, or there is no such Rollout.** The same two causes as [`ActuationFailed`](#actuationfailed): the RBAC, and an `appName` that differs from the Rollout's name.

    *Confirm:* `kubectl auth can-i patch rollouts.argoproj.io --as system:serviceaccount:<ns>:<manager-service-account> -n <rollout-ns>`; `kubectl get rollout <app> -n <ns>`.

    *Fix:* As there.

### `AutoAbortTriggered`

The p-value auto-abort aborted a Rollout.

**Where you see it.** Log, INFO `postgreswatch: auto-abort triggered`, with `rollout`, `confidence` and `query`. The regression's `status.autoAbortTriggered` is `true`.

**What it means.** The older p-value auto-abort set `status.abort: true` on the Rollout. It is not the attribution engine: it has no grade and no check afterwards. See [Auto-abort](../auto-abort.md).

### `MultipleTestingUnknown`

spec.multipleTesting has a value the radar does not know and was ignored.

**Where you see it.** Log, WARN `ignoring unknown spec.multipleTesting`, with `watch` and `value`.

**Causes.**

1. **The object was written around the CRD's validation.** The CRD only allows its enum values, so this appears only for an object applied with validation off or an older CRD.

    *Confirm:* `kubectl get postgreswatch <watch> -n <ns> -o jsonpath='{.spec.multipleTesting}'`.

    *Fix:* Set a value the CRD allows. Until then the radar uses no multiple-testing control.

## DeploySource

Where deploy events come from.

### `RouteRegistered`

The webhook route of the DeploySource is registered.

**Where you see it.** Log, INFO `deploysource webhook route registered`, with `path` and `sourceType`. DeploySource `Phase: Ready`, condition `Ready` is `True`, and `status.webhookPath` is the path.

**What it means.** The webhook listener (`:8080` on the leader) now accepts deploy events for this DeploySource at `status.webhookPath`.

### `NativeWatchActive`

The DeploySource is watched through the Kubernetes API, with no webhook route.

**Where you see it.** Log, INFO `deploysource watched natively, no webhook route registered`, with `sourceType` and `appName`. DeploySource `Phase: Ready`, `status.webhookPath` empty.

**What it means.** For `sourceType: kubernetes` and `argo-rollouts` the manager watches the workload through the Kubernetes API, so there is no webhook route and nothing to send. A deploy is recorded when a Rollout or Deployment completes a new revision.

### `InvalidSpec`

The DeploySource's spec is invalid, so no route is registered.

**Where you see it.** Log, INFO `deploysource has an invalid spec, route not registered`, with `err`. DeploySource `Phase: Failed`, `status.message` is `invalid spec: ...`, and condition `Ready` is `False` with this reason.

**Causes.**

1. **`sourceType: otlp` without a usable span mapping.** Serving a half-configured OTLP route would ingest nothing and say nothing, so the route is dropped instead. `err` is `source type otlp requires an otlp span mapping (spanNames and/or matchAttributes)`, or the mapping validator's own message.

    *Confirm:* `kubectl get deploysource <name> -n <ns> -o jsonpath='{.status.message}'`.

    *Fix:* Add `spec.otlp.spanNames` and/or `matchAttributes`; see [OTLP deploy events](../otlp-deploy-events.md). The route is registered at the next reconcile.

### `PostgresWatchNotReady`

The PostgresWatch the DeploySource points to does not exist or is not running yet.

**Where you see it.** DeploySource `Phase: Pending`, `status.message` `postgresWatchRef "<name>" not found or not yet running`, condition `Ready` is `False` with this reason. The native workload watch logs `workloadwatch: postgresWatchRef not ready yet, will retry`.

**Causes.**

1. **`postgresWatchRef` names a watch that is not in the same namespace.** The reference is by name, in the DeploySource's own namespace.

    *Confirm:* `kubectl get postgreswatch -n <ns>`.

    *Fix:* Correct `spec.postgresWatchRef`, or create the watch in that namespace.

2. **The watch exists but is not running.** Its worker failed to start; see [`WorkerStartFailed`](#workerstartfailed).

    *Confirm:* `kubectl get postgreswatch -n <ns>` shows `Failed` or no `Running`.

    *Fix:* Fix the watch first. The DeploySource retries by itself.

3. **The manager has just started.** The DeploySource can be reconciled before the PostgresWatch it points to has started its worker. It retries every 10 seconds, so this clears by itself.

    *Confirm:* The manager's log for `postgreswatch worker started` after the DeploySource's first reconcile.

    *Fix:* Wait.

### `DeployEventSynthesised`

A deploy event was made from a native Rollout or Deployment change.

**Where you see it.** Log, INFO `workloadwatch: deploy event synthesised from native rollout`, with `kind`, `workload`, `revision` and `deploySource`.

**What it means.** A Rollout or Deployment completed a new revision and the manager made a deploy event from it. It is followed by `ingester: deploy event stored` when the watch accepts it, and then by [`DeployRegistered`](#deployregistered).

## Dashboard history

The record the dashboard reads.

### `HistoryNotRecording`

The dashboard's history could not be written; it will have a gap.

**Where you see it.** Log, WARN `observe history: cannot record; the radar keeps acting, history will have a gap`, once when it starts to fail. The dashboard says history is not being recorded.

**Causes.**

1. **The history store is down.** With `--observe-history=postgres` the Postgres behind `--observe-history-dsn-file` is unreachable or refused a write. Writing history never delays a decision, so the radar keeps judging and acting.

    *Confirm:* `err` in the line; `psql` with the DSN from the mounted file.

    *Fix:* Restore the database. The gap stays; recording resumes.

## PostgresRadarCluster

Remote clusters.

### `ClusterHealthCheckFailed`

The health check of a PostgresRadarCluster failed.

**Where you see it.** Log, WARN `postgresradarcluster health check failed`, with `cluster` and `err`. The PostgresRadarCluster's status shows it unhealthy.

**Causes.**

1. **The remote cluster's kubeconfig no longer works.** The credentials expired or were rotated, or the API server is unreachable from the manager. A lightweight version probe runs every `--cluster-health-check-interval` (5 minutes by default) and at once when the kubeconfig Secret changes.

    *Confirm:* `err`; `kubectl get postgresradarcluster <name> -o yaml` for the status.

    *Fix:* Update the kubeconfig Secret; see [Multi-cluster](../multi-cluster.md#cluster-health-checks).

## See also

- [Where to look](where-to-look.md): which signal answers which question.
- [Logs](logs.md): the format, the fields, and how to turn on more detail.
- [Log catalog](log-catalog.md): every message the radar writes.
- [Attribution and enforce](enforce.md): the engine, step by step.
