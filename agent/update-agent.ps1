# Requires Administrator privileges
$serviceName = "OpenLabStats"
$installPath = "C:\Program Files\OpenLabStats"
$newBinary = "C:\Users\rcobb\WindowsLabStats\agent\openlabstats-agent.exe"

Write-Host "Stopping OpenLabStats service..." -ForegroundColor Cyan
Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue

Write-Host "Killing any manual agent processes..." -ForegroundColor Cyan
Get-Process agent -ErrorAction SilentlyContinue | Stop-Process -Force
Get-Process openlabstats-agent -ErrorAction SilentlyContinue | Stop-Process -Force

Write-Host "Updating binary..." -ForegroundColor Cyan
if (Test-Path $newBinary) {
    Copy-Item -Path $newBinary -Destination "$installPath\openlabstats-agent.exe" -Force
} else {
    Write-Error "New binary not found at $newBinary"
    exit 1
}

Write-Host "Starting OpenLabStats service..." -ForegroundColor Cyan
Start-Service -Name $serviceName

Write-Host "Done! Agent is updated and running with Foreground Tracking." -ForegroundColor Green
