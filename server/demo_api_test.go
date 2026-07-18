package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestDemoMutationRequiresCSRF(t *testing.T) {
	api := NewDemoAPI(openTestStore(t), nil, Config{})
	rec := demoMutation(t, api, http.MethodPatch, "/v1/demo", "", "operator@example.test", "one", `{}`)
	if rec.Code != http.StatusForbidden {
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
	if rec.Code != http.StatusForbidden {
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
func TestDemoPollRevisionConflict(t *testing.T) {
	store := openTestStore(t)
	poller := &demoPollerStub{}
	api := NewDemoAPI(store, poller, Config{})
	token := demoCSRF(t, api)
	rec := demoMutation(t, api, http.MethodPost, "/v1/demo/poll", token, "operator", "poll", `{"expectedRevision":999}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), demoErrRevision) || poller.calls != 0 {
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
	api := NewDemoAPI(openTestStore(t), &demoPollerStub{err: context.DeadlineExceeded}, Config{})
	token := demoCSRF(t, api)
	rec := demoMutation(t, api, http.MethodPost, "/v1/demo/poll", token, "operator", "error", `{}`)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "http") || strings.Contains(rec.Body.String(), "/") || !strings.Contains(rec.Body.String(), demoErrPoll) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
