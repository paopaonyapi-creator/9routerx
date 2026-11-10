# Stop 9RouterX background gateway and tray companion.
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $repo '9routerx.exe'

if (Test-Path -LiteralPath $exe) {
    Write-Host "Stopping 9RouterX gateway..." -ForegroundColor Yellow
    & $exe stop
}

# Stop tray companion if running
$trayProc = Get-Process '9routerx-tray' -ErrorAction SilentlyContinue
if ($trayProc) {
    Write-Host "Closing 9RouterX tray companion..." -ForegroundColor Yellow
    $trayProc | Stop-Process -Force
}

Write-Host "9RouterX has been stopped." -ForegroundColor Green
