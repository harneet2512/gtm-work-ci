# Creates (or refreshes) the three Desktop shortcuts that run the demo without a terminal:
#   "gtm_ai Demo - Start", "gtm_ai Demo - Stop" and the presenter-only "gtm_ai Demo - Reset" (hidden PowerShell: -WindowStyle Hidden).
# Nothing else is an operator control: Play, in the control plane, is the only trigger the audience sees.
# They point at the scripts of THIS checkout; run it again from the checkout you want them to use.
param([string]$Desktop = [Environment]::GetFolderPath('Desktop'))
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'local-common.ps1')

function New-DemoShortcut([string]$Name, [string]$Script, [string]$Description) {
    $path = Join-Path $Desktop "$Name.lnk"
    $shell = New-Object -ComObject WScript.Shell
    $lnk = $shell.CreateShortcut($path)
    $lnk.TargetPath = (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe')
    $lnk.Arguments = '-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "{0}"' -f (Join-Path $PSScriptRoot $Script)
    $lnk.WorkingDirectory = Get-RepoRoot
    $lnk.WindowStyle = 7
    $lnk.Description = $Description
    $lnk.Save()
    Write-Host "shortcut: $path"
}

# The product was renamed: drop the shortcuts made under the old name so the Desktop shows one set.
Get-ChildItem -LiteralPath $Desktop -Filter 'Ghost Demo*.lnk' -ErrorAction SilentlyContinue | Remove-Item -Force
$dash = [string][char]0x2013
New-DemoShortcut "gtm_ai Demo $dash Start" 'start-demo.ps1' 'Start the gtm_ai demo and open the browser when it is ready'
New-DemoShortcut "gtm_ai Demo $dash Stop" 'stop-demo.ps1' 'Stop the gtm_ai demo (data is kept)'
New-DemoShortcut "gtm_ai Demo $dash Reset" 'reset-demo.ps1' 'Presenter only: put the gtm_ai demo back at the start for a rehearsal'
