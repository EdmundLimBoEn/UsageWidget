param(
    [string]$CrossUsageBin = $env:CROSSUSAGE_BIN,
    [string]$CrossUsageUrl = $env:CROSSUSAGE_URL,
    [string]$DataDirectory = "",
    [string]$ListenAddress = "127.0.0.1:8377"
)

$ErrorActionPreference = "Stop"
$binWasPassed = $PSBoundParameters.ContainsKey("CrossUsageBin")
$urlWasPassed = $PSBoundParameters.ContainsKey("CrossUsageUrl")
$listenWasPassed = $PSBoundParameters.ContainsKey("ListenAddress")
if ($binWasPassed -and $urlWasPassed -and
    -not [string]::IsNullOrWhiteSpace($CrossUsageBin) -and
    -not [string]::IsNullOrWhiteSpace($CrossUsageUrl)) {
    throw "Pass only one of -CrossUsageBin or -CrossUsageUrl"
}
$root = $PSScriptRoot
if (-not (Test-Path -LiteralPath (Join-Path $root "bin\usagewidgetd.exe") -PathType Leaf)) {
    $root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
}
$daemon = Join-Path $root "bin\usagewidgetd.exe"
if (-not (Test-Path -LiteralPath $daemon -PathType Leaf)) {
    throw "UsageWidget server binary not found: $daemon"
}

if ([string]::IsNullOrWhiteSpace($DataDirectory)) {
    $DataDirectory = Join-Path $env:LOCALAPPDATA "UsageWidget"
}
$configPath = Join-Path $DataDirectory "server.json"
New-Item -ItemType Directory -Force -Path $DataDirectory | Out-Null

if (Test-Path -LiteralPath $configPath -PathType Leaf) {
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    $configChanged = $false
    if ($binWasPassed) {
        $config | Add-Member -NotePropertyName CROSSUSAGE_BIN -NotePropertyValue $CrossUsageBin -Force
        $config.PSObject.Properties.Remove("CROSSUSAGE_URL")
        $configChanged = $true
    } elseif ($urlWasPassed) {
        $config | Add-Member -NotePropertyName CROSSUSAGE_URL -NotePropertyValue $CrossUsageUrl -Force
        $config.PSObject.Properties.Remove("CROSSUSAGE_BIN")
        $configChanged = $true
    }
    if ($listenWasPassed) {
        $config | Add-Member -NotePropertyName LISTEN_ADDR -NotePropertyValue $ListenAddress -Force
        $configChanged = $true
    }
    if ($configChanged) {
        $config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding UTF8
    }
} else {
    if ([string]::IsNullOrWhiteSpace($CrossUsageBin)) {
        $command = Get-Command crossusage-cli -ErrorAction SilentlyContinue
        if ($null -eq $command) {
            $command = Get-Command crossusage-cli.exe -ErrorAction SilentlyContinue
        }
        if ($null -eq $command -and [string]::IsNullOrWhiteSpace($CrossUsageUrl)) {
            throw "crossusage-cli was not found; pass -CrossUsageUrl http://127.0.0.1:6736/v1/limits or -CrossUsageBin for the CLI"
        }
        if ($null -ne $command) {
            $CrossUsageBin = $command.Source
        }
    }

    $bytes = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    $token = -join ($bytes | ForEach-Object { $_.ToString("x2") })
    $config = [ordered]@{
        USAGEWIDGET_TOKEN = $token
        DB_PATH = (Join-Path $DataDirectory "usagewidget.db")
        LISTEN_ADDR = $ListenAddress
    }
    if (-not [string]::IsNullOrWhiteSpace($CrossUsageBin)) {
        $config["CROSSUSAGE_BIN"] = $CrossUsageBin
        $resources = Join-Path (Split-Path -Parent $CrossUsageBin) "resources"
        if (Test-Path -LiteralPath (Join-Path $resources "bundled_plugins") -PathType Container) {
            $config["CROSSUSAGE_RESOURCES"] = $resources
        }
    } else {
        $config["CROSSUSAGE_URL"] = $CrossUsageUrl
    }
    $config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding UTF8
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    Write-Host "Created private configuration: $configPath"
}

foreach ($name in @("CROSSUSAGE_CMD", "CROSSUSAGE_URL", "CROSSUSAGE_BIN", "CROSSUSAGE_RESOURCES", "CODEXBAR_CMD", "CODEXBAR_URL", "CODEXBAR_BIN", "OPENUSAGE_CMD", "OPENUSAGE_URL", "OPENUSAGE_BIN")) {
    [Environment]::SetEnvironmentVariable($name, $null, "Process")
}
foreach ($property in $config.PSObject.Properties) {
    [Environment]::SetEnvironmentVariable($property.Name, [string]$property.Value, "Process")
}
if ([string]::IsNullOrWhiteSpace($env:USAGEWIDGET_TOKEN) -or $env:USAGEWIDGET_TOKEN.Length -lt 32) {
    throw "USAGEWIDGET_TOKEN must be at least 32 characters"
}
if ([string]::IsNullOrWhiteSpace($env:CROSSUSAGE_BIN) -and [string]::IsNullOrWhiteSpace($env:CROSSUSAGE_URL) -and [string]::IsNullOrWhiteSpace($env:CROSSUSAGE_CMD)) {
    throw "CROSSUSAGE_BIN or CROSSUSAGE_URL must be configured"
}
if (-not [string]::IsNullOrWhiteSpace($env:CROSSUSAGE_BIN) -and
    -not (Test-Path -LiteralPath $env:CROSSUSAGE_BIN -PathType Leaf) -and
    $null -eq (Get-Command $env:CROSSUSAGE_BIN -ErrorAction SilentlyContinue)) {
    throw "CrossUsage CLI not found: $env:CROSSUSAGE_BIN"
}

Write-Host "UsageWidget is starting at http://$($env:LISTEN_ADDR) (press Control-C to stop)."
& $daemon
exit $LASTEXITCODE
