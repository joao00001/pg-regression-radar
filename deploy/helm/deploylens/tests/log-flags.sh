#!/usr/bin/env bash
# Helm render test: manager.logLevel and manager.logFormat reach the manager as
# --log-level and --log-format, and render nothing when they are left empty.
#
# Requires: helm
# Usage: bash deploy/helm/deploylens/tests/log-flags.sh
set -euo pipefail

CHART_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RELEASE="helm-render-test"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

render() {
  helm template "$RELEASE" "$CHART_DIR" --set mode=manager "$@"
}

DEFAULT=$(render)
if grep -q -- '--log-level\|--log-format' <<<"$DEFAULT"; then
  fail "default values must not render --log-level or --log-format"
fi
pass "default values render no log flags"

SET=$(render --set manager.logLevel=debug --set manager.logFormat=text)
grep -q -- '- "--log-level=debug"' <<<"$SET" || fail "manager.logLevel=debug did not render --log-level=debug"
grep -q -- '- "--log-format=text"' <<<"$SET" || fail "manager.logFormat=text did not render --log-format=text"
pass "manager.logLevel and manager.logFormat render the flags"

LEVEL_ONLY=$(render --set manager.logLevel=warn)
grep -q -- '- "--log-level=warn"' <<<"$LEVEL_ONLY" || fail "manager.logLevel=warn did not render"
if grep -q -- '--log-format' <<<"$LEVEL_ONLY"; then
  fail "--log-format rendered although manager.logFormat is empty"
fi
pass "each flag renders on its own"
