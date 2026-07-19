package server

import (
	"bytes"
	"context"
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
		Providers: []Provider{{ID: "codex", Name: "Codex", Raw: json.RawMessage(`{"private":"upstream"}`)}},
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
	if got.Providers[0].Raw != nil || strings.Contains(rec.Body.String(), `"private"`) {
		t.Fatalf("phone snapshot leaked raw upstream payload: %s", rec.Body.String())
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
	if got.DemoProviderEnabled {
		t.Fatalf("expected demo provider disabled by default")
	}
}

func TestPutSettingsUpdatesFields(t *testing.T) {
	api, _ := newTestAPI(t)
	body := []byte(`{"pollIntervalMinutes":15,"providerOrder":["claude","codex"],"demoProviderEnabled":true,"earlyThresholdPct":20,"notificationsEnabled":false}`)
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
	if len(got.ProviderOrder) != 3 || got.ProviderOrder[0] != "claude" {
		t.Fatalf("unexpected provider order: %+v", got.ProviderOrder)
	}
	if got.EarlyThresholdPct != 20 {
		t.Fatalf("expected early threshold 20, got %v", got.EarlyThresholdPct)
	}
	if got.NotificationsEnabled {
		t.Fatalf("expected notifications disabled")
	}
	if !got.DemoProviderEnabled {
		t.Fatalf("expected demo provider enabled")
	}
	if got.ProviderOrder[len(got.ProviderOrder)-1] != "demo" {
		t.Fatalf("expected enabled demo provider appended to order: %+v", got.ProviderOrder)
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

func TestPutSettingsPersistsAndValidatesAlertPolicy(t *testing.T) {
	api, _ := newTestAPI(t)
	body := []byte(`{"defaultRepeatIntervalMinutes":180,"quietHours":{"enabled":true,"startMinute":1320,"endMinute":420,"timeZone":"Asia/Singapore"},"alertOverrides":[{"providerID":"codex","rule":{"enabled":true,"earlyThresholdPct":25,"dangerThresholdPct":12,"repeatIntervalMinutes":60}},{"providerID":"codex","windowID":"codex.primary","rule":{"enabled":false,"earlyThresholdPct":30,"dangerThresholdPct":5,"repeatIntervalMinutes":0}}]}`)
	rec := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.DefaultRepeatIntervalMinutes != 180 || !got.QuietHours.Enabled || len(got.AlertOverrides) != 2 {
		t.Fatalf("settings=%+v", got)
	}
	if got.EffectiveRule("codex", "codex.primary").Enabled {
		t.Fatal("exact window override not effective")
	}
	bad := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", []byte(`{"quietHours":{"enabled":true,"startMinute":60,"endMinute":60,"timeZone":"UTC"}}`))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("equal quiet hours status=%d", bad.Code)
	}
	bad = doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", []byte(`{"defaultRepeatIntervalMinutes":30}`))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("repeat status=%d", bad.Code)
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

func TestAPIRejectsUnknownAndOversizedJSON(t *testing.T) {
	api, _ := newTestAPI(t)
	unknown := doRequest(t, api, http.MethodPut, "/v1/settings", "secret-token", []byte(`{"unexpected":true}`))
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown field rejection, got %d", unknown.Code)
	}
	oversized := []byte(`{"deviceID":"` + strings.Repeat("a", 17<<10) + `"}`)
	rec := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token", oversized)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected oversized body rejection, got %d", rec.Code)
	}
}

func TestHealthIsPassiveAndReportsLastPollOutcome(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"providers":[]}`))
	}))
	defer upstream.Close()

	store := openTestStore(t)
	cfg := Config{Token: "secret-token", CodexBarURL: upstream.URL}
	api := NewAPI(cfg, store, NewCodexBarClient(upstream.URL))
	api.RecordPollOutcome(PollResult{PolledAt: time.Now().UTC(), Success: true})

	rec := doRequest(t, api, http.MethodGet, "/v1/health", "secret-token", nil)
	var got healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !got.CodexBar {
		t.Fatalf("expected codexbar true after a successful poll")
	}
	if requests != 0 {
		t.Fatalf("health must not fetch upstream, got %d request(s)", requests)
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

type stubPoller struct {
	calls  int
	result PollResult
}

func (s *stubPoller) PollNow(context.Context) PollResult {
	s.calls++
	return s.result
}

type recordingNotifier struct {
	alerts  []Event
	widgets int
}

func (r *recordingNotifier) SendAlert(_ context.Context, _ string, ev Event) error {
	r.alerts = append(r.alerts, ev)
	return nil
}

func (r *recordingNotifier) SendWidgetRefresh(_ context.Context, _ string) error {
	r.widgets++
	return nil
}

func TestReadinessTestTargetsDevicePersistsAndRateLimits(t *testing.T) {
	store := openTestStore(t)
	cfg := Config{Token: "secret-token", CodexBarURL: "http://127.0.0.1:0", APNsKeyPath: "key", APNsKeyID: "kid", APNsTeamID: "team", APNsBundleID: "app"}
	api := NewAPI(cfg, store, NewCodexBarClient(cfg.CodexBarURL))
	notifier := &recordingNotifier{}
	api.SetNotifier(notifier)
	if err := store.UpsertDevice("phone-a", "alert-a", "widget-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDevice("phone-b", "alert-b", "widget-b"); err != nil {
		t.Fatal(err)
	}
	rec := doRequest(t, api, http.MethodPost, "/v1/readiness/phone-a/test", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("test status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(notifier.alerts) != 1 || notifier.widgets != 1 {
		t.Fatalf("delivery alerts=%d widgets=%d", len(notifier.alerts), notifier.widgets)
	}
	var result ReadinessTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.AlertAccepted || !result.WidgetAccepted {
		t.Fatalf("result=%+v", result)
	}
	stored, ok, err := store.LatestDeviceTestResult("phone-a")
	if err != nil || !ok || !stored.AlertAccepted {
		t.Fatalf("stored=%+v ok=%v err=%v", stored, ok, err)
	}
	rec = doRequest(t, api, http.MethodPost, "/v1/readiness/phone-a/test", "secret-token", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d", rec.Code)
	}
	rec = doRequest(t, api, http.MethodGet, "/v1/readiness/phone-a", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "alert-a") || strings.Contains(rec.Body.String(), "widget-a") {
		t.Fatalf("readiness leaked token: %s", rec.Body.String())
	}
}

func TestForcePollRequiresAuth(t *testing.T) {
	api, _ := newTestAPI(t)
	api.SetPoller(&stubPoller{result: PollResult{Success: true, PolledAt: time.Now().UTC()}})
	rec := doRequest(t, api, http.MethodPost, "/v1/poll", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestForcePollUnavailableWithoutPoller(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodPost, "/v1/poll", "secret-token", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestForcePollSuccess(t *testing.T) {
	api, _ := newTestAPI(t)
	polledAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	stub := &stubPoller{result: PollResult{
		PolledAt:        polledAt,
		Success:         true,
		Events:          2,
		SnapshotChanged: true,
	}}
	api.SetPoller(stub)

	rec := doRequest(t, api, http.MethodPost, "/v1/poll", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if stub.calls != 1 {
		t.Fatalf("expected 1 poll call, got %d", stub.calls)
	}
	var got pollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || !got.Success || got.Events != 2 || !got.SnapshotChanged {
		t.Fatalf("unexpected response: %+v", got)
	}
	if !got.PolledAt.Equal(polledAt) {
		t.Fatalf("polledAt: got %v want %v", got.PolledAt, polledAt)
	}
}

func TestForcePollFailureBadGateway(t *testing.T) {
	api, _ := newTestAPI(t)
	api.SetPoller(&stubPoller{result: PollResult{
		PolledAt: time.Now().UTC(),
		Success:  false,
		Error:    "codexbar down",
	}})
	rec := doRequest(t, api, http.MethodPost, "/v1/poll", "secret-token", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
	var got pollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.OK || got.Success || got.Error != "codexbar down" {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestDemoAlertRequiresAuth(t *testing.T) {
	api, _ := newTestAPI(t)
	api.SetNotifier(&recordingNotifier{})
	rec := doRequest(t, api, http.MethodPost, "/v1/demo/alert", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestDemoAlertUnavailableWithoutNotifier(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := doRequest(t, api, http.MethodPost, "/v1/demo/alert", "secret-token", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDemoAlertNoDevices(t *testing.T) {
	api, _ := newTestAPI(t)
	api.SetNotifier(&recordingNotifier{})
	rec := doRequest(t, api, http.MethodPost, "/v1/demo/alert", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got demoAlertResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || got.DevicesAlerted != 0 || got.WidgetsRefreshed != 0 {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestBearerDemoAlertAllowlistsDemoDevices(t *testing.T) {
	api, store := newTestAPI(t)
	api.cfg.DemoDeviceIDs = []string{"dev-1"}
	recN := &recordingNotifier{}
	api.SetNotifier(recN)

	if err := store.UpsertDevice("dev-1", "apns-1", "widget-1"); err != nil {
		t.Fatalf("UpsertDevice: %v", err)
	}
	if err := store.UpsertDevice("real-device", "apns-real", "widget-real"); err != nil {
		t.Fatal(err)
	}

	rec := doRequest(t, api, http.MethodPost, "/v1/demo/alert", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got demoAlertResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || got.DevicesAlerted != 1 || got.WidgetsRefreshed != 1 {
		t.Fatalf("unexpected response: %+v", got)
	}
	if len(recN.alerts) != 1 || recN.alerts[0].Type != "test_alert" || !strings.HasPrefix(recN.alerts[0].Key, "demo.test_alert.") {
		t.Fatalf("expected one namespaced test alert, got %+v", recN.alerts)
	}
	if recN.widgets != 1 {
		t.Fatalf("expected one widget refresh, got %d", recN.widgets)
	}
	notified, err := store.EventNotified(recN.alerts[0].Key)
	if err != nil {
		t.Fatalf("EventNotified: %v", err)
	}
	if notified {
		t.Fatalf("demo alert must not record event keys for dedup")
	}
}
