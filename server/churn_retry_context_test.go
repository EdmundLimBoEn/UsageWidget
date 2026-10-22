package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

func TestDeletedDeviceDoesNotRetainReadinessResults(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "existing result"
		if late {
			name = "in-flight result"
		}
		t.Run(name, func(t *testing.T) {
			api, store := newTestAPI(t)
			if err := store.UpsertDevice("phone", "alert", "widget"); err != nil {
				t.Fatal(err)
			}
			result := DeviceTestResult{DeviceID: "phone", AttemptedAt: time.Now(), AlertAttempted: true}
			if !late {
				if err := store.SaveDeviceTestResult(result); err != nil {
					t.Fatal(err)
				}
			}
			rec := doRequest(t, api, http.MethodDelete, "/v1/devices/phone", "secret-token", nil)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("delete status=%d", rec.Code)
			}
			if late {
				// A readiness request may finish its APNs call after deletion.
				if err := store.SaveDeviceTestResult(result); err != nil {
					t.Fatal(err)
				}
			}
			if _, found, err := store.LatestDeviceTestResult("phone"); err != nil || found {
				t.Fatalf("deleted device retained readiness result: found=%v err=%v", found, err)
			}
		})
	}
}

func TestDeviceDeletionRollsBackWithReadinessFailure(t *testing.T) {
	store := openTestStore(t)
	if err := store.UpsertDevice("phone", "alert", "widget"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDeviceTestResult(DeviceTestResult{DeviceID: "phone", AttemptedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_result_delete BEFORE DELETE ON device_test_results BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDevice("phone"); err == nil {
		t.Fatal("delete succeeded despite readiness cleanup failure")
	}
	if _, found, err := store.GetDevice("phone"); err != nil || !found {
		t.Fatalf("device not rolled back: %v", err)
	}
}

func TestRegistrationPreservesTokenRotatedBeforeUpsert(t *testing.T) {
	for _, field := range []string{"apns_token", "widget_token"} {
		t.Run(field, func(t *testing.T) {
			api, store := newTestAPI(t)
			if err := store.UpsertDevice("phone", "old-alert", "old-widget"); err != nil {
				t.Fatal(err)
			}
			// Deterministically rotate the omitted token between any API read and the upsert.
			if _, err := store.db.Exec(`CREATE TRIGGER rotate_token BEFORE INSERT ON devices BEGIN UPDATE devices SET ` + field + ` = 'rotated' WHERE device_id = NEW.device_id; END`); err != nil {
				t.Fatal(err)
			}
			body := `{"deviceID":"phone","apnsToken":"new-alert"}`
			wantAlert, wantWidget := "new-alert", "rotated"
			if field == "apns_token" {
				body = `{"deviceID":"phone","widgetToken":"new-widget"}`
				wantAlert, wantWidget = "rotated", "new-widget"
			}
			rec := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token", []byte(body))
			if rec.Code != http.StatusOK {
				t.Fatalf("registration status=%d", rec.Code)
			}
			got, found, err := store.GetDevice("phone")
			if err != nil || !found || got.APNsToken != wantAlert || got.WidgetToken != wantWidget {
				t.Fatalf("lost token rotation: %+v, %v", got, err)
			}
			var response deviceResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.APNsToken != wantAlert || response.WidgetToken != wantWidget {
				t.Fatalf("stale registration response: %+v", response)
			}
		})
	}
}

type failingReadBody struct{}

func (failingReadBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingReadBody) Close() error             { return nil }

func TestRedactedFetchFailuresKeepRetrySchedule(t *testing.T) {
	for _, tc := range []struct {
		name         string
		transportErr error
		status       int
		readFailure  bool
		retry        bool
	}{
		{name: "timeout", transportErr: context.DeadlineExceeded, retry: true},
		{name: "EOF", transportErr: io.EOF, retry: true},
		{name: "connection", transportErr: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, retry: true},
		{name: "body EOF", readFailure: true, retry: true},
		{name: "request timeout", status: http.StatusRequestTimeout, retry: true},
		{name: "gateway timeout", status: http.StatusGatewayTimeout, retry: true},
		{name: "authentication", status: http.StatusUnauthorized},
		{name: "rate limit", status: http.StatusTooManyRequests},
		{name: "overload", status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := openTestStore(t)
				var calls atomic.Int32
				client := NewCrossUsageHTTPClient("http://example.invalid/private-marker")
				client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
					if calls.Add(1) > 1 {
						return testHTTPResponse(http.StatusOK, crossUsageBody), nil
					}
					if tc.transportErr != nil {
						return nil, tc.transportErr
					}
					if tc.readFailure {
						return &http.Response{StatusCode: http.StatusOK, Body: failingReadBody{}}, nil
					}
					return testHTTPResponse(tc.status, "private-marker"), nil
				})
				poller := NewPoller(store, client, noopNotifier{}, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan struct{})
				go func() { defer close(done); poller.Run(ctx) }()
				time.Sleep(31 * time.Second)
				cancel()
				<-done
				want := int32(1)
				if tc.retry {
					want = 2
				}
				if calls.Load() != want {
					t.Fatalf("fetch count=%d want=%d after retry window", calls.Load(), want)
				}
			})
		})
	}
}

type contextBlockingSource struct {
	started chan context.Context
	release chan struct{}
	calls   atomic.Int32
}

func (s *contextBlockingSource) SourceName() string { return "test" }
func (s *contextBlockingSource) Fetch(ctx context.Context) ([]byte, error) {
	s.calls.Add(1)
	s.started <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return []byte(crossUsageBody), nil
	}
}

func TestSharedPollSurvivesInitiatorCancellation(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "poller"
		if manual {
			name = "manual API"
		}
		t.Run(name, func(t *testing.T) {
			api, store := newTestAPI(t)
			source := &contextBlockingSource{started: make(chan context.Context, 2), release: make(chan struct{}, 1)}
			defer close(source.release)
			poller := NewPoller(store, source, noopNotifier{}, api)
			api.SetPoller(poller)
			run := func(ctx context.Context) PollResult {
				if !manual {
					return poller.PollNow(ctx)
				}
				req := httptest.NewRequest(http.MethodPost, "/v1/poll", nil).WithContext(ctx)
				rec := httptest.NewRecorder()
				api.handleForcePoll(rec, req)
				var response pollResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					return PollResult{Error: err.Error()}
				}
				return PollResult{Success: response.Success, Error: response.Error, PolledAt: response.PolledAt}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := make(chan PollResult, 1)
			go func() { first <- run(ctx) }()
			var fetchCtx context.Context
			select {
			case fetchCtx = <-source.started:
			case <-time.After(3 * time.Second):
				t.Fatal("fetch did not start")
			}
			waiting := make(chan struct{})
			second := make(chan PollResult, 1)
			go func() { second <- run(observedWaitContext{context.Background(), waiting}) }()
			awaitSignal(t, waiting)
			cancel()
			select {
			case got := <-first:
				if got.Error != context.Canceled.Error() {
					t.Errorf("initiator result=%+v", got)
				}
			case <-time.After(3 * time.Second):
				t.Error("canceled initiator kept waiting")
			}
			if fetchCtx.Err() != nil {
				t.Error("initiator canceled shared fetch")
			}
			if deadline, ok := fetchCtx.Deadline(); !ok || time.Until(deadline) > 2*time.Minute {
				t.Error("shared fetch lacks bounded deadline")
			}
			// Release without closing, so deferred cleanup also releases on test failure.
			source.release <- struct{}{}
			select {
			case got := <-second:
				if !got.Success {
					t.Errorf("remaining caller failed: %+v", got)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("remaining caller did not finish")
			}
			if manual {
				if got := run(context.Background()); !got.Success {
					t.Errorf("cached result poisoned by cancellation: %+v", got)
				}
			}
			if source.calls.Load() != 1 {
				t.Errorf("fetches=%d want=1", source.calls.Load())
			}
		})
	}
}
