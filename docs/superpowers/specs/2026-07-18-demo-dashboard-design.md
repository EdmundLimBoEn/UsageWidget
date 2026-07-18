# UsageWidget Demo Dashboard Design

## Goal

Build a private web dashboard at `demo.usagewidget.edmundlim.systems` for controlling and showcasing a synthetic UsageWidget provider through the real server-to-iPhone pipeline.

The frontend stays clean, restrained, and easy to operate. The deliberate complexity belongs in the secure end-to-end system behind it.

## Scope

The dashboard controls only the synthetic demo provider. It does not monitor or mutate real providers, production APNs configuration, the database, registered devices, deployments, or general server operations.

The dashboard supports:

- Primary and secondary usage-window percentages
- Reset timing
- Surprise reset scenarios
- Available credits
- Stale and provider-error states
- Immediate demo polling
- Existing test-alert delivery
- Demo-only pipeline results and recent events

## Approved Frontend

The selected mockup is `.superpowers/brainstorm/79315-1784346965/content/lab-console.html`.

Preserve its visual direction:

- Compact, dark, three-column single-screen workspace
- System font stack and tabular numerals
- Restrained status colors and no ornamental effects
- Demo controls in the first column
- Latest pipeline trace and normalized snapshot in the second column
- Filterable demo-event feed in the third column
- One-column responsive layout
- Keyboard-accessible controls
- Reduced-motion support

The mockup is the frontend design source of truth. Implementation may replace placeholder data and inactive controls, but must not broaden the information architecture or introduce NOC, CRT, terminal, draggable-pane, or production-monitoring concepts.

## Architecture

```text
Browser
  -> demo.usagewidget.edmundlim.systems
  -> Cloudflare Access
  -> Cloudflare Tunnel
  -> 127.0.0.1:8378 on edServe
  -> static Lab Console and demo-only API
  -> demo state merged into raw provider payload
  -> existing normalization
  -> existing snapshot persistence
  -> existing event engine and deduplication
  -> existing APNs alert and widget refresh delivery
  -> signed iPhone app and widget
```

Cloudflare Access authenticates the operator before the request reaches edServe. The Tunnel targets a dedicated loopback listener on port 8378 that serves only the Lab Console and demo routes. The existing full API remains on port 8377 behind Tailscale and is not exposed through the demo hostname.

The dashboard and demo API share one origin, so the design needs no Pages deployment, Worker proxy, browser-held upstream token, or CORS configuration.

## Components

### Lab Console

A static TypeScript/CSS frontend based on the approved mockup. It reads demo state, edits controls, starts actions, and displays the latest normalized snapshot, pipeline result, and demo events.

### Demo HTTP Listener

A second HTTP listener bound to `127.0.0.1:8378` that:

- Serves the compiled Lab Console assets
- Exposes only the defined demo routes
- Trusts requests only after Cloudflare Access and Tunnel routing
- Applies request-size and timeout limits
- Shares demo services with the existing server without exposing the full API

The existing port 8377 listener and bearer-authenticated Tailscale API remain unchanged.

### Go Demo Source

The Go server persists synthetic demo state in SQLite. During a poll, it merges the demo provider into the raw provider payload before normalization. This ensures normalization, snapshot storage, event detection, deduplication, and APNs delivery use the existing production code paths.

Demo state and event keys are namespaced under `demo.*`. Demo APIs never accept an arbitrary provider ID.

### Demo Run Record

Each requested demo poll records the latest stage outcomes needed by the Lab Console:

- Demo state loaded
- Provider normalized
- Snapshot persisted
- Events emitted or deduplicated
- APNs delivery result

The dashboard displays concise real outcomes rather than simulated pipeline success.

## API

The dedicated demo listener exposes this narrow API:

### `GET /v1/demo`

Returns persisted demo state, the current normalized demo-provider snapshot, and the latest pipeline result.

### `PATCH /v1/demo`

Validates and persists any supplied demo-state fields. Supported fields cover window usage and reset times, credits, stale state, and provider error state.

The endpoint does not poll implicitly. This keeps state editing and execution explicit.

### `POST /v1/demo/poll`

Runs an immediate serialized poll using the persisted demo state. Returns the recorded pipeline result and emitted demo events.

### `GET /v1/demo/events`

Returns recent demo-only events with optional event-type filtering and a bounded limit.

### `POST /v1/demo/alert`

Reuses the existing test-alert route.

The Surprise Reset control performs a demo-state update followed by a demo poll. It does not require a separate backend subsystem.

## Data Flow

1. The operator opens the Cloudflare Access-protected Lab Console through the Tunnel.
2. The operator changes demo controls.
3. The same-origin frontend sends a partial state update to the demo-only listener.
4. The Go server validates and persists the demo state.
5. The operator chooses Apply and Poll, Surprise Reset, or Test Alert.
6. For a poll, the Go server loads demo state and merges a synthetic raw provider before normalization.
7. The existing pipeline normalizes and stores the snapshot, evaluates events, deduplicates notifications, and sends APNs updates.
8. The server records and returns the pipeline result and demo events.
9. The Lab Console updates the pipeline trace, normalized snapshot, and event feed.
10. The signed iPhone app and widget receive the same resulting demo-provider state through their existing paths.

## Validation and Failure Handling

The Go boundary rejects:

- Percentages outside 0–100
- Invalid or unsupported reset timestamps
- Negative credits
- Unknown demo windows
- Unknown fields
- Oversized request bodies

A state update is persisted before a requested poll. If polling fails, the requested state remains available for retry and the response identifies the failed pipeline stage.

The existing poll mutex prevents concurrent polls. Existing event deduplication applies to demo windows. Demo errors cannot overwrite or alter real-provider snapshots or baselines.

Cloudflare Access rejects unauthenticated requests before the Tunnel forwards them. The dedicated listener:

- Registers only static asset and demo API routes
- Uses bounded request and response sizes
- Applies request timeouts
- Returns concise errors without internal paths or configuration values

The Lab Console keeps the last successful snapshot visible when a request fails, marks it stale, and provides a retry action.

## Security Boundaries

- Cloudflare Access is the operator authentication layer.
- The Tunnel is the only path from the public hostname to edServe.
- The Tunnel targets only `127.0.0.1:8378`.
- Port 8378 registers no real-provider or operational routes.
- The existing bearer-authenticated API on port 8377 remains reachable only through its current Tailscale path.
- Demo routes cannot select a provider ID.
- All mutable keys and event keys are restricted to the `demo.*` namespace.
- The browser stores no edServe bearer token.

## Verification

### Go tests

- Demo-state validation
- SQLite persistence
- Raw demo-provider injection before normalization
- Normalized window identifiers
- Threshold, reset, surprise-reset, credits, stale, and error behavior
- Event deduplication across restarts
- Real-provider state and baseline isolation
- Serialized immediate polling

### Demo-listener tests

- Loopback-only binding configuration
- Static asset serving
- Demo-route allowlist
- Request and response limits
- Timeout and error redaction
- Rejection of real-provider and operational routes

### Frontend check

Exercise the approved controls and confirm they produce the expected requests and render:

- Successful state updates
- Successful and failed pipeline stages
- Normalized snapshots
- Empty and populated event feeds
- Stale and provider-error states
- Responsive and keyboard-only operation

### Release gate

The release is complete only after a signed physical iPhone demonstrates the full flow from the Cloudflare dashboard:

- Threshold alert
- Surprise reset alert
- Credits increase
- Stale state
- Provider error state
- Event deduplication after a server restart
- Existing test alert
- App and widget refresh with the resulting demo-provider snapshot

Real-provider behavior must remain unchanged.

## Deployment

- Start the demo-only listener on `127.0.0.1:8378` as part of `usagewidgetd`.
- Configure a Cloudflare Tunnel public hostname for `demo.usagewidget.edmundlim.systems` targeting `http://127.0.0.1:8378`.
- Protect the hostname with Cloudflare Access.
- Serve the Lab Console and demo API from the same local listener and origin.
- Keep the existing port 8377 Tailscale endpoint and iOS configuration unchanged.
- Do not deploy Cloudflare Pages or a Worker for this dashboard.

## Explicit Non-Goals

- General production operations dashboard
- Real-provider editing
- APNs, database, device, or deployment administration
- Historical analytics beyond recent demo events
- Multi-user collaboration
- Draggable dashboards, command palettes, terminals, or decorative monitoring theater
- A new notification or event engine
