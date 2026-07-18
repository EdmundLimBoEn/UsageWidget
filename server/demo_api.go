package server

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed web/index.html
var demoIndex []byte

//go:embed web/styles.css
var demoStyles []byte

//go:embed web/app.js
var demoScript []byte

const (
	demoErrInvalidRequest = "invalid request"
	demoErrIdentity       = "access identity required"
	demoErrRevision       = "revision conflict"
	demoErrState          = "demo state unavailable"
	demoErrPoll           = "demo poll failed"
	demoErrNormalize      = "demo normalization failed"
	demoErrDelivery       = "demo delivery enqueue failed"
	demoErrRate           = "rate limited"
	demoErrInProgress     = "request in progress"
)

type DemoPoller interface {
	PollDemoNow(context.Context, int64, string, []Device) (DemoPipelineResult, error)
}

type DemoAPI struct {
	store          *Store
	poller         DemoPoller
	notifier       Notifier
	deviceIDs      map[string]bool
	identityHeader string
	csrfKey        []byte
	limiter        *rateLimiter
	idem           *idempotencyStore
}

func (d *DemoAPI) SetNotifier(notifier Notifier) { d.notifier = notifier }

// NewDemoAPI intentionally has no bearer middleware. Cloudflare Access is the
// listener's trust boundary and supplies the configured identity header.
func NewDemoAPI(store *Store, poller DemoPoller, cfg Config, notifiers ...Notifier) *DemoAPI {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	ids := make(map[string]bool, len(cfg.DemoDeviceIDs))
	for _, id := range cfg.DemoDeviceIDs {
		ids[id] = true
	}
	header := strings.TrimSpace(cfg.AccessIdentityHeader)
	if header == "" {
		header = "Cf-Access-Authenticated-User-Email"
	}
	// ponytail: this process-lifetime key intentionally invalidates outstanding
	// tokens on restart. Use an env-provided key if restart stability is needed.
	var notifier Notifier
	if len(notifiers) > 0 {
		notifier = notifiers[0]
	}
	return &DemoAPI{store: store, poller: poller, notifier: notifier, deviceIDs: ids, identityHeader: header, csrfKey: key, limiter: newRateLimiter(), idem: newIdempotencyStore()}
}

func (d *DemoAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", d.handleIndex)
	mux.HandleFunc("GET /styles.css", d.handleStyles)
	mux.HandleFunc("GET /app.js", d.handleScript)
	mux.HandleFunc("GET /v1/demo", d.handleGetDemo)
	mux.HandleFunc("PATCH /v1/demo", d.guardMutation("patch", d.handlePatchDemo))
	mux.HandleFunc("POST /v1/demo/poll", d.guardMutation("poll", d.handleDemoPoll))
	mux.HandleFunc("GET /v1/demo/events", d.handleDemoEvents)
	mux.HandleFunc("POST /v1/demo/alert", d.guardMutation("alert", d.handleDemoAlert))
	return d.withSecurityHeaders(mux)
}

func (d *DemoAPI) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture := &demoResponseWriter{header: make(http.Header), limit: 256 << 10}
		capture.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'")
		capture.Header().Set("X-Content-Type-Options", "nosniff")
		capture.Header().Set("Referrer-Policy", "no-referrer")
		capture.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(capture, r)
		if capture.overflow {
			capture = &demoResponseWriter{header: capture.header, limit: 256 << 10, status: http.StatusInternalServerError}
			capture.Header().Set("Content-Type", "application/json")
			_, _ = capture.Write([]byte(`{"error":"demo state unavailable"}` + "\n"))
		}
		for k, values := range capture.header {
			w.Header()[k] = append([]string(nil), values...)
		}
		w.WriteHeader(capture.statusCode())
		_, _ = w.Write(capture.body.Bytes())
	})
}

func (d *DemoAPI) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(demoIndex)
}
func (d *DemoAPI) handleStyles(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(demoStyles)
}
func (d *DemoAPI) handleScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write(demoScript)
}

type demoViewResponse struct {
	State          DemoState           `json:"state"`
	Snapshot       *DemoSnapshot       `json:"snapshot"`
	Pipeline       *DemoPipelineResult `json:"pipeline"`
	CSRFToken      string              `json:"csrfToken"`
	DeliveryHealth string              `json:"deliveryHealth"`
}
type DemoSnapshot struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Provider  Provider  `json:"provider"`
}

func (d *DemoAPI) handleGetDemo(w http.ResponseWriter, r *http.Request) {
	state, err := d.store.LoadDemoState()
	if err != nil {
		d.logError("load demo state", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrState)
		return
	}
	view := demoViewResponse{State: state, CSRFToken: issueCSRFToken(d.csrfKey, time.Now().UTC()), DeliveryHealth: "ok"}
	if run, ok, err := d.store.LatestDemoRun(); err != nil {
		d.logError("load latest demo run", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrState)
		return
	} else if ok {
		view.Pipeline = &run
		if !run.Success || run.Delivery.Alerts.Failed > 0 || run.Delivery.WidgetRefresh.Failed > 0 {
			view.DeliveryHealth = "degraded"
		}
	}
	if fetchedAt, payload, ok, err := d.store.LatestSnapshot(); err == nil && ok {
		var snapshot Snapshot
		if json.Unmarshal(payload, &snapshot) == nil {
			for _, p := range snapshot.Providers {
				if p.ID == "demo" {
					p.Raw = nil
					view.Snapshot = &DemoSnapshot{FetchedAt: fetchedAt, Provider: p}
					break
				}
			}
		}
	}
	d.writeJSON(w, http.StatusOK, view)
}

func (d *DemoAPI) handleDemoEvents(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
			return
		}
		limit = n
	}
	events, err := d.store.ListDemoEvents(limit)
	if err != nil {
		d.logError("list demo events", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrState)
		return
	}
	d.writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

type demoMutationHandler func(http.ResponseWriter, *http.Request, DemoAction)

func (d *DemoAPI) guardMutation(route string, next demoMutationHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOriginOK(r) {
			d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
			return
		}
		if err := verifyCSRFToken(d.csrfKey, r.Header.Get("X-Demo-CSRF"), time.Now().UTC()); err != nil {
			d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
			return
		}
		identity := strings.TrimSpace(r.Header.Get(d.identityHeader))
		if identity == "" {
			d.writeError(w, http.StatusForbidden, demoErrIdentity)
			return
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" || len(key) > 128 {
			d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
			return
		}
		if ok, retry := d.limiter.allow(identity, time.Now().UTC()); !ok {
			seconds := int(retry.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			d.writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": demoErrRate, "retryAfterSeconds": seconds})
			return
		}
		idemKey := idempotencyKey{Identity: identity, Route: route, Key: key}
		entry, owner, done := d.idem.reserve(idemKey, time.Now().UTC())
		if !owner {
			if entry.complete {
				d.replay(w, entry)
				return
			}
			select {
			case <-done:
				d.idem.mu.Lock()
				finished := d.idem.entries[idemKey]
				d.idem.mu.Unlock()
				if finished.complete {
					d.replay(w, finished)
					return
				}
			case <-time.After(5 * time.Second):
			}
			d.writeError(w, http.StatusConflict, demoErrInProgress)
			return
		}
		action := NewDemoAction(identity, route, time.Now().UTC())
		capture := &demoResponseWriter{header: make(http.Header), limit: 64 << 10}
		committed := &demoActionCommitted{}
		next(capture, r.WithContext(context.WithValue(r.Context(), demoActionCommittedKey{}, committed)), action)
		if capture.overflow {
			capture = &demoResponseWriter{header: make(http.Header), limit: 64 << 10, status: http.StatusInternalServerError}
			d.writeError(capture, http.StatusInternalServerError, demoErrState)
		}
		if !committed.value {
			result := "ok"
			if capture.statusCode() >= 400 {
				result = demoResult(capture.body.Bytes())
			}
			if _, err := d.store.CommitDemoAction(DemoActionCommit{Audit: DemoAuditEntry{DemoRunID: action.ID, Identity: identity, Route: route, Action: route, Result: result, Status: capture.statusCode(), CreatedAt: action.CreatedAt}}); err != nil {
				d.logError("save demo audit", err)
				capture = &demoResponseWriter{header: make(http.Header), limit: 64 << 10, status: http.StatusServiceUnavailable}
				d.writeError(capture, http.StatusServiceUnavailable, demoErrState)
			}
		}
		for k, values := range capture.header {
			w.Header()[k] = append([]string(nil), values...)
		}
		w.WriteHeader(capture.statusCode())
		_, _ = w.Write(capture.body.Bytes())
		d.idem.complete(idemKey, capture.statusCode(), capture.body.Bytes(), time.Now().UTC())
	}
}

type demoActionCommittedKey struct{}
type demoActionCommitted struct{ value bool }

func markDemoActionCommitted(r *http.Request) {
	if committed, _ := r.Context().Value(demoActionCommittedKey{}).(*demoActionCommitted); committed != nil {
		committed.value = true
	}
}

func (d *DemoAPI) replay(w http.ResponseWriter, entry idempotencyEntry) {
	w.Header().Set("Idempotency-Replayed", "true")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(entry.status)
	_, _ = w.Write(entry.body)
}
func demoResult(body []byte) string {
	var value struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &value) == nil && value.Error != "" {
		return value.Error
	}
	return demoErrInvalidRequest
}

func (d *DemoAPI) handlePatchDemo(w http.ResponseWriter, r *http.Request, action DemoAction) {
	var patch DemoStatePatch
	if !decodeDemoRequest(w, r, &patch) {
		d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
		return
	}
	next, err := d.store.CommitDemoPatch(action, patch, http.StatusOK, "ok")
	if err != nil {
		if errors.Is(err, errInvalidDemoPatch) {
			d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
			return
		}
		d.logError("commit demo patch", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrState)
		return
	}
	markDemoActionCommitted(r)
	d.writeJSON(w, http.StatusOK, map[string]any{"state": next, "demoRunID": action.ID, "deliveryHealth": "ok"})
}

func (d *DemoAPI) handleDemoPoll(w http.ResponseWriter, r *http.Request, action DemoAction) {
	var request struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}
	if !decodeDemoRequest(w, r, &request) {
		d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
		return
	}
	targets, err := d.store.DemoTargets(d.allowlist())
	if err != nil {
		d.logError("select demo targets", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrDelivery)
		return
	}
	if d.poller == nil {
		d.writeError(w, http.StatusServiceUnavailable, demoErrPoll)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var pipeline DemoPipelineResult
	if actionPoller, ok := d.poller.(interface {
		PollDemoAction(context.Context, int64, DemoAction, []Device) (DemoPipelineResult, error)
	}); ok {
		pipeline, err = actionPoller.PollDemoAction(ctx, request.ExpectedRevision, action, targets)
		if err == nil {
			markDemoActionCommitted(r)
		}
	} else {
		pipeline, err = d.poller.PollDemoNow(ctx, request.ExpectedRevision, action.ID, targets)
	}
	if errors.Is(err, ErrDemoRevisionConflict) {
		state, loadErr := d.store.LoadDemoState()
		if loadErr != nil {
			d.writeError(w, http.StatusServiceUnavailable, demoErrState)
			return
		}
		d.writeJSON(w, http.StatusConflict, map[string]any{"error": demoErrRevision, "currentRevision": state.Revision})
		return
	}
	if err != nil {
		d.logError("demo poll", err)
		d.writeError(w, http.StatusBadGateway, demoErrPoll)
		return
	}
	events, err := d.store.ListDemoEvents(100)
	if err != nil {
		d.logError("list demo poll events", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrState)
		return
	}
	d.writeJSON(w, http.StatusOK, map[string]any{"pipeline": pipeline, "events": events, "demoRunID": action.ID, "deliveryHealth": deliveryHealth(pipeline)})
}

func (d *DemoAPI) handleDemoAlert(w http.ResponseWriter, r *http.Request, action DemoAction) {
	var request struct{}
	if !decodeDemoRequest(w, r, &request) {
		d.writeError(w, http.StatusBadRequest, demoErrInvalidRequest)
		return
	}
	targets, err := d.store.DemoTargets(d.allowlist())
	if err != nil {
		d.logError("select demo alert targets", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrDelivery)
		return
	}
	if len(targets) > 0 && d.notifier == nil {
		d.writeError(w, http.StatusBadGateway, demoErrDelivery)
		return
	}
	delivery := DemoDeliveryResult{}
	if d.notifier != nil {
		ev := demoEvent()
		for _, target := range targets {
			if target.APNsToken != "" {
				delivery.Alerts.Attempted++
				if err := d.notifier.SendAlert(r.Context(), target.APNsToken, ev); err != nil {
					delivery.Alerts.Failed++
					d.logError("send demo alert", err)
				} else {
					delivery.Alerts.Succeeded++
				}
			}
			if target.WidgetToken != "" {
				delivery.WidgetRefresh.Attempted++
				if err := d.notifier.SendWidgetRefresh(r.Context(), target.WidgetToken); err != nil {
					delivery.WidgetRefresh.Failed++
					d.logError("refresh demo widget", err)
				} else {
					delivery.WidgetRefresh.Succeeded++
				}
			}
		}
	}
	now := time.Now().UTC()
	run := DemoRun{StartedAt: action.CreatedAt, CompletedAt: now, Success: true, Delivery: delivery, DemoRunID: action.ID}
	event := DemoEvent{Key: "demo.test_alert:" + action.ID, Type: "test_alert", CreatedAt: now, Delivery: delivery, DemoRunID: action.ID}
	if _, err := d.store.CommitDemoActionWithCurrentState(DemoActionCommit{Run: &run, Events: []DemoEvent{event}, Audit: DemoAuditEntry{DemoRunID: action.ID, Identity: action.Identity, Route: action.Route, Action: action.Route, Result: "ok", Status: http.StatusOK, CreatedAt: action.CreatedAt}}); err != nil {
		d.logError("commit demo alert", err)
		d.writeError(w, http.StatusServiceUnavailable, demoErrState)
		return
	}
	markDemoActionCommitted(r)
	d.writeJSON(w, http.StatusOK, map[string]any{"delivery": delivery, "demoRunID": action.ID, "deliveryHealth": deliveryHealth(run)})
}

func (d *DemoAPI) allowlist() []string {
	ids := make([]string, 0, len(d.deviceIDs))
	for id := range d.deviceIDs {
		ids = append(ids, id)
	}
	return ids
}
func deliveryHealth(run DemoPipelineResult) string {
	if !run.Success || run.Delivery.Alerts.Failed > 0 || run.Delivery.WidgetRefresh.Failed > 0 {
		return "degraded"
	}
	return "ok"
}
func (d *DemoAPI) writeError(w http.ResponseWriter, status int, message string) {
	d.writeJSON(w, status, map[string]any{"error": message})
}
func (d *DemoAPI) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (d *DemoAPI) logError(stage string, err error) { log.Printf("demo api %s: %v", stage, err) }

func decodeDemoRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

type demoResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	limit    int
	overflow bool
}

func (w *demoResponseWriter) Header() http.Header { return w.header }
func (w *demoResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *demoResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(p) > w.limit {
		w.overflow = true
		return 0, io.ErrShortWrite
	}
	return w.body.Write(p)
}
func (w *demoResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
