# Creates (or refreshes) the three Desktop shortcuts that run the demo without a terminal:
#   "Ghost Demo - Start", "Ghost Demo - Stop" and the presenter-only "Ghost Demo - Reset" (hidden PowerShell: -WindowStyle Hidden).
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

$dash = [string][char]0x2013
New-DemoShortcut "Ghost Demo $dash Start" 'start-demo.ps1' 'Start the Ghost demo and open the browser when it is ready'
New-DemoShortcut "Ghost Demo $dash Stop" 'stop-demo.ps1' 'Stop the Ghost demo (data is kept)'
New-DemoShortcut "Ghost Demo $dash Reset" 'reset-demo.ps1' 'Presenter only: put the Ghost demo back at the start for a rehearsal'
