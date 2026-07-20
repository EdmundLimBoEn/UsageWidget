const PUBLIC_URL = "https://usagewidget.edmundlim.systems";

const unixInstallScript = String.raw`#!/usr/bin/env bash
set -euo pipefail

REPO="\${USAGEWIDGET_REPO:-EdmundLimBoEn/UsageWidget}"
VERSION="\${USAGEWIDGET_VERSION:-latest}"
CONFIG_FILE="\${USAGEWIDGET_CONFIG:-\${XDG_CONFIG_HOME:-$HOME/.config}/usagewidget/env}"

say() { printf 'usagewidget setup: %s\n' "$*"; }
die() { printf 'usagewidget setup: %s\n' "$*" >&2; exit 1; }
prompt() {
  [[ -r /dev/tty && -w /dev/tty ]] || die "run this installer from an interactive terminal"
  printf '%s' "$1" >/dev/tty
  IFS= read -r "$2" </dev/tty
}
for tool in ssh curl sed uname mktemp; do command -v "$tool" >/dev/null 2>&1 || die "required local tool not found: $tool"; done

default_target="\${USAGEWIDGET_DEPLOY_HOST:-}"
if [[ -z $default_target && -r $CONFIG_FILE ]]; then
  default_target="$(sed -n 's/^USAGEWIDGET_DEPLOY_HOST=//p' "$CONFIG_FILE" | tail -1)"
fi
default_ssh_user=root
default_server=""
if [[ $default_target == *@* ]]; then
  default_ssh_user="\${default_target%%@*}"
  default_server="\${default_target#*@}"
fi

prompt "SSH user [$default_ssh_user]: " ssh_user
ssh_user="\${ssh_user:-$default_ssh_user}"
server_prompt="Target IP or hostname"
[[ -n $default_server ]] && server_prompt+=" [$default_server]"
prompt "$server_prompt: " server
server="\${server:-$default_server}"
[[ $ssh_user =~ ^[A-Za-z_][A-Za-z0-9._-]*$ ]] || die "SSH user contains unsupported characters"
[[ $server =~ ^[A-Za-z0-9][A-Za-z0-9.:-]*$ ]] || die "enter a plain IP address or hostname"
TARGET="$ssh_user@$server"

LOCAL_WORK="$(mktemp -d /tmp/usagewidget-bootstrap.XXXXXX)"
SSH_OPTIONS=(-o "ControlMaster=auto" -o "ControlPersist=120" -o "ControlPath=$LOCAL_WORK/ssh-%C")
cleanup() { rm -rf -- "$LOCAL_WORK"; }
trap cleanup EXIT

say "connecting to $TARGET"
REMOTE_OS="$(ssh -n "\${SSH_OPTIONS[@]}" "$TARGET" uname -s 2>/dev/null || true)"
case "$REMOTE_OS" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *)
    win_arch="$(ssh -n "\${SSH_OPTIONS[@]}" "$TARGET" 'powershell.exe -NoProfile -Command "[Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()"' 2>/dev/null | tr -d '\r' || true)"
    [[ -n $win_arch ]] || die "could not detect Linux, macOS, or Windows on $TARGET"
    OS=windows
    ;;
esac

if [[ $OS == windows ]]; then
  case "$win_arch" in X64) ARCH=amd64 ;; Arm64) ARCH=arm64 ;; *) die "Windows target must be amd64 or arm64" ;; esac
  REMOTE_SUDO=""
else
  remote_uid="$(ssh -n "\${SSH_OPTIONS[@]}" "$TARGET" id -u)" || die "could not connect to $TARGET"
  if [[ $remote_uid == 0 || $OS == darwin ]]; then REMOTE_SUDO=""; else REMOTE_SUDO=sudo; ssh "\${SSH_OPTIONS[@]}" -tt "$TARGET" sudo -v </dev/tty; fi
  remote_arch="$(ssh -n "\${SSH_OPTIONS[@]}" "$TARGET" uname -m)"
  case "$remote_arch" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) die "target must be amd64 or arm64" ;; esac
fi

collector_user=""
CODEXBAR_SOURCE_URL=""
if [[ $OS == linux ]]; then
  detected_collector="$(ssh -n "\${SSH_OPTIONS[@]}" "$TARGET" "getent passwd | awk -F: '\$3>=1000 && \$3<65534 && \$7 !~ /(nologin|false)\$/ {print \$1; exit}'")"
  collector_prompt="Collector user"; [[ -n $detected_collector ]] && collector_prompt+=" [$detected_collector]"
  prompt "$collector_prompt: " collector_user; collector_user="\${collector_user:-$detected_collector}"
  [[ $collector_user =~ ^[A-Za-z_][A-Za-z0-9._-]*$ ]] || die "collector user contains unsupported characters"
  ssh -n "\${SSH_OPTIONS[@]}" "$TARGET" "id -u '$collector_user'" >/dev/null || die "collector user does not exist on the server"
else
  prompt "Private CodexBar URL (leave blank to use a working CLI on the target): " CODEXBAR_SOURCE_URL
  url_pattern='^https?://[A-Za-z0-9._:/?&=%+-]+$'
  [[ -z $CODEXBAR_SOURCE_URL || $CODEXBAR_SOURCE_URL =~ $url_pattern ]] || die "CodexBar URL contains unsupported characters"
fi

if [[ $VERSION == latest ]]; then
  VERSION="$(curl --http1.1 --retry 5 --retry-all-errors --retry-delay 1 -fsSL "https://api.github.com/repos/\${REPO}/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' | head -1)"
fi
[[ $VERSION =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die "invalid release version: $VERSION"
BASE_URL="https://github.com/\${REPO}/releases/download/v\${VERSION}"

if [[ $OS == linux ]]; then
  ASSET="usagewidget-\${VERSION}-linux-\${ARCH}.tar.gz"
  remote_command="set -euo pipefail
work=\$(mktemp -d /tmp/usagewidget-bootstrap.XXXXXX)
cleanup_remote() { \${REMOTE_SUDO:+$REMOTE_SUDO }rm -rf -- \"\$work\"; }
trap cleanup_remote EXIT
\${REMOTE_SUDO:+$REMOTE_SUDO }apt-get update
\${REMOTE_SUDO:+$REMOTE_SUDO }apt-get install -y --no-install-recommends ca-certificates curl iproute2 jq openssl qrencode sqlite3 tar util-linux
cd \"\$work\"
curl --http1.1 -fL --retry 5 --retry-all-errors --retry-delay 1 -O '$BASE_URL/$ASSET'
curl --http1.1 -fL --retry 5 --retry-all-errors --retry-delay 1 -O '$BASE_URL/$ASSET.sha256'
sha256sum -c '$ASSET.sha256'
tar -xzf '$ASSET'
\${REMOTE_SUDO:+$REMOTE_SUDO }'./usagewidget-\${VERSION}-linux-\${ARCH}/server-install.sh' install --collector-user '$collector_user'"
elif [[ $OS == darwin ]]; then
  ASSET="usagewidget-\${VERSION}-darwin-\${ARCH}.tar.gz"
  remote_command="set -euo pipefail
work=\$(mktemp -d /tmp/usagewidget-bootstrap.XXXXXX)
trap 'rm -rf -- \"\$work\"' EXIT
cd \"\$work\"
curl --http1.1 -fL --retry 5 --retry-all-errors --retry-delay 1 -O '$BASE_URL/$ASSET'
curl --http1.1 -fL --retry 5 --retry-all-errors --retry-delay 1 -O '$BASE_URL/$ASSET.sha256'
shasum -a 256 -c '$ASSET.sha256'
tar -xzf '$ASSET'
app=\"\$HOME/Library/Application Support/UsageWidget/App\"
mkdir -p \"\$app\"
cp -R './usagewidget-\${VERSION}-darwin-\${ARCH}/.' \"\$app/\"
CODEXBAR_URL='$CODEXBAR_SOURCE_URL' \"\$app/install-server.sh\""
else
  ASSET="usagewidget-\${VERSION}-windows-\${ARCH}.zip"
  escaped_url="\${CODEXBAR_SOURCE_URL//\'/\'\'}"
  ps_script="\$ErrorActionPreference='Stop'; \$work=Join-Path ([IO.Path]::GetTempPath()) ('usagewidget-'+[guid]::NewGuid().ToString('N')); New-Item -ItemType Directory -Path \$work|Out-Null; try { \$asset='$ASSET'; \$base='$BASE_URL'; Invoke-WebRequest -UseBasicParsing -Uri \"\$base/\$asset\" -OutFile (Join-Path \$work \$asset); Invoke-WebRequest -UseBasicParsing -Uri \"\$base/\$asset.sha256\" -OutFile (Join-Path \$work \"\$asset.sha256\"); \$expected=((Get-Content (Join-Path \$work \"\$asset.sha256\") -Raw).Trim() -split '\\s+')[0]; \$actual=(Get-FileHash -Algorithm SHA256 (Join-Path \$work \$asset)).Hash; if(\$actual -ne \$expected){throw 'checksum failed'}; Expand-Archive (Join-Path \$work \$asset) \$work -Force; Stop-ScheduledTask -TaskName 'UsageWidget Server' -ErrorAction SilentlyContinue; Get-Process usagewidgetd -ErrorAction SilentlyContinue|Stop-Process -Force; \$app=Join-Path \$env:LOCALAPPDATA 'UsageWidget\\App'; New-Item -ItemType Directory -Force -Path \$app|Out-Null; Copy-Item (Join-Path \$work 'usagewidget-\${VERSION}-windows-\${ARCH}\\*') \$app -Recurse -Force; \$env:CODEXBAR_URL='$escaped_url'; & (Join-Path \$app 'install-server.ps1') } finally { Remove-Item \$work -Recurse -Force -ErrorAction SilentlyContinue }"
  command -v iconv >/dev/null 2>&1 && command -v base64 >/dev/null 2>&1 || die "iconv and base64 are required to install a Windows target"
  encoded="$(printf '%s' "$ps_script" | iconv -f UTF-8 -t UTF-16LE | base64 | tr -d '\n')"
  remote_command="powershell.exe -NoProfile -EncodedCommand $encoded"
fi

say "installing the $OS/$ARCH server on $TARGET"
say "the private iPhone setup QR will appear below when installation completes"
ssh "\${SSH_OPTIONS[@]}" -tt "$TARGET" "$remote_command" </dev/tty
say "complete; scan the QR above in UsageWidget"
`.replaceAll("\\${", "${");

const windowsInstallScript = String.raw`param([string]$Version=$env:USAGEWIDGET_VERSION,[string]$Repository=$(if($env:USAGEWIDGET_REPO){$env:USAGEWIDGET_REPO}else{"EdmundLimBoEn/UsageWidget"}))
$ErrorActionPreference="Stop"; [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
function Say([string]$m){Write-Host "usagewidget setup: $m"}
if($null -eq (Get-Command ssh -ErrorAction SilentlyContinue)){throw "The Windows OpenSSH client is required"}
$defaultTarget=$env:USAGEWIDGET_DEPLOY_HOST; $configPath=Join-Path $HOME ".config\usagewidget\env"
if(!$defaultTarget -and (Test-Path $configPath)){ $line=Get-Content $configPath|Where-Object{$_ -like "USAGEWIDGET_DEPLOY_HOST=*"}|Select-Object -Last 1; if($line){$defaultTarget=$line.Split('=',2)[1]} }
$defaultUser="root";$defaultServer="";if($defaultTarget -match '^([^@]+)@(.+)$'){$defaultUser=$Matches[1];$defaultServer=$Matches[2]}
$sshUser=Read-Host "SSH user [$defaultUser]";if(!$sshUser){$sshUser=$defaultUser}
$server=Read-Host $(if($defaultServer){"Target IP or hostname [$defaultServer]"}else{"Target IP or hostname"});if(!$server){$server=$defaultServer}
if($sshUser -notmatch '^[A-Za-z_][A-Za-z0-9._-]*$' -or $server -notmatch '^[A-Za-z0-9][A-Za-z0-9.:-]*$'){throw "Invalid SSH destination"}
$target="$sshUser@$server";Say "connecting to $target"
$remoteOS=(& ssh $target "uname -s" 2>$null|Out-String).Trim()
if($remoteOS -eq "Linux"){$os="linux"}elseif($remoteOS -eq "Darwin"){$os="darwin"}else{$os="windows"}
if($os -eq "windows"){$rawArch=(& ssh $target 'powershell.exe -NoProfile -Command "[Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()"'|Out-String).Trim()}else{$rawArch=(& ssh $target uname -m|Out-String).Trim()}
if($rawArch -match '^(x86_64|amd64|X64)$'){$arch="amd64"}elseif($rawArch -match '^(aarch64|arm64|Arm64)$'){$arch="arm64"}else{throw "Target must be amd64 or arm64"}
$sudo="";$collector="";$sourceUrl=""
if($os -eq "linux"){
  $uid=(& ssh $target id -u|Out-String).Trim();if($uid -ne "0"){$sudo="sudo ";& ssh -t $target "sudo -v"}
  $detected=(& ssh $target "getent passwd 1000 | cut -d: -f1"|Out-String).Trim();$collector=Read-Host $(if($detected){"Collector user [$detected]"}else{"Collector user"});if(!$collector){$collector=$detected}
  if($collector -notmatch '^[A-Za-z_][A-Za-z0-9._-]*$'){throw "Invalid collector user"}
}else{$sourceUrl=Read-Host "Private CodexBar URL (leave blank to use a working CLI on the target)";if($sourceUrl -and $sourceUrl -notmatch '^https?://[A-Za-z0-9._:/?&=%+-]+$'){throw "Invalid CodexBar URL"}}
if(!$Version -or $Version -eq "latest"){$release=Invoke-RestMethod -Headers @{"User-Agent"="UsageWidget-Installer"} -Uri "https://api.github.com/repos/$Repository/releases/latest";$Version=([string]$release.tag_name)-replace '^v',''}
if($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$'){throw "Invalid release version"};$base="https://github.com/$Repository/releases/download/v$Version"
if($os -eq "linux"){$asset="usagewidget-$Version-linux-$arch.tar.gz";$template=@'
set -euo pipefail
work="$(mktemp -d /tmp/usagewidget-bootstrap.XXXXXX)"; trap '__SUDO__rm -rf -- "$work"' EXIT
__SUDO__apt-get update
__SUDO__apt-get install -y --no-install-recommends ca-certificates curl iproute2 jq openssl qrencode sqlite3 tar util-linux
cd "$work"; curl --http1.1 -fL --retry 5 --retry-all-errors -O '__BASE__/__ASSET__'; curl --http1.1 -fL --retry 5 --retry-all-errors -O '__BASE__/__ASSET__.sha256'
sha256sum -c '__ASSET__.sha256'; tar -xzf '__ASSET__'; __SUDO__'./usagewidget-__VERSION__-linux-__ARCH__/server-install.sh' install --collector-user '__COLLECTOR__'
'@;$command=$template.Replace('__SUDO__',$sudo).Replace('__BASE__',$base).Replace('__ASSET__',$asset).Replace('__VERSION__',$Version).Replace('__ARCH__',$arch).Replace('__COLLECTOR__',$collector)
}elseif($os -eq "darwin"){$asset="usagewidget-$Version-darwin-$arch.tar.gz";$template=@'
set -euo pipefail
work="$(mktemp -d /tmp/usagewidget-bootstrap.XXXXXX)"; trap 'rm -rf -- "$work"' EXIT; cd "$work"
curl --http1.1 -fL --retry 5 --retry-all-errors -O '__BASE__/__ASSET__'; curl --http1.1 -fL --retry 5 --retry-all-errors -O '__BASE__/__ASSET__.sha256'; shasum -a 256 -c '__ASSET__.sha256'; tar -xzf '__ASSET__'
app="$HOME/Library/Application Support/UsageWidget/App"; mkdir -p "$app"; cp -R './usagewidget-__VERSION__-darwin-__ARCH__/.' "$app/"; CODEXBAR_URL='__SOURCE__' "$app/install-server.sh"
'@;$command=$template.Replace('__BASE__',$base).Replace('__ASSET__',$asset).Replace('__VERSION__',$Version).Replace('__ARCH__',$arch).Replace('__SOURCE__',$sourceUrl)
}else{$asset="usagewidget-$Version-windows-$arch.zip";$template=@'
$ErrorActionPreference='Stop';$work=Join-Path ([IO.Path]::GetTempPath()) ('usagewidget-'+[guid]::NewGuid().ToString('N'));New-Item -ItemType Directory $work|Out-Null
try{$asset='__ASSET__';$base='__BASE__';Invoke-WebRequest -UseBasicParsing "$base/$asset" -OutFile (Join-Path $work $asset);Invoke-WebRequest -UseBasicParsing "$base/$asset.sha256" -OutFile (Join-Path $work "$asset.sha256");$expected=((Get-Content (Join-Path $work "$asset.sha256") -Raw).Trim() -split '\s+')[0];if((Get-FileHash (Join-Path $work $asset) -Algorithm SHA256).Hash -ne $expected){throw 'checksum failed'};Expand-Archive (Join-Path $work $asset) $work -Force;Stop-ScheduledTask -TaskName 'UsageWidget Server' -ErrorAction SilentlyContinue;Get-Process usagewidgetd -ErrorAction SilentlyContinue|Stop-Process -Force;$app=Join-Path $env:LOCALAPPDATA 'UsageWidget\App';New-Item -ItemType Directory -Force $app|Out-Null;Copy-Item (Join-Path $work 'usagewidget-__VERSION__-windows-__ARCH__\*') $app -Recurse -Force;$env:CODEXBAR_URL='__SOURCE__';& (Join-Path $app 'install-server.ps1')}finally{Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue}
'@;$ps=$template.Replace('__BASE__',$base).Replace('__ASSET__',$asset).Replace('__VERSION__',$Version).Replace('__ARCH__',$arch).Replace('__SOURCE__',$sourceUrl);$encoded=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($ps));$command="powershell.exe -NoProfile -EncodedCommand $encoded"}
Say "installing the $os/$arch server on $target";Say "the private iPhone setup QR will appear below when installation completes";& ssh -t $target $command;if($LASTEXITCODE -ne 0){throw "Remote installation failed"};Say "complete; scan the QR above in UsageWidget"
`;

const unixInstallCommand = `curl -fsSL ${PUBLIC_URL}/install.sh | bash`;
const windowsInstallCommand = `irm ${PUBLIC_URL}/install.ps1 | iex`;

function text(body, status = 200, contentType = "text/plain; charset=utf-8", cacheControl = "public, max-age=300") {
  return new Response(body, { status, headers: { "content-type": contentType, "cache-control": cacheControl, "x-content-type-options": "nosniff" } });
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === "/install.sh") return text(unixInstallScript, 200, "text/x-shellscript; charset=utf-8", "no-store");
    if (url.pathname === "/install.ps1") return text(windowsInstallScript, 200, "text/plain; charset=utf-8", "no-store");
    if (url.pathname === "/" || url.pathname === "") return text(`UsageWidget SSH server installer

Run from Linux or macOS:
${unixInstallCommand}

Run from Windows PowerShell:
${windowsInstallCommand}

The installer detects a Linux, macOS, or Windows SSH target, installs the native
UsageWidget server there, configures its private Tailscale route, and prints the
iPhone setup QR in your local terminal.`, 200);
    return text("Not found\n", 404);
  }
};
