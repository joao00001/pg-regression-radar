#!/usr/bin/env bash
# Helm render test: assert that every Deployment produced by the chart carries
# all required security-context fields from the latest hardening cycle:
#
#   Pod-level (podSecurityContext):
#     runAsNonRoot: true
#     runAsUser:    65532
#     runAsGroup:   65532
#     seccompProfile.type: RuntimeDefault
#
#   Container-level (securityContext):
#     allowPrivilegeEscalation: false
#     readOnlyRootFilesystem:   true
#     capabilities.drop:        [ALL]
#
# The test is run for both "operator" and "manager" chart modes so that a
# values.yaml or template regression is caught regardless of which mode a
# user deploys.
#
# Requires: helm, yq (mikefarah/yq v4+)
# Usage: bash deploy/helm/deploylens/tests/security-context.sh
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE="helm-render-test"

PASS=0
FAIL=0

assert_eq() {
  local description="$1" expected="$2" actual="$3"
  if [[ "$actual" == "$expected" ]]; then
    echo "PASS: $description"
    (( PASS++ )) || true
  else
    echo "FAIL: $description — expected '${expected}', got '${actual}'" >&2
    (( FAIL++ )) || true
  fi
}

check_deployment_security_context() {
  local mode="$1"
  local manifest="$2"
  local deployment_name="$3"

  # Extract the single Deployment (or the named one when there are multiple).
  local dep
  dep=$(printf '%s' "$manifest" \
    | yq '. | select(.kind == "Deployment" and .metadata.name == "'"$deployment_name"'")')

  if [[ -z "$dep" ]]; then
    echo "FAIL: no Deployment named '${deployment_name}' found in mode=${mode} output" >&2
    (( FAIL++ )) || true
    return
  fi

  # ── Pod-level security context ──────────────────────────────────────────────
  local run_as_non_root run_as_user run_as_group seccomp_type
  run_as_non_root=$(printf '%s' "$dep" | yq '.spec.template.spec.securityContext.runAsNonRoot')
  run_as_user=$(printf '%s' "$dep"     | yq '.spec.template.spec.securityContext.runAsUser')
  run_as_group=$(printf '%s' "$dep"    | yq '.spec.template.spec.securityContext.runAsGroup')
  seccomp_type=$(printf '%s' "$dep"    | yq '.spec.template.spec.securityContext.seccompProfile.type')

  assert_eq "[${mode}/${deployment_name}] podSecurityContext.runAsNonRoot"    "true"           "$run_as_non_root"
  assert_eq "[${mode}/${deployment_name}] podSecurityContext.runAsUser"       "65532"          "$run_as_user"
  assert_eq "[${mode}/${deployment_name}] podSecurityContext.runAsGroup"      "65532"          "$run_as_group"
  assert_eq "[${mode}/${deployment_name}] podSecurityContext.seccompProfile"  "RuntimeDefault" "$seccomp_type"

  # ── Container-level security context (all containers) ───────────────────────
  local container_count
  container_count=$(printf '%s' "$dep" | yq '.spec.template.spec.containers | length')

  local i
  for (( i=0; i<container_count; i++ )); do
    local container_name ape rofs caps_drop
    container_name=$(printf '%s' "$dep" | yq ".spec.template.spec.containers[${i}].name")
    ape=$(printf '%s' "$dep"  | yq ".spec.template.spec.containers[${i}].securityContext.allowPrivilegeEscalation")
    rofs=$(printf '%s' "$dep" | yq ".spec.template.spec.containers[${i}].securityContext.readOnlyRootFilesystem")
    # Assert that the drop list is non-empty and contains "ALL".
    caps_drop_contains_all=$(printf '%s' "$dep" \
      | yq ".spec.template.spec.containers[${i}].securityContext.capabilities.drop | contains([\"ALL\"])")

    assert_eq "[${mode}/${deployment_name}/${container_name}] securityContext.allowPrivilegeEscalation"    "false" "$ape"
    assert_eq "[${mode}/${deployment_name}/${container_name}] securityContext.readOnlyRootFilesystem"      "true"  "$rofs"
    assert_eq "[${mode}/${deployment_name}/${container_name}] securityContext.capabilities.drop contains ALL" "true" "$caps_drop_contains_all"
  done

  # ── No volumeMounts writing to arbitrary temp paths ─────────────────────────
  # Containers that run with readOnlyRootFilesystem must not mount arbitrary
  # writable paths under /tmp or /var/tmp unless those mounts are explicitly
  # tested.  We assert that no such mounts exist in the default values.
  local tmp_mounts
  tmp_mounts=$(printf '%s' "$dep" \
    | yq '[.spec.template.spec.containers[].volumeMounts[]? | select(.mountPath | test("^/tmp|^/var/tmp")) | .mountPath] | join(",")' \
    2>/dev/null || echo "")

  if [[ -n "$tmp_mounts" ]]; then
    echo "FAIL: [${mode}/${deployment_name}] unexpected volumeMounts to temp paths: '${tmp_mounts}'" >&2
    (( FAIL++ )) || true
  else
    echo "PASS: [${mode}/${deployment_name}] no volumeMounts to arbitrary temp paths"
    (( PASS++ )) || true
  fi
}

# ── Mode: operator ─────────────────────────────────────────────────────────────
MANIFEST_OPERATOR=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=operator \
  --set postgres.dsn="postgres://localhost/db")

check_deployment_security_context "operator" "$MANIFEST_OPERATOR" "${RELEASE}-pg-regression-radar"

# ── Mode: manager ──────────────────────────────────────────────────────────────
MANIFEST_MANAGER=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=manager \
  --set manager.createDefaultWatch=true \
  --set postgres.dsn="postgres://localhost/db" \
  --set postgres.clusterName="test-cluster")

check_deployment_security_context "manager" "$MANIFEST_MANAGER" "${RELEASE}-pg-regression-radar-manager"

# ── Summary ────────────────────────────────────────────────────────────────────
echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
if (( FAIL > 0 )); then
  exit 1
fi
