#!/usr/bin/env node
/**
 * Fail if ios/Sources/Core/Models.swift no longer declares the Codable fields
 * this Worker encodes. The HTTP tests decode those same keys.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const swiftPath = path.resolve(root, "../ios/Sources/Core/Models.swift");
const schemaPath = path.resolve(root, "src/ios-schema.ts");

const swift = fs.readFileSync(swiftPath, "utf8");
const schema = fs.readFileSync(schemaPath, "utf8");

const structs = {
  Snapshot: ["fetchedAt", "stale", "providers", "pollIntervalMinutes"],
  Provider: ["id", "name"],
  UsageWindow: ["id", "key", "title", "usedPercent", "remainingPercent"],
  WindowForecast: [
    "computedAt",
    "burnRatePercentPerHour",
    "estimatedExhaustionAt",
    "exhaustsBeforeReset",
    "sampleCount",
    "basedOnHours",
  ],
  Health: ["service", "codexbar", "database", "polling", "apns"],
  CollectorHealth: ["source", "status", "durationMs", "consecutiveFailures"],
  ServerSettings: [
    "pollIntervalMinutes",
    "providerOrder",
    "hiddenProviders",
    "notificationsEnabled",
    "earlyThresholdPct",
    "dangerThresholdPct",
    "defaultRepeatIntervalMinutes",
    "quietHours",
    "alertOverrides",
  ],
  QuietHours: ["enabled", "startMinute", "endMinute", "timeZone"],
  DeviceRegistration: ["deviceID"],
  PollResult: ["ok", "polledAt", "success", "events", "snapshotChanged"],
  Readiness: ["ready", "checkedAt", "checks"],
  ReadinessCheck: ["id", "title", "status", "detail", "core"],
  ReadinessTestResult: [
    "attemptedAt",
    "alertAttempted",
    "alertAccepted",
    "widgetAttempted",
    "widgetAccepted",
    "acceptanceNote",
  ],
};

function structBody(source, name) {
  const match = source.match(new RegExp(`public struct ${name}\\b`));
  if (!match || match.index === undefined) {
    throw new Error(`Missing public struct ${name} in ${swiftPath}`);
  }
  const start = match.index;
  const brace = source.indexOf("{", start);
  let depth = 0;
  for (let i = brace; i < source.length; i++) {
    if (source[i] === "{") depth++;
    if (source[i] === "}") {
      depth--;
      if (depth === 0) {
        return source.slice(brace, i + 1);
      }
    }
  }
  throw new Error(`Unclosed struct ${name}`);
}

for (const [name, keys] of Object.entries(structs)) {
  const body = structBody(swift, name);
  for (const key of keys) {
    if (!body.includes(`public var ${key}`)) {
      throw new Error(`${name} is missing public var ${key} in ${swiftPath}`);
    }
    if (!schema.includes(`"${key}"`)) {
      throw new Error(`src/ios-schema.ts does not mention Codable key ${key} from ${name}`);
    }
  }
}

console.log("iOS Codable schema still matches review-worker/src/ios-schema.ts");
