param([string]$CrossUsageUrl = $env:CROSSUSAGE_URL)
$ErrorActionPreference = "Stop"
$root = $PSScriptRoot
$dataDirectory = Join-Path $env:LOCALAPPDATA "UsageWidget"
$configPath = Join-Path $dataDirectory "server.json"
$launcher = Join-Path $root "start-server.ps1"
$qr = Join-Path $root "bin\usagewidget-qr.exe"
New-Item -ItemType Directory -Force -Path $dataDirectory | Out-Null

$tailscale = (Get-Command tailscale.exe -ErrorAction SilentlyContinue).Source
if ([string]::IsNullOrWhiteSpace($tailscale)) { throw "Tailscale must be installed and signed in on the target Windows machine" }
$status = (& $tailscale status --json | ConvertFrom-Json)
$dns = ([string]$status.Self.DNSName).TrimEnd('.')
if ([string]::IsNullOrWhiteSpace($dns)) { throw "Could not determine the target Windows Tailscale name" }
$publicUrl = "https://$dns/usagewidget"

if (-not (Test-Path -LiteralPath $configPath)) {
    $crossUsage = Get-Command crossusage-cli.exe, crossusage-cli -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $crossUsage -and [string]::IsNullOrWhiteSpace($CrossUsageUrl)) {
        throw "No crossusage-cli found; rerun with CROSSUSAGE_URL=http://127.0.0.1:6736/v1/limits after starting CrossUsage, or install the CLI"
    }
    $bytes = New-Object byte[] 32
    [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $token = -join ($bytes | ForEach-Object { $_.ToString("x2") })
    $config = [ordered]@{ USAGEWIDGET_TOKEN=$token; USAGEWIDGET_PUBLIC_URL=$publicUrl; DB_PATH=(Join-Path $dataDirectory "usagewidget.db"); LISTEN_ADDR="127.0.0.1:8377" }
    if ($null -ne $crossUsage) {
        $config.CROSSUSAGE_BIN = $crossUsage.Source
        $resources = Join-Path (Split-Path -Parent $crossUsage.Source) "resources"
        if (Test-Path -LiteralPath (Join-Path $resources "bundled_plugins") -PathType Container) {
            $config.CROSSUSAGE_RESOURCES = $resources
        }
    } else {
        $config.CROSSUSAGE_URL = $CrossUsageUrl
    }
    $config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding UTF8
} elseif (-not [string]::IsNullOrWhiteSpace($CrossUsageUrl)) {
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $config | Add-Member -NotePropertyName CROSSUSAGE_URL -NotePropertyValue $CrossUsageUrl -Force
    $config | Add-Member -NotePropertyName USAGEWIDGET_PUBLIC_URL -NotePropertyValue $publicUrl -Force
    $config.PSObject.Properties.Remove("CROSSUSAGE_BIN")
    $config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding UTF8
}
$config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$launcher`""
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
Register-ScheduledTask -TaskName "UsageWidget Server" -Action $action -Trigger $trigger -Force | Out-Null
Start-ScheduledTask -TaskName "UsageWidget Server"
& $tailscale serve --bg --https=443 --set-path=/usagewidget http://127.0.0.1:8377 | Out-Null
$headers = @{ Authorization = "Bearer $($config.USAGEWIDGET_TOKEN)" }
for ($attempt=0; $attempt -lt 30; $attempt++) {
    try { Invoke-RestMethod -Headers $headers -Uri "http://127.0.0.1:8377/v1/health" | Out-Null; break } catch { Start-Sleep -Seconds 1 }
}
Invoke-RestMethod -Headers $headers -Uri "http://127.0.0.1:8377/v1/health" | Out-Null
Write-Host "UsageWidget is healthy at $publicUrl"
& $qr -url $publicUrl -token $config.USAGEWIDGET_TOKEN
