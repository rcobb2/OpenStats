# Check log file details
$logPath = "C:\Program Files\OpenLabStats\logs\agent.log"
if (Test-Path $logPath) {
    $file = Get-Item $logPath
    Write-Host "File: $logPath"
    Write-Host "Size: $($file.Length) bytes"
    Write-Host "LastWriteTime: $($file.LastWriteTime)"
    Write-Host ""
    Write-Host "Content:"
    Get-Content $logPath
} else {
    Write-Host "Log file not found"
}
