package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexBarClientFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"providers":[]}`))
	}))
	defer srv.Close()

	c := NewCodexBarClient(srv.URL)
	body, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != `{"providers":[]}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestCodexBarClientFetchNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewCodexBarClient(srv.URL)
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatalf("expected error for non-200 status")
	}
}

func TestCodexBarCommandClientFetch(t *testing.T) {
	c := NewCodexBarCommandClient(`echo [{"provider":"codex"}]`)
	body, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "[{\"provider\":\"codex\"}]\n" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestCodexBarCommandClientFetchError(t *testing.T) {
	c := NewCodexBarCommandClient("false")
	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatalf("expected error for failing command")
	}
}

func TestCodexBarBinaryClientSupportsPathsWithSpaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CodexBar Test")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '[{\"provider\":\"codex\"}]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := NewCodexBarBinaryClient(path)
	body, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != `[{"provider":"codex"}]` {
		t.Fatalf("unexpected body: %q", body)
	}
}
