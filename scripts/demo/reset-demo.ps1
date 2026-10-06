# "gtm_ai Demo - Reset": PRESENTER ONLY, a hidden desktop shortcut (never part of the audience app). Starting the demo always
# resets it, so this is the same path as "gtm_ai Demo - Start": the sealed baseline is restored (both cases back at Event N-1, a file copy, seconds, nothing rebuilt), the Slack bot and the web
# app restart, the bot's own earlier messages are deleted from #gtm-ai-demo, and the browser opens on the control plane again.
# Recorded model answers are kept, so a rehearsal costs nothing. Runs hidden; output goes to <home>\logs\start.log.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')

Write-DemoLog 'start' '--- reset (presenter shortcut) ---'
& (Join-Path $PSScriptRoot 'start-demo.ps1') -Mode restore   # Reset always restores, even if .env says GHOST_DEMO_START=resume
exit $LASTEXITCODE
