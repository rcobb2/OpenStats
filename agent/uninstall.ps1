#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Uninstalls the OpenLabStats agent service.
.PARAMETER InstallDir
    Installation directory. Default: C:\Program Files\OpenLabStats
.PARAMETER KeepData
    Keep the data directory (SQLite database) after uninstall.
.PARAMETER KeepAll
    Only remove the service; keep all files in place.
#>
param(
    [string]$InstallDir = "C:\Program Files\OpenLabStats",
    [switch]$KeepData,
    [switch]$KeepAll
)

$ErrorActionPreference = "Stop"
$ServiceName = "OpenLabStats"

Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  OpenLabStats Agent Uninstaller" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""

# -------------------------------------------------------------------
# Stop and remove the service
# -------------------------------------------------------------------
$svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.Status -eq "Running") {
        Write-Host "Stopping service..." -ForegroundColor Yellow
        Stop-Service -Name $ServiceName -Force
        Start-Sleep -Seconds 2
    }

    Write-Host "Removing service registration..." -ForegroundColor Yellow
    sc.exe delete $ServiceName | Out-Null
    Write-Host "  Service removed." -ForegroundColor Green
} else {
    Write-Host "  Service not found (already removed)." -ForegroundColor Gray
}

# -------------------------------------------------------------------
# Remove firewall rule
# -------------------------------------------------------------------
$fwRule = Get-NetFirewallRule -DisplayName "OpenLabStats Agent" -ErrorAction SilentlyContinue
if ($fwRule) {
    Write-Host "Removing firewall rule..." -ForegroundColor Yellow
    Remove-NetFirewallRule -DisplayName "OpenLabStats Agent"
    Write-Host "  Firewall rule removed." -ForegroundColor Green
}

# -------------------------------------------------------------------
# Remove files
# -------------------------------------------------------------------
if (-not $KeepAll -and (Test-Path $InstallDir)) {
    if ($KeepData) {
        Write-Host "Removing files (keeping data)..." -ForegroundColor Yellow
        # Remove everything except data/
        Get-ChildItem -Path $InstallDir -Exclude "data" | Remove-Item -Recurse -Force
    } else {
        Write-Host "Removing installation directory..." -ForegroundColor Yellow
        Remove-Item -Path $InstallDir -Recurse -Force
    }
    Write-Host "  Files removed." -ForegroundColor Green
} elseif ($KeepAll) {
    Write-Host "  Files kept in place at $InstallDir" -ForegroundColor Gray
}

Write-Host ""
Write-Host "============================================" -ForegroundColor Green
Write-Host "  Uninstall complete." -ForegroundColor Green
Write-Host "============================================" -ForegroundColor Green
