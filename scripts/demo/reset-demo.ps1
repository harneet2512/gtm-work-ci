# "Ghost Demo - Reset": PRESENTER ONLY, a hidden desktop shortcut (never part of the audience app). Starting the demo always
# resets it, so this is the same path as "Ghost Demo - Start": both cases are restored to Event N-1, the Slack bot and the web
# app restart, the bot's own earlier messages are deleted from #ghost-demo, and the browser opens on the control plane again.
# Recorded model answers are kept, so a rehearsal costs nothing. Runs hidden; output goes to <home>\logs\start.log.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')

Write-DemoLog 'start' '--- reset (presenter shortcut) ---'
& (Join-Path $PSScriptRoot 'start-demo.ps1')
exit $LASTEXITCODE
