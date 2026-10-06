# Integration smoke of the live demo runner: up, seed, play, verify, restart, down, reset against real Neo4j, Postgres, the
# Python worker (replay mode, no live LLM) and core, with Slack and the web app not started. About six minutes.
# Prerequisites: Java 17 or 21, Python with the worker's dependencies, and (for Play to finish offline) the extraction cassettes
# (GHOST_DEMO_CASSETTES or data/crmarena_extraction/cassettes). The report says what it skipped.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Push-Location (Join-Path $root 'core-go')
try {
    $env:GHOST_DEMO_SMOKE = '1'
    go test ./cmd/ghostctl -run TestDemoSmokeUpSeedPlayVerify -v -count=1 -timeout 45m
    exit $LASTEXITCODE
} finally {
    Remove-Item Env:GHOST_DEMO_SMOKE -ErrorAction SilentlyContinue
    Pop-Location
}
