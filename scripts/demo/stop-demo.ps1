# "gtm_ai Demo - Stop": stops every demo service (control service, web, Slack bot, core, worker, Postgres, Neo4j).
# All data stays under GHOST_DEMO_HOME. Runs hidden; output goes to <home>\logs\stop.log.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')

Write-DemoLog 'stop' '--- stop ---'
$exe = Build-Ghostctl
$code = Invoke-Ghostctl $exe @('codespace', 'down') 'stop'
if ($code -eq 0) {
    Show-DemoMessage 'gtm_ai Demo stopped. Your data is kept.' 'gtm_ai Demo' 4
} else {
    Show-DemoMessage ("gtm_ai Demo could not stop everything. Details: {0}\logs\stop.log" -f (Get-DemoHome)) 'gtm_ai Demo - problem' 0 48
}
exit $code
