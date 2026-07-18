import { describe, expect, test } from "bun:test";

import {
  canSurpriseReset,
  filterEvents,
  findPipelineStage,
  formatBeforeAfter,
  formatDelivery,
  makePatch,
	makePollBody,
	makePollBodyForPatch,
	mutationHeaders,
  resetAtForPreset,
  stageStatusLabel,
  surpriseResetNeedsArming,
  type DemoEventRecord,
  type DemoPipelineResult,
} from "./model";

const event = (overrides: Partial<DemoEventRecord>): DemoEventRecord => ({
  id: 1,
  key: "demo.manual_poll.1",
  type: "manual_poll",
  createdAt: "2026-07-18T06:00:00Z",
  deduplicated: false,
  delivery: {
    alerts: { attempted: 0, succeeded: 0, failed: 0 },
    widgetRefresh: { attempted: 0, succeeded: 0, failed: 0 },
  },
  ...overrides,
});

describe("demo controls", () => {

  test("builds CSRF/idempotency headers and carries the current revision", () => {
    expect(mutationHeaders("csrf", "request-id")).toEqual({
      "Content-Type": "application/json", "X-Demo-CSRF": "csrf", "Idempotency-Key": "request-id",
    });
    expect(makePollBody({ primary: { usedPercent: 1, resetsAt: "x" }, secondary: { usedPercent: 2, resetsAt: "y" }, creditsAvailable: 0, stale: false, providerError: false, updatedAt: "z", revision: 7 })).toEqual({ expectedRevision: 7 });
		expect(makePollBodyForPatch({ state: { primary: { usedPercent: 1, resetsAt: "x" }, secondary: { usedPercent: 2, resetsAt: "y" }, creditsAvailable: 0, stale: false, providerError: false, updatedAt: "z", revision: 8 }, demoRunID: "patch", deliveryHealth: "ok" })).toEqual({ expectedRevision: 8 });
  });
  test("surprise reset requires an established primary baseline", () => {
    expect(canSurpriseReset(19)).toBe(false);
    expect(canSurpriseReset(20)).toBe(true);
  });

  test("surprise reset arms low, expired, and invalid baselines", () => {
    const now = new Date("2026-07-18T10:00:00Z");
    expect(surpriseResetNeedsArming(5, "2026-07-18T12:00:00Z", now)).toBe(true);
    expect(surpriseResetNeedsArming(20, "2026-07-18T09:00:00Z", now)).toBe(true);
    expect(surpriseResetNeedsArming(20, "invalid", now)).toBe(true);
    expect(surpriseResetNeedsArming(20, "2026-07-18T12:00:00Z", now)).toBe(false);
  });

  test("builds a demo-only patch", () => {
    expect(makePatch({ primaryUsed: 81, credits: 3, stale: false, providerError: false })).toEqual({
      primary: { usedPercent: 81 },
      creditsAvailable: 3,
      stale: false,
      providerError: false,
    });
  });

  test("includes secondary usage and a selected primary reset when supplied", () => {
    expect(makePatch({
      primaryUsed: 62,
      secondaryUsed: 34,
      credits: 2,
      stale: true,
      providerError: false,
      primaryResetsAt: "2026-07-18T08:08:00.000Z",
    })).toEqual({
      primary: { usedPercent: 62, resetsAt: "2026-07-18T08:08:00.000Z" },
      secondary: { usedPercent: 34 },
      creditsAvailable: 2,
      stale: true,
      providerError: false,
    });
  });

  test("maps all reset presets from a stable clock", () => {
    const now = new Date("2026-07-18T06:00:00.000Z");
    expect(resetAtForPreset("five-minutes", now)).toBe("2026-07-18T06:05:00.000Z");
    expect(resetAtForPreset("thirty-minutes", now)).toBe("2026-07-18T06:30:00.000Z");
    expect(resetAtForPreset("two-hours-eight", now)).toBe("2026-07-18T08:08:00.000Z");
    expect(resetAtForPreset("one-minute-ago", now)).toBe("2026-07-18T05:59:00.000Z");
  });
});

describe("event presentation", () => {
  const events = [
    event({ id: 1, type: "early_threshold" }),
    event({ id: 2, type: "danger_threshold" }),
    event({ id: 3, type: "reset" }),
    event({ id: 4, type: "tibo_reset" }),
    event({
      id: 5,
      type: "test_alert",
      delivery: {
        alerts: { attempted: 2, succeeded: 2, failed: 0 },
        widgetRefresh: { attempted: 1, succeeded: 1, failed: 0 },
      },
    }),
    event({ id: 6, type: "manual_poll" }),
  ];

  test("filters threshold, reset, and delivery results without mutating the feed", () => {
    expect(filterEvents(events, "all")).toEqual(events);
    expect(filterEvents(events, "thresholds").map(({ id }) => id)).toEqual([1, 2]);
    expect(filterEvents(events, "resets").map(({ id }) => id)).toEqual([3, 4]);
    expect(filterEvents(events, "delivery").map(({ id }) => id)).toEqual([5]);
    expect(events).toHaveLength(6);
  });

  test("formats usage, credit, and reset before/after values", () => {
    expect(formatBeforeAfter({ usedPercent: 62 }, { usedPercent: 78 })).toBe("62 → 78%");
    expect(formatBeforeAfter({ creditsAvailable: 1 }, { creditsAvailable: 2 })).toBe("credits 1 → 2");
    expect(formatBeforeAfter(
      { resetsAt: "2026-07-18T08:00:00Z" },
      { resetsAt: "2026-07-18T10:00:00Z" },
    )).toBe("reset time changed");
    expect(formatBeforeAfter(undefined, undefined)).toBe("no state change");
  });

  test("formats successful, partial, and empty delivery counts", () => {
    expect(formatDelivery({
      alerts: { attempted: 2, succeeded: 2, failed: 0 },
      widgetRefresh: { attempted: 2, succeeded: 1, failed: 1 },
    })).toBe("alert 2/2 · widget 1/2");
    expect(formatDelivery({
      alerts: { attempted: 0, succeeded: 0, failed: 0 },
      widgetRefresh: { attempted: 0, succeeded: 0, failed: 0 },
    })).toBe("no delivery");
  });
});

describe("pipeline presentation", () => {
  test("tolerates legacy pipeline records with null stages", () => {
    expect(findPipelineStage({ stages: null } as DemoPipelineResult, "normalize")).toBeUndefined();
  });

  test("provides readable status labels", () => {
    expect(stageStatusLabel("ok")).toBe("Complete");
    expect(stageStatusLabel("warning")).toBe("Warning");
    expect(stageStatusLabel("failed")).toBe("Failed");
    expect(stageStatusLabel("skipped")).toBe("Skipped");
  });
});
