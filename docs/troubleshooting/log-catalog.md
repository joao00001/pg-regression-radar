# Log catalog

*Every message the radar writes, with its level, the fields it carries, and what it means.*

## Overview

This page lists each log message by the part of the radar that writes it. Find a message you saw with your browser's search, read what it means, and follow the link when it has a [reason code](reasons.md). A message is free text and can be reworded between releases; the `reason` code is the part that stays. How to read the log and how to turn on more detail is on the [Logs](logs.md) page.

A test compares this page with the source on every build: a message that is logged and is not here, or listed here and not logged, or a field that does not match, fails it. A message that ends in `…` is built at run time and the text after it varies.

Fields that controller-runtime adds to every line written while it reconciles an object (`controller`, `controllerKind`, `name`, `namespace`, `reconcileID`) are described on [Logs](logs.md#fields-you-can-rely-on), and are not repeated in the rows.

## Manager: start-up and shutdown

What `manager` writes while it starts. Almost every error here ends the process, and the pod restarts.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| info | `http server listening` | `server` `addr` | One of the manager's HTTP servers is up. `server` names it (`webhook`, `pg-metrics`, `dashboard-api`, `attribution-verdict`, `observe`) and `addr` is where it listens. Every manager pod serves `dashboard-api`; the others start only on the leader, so a follower does not log them. |
| error | `invalid --alerting-allowed-destinations` | `err` | The allowlist could not be parsed. The manager exits. Use comma-separated hostnames, IPs or CIDRs. |
| error | `invalid --cluster-health-check-interval value: must be positive` | `value` | The interval must be positive. The manager exits. |
| error | `invalid --security-profile value` | `value` `allowed` | `--security-profile` is not `controlled` or `hardened`. The manager exits; `allowed` lists the choices. |
| error | `invalid alerting destination policy configuration` | `err` | The policy and the allowlist or relay URL do not fit (for example `allowlist` with no list, or `relay-only` with no URL). The manager exits. |
| error | `invalid logging flag` | `err` | `--log-level` or `--log-format` has a value that is not accepted. The process exits with a non-zero code. Use `debug`, `info`, `warn` or `error`, and `json` or `text`. |
| error | `invalid or unreachable observe history` | `err` | `--observe-history` is `postgres` and the store could not be opened with the DSN in `--observe-history-dsn-file`. The manager exits. Fix the DSN, or use `memory`. |
| warn | `logging: …` |  | A deprecated `--zap-*` flag was passed and was mapped to `--log-level` or `--log-format` (or ignored). Replace it with the new flag; the line says which. |
| error | `problem running manager` | `err` | The manager stopped with an error after it had started. The pod restarts. Read `err`. |
| info | `starting manager` | `leaderElection` | Everything is registered and the manager is starting. `leaderElection` says whether it will wait for the lease. After it, the leader logs `postgreswatch worker started` for each watch. |
| error | `unable to add attribution verdict server` | `err` | The verdict endpoint could not be added (`--attribution-verdict-addr`). The manager exits. |
| error | `unable to add dashboard API server` | `err` | The read-only dashboard API could not be added (`--dashboard-addr`). The manager exits. |
| error | `unable to add observe history recorder` | `err` | The recorder that writes the dashboard's history could not be added. The manager exits. |
| error | `unable to add observe server` | `err` | The Observe API the live dashboard reads (`--observe-addr`) could not be added. The manager exits. |
| error | `unable to add pg-metrics server` | `err` | The Postgres query metrics listener could not be added (`--pg-metrics-bind-address`). The manager exits. |
| error | `unable to add webhook server` | `err` | The deploy-event webhook listener could not be added (`--webhook-bind-address`). The manager exits; check for an address already in use. |
| error | `unable to build Kubernetes client; attribution.arms.podLogs will have no effect` | `err` | The client used to read Postgres pod logs could not be built, so `attribution.arms.podLogs` does nothing (reason [`ArmsDisabled`](reasons.md#armsdisabled)). |
| error | `unable to build direct Kubernetes client for startup failure reporting` | `err` | The manager could not build the client it uses to write a startup failure into the status of its custom resources. A startup failure will still be logged, but not shown in `kubectl get postgreswatch`. |
| error | `unable to build dynamic client; PostgresWatch.spec.autoAbort will have no effect` | `err` | The client used to patch Rollouts could not be built, so `spec.autoAbort` does nothing. Fix the in-cluster configuration. |
| error | `unable to create controller` | `err` `controller` | A controller could not be registered. The manager exits. `controller` says which (`PostgresWatch`, `DeploySource`, `WorkloadWatch`, `PostgresRadarCluster`); the usual cause is a missing CRD, so check `kubectl get crd | grep radar`. |
| error | `unable to report manager startup failure in custom resource status` | `err` | The manager failed to start and then also failed to write the failure into the custom resources' status. Read the line before this one: that is the real failure. |
| error | `unable to resolve Kubernetes API server config` | `err` | The manager found neither an in-cluster configuration nor a kubeconfig. Outside a cluster, set `KUBECONFIG`; inside one, check that the pod has its service account token. |
| error | `unable to set up health check` | `err` | The `/healthz` endpoint could not be registered. The manager exits. |
| error | `unable to set up ready check` | `err` | The `/readyz` endpoint could not be registered. The manager exits. |
| error | `unable to start manager` | `err` | controller-runtime could not start (the API server is unreachable, or the cache could not sync). The manager exits and the pod restarts. Read `err`. |

## PostgresWatch, DeploySource and workloads

The controllers. They run on the leader only.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| warn | `attribution: circuit breaker closed by the reset-breaker annotation, the engine may act again` | `reason` `watch` `token` | An operator closed the circuit breaker: [`BreakerReset`](reasons.md#breakerreset). |
| info | `attribution: reset-breaker annotation changed, but the circuit breaker was already closed` | `watch` `token` | Someone set `pg-regression-radar.io/reset-breaker` while the breaker was closed. Nothing changed. |
| info | `deploysource deleted, webhook route removed` |  | The DeploySource was deleted and its webhook route removed. Events sent to that path now get `404`. |
| info | `deploysource has an invalid spec, route not registered` | `reason` `err` | The DeploySource's spec was rejected and no route is registered: [`InvalidSpec`](reasons.md#invalidspec). |
| info | `deploysource watched natively, no webhook route registered` | `reason` `sourceType` `appName` | A `kubernetes` or `argo-rollouts` source is watched through the API, so it has no webhook route: [`NativeWatchActive`](reasons.md#nativewatchactive). |
| info | `deploysource webhook route registered` | `reason` `path` `sourceType` | The webhook route is live at `path`: [`RouteRegistered`](reasons.md#routeregistered). |
| warn | `ignoring unknown spec.multipleTesting` | `reason` `watch` `value` `err` | The value is not one the radar knows, and was ignored: [`MultipleTestingUnknown`](reasons.md#multipletestingunknown). |
| error | `list postgresradarclusters for secret-triggered health check` | `err` | The manager could not list PostgresRadarClusters after a kubeconfig Secret changed, so the immediate re-check was skipped. The next scheduled check still runs. Usually a transient API error. |
| warn | `postgresradarcluster health check failed` | `reason` `cluster` `err` | A remote cluster's kubeconfig does not work: [`ClusterHealthCheckFailed`](reasons.md#clusterhealthcheckfailed). |
| info | `postgreswatch deleted, worker stopped` |  | The PostgresWatch was deleted and its collector and engine were stopped. Its in-memory samples and action ledger went with it. |
| info | `postgreswatch worker started` | `reason` `cluster` | The watch's collector and engine are running: [`WorkerStarted`](reasons.md#workerstarted). It is logged again whenever the spec changes, because the worker is replaced. |
| error | `postgreswatch: alert notify failed` | `reason` `watch` `regression` `err` | An alert for a regression tied to a deploy was not delivered: [`AlertNotifyFailed`](reasons.md#alertnotifyfailed). |
| error | `postgreswatch: attribution revert disabled by invalid configuration` | `reason` `watch` `err` | Draft revert pull requests are off because `attribution.revert` is wrong: [`RevertDisabled`](reasons.md#revertdisabled). The engine keeps working. |
| info | `postgreswatch: attribution.actuate ignored, it only acts in enforce mode` | `reason` `watch` | `attribution.actuate` is set but the watch is in shadow mode, so nothing will be paused or aborted: [`ShadowMode`](reasons.md#shadowmode). |
| info | `postgreswatch: attribution.actuate ignored, no Argo Rollouts actuator is available (enable manager.autoAbort.rbacEnabled)` | `reason` `watch` | `attribution.actuate` is set but the manager has no Argo Rollouts RBAC: [`NoActuator`](reasons.md#noactuator). |
| error | `postgreswatch: auto-abort failed` | `reason` `watch` `rollout` `confidence` `err` | The p-value auto-abort tried to abort a Rollout and failed: [`AutoAbortFailed`](reasons.md#autoabortfailed). |
| info | `postgreswatch: auto-abort skipped, deploysource not found` | `reason` `watch` `source` `err` | The p-value auto-abort found a regression and no DeploySource to act through: [`AutoAbortSkipped`](reasons.md#autoabortskipped). Normal for sources that have no abort action. |
| info | `postgreswatch: auto-abort triggered` | `reason` `watch` `rollout` `confidence` `query` | The p-value auto-abort aborted a Rollout: [`AutoAbortTriggered`](reasons.md#autoaborttriggered). |
| error | `postgreswatch: collector exited unexpectedly` | `reason` `watch` `err` | The collector stopped on its own: [`CollectorExited`](reasons.md#collectorexited). The watch stops scraping until its worker is replaced. |
| error | `postgreswatch: failed to persist PerformanceRegression CR` | `reason` `watch` `regression` `err` | The regression was detected and alerted but could not be saved as a `PerformanceRegression`: [`RegressionPersistFailed`](reasons.md#regressionpersistfailed). |
| error | `postgreswatch: failed to persist periodic PerformanceRegression CR` | `reason` `watch` `regression` `err` | A periodic regression could not be saved as a `PerformanceRegression`: [`RegressionPersistFailed`](reasons.md#regressionpersistfailed). |
| error | `postgreswatch: periodic alert notify failed` | `reason` `watch` `regression` `err` | An alert for a periodic regression (not tied to a deploy) was not delivered: [`AlertNotifyFailed`](reasons.md#alertnotifyfailed). |
| info | `postgreswatch: spec.autoAbort ignored, attribution.actuate decides instead` | `reason` `watch` | The older p-value auto-abort stands down because `attribution.actuate` decides: [`AutoAbortIgnored`](reasons.md#autoabortignored). |
| info | `remoteClusterSecretRef is deprecated; register the cluster as a PostgresRadarCluster and use remoteClusterRef instead` | `watch` `namespace` `see` | The watch uses the deprecated `remoteClusterSecretRef`. It still works. Register the cluster as a PostgresRadarCluster and use `remoteClusterRef`; `see` points to the page. |
| error | `unable to resolve DSN` | `err` `reason` | The watch's connection string could not be read from its Secret: [`DSNUnresolved`](reasons.md#dsnunresolved). |
| error | `unable to resolve referenced secrets` | `err` `reason` | A Secret the watch refers to besides the DSN could not be read: [`SecretsUnresolved`](reasons.md#secretsunresolved). |
| error | `unable to start watch worker` | `err` `reason` | The watch's worker could not be built: [`WorkerStartFailed`](reasons.md#workerstartfailed). The watch shows `Phase: Failed`. |
| info | `workloadwatch: deploy event synthesised from native rollout` | `reason` `kind` `workload` `revision` `deploySource` | A Rollout or Deployment finished a new revision and the manager made a deploy event from it: [`DeployEventSynthesised`](reasons.md#deployeventsynthesised). |
| info | `workloadwatch: postgresWatchRef not ready yet, will retry` | `reason` `deploySource` `workload` `kind` | A deploy was seen but its PostgresWatch is not running yet, so it will be retried rather than lost: [`PostgresWatchNotReady`](reasons.md#postgreswatchnotready). |

## Attribution engine

Judging deploys, acting on Rollouts and reading the Postgres log. These lines carry a [reason code](reasons.md).

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| info | `attribution (enforce): decision below the acting threshold, nothing to execute` |  | Enforce mode, and the decision is `none` or `alert`: [`BelowActingThreshold`](reasons.md#belowactingthreshold). |
| info | `attribution (enforce): verdict served to the rollout controller` |  | A `hold` or `rollback` decision, enforced and now served at the verdict endpoint: [`VerdictServed`](reasons.md#verdictserved). |
| info | `attribution (shadow): decision, not executed` |  | The engine judged a deploy and the watch is in shadow mode, so nothing was done: [`ShadowMode`](reasons.md#shadowmode). `action` is what it would have done; `reasons` says why. |
| info | `attribution (shadow): would open a draft revert pull request, not opened` | `deploy` `revision` `grade` | Shadow mode, and a deploy was convicted at `hold` or above: this is the pull request the radar would open in enforce mode. Nothing was sent to GitHub. |
| warn | `attribution.arms: cannot read the server's settings` | `reason` `watch` `err` | The Postgres settings could not be read, retried every 30 seconds: [`ArmsSettingsUnread`](reasons.md#armssettingsunread). |
| info | `attribution.arms: the Postgres log can serve as an arm-labelled source` | `reason` `watch` `sampleRate` | The log is usable: [`ArmsUsable`](reasons.md#armsusable). |
| warn | `attribution.arms: the Postgres log cannot be used yet` |  | One Postgres setting stops the log from being used: [`ArmsSettingsRejected`](reasons.md#armssettingsrejected). `setting`, `value`, `problem` and `fix` name it and say what to set. |
| warn | `attribution.arms: the Postgres log setting costs more than it needs to` |  | The log works, but a setting makes it cost more than it needs to. `setting`, `problem` and `fix` say which; this line has no reason code because nothing is wrong. |
| info | `attribution: acted on the rollout` | `reason` `deploy` `rollout` `action` `grade` `impact` `reasons` | The Rollout was paused or aborted: [`Acted`](reasons.md#acted). |
| warn | `attribution: actuation paused by annotation, nothing paused or aborted` | `reason` `deploy` `action` | The engine wanted to act but the kill switch is on: [`ActuationPaused`](reasons.md#actuationpaused). |
| warn | `attribution: cannot tell the arms of a deploy apart` | `reason` `deploy` `err` | The pods of the canary and the stable arm could not be told apart for this evaluation, which falls back to grade B at best: [`ArmsUndistinguished`](reasons.md#armsundistinguished). |
| info | `attribution: checked the abort` | `reason` `deploy` `result` `detail` | The check after an abort: `result` is `recovered` ([`AbortRecovered`](reasons.md#abortrecovered)) or `persisted` ([`AbortPersisted`](reasons.md#abortpersisted)). |
| warn | `attribution: circuit breaker open, the engine will only alert until it is reset` | `reason` `deploy` `result` `detail` `howToReset` | Two aborts in a row did not help and the engine now only alerts: [`BreakerOpen`](reasons.md#breakeropen). `howToReset` says how to close it. |
| error | `attribution: could not act on the rollout, will retry at the next evaluation` | `reason` `deploy` `rollout` `action` `err` | The pause or abort call failed: [`ActuationFailed`](reasons.md#actuationfailed). It is retried at the next evaluation. |
| info | `attribution: could not check the abort, too little data after it` | `reason` `deploy` | The check 15 minutes after an abort had too little data: [`AbortUnverifiable`](reasons.md#abortunverifiable). |
| info | `attribution: deploy registered, it will be judged from 10 to 45 minutes after it` | `reason` `deploy` `namespace` `app` `revision` `source` `firstEvaluation` `lastEvaluation` | The engine tracks this deploy: [`DeployRegistered`](reasons.md#deployregistered). `firstEvaluation` and `lastEvaluation` bound when it is judged. |
| info | `attribution: draft revert pull request` | `deploy` `outcome` `detail` `url` | The result of an attempt to open a draft revert pull request. `outcome` is `opened`, `already-proposed`, `skipped` (a guard declined) or `failed`, and `detail` explains it. |
| info | `attribution: namespace not allowed for actuation, nothing paused or aborted` | `reason` `deploy` `namespace` `action` | The engine wanted to act but the namespace is not allowed: [`NamespaceNotAllowed`](reasons.md#namespacenotallowed). |
| warn | `attribution: no query samples to judge the deploy by, evaluation skipped` | `reason` `deploy` `namespace` `app` | An evaluation was skipped because there is nothing to read: [`NoSamples`](reasons.md#nosamples). |
| info | `attribution: not an Argo Rollouts deploy, nothing to pause or abort` | `reason` `deploy` `source` `action` | The engine wanted to act but the deploy is not a Rollout from an `argo-rollouts` source: [`NotArgoRollout`](reasons.md#notargorollout). |
| error | `postgreswatch: attribution.arms disabled, …` | `reason` `watch` | `attribution.arms` is configured but cannot work, so grade A evidence is off: [`ArmsDisabled`](reasons.md#armsdisabled). The text after the comma says why. |

## Dashboard history

The record behind the live dashboard.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| warn | `observe history: cannot record; the radar keeps acting, history will have a gap` | `reason` `err` | The dashboard's history store could not be written: [`HistoryNotRecording`](reasons.md#historynotrecording). Decisions are not affected. |
| debug | `observe history: series not stored` | `deploy` `err` | Debug. A deploy's query series could not be saved to the history. Only seen with `--log-level=debug`. |

## Collector

Reading `pg_stat_statements`. The standalone `collector` command writes the same lines.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| error | `collector exited with error` | `err` | The collector stopped with an error and the process exits. Read `err`. |
| info | `collector: detected PostgreSQL older than 13; using legacy pg_stat_statements column names (total_time/mean_time)` | `server_version_num` | The server is older than 13, so the collector reads the legacy column names (`total_time`, `mean_time`). Informational. |
| info | `collector: metrics server listening` | `addr` | The standalone collector's metrics endpoint is up on `addr`. |
| info | `collector: pg_stat_statements counters restarted for query; using activity since the reset` | `query_id` `calls` `stats_since` | A query's counters went backwards: a `pg_stat_statements_reset()`, an eviction or a crash. The collector uses only the activity since the reset. If this is frequent for many queries, something is resetting the statistics. |
| debug | `collector: plan capture failed for query, skipping` | `query_id` `err` | Debug. A plan could not be captured for one query and was skipped. |
| info | `collector: plan-diff capture unavailable (requires PostgreSQL 16+ for EXPLAIN GENERIC_PLAN); continuing without it` | `err` | `spec.capturePlans` is on and the server cannot run `EXPLAIN (GENERIC_PLAN)`, which needs PostgreSQL 16 or newer. Detection continues without plan diffs. |
| error | `collector: scrape failed` | `err` | One scrape of `pg_stat_statements` failed. Read `err`: `relation "pg_stat_statements" does not exist` and `permission denied` are covered in [Setup](setup.md). The next scrape tries again; the `Collecting` condition turns `False` after three missed intervals. |
| info | `collector: starting scrape loop` | `interval` `retention` `cluster` | The collector started. `interval` is the scrape interval and `retention` how long samples stay in memory. |
| info | `collector: stopping` |  | The collector's context was cancelled: the watch was deleted or its spec changed, or the process is shutting down. |
| error | `--dry-run: collector ping failed` | `err` | `--dry-run` could not reach the database. Read `err`, and see [Setup](setup.md). |
| info | `--dry-run: configuration and connectivity OK` | `version` | `--dry-run` connected to the database and exits with code 0. |
| error | `--dsn is required` |  | No `--dsn` was given. The process exits with code 1. |
| error | `failed to create collector` | `err` | The collector could not be built from the flags, usually a malformed `--dsn`. The process exits. |
| error | `--max-query-text-len must be positive` | `value` | The flag must be above zero. The process exits. |
| error | `metrics server error` | `err` | The metrics server stopped with an error, usually because the address is in use (`--listen`). |
| error | `metrics server shutdown error` | `err` | The metrics server did not shut down cleanly. Informational. |

## Correlation engine

Detecting regressions. Lines marked `debug` only appear with `--log-level=debug`.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| info | `correlation: analysing deploy` | `event_id` `query_count` `window_minutes` | The correlation engine started comparing the queries before and after a deploy. `query_count` is how many it looks at. |
| debug | `correlation: change point too far from deploy, rejecting` | `query_id` `change_at` `deploy_at` `distance` `tolerance` | Debug. A change was found, but too far from the deploy time to be blamed on it. |
| info | `correlation: deploy event registered for retry` | `event_id` `deploy_at` `retry_until` | A deploy arrived before the radar had enough history. It is retried until `retry_until`. |
| info | `correlation: deploy event's analysis window elapsed` | `event_id` `queries_detected` | The retry window ended. `queries_detected` is how many regressions were found for the deploy in total. |
| debug | `correlation: detection did not survive multiple-testing control` | `query_id` `p_value` `mode` `queries_tested` | Debug. A detection was dropped by the multiple-testing control (`mode`) because many queries were tested at once. |
| debug | `correlation: insufficient data, not yet ready to evaluate` | `query_id` `pre_samples` `post_samples` `min_samples` `pre_executions` `post_executions` `min_executions` `executions_checked` | Debug. A query does not have enough samples or executions yet before and after the deploy; the engine waits and tries again. |
| info | `correlation: multiple-testing control removed detections` | `mode` `queries_tested` `detected_before` `removed` `level` | The multiple-testing control removed `removed` of `detected_before` detections. Informational; set `spec.multipleTesting: none` to turn it off. |
| debug | `correlation: no change point found, rejecting naive mean shift` | `query_id` `change_factor` | Debug. The mean shifted but no change point was found in the series, so the naive shift was rejected as noise. |
| info | `correlation: periodic analysis` | `query_count` `window_minutes` `now` | The scheduled analysis, which looks for regressions not tied to a deploy, ran over the retained window. |
| info | `correlation: periodic regression detected` | `query_id` `change_factor` `confidence` `change_at` | A query regressed with no deploy to blame, and passed every test. |
| info | `correlation: periodic regression newly detected, suppressing until recovery` | `query_id` `confidence` | A periodic regression was reported once and is now suppressed until the query recovers, so it does not alert again. |
| info | `correlation: periodic regression recovered, re-arming` | `query_id` `status` | A suppressed periodic regression recovered and is armed again. |
| info | `correlation: regression detected` | `query_id` `change_factor` `confidence` `change_at` | A query regressed after a deploy and passed every test. A `PerformanceRegression` and an alert follow. `change_factor` is the new latency over the old. |
| debug | `correlation: t-test result` | `query_id` `p_value` `change_factor` `change_at` | Debug. The significance test of one query: `p_value`, `change_factor` and the `change_at` it found. |

## Deploy events

Receiving deploy events. The standalone `ingester` command writes the same lines.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| info | `--dry-run: configuration OK` | `version` `listen` `apiServerHost` | `--dry-run` found the configuration valid and exits with code 0. |
| error | `--dry-run: invalid --listen address` | `value` `err` | `--listen` is not a valid address. Exits. |
| error | `--dry-run: unknown --source-type` | `value` | `--source-type` is not one the ingester knows. Exits. |
| error | `events handler: encode` | `err` | The response for `/events` could not be encoded. Informational. |
| warn | `ingester: OTLP spans matched but were rejected` | `rejected` `detail` | Spans matched the mapping and were rejected. `detail` says why (for example a missing app name or revision attribute). |
| debug | `ingester: deploy event ignored, its app is not the one this source is configured for` | `id` `app` `configuredApp` | Debug. The event is for an app other than the DeploySource's `appName`, so it was dropped and the sender still got `204`. If you expected this deploy to be judged, `app` and `configuredApp` show the mismatch. |
| warn | `ingester: deploy event not stored, the watch this source feeds is not running` | `source` | A webhook arrived for a DeploySource whose PostgresWatch is not running, so the event was not stored. The sender gets `503` with `Retry-After: 10` and can send it again; start the watch (see [`PostgresWatchNotReady`](reasons.md#postgreswatchnotready)). |
| info | `ingester: deploy event stored` | `id` `app` `revision` `timestamp` | A deploy event was received and stored for the watch. It is followed by `attribution: deploy registered`. If you do not see this line for a deploy, the event never arrived: see [A deploy never shows up](deploy-sources.md#a-deploy-never-shows-up). |
| error | `ingester: failed to decode OTLP request` | `err` | The OTLP request body could not be decoded. Check the exporter's protocol and content type. |
| error | `ingester: failed to parse webhook` | `source_type` `err` | The body is not the format `source_type` expects. Check the sender's payload against [Deploy sources](../webhooks.md). |
| info | `ingester: listening` | `addr` `source_type` | The standalone ingester is up on `addr`. |
| error | `ingester: otlp source is misconfigured` | `err` | An `otlp` source has no usable span mapping, so every request is answered with `500`. `err` says what is wrong with the mapping; fix it in the DeploySource or the ingester flags. |
| warn | `ingester: rejected /events request with invalid or missing token` | `remote_addr` | A read of `/events` had an invalid or missing token and was refused. |
| warn | `ingester: rejected webhook request with invalid or missing token` | `source_type` `remote_addr` | A webhook arrived with an invalid or missing token and was refused with `401`. Send `X-Webhook-Token`, or check `remote_addr` if you do not recognise the sender. |
| error | `invalid OTLP flags` | `err` | The OTLP flags do not fit the `--source-type`. The process exits. |
| error | `invalid deploy source configuration` | `err` | The deploy source configuration is unusable (an `otlp` source without a mapping). The process exits. |
| error | `server error` | `err` | The ingester's HTTP server stopped with an error, usually an address already in use. |

## Alerting, OTLP export, Prometheus and storage

Everything that leaves the radar or is stored by it.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| info | `alerting: notification sent` | `regression` `status_code` | An alert was delivered. `status_code` is the destination's answer. |
| info | `alerting: notification skipped; webhook url is not configured` | `regression` | A regression was detected but the watch has no alert URL, so nothing was sent. |
| warn | `otlpexport: delivery failed, will retry` | `attempt` `in` `err` | One delivery attempt failed. It is retried after `in`. |
| error | `otlpexport: giving up on regression` | `regression` `endpoint` `err` | Delivery failed after every retry and the regression was not exported. `err` is the last failure. |
| warn | `otlpexport: queue full, dropping regression` | `regression` `endpoint` | Regressions are produced faster than the OTLP exporter delivers them, and one was dropped. Check the collector endpoint's health. |
| warn | `promsource: query failed; the engine will see no samples until this is fixed` | `operation` `err` | A query against Prometheus failed. Until it is fixed the engine sees no samples, which shows up as `NoSamples`: [`NoSamples`](reasons.md#nosamples). `operation` is `samples_in_range` or `all_query_ids`. |
| error | `storage: prune failed` | `store` `err` | A prune sweep failed. Read `err`; it is retried at the next interval. |
| info | `storage: prune loop started` | `store` `interval` `retention` | The state store's prune loop started. |
| debug | `storage: prune swept` | `store` `cutoff` | Debug. A prune sweep finished; rows older than `cutoff` were removed. |
| warn | `storage: retention <= 0, disabling prune loop` | `store` | The retention is zero or negative, so the prune loop is off and the state store grows without bound. |

## Operator (all-in-one mode)

What the `operator` command writes. It shares the collector, correlation and ingester lines above.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| error | `dashboard API server error` | `err` | The dashboard API stopped with an error. |
| error | `--dry-run: --periodic-interval-minutes must be positive` | `value` | The interval must be positive. Exits. |
| error | `--dry-run: --periodic-window-minutes must be positive` | `value` | The window must be positive. Exits. |
| error | `--dry-run: invalid alerting configuration` | `err` | `--dry-run` found invalid alerting settings. Exits. |
| error | `--dry-run: postgres state backend connection failed` | `err` | `--dry-run` could not reach the state backend. Exits. |
| error | `--dry-run: unknown --state-backend (want memory or postgres)` | `value` | `--state-backend` is not `memory` or `postgres`. Exits. |
| error | `failed to configure OTLP export` | `err` | The OTLP exporter could not be built. Exits. |
| error | `failed to configure alerting` | `err` | The alerting flags are invalid. Exits. |
| error | `failed to initialise postgres state backend` | `err` | `--state-backend=postgres` could not connect or create its schema. Exits. |
| error | `failed to read --alert-template-file` | `err` | The alert template file could not be read. Exits. |
| error | `invalid --dsn` | `err` | The connection string is malformed. The process exits. |
| error | `invalid --multiple-testing` | `err` | `--multiple-testing` is not an allowed value. Exits. |
| error | `invalid --state-dsn` | `err` | The state backend's DSN is malformed. Exits. |
| error | `invalid OTLP export configuration` | `err` | The OTLP export flags are invalid. Exits. |
| warn | `operator: backfill: list query ids failed, starting with cold sample history` | `err` | On start the operator could not read the stored query ids, so it starts with an empty sample history. |
| warn | `operator: backfill: load events failed, starting with cold event history` | `err` | The stored deploy events could not be read, so it starts with no event history. |
| warn | `operator: backfill: load samples failed` | `query_id` `err` | The stored samples of one query could not be loaded; that query starts cold. |
| info | `operator: backfilled collector sample history` | `samples` `query_ids` | Stored samples were loaded back after a restart. |
| info | `operator: backfilled event history` | `events` `cursor` | Stored deploy events were loaded back after a restart. |
| info | `operator: dashboard API server listening` | `addr` | The dashboard API is up on `addr`. |
| info | `operator: exporting regressions as OTLP logs` | `url` | OTLP export is on and sends to `url`. |
| info | `operator: metrics server listening` | `addr` | The metrics listener is up on `addr`. |
| error | `operator: notify failed` | `err` `regression` | An alert was not delivered (the operator's counterpart of [`AlertNotifyFailed`](reasons.md#alertnotifyfailed)). |
| error | `operator: periodic notify failed` | `err` `regression` | A periodic alert was not delivered (the operator's counterpart of [`AlertNotifyFailed`](reasons.md#alertnotifyfailed)). |
| error | `operator: persist event failed` | `err` `event_id` | A deploy event could not be written to the state backend. |
| error | `operator: persist sample failed` | `err` `query_id` | A sample could not be written to the state backend. The radar keeps running from memory; history will have a gap after a restart. |
| info | `operator: postgres state backend enabled` | `schema` `retention` `prune_interval` `separate_state_dsn` | Samples and events are persisted to Postgres and survive a restart. `separate_state_dsn` is whether it is a different database from the monitored one. |
| info | `operator: webhook server listening` | `addr` | The webhook listener is up on `addr`. |
| info | `security profile` | `security-profile` | The operator started with this `--security-profile`. |
| error | `unknown --state-backend (want memory or postgres)` | `value` | `--state-backend` is not `memory` or `postgres`. Exits. |
| error | `webhook server error` | `err` | The webhook listener stopped with an error, usually an address in use. |

## Dashboard API and command

The read-only API and the `pg-regression-radar dashboard` command.

| Level | Message | Fields | What it means and what to do |
|---|---|---|---|
| warn | `dashboard: cannot reach the manager` | `err` | `pg-regression-radar dashboard` lost its port-forward to the manager. It retries. Check that the manager pod runs and that you may `get pods` and port-forward. |
| info | `dashboard: connected to the manager` | `pod` | The dashboard is connected to the manager pod `pod`. |
| info | `dashboard: the leader changed` | `from` `to` | The leader moved from one manager pod to another and the dashboard followed it. |
| error | `dashboardapi: list deploys failed` | `err` | The dashboard API could not read deploys from its store. The request gets `500`. |
| error | `dashboardapi: list query ids failed` | `err` | The dashboard API could not list query ids. The request gets `500`. |
| error | `dashboardapi: list regressions failed` | `err` | The dashboard API could not list `PerformanceRegression` objects (usually RBAC or an API error). The request gets `500`. |
| error | `dashboardapi: list watches failed` | `err` | The dashboard API could not list `PostgresWatch` objects. The request gets `500`. |
| error | `dashboardapi: load query samples failed` | `query_id` `err` | The dashboard API could not load the samples of one query. The request gets `500`. |
| error | `dashboardapi: load samples failed` | `query_id` `err` | The dashboard API could not load samples. The request gets `500`. |

## See also

- [Logs](logs.md): format, levels and how to filter.
- [Reason codes](reasons.md): the causes behind the lines that carry a `reason`.
- [Where to look](where-to-look.md): which signal answers which question.
