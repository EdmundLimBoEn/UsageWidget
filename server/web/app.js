// model.ts
function findPipelineStage(pipeline, stageID) {
  return pipeline?.stages?.find(({ id }) => id === stageID);
}
function mutationHeaders(csrfToken, idempotencyKey) {
  return { "Content-Type": "application/json", "X-Demo-CSRF": csrfToken, "Idempotency-Key": idempotencyKey };
}
function makePollBody(state) {
  return { expectedRevision: state.revision };
}
function makePollBodyForPatch(response) {
  return makePollBody(response.state);
}
function makePatch(values) {
  const primary = { usedPercent: values.primaryUsed };
  if (values.primaryResetsAt !== undefined) {
    primary.resetsAt = values.primaryResetsAt;
  }
  const patch = {
    primary,
    creditsAvailable: values.credits,
    stale: values.stale,
    providerError: values.providerError
  };
  if (values.secondaryUsed !== undefined) {
    patch.secondary = { usedPercent: values.secondaryUsed };
  }
  return patch;
}
function canSurpriseReset(primaryUsedPercent) {
  return Number.isFinite(primaryUsedPercent) && primaryUsedPercent >= 20;
}
function surpriseResetNeedsArming(primaryUsedPercent, resetsAt, now = new Date) {
  if (!canSurpriseReset(primaryUsedPercent) || !resetsAt) {
    return true;
  }
  const reset = new Date(resetsAt);
  return Number.isNaN(reset.getTime()) || reset <= now;
}
var resetOffsets = {
  "five-minutes": 5,
  "thirty-minutes": 30,
  "two-hours-eight": 128,
  "one-minute-ago": -1
};
function resetAtForPreset(preset, now = new Date) {
  return new Date(now.getTime() + resetOffsets[preset] * 60000).toISOString();
}
var thresholdTypes = new Set(["early_threshold", "danger_threshold"]);
var resetTypes = new Set(["reset", "tibo_reset"]);
function hasDelivery(event) {
  return event.delivery.alerts.attempted > 0 || event.delivery.widgetRefresh.attempted > 0;
}
function filterEvents(events, filter) {
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
function formatNumber(value) {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value);
}
function formatBeforeAfter(before, after) {
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
function formatDelivery(delivery) {
  const parts = [];
  if (delivery.alerts.attempted > 0) {
    parts.push(`alert ${delivery.alerts.succeeded}/${delivery.alerts.attempted}`);
  }
  if (delivery.widgetRefresh.attempted > 0) {
    parts.push(`widget ${delivery.widgetRefresh.succeeded}/${delivery.widgetRefresh.attempted}`);
  }
  return parts.length > 0 ? parts.join(" · ") : "no delivery";
}
function stageStatusLabel(status) {
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

// app.ts
function required(id) {
  const element = document.getElementById(id);
  if (!element) {
    throw new Error(`Missing required element #${id}`);
  }
  return element;
}
var consoleElement = required("console");
var errorBanner = required("error-banner");
var errorMessage = required("error-message");
var retryButton = required("retry");
var announcement = required("announcement");
var primaryInput = required("primary-usage");
var primaryOutput = required("primary-output");
var primaryHelp = required("primary-help");
var secondaryInput = required("secondary-usage");
var secondaryOutput = required("secondary-output");
var secondaryHelp = required("secondary-help");
var creditsInput = required("credits");
var staleInput = required("stale");
var providerErrorInput = required("provider-error");
var resetPreset = required("reset-preset");
var applyPollButton = required("apply-poll");
var surpriseResetButton = required("surprise-reset");
var testAlertButton = required("test-alert");
var pipelineKPI = required("kpi-pipeline");
var pollKPI = required("kpi-poll");
var stateKPI = required("kpi-state");
var eventsKPI = required("kpi-events");
var snapshotJSON = required("snapshot-json");
var eventList = required("event-list");
var filterButtons = Array.from(document.querySelectorAll("[data-filter]"));
var actionButtons = [applyPollButton, surpriseResetButton, testAlertButton];
var inputs = [primaryInput, secondaryInput, creditsInput, staleInput, providerErrorInput, resetPreset];
var latestView = null;
var resetPresetDirty = false;
var recentEvents = [];
var activeFilter = "all";
var busy = false;
var rateLimitedUntil = 0;
async function requestJSON(path, init) {
  const response = await fetch(path, {
    ...init,
    credentials: "same-origin",
    headers: init?.body ? { "Content-Type": "application/json", ...init?.headers ?? {} } : init?.headers
  });
  if (!response.ok) {
    let detail = `${response.status} ${response.statusText}`.trim();
    let retryAfterSeconds;
    try {
      const body = await response.json();
      if (body.error) {
        detail = body.error;
      }
      retryAfterSeconds = body.retryAfterSeconds;
    } catch {}
    const error = new Error(detail);
    error.status = response.status;
    error.retryAfterSeconds = retryAfterSeconds;
    throw error;
  }
  return response.json();
}
function announce(message) {
  announcement.textContent = message;
}
function setBusy(nextBusy) {
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
function showFailure(error) {
  const detail = error instanceof Error ? error.message : "Unknown request failure";
  errorMessage.textContent = `The demo API could not refresh. The last successful snapshot is still shown. ${detail}`;
  errorBanner.hidden = false;
  retryButton.hidden = false;
  announce(`Demo API error. ${detail}`);
}
function showValidation(message) {
  errorMessage.textContent = message;
  errorBanner.hidden = false;
  retryButton.hidden = true;
  announce(message);
}
function clearFailure() {
  errorBanner.hidden = true;
  errorMessage.textContent = "";
}
async function perform(label, operation, success) {
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
    const requestError = error;
    if (requestError.status === 409) {
      await loadAll().catch(() => {
        return;
      });
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
function formatPercent(value) {
  return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 }).format(value)}%`;
}
function formatClock(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  return new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" }).format(date);
}
function formatReset(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "—";
  }
  const withinDay = Math.abs(date.getTime() - Date.now()) < 24 * 60 * 60 * 1000;
  const options = withinDay ? { hour: "2-digit", minute: "2-digit" } : { weekday: "short", hour: "2-digit", minute: "2-digit" };
  return new Intl.DateTimeFormat(undefined, options).format(date);
}
function formatDuration(durationMS) {
  if (durationMS < 1000) {
    return `${durationMS}ms`;
  }
  return `${(durationMS / 1000).toFixed(1)}s`;
}
function renderControls(state) {
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
function setKPI(element, text, tone) {
  element.textContent = text;
  element.className = tone ?? "";
}
function renderKPIs(view) {
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
function providerPrimaryUsed(provider) {
  if (!provider) {
    return Number.NaN;
  }
  const primary = provider.windows.find((window2) => window2.id === "demo.primary" || window2.key === "primary");
  return primary?.usedPercent ?? Number.NaN;
}
function providerPrimaryReset(provider) {
  return provider?.windows.find((window2) => window2.id === "demo.primary" || window2.key === "primary")?.resetsAt;
}
function updateSurpriseEligibility() {
  surpriseResetButton.disabled = busy || !Number.isFinite(providerPrimaryUsed(latestView?.snapshot?.provider));
}
function contextualStageDetail(stageID, pipeline, view) {
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
function renderPipeline(view) {
  const nodes = Array.from(document.querySelectorAll("[data-stage]"));
  for (const node of nodes) {
    const detail = node.querySelector(".stage-detail");
    if (!detail) {
      continue;
    }
    const stageID = node.dataset.stage ?? "";
    const stage = findPipelineStage(view.pipeline, stageID);
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
function jsonSpan(className, value) {
  const span = document.createElement("span");
  span.className = className;
  span.textContent = value;
  return span;
}
function appendJSON(container, value, depth = 0) {
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
      container.append(`
`);
      value.forEach((item, index) => {
        container.append(indentation.repeat(depth + 1));
        appendJSON(container, item, depth + 1);
        container.append(index === value.length - 1 ? `
` : `,
`);
      });
      container.append(indentation.repeat(depth));
    }
    container.append("]");
    return;
  }
  if (typeof value === "object") {
    const entries = Object.entries(value).filter(([, item]) => item !== undefined);
    container.append("{");
    if (entries.length > 0) {
      container.append(`
`);
      entries.forEach(([key, item], index) => {
        container.append(indentation.repeat(depth + 1));
        container.append(jsonSpan("json-key", JSON.stringify(key)), ": ");
        appendJSON(container, item, depth + 1);
        container.append(index === entries.length - 1 ? `
` : `,
`);
      });
      container.append(indentation.repeat(depth));
    }
    container.append("}");
  }
}
function renderSnapshot(provider) {
  snapshotJSON.replaceChildren();
  if (!provider) {
    snapshotJSON.textContent = "No normalized demo snapshot yet. Run Apply + poll to create one.";
    return;
  }
  appendJSON(snapshotJSON, provider);
}
function eventTagClass(type) {
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
function eventDetail(event) {
  const parts = [];
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
function renderEvents() {
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
function renderView(view) {
  renderControls(view.state);
  renderKPIs(view);
  renderPipeline(view);
  renderSnapshot(view.snapshot?.provider);
  updateSurpriseEligibility();
}
async function loadView() {
  const view = await requestJSON("/v1/demo");
  if (!view.snapshot && latestView?.snapshot) {
    view.snapshot = latestView.snapshot;
  }
  latestView = view;
  renderView(view);
}
async function loadEvents() {
  const response = await requestJSON("/v1/demo/events?limit=50");
  recentEvents = response.events;
  renderEvents();
  eventsKPI.textContent = String(recentEvents.length);
}
async function loadAll() {
  await loadView();
  await loadEvents();
}
function readPatch() {
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
    primaryResetsAt: resetPresetDirty ? resetAtForPreset(resetPreset.value) : undefined
  });
}
function mutationRequest(path, body) {
  if (!latestView) {
    return Promise.reject(new Error("Demo state is not loaded."));
  }
  return requestJSON(path, {
    method: "PATCH",
    body: JSON.stringify(body),
    headers: mutationHeaders(latestView.csrfToken, crypto.randomUUID())
  });
}
async function patchDemo(patch) {
  return mutationRequest("/v1/demo", patch);
}
async function pollDemo(state, patched) {
  if (!latestView) {
    throw new Error("Demo state is not loaded.");
  }
  return requestJSON("/v1/demo/poll", {
    method: "POST",
    body: JSON.stringify(patched ? makePollBodyForPatch(patched) : makePollBody(state)),
    headers: mutationHeaders(latestView.csrfToken, crypto.randomUUID())
  });
}
async function alertDemo() {
  if (!latestView) {
    throw new Error("Demo state is not loaded.");
  }
  return requestJSON("/v1/demo/alert", {
    method: "POST",
    body: "{}",
    headers: mutationHeaders(latestView.csrfToken, crypto.randomUUID())
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
  let patch;
  try {
    patch = readPatch();
  } catch (error) {
    showValidation(error instanceof Error ? error.message : "Invalid input.");
    return;
  }
  perform("Applying demo state and polling.", async () => {
    const patched = await patchDemo(patch);
    await pollDemo(patched.state, patched);
    await loadAll();
  }, "Demo state applied and poll complete.");
});
surpriseResetButton.addEventListener("click", () => {
  perform("Running surprise reset.", async () => {
    const baseline = providerPrimaryUsed(latestView?.snapshot?.provider);
    if (!Number.isFinite(baseline)) {
      throw new Error("Surprise reset requires a normalized primary window.");
    }
    if (surpriseResetNeedsArming(baseline, providerPrimaryReset(latestView?.snapshot?.provider))) {
      const armed = await patchDemo({
        primary: {
          usedPercent: 20,
          resetsAt: resetAtForPreset("two-hours-eight")
        }
      });
      await pollDemo(armed.state, armed);
    }
    const patched = await patchDemo({ primary: { usedPercent: 5 } });
    await pollDemo(patched.state, patched);
    await loadAll();
  }, "Surprise reset complete.");
});
testAlertButton.addEventListener("click", () => {
  perform("Sending test alert.", async () => {
    await alertDemo();
    await loadEvents();
  }, "Test alert complete and events refreshed.");
});
retryButton.addEventListener("click", () => {
  perform("Retrying demo API.", loadAll, "Demo console refreshed.");
});
for (const button of filterButtons) {
  button.addEventListener("click", () => {
    activeFilter = button.dataset.filter;
    for (const candidate of filterButtons) {
      candidate.setAttribute("aria-pressed", String(candidate === button));
    }
    renderEvents();
    announce(`${button.textContent ?? "Event"} filter selected. ${filterEvents(recentEvents, activeFilter).length} events shown.`);
  });
}
perform("Loading demo console.", loadAll, "Demo console loaded.");
