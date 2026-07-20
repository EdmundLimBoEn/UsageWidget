import assert from "node:assert/strict";
import worker from "../worker/install.js";

const unixResponse = await worker.fetch(new Request("https://usagewidget.edmundlim.systems/install.sh"));
assert.equal(unixResponse.status, 200);
assert.match(unixResponse.headers.get("content-type"), /^text\/x-shellscript/);
assert.equal(unixResponse.headers.get("cache-control"), "no-store");
const unixScript = await unixResponse.text();
assert.match(unixScript, /SSH user/);
assert.match(unixScript, /Target IP or hostname/);
assert.match(unixScript, /Collector user/);
assert.match(unixScript, /ssh .* -tt "\$TARGET" "\$remote_command"/);
assert.match(unixScript, /usagewidget-\$\{VERSION\}-linux-\$\{ARCH\}/);
assert.match(unixScript, /sha256sum -c/);
assert.match(unixScript, /apt-get install[^\n]*qrencode/);
assert.match(unixScript, /private iPhone setup QR will appear below/);
assert.match(unixScript, /OS=darwin/);
assert.match(unixScript, /OS=windows/);
assert.match(unixScript, /install-server\.sh/);
assert.match(unixScript, /install-server\.ps1/);
assert.doesNotMatch(unixScript, /\\\$\{/);

const windowsResponse = await worker.fetch(new Request("https://usagewidget.edmundlim.systems/install.ps1"));
assert.equal(windowsResponse.status, 200);
assert.equal(windowsResponse.headers.get("cache-control"), "no-store");
const windowsScript = await windowsResponse.text();
assert.match(windowsScript, /Windows OpenSSH client is required/);
assert.match(windowsScript, /Target IP or hostname/);
assert.match(windowsScript, /Collector user/);
assert.match(windowsScript, /usagewidget-\$Version-linux-\$arch/);
assert.match(windowsScript, /sha256sum -c/);
assert.match(windowsScript, /private iPhone setup QR will appear below/);
assert.match(windowsScript, /Expand-Archive/);
assert.match(windowsScript, /install-server\.ps1/);

const landing = await worker.fetch(new Request("https://usagewidget.edmundlim.systems/"));
const landingText = await landing.text();
assert.match(landingText, /UsageWidget SSH server installer/);
assert.match(landingText, /curl -fsSL https:\/\/usagewidget\.edmundlim\.systems\/install\.sh \| bash/);
assert.match(landingText, /irm https:\/\/usagewidget\.edmundlim\.systems\/install\.ps1 \| iex/);
assert.match(landingText, /Linux, macOS, or Windows SSH target/);

const missing = await worker.fetch(new Request("https://usagewidget.edmundlim.systems/nope"));
assert.equal(missing.status, 404);
console.log("install worker tests passed");
