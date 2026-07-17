package server

import (
	"context"
	"net/http"
	"net/http/httptest"
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
