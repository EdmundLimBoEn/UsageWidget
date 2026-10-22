import { SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import worker from "../src/index";
import { REVIEW_TOKEN } from "../src/token";

const headers = { Authorization: `Bearer ${REVIEW_TOKEN}` };
const encoder = new TextEncoder();

async function settings(patch: unknown): Promise<Response> {
  return SELF.fetch("https://example.com/v1/settings", {
    method: "PUT", headers, body: JSON.stringify(patch),
  });
}

describe.each([
  ["PUT", "/v1/settings", {}],
  ["POST", "/v1/devices", { deviceID: "test-device" }],
] as const)("%s %s body limit", (method, path, payload) => {
  it.each([undefined, "1"])("stops and cancels an oversized stream with Content-Length %s", async (length) => {
    let pulls = 0;
    let cancelled = false;
    const prefix = encoder.encode(JSON.stringify(payload));
    const stream = new ReadableStream<Uint8Array>({
      pull(controller) {
        pulls++;
        if (pulls === 1) controller.enqueue(prefix);
        else if (pulls <= 20) controller.enqueue(new Uint8Array(1024).fill(32));
        else controller.close();
      },
      cancel() { cancelled = true; },
    }, { highWaterMark: 0 });
    // Direct entrypoint invocation preserves an intentionally dishonest header.
    const response = await worker.fetch(new Request(`https://example.com${path}`, {
      method, headers: { ...headers, ...(length ? { "Content-Length": length } : {}) }, body: stream,
    }));
    expect(response.status).toBe(413);
    expect(await response.json()).toEqual({ error: "request body too large" });
    expect(cancelled).toBe(true);
    expect(pulls).toBe(17);
  });

  it.each([16384, 16385])("enforces the byte boundary at %i bytes", async (size) => {
    const body = JSON.stringify(payload).padEnd(size, " ");
    const response = await SELF.fetch(`https://example.com${path}`, { method, headers, body });
    expect(response.status).toBe(size === 16384 ? 200 : 413);
  });

  it("counts UTF-8 bytes rather than characters", async () => {
    const body = JSON.stringify({ ...payload, ignored: "é".repeat(8192) });
    expect(body.length).toBeLessThan(16384);
    const response = await SELF.fetch(`https://example.com${path}`, { method, headers, body });
    expect(response.status).toBe(413);
  });

  it.each(["", "{", "null", "[]"])("keeps invalid JSON/object handling for %s", async (body) => {
    const response = await SELF.fetch(`https://example.com${path}`, { method, headers, body });
    expect(response.status).toBe(400);
  });
});

describe("field limits", () => {
  it.each(["providerOrder", "hiddenProviders", "alertOverrides"])("bounds %s count", async (key) => {
    const item = key === "alertOverrides" ? { providerID: "codex", windowID: "codex.primary" } : "codex";
    expect((await settings({ [key]: Array(64).fill(item) })).status).toBe(200);
    expect((await settings({ [key]: Array(65).fill(item) })).status).toBe(400);
  });

  it.each(["providerOrder", "hiddenProviders"])("bounds strings in %s", async (key) => {
    expect((await settings({ [key]: ["x".repeat(128)] })).status).toBe(200);
    expect((await settings({ [key]: ["x".repeat(129)] })).status).toBe(400);
  });

  it.each(["providerID", "windowID"])("bounds override %s", async (key) => {
    expect((await settings({ alertOverrides: [{ [key]: "x".repeat(128) }] })).status).toBe(200);
    expect((await settings({ alertOverrides: [{ [key]: "x".repeat(129) }] })).status).toBe(400);
  });

  it("bounds the time zone string", async () => {
    expect((await settings({ quietHours: { timeZone: "x".repeat(128) } })).status).toBe(200);
    expect((await settings({ quietHours: { timeZone: "x".repeat(129) } })).status).toBe(400);
  });

  it.each(["apnsToken", "widgetToken"])("bounds %s without truncating accepted tokens", async (key) => {
    for (const size of [0, 64, 1024, 1025]) {
      const token = "a".repeat(size);
      const response = await SELF.fetch("https://example.com/v1/devices", {
        method: "POST", headers, body: JSON.stringify({ deviceID: "test-device", [key]: token }),
      });
      expect(response.status).toBe(size <= 1024 ? 200 : 400);
      if (response.status === 200) expect(await response.json()).toMatchObject({ [key]: token });
    }
  });

  it("echoes a complete iOS settings payload", async () => {
    const patch = {
      pollIntervalMinutes: 5,
      providerOrder: ["cursor", "codex", "claude_code", "copilot", "gemini_cli", "grok", "devin"],
      hiddenProviders: ["devin"], notificationsEnabled: true,
      earlyThresholdPct: 25, dangerThresholdPct: 10, defaultRepeatIntervalMinutes: 0,
      quietHours: { enabled: true, startMinute: 1320, endMinute: 420, timeZone: "Asia/Singapore" },
      alertOverrides: [{ providerID: "codex", windowID: "codex.primary", rule: {
        enabled: true, earlyThresholdPct: 20, dangerThresholdPct: 5, repeatIntervalMinutes: 30,
      } }],
    };
    const response = await settings(patch);
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual(patch);
  });
});
