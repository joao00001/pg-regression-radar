#!/usr/bin/env bash
# End-to-end validation of the worked example in docs/examples/otel/:
#
#   PostgreSQL (pg_stat_statements) -> OTel Collector contrib (postgresqlreceiver
#   -> signaltometrics -> deltatocumulative -> prometheusexporter) -> Prometheus
#   (recording rule) -> pg_regression_radar_query_mean_exec_time_ms
#
# It generates a steady "control" query plus a "probe" query whose latency is
# raised from ~10ms to ~60ms midway, then asserts through the Prometheus HTTP
# API that the recording rule metric carries numeric queryid labels, the query
# text, and plausible millisecond values that track the slowdown.
#
# Requirements: docker, curl, python3. Containers and the network are removed
# on exit. Only Prometheus is published, on 127.0.0.1 with a random port.
#
# Tunables (environment):
#   PG_IMAGE, OTEL_IMAGE, PROM_IMAGE   image overrides
#   BASELINE_SECS (default 100)        load time before the probe slows down
#   SLOW_SECS     (default 110)        load time after the probe slows down
#   POST_CHECK_CMD                     command run (with PROMSOURCE_SMOKE_URL
#                                      exported) after the assertions pass and
#                                      before cleanup, e.g. a promsource smoke test
#   KEEP_STACK=1                       do not tear down on exit (you must
#                                      docker rm -f the printed names yourself)
set -euo pipefail

PG_IMAGE="${PG_IMAGE:-postgres:17}"
OTEL_IMAGE="${OTEL_IMAGE:-otel/opentelemetry-collector-contrib:0.165.0}"
PROM_IMAGE="${PROM_IMAGE:-prom/prometheus:v3.5.0}"
BASELINE_SECS="${BASELINE_SECS:-100}"
SLOW_SECS="${SLOW_SECS:-110}"
BASELINE_DELAY_MS=10
SLOW_DELAY_MS=60

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EX="${ROOT}/docs/examples/otel"
SUFFIX="$$"
NET="radar-otel-net-${SUFFIX}"
PG="radar-otel-pg-${SUFFIX}"
OTEL="radar-otel-collector-${SUFFIX}"
PROM="radar-otel-prom-${SUFFIX}"
PG_PASSWORD="$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"
METRIC="pg_regression_radar_query_mean_exec_time_ms"

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  if [[ "${KEEP_STACK:-0}" == "1" ]]; then
    echo "KEEP_STACK=1: leaving ${PG} ${OTEL} ${PROM} on ${NET}"
  else
    docker rm -f "${PG}" "${OTEL}" "${PROM}" >/dev/null 2>&1 || true
    docker network rm "${NET}" >/dev/null 2>&1 || true
  fi
  if [[ ${rc} -ne 0 ]]; then
    echo "FAIL: otel example validation (exit ${rc})"
  fi
  exit "${rc}"
}
trap cleanup EXIT INT TERM

for bin in docker curl python3; do
  command -v "${bin}" >/dev/null || { echo "missing required tool: ${bin}" >&2; exit 2; }
done
for f in otel-collector.yaml prometheus.yml prometheus-rules.yaml; do
  [[ -r "${EX}/${f}" ]] || { echo "missing ${EX}/${f}" >&2; exit 2; }
done

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
psql_pg() { docker exec -i -e PGPASSWORD="${PG_PASSWORD}" "${PG}" psql -v ON_ERROR_STOP=1 -h localhost -U postgres -qAt "$@"; }

log "creating network ${NET}"
docker network create "${NET}" >/dev/null

log "starting PostgreSQL (${PG_IMAGE}) with pg_stat_statements"
docker run -d --name "${PG}" --network "${NET}" --network-alias postgres \
  -e POSTGRES_PASSWORD="${PG_PASSWORD}" \
  "${PG_IMAGE}" \
  -c shared_preload_libraries=pg_stat_statements \
  -c compute_query_id=on \
  -c pg_stat_statements.track=top >/dev/null

# The image's init phase listens on the unix socket only, so wait on TCP.
for _ in $(seq 1 60); do
  docker exec "${PG}" pg_isready -q -h localhost -U postgres && break
  sleep 1
done
docker exec "${PG}" pg_isready -q -h localhost -U postgres || { echo "postgres never became ready" >&2; exit 1; }

log "seeding schema, extension and monitoring role"
psql_pg -d postgres <<SQL
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
CREATE ROLE otel_monitor LOGIN PASSWORD '${PG_PASSWORD}';
GRANT pg_monitor TO otel_monitor;
CREATE DATABASE appdb;
SQL
psql_pg -d appdb <<SQL
CREATE TABLE bench_items AS SELECT g AS id, md5(g::text) AS payload FROM generate_series(1, 200000) g;
CREATE TABLE workload_cfg (delay_ms int NOT NULL);
INSERT INTO workload_cfg VALUES (${BASELINE_DELAY_MS});
CREATE FUNCTION workload_probe() RETURNS int LANGUAGE plpgsql AS \$\$
BEGIN
  PERFORM pg_sleep((SELECT delay_ms FROM workload_cfg) / 1000.0);
  RETURN 1;
END \$\$;
SQL
# Start from clean statistics so the first collection is not a huge backlog.
psql_pg -d postgres -c "SELECT pg_stat_statements_reset()" >/dev/null

docker exec -i "${PG}" sh -c 'cat > /tmp/probe.sql' <<'SQL'
SELECT workload_probe();
SQL
docker exec -i "${PG}" sh -c 'cat > /tmp/control.sql' <<'SQL'
\set x random(0, 96)
SELECT count(*) FROM bench_items WHERE id % 97 = :x;
SQL

log "starting OTel Collector (${OTEL_IMAGE})"
docker run -d --name "${OTEL}" --network "${NET}" --network-alias otel-collector \
  -e PG_PASSWORD="${PG_PASSWORD}" \
  -e PG_ENDPOINT="postgres:5432" \
  -v "${EX}/otel-collector.yaml:/etc/otelcol-contrib/config.yaml:ro" \
  "${OTEL_IMAGE}" >/dev/null

log "starting Prometheus (${PROM_IMAGE})"
docker run -d --name "${PROM}" --network "${NET}" \
  -p 127.0.0.1::9090 \
  -v "${EX}/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
  -v "${EX}/prometheus-rules.yaml:/etc/prometheus/prometheus-rules.yaml:ro" \
  "${PROM_IMAGE}" >/dev/null

PROM_ADDR="$(docker port "${PROM}" 9090/tcp | head -n1)"
PROM_URL="http://${PROM_ADDR}"
for _ in $(seq 1 60); do
  curl -fsS "${PROM_URL}/-/ready" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "${PROM_URL}/-/ready" >/dev/null || { echo "prometheus never became ready" >&2; exit 1; }
log "Prometheus at ${PROM_URL}"

pgbench_run() { # secs
  docker exec -e PGPASSWORD="${PG_PASSWORD}" "${PG}" \
    pgbench -n -h localhost -U postgres -c 2 -R 20 -T "$1" \
    -f /tmp/probe.sql@1 -f /tmp/control.sql@1 appdb >/dev/null
}

T0="$(date +%s)"
log "phase 1: baseline load (${BASELINE_SECS}s, probe ~${BASELINE_DELAY_MS}ms)"
pgbench_run "${BASELINE_SECS}"
T_SWITCH="$(date +%s)"
psql_pg -d appdb -c "UPDATE workload_cfg SET delay_ms = ${SLOW_DELAY_MS}" >/dev/null
log "phase 2: regression load (${SLOW_SECS}s, probe ~${SLOW_DELAY_MS}ms)"
pgbench_run "${SLOW_SECS}"
# One more collection + scrape + rule evaluation so the tail is visible.
sleep 25
T_END="$(date +%s)"

log "querying Prometheus"
RANGE_JSON="$(curl -fsS -G "${PROM_URL}/api/v1/query_range" \
  --data-urlencode "query=${METRIC}" \
  --data-urlencode "start=${T0}" --data-urlencode "end=${T_END}" \
  --data-urlencode "step=10s")"
INSTANT_JSON="$(curl -fsS -G "${PROM_URL}/api/v1/query" \
  --data-urlencode "query=count by (queryid) (max_over_time(${METRIC}[10m]))")"

RANGE_JSON="${RANGE_JSON}" INSTANT_JSON="${INSTANT_JSON}" \
T0="${T0}" T_SWITCH="${T_SWITCH}" T_END="${T_END}" \
BASELINE_DELAY_MS="${BASELINE_DELAY_MS}" SLOW_DELAY_MS="${SLOW_DELAY_MS}" \
python3 - <<'PY'
import json, math, os, re, statistics, sys

rng = json.loads(os.environ["RANGE_JSON"])
inst = json.loads(os.environ["INSTANT_JSON"])
t_switch = float(os.environ["T_SWITCH"])
base_ms = float(os.environ["BASELINE_DELAY_MS"])
slow_ms = float(os.environ["SLOW_DELAY_MS"])
RATE_WINDOW = 60  # seconds; must match the rate() range in prometheus-rules.yaml
SETTLE = 15       # extra slack for the 10s collection/scrape/eval chain

errors = []
def check(cond, msg):
    if not cond:
        errors.append(msg)

check(rng.get("status") == "success" and rng["data"]["resultType"] == "matrix", "query_range did not return a matrix")
series = rng["data"]["result"]
check(len(series) > 0, "recording rule metric has no series")

by_queryid = {}
for s in series:
    m = s["metric"]
    qid, text = m.get("queryid", ""), m.get("query", "")
    check(re.fullmatch(r"-?[0-9]+", qid) is not None, f"queryid label not an integer: {qid!r}")
    check(text != "", f"series for queryid {qid} has empty query label")
    by_queryid.setdefault(qid, []).append(s)
    for ts, v in s["values"]:
        check(v not in ("NaN", "+Inf", "-Inf"), f"non-finite value {v} for queryid {qid}")
for qid, ss in by_queryid.items():
    check(len(ss) == 1, f"queryid {qid} has {len(ss)} series; promsource expects exactly one")

inst_ids = {r["metric"].get("queryid") for r in inst["data"]["result"]}
check(set(by_queryid) <= inst_ids and len(inst_ids) > 0, "instant count-by-queryid query disagrees with query_range")

def pick(fragment):
    hits = [ss[0] for ss in by_queryid.values() if fragment in ss[0]["metric"].get("query", "")]
    check(len(hits) == 1, f"expected exactly one series whose query contains {fragment!r}, got {len(hits)}")
    return hits[0] if hits else None

probe, control = pick("workload_probe"), pick("bench_items")

def window_values(s, lo, hi):
    return [float(v) for ts, v in s["values"] if lo <= float(ts) <= hi]

if probe:
    post = window_values(probe, t_switch + RATE_WINDOW + SETTLE, 1e12)
    # Skip the first 30s of warm-up (no counter increments to rate() yet).
    pre = window_values(probe, float(os.environ["T0"]) + 30, t_switch - 1)
    check(len(pre) >= 3, f"too few baseline probe points: {len(pre)}")
    check(len(post) >= 3, f"too few slow-phase probe points: {len(post)}")
    if pre and post:
        pre_m, post_m = statistics.mean(pre), statistics.mean(post)
        print(f"probe   queryid={probe['metric']['queryid']} baseline={pre_m:.1f}ms slow={post_m:.1f}ms ({len(pre)}/{len(post)} points)")
        check(base_ms <= pre_m <= base_ms + 15, f"baseline mean {pre_m:.1f}ms not in [{base_ms}, {base_ms + 15}]")
        check(slow_ms <= post_m <= slow_ms + 25, f"slow mean {post_m:.1f}ms not in [{slow_ms}, {slow_ms + 25}]")
        check(post_m > 3 * pre_m, "slowdown not clearly visible in the metric")

if control:
    vals = [float(v) for _, v in control["values"]]
    cm = statistics.mean(vals)
    print(f"control queryid={control['metric']['queryid']} mean={cm:.1f}ms ({len(vals)} points)")
    check(0 < min(vals) and max(vals) < 500, f"control values implausible: min={min(vals)} max={max(vals)}")

print("queryids:", ", ".join(sorted(by_queryid)))
if errors:
    for qid, ss in by_queryid.items():
        pts = " ".join(f"{float(ts) - float(os.environ['T0']):.0f}:{float(v):.1f}" for ts, v in ss[0]["values"])
        print(f"  series {qid} (t-T0:value) {pts}")
    print(f"  switch at t-T0={t_switch - float(os.environ['T0']):.0f}")
    print("ASSERTION FAILURES:\n  " + "\n  ".join(errors))
    sys.exit(1)
PY

if [[ -n "${POST_CHECK_CMD:-}" ]]; then
  log "running POST_CHECK_CMD against ${PROM_URL}"
  PROMSOURCE_SMOKE_URL="${PROM_URL}" PROMSOURCE_SMOKE_T0="${T0}" PROMSOURCE_SMOKE_T_SWITCH="${T_SWITCH}" \
    bash -c "${POST_CHECK_CMD}"
fi

echo "PASS: pg_stat_statements -> OTel Collector -> Prometheus -> ${METRIC} validated"
