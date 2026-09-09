package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCrossUsageClientFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"schema":"crossusage.limits.v1","providers":{},"errors":[]}`))
	}))
	defer srv.Close()

	c := NewCrossUsageHTTPClient(srv.URL)
	body, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != `{"schema":"crossusage.limits.v1","providers":{},"errors":[]}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestCrossUsageClientFetchNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewCrossUsageHTTPClient(srv.URL)
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatalf("expected error for non-200 status")
	}
}

func TestCrossUsageCommandClientFetch(t *testing.T) {
	c := NewCrossUsageCommandClient(`echo {"schema":"crossusage.limits.v1"}`)
	body, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "{\"schema\":\"crossusage.limits.v1\"}\n" && string(body) != `{"schema":"crossusage.limits.v1"}` {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestCrossUsageCommandClientFetchError(t *testing.T) {
	c := NewCrossUsageCommandClient("false")
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatalf("expected error for failing command")
	}
}

func TestCrossUsageBinaryClientSupportsPathsWithSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CrossUsage Test")
	script := `#!/bin/sh
if [ "$1" != "limits" ]; then exit 2; fi
printf '%s' '{"schema":"crossusage.limits.v1","providers":{"codex":{"displayName":"Codex","resources":{"session":{"unit":"percent","used":10,"limit":100,"utilization":0.1,"label":"Session"}}}},"errors":[]}'
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c := NewCrossUsageBinaryClient(path)
	body, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !looksLikeLimitsV1(body) {
		t.Fatalf("unexpected body: %q", body)
	}
}
