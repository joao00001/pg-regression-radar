# FAQ

*Short answers to the questions that come up after the radar is running.*

## Overview

Each answer is a few lines and points to the page that goes deeper. For a symptom, start at [Troubleshooting](index.md); for the place to look, at [Where to look](where-to-look.md).

## Why did the radar wait 10 minutes before judging my deploy?

The engine reads the database before and after the deploy and needs enough after to compare. It evaluates from 10 to 45 minutes after, every 5 minutes, and cannot convict before about 15. A Rollout that is promoted in five minutes is out of reach, so give it a pause step of at least 20 minutes. See [Enforce mode](enforce.md#before-you-turn-enforce-on).

## What is the difference between shadow and enforce?

In **shadow** the engine judges every deploy and logs what it would do; the verdict endpoint always answers `pass` (with `wouldBe` showing the decision) and nothing is changed. In **enforce** it serves a verdict, and with `actuate.enabled` the manager pauses or aborts the Rollout itself. Run shadow for a few deploys and compare before you enforce.

## The watch says `Running`, so why does it detect nothing?

`Phase: Running` means the worker started. Whether it is reading the database is the `Collecting` condition, and whether it can act is `Actuating`. See [Nothing is detected](detection.md#1-is-it-reading-the-database).

## Do I need the Postgres log (`attribution.arms`)?

Only to **abort**. Through `pg_stat_statements` alone the best evidence is grade B and the strongest action a pause, which a person can resume with one click. An abort needs grade A, which needs the Postgres log to tell the canary from the stable pods. See [Grade A needs the Postgres log](enforce.md#grade-a-needs-the-postgres-log).

## Is it a problem that the `Actuating` condition is `False`?

It is a status, not an error, and the reason says why: in shadow mode it is `ShadowMode`, and without `attribution.actuate` it is `ActuationNotAsked`. It is a problem only if you expected the manager to act. See [Enforce mode](enforce.md#read-the-three-conditions-first).

## How do I turn the engine off quickly?

Annotate the watch: `kubectl annotate postgreswatch <name> -n <ns> pg-regression-radar.io/actuation-paused=true`. Nothing is paused or aborted until you remove it, and no restart is needed. The engine keeps judging and logging.

## What happens when the manager restarts?

The collector's samples, the action ledger (the last 200 attempts) and the circuit breaker's count are in memory and start empty. A deploy that had not been judged yet cannot be: it logs [`NoSamples`](reasons.md#nosamples). The metrics and the log are the durable record; the dashboard's history is kept in memory too unless `--observe-history=postgres`.

## Why is only one manager pod doing anything?

Leader election: with more than one replica exactly one runs the watches and serves the webhook, the metrics and the verdict listeners; a standby takes over if the leader goes. Find the leader in the Lease `pg-regression-radar-manager.radar.pgregressionradar.io`. A request that a Service sends to a standby is refused: see [A Service can send an event to a standby](deploy-sources.md#2-did-the-request-reach-the-manager).

## Are the logs JSON?

Yes, on every command, and since 1.9.2 on every line. Earlier releases of the manager mixed JSON, controller-runtime's own format and plain text. Use `--log-format=text` for a terminal. See [Logs](logs.md).

## How do I get more detail?

`--log-level=debug` (chart: `manager.logLevel: debug`). It writes a line per query per scrape, so turn it on for one investigation. `--zap-log-level` still works and is mapped.

## A log message changed between releases. Is that a break?

No. Messages are sentences and can be reworded; the `reason` field is the stable part, and each code has an entry in [Reason codes](reasons.md). Match on `reason` in alerts and parsers.

## Where did the old Troubleshooting page go?

It is now a section of its own. The database-side errors are in [Setup and the database](setup.md), "nothing is detected" in [its own page](detection.md), and the attribution engine in [Enforce mode](enforce.md).

## See also

- [Troubleshooting](index.md)
- [Collect a report](report.md)
