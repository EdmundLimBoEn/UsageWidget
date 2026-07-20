param(
    [string]$CodexBarBin = $env:CODEXBAR_BIN,
    [string]$CodexBarUrl = $env:CODEXBAR_URL,
    [string]$DataDirectory = "",
    [string]$ListenAddress = "127.0.0.1:8377"
)

$ErrorActionPreference = "Stop"
$binWasPassed = $PSBoundParameters.ContainsKey("CodexBarBin")
$urlWasPassed = $PSBoundParameters.ContainsKey("CodexBarUrl")
$listenWasPassed = $PSBoundParameters.ContainsKey("ListenAddress")
if ($binWasPassed -and $urlWasPassed -and
    -not [string]::IsNullOrWhiteSpace($CodexBarBin) -and
    -not [string]::IsNullOrWhiteSpace($CodexBarUrl)) {
    throw "Pass only one of -CodexBarBin or -CodexBarUrl"
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
        $config | Add-Member -NotePropertyName CODEXBAR_BIN -NotePropertyValue $CodexBarBin -Force
        $config.PSObject.Properties.Remove("CODEXBAR_URL")
        $configChanged = $true
    } elseif ($urlWasPassed) {
        $config | Add-Member -NotePropertyName CODEXBAR_URL -NotePropertyValue $CodexBarUrl -Force
        $config.PSObject.Properties.Remove("CODEXBAR_BIN")
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
    if ([string]::IsNullOrWhiteSpace($CodexBarBin)) {
        $command = Get-Command codexbar -ErrorAction SilentlyContinue
        if ($null -eq $command) {
            $command = Get-Command CodexBarCLI -ErrorAction SilentlyContinue
        }
        if ($null -eq $command -and [string]::IsNullOrWhiteSpace($CodexBarUrl)) {
            throw "CodexBar has no official Windows CLI; pass -CodexBarUrl http://another-machine:8765/usage or -CodexBarBin for a compatible build"
        }
        if ($null -ne $command) {
            $CodexBarBin = $command.Source
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
    if (-not [string]::IsNullOrWhiteSpace($CodexBarBin)) {
        $config["CODEXBAR_BIN"] = $CodexBarBin
    } else {
        $config["CODEXBAR_URL"] = $CodexBarUrl
    }
    $config | ConvertTo-Json | Set-Content -LiteralPath $configPath -Encoding UTF8
    $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
    Write-Host "Created private configuration: $configPath"
}

foreach ($name in @("CODEXBAR_CMD", "CODEXBAR_URL", "CODEXBAR_BIN")) {
    [Environment]::SetEnvironmentVariable($name, $null, "Process")
}
foreach ($property in $config.PSObject.Properties) {
    [Environment]::SetEnvironmentVariable($property.Name, [string]$property.Value, "Process")
}
if ([string]::IsNullOrWhiteSpace($env:USAGEWIDGET_TOKEN) -or $env:USAGEWIDGET_TOKEN.Length -lt 32) {
    throw "USAGEWIDGET_TOKEN must be at least 32 characters"
}
if ([string]::IsNullOrWhiteSpace($env:CODEXBAR_BIN) -and [string]::IsNullOrWhiteSpace($env:CODEXBAR_URL)) {
    throw "CODEXBAR_BIN or CODEXBAR_URL must be configured"
}
if (-not [string]::IsNullOrWhiteSpace($env:CODEXBAR_BIN) -and
    -not (Test-Path -LiteralPath $env:CODEXBAR_BIN -PathType Leaf) -and
    $null -eq (Get-Command $env:CODEXBAR_BIN -ErrorAction SilentlyContinue)) {
    throw "CodexBar CLI not found: $env:CODEXBAR_BIN"
}

Write-Host "UsageWidget is starting at http://$($env:LISTEN_ADDR) (press Control-C to stop)."
& $daemon
exit $LASTEXITCODE
