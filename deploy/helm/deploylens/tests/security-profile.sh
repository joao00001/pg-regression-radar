#!/usr/bin/env bash
# Helm render test: assert that the --security-profile flag is present in
# the args of every Deployment produced by the chart, and that the value
# matches securityProfile in values.yaml.
#
# Tests both chart modes (operator and manager) and checks that an explicit
# override value ("hardened") is propagated correctly.
#
# Requires: helm, yq (mikefarah/yq v4+)
# Usage: bash deploy/helm/deploylens/tests/security-profile.sh
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE="helm-render-test"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

# --- helper: extract --security-profile arg value from a Deployment manifest ---
security_profile_arg() {
  local manifest="$1"
  # args is a YAML list; find the element that starts with --security-profile=
  printf '%s' "$manifest" \
    | yq '. | select(.kind == "Deployment") | .spec.template.spec.containers[0].args[]
           | select(test("^--security-profile=")) | sub("^--security-profile=", "")'
}

# ---- Test 1: operator mode, default value (controlled) ----
MANIFEST_OP=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=operator \
  --set postgres.dsn="******localhost/db" \
  --set postgres.clusterName="test")

PROFILE=$(security_profile_arg "$MANIFEST_OP")
if [[ "$PROFILE" != "controlled" ]]; then
  fail "operator mode: expected --security-profile=controlled, got '${PROFILE}'"
fi
pass "operator mode: --security-profile=controlled present"

# ---- Test 2: manager mode, default value (controlled) ----
MANIFEST_MGR=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=manager \
  --set postgres.dsn="******localhost/db" \
  --set postgres.clusterName="test")

PROFILE=$(security_profile_arg "$MANIFEST_MGR")
if [[ "$PROFILE" != "controlled" ]]; then
  fail "manager mode: expected --security-profile=controlled, got '${PROFILE}'"
fi
pass "manager mode: --security-profile=controlled present"

# ---- Test 3: operator mode, explicit hardened override ----
MANIFEST_HARD=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=operator \
  --set securityProfile=hardened \
  --set postgres.dsn="******localhost/db" \
  --set postgres.clusterName="test")

PROFILE=$(security_profile_arg "$MANIFEST_HARD")
if [[ "$PROFILE" != "hardened" ]]; then
  fail "operator mode hardened: expected --security-profile=hardened, got '${PROFILE}'"
fi
pass "operator mode: --security-profile=hardened propagated correctly"

# ---- Test 4: manager mode, explicit hardened override ----
MANIFEST_MGR_HARD=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=manager \
  --set securityProfile=hardened \
  --set postgres.dsn="******localhost/db" \
  --set postgres.clusterName="test")

PROFILE=$(security_profile_arg "$MANIFEST_MGR_HARD")
if [[ "$PROFILE" != "hardened" ]]; then
  fail "manager mode hardened: expected --security-profile=hardened, got '${PROFILE}'"
fi
pass "manager mode: --security-profile=hardened propagated correctly"
