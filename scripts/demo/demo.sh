#!/usr/bin/env bash
# One-command live demo runner (HAR-137 live smoke, HAR-129). Thin wrapper over `ghostctl demo`; all logic is Go
# (core-go/internal/demorun). Same commands as demo.ps1:
#
#   scripts/demo/demo.sh up [--llm-mode live|replay] [--no-slack] [--no-web]
#   scripts/demo/demo.sh seed [--case medtech|pioneer]
#   scripts/demo/demo.sh play
#   scripts/demo/demo.sh verify
#   scripts/demo/demo.sh status | logs <service> [-n N] | down | reset --yes
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EXT=""
case "$(uname -s 2>/dev/null)" in MINGW*|MSYS*|CYGWIN*) EXT=".exe" ;; esac
EXE="$(mktemp -u "${TMPDIR:-/tmp}/ghostctl-demo-XXXXXX")${EXT}"
trap 'rm -f "$EXE"' EXIT
(cd "$ROOT/core-go" && go build -o "$EXE" ./cmd/ghostctl)
cd "$ROOT"
"$EXE" demo "$@"
