package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ForcePoller is implemented by *Poller; tests may inject stubs.
type ForcePoller interface {
	PollNow(ctx context.Context) PollResult
}

type API struct {
	cfg      Config
	store    *Store
	codexbar *CodexBarClient
	poller   ForcePoller
	notifier Notifier

	mu                  sync.Mutex
	polling             bool
	lastPollAt          *time.Time
	lastSuccessAt       *time.Time
	lastChangedAt       *time.Time
	nextPollAt          *time.Time
	lastPollDurationMS  int64
	consecutiveFailures int
	lastPollError       string
	widgetDelivery      widgetDeliveryHealth
	readinessTestAt     map[string]time.Time
}

func NewAPI(cfg Config, store *Store, codexbar *CodexBarClient) *API {
	api := &API{cfg: cfg, store: store, codexbar: codexbar, readinessTestAt: make(map[string]time.Time)}
	if results, err := store.RecentPollOutcomes(50); err == nil {
		for _, result := range results {
			api.RecordPollOutcome(result)
		}
	}
	return api
}

func (a *API) SetPoller(p ForcePoller) {
	a.poller = p
}

func (a *API) SetNotifier(n Notifier) {
	a.notifier = n
}

func (a *API) apnsOperational() bool {
	if !a.cfg.APNsEnabled() {
		return false
	}
	if status, ok := a.notifier.(notifierStatus); ok {
		return status.Enabled()
	}
	return a.notifier != nil
}

func (a *API) RecordPollResult(at time.Time, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastPollAt = &at
	if success {
		a.lastSuccessAt = &at
	}
}

func (a *API) RecordPollOutcome(result PollResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	at := result.PolledAt
	a.lastPollAt = &at
	a.lastPollDurationMS = result.DurationMS
	if result.Success {
		a.lastSuccessAt = &at
		a.consecutiveFailures = 0
		a.lastPollError = ""
		if result.SnapshotChanged {
			a.lastChangedAt = &at
		}
		return
	}
	a.consecutiveFailures++
	a.lastPollError = truncateDiagnostic(result.Error, 240)
}

func (a *API) SetNextPollAt(at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextPollAt = &at
}

func (a *API) RecordWidgetDelivery(delivery DeliveryCount, status DeliveryStatus, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	a.widgetDelivery = widgetDeliveryHealth{
		Status: string(status), LastAttemptAt: &now,
		Attempted: delivery.Attempted, Succeeded: delivery.Succeeded, Failed: delivery.Failed,
		LastError: truncateDiagnostic(detail, 180),
	}
}

func (a *API) SetPolling(polling bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.polling = polling
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", a.handleHealth)
	mux.HandleFunc("GET /v1/snapshot", a.handleGetSnapshot)
	mux.HandleFunc("GET /v1/settings", a.handleGetSettings)
	mux.HandleFunc("PUT /v1/settings", a.handlePutSettings)
	mux.HandleFunc("POST /v1/devices", a.handlePostDevice)
	mux.HandleFunc("DELETE /v1/devices/{deviceID}", a.handleDeleteDevice)
	mux.HandleFunc("POST /v1/poll", a.handleForcePoll)
	mux.HandleFunc("GET /v1/readiness/{deviceID}", a.handleGetReadiness)
	mux.HandleFunc("POST /v1/readiness/{deviceID}/test", a.handleReadinessTest)
	return a.withAuth(mux)
}

type ReadinessCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Core   bool   `json:"core"`
}

type ReadinessResponse struct {
	Ready     bool                   `json:"ready"`
	CheckedAt time.Time              `json:"checkedAt"`
	Checks    []ReadinessCheck       `json:"checks"`
	Test      *ReadinessTestResponse `json:"latestTest,omitempty"`
}

type ReadinessTestResponse struct {
	AttemptedAt     time.Time `json:"attemptedAt"`
	AlertAttempted  bool      `json:"alertAttempted"`
	AlertAccepted   bool      `json:"alertAccepted"`
	WidgetAttempted bool      `json:"widgetAttempted"`
	WidgetAccepted  bool      `json:"widgetAccepted"`
	AcceptanceNote  string    `json:"acceptanceNote"`
}

func readinessCheck(id, title, status, detail string, core bool) ReadinessCheck {
	return ReadinessCheck{ID: id, Title: title, Status: status, Detail: detail, Core: core}
}

func (a *API) readiness(deviceID string, now time.Time) (ReadinessResponse, error) {
	var checks []ReadinessCheck
	_, dbErr := a.store.SchemaVersion()
	if dbErr != nil {
		checks = append(checks, readinessCheck("database", "Database and migrations", "fail", "Database check failed", true))
	} else {
		checks = append(checks, readinessCheck("database", "Database and migrations", "pass", fmt.Sprintf("Schema %d is available", CurrentSchemaVersion), true))
	}
	a.mu.Lock()
	polling, lastSuccess := a.polling, a.lastSuccessAt
	a.mu.Unlock()
	if polling {
		checks = append(checks, readinessCheck("polling", "Polling loop", "pass", "Polling is running", true))
	} else {
		checks = append(checks, readinessCheck("polling", "Polling loop", "warning", "Polling is not currently marked running", true))
	}
	settings, err := loadSettings(a.store)
	if err != nil {
		return ReadinessResponse{}, err
	}
	freshLimit := time.Duration(settings.PollIntervalMinutes*2) * time.Minute
	if freshLimit < 10*time.Minute {
		freshLimit = 10 * time.Minute
	}
	if lastSuccess == nil {
		checks = append(checks, readinessCheck("collector", "Collector freshness", "fail", "No successful poll has been recorded", true))
	} else if now.Sub(*lastSuccess) > freshLimit {
		checks = append(checks, readinessCheck("collector", "Collector freshness", "fail", "The latest successful poll is too old", true))
	} else {
		checks = append(checks, readinessCheck("collector", "Collector freshness", "pass", "A recent poll succeeded", true))
	}
	fetched, payload, ok, snapErr := a.store.LatestSnapshot()
	if snapErr != nil || !ok {
		checks = append(checks, readinessCheck("snapshot", "Latest snapshot", "fail", "No decodable snapshot is available", true))
	} else {
		var snap Snapshot
		decodeErr := json.Unmarshal(payload, &snap)
		switch {
		case decodeErr != nil:
			checks = append(checks, readinessCheck("snapshot", "Latest snapshot", "fail", "The stored snapshot cannot be decoded", true))
		case snap.Stale || now.Sub(fetched) > freshLimit:
			checks = append(checks, readinessCheck("snapshot", "Latest snapshot", "fail", "The latest snapshot is stale", true))
		default:
			checks = append(checks, readinessCheck("snapshot", "Latest snapshot", "pass", "The latest snapshot is current", true))
		}
	}
	if a.apnsOperational() {
		checks = append(checks, readinessCheck("apns", "APNs configuration", "pass", "APNs credentials are configured", true))
	} else {
		checks = append(checks, readinessCheck("apns", "APNs configuration", "fail", "APNs is not configured; dashboard-only mode is available", true))
	}
	device, found, err := a.store.GetDevice(deviceID)
	if err != nil {
		return ReadinessResponse{}, err
	}
	if !found {
		checks = append(checks, readinessCheck("device", "Device registration", "fail", "This device is not registered", true))
	} else {
		checks = append(checks, readinessCheck("device", "Device registration", "pass", "This device is registered", true))
	}
	if found && device.APNsToken != "" {
		checks = append(checks, readinessCheck("alert_token", "Alert token", "pass", "An alert token is present", true))
	} else {
		checks = append(checks, readinessCheck("alert_token", "Alert token", "fail", "No alert token is registered", true))
	}
	if found && device.WidgetToken != "" {
		checks = append(checks, readinessCheck("widget_token", "Widget token", "pass", "A widget token is present", true))
	} else {
		checks = append(checks, readinessCheck("widget_token", "Widget token", "warning", "No widget push token is registered", true))
	}
	result, hasTest, err := a.store.LatestDeviceTestResult(deviceID)
	if err != nil {
		return ReadinessResponse{}, err
	}
	var test *ReadinessTestResponse
	if hasTest {
		test = &ReadinessTestResponse{AttemptedAt: result.AttemptedAt, AlertAttempted: result.AlertAttempted, AlertAccepted: result.AlertAccepted, WidgetAttempted: result.WidgetAttempted, WidgetAccepted: result.WidgetAccepted, AcceptanceNote: "APNs acceptance does not prove iOS displayed the notification."}
	}
	testPass := hasTest && result.AlertAccepted && result.WidgetAccepted && now.Sub(result.AttemptedAt) <= 15*time.Minute
	if testPass {
		checks = append(checks, readinessCheck("delivery_test", "Recent device test", "pass", "APNs accepted a test for this device in the last 15 minutes", true))
	} else if hasTest {
		checks = append(checks, readinessCheck("delivery_test", "Recent device test", "fail", "Run another device test; the last accepted test is missing, failed, or older than 15 minutes", true))
	} else {
		checks = append(checks, readinessCheck("delivery_test", "Recent device test", "fail", "No device-specific test has been recorded", true))
	}
	ready := true
	for _, c := range checks {
		if c.Core && c.Status != "pass" {
			ready = false
		}
	}
	return ReadinessResponse{Ready: ready, CheckedAt: now.UTC(), Checks: checks, Test: test}, nil
}

func (a *API) handleGetReadiness(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if deviceID == "" || len(deviceID) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deviceID"})
		return
	}
	result, err := a.readiness(deviceID, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleReadinessTest(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if deviceID == "" || len(deviceID) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deviceID"})
		return
	}
	now := time.Now().UTC()
	a.mu.Lock()
	last := a.readinessTestAt[deviceID]
	if !last.IsZero() && now.Sub(last) < time.Minute {
		retry := int(time.Minute.Seconds() - now.Sub(last).Seconds())
		a.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "readiness test is limited to once per minute"})
		return
	}
	a.readinessTestAt[deviceID] = now
	a.mu.Unlock()
	device, found, err := a.store.GetDevice(deviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device is not registered"})
		return
	}
	result := DeviceTestResult{DeviceID: deviceID, AttemptedAt: now}
	var details []string
	if device.APNsToken != "" {
		result.AlertAttempted = true
		if a.apnsOperational() {
			ev := readinessTestEvent()
			if err := a.notifier.SendAlert(r.Context(), device.APNsToken, ev); err == nil {
				result.AlertAccepted = true
			} else {
				details = append(details, "alert: "+truncateDiagnostic(err.Error(), 100))
			}
		}
	}
	if device.WidgetToken != "" {
		result.WidgetAttempted = true
		if a.apnsOperational() {
			if err := a.notifier.SendWidgetRefresh(r.Context(), device.WidgetToken); err == nil {
				result.WidgetAccepted = true
			} else {
				details = append(details, "widget: "+truncateDiagnostic(err.Error(), 100))
			}
		}
	}
	result.Detail = strings.Join(details, "; ")
	if err := a.store.SaveDeviceTestResult(result); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := ReadinessTestResponse{AttemptedAt: now, AlertAttempted: result.AlertAttempted, AlertAccepted: result.AlertAccepted, WidgetAttempted: result.WidgetAttempted, WidgetAccepted: result.WidgetAccepted, AcceptanceNote: "APNs acceptance does not prove iOS displayed the notification."}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !tokensEqual(token, a.cfg.Token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tokensEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type healthResponse struct {
	Service        string               `json:"service"`
	CodexBar       bool                 `json:"codexbar"`
	Database       bool                 `json:"database"`
	Polling        bool                 `json:"polling"`
	APNs           bool                 `json:"apns"`
	LastPollAt     *time.Time           `json:"lastPollAt"`
	LastSuccessAt  *time.Time           `json:"lastSuccessAt"`
	Collector      collectorHealth      `json:"collector"`
	WidgetDelivery widgetDeliveryHealth `json:"widgetDelivery"`
	Version        string               `json:"version"`
	SchemaVersion  int                  `json:"schemaVersion"`
}

type collectorHealth struct {
	Source              string     `json:"source"`
	Status              string     `json:"status"`
	LastAttemptAt       *time.Time `json:"lastAttemptAt"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt"`
	LastChangedAt       *time.Time `json:"lastChangedAt"`
	NextAttemptAt       *time.Time `json:"nextAttemptAt"`
	DurationMS          int64      `json:"durationMs"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LastError           string     `json:"lastError,omitempty"`
}

type widgetDeliveryHealth struct {
	Status        string     `json:"status"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	Attempted     int        `json:"attempted"`
	Succeeded     int        `json:"succeeded"`
	Failed        int        `json:"failed"`
	LastError     string     `json:"lastError,omitempty"`
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, dbErr := a.store.AllSettings()
	schemaVersion, schemaErr := a.store.SchemaVersion()
	if schemaErr != nil {
		dbErr = schemaErr
	}

	a.mu.Lock()
	polling, lastPollAt, lastSuccessAt := a.polling, a.lastPollAt, a.lastSuccessAt
	collector := collectorHealth{
		Source: a.codexbar.Source, LastAttemptAt: a.lastPollAt, LastSuccessAt: a.lastSuccessAt,
		LastChangedAt: a.lastChangedAt, NextAttemptAt: a.nextPollAt, DurationMS: a.lastPollDurationMS,
		ConsecutiveFailures: a.consecutiveFailures, LastError: a.lastPollError,
	}
	widgetDelivery := a.widgetDelivery
	a.mu.Unlock()
	if collector.Source == "" {
		collector.Source = "http"
	}
	switch {
	case collector.ConsecutiveFailures > 0 && collector.LastSuccessAt == nil:
		collector.Status = "down"
	case collector.ConsecutiveFailures > 0:
		collector.Status = "degraded"
	case collector.LastSuccessAt != nil:
		collector.Status = "ok"
	default:
		collector.Status = "starting"
	}
	codexbarOK := collector.Status == "ok"

	writeJSON(w, http.StatusOK, healthResponse{
		Service:        "ok",
		CodexBar:       codexbarOK,
		Database:       dbErr == nil,
		Polling:        polling,
		APNs:           a.apnsOperational(),
		LastPollAt:     lastPollAt,
		LastSuccessAt:  lastSuccessAt,
		Collector:      collector,
		WidgetDelivery: widgetDelivery,
		Version:        Version,
		SchemaVersion:  schemaVersion,
	})
}

func (a *API) handleGetSnapshot(w http.ResponseWriter, r *http.Request) {
	fetchedAt, payload, ok, err := a.store.LatestSnapshot()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no snapshot available"})
		return
	}

	var snap Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "corrupt stored snapshot"})
		return
	}
	snap.FetchedAt = fetchedAt

	settings, err := a.readSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	snap.PollIntervalMinutes = settings.PollIntervalMinutes
	freshLimit := time.Duration(settings.PollIntervalMinutes*2) * time.Minute
	if freshLimit < 10*time.Minute {
		freshLimit = 10 * time.Minute
	}
	if snap.Stale || time.Since(fetchedAt) > freshLimit {
		clearForecasts(&snap)
	}
	snap.Providers = filterHidden(snap.Providers, settings.HiddenProviders)
	for i := range snap.Providers {
		snap.Providers[i].Raw = nil
	}

	writeJSON(w, http.StatusOK, snap)
}

func filterHidden(providers []Provider, hiddenIDs []string) []Provider {
	if len(hiddenIDs) == 0 {
		return providers
	}
	hidden := make(map[string]bool, len(hiddenIDs))
	for _, id := range hiddenIDs {
		hidden[id] = true
	}
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if !hidden[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

type Settings struct {
	PollIntervalMinutes          int             `json:"pollIntervalMinutes"`
	ProviderOrder                []string        `json:"providerOrder"`
	HiddenProviders              []string        `json:"hiddenProviders"`
	NotificationsEnabled         bool            `json:"notificationsEnabled"`
	EarlyThresholdPct            float64         `json:"earlyThresholdPct"`
	DangerThresholdPct           float64         `json:"dangerThresholdPct"`
	DefaultRepeatIntervalMinutes int             `json:"defaultRepeatIntervalMinutes"`
	QuietHours                   QuietHours      `json:"quietHours"`
	AlertOverrides               []AlertOverride `json:"alertOverrides"`
}

type AlertRule struct {
	Enabled               bool    `json:"enabled"`
	EarlyThresholdPct     float64 `json:"earlyThresholdPct"`
	DangerThresholdPct    float64 `json:"dangerThresholdPct"`
	RepeatIntervalMinutes int     `json:"repeatIntervalMinutes"`
}

type QuietHours struct {
	Enabled     bool   `json:"enabled"`
	StartMinute int    `json:"startMinute"`
	EndMinute   int    `json:"endMinute"`
	TimeZone    string `json:"timeZone"`
}

type AlertOverride struct {
	ProviderID string    `json:"providerID"`
	WindowID   *string   `json:"windowID,omitempty"`
	Rule       AlertRule `json:"rule"`
}

var validRepeatIntervals = map[int]bool{0: true, 60: true, 180: true, 360: true}

func (s Settings) GlobalRule() AlertRule {
	return AlertRule{Enabled: s.NotificationsEnabled, EarlyThresholdPct: s.EarlyThresholdPct, DangerThresholdPct: s.DangerThresholdPct, RepeatIntervalMinutes: s.DefaultRepeatIntervalMinutes}
}

func (s Settings) EffectiveRule(providerID, windowID string) AlertRule {
	rule := s.GlobalRule()
	for _, o := range s.AlertOverrides {
		if o.ProviderID == providerID && o.WindowID == nil {
			rule = o.Rule
		}
	}
	for _, o := range s.AlertOverrides {
		if o.ProviderID == providerID && o.WindowID != nil && *o.WindowID == windowID {
			rule = o.Rule
		}
	}
	return rule
}

func policyFingerprint(rule AlertRule) string {
	b, _ := json.Marshal(rule)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:12])
}

var validPollIntervals = map[int]bool{1: true, 5: true, 15: true, 30: true, 60: true}

func (a *API) readSettings() (Settings, error) {
	return loadSettings(a.store)
}

func loadSettings(store *Store) (Settings, error) {
	raw, err := store.AllSettings()
	if err != nil {
		return Settings{}, err
	}

	var s Settings
	s.PollIntervalMinutes, _ = strconv.Atoi(raw["poll_interval_minutes"])
	json.Unmarshal([]byte(raw["provider_order"]), &s.ProviderOrder)
	json.Unmarshal([]byte(raw["hidden_providers"]), &s.HiddenProviders)
	s.NotificationsEnabled = raw["notifications_enabled"] == "true"
	s.EarlyThresholdPct, _ = strconv.ParseFloat(raw["early_threshold_pct"], 64)
	s.DangerThresholdPct, _ = strconv.ParseFloat(raw["danger_threshold_pct"], 64)
	s.DefaultRepeatIntervalMinutes, _ = strconv.Atoi(raw["default_repeat_interval_minutes"])
	if err := json.Unmarshal([]byte(raw["quiet_hours"]), &s.QuietHours); err != nil {
		s.QuietHours = QuietHours{TimeZone: "UTC", StartMinute: 1320, EndMinute: 420}
	}
	s.AlertOverrides, err = store.ListAlertOverrides()
	if err != nil {
		return Settings{}, err
	}
	return s, nil
}

func removeString(values []string, target string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			out = append(out, value)
		}
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.readSettings()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

type updateSettingsRequest struct {
	PollIntervalMinutes          *int             `json:"pollIntervalMinutes"`
	ProviderOrder                *[]string        `json:"providerOrder"`
	HiddenProviders              *[]string        `json:"hiddenProviders"`
	NotificationsEnabled         *bool            `json:"notificationsEnabled"`
	EarlyThresholdPct            *float64         `json:"earlyThresholdPct"`
	DangerThresholdPct           *float64         `json:"dangerThresholdPct"`
	DefaultRepeatIntervalMinutes *int             `json:"defaultRepeatIntervalMinutes"`
	QuietHours                   *QuietHours      `json:"quietHours"`
	AlertOverrides               *[]AlertOverride `json:"alertOverrides"`
}

func (a *API) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if !decodeAPIRequest(w, r, &req) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if req.PollIntervalMinutes != nil && !validPollIntervals[*req.PollIntervalMinutes] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pollIntervalMinutes must be one of 1, 5, 15, 30, 60"})
		return
	}
	if req.EarlyThresholdPct != nil && !inRange(*req.EarlyThresholdPct) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "earlyThresholdPct must be in (0, 100)"})
		return
	}
	if req.DangerThresholdPct != nil && !inRange(*req.DangerThresholdPct) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dangerThresholdPct must be in (0, 100)"})
		return
	}
	if req.DefaultRepeatIntervalMinutes != nil && !validRepeatIntervals[*req.DefaultRepeatIntervalMinutes] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "defaultRepeatIntervalMinutes must be one of 0, 60, 180, 360"})
		return
	}
	if req.QuietHours != nil {
		if err := validateQuietHours(*req.QuietHours); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if req.AlertOverrides != nil {
		if err := validateAlertOverrides(*req.AlertOverrides); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	var setErr error
	set := func(key, value string) {
		if setErr == nil {
			setErr = a.store.SetSetting(key, value)
		}
	}
	if req.PollIntervalMinutes != nil {
		set("poll_interval_minutes", strconv.Itoa(*req.PollIntervalMinutes))
	}
	if req.ProviderOrder != nil {
		b, _ := json.Marshal(*req.ProviderOrder)
		set("provider_order", string(b))
	}
	if req.HiddenProviders != nil {
		b, _ := json.Marshal(*req.HiddenProviders)
		set("hidden_providers", string(b))
	}
	if req.NotificationsEnabled != nil {
		set("notifications_enabled", strconv.FormatBool(*req.NotificationsEnabled))
	}
	if req.EarlyThresholdPct != nil {
		set("early_threshold_pct", strconv.FormatFloat(*req.EarlyThresholdPct, 'f', -1, 64))
	}
	if req.DangerThresholdPct != nil {
		set("danger_threshold_pct", strconv.FormatFloat(*req.DangerThresholdPct, 'f', -1, 64))
	}
	if req.DefaultRepeatIntervalMinutes != nil {
		set("default_repeat_interval_minutes", strconv.Itoa(*req.DefaultRepeatIntervalMinutes))
	}
	if req.QuietHours != nil {
		b, _ := json.Marshal(*req.QuietHours)
		set("quiet_hours", string(b))
	}
	if setErr == nil && req.AlertOverrides != nil {
		setErr = a.store.ReplaceAlertOverrides(*req.AlertOverrides)
	}
	if setErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": setErr.Error()})
		return
	}

	a.handleGetSettings(w, r)
}

func validateRule(rule AlertRule) error {
	if !inRange(rule.EarlyThresholdPct) || !inRange(rule.DangerThresholdPct) {
		return fmt.Errorf("alert thresholds must be in (0, 100)")
	}
	if !validRepeatIntervals[rule.RepeatIntervalMinutes] {
		return fmt.Errorf("repeatIntervalMinutes must be one of 0, 60, 180, 360")
	}
	return nil
}

func validateAlertOverrides(overrides []AlertOverride) error {
	seen := map[string]bool{}
	for _, o := range overrides {
		if strings.TrimSpace(o.ProviderID) == "" || len(o.ProviderID) > 128 {
			return fmt.Errorf("alert override providerID is required")
		}
		key := o.ProviderID + "\x00"
		if o.WindowID != nil {
			if strings.TrimSpace(*o.WindowID) == "" || len(*o.WindowID) > 128 {
				return fmt.Errorf("alert override windowID is invalid")
			}
			key += *o.WindowID
		}
		if seen[key] {
			return fmt.Errorf("duplicate alert override")
		}
		seen[key] = true
		if err := validateRule(o.Rule); err != nil {
			return err
		}
	}
	return nil
}

func validateQuietHours(q QuietHours) error {
	if q.StartMinute < 0 || q.StartMinute >= 1440 || q.EndMinute < 0 || q.EndMinute >= 1440 || q.StartMinute == q.EndMinute {
		return fmt.Errorf("quiet hours require different minutes in 0...1439")
	}
	if _, err := time.LoadLocation(q.TimeZone); err != nil {
		return fmt.Errorf("quiet hours timeZone must be a valid IANA timezone")
	}
	return nil
}

func (q QuietHours) Contains(now time.Time) bool {
	if !q.Enabled {
		return false
	}
	loc, err := time.LoadLocation(q.TimeZone)
	if err != nil {
		return false
	}
	local := now.In(loc)
	minute := local.Hour()*60 + local.Minute()
	if q.StartMinute < q.EndMinute {
		return minute >= q.StartMinute && minute < q.EndMinute
	}
	return minute >= q.StartMinute || minute < q.EndMinute
}

var Version = "dev"

func inRange(pct float64) bool {
	return pct > 0 && pct < 100
}

type deviceRequest struct {
	DeviceID    string  `json:"deviceID"`
	APNsToken   *string `json:"apnsToken"`
	WidgetToken *string `json:"widgetToken"`
}

type deviceResponse struct {
	DeviceID    string `json:"deviceID"`
	APNsToken   string `json:"apnsToken"`
	WidgetToken string `json:"widgetToken"`
}

func (a *API) handlePostDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if !decodeAPIRequest(w, r, &req) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.DeviceID == "" || len(req.DeviceID) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceID is required"})
		return
	}
	if (req.APNsToken != nil && len(*req.APNsToken) > 1024) ||
		(req.WidgetToken != nil && len(*req.WidgetToken) > 1024) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device token is too long"})
		return
	}

	existing, _, err := a.store.GetDevice(req.DeviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := deviceResponse{DeviceID: req.DeviceID, APNsToken: existing.APNsToken, WidgetToken: existing.WidgetToken}
	if req.APNsToken != nil {
		resp.APNsToken = *req.APNsToken
	}
	if req.WidgetToken != nil {
		resp.WidgetToken = *req.WidgetToken
	}

	if err := a.store.UpsertDevice(resp.DeviceID, resp.APNsToken, resp.WidgetToken); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if deviceID == "" || len(deviceID) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deviceID"})
		return
	}
	if err := a.store.DeleteDevice(deviceID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeAPIRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

type pollResponse struct {
	OK              bool      `json:"ok"`
	PolledAt        time.Time `json:"polledAt"`
	Success         bool      `json:"success"`
	Events          int       `json:"events"`
	SnapshotChanged bool      `json:"snapshotChanged"`
	Error           string    `json:"error,omitempty"`
}

func (a *API) handleForcePoll(w http.ResponseWriter, r *http.Request) {
	if a.poller == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "poller not available"})
		return
	}
	result := a.poller.PollNow(r.Context())
	resp := pollResponse{
		OK:              result.Success,
		PolledAt:        result.PolledAt,
		Success:         result.Success,
		Events:          result.Events,
		SnapshotChanged: result.SnapshotChanged,
		Error:           result.Error,
	}
	if !result.Success {
		writeJSON(w, http.StatusBadGateway, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func readinessTestEvent() Event {
	return Event{
		Key:          fmt.Sprintf("readiness.test_alert.%d", time.Now().UTC().UnixNano()),
		Type:         "readiness_test",
		Title:        "UsageWidget readiness test",
		ProviderID:   "system",
		ProviderName: "UsageWidget",
		WindowID:     "readiness",
		WindowTitle:  "Delivery",
	}
}
