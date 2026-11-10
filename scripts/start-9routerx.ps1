# Start 9RouterX in background, launch System Tray companion, and open the dashboard in browser.
param([switch]$NoBrowser)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$exe = Join-Path $repo '9routerx.exe'
$trayExe = Join-Path $repo '9routerx-tray.exe'

if (-not (Test-Path -LiteralPath $exe)) {
    throw "9routerx.exe not found at $exe"
}

# Resolve port from .env or the code default (internal/config DefaultPort)
$port = 20128
$envFile = Join-Path $repo '.env'
if (Test-Path -LiteralPath $envFile) {
    $match = Select-String -Path $envFile -Pattern '^PORT\s*=\s*(\d+)' | Select-Object -First 1
    if ($match -and $match.Matches.Groups[1].Value) {
        $port = [int]$match.Matches.Groups[1].Value
    }
}

# 1. Start gateway daemon if not already running
Write-Host "Checking 9RouterX gateway status..." -ForegroundColor Cyan
& $exe start

# 2. Launch tray companion if present and not running
if (Test-Path -LiteralPath $trayExe) {
    $trayProc = Get-Process '9routerx-tray' -ErrorAction SilentlyContinue
    if (-not $trayProc) {
        Write-Host "Launching 9RouterX tray companion..." -ForegroundColor Cyan
        Start-Process -FilePath $trayExe -WorkingDirectory $repo -WindowStyle Hidden
    }
}

# 3. Open dashboard in default browser
if (-not $NoBrowser) {
    $url = "http://127.0.0.1:$port/dashboard"
    Write-Host "Opening dashboard: $url" -ForegroundColor Green
    Start-Process $url
}
