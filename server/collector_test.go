package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func collectorScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codexbar-test")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectorReturnsValidCLIJSON(t *testing.T) {
	binary := collectorScript(t, `printf '[{"provider":"claude"}]'`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/usage", nil)
	NewCollector(binary).Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"claude"`) {
		t.Fatalf("unexpected response %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-UsageWidget-Collected-At") == "" {
		t.Fatal("missing collection timestamp")
	}
}

func TestCollectorReturnsPartialJSONWhenCLIExitsNonZero(t *testing.T) {
	binary := collectorScript(t, `printf '[{"provider":"codex","usage":{}},{"provider":"claude","error":{"message":"No available fetch strategy for claude."}}]'; exit 1`)
	rec := httptest.NewRecorder()
	NewCollector(binary).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"No available fetch strategy for claude."`) {
		t.Fatalf("unexpected partial response %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCollectorClassifiesRateLimitAndRejectsOtherRoutes(t *testing.T) {
	binary := collectorScript(t, `echo 'usage CLI overloaded / rate limited' >&2; exit 1`)
	handler := NewCollector(binary).Handler()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "rate limited") {
		t.Fatalf("unexpected failure %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/usage", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestCollectorTimesOutAndRejectsInvalidJSON(t *testing.T) {
	timeoutBinary := collectorScript(t, `sleep 2 & wait`)
	collector := NewCollector(timeoutBinary)
	collector.Timeout = 10 * time.Millisecond
	started := time.Now()
	rec := httptest.NewRecorder()
	collector.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected timeout, got %d: %s", rec.Code, rec.Body.String())
	}
	if time.Since(started) > time.Second {
		t.Fatalf("collector did not terminate the command process group promptly")
	}

	invalidBinary := collectorScript(t, `printf 'not-json'`)
	rec = httptest.NewRecorder()
	NewCollector(invalidBinary).Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/usage", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected invalid JSON failure, got %d", rec.Code)
	}
}

func TestRetryablePollErrorSkipsRateLimits(t *testing.T) {
	if retryablePollError("collector rate limited") {
		t.Fatal("rate limits must wait for the normal cadence")
	}
	if !retryablePollError("dial unix: connect: no such file") {
		t.Fatal("transport failures should receive one retry")
	}
}
