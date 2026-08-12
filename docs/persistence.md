# Persistence

*The optional Postgres-backed state store: what it persists, its schema, retention, and current trade-offs.*

## Overview

By default (`--state-backend=memory`) all state lives only in process memory: the Collector keeps its per-`queryid` samples in an in-memory map, and the Ingester keeps deploy events in an in-memory slice. That means a pod restart loses all history, and running multiple replicas gives each one an inconsistent, independent view of the world. Setting `--state-backend=postgres` additionally persists both to Postgres, so history survives restarts and can be inspected/shared across replicas.

## Configuring it

```bash
operator --dsn "host:5432/monitored_db?sslmode=disable" --state-backend postgres
# --state-dsn defaults to --dsn above: state lives in the SAME Postgres
# cluster being monitored. To isolate it, point at a separate instance:
# --state-dsn "host:5432/pgrr_state?sslmode=disable"
```

- **Same Postgres as the monitored cluster** (default, `--state-dsn` empty): simplest to operate — one connection string, one cluster. Trade-off: the tool's own write traffic shows up in the very `pg_stat_statements` it watches, and an outage of the monitored cluster also takes down the history.
- **Separate Postgres instance** (`--state-dsn` set): isolates storage load and availability from the cluster under observation, at the cost of running a second Postgres.

## Schema

Everything lives in its own schema so it never collides with application tables and can be dropped cleanly:

```sql
CREATE SCHEMA IF NOT EXISTS regression_radar;

CREATE TABLE regression_radar.query_samples (
    id                  BIGSERIAL PRIMARY KEY,
    query_id            BIGINT NOT NULL,
    query_text          TEXT NOT NULL,
    calls               BIGINT NOT NULL,
    total_exec_time_ms  DOUBLE PRECISION NOT NULL,
    mean_exec_time_ms   DOUBLE PRECISION NOT NULL,
    recorded_at         TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_query_samples_queryid_recorded_at ON regression_radar.query_samples (query_id, recorded_at);
CREATE INDEX idx_query_samples_recorded_at         ON regression_radar.query_samples (recorded_at);

CREATE TABLE regression_radar.deploy_events (
    id               TEXT PRIMARY KEY,
    source           TEXT NOT NULL DEFAULT '',
    app              TEXT NOT NULL DEFAULT '',
    cluster          TEXT NOT NULL DEFAULT '',
    namespace        TEXT NOT NULL DEFAULT '',
    revision         TEXT NOT NULL DEFAULT '',
    image_tag        TEXT NOT NULL DEFAULT '',
    event_timestamp  TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_deploy_events_event_timestamp ON regression_radar.deploy_events (event_timestamp);
```

!!! note "Why not `pg_regression_radar`?"
    PostgreSQL reserves the literal `pg_` prefix for its own system schemas — `CREATE SCHEMA pg_regression_radar` is rejected outright with "unacceptable schema name". The schema is named `regression_radar` for exactly this reason.

The schema is applied automatically on startup (`internal/storage/postgres`) using hand-rolled, idempotent DDL (`CREATE ... IF NOT EXISTS`) rather than a migration framework like `golang-migrate`/`goose` — both tables are append-mostly, derived/observational data that evolves purely additively, so the extra machinery of a versioned migration runner isn't earning its keep yet. If the schema ever needs a genuinely breaking change (rename, backfill, tightened constraint), that's the point to introduce one.

## Retention

Old rows are pruned periodically (`--state-prune-interval`, default 15 min) using a `DELETE ... WHERE recorded_at < now() - retention` sweep, where retention defaults to 7 days (`--state-retention`).

## Trade-offs and limitations

- **Not a replacement for leader election.** Pointing multiple operator replicas at the same state backend makes the *data* consistent and durable, but each replica still independently scrapes `pg_stat_statements` and runs the correlation engine — so you'd get duplicate scrapes and duplicate alerts. Preventing that requires only one replica being active at a time (leader election), which `cmd/manager` provides via controller-runtime's built-in leader election, not this package. Don't run N unattended `cmd/operator` replicas against a shared backend.
- **Extra write load.** Every scrape/webhook now also does a write to Postgres. The connection pool used for this is intentionally small (see `internal/storage/postgres.Open`) to keep the footprint low, especially when reusing the monitored cluster's own Postgres.

## Backfill on startup

When `--state-backend=postgres`, `RunOperator` loads recent history from the store and seeds the live in-memory Collector/Ingester with it *before* the webhook/metrics servers or the deploy-event poll loop start:

- `collector.Collector.Backfill` seeds the per-`queryid` sample history (covering the last `--retention-minutes`), so the correlation engine's view isn't empty for the first `--window-minutes` after a restart. It does **not** touch the live Prometheus gauges (`pg_regression_radar_query_mean_exec_time_ms`/`..._calls_total`) — those reflect the most recent *live* scrape, and overwriting them with historical values would misrepresent current server state to anything scraping `/metrics` before the first real scrape completes.
- `ingester.Store.Backfill` seeds deploy-event history the same way, and returns the resulting event count, which becomes the poll loop's initial `DrainSince` cursor. This matters for correctness, not just completeness: `DrainSince` treats anything past its cursor as newly-arrived work to analyse and potentially alert on. Backfilling events without also advancing the cursor past them would make every historical deploy event look brand new on restart, re-running correlation and potentially re-sending duplicate Slack alerts for regressions already reported in a previous process lifetime.

Both backfills are best-effort: a failure to load either kind of history is logged at `Warn` and the process starts with a cold in-memory view for that piece, exactly like before this existed, rather than failing startup outright.

## See also

- [Configuration Reference](configuration.md) — the full `--state-*` flag list.
- [Collector Internals](collector-internals.md) — the in-memory samples this store durably copies.
- [Roadmap](roadmap.md) — other known gaps.
