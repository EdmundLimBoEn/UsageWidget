# App Review backend

Always-on Cloudflare Worker that serves the same phone API as `usagewidgetd`
(`GET /v1/health`, `GET /v1/snapshot`, settings, devices, poll, readiness)
with **static synthetic data**. Apple Review uses this so Guideline 2.1(a)
does not depend on a home machine or tunnel.

There are **no production keys** in this directory. The bearer token below is
a public App Review demo credential. It only unlocks fake usage windows.

## Deploy (deploy bot)

From this directory, authenticated to the Cloudflare account that already
serves `usagewidget.edmundlim.systems`:

```bash
cd review-worker
npx wrangler deploy
```

That command publishes Worker `usagewidget-apple-review` and attaches custom
domain:

`https://apple-review-testing.usagewidget.edmundlim.systems`

If the custom domain is not ready yet, the same Worker also has a
`workers.dev` URL (`https://usagewidget-apple-review.<subdomain>.workers.dev`).
Point App Review notes at the custom domain once DNS is live.

No extra Cloudflare secrets, KV, or Durable Objects are required.

## App Review connection

| Field | Value |
|-------|--------|
| Server URL | `https://apple-review-testing.usagewidget.edmundlim.systems` |
| Bearer token | `usagewidget-apple-review-only-not-a-secret` |

The iOS app sends `Authorization: Bearer <token>` on every `/v1/` route, same
as production. A wrong token returns HTTP 401.

Optional Tailscale-style prefix `https://apple-review-testing.usagewidget.edmundlim.systems/usagewidget` also works.

## Verify locally

```bash
cd review-worker
npm install
npm test
npx wrangler deploy --dry-run
```

Tests decode responses against the Codable fields in
`ios/Sources/Core/Models.swift` and fail if a required key disappears.
