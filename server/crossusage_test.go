package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestCrossUsageErrorsRedactedInPollAPI(t *testing.T) {
	diagnostic := "diagnostic-marker " + strings.Repeat("x", 500) + " tail-marker"
	httpClient := &CrossUsageClient{URL: "http://collector/usage", httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusBadGateway, diagnostic), nil
	})}}
	binary := collectorScript(t, "printf '%s ' \"$1\" >&2; printf '%s' '"+diagnostic+"' >&2; exit 1")
	for _, tc := range []struct {
		name     string
		client   *CrossUsageClient
		category string
	}{
		{"command", &CrossUsageClient{Cmd: []string{binary, "argument-marker"}}, "command failed"},
		{"binary", NewCrossUsageBinaryClient(binary), "command failed"},
		{"http status", httpClient, "unexpected HTTP status"},
		{"invalid URL", NewCrossUsageHTTPClient("http://invalid%url/credential-marker"), "build request failed"},
		{"transport", &CrossUsageClient{URL: "http://example.invalid/credential-marker", httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("transport credential-marker")
		})}}, "fetch failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			api, store := newTestAPI(t)
			poller := NewPoller(store, tc.client, noopNotifier{}, api)
			api.SetPoller(poller)
			rec := doRequest(t, api, http.MethodPost, "/v1/poll", "secret-token", nil)
			var got pollResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if rec.Code != http.StatusBadGateway || got.Error != "crossusage: "+tc.category {
				t.Fatalf("expected fixed error category %q", tc.category)
			}
			for _, forbidden := range []string{"argument-marker", "credential-marker", "tail-marker", binary} {
				if strings.Contains(logs.String(), forbidden) {
					t.Fatal("sensitive or unbounded diagnostic in log")
				}
			}
			if tc.category == "command failed" || tc.category == "unexpected HTTP status" {
				if !strings.Contains(logs.String(), "diagnostic-marker") || !strings.Contains(logs.String(), "…") {
					t.Fatal("missing truncated diagnostic")
				}
			}
		})
	}
}
