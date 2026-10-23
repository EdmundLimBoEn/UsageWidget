import { SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { REVIEW_TOKEN } from "../src/token";
import {
  COLLECTOR_REQUIRED_KEYS,
  DEVICE_REQUIRED_KEYS,
  FORECAST_REQUIRED_KEYS,
  HEALTH_REQUIRED_KEYS,
  POLL_REQUIRED_KEYS,
  PROVIDER_REQUIRED_KEYS,
  PROVIDER_AVAILABILITY_REQUIRED_KEYS,
  QUIET_HOURS_REQUIRED_KEYS,
  READINESS_CHECK_REQUIRED_KEYS,
  READINESS_REQUIRED_KEYS,
  READINESS_TEST_REQUIRED_KEYS,
  SETTINGS_REQUIRED_KEYS,
  SNAPSHOT_REQUIRED_KEYS,
  WINDOW_REQUIRED_KEYS,
  assertIsoDate,
  assertObjectWithKeys,
} from "../src/ios-schema";

async function call(path: string, init: RequestInit = {}, authenticate = true): Promise<Response> {
  const headers = new Headers(init.headers);
  if (authenticate && !headers.has("Authorization")) {
    headers.set("Authorization", `Bearer ${REVIEW_TOKEN}`);
  }
  return SELF.fetch(`https://apple-review-testing.usagewidget.edmundlim.systems${path}`, {
    ...init,
    headers,
  });
}

async function json(path: string, init: RequestInit = {}): Promise<{ status: number; body: unknown }> {
  const response = await call(path, init);
  return { status: response.status, body: await response.json() };
}

function assertSnapshot(body: unknown): void {
  const snap = assertObjectWithKeys("Snapshot", body, SNAPSHOT_REQUIRED_KEYS);
  assertIsoDate("Snapshot.fetchedAt", snap.fetchedAt);
  expect(snap.stale).toBe(false);
  expect(snap.pollIntervalMinutes).toBe(5);
  expect(Array.isArray(snap.providers)).toBe(true);
  const catalog = snap.providerCatalog as unknown[];
  expect(catalog).toHaveLength(7);
  expect(new Set(catalog.map((row) => (row as { id: string }).id)).size).toBe(7);
  for (const row of catalog) {
    const provider = assertObjectWithKeys("ProviderAvailability", row, PROVIDER_AVAILABILITY_REQUIRED_KEYS);
    expect(provider.available).toBe(true);
    expect(provider.status).toBe("available");
  }
  const providers = snap.providers as unknown[];
  expect(providers.length).toBeGreaterThan(0);
  for (const provider of providers) {
    const p = assertObjectWithKeys("Provider", provider, PROVIDER_REQUIRED_KEYS);
    expect(typeof p.id).toBe("string");
    expect(typeof p.name).toBe("string");
    const windows = (p.windows ?? []) as unknown[];
    for (const window of windows) {
      const w = assertObjectWithKeys("UsageWindow", window, WINDOW_REQUIRED_KEYS);
      expect(typeof w.usedPercent).toBe("number");
      expect(typeof w.remainingPercent).toBe("number");
      if (w.resetsAt != null) {
        assertIsoDate("UsageWindow.resetsAt", w.resetsAt);
      }
      if (w.forecast != null) {
        const forecast = assertObjectWithKeys("WindowForecast", w.forecast, FORECAST_REQUIRED_KEYS);
        assertIsoDate("WindowForecast.computedAt", forecast.computedAt);
        assertIsoDate("WindowForecast.estimatedExhaustionAt", forecast.estimatedExhaustionAt);
      }
    }
  }
}

describe("App Review worker", () => {
  it("rejects a missing token with 401", async () => {
    const response = await call("/v1/snapshot", {}, false);
    expect(response.status).toBe(401);
    expect(await response.json()).toEqual({ error: "unauthorized" });
  });

  it("rejects a wrong token with 401", async () => {
    const { status, body } = await json("/v1/health", {
      headers: { Authorization: "Bearer definitely-not-the-review-token" },
    });
    expect(status).toBe(401);
    expect(body).toEqual({ error: "unauthorized" });
  });

  it("returns a Snapshot the iOS Codable model can decode", async () => {
    const { status, body } = await json("/v1/snapshot");
    expect(status).toBe(200);
    assertSnapshot(body);
  });

  it("returns Health matching ios/Sources/Core/Models.swift", async () => {
    const { status, body } = await json("/v1/health");
    expect(status).toBe(200);
    const health = assertObjectWithKeys("Health", body, HEALTH_REQUIRED_KEYS);
    expect(health.service).toBe("ok");
    expect(health.codexbar).toBe(true);
    expect(health.database).toBe(true);
    expect(health.polling).toBe(true);
    const collector = assertObjectWithKeys("CollectorHealth", health.collector, COLLECTOR_REQUIRED_KEYS);
    expect(collector.status).toBe("ok");
  });

  it("returns ServerSettings the app decodes", async () => {
    const { status, body } = await json("/v1/settings");
    expect(status).toBe(200);
    const settings = assertObjectWithKeys("ServerSettings", body, SETTINGS_REQUIRED_KEYS);
    assertObjectWithKeys("QuietHours", settings.quietHours, QUIET_HOURS_REQUIRED_KEYS);
    expect(settings.providerOrder).toEqual([
      "cursor",
      "codex",
      "claude_code",
      "copilot",
      "gemini_cli",
      "grok",
      "devin",
    ]);
  });

  it("echoes a settings PUT using the same Codable shape", async () => {
    const { status, body } = await json("/v1/settings", {
      method: "PUT",
      headers: { "content-type": "application/json", Authorization: `Bearer ${REVIEW_TOKEN}` },
      body: JSON.stringify({ pollIntervalMinutes: 15, notificationsEnabled: false }),
    });
    expect(status).toBe(200);
    const settings = assertObjectWithKeys("ServerSettings", body, SETTINGS_REQUIRED_KEYS);
    expect(settings.pollIntervalMinutes).toBe(15);
    expect(settings.notificationsEnabled).toBe(false);
  });

  it("registers a device using DeviceRegistration keys", async () => {
    const { status, body } = await json("/v1/devices", {
      method: "POST",
      headers: { "content-type": "application/json", Authorization: `Bearer ${REVIEW_TOKEN}` },
      body: JSON.stringify({ deviceID: "review-iphone-1", apnsToken: "deadbeef" }),
    });
    expect(status).toBe(200);
    const device = assertObjectWithKeys("DeviceRegistration", body, DEVICE_REQUIRED_KEYS);
    expect(device.deviceID).toBe("review-iphone-1");
    expect(device.apnsToken).toBe("deadbeef");
  });

  it("returns PollResult for a forced poll", async () => {
    const { status, body } = await json("/v1/poll", { method: "POST" });
    expect(status).toBe(200);
    const poll = assertObjectWithKeys("PollResult", body, POLL_REQUIRED_KEYS);
    assertIsoDate("PollResult.polledAt", poll.polledAt);
    expect(poll.ok).toBe(true);
    expect(poll.success).toBe(true);
  });

  it("returns Readiness and a simulated device test", async () => {
    const readiness = await json("/v1/readiness/review-iphone-1");
    expect(readiness.status).toBe(200);
    const payload = assertObjectWithKeys("Readiness", readiness.body, READINESS_REQUIRED_KEYS);
    expect(payload.ready).toBe(true);
    assertIsoDate("Readiness.checkedAt", payload.checkedAt);
    const checks = payload.checks as unknown[];
    expect(checks.length).toBeGreaterThan(0);
    for (const check of checks) {
      const row = assertObjectWithKeys("ReadinessCheck", check, READINESS_CHECK_REQUIRED_KEYS);
      expect(row.status).toBe("pass");
    }
    const latest = assertObjectWithKeys(
      "ReadinessTestResult",
      payload.latestTest,
      READINESS_TEST_REQUIRED_KEYS,
    );
    expect(latest.alertAccepted).toBe(true);

    const test = await json("/v1/readiness/review-iphone-1/test", { method: "POST" });
    expect(test.status).toBe(200);
    const result = assertObjectWithKeys("ReadinessTestResult", test.body, READINESS_TEST_REQUIRED_KEYS);
    expect(result.widgetAccepted).toBe(true);
  });

  it("serves the same API under the /usagewidget prefix the production server uses", async () => {
    const { status, body } = await json("/usagewidget/v1/snapshot");
    expect(status).toBe(200);
    assertSnapshot(body);
  });
});
