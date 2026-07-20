import assert from "node:assert/strict";
import worker from "../worker/install.js";

const response = await worker.fetch(
  new Request("https://usagewidget.edmundlim.systems/install.sh"),
);
assert.equal(response.status, 200);
assert.match(response.headers.get("content-type"), /^text\/x-shellscript/);

const script = await response.text();
assert.match(script, /SERVER_URL="\$\{USAGEWIDGET_SERVER_URL/);
assert.match(script, /if \[\[ -n "\$SERVER_URL" \]\]; then INSTALL_ARGS\+=\(--public-url/);
assert.doesNotMatch(
  script,
  /--public-url "https:\/\/usagewidget\.edmundlim\.systems"/,
  "the download domain must not become the installed phone API URL",
);
assert.match(script, /apt-get install -y ca-certificates curl jq coreutils tar/);

const landing = await worker.fetch(
  new Request("https://usagewidget.edmundlim.systems/"),
);
assert.match(
  await landing.text(),
  /curl -fsSL https:\/\/usagewidget\.edmundlim\.systems\/install\.sh/,
);

console.log("install worker tests passed");
