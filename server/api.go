package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
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

	mu            sync.Mutex
	polling       bool
	lastPollAt    *time.Time
	lastSuccessAt *time.Time
}

func NewAPI(cfg Config, store *Store, codexbar *CodexBarClient) *API {
	return &API{cfg: cfg, store: store, codexbar: codexbar}
}

func (a *API) SetPoller(p ForcePoller) {
	a.poller = p
}

func (a *API) SetNotifier(n Notifier) {
	a.notifier = n
}

func (a *API) RecordPollResult(at time.Time, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastPollAt = &at
	if success {
		a.lastSuccessAt = &at
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
	mux.HandleFunc("POST /v1/demo/alert", a.handleDemoAlert)
	return a.withAuth(mux)
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
	Service       string     `json:"service"`
	CodexBar      bool       `json:"codexbar"`
	Database      bool       `json:"database"`
	Polling       bool       `json:"polling"`
	APNs          bool       `json:"apns"`
	LastPollAt    *time.Time `json:"lastPollAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	_, dbErr := a.store.AllSettings()

	a.mu.Lock()
	polling, lastPollAt, lastSuccessAt := a.polling, a.lastPollAt, a.lastSuccessAt
	a.mu.Unlock()

	var codexbarOK bool
	if len(a.codexbar.Cmd) > 0 {
		// CLI invocations are too slow for a live health probe; trust the poller.
		codexbarOK = lastSuccessAt != nil
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		_, err := a.codexbar.Fetch(ctx)
		codexbarOK = err == nil
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Service:       "ok",
		CodexBar:      codexbarOK,
		Database:      dbErr == nil,
		Polling:       polling,
		APNs:          a.cfg.APNsEnabled(),
		LastPollAt:    lastPollAt,
		LastSuccessAt: lastSuccessAt,
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
	snap.Providers = filterHidden(snap.Providers, settings.HiddenProviders)

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
	PollIntervalMinutes  int      `json:"pollIntervalMinutes"`
	ProviderOrder        []string `json:"providerOrder"`
	HiddenProviders      []string `json:"hiddenProviders"`
	DemoProviderEnabled  bool     `json:"demoProviderEnabled"`
	NotificationsEnabled bool     `json:"notificationsEnabled"`
	EarlyThresholdPct    float64  `json:"earlyThresholdPct"`
	DangerThresholdPct   float64  `json:"dangerThresholdPct"`
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
	s.DemoProviderEnabled = raw["demo_provider_enabled"] == "true"
	if s.DemoProviderEnabled {
		s.HiddenProviders = removeString(s.HiddenProviders, "demo")
		if !containsString(s.ProviderOrder, "demo") {
			s.ProviderOrder = append(s.ProviderOrder, "demo")
		}
	}
	s.NotificationsEnabled = raw["notifications_enabled"] == "true"
	s.EarlyThresholdPct, _ = strconv.ParseFloat(raw["early_threshold_pct"], 64)
	s.DangerThresholdPct, _ = strconv.ParseFloat(raw["danger_threshold_pct"], 64)
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
	PollIntervalMinutes  *int      `json:"pollIntervalMinutes"`
	ProviderOrder        *[]string `json:"providerOrder"`
	HiddenProviders      *[]string `json:"hiddenProviders"`
	DemoProviderEnabled  *bool     `json:"demoProviderEnabled"`
	NotificationsEnabled *bool     `json:"notificationsEnabled"`
	EarlyThresholdPct    *float64  `json:"earlyThresholdPct"`
	DangerThresholdPct   *float64  `json:"dangerThresholdPct"`
}

func (a *API) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if req.DemoProviderEnabled != nil {
		set("demo_provider_enabled", strconv.FormatBool(*req.DemoProviderEnabled))
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
	if setErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": setErr.Error()})
		return
	}

	a.handleGetSettings(w, r)
}

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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceID is required"})
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
	if err := a.store.DeleteDevice(deviceID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

type demoAlertResponse struct {
	OK               bool `json:"ok"`
	DevicesAlerted   int  `json:"devicesAlerted"`
	WidgetsRefreshed int  `json:"widgetsRefreshed"`
}

func demoEvent() Event {
	return Event{
		Key:              fmt.Sprintf("demo.test_alert.%d", time.Now().UTC().UnixNano()),
		Type:             "test_alert",
		Title:            "UsageWidget demo",
		ProviderID:       "demo",
		ProviderName:     "Demo",
		WindowID:         "demo.primary",
		WindowTitle:      "Primary",
		UsedPercent:      72,
		RemainingPercent: 28,
	}
}

func (a *API) handleDemoAlert(w http.ResponseWriter, r *http.Request) {
	if a.notifier == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifier not available"})
		return
	}
	devices, err := a.store.DemoTargets(a.cfg.DemoDeviceIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ev := demoEvent()
	var alerted, refreshed int
	for _, d := range devices {
		if d.APNsToken != "" {
			if err := a.notifier.SendAlert(r.Context(), d.APNsToken, ev); err != nil {
				log.Printf("demo alert: send to %s: %v", d.DeviceID, err)
			} else {
				alerted++
			}
		}
		if d.WidgetToken != "" {
			if err := a.notifier.SendWidgetRefresh(r.Context(), d.WidgetToken); err != nil {
				log.Printf("demo alert: widget refresh %s: %v", d.DeviceID, err)
			} else {
				refreshed++
			}
		}
	}
	writeJSON(w, http.StatusOK, demoAlertResponse{
		OK:               true,
		DevicesAlerted:   alerted,
		WidgetsRefreshed: refreshed,
	})
}
