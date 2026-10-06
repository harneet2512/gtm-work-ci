#!/usr/bin/env bash
# Asserts the devcontainer provides the toolchain the demo needs, matching what the repository pins: Go as in
# core-go/go.mod, Python 3.12, Node >= 22.12 (web/package.json engines), Java 17 or 21 for the pinned Neo4j, gh and flock.
# CI runs this inside the built devcontainer image; it needs no network and starts nothing.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fail=0
check() { if ! "$@"; then fail=1; fi; }
ok() { printf 'ok   %s\n' "$*"; }
bad() { printf 'FAIL %s\n' "$*" >&2; return 1; }

want_go="$(awk '/^go /{print $2; exit}' "$root/core-go/go.mod")"
got_go="$(go version | awk '{print $3}' | sed 's/^go//')"
if [ "$got_go" = "$want_go" ]; then ok "go $got_go"; else check bad "go $got_go, core-go/go.mod wants $want_go"; fi

py="$(python3 --version 2>&1)"
case "$py" in "Python 3.12."*) ok "$py" ;; *) check bad "$py, want 3.12" ;; esac

node_v="$(node --version | sed 's/^v//')"
node_major="${node_v%%.*}"
node_minor="$(echo "$node_v" | cut -d. -f2)"
if [ "$node_major" -gt 22 ] || { [ "$node_major" -eq 22 ] && [ "$node_minor" -ge 12 ]; }; then ok "node $node_v"; else check bad "node $node_v, want >= 22.12"; fi

java_major="$( (java -version 2>&1 || true) | awk -F\" '/version/{split($2,a,"."); print a[1]; exit}')"
case "$java_major" in 17|21) ok "java $java_major" ;; *) check bad "java ${java_major:-missing}, want 17 or 21" ;; esac

for tool in gh flock git npm; do
  if command -v "$tool" >/dev/null 2>&1; then ok "$tool"; else check bad "$tool is missing"; fi
done
exit "$fail"
