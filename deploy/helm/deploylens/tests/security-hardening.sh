#!/usr/bin/env bash
# Helm render/lint test: validate optional hardening templates and toggles.
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE="helm-security-hardening-test"
HARDENED_VALUES="$CHART_DIR/../../../docs/examples/values-hardened.yaml"

assert_eq() {
  local description="$1" expected="$2" actual="$3"
  if [[ "$actual" == "$expected" ]]; then
    echo "PASS: $description"
  else
    echo "FAIL: $description — expected '${expected}', got '${actual}'" >&2
    exit 1
  fi
}

count_kind() {
  local manifest="$1" kind="$2"
  printf '%s' "$manifest" | yq 'select(.kind == "'"$kind"'") | .kind' | grep -c "^${kind}$" || true
}

echo "Running helm lint..."
helm lint "$CHART_DIR" >/dev/null
echo "PASS: helm lint"

MANIFEST_DEFAULT=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=manager \
  --set manager.createDefaultWatch=true \
  --set postgres.dsn="postgres://localhost/db")

assert_eq "NetworkPolicy disabled by default" "0" "$(count_kind "$MANIFEST_DEFAULT" "NetworkPolicy")"
assert_eq "ResourceQuota disabled by default" "0" "$(count_kind "$MANIFEST_DEFAULT" "ResourceQuota")"
assert_eq "LimitRange disabled by default" "0" "$(count_kind "$MANIFEST_DEFAULT" "LimitRange")"

MANIFEST_HARDENED=$(helm template "$RELEASE" "$CHART_DIR" \
  -f "$HARDENED_VALUES")

assert_eq "Manager NetworkPolicies rendered" "2" "$(count_kind "$MANIFEST_HARDENED" "NetworkPolicy")"
assert_eq "ResourceQuota rendered when enabled" "1" "$(count_kind "$MANIFEST_HARDENED" "ResourceQuota")"
assert_eq "LimitRange rendered when enabled" "1" "$(count_kind "$MANIFEST_HARDENED" "LimitRange")"

assert_eq "Manager allow policy has Postgres egress CIDR" "true" \
  "$(printf '%s' "$MANIFEST_HARDENED" | yq '. | select(.kind == "NetworkPolicy" and .metadata.name == "'"$RELEASE"'-pg-regression-radar-manager-allow") | .spec.egress[]?.to[]?.ipBlock.cidr == "10.10.0.0/16"' | grep -q true && echo true || echo false)"

assert_eq "Manager allow policy has API server egress CIDR" "true" \
  "$(printf '%s' "$MANIFEST_HARDENED" | yq '. | select(.kind == "NetworkPolicy" and .metadata.name == "'"$RELEASE"'-pg-regression-radar-manager-allow") | .spec.egress[]?.to[]?.ipBlock.cidr == "10.20.0.0/16"' | grep -q true && echo true || echo false)"

assert_eq "Manager allow policy has alert relay egress CIDR" "true" \
  "$(printf '%s' "$MANIFEST_HARDENED" | yq '. | select(.kind == "NetworkPolicy" and .metadata.name == "'"$RELEASE"'-pg-regression-radar-manager-allow") | .spec.egress[]?.to[]?.ipBlock.cidr == "10.30.0.0/16"' | grep -q true && echo true || echo false)"

MANIFEST_OPERATOR_HARDENED=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=operator \
  --set postgres.dsn="postgres://localhost/db" \
  --set networkPolicy.enabled=true \
  --set networkPolicy.ingressCIDR="10.0.0.0/24" \
  --set networkPolicy.egress.postgresCIDRs[0]="10.10.0.0/16")

assert_eq "Operator NetworkPolicies rendered" "2" "$(count_kind "$MANIFEST_OPERATOR_HARDENED" "NetworkPolicy")"
assert_eq "Operator allow policy has no API-server CIDR egress by default" "false" \
  "$(printf '%s' "$MANIFEST_OPERATOR_HARDENED" | yq '. | select(.kind == "NetworkPolicy" and .metadata.name == "'"$RELEASE"'-pg-regression-radar-operator-allow") | .spec.egress[]?.to[]?.ipBlock.cidr == "10.20.0.0/16"' | grep -q true && echo true || echo false)"
