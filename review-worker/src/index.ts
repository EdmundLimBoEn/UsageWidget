import { bearerToken, tokensEqual } from "./auth";
import {
  defaultSettings,
  publicIndex,
  reviewHealth,
  reviewPoll,
  reviewReadiness,
  reviewReadinessTest,
  reviewSnapshot,
  type ServerSettings,
} from "./synthetic";
import { REVIEW_TOKEN } from "./token";

export { REVIEW_TOKEN } from "./token";

const JSON_HEADERS = {
  "content-type": "application/json; charset=utf-8",
  "cache-control": "no-store",
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body) + "\n", { status, headers: JSON_HEADERS });
}

function normalizePath(pathname: string): string {
  let path = pathname;
  if (path.length > 1 && path.endsWith("/")) {
    path = path.slice(0, -1);
  }
  if (path === "/usagewidget") {
    return "/";
  }
  if (path.startsWith("/usagewidget/")) {
    return path.slice("/usagewidget".length);
  }
  return path;
}

function mergeSettings(body: unknown): ServerSettings | null {
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return null;
  }
  const patch = body as Record<string, unknown>;
  const next = defaultSettings();
  if (typeof patch.pollIntervalMinutes === "number") {
    next.pollIntervalMinutes = patch.pollIntervalMinutes;
  }
  if (Array.isArray(patch.providerOrder) && patch.providerOrder.every((item) => typeof item === "string")) {
    next.providerOrder = patch.providerOrder;
  }
  if (Array.isArray(patch.hiddenProviders) && patch.hiddenProviders.every((item) => typeof item === "string")) {
    next.hiddenProviders = patch.hiddenProviders;
  }
  if (typeof patch.notificationsEnabled === "boolean") {
    next.notificationsEnabled = patch.notificationsEnabled;
  }
  if (typeof patch.earlyThresholdPct === "number") {
    next.earlyThresholdPct = patch.earlyThresholdPct;
  }
  if (typeof patch.dangerThresholdPct === "number") {
    next.dangerThresholdPct = patch.dangerThresholdPct;
  }
  if (typeof patch.defaultRepeatIntervalMinutes === "number") {
    next.defaultRepeatIntervalMinutes = patch.defaultRepeatIntervalMinutes;
  }
  if (patch.quietHours && typeof patch.quietHours === "object" && !Array.isArray(patch.quietHours)) {
    const quiet = patch.quietHours as Record<string, unknown>;
    if (typeof quiet.enabled === "boolean") {
      next.quietHours.enabled = quiet.enabled;
    }
    if (typeof quiet.startMinute === "number") {
      next.quietHours.startMinute = quiet.startMinute;
    }
    if (typeof quiet.endMinute === "number") {
      next.quietHours.endMinute = quiet.endMinute;
    }
    if (typeof quiet.timeZone === "string") {
      next.quietHours.timeZone = quiet.timeZone;
    }
  }
  if (Array.isArray(patch.alertOverrides)) {
    next.alertOverrides = patch.alertOverrides;
  }
  return next;
}

async function readJson(request: Request): Promise<unknown | undefined> {
  const text = await request.text();
  if (text.trim() === "") {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function deviceIDFrom(path: string, prefix: string): string | null {
  if (!path.startsWith(prefix)) {
    return null;
  }
  const rest = path.slice(prefix.length);
  if (!rest || rest.includes("/")) {
    return null;
  }
  try {
    const id = decodeURIComponent(rest);
    if (!id || id.length > 128) {
      return null;
    }
    return id;
  } catch {
    return null;
  }
}

export default {
  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    const path = normalizePath(url.pathname);
    const method = request.method.toUpperCase();

    if (method === "GET" && (path === "/" || path === "")) {
      return jsonResponse(200, publicIndex());
    }

    if (!path.startsWith("/v1/")) {
      return jsonResponse(404, { error: "not found" });
    }

    const token = bearerToken(request.headers.get("Authorization"));
    if (!(await tokensEqual(token, REVIEW_TOKEN))) {
      console.log(JSON.stringify({ msg: "unauthorized", path, method }));
      return jsonResponse(401, { error: "unauthorized" });
    }

    const now = new Date();
    const settings = defaultSettings();

    if (method === "GET" && path === "/v1/health") {
      return jsonResponse(200, reviewHealth(now));
    }
    if (method === "GET" && path === "/v1/snapshot") {
      return jsonResponse(200, reviewSnapshot(now, settings.pollIntervalMinutes));
    }
    if (method === "GET" && path === "/v1/settings") {
      return jsonResponse(200, settings);
    }
    if (method === "PUT" && path === "/v1/settings") {
      const body = await readJson(request);
      const merged = mergeSettings(body);
      if (!merged) {
        return jsonResponse(400, { error: "invalid JSON body" });
      }
      return jsonResponse(200, merged);
    }
    if (method === "POST" && path === "/v1/devices") {
      const body = await readJson(request);
      if (body === null || typeof body !== "object" || Array.isArray(body)) {
        return jsonResponse(400, { error: "invalid JSON body" });
      }
      const record = body as Record<string, unknown>;
      if (typeof record.deviceID !== "string" || record.deviceID === "" || record.deviceID.length > 128) {
        return jsonResponse(400, { error: "deviceID is required" });
      }
      const asString = (value: unknown): string => (typeof value === "string" ? value : "");
      return jsonResponse(200, {
        deviceID: record.deviceID,
        apnsToken: asString(record.apnsToken),
        widgetToken: asString(record.widgetToken),
      });
    }
    if (method === "POST" && path === "/v1/poll") {
      return jsonResponse(200, reviewPoll(now));
    }

    const readinessTestPrefix = "/v1/readiness/";
    if (method === "POST" && path.startsWith(readinessTestPrefix) && path.endsWith("/test")) {
      const deviceID = deviceIDFrom(path.slice(0, -"/test".length), readinessTestPrefix);
      if (!deviceID) {
        return jsonResponse(400, { error: "invalid deviceID" });
      }
      return jsonResponse(200, reviewReadinessTest(now));
    }
    if (method === "GET" && path.startsWith(readinessTestPrefix)) {
      const deviceID = deviceIDFrom(path, readinessTestPrefix);
      if (!deviceID) {
        return jsonResponse(400, { error: "invalid deviceID" });
      }
      return jsonResponse(200, reviewReadiness(now, deviceID));
    }
    if (method === "DELETE" && path.startsWith("/v1/devices/")) {
      const deviceID = deviceIDFrom(path, "/v1/devices/");
      if (!deviceID) {
        return jsonResponse(400, { error: "invalid deviceID" });
      }
      return new Response(null, { status: 204, headers: { "cache-control": "no-store" } });
    }

    return jsonResponse(404, { error: "not found" });
  },
} satisfies ExportedHandler<Env>;
