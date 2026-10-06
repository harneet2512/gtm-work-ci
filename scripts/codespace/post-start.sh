#!/usr/bin/env bash
# Every codespace start (devcontainer postStartCommand): bring the whole demo up in the background and return at once,
# so starting the codespace is never blocked by it. The web header (forwarded port 3000) reads "Starting <service>..."
# and then "All systems ready"; nothing needs a terminal. One boot at a time (flock); idempotent.
set -uo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
mkdir -p .demo/logs
log=".demo/logs/codespace-up.log"
printf '\n--- codespace start %s ---\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$log"
nohup flock -n .demo/boot.lock bash scripts/codespace/ghostctl.sh codespace up >> "$log" 2>&1 &
disown || true
echo "[codespace start] bringing the demo up in the background (log: .demo/logs/codespace-up.log)"
