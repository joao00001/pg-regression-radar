#!/usr/bin/env bash
# Helm render test: assert that the Secret carries the consent label required
# by the reconciler's checkSecretConsent gate, and that the PostgresWatch
# spec.dsnSecretRef points to the same Secret.
#
# Requires: helm, yq (mikefarah/yq v4+)
# Usage: bash deploy/helm/deploylens/tests/consent-label.sh
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE="helm-render-test"

MANIFEST=$(helm template "$RELEASE" "$CHART_DIR" \
  --set mode=manager \
  --set manager.createDefaultWatch=true \
  --set postgres.dsn="******localhost/db" \
  --set postgres.clusterName="test-cluster")

# --- assertion 1: Secret carries the consent label ---
CONSENT=$(printf '%s' "$MANIFEST" \
  | yq '. | select(.kind == "Secret") | .metadata.labels["pg-regression-radar.io/allow-postgreswatch-access"]')

if [[ "$CONSENT" != "true" ]]; then
  echo "FAIL: Secret is missing consent label pg-regression-radar.io/allow-postgreswatch-access=true (got: '${CONSENT}')" >&2
  exit 1
fi
echo "PASS: Secret carries consent label"

# --- assertion 2: PostgresWatch.spec.dsnSecretRef.name matches the Secret name ---
SECRET_NAME=$(printf '%s' "$MANIFEST" \
  | yq '. | select(.kind == "Secret") | .metadata.name')
WATCH_REF=$(printf '%s' "$MANIFEST" \
  | yq '. | select(.kind == "PostgresWatch") | .spec.dsnSecretRef.name')

if [[ "$SECRET_NAME" != "$WATCH_REF" ]]; then
  echo "FAIL: PostgresWatch.spec.dsnSecretRef.name ('${WATCH_REF}') does not match Secret name ('${SECRET_NAME}')" >&2
  exit 1
fi
echo "PASS: PostgresWatch.spec.dsnSecretRef.name matches Secret name ('${SECRET_NAME}')"
