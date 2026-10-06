#!/usr/bin/env bash
# Build ghostctl into .demo/bin (incremental, seconds when nothing changed) and run it: the one entry the codespace
# scripts use. `--build-only` builds and exits. Nothing here needs a terminal at demo time.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
bin="$root/.demo/bin/ghostctl"
mkdir -p "$root/.demo/bin"
(cd "$root/core-go" && go build -o "$bin" ./cmd/ghostctl)
if [ "${1:-}" = "--build-only" ]; then
  exit 0
fi
cd "$root"
exec "$bin" "$@"
