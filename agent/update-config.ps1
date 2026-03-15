# Requires Administrator privileges
$installPath = "C:\Program Files\OpenLabStats"
$newConfig = "C:\Users\rcobb\WindowsLabStats\agent\configs\agent.yaml"
$serviceName = "OpenLabStats"

Write-Host "Updating local configuration from workspace..." -ForegroundColor Cyan
Copy-Item -Path $newConfig -Destination "$installPath\configs\agent.yaml" -Force

Write-Host "Restarting service to apply config..." -ForegroundColor Cyan
Restart-Service -Name $serviceName

Write-Host "Config updated! The agent will now pull software mappings from the server." -ForegroundColor Green
