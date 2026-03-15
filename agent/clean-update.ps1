# Requires Administrator privileges
$serviceName = "OpenLabStats"
$installPath = "C:\Program Files\OpenLabStats"
$agentDir = "c:\Users\rcobb\WindowsLabStats\agent"

Write-Host "Building new agent binary..." -ForegroundColor Cyan
cd $agentDir
go build -o openlabstats-agent.exe ./cmd/agent/

Write-Host "Stopping OpenLabStats service..." -ForegroundColor Cyan
Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue

Write-Host "Wiping local database to clear 'SYSTEM' usage data..." -ForegroundColor Cyan
Remove-Item "$installPath\data\openlabstats.db" -Force -ErrorAction SilentlyContinue

Write-Host "Updating binary..." -ForegroundColor Cyan
Copy-Item -Path ".\openlabstats-agent.exe" -Destination "$installPath\openlabstats-agent.exe" -Force

Write-Host "Starting OpenLabStats service..." -ForegroundColor Cyan
Start-Service -Name $serviceName

Write-Host "Restarting Prometheus to clear 'Active User' gauges..." -ForegroundColor Cyan
cd "..\server"
docker compose restart openlabstats-prometheus

Write-Host ""
Write-Host "Done! The agent is now filtering for human users only, and history has been cleared." -ForegroundColor Green
