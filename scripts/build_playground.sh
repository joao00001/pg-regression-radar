#!/usr/bin/env bash
# Builds the docs playground's WebAssembly module and the matching Go runtime
# shim into docs/assets/playground/. Both files are generated, git-ignored, and
# must come from the same Go toolchain, which is why they are built together.
# It also captures the CLI's real --help output (cli-help.json) for the page's
# "Command line" terminal, so what the docs show cannot drift from the flags.
#
# Usage: scripts/build_playground.sh
#
# To preview the page locally afterwards (the playground is an unversioned
# page, see hooks.py): mkdocs serve -f mkdocs-unversioned.yml
set -euo pipefail

cd "$(dirname "$0")/.."
out=docs/assets/playground
mkdir -p "$out"

GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$out/pgrr.wasm" ./cmd/playground

# Ship the module gzipped: GitHub Pages does not document compressing
# application/wasm, and the page decompresses it itself either way.
gzip -9nc "$out/pgrr.wasm" > "$out/pgrr.wasm.gz"
rm "$out/pgrr.wasm"

# wasm_exec.js lives in lib/wasm since Go 1.24 (misc/wasm before that).
goroot="$(go env GOROOT)"
shim="$goroot/lib/wasm/wasm_exec.js"
[ -f "$shim" ] || shim="$goroot/misc/wasm/wasm_exec.js"
install -m 644 "$shim" "$out/wasm_exec.js"

# The page's "Command line" terminal replays the binary's own help text.
bin="$(mktemp -d)/pg-regression-radar"
go build -trimpath -o "$bin" ./cmd/pg-regression-radar
python3 - "$bin" "$out/cli-help.json" <<'PY'
import json, subprocess, sys

binary, dest = sys.argv[1:3]
runs = [("help", ["help"])] + [(c, [c, "--help"]) for c in ("operator", "manager", "collector", "ingester")]
commands = []
for name, argv in runs:
    done = subprocess.run([binary, *argv], capture_output=True, text=True)
    commands.append({"id": name, "argv": " ".join(argv), "text": done.stdout or done.stderr})
with open(dest, "w") as f:
    json.dump({"binary": "pg-regression-radar", "commands": commands}, f, indent=1)
PY

ls -l "$out"
