import { REVIEW_HOST, REVIEW_TOKEN } from "./token";

export function isoUtc(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

export function hoursFrom(now: Date, hours: number): Date {
  return new Date(now.getTime() + hours * 60 * 60 * 1000);
}

const PROVIDER_ORDER = [
  "cursor",
  "codex",
  "claude_code",
  "copilot",
  "gemini_cli",
  "grok",
  "devin",
] as const;

export type ServerSettings = {
  pollIntervalMinutes: number;
  providerOrder: string[];
  hiddenProviders: string[];
  notificationsEnabled: boolean;
  earlyThresholdPct: number;
  dangerThresholdPct: number;
  defaultRepeatIntervalMinutes: number;
  quietHours: {
    enabled: boolean;
    startMinute: number;
    endMinute: number;
    timeZone: string;
  };
  alertOverrides: unknown[];
};

export function defaultSettings(): ServerSettings {
  return {
    pollIntervalMinutes: 5,
    providerOrder: [...PROVIDER_ORDER],
    hiddenProviders: [],
    notificationsEnabled: true,
    earlyThresholdPct: 10,
    dangerThresholdPct: 10,
    defaultRepeatIntervalMinutes: 0,
    quietHours: {
      enabled: false,
      startMinute: 1320,
      endMinute: 420,
      timeZone: "UTC",
    },
    alertOverrides: [],
  };
}

function windowForecast(
  now: Date,
  usedPercent: number,
  resetsAt: Date,
  windowLabel: string,
): Record<string, unknown> {
  const hoursLeft = Math.max((resetsAt.getTime() - now.getTime()) / 3_600_000, 0.25);
  const burn = usedPercent > 0 ? usedPercent / Math.max((24 - hoursLeft) / 2, 1) : 1;
  const remaining = 100 - usedPercent;
  const hoursToEmpty = remaining / Math.max(burn, 0.1);
  const exhaustsBeforeReset = hoursToEmpty < hoursLeft;
  const estimatedExhaustionAt = hoursFrom(now, hoursToEmpty);
  const projected = Math.min(100, usedPercent + burn * hoursLeft);
  return {
    computedAt: isoUtc(now),
    burnRatePercentPerHour: burn,
    estimatedExhaustionAt: isoUtc(estimatedExhaustionAt),
    exhaustsBeforeReset,
    sampleCount: 6,
    basedOnHours: 4,
    source: "review",
    windowLabel,
    projectedPercentAtReset: exhaustsBeforeReset ? undefined : projected,
    annotation: exhaustsBeforeReset
      ? `likely out before reset · resets ${isoUtc(resetsAt)}`
      : `~${projected.toFixed(0)}% by reset`,
  };
}

function usageWindow(args: {
  id: string;
  key: string;
  title: string;
  usedPercent: number;
  resetsAt: Date;
  now: Date;
  windowLabel: string;
}): Record<string, unknown> {
  return {
    id: args.id,
    key: args.key,
    title: args.title,
    usedPercent: args.usedPercent,
    remainingPercent: 100 - args.usedPercent,
    resetsAt: isoUtc(args.resetsAt),
    windowLabel: args.windowLabel,
    forecast: windowForecast(args.now, args.usedPercent, args.resetsAt, args.windowLabel),
  };
}

export function reviewSnapshot(now: Date, pollIntervalMinutes: number): Record<string, unknown> {
  const reset5h = hoursFrom(now, 3.5);
  const reset7d = hoursFrom(now, 4 * 24);
  const reset30d = hoursFrom(now, 12 * 24);
  return {
    fetchedAt: isoUtc(now),
    stale: false,
    pollIntervalMinutes,
    sourceKind: "review",
    providers: [
      {
        id: "cursor",
        name: "Cursor",
        stale: false,
        windows: [
          usageWindow({
            id: "cursor.plan",
            key: "plan",
            title: "Plan",
            usedPercent: 41,
            resetsAt: reset30d,
            now,
            windowLabel: "30d",
          }),
          usageWindow({
            id: "cursor.auto",
            key: "auto",
            title: "Auto",
            usedPercent: 12.5,
            resetsAt: reset30d,
            now,
            windowLabel: "30d",
          }),
        ],
      },
      {
        id: "codex",
        name: "Codex",
        stale: false,
        windows: [
          usageWindow({
            id: "codex.primary",
            key: "primary",
            title: "5h limit",
            usedPercent: 42,
            resetsAt: reset5h,
            now,
            windowLabel: "5h",
          }),
          usageWindow({
            id: "codex.weekly",
            key: "weekly",
            title: "Weekly",
            usedPercent: 28,
            resetsAt: reset7d,
            now,
            windowLabel: "7d",
          }),
        ],
        credits: { availableCount: 7 },
      },
      {
        id: "claude_code",
        name: "Claude Code",
        stale: false,
        windows: [
          usageWindow({
            id: "claude_code.primary",
            key: "primary",
            title: "5h limit",
            usedPercent: 18,
            resetsAt: hoursFrom(now, 4.2),
            now,
            windowLabel: "5h",
          }),
          usageWindow({
            id: "claude_code.weekly",
            key: "weekly",
            title: "Weekly",
            usedPercent: 33,
            resetsAt: reset7d,
            now,
            windowLabel: "7d",
          }),
        ],
      },
      {
        id: "copilot",
        name: "Copilot",
        stale: false,
        windows: [
          usageWindow({
            id: "copilot.premium",
            key: "premium",
            title: "Premium",
            usedPercent: 22,
            resetsAt: reset30d,
            now,
            windowLabel: "30d",
          }),
        ],
      },
      {
        id: "gemini_cli",
        name: "Gemini",
        stale: false,
        windows: [
          usageWindow({
            id: "gemini_cli.primary",
            key: "primary",
            title: "Daily",
            usedPercent: 9,
            resetsAt: hoursFrom(now, 14),
            now,
            windowLabel: "1d",
          }),
        ],
      },
      {
        id: "grok",
        name: "Grok",
        stale: false,
        windows: [
          usageWindow({
            id: "grok.primary",
            key: "primary",
            title: "Rate",
            usedPercent: 5,
            resetsAt: hoursFrom(now, 6),
            now,
            windowLabel: "5h",
          }),
        ],
      },
      {
        id: "devin",
        name: "Devin",
        stale: false,
        windows: [
          usageWindow({
            id: "devin.credits",
            key: "credits",
            title: "Credits",
            usedPercent: 14,
            resetsAt: reset30d,
            now,
            windowLabel: "30d",
          }),
        ],
        credits: { availableCount: 86 },
      },
    ],
  };
}

export function reviewHealth(now: Date): Record<string, unknown> {
  const at = isoUtc(now);
  return {
    service: "ok",
    codexbar: true,
    upstream: true,
    database: true,
    polling: true,
    apns: true,
    lastPollAt: at,
    lastSuccessAt: at,
    collector: {
      source: "review-synthetic",
      status: "ok",
      lastAttemptAt: at,
      lastSuccessAt: at,
      lastChangedAt: at,
      nextAttemptAt: isoUtc(hoursFrom(now, 5 / 60)),
      durationMs: 12,
      consecutiveFailures: 0,
    },
    widgetDelivery: {
      status: "ok",
      lastAttemptAt: at,
      attempted: 1,
      succeeded: 1,
      failed: 0,
    },
    version: "review",
    schemaVersion: 1,
  };
}

export function reviewReadiness(now: Date, deviceID: string): Record<string, unknown> {
  const at = isoUtc(now);
  return {
    ready: true,
    checkedAt: at,
    checks: [
      {
        id: "database",
        title: "Database and migrations",
        status: "pass",
        detail: "Schema 1 is available (synthetic review data)",
        core: true,
      },
      {
        id: "polling",
        title: "Polling loop",
        status: "pass",
        detail: "Review mode serves a static snapshot; no live collector",
        core: true,
      },
      {
        id: "collector",
        title: "Collector freshness",
        status: "pass",
        detail: "Synthetic snapshot is always current",
        core: true,
      },
      {
        id: "snapshot",
        title: "Latest snapshot",
        status: "pass",
        detail: "The latest snapshot is current",
        core: true,
      },
      {
        id: "apns",
        title: "APNs configuration",
        status: "pass",
        detail: "Push delivery is simulated in App Review",
        core: true,
      },
      {
        id: "device",
        title: "Device registration",
        status: "pass",
        detail: `Device ${deviceID} is accepted in review mode`,
        core: true,
      },
      {
        id: "alert_token",
        title: "Alert token",
        status: "pass",
        detail: "Alert delivery is simulated",
        core: true,
      },
      {
        id: "widget_token",
        title: "Widget token",
        status: "pass",
        detail: "Widget refresh is simulated",
        core: true,
      },
      {
        id: "delivery_test",
        title: "Recent device test",
        status: "pass",
        detail: "Review mode treats device tests as accepted",
        core: true,
      },
    ],
    latestTest: reviewReadinessTest(now),
  };
}

export function reviewReadinessTest(now: Date): Record<string, unknown> {
  return {
    attemptedAt: isoUtc(now),
    alertAttempted: true,
    alertAccepted: true,
    widgetAttempted: true,
    widgetAccepted: true,
    acceptanceNote:
      "App Review mode simulates APNs acceptance and does not send a real push.",
  };
}

export function reviewPoll(now: Date): Record<string, unknown> {
  return {
    ok: true,
    polledAt: isoUtc(now),
    success: true,
    events: 0,
    snapshotChanged: false,
  };
}

export function publicIndex(): Record<string, unknown> {
  return {
    service: "usagewidget-apple-review",
    mode: "review",
    serverURL: `https://${REVIEW_HOST}`,
    token: REVIEW_TOKEN,
    note: "Synthetic App Review API. This token is a public demo credential, not a production key.",
  };
}
