# Playground

*Run the real detection engine against a real PostgreSQL, entirely in your browser: no Postgres, no Kubernetes, no install.*

## Overview

The playground runs a small SQL workload against PostgreSQL with `pg_stat_statements` enabled, applies a "deploy" halfway through, and feeds what the collector would have scraped to pg-regression-radar's correlation engine. Pick a scenario, change the traffic or the thresholds, or write your own schema, workload and deploy, and see which queries the engine flags and why.

Nothing is sent to a server. Everything runs in your browser tab.

<link rel="stylesheet" href="../assets/playground/playground.css">
<div id="pgrr-playground"></div>
<noscript><p>The playground needs JavaScript and WebAssembly.</p></noscript>
<script type="module" src="../assets/playground/playground.js"></script>

## What you are looking at

The chart shows one query's latency over the 22 simulated minutes. Two lines tell the story that motivated the engine's design:

- **The dashed line** is `pg_stat_statements.mean_exec_time`, the average of every call since the counters were last reset. It moves slowly after a regression, and jumps for no reason after a reset.
- **The solid line** is the mean latency of only the calls made since the previous scrape, derived from the cumulative `calls` and `total_exec_time` counters. This is what the engine analyses. See [Collector Internals](collector-internals.md) and [Detection Algorithm](detection-algorithm.md).

The table gives the engine's verdict per query. `InsufficientData` means the query ran too rarely in one of the windows for either answer to be trustworthy; see `--min-samples` and `--min-executions` in the [Configuration Reference](configuration.md).

## The scenarios

| Scenario | What happens | Expected verdict |
|---|---|---|
| Deploy removes an index | The customer lookup turns into a sequential scan. | `Detected` on that query only |
| Harmless deploy | An unrelated index is added. | `NoRegression` everywhere |
| Stats reset after a harmless deploy | `pg_stat_statements_reset()` runs three scrapes after the deploy. | `NoRegression`: the collector detects the reset |
| Deploy adds a slow trigger | Only the `UPDATE` gets slower. | `Detected` on the `UPDATE` only |
| Same regression, almost no traffic | The index is dropped, but each query runs twice per scrape. | `InsufficientData` |

Under **Edit the SQL** you can replace the schema, the workload (one statement per line, `:id` becomes a random id) and the deploy.

## How it works

```text
PGlite (PostgreSQL in WebAssembly)        Go engine compiled to WebAssembly
  workload + deploy SQL                     internal/correlation
        |                                          ^
        v                                          |
  pg_stat_statements --snapshot every 30 s--> intervals, change point, t-test
```

- **PostgreSQL** is [PGlite](https://pglite.dev) running PostgreSQL 18 compiled to WebAssembly, with the real `pg_stat_statements` extension. It is loaded on first use from the jsDelivr CDN (about 5 MB compressed).
- **The engine** is the same `internal/correlation` package the operator runs, compiled with `GOOS=js GOARCH=wasm` (about 1 MB compressed) from the same commit as this page. The playground is part of each release's docs, so it runs that release's algorithm.
- **Time is virtual.** The simulation executes the workload as fast as it can and stamps each snapshot as if it were taken every 30 seconds, so a 22-minute scenario takes a few seconds.

## Limits

- **Timings are not production timings.** A browser tab running PostgreSQL in WebAssembly is far slower than a server and its timings are noisier. The playground shows how the engine reasons about a latency series, not what your database will do.
- **One database, one process.** There is no replication, connection pooling, I/O contention or autovacuum behaviour of a real cluster.
- **No plan capture and no alerting.** The playground covers detection; plan diffs (`--capture-plans`), alert delivery and deploy-event sources need a real cluster. See [Getting Started](getting-started.md).
- **Browser support.** A current Chrome, Edge, Firefox or Safari (16.4 or later).

## See also

- [Detection Algorithm](detection-algorithm.md): the change-point search and t-test the engine applies.
- [Collector Internals](collector-internals.md): how cumulative counters become interval samples.
- [Quickstart Validation](quickstart-validation.md): the same idea against a real cluster.
