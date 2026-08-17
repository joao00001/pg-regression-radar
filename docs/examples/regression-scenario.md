# Example regression scenario

*Reproducible walkthrough that simulates a slower query after deployment and verifies that pg-regression-radar links the regression to that deployment event.*

## Overview

This scenario gives external users a deterministic way to validate end-to-end behavior in local/test environments. You will generate fast baseline query samples, emit a deploy event, intentionally slow the same query path, emit another deploy event, and then verify that pg-regression-radar attributes the regression to the second deployment.

The workflow is run-mode agnostic: `operator`, `manager`, and Helm all use the same database workload and webhook payloads. Only where you read results differs by mode.

> **Important timing note (E-divisive requirement):** detection needs a time series that spans multiple scrape windows, not a single burst of queries in one scrape cycle. Keep each traffic phase running for at least **3–4 scrape intervals**. For quick demos, start pg-regression-radar with `--scrape-interval 1s` and keep each phase running for ~20–30 seconds total.

## 1) Set up PostgreSQL and test data

Start a local PostgreSQL for testing:

```bash
docker run --name radar-pg -e POSTGRES_PASSWORD=pass -e POSTGRES_DB=mydb \
  -p 5432:5432 -d postgres:16 \
  -c shared_preload_libraries=pg_stat_statements \
  -c pg_stat_statements.track=all
```

Create schema, data, and an indexed query:

```bash
psql "******localhost:5432/mydb?sslmode=disable" <<'SQL'
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE TABLE IF NOT EXISTS orders (
  id BIGSERIAL PRIMARY KEY,
  customer_id BIGINT NOT NULL,
  amount_cents BIGINT NOT NULL
);
TRUNCATE orders;
INSERT INTO orders (customer_id, amount_cents)
SELECT (random() * 10000)::bigint, (random() * 100000)::bigint
FROM generate_series(1, 200000);
CREATE INDEX IF NOT EXISTS idx_orders_customer_id ON orders(customer_id);
ANALYZE orders;
SQL
```

Generate baseline (fast) query executions:

```bash
for i in $(seq 1 600); do
  psql "******localhost:5432/mydb?sslmode=disable" \
    -c "SELECT pg_sleep(0.04); SELECT count(*) FROM orders WHERE customer_id = 42;" >/dev/null
done
```

## 2) Emit baseline deploy event (v1)

```bash
curl -sS -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "app": "checkout-api",
    "namespace": "demo",
    "revision": "v1",
    "imageTag": "checkout-api:v1",
    "timestamp": "2026-08-16T12:00:00Z"
  }'
```

## 3) Trigger a simulated regression

Remove the index so the same query becomes much slower:

```bash
psql "******localhost:5432/mydb?sslmode=disable" \
  -c "DROP INDEX IF EXISTS idx_orders_customer_id;"
```

Drive post-change workload with the same query:

```bash
for i in $(seq 1 600); do
  psql "******localhost:5432/mydb?sslmode=disable" \
    -c "SELECT pg_sleep(0.04); SELECT count(*) FROM orders WHERE customer_id = 42;" >/dev/null
done
```

## 4) Emit deploy event for the regression window (v2)

```bash
curl -sS -X POST http://localhost:8080/webhook \
  -H "Content-Type: application/json" \
  -d '{
    "app": "checkout-api",
    "namespace": "demo",
    "revision": "v2",
    "imageTag": "checkout-api:v2",
    "timestamp": "2026-08-16T12:10:00Z"
  }'
```

## 5) Verify detection and deployment attribution

### Where to verify by run mode

| Mode | Where to look |
|---|---|
| `operator` | Process logs and configured alert destination output |
| `manager` | Controller logs plus `PerformanceRegression` CRs |
| Helm (`mode=operator`) | Same as `operator` |
| Helm (`mode=manager`) | Same as `manager` |

Manager CR check:

```bash
kubectl get performanceregressions -A
```

A successful result should include:

- the slowed query (query text and/or query ID),
- before/after latency evidence,
- deployment metadata matching `checkout-api` revision `v2`.

Example (illustrative only):

```text
regression detected: app=checkout-api namespace=demo revision=v2 query_id=123456
latency_before_ms=3.1 latency_after_ms=48.7 change=15.7x confidence=0.98
```

## 6) Optional cleanup

```bash
docker rm -f radar-pg
```

## See also

- [Quickstart Validation](../quickstart-validation.md) — full validation checklist across all run modes.
- [Getting Started](../getting-started.md) — startup commands for `operator`, `manager`, and Helm.
- [Alerting example](alerting-example.md) — concrete webhook/Slack alert configuration and interpretation.
