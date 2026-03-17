# Check service status
$svc = Get-WmiObject win32_service | Where-Object {$_.Name -eq 'OpenLabStats'}
Write-Host "Service Name: $($svc.Name)"
Write-Host "Path: $($svc.PathName)"
Write-Host "Status: $($svc.State)"
Write-Host "StartType: $($svc.StartMode)"

# Check if process is running
$proc = Get-Process -Name "openlabstats-agent" -ErrorAction SilentlyContinue
if ($proc) {
    Write-Host "Process running: $($proc.Id) - $($proc.Version)"
} else {
    Write-Host "No agent process running"
}
