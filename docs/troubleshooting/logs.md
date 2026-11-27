# Logs

*One format for every line, how to read it, how to ask for more detail, and recipes to find what you need.*

## Overview

Every command the radar ships (`manager`, `operator`, `collector`, `ingester`) writes its log to standard output, one JSON object per line, through one handler. The radar's own code, controller-runtime and anything that logs before the flags are read all come out the same way, so one `jq` filter works on the whole stream. Since 1.9.2 there is also one switch for the level and one for the format.

```json
{"time":"2026-11-27T15:04:05.000-03:00","level":"INFO","msg":"attribution: acted on the rollout","reason":"Acted","deploy":"d1","rollout":"checkout","action":"abort","grade":"A","impact":0.12}
```

## Flags

| Flag | Values | Default | Effect |
|---|---|---|---|
| `--log-level` | `debug`, `info`, `warn`, `error` | `info` | The least severe line that is written. `debug` adds the evaluation and scrape details that are normally left out. |
| `--log-format` | `json`, `text` | `json` | `json` is one object per line. `text` is `key=value`, easier to read in a terminal. |

With Helm, set `manager.logLevel` and `manager.logFormat` (both empty by default, which means `info` and `json`). In the lab, add the flags to the `args` of the `pgrr-manager` container.

!!! note "The old `--zap-*` flags still work"
    The manager used to take controller-runtime's `--zap-log-level`, `--zap-encoder` and `--zap-devel`. They are accepted and mapped (`--zap-log-level=debug` to `--log-level=debug`, `--zap-encoder=console` to `--log-format=text`, `--zap-devel` to both), and the manager logs one `logging:` line saying what it did. An explicit `--log-level` or `--log-format` wins over them. Any other `--zap-*` flag is accepted and has no effect. Replace them when you next edit the Deployment.

A value that is not accepted stops the process with a message naming the allowed values.

## Format

| Field | Meaning |
|---|---|
| `time` | When the line was written, RFC 3339. |
| `level` | `DEBUG`, `INFO`, `WARN` or `ERROR`. |
| `msg` | What happened, as a sentence. It can be reworded between releases; match on `reason` instead when there is one. |
| `reason` | A stable [reason code](reasons.md), on lines that explain a decision or a fault. |
| `err` | The error text, on a line about a failure. |

### Fields you can rely on

The names below are the same on every line that has them.

| Field | Where | Meaning |
|---|---|---|
| `watch` | watch and arms lines | the PostgresWatch, as `namespace/name` |
| `deploy` | attribution lines | the deploy event id |
| `app`, `namespace`, `revision` | deploy lines | what was deployed, and where |
| `action` | attribution lines | `none`, `alert`, `hold` or `rollback` for a decision, `pause` or `abort` for something done |
| `grade` | attribution lines | the evidence grade, `A` to `D` |
| `detail` | lines that carry a `reason` | one sentence about this occurrence. Free text goes here, never in `reason`. |
| `source`, `source_type` | deploy event lines | the DeploySource, and its type |

Lines written by controller-runtime itself (the manager's own start-up, "Starting workers", "Reconciler error") carry the fields it adds while it reconciles an object:

```json
{"level":"ERROR","msg":"Reconciler error","controller":"postgreswatch","controllerGroup":"radar.pgregressionradar.io","controllerKind":"PostgresWatch","PostgresWatch":{"name":"w","namespace":"ns"},"namespace":"ns","name":"w","reconcileID":"abc","logger":"controller-runtime","err":"..."}
```

`controller`, `controllerKind` and `reconcileID` say which reconciler and which pass; `namespace` and `name` the object; `logger` which controller-runtime component. They are not repeated in the [log catalog](log-catalog.md).

## Levels

| Level | What it is for | Example |
|---|---|---|
| `error` | Something the radar was asked to do did not happen. | `attribution: could not act on the rollout, will retry at the next evaluation` |
| `warn` | It keeps working, and an operator may want to look. | `attribution: no query samples to judge the deploy by, evaluation skipped` |
| `info` | A normal event or a deliberate choice, especially every decision the engine makes. | `attribution: deploy registered, ...` |
| `debug` | Per-scrape and per-query detail. Off by default because there is a line for each query. | `correlation: t-test result` |

Turn `debug` on for one investigation and back off after, because it writes a line per query per scrape.

## Recipes

Every recipe reads the manager log. Put one of these in front:

```bash
LOG='kubectl logs -n <ns> -l app.kubernetes.io/component=manager --since=2h --tail=-1'
```

**What went wrong lately**

```bash
$LOG | jq -c 'select(.level == "ERROR" or .level == "WARN") | {time, level, msg, reason, err}'
```

**Everything about one deploy**

```bash
$LOG | jq -c 'select(.deploy == "<deploy id>")'
```

**Every decision the engine made, and why**

```bash
$LOG | jq -r 'select(.msg | startswith("attribution")) | select(.action) | "\(.time) \(.deploy) \(.action) grade=\(.grade) \(.reason)"'
```

**Which reasons are being emitted, and how often**

```bash
$LOG | jq -r 'select(.reason) | .reason' | sort | uniq -c | sort -rn
```

**One watch**

```bash
$LOG | jq -c 'select(.watch == "<ns>/<name>")'
```

**The same, from a log shipper.** The fields above are plain top-level keys, so a Loki query `{app="pg-regression-radar-manager"} | json | reason="BreakerOpen"` or an Elasticsearch filter on `reason` works without parsing.

**A crash loop.** The line before the restart is in the previous container:

```bash
kubectl logs -n <ns> deploy/<manager> --previous | tail -20
```

## Alerting on logs

Alert on the `reason`, not on the message. A reason is a code that stays; a message is a sentence that gets reworded. The reasons worth an alert are marked `warn` or `error` in the [reason codes](reasons.md); `BreakerOpen`, `ArmsSettingsRejected`, `ArmsLogStale` and `ActuationFailed` are a good start. The same conditions exist as [metrics](metrics.md) and as status conditions.

## See also

- [Log catalog](log-catalog.md): every message with its fields and meaning.
- [Reason codes](reasons.md): causes and fixes.
- [Where to look](where-to-look.md).
