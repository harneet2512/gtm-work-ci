# Shared helpers of the laptop demo scripts (dot-sourced by setup-local, start-demo, stop-demo, ...).
# Everything the demo writes lives under GHOST_DEMO_HOME (default D:\ghost-demo), never inside the repository.
# Secrets are read by ghostctl from the git-ignored repo .env and the Slack token logic; nothing here prints one.

$ErrorActionPreference = 'Stop'

$script:RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$script:DemoHome = if ($env:GHOST_DEMO_HOME) { $env:GHOST_DEMO_HOME } else { 'D:\ghost-demo' }
$env:GHOST_DEMO_HOME = $script:DemoHome

function Get-DemoHome { $script:DemoHome }
function Get-RepoRoot { $script:RepoRoot }

function Initialize-DemoHome {
    foreach ($d in 'data', 'logs', 'cache', 'cli') {
        New-Item -ItemType Directory -Force -Path (Join-Path $script:DemoHome $d) | Out-Null
    }
}

function Write-DemoLog([string]$Name, [string]$Message) {
    Initialize-DemoHome
    $line = '{0:u} {1}' -f (Get-Date), $Message
    Add-Content -Path (Join-Path $script:DemoHome "logs\$Name.log") -Value $line -Encoding UTF8
}

# Builds ghostctl into <home>\cli (incremental: seconds when nothing changed) and returns its path.
function Build-Ghostctl {
    Initialize-DemoHome
    $exe = Join-Path $script:DemoHome 'cli\ghostctl.exe'
    Push-Location (Join-Path $script:RepoRoot 'core-go')
    try {
        & go build -o $exe ./cmd/ghostctl
        if ($LASTEXITCODE -ne 0) { throw "go build ghostctl failed (exit $LASTEXITCODE)" }
    } finally { Pop-Location }
    return $exe
}

# Runs ghostctl from the repo root, appending its output to <home>\logs\<log>.log. Returns the exit code.
function Invoke-Ghostctl([string]$Exe, [string[]]$Arguments, [string]$Log) {
    Push-Location $script:RepoRoot
    try {
        $out = Join-Path $script:DemoHome "logs\$Log.log"
        # Not -Wait: Windows PowerShell 5.1 then waits for every descendant of the process, and `codespace up` leaves the services
        # (control, web, core, worker, stores) running as its descendants, so the script would never return (the record hang).
        # WaitForExit waits for ghostctl itself only. Reading .Handle first keeps ExitCode readable after the process is gone.
        $p = Start-Process -FilePath $Exe -ArgumentList $Arguments -WorkingDirectory $script:RepoRoot `
            -RedirectStandardOutput "$out.stdout" -RedirectStandardError "$out.stderr" -NoNewWindow -PassThru
        $null = $p.Handle
        $p.WaitForExit()
        foreach ($s in "$out.stdout", "$out.stderr") {
            if (Test-Path $s) { Get-Content $s | Add-Content -Path $out -Encoding UTF8; Remove-Item $s -Force }
        }
        return $p.ExitCode
    } finally { Pop-Location }
}

# Shows a message without needing the console: auto-closes after $Seconds (0 = waits for OK).
function Show-DemoMessage([string]$Text, [string]$Title = 'gtm_ai Demo', [int]$Seconds = 0, [int]$Icon = 64) {
    try {
        $null = (New-Object -ComObject WScript.Shell).Popup($Text, $Seconds, $Title, $Icon)
    } catch { Write-DemoLog 'local' "message: $Text" }
}

function Get-DemoStatus([string]$Exe) {
    Push-Location $script:RepoRoot
    try {
        $json = & $Exe codespace status 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $json) { return $null }
        return ($json | ConvertFrom-Json)
    } catch { return $null } finally { Pop-Location }
}

# The page the browser opens: the control plane on the active case's frozen replay (demo mode on). Without an active frozen
# case it opens the System page, which shows what is not ready.
function Get-DemoUrl($Status, [int]$WebPort = 3000) {
    $base = "http://127.0.0.1:$WebPort"
    $case = $Status.cases | Where-Object { $_.active -and $_.manifest_id } | Select-Object -First 1
    if ($case) { return "$base/control?manifest=$($case.manifest_id)&demo=1" }
    return "$base/system"
}

function Get-WebPort { if ($env:GHOST_DEMO_PORT_WEB) { [int]$env:GHOST_DEMO_PORT_WEB } else { 3000 } }
