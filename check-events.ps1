# Check Windows Event Log
$logs = Get-WinEvent -FilterHashtable @{LogName='Application';ProviderName='OpenLabStats'} -MaxEvents 20 -ErrorAction SilentlyContinue
if ($logs) {
    $logs | Format-Table TimeCreated, Message -Wrap
} else {
    Write-Host "No OpenLabStats events in Application log"
    Write-Host ""
    Write-Host "Checking all recent events..."
    Get-WinEvent -FilterHashtable @{LogName='Application';StartTime=(Get-Date).AddHours(-1)} -MaxEvents 50 -ErrorAction SilentlyContinue | Where-Object {$_.Message -like '*OpenLabStats*'} | Format-Table TimeCreated, Message -Wrap
}
