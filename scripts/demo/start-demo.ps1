# "gtm_ai Demo - Start": brings up Neo4j, Postgres, the worker, core, the Slack bot (Socket Mode), the web app and the
# control service, RESETS the demo to the start by restoring the sealed baseline (both cases back at Event N-1 from a file copy in
# seconds; nothing is rebuilt or recomputed; the bot's earlier messages are deleted from #gtm-ai-demo), waits until every health check
# passes, then opens the browser on the control plane (MedTech Advances). GHOST_DEMO_START=resume keeps the live state instead.
# If the baseline is missing or damaged it stops and says to run the one-time setup; it never rebuilds.
# Runs hidden (the shortcut starts it with -WindowStyle Hidden); output goes to <home>\logs\start.log.
# If something fails, a message names the log and the browser opens the System page, which names the service that is down.
param([string]$Mode = '')   # 'restore' forces a restore whatever GHOST_DEMO_START says (the Reset shortcut passes it)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')
# The demo always runs with Slack on; only the record run turns it off, and it must never leak into a start.
Remove-Item Env:\GHOST_DEMO_NO_SLACK -ErrorAction SilentlyContinue

$mutex = New-Object System.Threading.Mutex($false, 'Global\GhostDemoStart')
if (-not $mutex.WaitOne(0)) { Show-DemoMessage 'gtm_ai Demo is already starting. Wait for the browser to open.' 'gtm_ai Demo' 5; exit 0 }
try {
    Write-DemoLog 'start' '--- start ---'
    $exe = Build-Ghostctl
    $upArgs = @('codespace', 'up')
    if ($Mode) { $upArgs += @('--mode', $Mode) }
    $code = Invoke-Ghostctl $exe $upArgs 'start'
    $status = Get-DemoStatus $exe
    if ($code -ne 0) {
        Write-DemoLog 'start' "codespace up failed with exit $code"
        Show-DemoMessage ("gtm_ai Demo did not start completely. Details: {0}\logs\start.log" -f (Get-DemoHome)) 'gtm_ai Demo - problem' 0 48
        exit $code  # a failed start opens nothing in front of the audience; the message names start.log
    }
    $url = if ($status) { Get-DemoUrl $status (Get-WebPort) } else { "http://127.0.0.1:$(Get-WebPort)/system" }
    Write-DemoLog 'start' "ready; opening $url"
    Start-Process $url
} finally {
    $mutex.ReleaseMutex()
}
