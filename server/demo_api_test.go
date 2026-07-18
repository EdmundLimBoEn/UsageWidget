package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type demoPollerStub struct {
	calls int
	err   error
}

func (p *demoPollerStub) PollDemoNow(_ context.Context, _ int64, id string, _ []Device) (DemoPipelineResult, error) {
	p.calls++
	return DemoPipelineResult{Success: true, DemoRunID: id}, p.err
}

func demoCSRF(t *testing.T, api *DemoAPI) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/demo", nil)
	api.Handler().ServeHTTP(rec, req)
	var response demoViewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.CSRFToken
}
func demoMutation(t *testing.T, api *DemoAPI, method, path, csrf, identity, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://demo.test"+path, strings.NewReader(body))
	req.Host = "demo.test"
	req.Header.Set("Origin", "https://demo.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Demo-CSRF", csrf)
	req.Header.Set("Cf-Access-Authenticated-User-Email", identity)
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDemoRouteAllowlist(t *testing.T) {
	store := openTestStore(t)
	api := NewDemoAPI(store, nil, Config{AccessIdentityHeader: "Cf-Access-Authenticated-User-Email"})
	for _, path := range []string{"/", "/styles.css", "/app.js", "/v1/demo", "/v1/demo/events"} {
		r := httptest.NewRecorder()
		api.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code == http.StatusNotFound {
			t.Fatalf("%s unexpectedly 404", path)
		}
	}
	r := httptest.NewRecorder()
	api.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	if r.Code != http.StatusNotFound {
		t.Fatalf("unexpected route status %d", r.Code)
	}
}

func TestDemoRouteMethodsAreAllowlisted(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	for _, tc := range []struct{ method, path string }{{http.MethodPost, "/"}, {http.MethodPatch, "/styles.css"}, {http.MethodDelete, "/v1/demo"}, {http.MethodGet, "/v1/demo/poll"}, {http.MethodPost, "/v1/demo/events"}, {http.MethodGet, "/v1/demo/alert"}} {
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s = %d, want 405", tc.method, tc.path, rec.Code)
		}
	}
	for _, path := range []string{"/v1/demo/unknown", "/not-embedded"} {
		rec := httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestDemoMutationRequiresCSRF(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", "", "operator@example.test", "one", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
func TestDemoMutationRejectsCrossOrigin(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	token := demoCSRF(t, api)
	req := httptest.NewRequest(http.MethodPatch, "http://demo.test/v1/demo", bytes.NewBufferString(`{}`))
	req.Host = "demo.test"
	req.Header.Set("Origin", "https://evil.test")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Demo-CSRF", token)
	req.Header.Set("Cf-Access-Authenticated-User-Email", "operator")
	req.Header.Set("Idempotency-Key", "one")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
func TestDemoMutationRejectsMissingOrBlankIdentity403(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	token := demoCSRF(t, api)
	for _, identity := range []string{"", "  "} {
		rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, identity, identity+"key", `{}`)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), demoErrIdentity) {
			t.Fatalf("identity %q: %d %s", identity, rec.Code, rec.Body.String())
		}
	}
}
func TestDemoMutationRequiresIdempotencyKey(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", demoCSRF(t, api), "operator", "", `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), demoErrInvalidRequest) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
func TestDemoMutationRateLimited(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	api.limiter = &rateLimiter{window: time.Minute, perID: 1, global: 100}
	token := demoCSRF(t, api)
	if rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", "one", `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", "two", `{}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), demoErrRate) {
		t.Fatalf("status=%d header=%q body=%s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}
func TestDemoIdempotentReplay(t *testing.T) {
	store := openTestStore(t)
	api := NewDemoAPI(store, nil, Config{})
	token := demoCSRF(t, api)
	first := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", "same", `{}`)
	second := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", "same", `{}`)
	if first.Code != http.StatusOK || second.Code != http.StatusOK || second.Header().Get("Idempotency-Replayed") != "true" || first.Body.String() != second.Body.String() {
		t.Fatalf("first=%d %q second=%d %q replay=%q", first.Code, first.Body, second.Code, second.Body, second.Header().Get("Idempotency-Replayed"))
	}
	state, err := store.LoadDemoState()
	if err != nil || state.Revision != 2 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestDemoIdempotentConcurrentDuplicate(t *testing.T) {
	store := openTestStore(t)
	api := NewDemoAPI(store, nil, Config{})
	token := demoCSRF(t, api)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", "same", `{}`)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var replay int
	for result := range results {
		if result.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
		}
		if result.Header().Get("Idempotency-Replayed") == "true" {
			replay++
		}
	}
	if replay != 1 {
		t.Fatalf("replays=%d, want one", replay)
	}
	state, err := store.LoadDemoState()
	if err != nil || state.Revision != 2 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestDemoConcurrentDistinctPatchesDoNotLoseUpdates(t *testing.T) {
	store := openTestStore(t)
	api := NewDemoAPI(store, nil, Config{})
	token := demoCSRF(t, api)
	start := make(chan struct{})
	var wg sync.WaitGroup
	requests := []struct{ key, body string }{{"a", `{"stale":true}`}, {"b", `{"providerError":true}`}}
	for _, request := range requests {
		request := request
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", request.key, request.body)
			if rec.Code != http.StatusOK {
				t.Errorf("%s: %d %s", request.key, rec.Code, rec.Body.String())
			}
		}()
	}
	close(start)
	wg.Wait()
	state, err := store.LoadDemoState()
	if err != nil || state.Revision != 3 || !state.Stale || !state.ProviderError {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestDemoMutationReturnsAndPersistsDemoRunID(t *testing.T) {
	store := openTestStore(t)
	api := NewDemoAPI(store, nil, Config{})
	rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", demoCSRF(t, api), "operator", "run-id", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		DemoRunID string `json:"demoRunID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.DemoRunID == "" {
		t.Fatalf("response=%s err=%v", rec.Body.String(), err)
	}
	state, err := store.LoadDemoState()
	if err != nil || state.LastDemoRunID != response.DemoRunID {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	audit, err := store.ListDemoAudit(1)
	if err != nil || len(audit) != 1 || audit[0].DemoRunID != response.DemoRunID {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
	run, ok, err := store.LatestDemoRun()
	if err != nil || !ok || run.DemoRunID != response.DemoRunID {
		t.Fatalf("run=%+v ok=%v err=%v", run, ok, err)
	}
	events, err := store.ListDemoEvents(1)
	if err != nil || len(events) != 1 || events[0].DemoRunID != response.DemoRunID {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestEmbeddedDemoAlertDeliversOnlyExplicitTargets(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("allowed", "alert-1", "widget-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertDevice("other", "alert-2", "widget-2"); err != nil {
		t.Fatal(err)
	}
	api := NewDemoAPI(store, nil, Config{DemoDeviceIDs: []string{"allowed"}})
	notifier := &recordingNotifier{}
	api.SetNotifier(notifier)
	rec := demoMutation(t, api, http.MethodPost, "/v1/demo/alert", demoCSRF(t, api), "operator", "alert", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Delivery DemoDeliveryResult `json:"delivery"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Delivery.Alerts.Succeeded != 1 || response.Delivery.WidgetRefresh.Succeeded != 1 || len(notifier.alerts) != 1 || notifier.widgets != 1 {
		t.Fatalf("delivery=%+v alerts=%d widgets=%d", response.Delivery, len(notifier.alerts), notifier.widgets)
	}
	api = NewDemoAPI(store, nil, Config{})
	api.SetNotifier(notifier)
	rec = demoMutation(t, api, http.MethodPost, "/v1/demo/alert", demoCSRF(t, api), "operator", "empty", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty status=%d", rec.Code)
	}
	if len(notifier.alerts) != 1 || notifier.widgets != 1 {
		t.Fatal("empty allowlist delivered")
	}
}
func TestDemoPollRevisionConflict(t *testing.T) {
	store := openTestStore(t)
	poller := &demoPollerStub{err: fmt.Errorf("%w: current 1", ErrDemoRevisionConflict)}
	api := NewDemoAPI(store, poller, Config{})
	token := demoCSRF(t, api)
	rec := demoMutation(t, api, http.MethodPost, "/v1/demo/poll", token, "operator", "poll", `{"expectedRevision":999}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), demoErrRevision) || poller.calls != 1 {
		t.Fatalf("status=%d body=%s calls=%d", rec.Code, rec.Body.String(), poller.calls)
	}
}
func TestDemoPatchRejectsOversizeBody(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	token := demoCSRF(t, api)
	rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", token, "operator", "large", `{"primary":{"usedPercent":1},"padding":"`+strings.Repeat("x", 17<<10)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
func TestDemoErrorsRedacted(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), &demoPollerStub{err: fmt.Errorf("poll https://secret.example/private: %w", fmt.Errorf("/absolute/private/path"))}, Config{})
	token := demoCSRF(t, api)
	rec := demoMutation(t, api, http.MethodPost, "/v1/demo/poll", token, "operator", "error", `{}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "https://secret.example") || strings.Contains(rec.Body.String(), "/absolute/private/path") || !strings.Contains(rec.Body.String(), demoErrPoll) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
