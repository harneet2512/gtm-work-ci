# One-command live demo runner (HAR-137 live smoke, HAR-129). Thin wrapper: all the logic is Go (core-go/internal/demorun),
# because detached process trees, PID files, graceful stop and secret-safe environments are not robust in PowerShell or
# Git Bash on Windows, and Go is unit-tested.
#
#   scripts\demo\demo.ps1 up [--llm-mode live|replay] [--no-slack] [--no-web]
#   scripts\demo\demo.ps1 seed [--case medtech|pioneer]
#   scripts\demo\demo.ps1 play
#   scripts\demo\demo.ps1 verify
#   scripts\demo\demo.ps1 status | logs <service> [-n N] | down | reset --yes
#
# It builds ghostctl to a private temp exe per invocation (so two terminals, say `play` and `logs`, never fight over a
# locked file) and removes it afterwards. Secrets are read by the Go code from .env, the environment and the Slack
# credential file; nothing is printed.
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$exe = Join-Path ([IO.Path]::GetTempPath()) ('ghostctl-demo-{0}.exe' -f $PID)
Push-Location (Join-Path $root 'core-go')
try {
    go build -o $exe ./cmd/ghostctl
    if ($LASTEXITCODE -ne 0) { throw 'go build ./cmd/ghostctl failed' }
} finally { Pop-Location }
Set-Location $root
$code = 1
try {
    & $exe demo @args
    $code = $LASTEXITCODE
} finally {
    Remove-Item $exe -Force -ErrorAction SilentlyContinue
}
exit $code
