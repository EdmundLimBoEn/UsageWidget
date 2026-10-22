package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type blockingUsageSource struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	err     error
}

func (s *blockingUsageSource) SourceName() string { return "test" }

func (s *blockingUsageSource) Fetch(context.Context) ([]byte, error) {
	if s.calls.Add(1) == 1 {
		close(s.started)
		<-s.release
	}
	return []byte(crossUsageBody), s.err
}

// Signals that the caller reached the select waiting for a shared result.
type observedWaitContext struct {
	context.Context
	waiting chan struct{}
}

func (c observedWaitContext) Done() <-chan struct{} {
	close(c.waiting)
	return c.Context.Done()
}

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for concurrent caller")
	}
}

func TestPollNowCoalescesConcurrentResults(t *testing.T) {
	for _, fetchErr := range []error{nil, errors.New("fetch failed")} {
		t.Run(fmt.Sprint(fetchErr), func(t *testing.T) {
			source := &blockingUsageSource{started: make(chan struct{}), release: make(chan struct{}), err: fetchErr}
			defer func() {
				select {
				case <-source.release:
				default:
					close(source.release)
				}
			}()
			poller := NewPoller(openTestStore(t), source, noopNotifier{}, nil)
			results := make(chan PollResult, 2)
			go func() { results <- poller.PollNow(context.Background()) }()
			awaitSignal(t, source.started)
			waiting := make(chan struct{})
			go func() { results <- poller.PollNow(observedWaitContext{context.Background(), waiting}) }()
			awaitSignal(t, waiting)
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if got := poller.PollNow(canceled); got.Error != context.Canceled.Error() {
				t.Fatalf("canceled waiter: %+v", got)
			}
			close(source.release)
			first, second := <-results, <-results
			if first != second || first.Success != (fetchErr == nil) || source.calls.Load() != 1 {
				t.Fatalf("results=%+v / %+v, fetches=%d", first, second, source.calls.Load())
			}
			poller.PollNow(context.Background())
			if source.calls.Load() != 2 {
				t.Fatal("later poll did not run")
			}
		})
	}
}

func TestManualPollCoalescesAndReusesRecentResult(t *testing.T) {
	for _, fetchErr := range []error{nil, errors.New("fetch failed")} {
		t.Run(fmt.Sprint(fetchErr), func(t *testing.T) {
			api, store := newTestAPI(t)
			source := &blockingUsageSource{started: make(chan struct{}), release: make(chan struct{}), err: fetchErr}
			defer func() {
				select {
				case <-source.release:
				default:
					close(source.release)
				}
			}()
			api.SetPoller(NewPoller(store, source, noopNotifier{}, api))
			responses := make(chan *httptest.ResponseRecorder, 2)
			request := func(ctx context.Context) {
				req := httptest.NewRequest(http.MethodPost, "/v1/poll", nil).WithContext(ctx)
				req.Header.Set("Authorization", "Bearer secret-token")
				rec := httptest.NewRecorder()
				api.Handler().ServeHTTP(rec, req)
				responses <- rec
			}
			go request(context.Background())
			awaitSignal(t, source.started)
			waiting := make(chan struct{})
			go request(observedWaitContext{context.Background(), waiting})
			awaitSignal(t, waiting)
			close(source.release)
			first, second := <-responses, <-responses
			want := http.StatusOK
			if fetchErr != nil {
				want = http.StatusBadGateway
			}
			if first.Code != want || second.Code != want || first.Body.String() != second.Body.String() || source.calls.Load() != 1 {
				t.Fatalf("responses=%d/%d, fetches=%d", first.Code, second.Code, source.calls.Load())
			}
			// Within the interval a repeat tap gets the same result without another upstream fetch.
			rec := doRequest(t, api, http.MethodPost, "/v1/poll", "secret-token", nil)
			if rec.Code != want || rec.Body.String() != first.Body.String() || source.calls.Load() != 1 {
				t.Fatalf("cooldown: status=%d fetches=%d", rec.Code, source.calls.Load())
			}
			api.mu.Lock()
			api.lastManualPollAt = time.Now().Add(-10 * time.Second)
			api.mu.Unlock()
			rec = doRequest(t, api, http.MethodPost, "/v1/poll", "secret-token", nil)
			if rec.Code != want || source.calls.Load() != 2 {
				t.Fatal("poll not allowed after cooldown")
			}
		})
	}
}

func TestReadinessLimiterRejectsUnknownAndEvictsExpired(t *testing.T) {
	api, store := newTestAPI(t)
	for i := 0; i < 10; i++ {
		rec := doRequest(t, api, http.MethodPost, fmt.Sprintf("/v1/readiness/missing-%d/test", i), "secret-token", nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unknown status=%d", rec.Code)
		}
	}
	if len(api.readinessTestAt) != 0 {
		t.Fatal("unknown devices allocated limiter entries")
	}
	if err := store.UpsertDevice("phone", "", ""); err != nil {
		t.Fatal(err)
	}
	api.readinessTestAt["expired"] = time.Now().Add(-time.Minute)
	api.readinessTestAt["phone"] = time.Now().Add(-time.Minute)
	api.readinessTestAt["recent"] = time.Now()
	rec := doRequest(t, api, http.MethodPost, "/v1/readiness/phone/test", "secret-token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expired limit status=%d", rec.Code)
	}
	if _, ok := api.readinessTestAt["expired"]; ok {
		t.Fatal("stale entry retained")
	}
	if _, ok := api.readinessTestAt["recent"]; !ok {
		t.Fatal("live entry evicted")
	}
	rec = doRequest(t, api, http.MethodPost, "/v1/readiness/phone/test", "secret-token", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("repeat status=%d", rec.Code)
	}
}

func TestDeviceRegistrationCap(t *testing.T) {
	for _, limit := range []int{DefaultMaxDevices, 2} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			var store *Store
			var err error
			path := filepath.Join(t.TempDir(), "devices.db")
			if limit == DefaultMaxDevices {
				store, err = OpenStore(path)
			} else {
				store, err = OpenStoreWithDeviceLimit(path, limit)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			for i := 0; i < limit; i++ {
				if err := store.UpsertDevice(fmt.Sprint(i), "", ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpsertDevice("extra", "", ""); !errors.Is(err, ErrDeviceLimit) {
				t.Fatalf("limit error=%v", err)
			}
			if err := store.UpsertDevice("0", "rotated", "widget"); err != nil {
				t.Fatal(err)
			}
			device, found, err := store.GetDevice("0")
			if err != nil || !found || device.APNsToken != "rotated" {
				t.Fatal("existing device update failed")
			}
			api := NewAPI(Config{Token: "secret-token"}, store, nil)
			rec := doRequest(t, api, http.MethodPost, "/v1/devices", "secret-token", []byte(`{"deviceID":"extra"}`))
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "registered device limit reached") {
				t.Fatalf("cap response=%d %s", rec.Code, rec.Body.String())
			}
			if err := store.DeleteDevice("0"); err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertDevice("extra", "", ""); err != nil {
				t.Fatalf("slot not freed: %v", err)
			}
		})
	}
}

func TestMaxDevicesConfiguration(t *testing.T) {
	t.Setenv("USAGEWIDGET_TOKEN", validTestToken)
	for _, value := range []string{"", "3", "0", "-1", "invalid"} {
		t.Setenv("MAX_DEVICES", value)
		cfg, err := LoadConfig()
		switch value {
		case "":
			if err != nil || cfg.MaxDevices != 50 {
				t.Fatalf("default config: %v", err)
			}
		case "3":
			if err != nil || cfg.MaxDevices != 3 {
				t.Fatalf("custom config: %v", err)
			}
		default:
			if err == nil {
				t.Fatalf("accepted invalid limit %q", value)
			}
		}
	}
}
