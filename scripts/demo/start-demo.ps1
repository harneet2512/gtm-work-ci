# "Ghost Demo - Start": brings up Neo4j, Postgres, the worker, core, the Slack bot (Socket Mode), the web app and the
# control service, RESETS the demo to the start (both cases back at Event N-1, the bot's earlier messages deleted from
# #ghost-demo), waits until every health check passes, then opens the browser on the control plane (MedTech Advances).
# Runs hidden (the shortcut starts it with -WindowStyle Hidden); output goes to <home>\logs\start.log.
# If something fails, a message names the log and the browser opens the System page, which names the service that is down.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')
# The demo always runs with Slack on; only the record run turns it off, and it must never leak into a start.
Remove-Item Env:\GHOST_DEMO_NO_SLACK -ErrorAction SilentlyContinue

$mutex = New-Object System.Threading.Mutex($false, 'Global\GhostDemoStart')
if (-not $mutex.WaitOne(0)) { Show-DemoMessage 'Ghost Demo is already starting. Wait for the browser to open.' 'Ghost Demo' 5; exit 0 }
try {
    Write-DemoLog 'start' '--- start ---'
    $exe = Build-Ghostctl
    $code = Invoke-Ghostctl $exe @('codespace', 'up') 'start'
    $status = Get-DemoStatus $exe
    if ($code -ne 0) {
        Write-DemoLog 'start' "codespace up failed with exit $code"
        Show-DemoMessage ("Ghost Demo did not start completely. Details: {0}\logs\start.log" -f (Get-DemoHome)) 'Ghost Demo - problem' 0 48
        Start-Process "http://127.0.0.1:$(Get-WebPort)/system"
        exit $code
    }
    $url = if ($status) { Get-DemoUrl $status (Get-WebPort) } else { "http://127.0.0.1:$(Get-WebPort)/system" }
    Write-DemoLog 'start' "ready; opening $url"
    Start-Process $url
} finally {
    $mutex.ReleaseMutex()
}
