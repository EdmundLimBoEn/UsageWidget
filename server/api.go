package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type API struct {
	cfg      Config
	store    *Store
	codexbar *CodexBarClient

	mu            sync.Mutex
	polling       bool
	lastPollAt    *time.Time
	lastSuccessAt *time.Time
}

func NewAPI(cfg Config, store *Store, codexbar *CodexBarClient) *API {
	return &API{cfg: cfg, store: store, codexbar: codexbar}
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	_, codexbarErr := a.codexbar.Fetch(ctx)

	_, dbErr := a.store.AllSettings()

	a.mu.Lock()
	polling, lastPollAt, lastSuccessAt := a.polling, a.lastPollAt, a.lastSuccessAt
	a.mu.Unlock()

	writeJSON(w, http.StatusOK, healthResponse{
		Service:       "ok",
		CodexBar:      codexbarErr == nil,
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

	writeJSON(w, http.StatusOK, snap)
}

type Settings struct {
	PollIntervalMinutes  int      `json:"pollIntervalMinutes"`
	ProviderOrder        []string `json:"providerOrder"`
	HiddenProviders      []string `json:"hiddenProviders"`
	NotificationsEnabled bool     `json:"notificationsEnabled"`
	EarlyThresholdPct    float64  `json:"earlyThresholdPct"`
	DangerThresholdPct   float64  `json:"dangerThresholdPct"`
}

var validPollIntervals = map[int]bool{1: true, 5: true, 15: true, 30: true, 60: true}

func (a *API) readSettings() (Settings, error) {
	raw, err := a.store.AllSettings()
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
	return s, nil
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
