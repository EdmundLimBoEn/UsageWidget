import {
  canSurpriseReset,
  filterEvents,
  formatBeforeAfter,
  formatDelivery,
  makePatch,
	makePollBody,
  mutationHeaders,
  resetAtForPreset,
  stageStatusLabel,
  type DemoDeliveryResult,
  type DemoEventRecord,
  type DemoEventsResponse,
  type DemoPipelineResult,
  type DemoPollResponse,
  type DemoMutationResponse,
  type DemoProvider,
  type DemoState,
  type DemoStatePatch,
  type DemoViewResponse,
  type EventFilter,
  type ResetPreset,
} from "./model";

function required<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element #${id}`);
  }
  return element as T;
}

const consoleElement = required<HTMLElement>("console");
const errorBanner = required<HTMLElement>("error-banner");
const errorMessage = required<HTMLElement>("error-message");
const retryButton = required<HTMLButtonElement>("retry");
const announcement = required<HTMLElement>("announcement");

const primaryInput = required<HTMLInputElement>("primary-usage");
const primaryOutput = required<HTMLOutputElement>("primary-output");
const primaryHelp = required<HTMLElement>("primary-help");
const secondaryInput = required<HTMLInputElement>("secondary-usage");
const secondaryOutput = required<HTMLOutputElement>("secondary-output");
const secondaryHelp = required<HTMLElement>("secondary-help");
const creditsInput = required<HTMLInputElement>("credits");
const staleInput = required<HTMLInputElement>("stale");
const providerErrorInput = required<HTMLInputElement>("provider-error");
const resetPreset = required<HTMLSelectElement>("reset-preset");

const applyPollButton = required<HTMLButtonElement>("apply-poll");
const surpriseResetButton = required<HTMLButtonElement>("surprise-reset");
const testAlertButton = required<HTMLButtonElement>("test-alert");

const pipelineKPI = required<HTMLElement>("kpi-pipeline");
const pollKPI = required<HTMLElement>("kpi-poll");
const stateKPI = required<HTMLElement>("kpi-state");
const eventsKPI = required<HTMLElement>("kpi-events");
const snapshotJSON = required<HTMLElement>("snapshot-json");
const eventList = required<HTMLElement>("event-list");

const filterButtons = Array.from(document.querySelectorAll<HTMLButtonElement>("[data-filter]"));
const actionButtons = [applyPollButton, surpriseResetButton, testAlertButton];
const inputs = [primaryInput, secondaryInput, creditsInput, staleInput, providerErrorInput, resetPreset];

let latestView: DemoViewResponse | null = null;
let resetPresetDirty = false;
let recentEvents: DemoEventRecord[] = [];
let activeFilter: EventFilter = "all";
let busy = false;
let rateLimitedUntil = 0;

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: init?.body ? { "Content-Type": "application/json", ...(init?.headers ?? {}) } : init?.headers,
  });
  if (!response.ok) {
    let detail = `${response.status} ${response.statusText}`.trim();
    let retryAfterSeconds: number | undefined;
    try {
      const body = await response.json() as { error?: string; retryAfterSeconds?: number };
      if (body.error) {
        detail = body.error;
      }
      retryAfterSeconds = body.retryAfterSeconds;
    } catch {
      // Keep the HTTP status when an error response is not JSON.
    }
    const error = new Error(detail) as Error & { status?: number; retryAfterSeconds?: number };
    error.status = response.status;
    error.retryAfterSeconds = retryAfterSeconds;
    throw error;
  }
  return response.json() as Promise<T>;
}

function announce(message: string): void {
  announcement.textContent = message;
}

function setBusy(nextBusy: boolean): void {
  busy = nextBusy;
  consoleElement.setAttribute("aria-busy", String(nextBusy));
  for (const input of inputs) {
    input.disabled = nextBusy || Date.now() < rateLimitedUntil;
  }
  for (const button of actionButtons) {
    button.disabled = nextBusy || Date.now() < rateLimitedUntil;
  }
  retryButton.disabled = nextBusy || Date.now() < rateLimitedUntil;
  if (!nextBusy) {
    updateSurpriseEligibility();
  }
}

function showFailure(error: unknown): void {
  const detail = error instanceof Error ? error.message : "Unknown request failure";
  errorMessage.textContent = `The demo API could not refresh. The last successful snapshot is still shown. ${detail}`;
  errorBanner.hidden = false;
  retryButton.hidden = false;
  announce(`Demo API error. ${detail}`);
}

function showValidation(message: string): void {
  errorMessage.textContent = message;
  errorBanner.hidden = false;
  retryButton.hidden = true;
  announce(message);
}

function clearFailure(): void {
  errorBanner.hidden = true;
  errorMessage.textContent = "";
}

async function perform(label: string, operation: () => Promise<void>, success: string): Promise<void> {
  if (busy) {
    return;
  }
  setBusy(true);
  announce(label);
  try {
    await operation();
    clearFailure();
    announce(success);
  } catch (error) {
    const requestError = error as Error & { status?: number; retryAfterSeconds?: number };
    if (requestError.status === 409) {
      await loadAll().catch(() => undefined);
    }
    if (requestError.status === 429) {
      const seconds = Math.max(1, requestError.retryAfterSeconds ?? 1);
      rateLimitedUntil = Date.now() + seconds * 1000;
      window.setTimeout(() => setBusy(false), seconds * 1000);
      showFailure(new Error(`Rate limited. Try again in ${seconds} seconds.`));
      return;
    }
    showFailure(error);
  } finally {
    setBusy(false);
  }
}

function formatPercent(value: number): string {
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value)}%`;
}

function formatClock(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(date);
}

function formatReset(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  const withinDay = Math.abs(date.getTime() - Date.now()) < 24 * 60 * 60 * 1000;
  const options: Intl.DateTimeFormatOptions = withinDay
    ? { hour: "2-digit", minute: "2-digit" }
    : { weekday: "short", hour: "2-digit", minute: "2-digit" };
  return new Intl.DateTimeFormat(undefined, options).format(date);
}

function formatDuration(durationMS: number): string {
  if (durationMS < 1000) {
    return `${durationMS}ms`;
  }
  return `${(durationMS / 1000).toFixed(1)}s`;
}

function renderControls(state: DemoState): void {
  primaryInput.value = String(state.primary.usedPercent);
  primaryOutput.value = formatPercent(state.primary.usedPercent);
  primaryHelp.textContent = `resets ${formatReset(state.primary.resetsAt)} · early 75 · danger 90`;
  secondaryInput.value = String(state.secondary.usedPercent);
  secondaryOutput.value = formatPercent(state.secondary.usedPercent);
  secondaryHelp.textContent = `resets ${formatReset(state.secondary.resetsAt)}`;
  creditsInput.value = String(state.creditsAvailable);
  staleInput.checked = state.stale;
  providerErrorInput.checked = state.providerError;
  resetPresetDirty = false;
}

function setKPI(element: HTMLElement, text: string, tone?: "good" | "warn" | "bad"): void {
  element.textContent = text;
  element.className = tone ?? "";
}

function renderKPIs(view: DemoViewResponse): void {
  if (!view.pipeline) {
    setKPI(pipelineKPI, "not run");
    setKPI(pollKPI, "—");
  } else if (view.pipeline.success) {
    setKPI(pipelineKPI, "healthy", "good");
    setKPI(pollKPI, formatClock(view.pipeline.completedAt));
  } else {
    setKPI(pipelineKPI, "failed", "bad");
    setKPI(pollKPI, formatClock(view.pipeline.completedAt));
  }

  if (view.state.providerError) {
    setKPI(stateKPI, "error", "bad");
  } else if (view.state.stale) {
    setKPI(stateKPI, "stale", "warn");
  } else {
    setKPI(stateKPI, "fresh", "good");
  }
  eventsKPI.textContent = String(recentEvents.length);
}

function providerPrimaryUsed(provider?: DemoProvider): number {
  if (!provider) {
    return Number.NaN;
  }
  const primary = provider.windows.find((window) => window.id === "demo.primary" || window.key === "primary");
  return primary?.usedPercent ?? Number.NaN;
}

function updateSurpriseEligibility(): void {
  surpriseResetButton.disabled = busy || !canSurpriseReset(providerPrimaryUsed(latestView?.snapshot?.provider));
}

function contextualStageDetail(stageID: string, pipeline: DemoPipelineResult, view: DemoViewResponse): string {
  const provider = view.snapshot?.provider;
  if (stageID === "demo_state") {
    return `state loaded · ${formatClock(view.state.updatedAt)}`;
  }
  if (stageID === "normalize" && provider) {
    return `${provider.windows.length} windows · ${provider.credits?.availableCount ?? 0} credits`;
  }
  if (stageID === "snapshot_persisted") {
    return pipeline.snapshotChanged ? "snapshot changed" : "snapshot unchanged";
  }
  if (stageID === "event_engine") {
    return `${pipeline.eventsEmitted} emitted · ${pipeline.eventsDeduplicated} deduplicated`;
  }
  if (stageID === "apns") {
    return formatDelivery(pipeline.delivery);
  }
  return "";
}

function renderPipeline(view: DemoViewResponse): void {
  const nodes = Array.from(document.querySelectorAll<HTMLElement>("[data-stage]"));
  for (const node of nodes) {
    const detail = node.querySelector<HTMLElement>(".stage-detail");
    if (!detail) {
      continue;
    }
    const stageID = node.dataset.stage ?? "";
    const stage = view.pipeline?.stages.find(({ id }) => id === stageID);
    node.classList.remove("ok", "warning", "failed", "skipped");
    if (!view.pipeline || !stage) {
      node.classList.add("skipped");
      detail.textContent = "Waiting for first poll";
      continue;
    }
    node.classList.add(stage.status);
    const outcome = stage.detail || contextualStageDetail(stageID, view.pipeline, view);
    const label = stage.status === "ok" ? "" : stageStatusLabel(stage.status);
    const pieces = [label, outcome, formatDuration(stage.durationMs)].filter(Boolean);
    detail.textContent = pieces.join(" · ");
  }
}

function jsonSpan(className: string, value: string): HTMLSpanElement {
  const span = document.createElement("span");
  span.className = className;
  span.textContent = value;
  return span;
}

function appendJSON(container: Node, value: unknown, depth = 0): void {
  const indentation = "  ";
  if (value === null || typeof value === "number" || typeof value === "boolean") {
    container.append(jsonSpan("json-value", JSON.stringify(value)));
    return;
  }
  if (typeof value === "string") {
    container.append(jsonSpan("json-string", JSON.stringify(value)));
    return;
  }
  if (Array.isArray(value)) {
    container.append("[");
    if (value.length > 0) {
      container.append("\n");
      value.forEach((item, index) => {
        container.append(indentation.repeat(depth + 1));
        appendJSON(container, item, depth + 1);
        container.append(index === value.length - 1 ? "\n" : ",\n");
      });
      container.append(indentation.repeat(depth));
    }
    container.append("]");
    return;
  }
  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>).filter(([, item]) => item !== undefined);
    container.append("{");
    if (entries.length > 0) {
      container.append("\n");
      entries.forEach(([key, item], index) => {
        container.append(indentation.repeat(depth + 1));
        container.append(jsonSpan("json-key", JSON.stringify(key)), ": ");
        appendJSON(container, item, depth + 1);
        container.append(index === entries.length - 1 ? "\n" : ",\n");
      });
      container.append(indentation.repeat(depth));
    }
    container.append("}");
  }
}

function renderSnapshot(provider: DemoProvider | undefined): void {
  snapshotJSON.replaceChildren();
  if (!provider) {
    snapshotJSON.textContent = "No normalized demo snapshot yet. Run Apply + poll to create one.";
    return;
  }
  appendJSON(snapshotJSON, provider);
}

function eventTagClass(type: string): string {
  if (type === "early_threshold" || type === "danger_threshold") {
    return "threshold";
  }
  if (type === "reset" || type === "tibo_reset") {
    return "reset";
  }
  if (type === "pipeline_error") {
    return "error";
  }
  return "info";
}

function eventDetail(event: DemoEventRecord): string {
  const parts: string[] = [];
  if (event.windowID) {
    parts.push(event.windowID);
  }
  const transition = formatBeforeAfter(event.before, event.after);
  if (transition !== "no state change") {
    parts.push(transition);
  }
  const delivery = formatDelivery(event.delivery);
  if (delivery !== "no delivery") {
    parts.push(delivery);
  }
  if (event.deduplicated) {
    parts.push("deduplicated");
  }
  if (parts.length === 0 && event.type === "manual_poll") {
    return "no events · snapshot unchanged, no push";
  }
  if (parts.length === 0 && event.type === "test_alert") {
    return "manual test alert";
  }
  if (parts.length === 0 && event.type === "pipeline_error") {
    return "demo pipeline failed";
  }
  return parts.join(" · ") || "recorded";
}

function renderEvents(): void {
  const visible = filterEvents(recentEvents, activeFilter);
  eventList.replaceChildren();
  if (visible.length === 0) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = recentEvents.length === 0 ? "No demo events recorded yet." : "No events match this filter.";
    eventList.append(empty);
    return;
  }
  for (const event of visible) {
    const article = document.createElement("article");
    article.className = "ev";

    const firstLine = document.createElement("div");
    firstLine.className = "l1";
    const time = document.createElement("time");
    time.dateTime = event.createdAt;
    time.textContent = formatClock(event.createdAt);
    const tag = document.createElement("span");
    tag.className = `tag ${eventTagClass(event.type)}`;
    tag.textContent = event.type;
    firstLine.append(time, tag);

    const detail = document.createElement("p");
    detail.textContent = eventDetail(event);
    article.append(firstLine, detail);
    eventList.append(article);
  }
}

function renderView(view: DemoViewResponse): void {
  renderControls(view.state);
  renderKPIs(view);
  renderPipeline(view);
  renderSnapshot(view.snapshot?.provider);
  updateSurpriseEligibility();
}

async function loadView(): Promise<void> {
  const view = await requestJSON<DemoViewResponse>("/v1/demo");
  if (!view.snapshot && latestView?.snapshot) {
    view.snapshot = latestView.snapshot;
  }
  latestView = view;
  renderView(view);
}

async function loadEvents(): Promise<void> {
  const response = await requestJSON<DemoEventsResponse>("/v1/demo/events?limit=50");
  recentEvents = response.events;
  renderEvents();
  eventsKPI.textContent = String(recentEvents.length);
}

async function loadAll(): Promise<void> {
  await loadView();
  await loadEvents();
}

function readPatch(): DemoStatePatch {
  const credits = creditsInput.valueAsNumber;
  if (!Number.isInteger(credits) || credits < 0 || credits > 99) {
    throw new Error("Credits must be a whole number between 0 and 99.");
  }
  return makePatch({
    primaryUsed: primaryInput.valueAsNumber,
    secondaryUsed: secondaryInput.valueAsNumber,
    credits,
    stale: staleInput.checked,
    providerError: providerErrorInput.checked,
    primaryResetsAt: resetPresetDirty ? resetAtForPreset(resetPreset.value as ResetPreset) : undefined,
  });
}

function mutationRequest<T>(path: string, body: unknown): Promise<T> {
  if (!latestView) {
    return Promise.reject(new Error("Demo state is not loaded."));
  }
  return requestJSON<T>(path, {
    method: "PATCH",
    body: JSON.stringify(body),
    headers: mutationHeaders(latestView.csrfToken, crypto.randomUUID()),
  });
}

async function patchDemo(patch: DemoStatePatch): Promise<DemoMutationResponse> {
  return mutationRequest<DemoMutationResponse>("/v1/demo", patch);
}

async function pollDemo(): Promise<DemoPollResponse> {
  if (!latestView) {
    throw new Error("Demo state is not loaded.");
  }
  return requestJSON<DemoPollResponse>("/v1/demo/poll", {
    method: "POST",
    body: JSON.stringify(makePollBody(latestView.state)),
    headers: mutationHeaders(latestView.csrfToken, crypto.randomUUID()),
  });
}

async function alertDemo(): Promise<DemoMutationResponse> {
  if (!latestView) {
    throw new Error("Demo state is not loaded.");
  }
  return requestJSON<DemoMutationResponse>("/v1/demo/alert", {
    method: "POST",
    body: "{}",
    headers: mutationHeaders(latestView.csrfToken, crypto.randomUUID()),
  });
}

primaryInput.addEventListener("input", () => {
  primaryOutput.value = formatPercent(primaryInput.valueAsNumber);
});

secondaryInput.addEventListener("input", () => {
  secondaryOutput.value = formatPercent(secondaryInput.valueAsNumber);
});

resetPreset.addEventListener("change", () => {
  resetPresetDirty = true;
});

applyPollButton.addEventListener("click", () => {
  let patch: DemoStatePatch;
  try {
    patch = readPatch();
  } catch (error) {
    showValidation(error instanceof Error ? error.message : "Invalid input.");
    return;
  }
  void perform("Applying demo state and polling.", async () => {
    await patchDemo(patch);
    await pollDemo();
    await loadAll();
  }, "Demo state applied and poll complete.");
});

surpriseResetButton.addEventListener("click", () => {
  void perform("Running surprise reset.", async () => {
    const baseline = providerPrimaryUsed(latestView?.snapshot?.provider);
    if (!canSurpriseReset(baseline)) {
      throw new Error("Surprise reset requires a normalized primary baseline of at least 20%.");
    }
    await patchDemo({ primary: { usedPercent: 5 } });
    await pollDemo();
    await loadAll();
  }, "Surprise reset complete.");
});

testAlertButton.addEventListener("click", () => {
  void perform("Sending test alert.", async () => {
    await alertDemo();
    await loadEvents();
  }, "Test alert complete and events refreshed.");
});

retryButton.addEventListener("click", () => {
  void perform("Retrying demo API.", loadAll, "Demo console refreshed.");
});

for (const button of filterButtons) {
  button.addEventListener("click", () => {
    activeFilter = button.dataset.filter as EventFilter;
    for (const candidate of filterButtons) {
      candidate.setAttribute("aria-pressed", String(candidate === button));
    }
    renderEvents();
    announce(`${button.textContent ?? "Event"} filter selected. ${filterEvents(recentEvents, activeFilter).length} events shown.`);
  });
}

void perform("Loading demo console.", loadAll, "Demo console loaded.");
