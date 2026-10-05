#!/usr/bin/env bash
# First-time setup of the cloud demo (devcontainer postCreateCommand). Idempotent: every step checks what is already
# there, so it is safe to run again (for example after the one-time data upload fallback).
#
#   1. Python environment: the worker (+ tests) and the data fetch's dependency, in .demo/venv
#   2. web dependencies, 3. Go modules and the ghostctl binary
#   4. the CRMArena-Pro B2B snapshot (public, CC BY-NC, git-ignored, never committed), hash-verified
#   5. freeze both demo cases to Event N-1, each in its own database, snapshot them, check Event N is invisible
#
# Everything persistent lives under .demo/ and data/, on the workspace volume that survives a codespace stop/start.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
mkdir -p .demo/logs
exec > >(tee -a .demo/logs/post-create.log) 2>&1
step() { printf '\n[codespace setup] %s\n' "$*"; }

venv=".demo/venv"
step "1/5 Python environment (worker, tests, data fetch)"
if ! "$venv/bin/python" -c "import ghost_worker, fastapi, uvicorn, litellm, simple_salesforce" 2>/dev/null; then
  python3 -m venv "$venv"
  "$venv/bin/python" -m pip install --quiet --upgrade pip
  "$venv/bin/python" -m pip install --quiet -e "$root/worker-py[test]" simple-salesforce
fi

step "2/5 Web dependencies"
if [ ! -d web/node_modules ]; then
  (cd web && npm ci --no-audit --no-fund)
fi

step "3/5 Go modules and the ghostctl binary"
(cd core-go && go mod download)
bash scripts/codespace/ghostctl.sh --build-only

step "4/5 CRMArena-Pro B2B snapshot (public Salesforce AI Research dataset, CC BY-NC 4.0)"
set +e
"$venv/bin/python" scripts/codespace/fetch_crmarena.py
rc=$?
set -e
if [ "$rc" -ne 0 ]; then
  echo "[codespace setup] the snapshot is not in place (exit $rc). If the hash did not verify, upload it once from the laptop:" >&2
  echo "  gh codespace cp -r -e <laptop>/data/crmarena_b2b remote:/workspaces/gtm-work/data/   (see docs/demo/codespace.md)" >&2
  echo "then re-run: bash scripts/codespace/post-create.sh" >&2
  exit "$rc"
fi

step "5/5 Freeze both cases at Event N-1, snapshot them, verify Event N is invisible (several minutes, once)"
bash scripts/codespace/ghostctl.sh codespace setup --data data/crmarena_b2b

step "done: stop and start the codespace, or wait for the start-up to finish; the web header will read 'All systems ready'"
