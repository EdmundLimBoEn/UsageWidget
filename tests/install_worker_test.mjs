import assert from "node:assert/strict";
import worker from "../worker/install.js";

const unixResponse = await worker.fetch(
  new Request("https://usagewidget.edmundlim.systems/install.sh"),
);
assert.equal(unixResponse.status, 200);
assert.match(unixResponse.headers.get("content-type"), /^text\/x-shellscript/);
assert.equal(unixResponse.headers.get("cache-control"), "no-store");

const unixScript = await unixResponse.text();
assert.match(unixScript, /curl -fsSL https:\/\/usagewidget\.edmundlim\.systems\/install\.sh \| bash/);
assert.match(unixScript, /Linux\) OS=linux/);
assert.match(unixScript, /Darwin\) OS=darwin/);
assert.match(unixScript, /x86_64\|amd64\) ARCH=amd64/);
assert.match(unixScript, /aarch64\|arm64\) ARCH=arm64/);
assert.match(unixScript, /Linux user that owns the CodexBar session/);
assert.match(unixScript, /Private CodexBar usage URL/);
assert.match(unixScript, /sha256sum -c/);
assert.match(unixScript, /shasum -a 256 -c/);
assert.match(unixScript, /USAGEWIDGET_INSTALL_DIR/);
assert.doesNotMatch(unixScript, /\\\$\{/);
assert.doesNotMatch(unixScript, /apt-get install[^\n]*jq/);

const windowsResponse = await worker.fetch(
  new Request("https://usagewidget.edmundlim.systems/install.ps1"),
);
assert.equal(windowsResponse.status, 200);
assert.match(windowsResponse.headers.get("content-type"), /^text\/plain/);
assert.equal(windowsResponse.headers.get("cache-control"), "no-store");

const windowsScript = await windowsResponse.text();
assert.match(windowsScript, /OSArchitecture/);
assert.match(windowsScript, /"X64" \{ \$arch = "amd64" \}/);
assert.match(windowsScript, /"Arm64" \{ \$arch = "arm64" \}/);
assert.match(windowsScript, /Get-FileHash -Algorithm SHA256/);
assert.match(windowsScript, /Read-Host "Private CodexBar usage URL"/);
assert.match(windowsScript, /Join-Path \$dataDirectory "App"/);
assert.match(windowsScript, /start-server\.ps1/);

const landing = await worker.fetch(
  new Request("https://usagewidget.edmundlim.systems/"),
);
const landingText = await landing.text();
assert.match(landingText, /Linux or macOS:/);
assert.match(landingText, /curl -fsSL https:\/\/usagewidget\.edmundlim\.systems\/install\.sh \| bash/);
assert.match(landingText, /Windows PowerShell:/);
assert.match(landingText, /irm https:\/\/usagewidget\.edmundlim\.systems\/install\.ps1 \| iex/);

const missing = await worker.fetch(
  new Request("https://usagewidget.edmundlim.systems/not-an-installer"),
);
assert.equal(missing.status, 404);

console.log("install worker tests passed");
