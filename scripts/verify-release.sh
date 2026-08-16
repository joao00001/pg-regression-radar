#!/usr/bin/env bash
# Verifies supply-chain controls for a pg-regression-radar release.
#
# Usage: ./scripts/verify-release.sh <tag>   e.g. ./scripts/verify-release.sh v1.2.3
#
# Requires: cosign, jq
#
# Checks performed:
#   - cosign signature on each of the five multi-arch manifest-list images
#   - cosign signature on the Helm OCI chart
#
# Exit codes: 0 = all checks passed, 1 = one or more checks failed.
set -euo pipefail

REPO="joao00001/pg-regression-radar"
IMAGE_BASE="ghcr.io/${REPO}"
CHART_REPO="ghcr.io/joao00001/charts/pg-regression-radar"
TARGETS=(cli operator manager collector ingester)

CERT_IDENTITY_REGEXP="^https://github.com/${REPO}/.github/workflows/release.yml@refs/tags/v"
CERT_OIDC_ISSUER="https://token.actions.githubusercontent.com"

# ── Argument handling ────────────────────────────────────────────────────────

usage() {
  echo "Usage: $0 <tag>"
  echo "  tag   Release tag to verify, e.g. v1.2.3"
  echo ""
  echo "Requires cosign and jq to be installed."
  exit 0
}

[[ "${1:-}" == "--help" || "${1:-}" == "-h" ]] && usage
[[ $# -lt 1 ]] && { echo "error: tag argument required" >&2; exit 1; }

TAG="$1"
# Chart version strips the leading 'v' (Helm SemVer requirement)
CHART_VERSION="${TAG#v}"

# ── Helpers ──────────────────────────────────────────────────────────────────

PASS=0
FAIL=0

check_pass() { echo "  ✓  $*"; ((PASS++)) || true; }
check_fail() { echo "  ✗  $*" >&2; ((FAIL++)) || true; }

verify_cosign() {
  local ref="$1"
  local label="$2"
  if cosign verify "$ref" \
       --certificate-identity-regexp "$CERT_IDENTITY_REGEXP" \
       --certificate-oidc-issuer "$CERT_OIDC_ISSUER" \
       --output text \
       > /dev/null 2>&1; then
    check_pass "$label: signature verified"
  else
    check_fail "$label: signature verification FAILED (cosign verify returned non-zero)"
  fi
}

# ── Preflight ────────────────────────────────────────────────────────────────

for cmd in cosign jq; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "error: '$cmd' not found in PATH — install it before running this script." >&2
    exit 1
  fi
done

echo ""
echo "Verifying release: ${TAG}"
echo "  Image base : ${IMAGE_BASE}"
echo "  Chart      : ${CHART_REPO}:${CHART_VERSION}"
echo "  Identity   : ${CERT_IDENTITY_REGEXP}"
echo "  OIDC issuer: ${CERT_OIDC_ISSUER}"
echo ""

# ── Image signatures ─────────────────────────────────────────────────────────

echo "── Container images ────────────────────────────────────────────────────"
for target in "${TARGETS[@]}"; do
  ref="${IMAGE_BASE}/${target}:${TAG}"
  verify_cosign "$ref" "${target}:${TAG}"
done
echo ""

# ── Helm chart signature ─────────────────────────────────────────────────────

echo "── Helm chart ──────────────────────────────────────────────────────────"
verify_cosign "${CHART_REPO}:${CHART_VERSION}" "chart:${CHART_VERSION}"
echo ""

# ── Summary ──────────────────────────────────────────────────────────────────

TOTAL=$((PASS + FAIL))
echo "── Summary ─────────────────────────────────────────────────────────────"
echo "  ${PASS}/${TOTAL} checks passed"
if [[ $FAIL -gt 0 ]]; then
  echo ""
  echo "One or more checks failed. See https://joao00001.github.io/pg-regression-radar/supply-chain/"
  echo "for troubleshooting guidance."
  exit 1
fi
echo "All checks passed."
