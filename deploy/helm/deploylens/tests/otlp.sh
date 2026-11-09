#!/usr/bin/env bash
# Helm render test: the OTLP deploy source and OTLP regression export values
# reach the operator's flags, the manager's CRs, and the NetworkPolicies, and
# are fully absent when unset.
#
# Requires: helm
# Usage: bash deploy/helm/deploylens/tests/otlp.sh
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE="helm-render-test"

fail() { echo "FAIL: $*" >&2; exit 1; }
expect() { # expect <label> <manifest> <fixed-string>
  printf '%s\n' "$2" | grep -qF -- "$3" || fail "$1: missing '$3'"
}
reject() { # reject <label> <manifest> <fixed-string>
  if printf '%s\n' "$2" | grep -qF -- "$3"; then fail "$1: unexpected '$3'"; fi
}

COMMON=(--set postgres.dsn="postgres://u:p@db/x" --set ingester.sourceType=otlp
  --set 'ingester.otlp.spanNames={deploy,rollout}' --set ingester.otlp.matchAttributes.cicd=release
  --set ingester.otlp.timestamp=start --set otlpExport.endpoint=http://otel:4318
  --set otlpExport.encoding=json --set otlpExport.gzip=true --set otlpExport.headers=authorization=abc
  --set networkPolicy.enabled=true --set 'networkPolicy.egress.otlpCIDRs={10.1.0.0/16}')

OP=$(helm template "$RELEASE" "$CHART_DIR" "${COMMON[@]}")
for want in '--source-type=otlp' '--otlp-span-name=deploy' '--otlp-span-name=rollout' \
  '--otlp-match-attribute=cicd=release' '--otlp-timestamp=start' '--otlp-endpoint=http://otel:4318' \
  '--otlp-encoding=json' '--otlp-gzip' 'OTEL_EXPORTER_OTLP_LOGS_HEADERS' 'otlpHeaders: "authorization=abc"' \
  'cidr: "10.1.0.0/16"' 'port: 4318'; do
  expect "operator mode" "$OP" "$want"
done
# Credentials must come from the Secret, never the argument list.
reject "operator mode" "$OP" '--otlp-headers'
echo "PASS: operator mode renders OTLP flags, env, and egress rule"

MGR=$(helm template "$RELEASE" "$CHART_DIR" --set mode=manager --set manager.createDefaultWatch=true "${COMMON[@]}")
for want in 'sourceType: "otlp"' '      - deploy' '      - rollout' 'cicd: release' 'timestamp: "start"' \
  'endpoint: "http://otel:4318"' 'encoding: "json"' 'gzip: true' 'headersSecretRef:' 'key: otlpHeaders'; do
  expect "manager mode" "$MGR" "$want"
done
echo "PASS: manager mode renders DeploySource.spec.otlp and PostgresWatch.spec.otlpExport"

DEF=$(helm template "$RELEASE" "$CHART_DIR" --set postgres.dsn="postgres://u:p@db/x" --set mode=manager --set manager.createDefaultWatch=true)
for nope in otlp otlpExport OTEL_EXPORTER; do
  reject "defaults" "$DEF" "$nope"
done
echo "PASS: defaults render no OTLP configuration"
