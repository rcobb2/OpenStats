#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Installs the OpenLabStats agent as a Windows service.
.DESCRIPTION
    Copies the agent binary and configuration files to Program Files,
    registers it as a Windows service, configures the firewall, and starts it.
.PARAMETER InstallDir
    Installation directory. Default: C:\Program Files\OpenLabStats
.PARAMETER Port
    Prometheus metrics port. Default: 9183
.PARAMETER ConfigOnly
    Only update config files without reinstalling the service.
#>
param(
    [string]$InstallDir = "C:\Program Files\OpenLabStats",
    [int]$Port = 9183,
    [switch]$ConfigOnly
)

$ErrorActionPreference = "Stop"
$ServiceName = "OpenLabStats"
$BinaryName = "openlabstats-agent.exe"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  OpenLabStats Agent Installer" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""

# -------------------------------------------------------------------
# Check for existing binary - build if needed
# -------------------------------------------------------------------
$SourceBinary = Join-Path $ScriptDir $BinaryName
if (-not (Test-Path $SourceBinary)) {
    # Try building from source
    $AgentExe = Join-Path $ScriptDir "agent.exe"
    if (Test-Path $AgentExe) {
        $SourceBinary = $AgentExe
    } else {
        Write-Host "Building agent from source..." -ForegroundColor Yellow
        $buildResult = & go build -o $SourceBinary ./cmd/agent/ 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-Error "Build failed: $buildResult"
            exit 1
        }
        Write-Host "  Build successful." -ForegroundColor Green
    }
}

# -------------------------------------------------------------------
# Stop existing service if running
# -------------------------------------------------------------------
$existingService = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existingService) {
    if ($existingService.Status -eq "Running") {
        Write-Host "Stopping existing service..." -ForegroundColor Yellow
        Stop-Service -Name $ServiceName -Force
        Start-Sleep -Seconds 2
    }

    if (-not $ConfigOnly) {
        Write-Host "Removing existing service..." -ForegroundColor Yellow
        $installedBinary = Join-Path $InstallDir $BinaryName
        if (Test-Path $installedBinary) {
            & $installedBinary uninstall 2>$null
        }
        # Fallback: sc.exe delete
        sc.exe delete $ServiceName 2>$null | Out-Null
        Start-Sleep -Seconds 1
    }
}

# -------------------------------------------------------------------
# Create installation directory structure
# -------------------------------------------------------------------
Write-Host "Installing to $InstallDir ..." -ForegroundColor Yellow

$dirs = @(
    $InstallDir,
    (Join-Path $InstallDir "configs"),
    (Join-Path $InstallDir "data"),
    (Join-Path $InstallDir "logs")
)
foreach ($dir in $dirs) {
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
}

# -------------------------------------------------------------------
# Copy files
# -------------------------------------------------------------------
if (-not $ConfigOnly) {
    Write-Host "  Copying agent binary..." -ForegroundColor Gray
    Copy-Item -Path $SourceBinary -Destination (Join-Path $InstallDir $BinaryName) -Force
}

Write-Host "  Copying configuration files..." -ForegroundColor Gray
Copy-Item -Path (Join-Path $ScriptDir "configs\agent.yaml") -Destination (Join-Path $InstallDir "configs\agent.yaml") -Force
Copy-Item -Path (Join-Path $ScriptDir "configs\software-map.json") -Destination (Join-Path $InstallDir "configs\software-map.json") -Force

# -------------------------------------------------------------------
# Patch config with absolute paths (relative to install dir)
# -------------------------------------------------------------------
$configPath = Join-Path $InstallDir "configs\agent.yaml"
$configContent = Get-Content $configPath -Raw
$configContent = $configContent -replace 'dbPath:\s*data/', "dbPath: $($InstallDir -replace '\\', '/')/data/"
$configContent = $configContent -replace 'filePath:\s*logs/', "filePath: $($InstallDir -replace '\\', '/')/logs/"
$configContent = $configContent -replace 'mappingFile:\s*configs/', "mappingFile: $($InstallDir -replace '\\', '/')/configs/"
if ($Port -ne 9183) {
    $configContent = $configContent -replace 'port:\s*\d+', "port: $Port"
}
Set-Content -Path $configPath -Value $configContent -NoNewline

# -------------------------------------------------------------------
# Register service
# -------------------------------------------------------------------
if (-not $ConfigOnly) {
    Write-Host "Registering Windows service..." -ForegroundColor Yellow
    $binPath = "`"$(Join-Path $InstallDir $BinaryName)`" --config `"$configPath`""

    New-Service -Name $ServiceName `
        -BinaryPathName $binPath `
        -DisplayName "OpenLabStats Agent" `
        -Description "Open-source software usage tracking agent for higher education" `
        -StartupType Automatic | Out-Null

    Write-Host "  Service registered." -ForegroundColor Green
}

# -------------------------------------------------------------------
# Configure firewall rule
# -------------------------------------------------------------------
$fwRule = Get-NetFirewallRule -DisplayName "OpenLabStats Agent" -ErrorAction SilentlyContinue
if (-not $fwRule) {
    Write-Host "Creating firewall rule for port $Port ..." -ForegroundColor Yellow
    New-NetFirewallRule -DisplayName "OpenLabStats Agent" `
        -Direction Inbound `
        -Protocol TCP `
        -LocalPort $Port `
        -Action Allow `
        -Description "Allow Prometheus to scrape OpenLabStats agent metrics" | Out-Null
    Write-Host "  Firewall rule created." -ForegroundColor Green
} else {
    Write-Host "  Firewall rule already exists." -ForegroundColor Gray
}

# -------------------------------------------------------------------
# Start the service
# -------------------------------------------------------------------
Write-Host "Starting service..." -ForegroundColor Yellow
Start-Service -Name $ServiceName
Start-Sleep -Seconds 2

$svc = Get-Service -Name $ServiceName
if ($svc.Status -eq "Running") {
    Write-Host ""
    Write-Host "============================================" -ForegroundColor Green
    Write-Host "  Installation complete!" -ForegroundColor Green
    Write-Host "============================================" -ForegroundColor Green
    Write-Host ""
    Write-Host "  Service:  $ServiceName ($($svc.Status))" -ForegroundColor White
    Write-Host "  Install:  $InstallDir" -ForegroundColor White
    Write-Host "  Config:   $configPath" -ForegroundColor White
    Write-Host "  Metrics:  http://localhost:$Port/metrics" -ForegroundColor White
    Write-Host "  Health:   http://localhost:$Port/health" -ForegroundColor White
    Write-Host ""

    # Quick health check
    try {
        $health = Invoke-WebRequest -Uri "http://localhost:$Port/health" -UseBasicParsing -TimeoutSec 5
        Write-Host "  Health check: $($health.Content)" -ForegroundColor Green
    } catch {
        Write-Host "  Health check pending (service may still be initializing)" -ForegroundColor Yellow
    }
} else {
    Write-Host "  Service status: $($svc.Status)" -ForegroundColor Red
    Write-Host "  Check logs at: $(Join-Path $InstallDir 'logs\agent.log')" -ForegroundColor Yellow
    Write-Host "  Also check: Event Viewer > Windows Logs > Application" -ForegroundColor Yellow
}
