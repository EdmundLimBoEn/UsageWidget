package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestAPI(t *testing.T) (*API, *Store) {
	t.Helper()
	store := openTestStore(t)
	cfg := Config{Token: "secret-token", CodexBarURL: "http://127.0.0.1:0/unreachable"}
	codexbar := NewCodexBarClient(cfg.CodexBarURL)
	return NewAPI(cfg, store, codexbar), store
}

func doRequest(t *testing.T, api *API, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAuthMissingToken(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/v1/health", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthWrongToken(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/v1/health", "wrong-token", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHealthShape(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/v1/health", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if got.Service != "ok" {
		t.Fatalf("expected service ok, got %q", got.Service)
	}
	if got.CodexBar {
		t.Fatalf("expected codexbar false against unreachable URL")
	}
	if !got.Database {
		t.Fatalf("expected database true")
	}
	if got.APNs {
		t.Fatalf("expected apns false when APNs env unset")
	}
}

func TestSnapshotReturnsStoredData(t *testing.T) {
	api, store := newTestAPI(t)
	fetchedAt := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	snap := Snapshot{
		FetchedAt: fetchedAt,
		Stale:     true,
		Providers: []Provider{{ID: "codex", Name: "Codex"}},
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := store.SaveSnapshot(fetchedAt, payload); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	rec := doRequest(t, api, http.MethodGet, "/v1/snapshot", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if !got.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("expected fetchedAt %v, got %v", fetchedAt, got.FetchedAt)
	}
	if !got.Stale {
		t.Fatalf("expected stale flag preserved from stored data")
	}
	if len(got.Providers) != 1 || got.Providers[0].ID != "codex" {
		t.Fatalf("unexpected providers: %+v", got.Providers)
	}
	if got.PollIntervalMinutes != 5 {
		t.Fatalf("expected pollIntervalMinutes from settings default 5, got %d", got.PollIntervalMinutes)
	}
}

func TestSnapshotNotFoundWhenEmpty(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/v1/snapshot", "secret-token", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGetSettingsDefaults(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/v1/settings", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got.PollIntervalMinutes != 5 {
		t.Fatalf("expected default poll interval 5, got %d", got.PollIntervalMinutes)
	}
	if len(got.ProviderOrder) != 3 || got.ProviderOrder[0] != "codex" {
		t.Fatalf("unexpected default provider order: %+v", got.ProviderOrder)
	}
	if !got.NotificationsEnabled {
		t.Fatalf("expected notifications enabled by default")
	}
}

func TestPutSettingsUpdatesFields(t *testing.T) {
	api, _ := newTestAPI(t)
	body := []byte(`{"pollIntervalMinutes":15,"providerOrder":["claude","codex"],"earlyThresholdPct":20,"notificationsEnabled":false}`)
	rec := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got.PollIntervalMinutes != 15 {
		t.Fatalf("expected updated poll interval 15, got %d", got.PollIntervalMinutes)
	}
	if len(got.ProviderOrder) != 2 || got.ProviderOrder[0] != "claude" {
		t.Fatalf("unexpected provider order: %+v", got.ProviderOrder)
	}
	if got.EarlyThresholdPct != 20 {
		t.Fatalf("expected early threshold 20, got %v", got.EarlyThresholdPct)
	}
	if got.NotificationsEnabled {
		t.Fatalf("expected notifications disabled")
	}
	if got.DangerThresholdPct != 10 {
		t.Fatalf("expected untouched danger threshold to stay at default 10, got %v", got.DangerThresholdPct)
	}

	rec2 := doRequest(t, api, http.MethodGet, "/v1/settings", "secret-token", nil)
	var got2 Settings
	if err := json.Unmarshal(rec2.Body.Bytes(), &got2); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if got2.PollIntervalMinutes != 15 {
		t.Fatalf("expected persisted poll interval 15, got %d", got2.PollIntervalMinutes)
	}
}

func TestPutSettingsRejectsBadInterval(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", []byte(`{"pollIntervalMinutes":7}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPutSettingsRejectsBadThreshold(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", []byte(`{"earlyThresholdPct":0}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for zero threshold, got %d", rec.Code)
	}
	rec2 := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", []byte(`{"dangerThresholdPct":100}`))
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for threshold=100, got %d", rec2.Code)
	}
}

func TestDeviceUpsertRotationAndDelete(t *testing.T) {
	api, store := newTestAPI(t)

	rec := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token",
		[]byte(`{"deviceID":"dev-1","apnsToken":"apns-a","widgetToken":"widget-a"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token",
		[]byte(`{"deviceID":"dev-1","apnsToken":"apns-b","widgetToken":"widget-b"}`))
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	devices, err := store.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].APNsToken != "apns-b" {
		t.Fatalf("expected rotated token, got %+v", devices)
	}

	rec3 := doRequest(t, api, http.MethodDelete, "/v1/devices/dev-1", "secret-token", nil)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec3.Code, rec3.Body.String())
	}

	devices, err = store.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices after delete: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected no devices after delete, got %+v", devices)
	}
}

func TestDevicePartialUpdatePreservesOtherToken(t *testing.T) {
	api, store := newTestAPI(t)

	rec := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token",
		[]byte(`{"deviceID":"dev-1","apnsToken":"apns-a","widgetToken":"widget-a"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token",
		[]byte(`{"deviceID":"dev-1","widgetToken":"widget-b"}`))
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}

	var got deviceResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode device response: %v", err)
	}
	if got.APNsToken != "apns-a" {
		t.Fatalf("expected apnsToken preserved as apns-a, got %q", got.APNsToken)
	}
	if got.WidgetToken != "widget-b" {
		t.Fatalf("expected widgetToken updated to widget-b, got %q", got.WidgetToken)
	}

	devices, err := store.ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].APNsToken != "apns-a" || devices[0].WidgetToken != "widget-b" {
		t.Fatalf("unexpected persisted device: %+v", devices)
	}
}

func TestDevicePostRequiresDeviceID(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token", []byte(`{"apnsToken":"x"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHealthReportsCodexBarReachable(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"providers":[]}`))
	}))
	defer upstream.Close()

	store := openTestStore(t)
	cfg := Config{Token: "secret-token", CodexBarURL: upstream.URL}
	api := NewAPI(cfg, store, NewCodexBarClient(upstream.URL))

	rec := doRequest(t, api, http.MethodGet, "/v1/health", "secret-token", nil)
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !got.CodexBar {
		t.Fatalf("expected codexbar true when upstream reachable")
	}
}

func TestNonV1RouteBypassesAuth(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/", "", nil)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("expected non-/v1/ route to bypass auth, got 401")
	}
}

func TestContentTypeJSON(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodGet, "/v1/settings", "secret-token", nil)
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected application/json content type, got %q", ct)
	}
}

func TestFilterHidden(t *testing.T) {
	providers := []Provider{{ID: "codex"}, {ID: "kimi"}, {ID: "claude"}}
	got := filterHidden(providers, []string{"kimi"})
	if len(got) != 2 || got[0].ID != "codex" || got[1].ID != "claude" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(filterHidden(providers, nil)) != 3 {
		t.Fatalf("nil hidden list should keep all providers")
	}
}
