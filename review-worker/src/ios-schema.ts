/**
 * JSON shapes the iOS app decodes in ios/Sources/Core/Models.swift and the Go
 * server emits from server/api.go / server/normalize.go. Tests fail if a
 * response is missing a required Codable field.
 */

export const SNAPSHOT_REQUIRED_KEYS = [
  "fetchedAt",
  "stale",
  "providers",
  "pollIntervalMinutes",
] as const;

export const PROVIDER_AVAILABILITY_REQUIRED_KEYS = ["id", "name", "available", "status"] as const;

export const PROVIDER_REQUIRED_KEYS = ["id", "name"] as const;

export const WINDOW_REQUIRED_KEYS = [
  "id",
  "key",
  "title",
  "usedPercent",
  "remainingPercent",
] as const;

export const FORECAST_REQUIRED_KEYS = [
  "computedAt",
  "burnRatePercentPerHour",
  "estimatedExhaustionAt",
  "exhaustsBeforeReset",
  "sampleCount",
  "basedOnHours",
] as const;

export const HEALTH_REQUIRED_KEYS = [
  "service",
  "codexbar",
  "database",
  "polling",
  "apns",
] as const;

export const COLLECTOR_REQUIRED_KEYS = [
  "source",
  "status",
  "durationMs",
  "consecutiveFailures",
] as const;

export const SETTINGS_REQUIRED_KEYS = [
  "pollIntervalMinutes",
  "providerOrder",
  "hiddenProviders",
  "notificationsEnabled",
  "earlyThresholdPct",
  "dangerThresholdPct",
  "defaultRepeatIntervalMinutes",
  "quietHours",
  "alertOverrides",
] as const;

export const QUIET_HOURS_REQUIRED_KEYS = [
  "enabled",
  "startMinute",
  "endMinute",
  "timeZone",
] as const;

export const DEVICE_REQUIRED_KEYS = ["deviceID"] as const;

export const POLL_REQUIRED_KEYS = [
  "ok",
  "polledAt",
  "success",
  "events",
  "snapshotChanged",
] as const;

export const READINESS_REQUIRED_KEYS = ["ready", "checkedAt", "checks"] as const;

export const READINESS_CHECK_REQUIRED_KEYS = [
  "id",
  "title",
  "status",
  "detail",
  "core",
] as const;

export const READINESS_TEST_REQUIRED_KEYS = [
  "attemptedAt",
  "alertAttempted",
  "alertAccepted",
  "widgetAttempted",
  "widgetAccepted",
  "acceptanceNote",
] as const;

export const SWIFT_STRUCT_FIELDS: Record<string, readonly string[]> = {
  Snapshot: SNAPSHOT_REQUIRED_KEYS,
  Provider: PROVIDER_REQUIRED_KEYS,
  ProviderAvailability: PROVIDER_AVAILABILITY_REQUIRED_KEYS,
  UsageWindow: WINDOW_REQUIRED_KEYS,
  WindowForecast: FORECAST_REQUIRED_KEYS,
  Health: HEALTH_REQUIRED_KEYS,
  CollectorHealth: COLLECTOR_REQUIRED_KEYS,
  ServerSettings: SETTINGS_REQUIRED_KEYS,
  QuietHours: QUIET_HOURS_REQUIRED_KEYS,
  DeviceRegistration: DEVICE_REQUIRED_KEYS,
  PollResult: POLL_REQUIRED_KEYS,
  Readiness: READINESS_REQUIRED_KEYS,
  ReadinessCheck: READINESS_CHECK_REQUIRED_KEYS,
  ReadinessTestResult: READINESS_TEST_REQUIRED_KEYS,
};

export function isIso8601Date(value: unknown): value is string {
  if (typeof value !== "string") {
    return false;
  }
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/.test(value)) {
    return false;
  }
  return !Number.isNaN(Date.parse(value));
}

export function hasKeys(value: unknown, keys: readonly string[]): value is Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const record = value as Record<string, unknown>;
  return keys.every((key) => key in record);
}

export function assertIsoDate(label: string, value: unknown): string {
  if (!isIso8601Date(value)) {
    throw new Error(`${label} is not an ISO-8601 UTC date the iOS JSONCoding decoder accepts: ${String(value)}`);
  }
  return value;
}

export function assertObjectWithKeys(
  label: string,
  value: unknown,
  keys: readonly string[],
): Record<string, unknown> {
  if (!hasKeys(value, keys)) {
    throw new Error(`${label} is missing required Codable keys ${keys.join(", ")}: ${JSON.stringify(value)}`);
  }
  return value;
}
