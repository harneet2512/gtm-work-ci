# One-time setup of the laptop demo (run by the lead, not the user). Idempotent: every step checks what is there.
#
#   .\scripts\demo\setup-local.ps1 [-Home D:\ghost-demo] [-CheckOnly]
#
# 1. verify the toolchain (Go, Node/npm, Python 3.12 with the worker's dependencies, Java 17/21 for Neo4j, git) and that .env has the keys
# 2. copy the demo data under <home>\data and verify the CRMArena export hash b553e9a3...
# 3. build, start the stores, migrate, freeze MedTech Advances (case 1) and EcoLite Innovations (case 2) to Event N-1 each in its own
#    database and graph through history N-1 (the only time anything is built), verify Event N is invisible, stop, and SEAL the
#    baseline under <home>\baseline (Start and Reset restore it in seconds; several minutes per case, once)
# 4. create the Desktop shortcuts "gtm_ai Demo - Start" / "gtm_ai Demo - Stop"
#
# Everything is written under GHOST_DEMO_HOME (default D:\ghost-demo), never inside the repository. Secrets are never printed:
# .env keys are checked by name only. -CheckOnly verifies the toolchain, .env and data and changes nothing.
param(
    [string]$DemoHome = $(if ($env:GHOST_DEMO_HOME) { $env:GHOST_DEMO_HOME } else { 'D:\ghost-demo' }),
    [string]$MainCheckout = 'C:\Users\Lenovo\Desktop\new_ideas+prototyope',
    [string]$B2BSource = '',
    [string]$ProSource = '',
    [string]$CassetteSource = '',
    [switch]$CheckOnly
)
$ErrorActionPreference = 'Stop'
$env:GHOST_DEMO_HOME = $DemoHome
. (Join-Path $PSScriptRoot 'local-common.ps1')

$agent = Join-Path $MainCheckout '.claude\worktrees\agent-a0192c8daa55c1d96\data'
if (-not $B2BSource) { $B2BSource = Join-Path $agent 'crmarena_b2b' }
if (-not $CassetteSource) { $CassetteSource = Join-Path $agent 'crmarena_extraction' }
if (-not $ProSource) { $ProSource = Join-Path $MainCheckout 'data\CRMArenaPro' }

$problems = New-Object System.Collections.Generic.List[string]
function Step([string]$Text) { Write-Host ""; Write-Host "[setup] $Text" }
function Need([bool]$Ok, [string]$Message) { if ($Ok) { Write-Host "ok    $Message" } else { Write-Host "FAIL  $Message"; $problems.Add($Message) } }

Step '1/4 toolchain and .env'
foreach ($tool in 'go', 'git', 'npm') { Need ([bool](Get-Command $tool -ErrorAction SilentlyContinue)) "$tool on PATH" }
$py = Get-Command python -ErrorAction SilentlyContinue
Need ($null -ne $py) 'python on PATH'
if ($py) {
    & python -c 'import sys; sys.exit(0 if sys.version_info[:2] >= (3, 12) else 1)'
    Need ($LASTEXITCODE -eq 0) 'python is 3.12 or newer'
    Push-Location (Join-Path (Get-RepoRoot) 'worker-py')
    & python -c 'import ghost_worker, fastapi, uvicorn, litellm, simple_salesforce' 2>$null
    $depsOk = ($LASTEXITCODE -eq 0)
    Pop-Location
    if (-not $depsOk) { Write-Host 'note  the worker dependencies are not installed in this python; the runner creates <home>\venv and installs worker-py (first start, a few minutes). simple-salesforce is only needed if the data must be re-fetched.' }
}
$jdks = Join-Path $HOME '.jdks'
$java = [bool]$env:JAVA_HOME -or [bool](Get-Command java -ErrorAction SilentlyContinue) -or ((Test-Path $jdks) -and (Get-ChildItem $jdks -Directory -ErrorAction SilentlyContinue))
Need $java 'Java 17 or 21 (JAVA_HOME, PATH or ~\.jdks) for Neo4j'

$envFile = Join-Path (Get-RepoRoot) '.env'
if (-not (Test-Path $envFile)) { $envFile = Join-Path $MainCheckout '.env' }
Need (Test-Path $envFile) '.env found (git-ignored; repo root, else the main checkout)'
if (Test-Path $envFile) {
    $names = Get-Content $envFile | Where-Object { $_ -match '^\s*[A-Z_][A-Z0-9_]*=\S' } | ForEach-Object { ($_ -split '=', 2)[0].Trim() }
    # The Slack tokens may also come from the process environment (processEnv wins); only names are tested, never values.
    foreach ($k in 'OPENROUTER_API_KEY', 'SLACK_CHANNEL_ID', 'SLACK_BOT_TOKEN', 'SLACK_APP_TOKEN') {
        $inEnv = [bool][Environment]::GetEnvironmentVariable($k)
        Need (($names -contains $k) -or $inEnv) "$k is set in .env or the environment (name checked, value not read)"
    }
    Write-Host 'note  GHOST_MODEL defaults to openrouter/qwen/qwen3.8-flash when .env does not set it; Slack tokens are read from the environment or .env only (names checked, values never printed).'
}
Need ((Test-Path (Join-Path $B2BSource 'Opportunity.json')) -or (Test-Path (Join-Path $DemoHome 'data\crmarena_b2b\Opportunity.json'))) 'crmarena_b2b source or copy present'
Need ((Test-Path (Join-Path $CassetteSource 'cassettes')) -or (Test-Path (Join-Path $DemoHome 'data\crmarena_extraction\cassettes'))) 'extraction cassettes source or copy present (the freeze reproduces the mined history through them)'
if ($problems.Count -gt 0) { throw ("setup stopped: " + ($problems -join '; ')) }
if ($CheckOnly) { Write-Host "`ncheck only: nothing changed."; exit 0 }

Initialize-DemoHome
Step "2/4 data under $DemoHome\data (copy, then verify the export hash)"
function Copy-Once([string]$From, [string]$To) {
    if (Test-Path $To) { Write-Host "have  $To"; return }
    if (-not (Test-Path $From)) { Write-Host "skip  $From (not present)"; return }
    Write-Host "copy  $From -> $To"
    New-Item -ItemType Directory -Force -Path (Split-Path $To) | Out-Null
    Copy-Item -Recurse -Force $From $To
}
Copy-Once $B2BSource (Join-Path $DemoHome 'data\crmarena_b2b')
Copy-Once $CassetteSource (Join-Path $DemoHome 'data\crmarena_extraction')
Copy-Once $ProSource (Join-Path $DemoHome 'data\CRMArenaPro')
& python (Join-Path (Get-RepoRoot) 'scripts\codespace\fetch_crmarena.py') --out (Join-Path $DemoHome 'data\crmarena_b2b')
if ($LASTEXITCODE -ne 0) { throw 'the crmarena_b2b copy does not match export hash b553e9a3... (see the message above)' }

Step '3/4 build, stores, migrate, freeze both cases, verify Event N is invisible, seal the baseline (several minutes per case, once)'
$exe = Build-Ghostctl
Push-Location (Get-RepoRoot)
try {
    & $exe codespace setup --data (Join-Path $DemoHome 'data\crmarena_b2b')
    if ($LASTEXITCODE -ne 0) { throw "ghostctl codespace setup failed (exit $LASTEXITCODE)" }
} finally { Pop-Location }

Step '4/4 Desktop shortcuts'
& (Join-Path $PSScriptRoot 'create-shortcuts.ps1')
Write-Host "`nsetup complete. Double-click 'gtm_ai Demo - Start' (Desktop) to run the demo."
