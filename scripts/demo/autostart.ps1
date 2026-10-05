# Optional: start the demo at login (a per-user scheduled task running start-demo.ps1 hidden).
#   .\autostart.ps1 -Enable      .\autostart.ps1 -Disable      .\autostart.ps1 -Status
param([switch]$Enable, [switch]$Disable, [switch]$Status)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')
$name = 'GhostDemoStart'

if ($Enable) {
    $script = Join-Path $PSScriptRoot 'start-demo.ps1'
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "{0}"' -f $script) -WorkingDirectory (Get-RepoRoot)
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Hours 1)
    Register-ScheduledTask -TaskName $name -Action $action -Trigger $trigger -Settings $settings -Description 'Start the Ghost demo at login' -Force | Out-Null
    Write-Host "enabled: the demo starts when you log in (undo with -Disable)"
} elseif ($Disable) {
    if (Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue) { Unregister-ScheduledTask -TaskName $name -Confirm:$false }
    Write-Host 'disabled'
} else {
    $t = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
    Write-Host ($(if ($t) { "enabled (state: $($t.State))" } else { 'disabled' }))
}
