#Requires -Version 5.1
<#
.SYNOPSIS
    Builds the OpenLabStats agent MSI installer.
.DESCRIPTION
    1. Compiles the Go agent binary.
    2. Runs WiX Toolset (v5+) to produce the MSI.
    Requires: Go 1.21+, WiX Toolset CLI (`wix` on PATH or .NET tool).
.PARAMETER Version
    Version number to stamp into the MSI (default: 0.1.0).
.PARAMETER OutputDir
    Directory for build artifacts (default: installer\build).
#>
param(
    [string]$Version = "0.1.5",
    [string]$OutputDir = ""
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Split-Path -Parent $ScriptDir

if ($OutputDir -eq "") {
    $OutputDir = Join-Path $ScriptDir "build"
}

Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  OpenLabStats MSI Builder" -ForegroundColor Cyan
Write-Host "  Version: $Version" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""

# -------------------------------------------------------------------
# Step 1: Build the Go agent
# -------------------------------------------------------------------
Write-Host "Building agent binary..." -ForegroundColor Yellow

$BuildOutput = Join-Path $OutputDir "bin"
if (-not (Test-Path $BuildOutput)) {
    New-Item -ItemType Directory -Path $BuildOutput -Force | Out-Null
}

$AgentExe = Join-Path $BuildOutput "openlabstats-agent.exe"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

$ldflags = "-s -w -X main.version=$Version"

# Store current location and hop to repo root to ensure go.mod is found
Push-Location $RepoRoot
$buildResult = & go build -ldflags $ldflags -o $AgentExe ".\cmd\agent\" 2>&1
Pop-Location

if ($LASTEXITCODE -ne 0) {
    Write-Error "Go build failed: $buildResult"
    exit 1
}
Write-Host "  Agent built: $AgentExe" -ForegroundColor Green

# -------------------------------------------------------------------
# Step 2: Check for WiX Toolset
# -------------------------------------------------------------------
$wixCmd = Get-Command "wix" -ErrorAction SilentlyContinue
if (-not $wixCmd) {
    # Check common install path for v6
    $commonPath = "C:\Program Files\WiX Toolset v6.0\bin\wix.exe"
    if (Test-Path $commonPath) {
        $wixPrefix = "`"$commonPath`""
        Write-Host "  WiX found at: $commonPath" -ForegroundColor Green
    } else {
        # Try .NET tool
        $wixCmd = Get-Command "dotnet" -ErrorAction SilentlyContinue
        if ($wixCmd) {
            Write-Host "  WiX CLI not found, trying 'dotnet tool run wix'..." -ForegroundColor Yellow
            $wixPrefix = "dotnet tool run wix"
        } else {
            Write-Error "WiX Toolset not found. Install with: winget install WiXToolset.WiXCLI"
            exit 1
        }
    }
} else {
    $wixPrefix = "wix"
}

# -------------------------------------------------------------------
# Step 3: Build the MSI
# -------------------------------------------------------------------
Write-Host "Building MSI package..." -ForegroundColor Yellow

$MsiOutput = Join-Path $OutputDir "openlabstats-agent-$Version.msi"
$PackageWxs = Join-Path $ScriptDir "Package.wxs"

# WiX build command with variable bindings
$wixArgs = @(
    "build"
    "-o", $MsiOutput
    "-arch", "x64"
    "-d", "BuildOutput=$BuildOutput"
    "-d", "SourceRoot=$RepoRoot"
    "-d", "Version=$Version"
    "-ext", "WixToolset.Firewall.wixext"
    "-ext", "WixToolset.Util.wixext"
    $PackageWxs
)

if ($wixPrefix -eq "dotnet tool run wix") {
    & dotnet tool run wix @wixArgs
} elseif ($wixPrefix -eq "wix") {
    & wix @wixArgs
} else {
    # It must be a full path (possibly quoted)
    # Remove outer quotes if present for & operator
    $unquoted = $wixPrefix.Trim('"' , "'")
    & $unquoted @wixArgs
}

if ($LASTEXITCODE -ne 0) {
    Write-Error "WiX build failed."
    exit 1
}

Write-Host ""
Write-Host "============================================" -ForegroundColor Green
Write-Host "  MSI built successfully!" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor Green
Write-Host ""
Write-Host "  Output:  $MsiOutput" -ForegroundColor White
Write-Host "  Version: $Version" -ForegroundColor White
Write-Host ""
Write-Host "  Install (silent):" -ForegroundColor Gray
Write-Host "    msiexec /i `"$MsiOutput`" /qn SERVERADDRESS=http://server:8080 BUILDING=Library ROOM=101" -ForegroundColor Gray
Write-Host ""
Write-Host "  Install (with logging):" -ForegroundColor Gray
Write-Host "    msiexec /i `"$MsiOutput`" /qn /l*v install.log SERVERADDRESS=http://server:8080 BUILDING=Library ROOM=101 PORT=9183" -ForegroundColor Gray
