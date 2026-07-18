export interface DemoWindowState {
  usedPercent: number;
  resetsAt: string;
}

export interface DemoState {
  primary: DemoWindowState;
  secondary: DemoWindowState;
  creditsAvailable: number;
  stale: boolean;
  providerError: boolean;
  updatedAt: string;
  revision: number;
  lastDemoRunID?: string;
}

export interface DemoStatePatch {
  primary?: Partial<DemoWindowState>;
  secondary?: Partial<DemoWindowState>;
  creditsAvailable?: number;
  stale?: boolean;
  providerError?: boolean;
}

export type StageStatus = "ok" | "warning" | "failed" | "skipped";

export interface DemoPipelineStage {
  id: string;
  status: StageStatus;
  detail?: string;
  durationMs: number;
}

export interface DeliveryCount {
  attempted: number;
  succeeded: number;
  failed: number;
}

export interface DemoDeliveryResult {
  alerts: DeliveryCount;
  widgetRefresh: DeliveryCount;
}

export interface DemoPipelineResult {
  id: number;
  startedAt: string;
  completedAt: string;
  success: boolean;
  failedStage?: string;
  snapshotChanged: boolean;
  eventsEmitted: number;
  eventsDeduplicated: number;
  stages: DemoPipelineStage[];
  delivery: DemoDeliveryResult;
  error?: string;
}

export interface EventValue {
  usedPercent?: number;
  resetsAt?: string;
  creditsAvailable?: number;
}

export interface DemoEventRecord {
  id: number;
  runID?: number;
  key: string;
  type: string;
  createdAt: string;
  windowID?: string;
  before?: EventValue;
  after?: EventValue;
  deduplicated: boolean;
  delivery: DemoDeliveryResult;
}

export interface DemoWindow {
  id: string;
  key: string;
  title: string;
  usedPercent: number;
  remainingPercent: number;
  resetsAt?: string;
}

export interface DemoCredits {
  availableCount: number;
}

export interface DemoProvider {
  id: string;
  name: string;
  error?: string;
  stale?: boolean;
  windows: DemoWindow[];
  credits?: DemoCredits;
}

export interface DemoSnapshot {
  fetchedAt: string;
  provider: DemoProvider;
}

export interface DemoViewResponse {
  state: DemoState;
  snapshot: DemoSnapshot | null;
  pipeline: DemoPipelineResult | null;
  csrfToken: string;
  deliveryHealth: "ok" | "degraded";
}

export interface DemoPollResponse {
  pipeline: DemoPipelineResult;
  events: DemoEventRecord[];
  demoRunID: string;
  deliveryHealth: "ok" | "degraded";
}

export interface DemoMutationResponse {
  state?: DemoState;
  demoRunID: string;
  deliveryHealth: "ok" | "degraded";
}

export function mutationHeaders(csrfToken: string, idempotencyKey: string): Record<string, string> {
  return { "Content-Type": "application/json", "X-Demo-CSRF": csrfToken, "Idempotency-Key": idempotencyKey };
}

export function makePollBody(state: DemoState): { expectedRevision: number } {
  return { expectedRevision: state.revision };
}

export interface DemoEventsResponse {
  events: DemoEventRecord[];
}

export type ResetPreset =
  | "five-minutes"
  | "thirty-minutes"
  | "two-hours-eight"
  | "one-minute-ago";

export type EventFilter = "all" | "thresholds" | "resets" | "delivery";

export interface DemoControlValues {
  primaryUsed: number;
  secondaryUsed?: number;
  credits: number;
  stale: boolean;
  providerError: boolean;
  primaryResetsAt?: string;
}

export function makePatch(values: DemoControlValues): DemoStatePatch {
  const primary: Partial<DemoWindowState> = { usedPercent: values.primaryUsed };
  if (values.primaryResetsAt !== undefined) {
    primary.resetsAt = values.primaryResetsAt;
  }

  const patch: DemoStatePatch = {
    primary,
    creditsAvailable: values.credits,
    stale: values.stale,
    providerError: values.providerError,
  };
  if (values.secondaryUsed !== undefined) {
    patch.secondary = { usedPercent: values.secondaryUsed };
  }
  return patch;
}

export function canSurpriseReset(primaryUsedPercent: number): boolean {
  return Number.isFinite(primaryUsedPercent) && primaryUsedPercent >= 20;
}

const resetOffsets: Record<ResetPreset, number> = {
  "five-minutes": 5,
  "thirty-minutes": 30,
  "two-hours-eight": 128,
  "one-minute-ago": -1,
};

export function resetAtForPreset(preset: ResetPreset, now = new Date()): string {
  return new Date(now.getTime() + resetOffsets[preset] * 60_000).toISOString();
}

const thresholdTypes = new Set(["early_threshold", "danger_threshold"]);
const resetTypes = new Set(["reset", "tibo_reset"]);

function hasDelivery(event: DemoEventRecord): boolean {
  return event.delivery.alerts.attempted > 0 || event.delivery.widgetRefresh.attempted > 0;
}

export function filterEvents(events: readonly DemoEventRecord[], filter: EventFilter): DemoEventRecord[] {
  if (filter === "all") {
    return [...events];
  }
  if (filter === "thresholds") {
    return events.filter(({ type }) => thresholdTypes.has(type));
  }
  if (filter === "resets") {
    return events.filter(({ type }) => resetTypes.has(type));
  }
  return events.filter(hasDelivery);
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value);
}

export function formatBeforeAfter(before?: EventValue, after?: EventValue): string {
  if (before?.usedPercent !== undefined && after?.usedPercent !== undefined) {
    return `${formatNumber(before.usedPercent)} → ${formatNumber(after.usedPercent)}%`;
  }
  if (before?.creditsAvailable !== undefined && after?.creditsAvailable !== undefined) {
    return `credits ${before.creditsAvailable} → ${after.creditsAvailable}`;
  }
  if (before?.resetsAt !== undefined || after?.resetsAt !== undefined) {
    return "reset time changed";
  }
  return "no state change";
}

export function formatDelivery(delivery: DemoDeliveryResult): string {
  const parts: string[] = [];
  if (delivery.alerts.attempted > 0) {
    parts.push(`alert ${delivery.alerts.succeeded}/${delivery.alerts.attempted}`);
  }
  if (delivery.widgetRefresh.attempted > 0) {
    parts.push(`widget ${delivery.widgetRefresh.succeeded}/${delivery.widgetRefresh.attempted}`);
  }
  return parts.length > 0 ? parts.join(" · ") : "no delivery";
}

export function stageStatusLabel(status: StageStatus): string {
  switch (status) {
    case "ok":
      return "Complete";
    case "warning":
      return "Warning";
    case "failed":
      return "Failed";
    case "skipped":
      return "Skipped";
  }
}
