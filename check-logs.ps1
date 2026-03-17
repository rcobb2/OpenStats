# Check service and logs
$svc = Get-Service -Name OpenLabStats -ErrorAction SilentlyContinue
Write-Host "Service Status: $($svc.Status)"
Write-Host ""
Write-Host "Log file exists: $(Test-Path 'C:\Program Files\OpenLabStats\logs\agent.log')"
if (Test-Path 'C:\Program Files\OpenLabStats\logs\agent.log') {
    Write-Host ""
    Write-Host "=== Last 30 lines ==="
    Get-Content "C:\Program Files\OpenLabStats\logs\agent.log" -Tail 30
}
