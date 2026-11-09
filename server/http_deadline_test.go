package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type delayedHTTPPoller struct {
	delay  time.Duration
	result PollResult
}

func (p delayedHTTPPoller) PollNow(ctx context.Context) PollResult {
	select {
	case <-time.After(p.delay):
		return p.result
	case <-ctx.Done():
		return PollResult{Error: ctx.Err().Error()}
	}
}

// A real HTTP server over net.Pipe exercises connection deadlines without
// requiring a listening TCP socket (which is unavailable in restricted runners).
type pollTestListener struct {
	net.Conn
	once     sync.Once
	done     chan struct{}
	accepted bool
}

func (l *pollTestListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.Conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *pollTestListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *pollTestListener) Addr() net.Addr { return l.LocalAddr() }

func TestManualPollResponseOutlivesServerWriteTimeout(t *testing.T) {
	api, _ := newTestAPI(t)
	polledAt := time.Now().UTC()
	api.SetPoller(delayedHTTPPoller{
		delay: 120 * time.Millisecond,
		result: PollResult{
			Success: true, PolledAt: polledAt, Events: 2, SnapshotChanged: true,
		},
	})
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	listener := &pollTestListener{Conn: serverConn, done: make(chan struct{})}
	srv := &http.Server{
		Handler: WithManualPollWriteDeadline(api.Handler()),
		// Scale down the daemon's deadline while keeping collection longer.
		WriteTimeout: 30 * time.Millisecond,
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	defer func() {
		srv.Close()
		if err := <-done; err != http.ErrServerClosed {
			t.Errorf("HTTP server: %v", err)
		}
	}()
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	req, err := http.NewRequest(http.MethodPost, "http://poll.test/v1/poll", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+api.cfg.Token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("manual poll response lost after successful collection: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("incomplete response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got pollResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !got.OK || !got.Success || !got.PolledAt.Equal(polledAt) || got.Events != 2 || !got.SnapshotChanged || got.Error != "" {
		t.Fatalf("unexpected poll response: %+v", got)
	}
}

type deadlineResponseWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestManualPollWriteDeadlineBudgetAndScope(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		poll   bool
	}{
		{http.MethodPost, "/v1/poll", true},
		{http.MethodPost, "/v1/poll?refresh=1", true},
		{http.MethodGet, "/v1/poll", false},
		{http.MethodGet, "/v1/health", false},
		{http.MethodPost, "/v1/devices", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := &deadlineResponseWriter{ResponseRecorder: httptest.NewRecorder()}
			called := false
			handler := WithManualPollWriteDeadline(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
				if tc.poll {
					// The entire shared poll must fit, with bounded response headroom.
					remaining := time.Until(w.deadline)
					if remaining < sharedPollTimeout+10*time.Second || remaining > sharedPollTimeout+30*time.Second {
						t.Errorf("write budget = %v, shared poll budget = %v", remaining, sharedPollTimeout)
					}
				} else if !w.deadline.IsZero() {
					t.Errorf("non-poll deadline overridden: %v", w.deadline)
				}
			}))
			handler.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if !called {
				t.Fatal("wrapped handler was not called")
			}
		})
	}
}
