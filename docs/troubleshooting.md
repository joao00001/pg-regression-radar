# Troubleshooting and FAQ

*The errors people hit most often when pointing the radar at a Postgres, what each one means, and the shortest fix.*

## Overview

Most first-run problems come from the database side, not from the radar: the extension is not loaded, the role cannot read what it needs, or the connection string points at the wrong place. This page lists those cases with the message you will actually see. Start with a dry run, which checks the connection and the extension without starting any server:

```bash
pg-regression-radar operator --dsn "postgres://user:password@host:5432/mydb?sslmode=disable" --dry-run
```

For problems inside a cluster (pods, CRDs, RBAC), use the [Troubleshooting toolkit](kubernetes/troubleshooting.md). For a short list of setup symptoms, see [Getting started](getting-started.md#troubleshooting-common-setup-issues).

## `pg_stat_statements` is not available

There are two different failures, and they have different fixes.

**The extension is not created in this database.** `--dry-run` fails with:

```text
collector: pg_stat_statements extension is not installed in this database (run: CREATE EXTENSION pg_stat_statements)
```

Connect to the database named in your DSN (extensions are per database) and run:

```sql
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

**The library is not loaded by the server.** The extension exists, but every scrape fails with a message from Postgres, wrapped by the collector:

```text
query pg_stat_statements: pq: pg_stat_statements must be loaded via "shared_preload_libraries"
```

Add it to the server configuration and **restart** Postgres (a reload is not enough for this setting):

```text
shared_preload_libraries = 'pg_stat_statements'
```

Check it afterwards:

```sql
SHOW shared_preload_libraries;
SELECT count(*) FROM pg_stat_statements;
```

On managed services (RDS, Cloud SQL, Azure) this is a parameter-group or flag setting, not a file you edit. If you already preload other libraries, keep them in the list, separated by commas.

## Permission denied (`42501`)

Reading `pg_stat_statements` and capturing plans need different privileges.

| What fails | Why | Fix |
|---|---|---|
| Scrape errors with `permission denied` | The role cannot read other users' statements | Grant the monitoring role `pg_read_all_stats` |
| Queries show as `<insufficient privilege>` | Same cause: Postgres hides text and statistics of other roles from non-privileged ones | Same grant |
| No plan diffs, and nothing in the logs at the default level | Plan capture runs `EXPLAIN (FORMAT JSON, GENERIC_PLAN)` on your query text, which needs `SELECT` on the tables involved | Grant `SELECT` on those tables (see [Installation](installation.md)) |

```sql
GRANT pg_read_all_stats TO pgrr_monitor;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO pgrr_monitor;
```

!!! note "Plan capture fails quietly"
    A query whose plan cannot be captured is logged at `DEBUG` and skipped, so a `42501` from `EXPLAIN` does not stop detection and does not show up in a default log. Run with debug logging and look for `collector: plan capture failed for query, skipping`. `EXPLAIN (GENERIC_PLAN)` also needs PostgreSQL 16 or newer; on older servers the radar logs once that plan-diff capture is unavailable and continues without it.

## Nothing is detected

If the radar runs without errors but never reports a regression, check these in order:

1. **The counters were reset.** After `pg_stat_statements_reset()` or a server restart, the radar logs `collector: pg_stat_statements counters restarted for query; using activity since the reset`. It then needs new samples before it can compare windows.
2. **Not enough data yet.** A window needs `minSamples` scrape intervals with activity and `minExecutions` calls. See [Detection algorithm](detection-algorithm.md).
3. **The query is outside the top 500.** Each scrape reads the 500 busiest statements.
4. **Its own queries are excluded.** Statements containing `pg_stat_statements` are filtered out on purpose.

In Kubernetes, `PerformanceRegression` objects report `Detected`, `NoRegression` or `InsufficientData`; the last one means case 2.

## Connecting to a local Postgres from Docker

`localhost` inside a container is the container itself, not your machine. A DSN that works from your shell fails from the radar's container with `connection refused`.

| Where is Postgres? | Use as host |
|---|---|
| On your laptop (Docker Desktop on macOS or Windows) | `host.docker.internal` |
| On your Linux host | `host.docker.internal`, after adding `--add-host=host.docker.internal:host-gateway` to `docker run` |
| In another container | the container or Compose service name, on the **same Docker network** |

```bash
docker run --rm --add-host=host.docker.internal:host-gateway \
  ghcr.io/joao00001/pg-regression-radar/operator:{{ release_tag }} \
  --dsn "postgres://user:password@host.docker.internal:5432/mydb?sslmode=disable" --dry-run
```

If Postgres itself runs in Docker, publish its port (`-p 5432:5432`) and enable the extension when you start it:

```bash
docker run -d --name pg -e POSTGRES_PASSWORD=password -p 5432:5432 postgres:16 \
  -c shared_preload_libraries=pg_stat_statements
```

Then run `CREATE EXTENSION pg_stat_statements;` in the database. For a cluster running in kind or minikube, a pod reaches your host the same way, through the host's address on the Docker network, never `localhost`.

Other connection errors:

| Message | Likely cause |
|---|---|
| `connection refused` | wrong host or port, or Postgres is not listening on that interface (`listen_addresses`) |
| `no pg_hba.conf entry` | the server does not allow this address or SSL mode; add a rule or change `sslmode` |
| `SSL is not enabled on the server` | use `sslmode=disable` locally, `sslmode=require` otherwise |
| `password authentication failed` | wrong role or password, or special characters in the password that need URL-encoding |

## `cosign verify` fails on the network

`cosign verify` first fetches the Sigstore trust root over TLS. On a host with a broken IPv6 route, or behind a proxy that blocks the Sigstore domains, it can hang or fail before it ever looks at the image, with an error about updating the TUF root or a `dial tcp` failure to an IPv6 address.

- Try from a network where IPv6 works, or temporarily prefer IPv4 for the command.
- Allow outbound HTTPS to the Sigstore hosts, and set `HTTPS_PROXY` if you use a proxy.
- If you see "no matching signatures" instead, the network is fine; see [Supply chain](supply-chain.md#troubleshooting).

## See also

- [Troubleshooting toolkit](kubernetes/troubleshooting.md): the Kubernetes order of checks.
- [Collector internals](collector-internals.md): what a scrape reads and keeps.
- [Support matrix](support-matrix.md): supported PostgreSQL versions.
- [Supply chain](supply-chain.md): verifying images and charts.
