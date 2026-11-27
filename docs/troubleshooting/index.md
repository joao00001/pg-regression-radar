# Troubleshooting

*Find what the radar is doing, why it is not doing what you expect, and what to change.*

## Overview

The radar reports on itself in several places, each suited to a different question. This section is organised the way you meet a problem: first **where to look**, then the **symptom**, then the **catalogs** that name every log line, reason and metric.

Before anything else, read the conditions of the watch. They answer the three questions most problems come down to, in one command:

```bash
kubectl get pgw <name> -n <ns> -o json \
  | jq '.status.conditions[] | {type, status, reason, message}'
```

| Condition | Question it answers |
|---|---|
| `Collecting` | Is the radar reading my database? |
| `ArmsLog` | Can it tell a canary from the stable pods? |
| `Actuating` | May it pause or abort a Rollout right now, and if not, why? |

A condition that is not healthy has a `reason` and a message that ends with the link to its entry in [Reason codes](reasons.md).

!!! note "New in 1.9.2"
    Conditions, reason codes, one log format on every command, `--log-level` and `--log-format`, and the `reset-breaker` annotation. They change no CRD schema and no default.

## Start with a symptom

| Symptom | Page |
|---|---|
| I need to know where a given kind of information lives | [Where to look](where-to-look.md) |
| The radar will not start, cannot reach the database, or `pg_stat_statements` is not available | [Setup and the database](setup.md) |
| It runs, and never reports a regression | [Nothing is detected](detection.md) |
| A deploy happened and the radar did not judge it | [Deploy sources](deploy-sources.md) |
| It judged a deploy and did not pause or abort it, or I want to turn enforce on | [Enforce mode](enforce.md) |
| A regression was found and no alert arrived, or the dashboard shows nothing | [Alerts and the dashboard](alerts-and-dashboard.md) |
| I want to read the log, or ask for more detail | [Logs](logs.md) |
| I want to ask for help | [Collect a report](report.md) |
| A short question | [FAQ](faq.md) |

## Catalogs

These pages list everything, and a test keeps them equal to the code on every build.

| Catalog | What it lists | Use it to |
|---|---|---|
| [Reason codes](reasons.md) | the stable name of each decision or fault, with its causes, how to confirm each, and the fix | go from a `reason` in a log or a condition to what to do |
| [Log catalog](log-catalog.md) | every log message, its level, its fields and what it means | find what a line you saw means |
| [Metrics](metrics.md) | every Prometheus metric, its type and labels | choose what to graph and alert on |

## Where this page used to be

The single *Troubleshooting and FAQ* page that lived under Get started has been split into the pages above, and the old section titles now live at:

| Old section | Now |
|---|---|
| `pg_stat_statements` is not available | [Setup and the database](setup.md#pg_stat_statements-is-not-available) |
| Permission denied (`42501`) | [Setup and the database](setup.md#permission-denied-42501) |
| Nothing is detected | [Nothing is detected](detection.md) |
| The attribution engine only alerts, or does not act | [Enforce mode](enforce.md#the-engine-only-alerts-and-does-not-act) |
| Connecting to a local Postgres from Docker | [Setup and the database](setup.md#connecting-to-a-local-postgres-from-docker) |
| `cosign verify` fails on the network | [Setup and the database](setup.md#cosign-verify-fails-on-the-network) |

## See also

- [Troubleshooting toolkit](../kubernetes/troubleshooting.md): the Kubernetes order of checks (pods, events, RBAC).
- [Getting started](../getting-started.md#troubleshooting-common-setup-issues): a short list of first-run symptoms.
