# Requires Administrator privileges
$serviceName = "OpenLabStats"
$installPath = "C:\Program Files\OpenLabStats"
$newBinary = "C:\Users\rcobb\WindowsLabStats\agent\openlabstats-agent.exe"
$newConfig = "C:\Users\rcobb\WindowsLabStats\agent\configs\agent.yaml"

Write-Host "Stopping OpenLabStats service..." -ForegroundColor Cyan
Stop-Service -Name $serviceName -Force -ErrorAction SilentlyContinue

Write-Host "Updating binary and configuration..." -ForegroundColor Cyan
Copy-Item -Path $newBinary -Destination "$installPath\openlabstats-agent.exe" -Force
Copy-Item -Path $newConfig -Destination "$installPath\configs\agent.yaml" -Force

Write-Host "Starting OpenLabStats service..." -ForegroundColor Cyan
Start-Service -Name $serviceName

Write-Host "Done! Agent is updated with expanded exclusions and 'Double-Counting' fixes." -ForegroundColor Green
