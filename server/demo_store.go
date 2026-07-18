package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var errInvalidDemoPatch = errors.New("invalid demo patch")

// demoActionMu serializes the state read/commit portion of an admitted demo
// action. SQLite transactions make writes atomic; this lock also prevents a
// slow poll from committing an earlier state snapshot over a concurrent PATCH.
// Stores are intentionally single-process for this demo surface.
var demoActionMu sync.Mutex

const demoSchema = `
CREATE TABLE IF NOT EXISTS demo_state (
    key TEXT PRIMARY KEY CHECK (key = 'demo.state'),
    payload TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_demo_run_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS demo_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    success INTEGER NOT NULL,
    payload TEXT NOT NULL,
    demo_run_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS demo_event_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER,
    event_key TEXT NOT NULL CHECK (event_key LIKE 'demo.%'),
    event_type TEXT NOT NULL,
    created_at TEXT NOT NULL,
    payload TEXT NOT NULL,
    demo_run_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_demo_event_log_recent ON demo_event_log(id DESC);
CREATE INDEX IF NOT EXISTS idx_demo_event_log_type_recent ON demo_event_log(event_type, id DESC);
CREATE TABLE IF NOT EXISTS demo_audit (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    demo_run_id TEXT NOT NULL,
    identity TEXT NOT NULL,
    route TEXT NOT NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    status INTEGER NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_demo_audit_recent ON demo_audit(id DESC);
`

type DemoRun = DemoPipelineResult

type DemoEventRecord struct {
	ID           int64              `json:"id"`
	RunID        *int64             `json:"runID,omitempty"`
	Key          string             `json:"key"`
	Type         string             `json:"type"`
	CreatedAt    time.Time          `json:"createdAt"`
	WindowID     string             `json:"windowID,omitempty"`
	Before       *EventValue        `json:"before,omitempty"`
	After        *EventValue        `json:"after,omitempty"`
	Deduplicated bool               `json:"deduplicated"`
	Delivery     DemoDeliveryResult `json:"delivery"`
	DemoRunID    string             `json:"-"`

	// Compatibility fields for the preliminary Task 1 store contract. New code
	// uses Key and Type; these fields are never included in persisted JSON.
	EventKey  string          `json:"-"`
	EventType string          `json:"-"`
	Payload   json.RawMessage `json:"-"`
}

type DemoAction struct {
	ID, Identity, Route string
	CreatedAt           time.Time
}
type DemoAuditEntry struct {
	DemoRunID, Identity, Route, Action, Result string
	Status                                     int
	CreatedAt                                  time.Time
}

func NewDemoAction(identity, route string, now time.Time) DemoAction {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return DemoAction{ID: fmt.Sprintf("%x", b), Identity: identity, Route: route, CreatedAt: now.UTC()}
}

type DemoEvent = DemoEventRecord

var allowedDemoEventTypes = map[string]bool{
	"early_threshold":  true,
	"danger_threshold": true,
	"reset":            true,
	"tibo_reset":       true,
	"credits_increase": true,
	"manual_poll":      true,
	"test_alert":       true,
	"pipeline_error":   true,
}

func (s *Store) seedDefaultDemoState(now time.Time) error {
	if err := s.ensureDemoMigrations(); err != nil {
		return err
	}
	state := DefaultDemoState(now)
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("store: encode default demo state: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO demo_state (key, payload, updated_at, last_demo_run_id) VALUES ('demo.state', ?, ?, '') ON CONFLICT(key) DO NOTHING`,
		string(payload), state.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store: seed demo state: %w", err)
	}
	// Task 1--4 payloads predate revisions. Upgrade only the zero-value
	// historical payload, preserving every other field and its timestamp.
	var existing string
	if err := s.db.QueryRow(`SELECT payload FROM demo_state WHERE key = 'demo.state'`).Scan(&existing); err != nil {
		return fmt.Errorf("store: load seeded demo state: %w", err)
	}
	var persisted DemoState
	if err := json.Unmarshal([]byte(existing), &persisted); err != nil {
		return fmt.Errorf("store: decode seeded demo state: %w", err)
	}
	if persisted.Revision == 0 {
		persisted.Revision = 1
		upgraded, err := json.Marshal(persisted)
		if err != nil {
			return fmt.Errorf("store: encode migrated demo state: %w", err)
		}
		if _, err := s.db.Exec(`UPDATE demo_state SET payload = ? WHERE key = 'demo.state'`, string(upgraded)); err != nil {
			return fmt.Errorf("store: migrate demo state revision: %w", err)
		}
	}
	return nil
}

func (s *Store) LoadDemoState() (DemoState, error) {
	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM demo_state WHERE key = 'demo.state'`).Scan(&payload); err != nil {
		return DemoState{}, fmt.Errorf("store: load demo state: %w", err)
	}
	var state DemoState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return DemoState{}, fmt.Errorf("store: decode demo state: %w", err)
	}
	return state, nil
}

func (s *Store) SaveDemoState(state DemoState, actionIDs ...string) error {
	demoRunID := ""
	if len(actionIDs) > 0 {
		demoRunID = actionIDs[0]
	}
	if err := validateDemoState(state, state.UpdatedAt); err != nil {
		return fmt.Errorf("store: validate demo state: %w", err)
	}
	state.LastDemoRunID = demoRunID
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("store: encode demo state: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO demo_state (key, payload, updated_at, last_demo_run_id) VALUES ('demo.state', ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at, last_demo_run_id = excluded.last_demo_run_id`,
		string(payload), state.UpdatedAt.Format(time.RFC3339Nano), demoRunID,
	)
	if err != nil {
		return fmt.Errorf("store: save demo state: %w", err)
	}
	return nil
}

// DemoActionCommit is the one durable boundary for an admitted demo action.
// State, the optional run and feed records, and the required audit record are
// committed together or not at all.
type DemoActionCommit struct {
	State  *DemoState
	Run    *DemoRun
	Events []DemoEvent
	Audit  DemoAuditEntry
}

func (s *Store) CommitDemoAction(commit DemoActionCommit) (int64, error) {
	if commit.Audit.DemoRunID == "" || commit.Audit.Identity == "" || commit.Audit.Route == "" || commit.Audit.Action == "" || commit.Audit.Result == "" || commit.Audit.CreatedAt.IsZero() {
		return 0, fmt.Errorf("store: incomplete demo action audit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin demo action: %w", err)
	}
	defer tx.Rollback()
	if commit.State != nil {
		if err := saveDemoStateTx(tx, *commit.State, commit.Audit.DemoRunID); err != nil {
			return 0, err
		}
	}
	var runID int64
	if commit.Run != nil {
		var err error
		runID, err = saveDemoExecutionTx(tx, *commit.Run, commit.Events)
		if err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO demo_audit (demo_run_id, identity, route, action, result, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, commit.Audit.DemoRunID, commit.Audit.Identity, commit.Audit.Route, commit.Audit.Action, commit.Audit.Result, commit.Audit.Status, commit.Audit.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("store: save demo action audit: %w", err)
	}
	if err := pruneDemoRetention(tx, "demo_audit", time.Now().UTC()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit demo action: %w", err)
	}
	return runID, nil
}

// CommitDemoPatch performs the entire PATCH read/apply/revision/action write
// while holding the demo action lock. It prevents lost updates between distinct
// idempotency keys without broadening the normal Store API.
func (s *Store) CommitDemoPatch(action DemoAction, patch DemoStatePatch, status int, result string) (DemoState, error) {
	demoActionMu.Lock()
	defer demoActionMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return DemoState{}, fmt.Errorf("store: begin demo patch: %w", err)
	}
	defer tx.Rollback()
	state, err := loadDemoStateTx(tx)
	if err != nil {
		return DemoState{}, err
	}
	next, err := ApplyDemoPatch(state, patch, time.Now().UTC())
	if err != nil {
		return DemoState{}, fmt.Errorf("%w: %v", errInvalidDemoPatch, err)
	}
	next.LastDemoRunID = action.ID
	if err := saveDemoStateTx(tx, next, action.ID); err != nil {
		return DemoState{}, err
	}
	run := DemoRun{StartedAt: action.CreatedAt, CompletedAt: time.Now().UTC(), Success: status < 400, DemoRunID: action.ID}
	event := DemoEvent{Key: "demo.manual_poll:" + action.ID, Type: "manual_poll", CreatedAt: run.CompletedAt, DemoRunID: action.ID}
	if _, err := saveDemoExecutionTx(tx, run, []DemoEvent{event}); err != nil {
		return DemoState{}, err
	}
	audit := DemoAuditEntry{DemoRunID: action.ID, Identity: action.Identity, Route: action.Route, Action: action.Route, Result: result, Status: status, CreatedAt: action.CreatedAt}
	if _, err := tx.Exec(`INSERT INTO demo_audit (demo_run_id, identity, route, action, result, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, audit.DemoRunID, audit.Identity, audit.Route, audit.Action, audit.Result, audit.Status, audit.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return DemoState{}, fmt.Errorf("store: save demo patch audit: %w", err)
	}
	if err := pruneDemoRetention(tx, "demo_audit", time.Now().UTC()); err != nil {
		return DemoState{}, err
	}
	if err := tx.Commit(); err != nil {
		return DemoState{}, fmt.Errorf("store: commit demo patch: %w", err)
	}
	return next, nil
}

// CommitDemoActionWithCurrentState records a non-patch action against the
// state current at commit time. It is used after synchronous alert delivery so
// an earlier state read cannot overwrite a concurrent PATCH.
func (s *Store) CommitDemoActionWithCurrentState(commit DemoActionCommit) (int64, error) {
	demoActionMu.Lock()
	defer demoActionMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin demo current-state action: %w", err)
	}
	defer tx.Rollback()
	state, err := loadDemoStateTx(tx)
	if err != nil {
		return 0, err
	}
	state.LastDemoRunID = commit.Audit.DemoRunID
	commit.State = &state
	if commit.Audit.DemoRunID == "" || commit.Audit.Identity == "" || commit.Audit.Route == "" || commit.Audit.Action == "" || commit.Audit.Result == "" || commit.Audit.CreatedAt.IsZero() {
		return 0, fmt.Errorf("store: incomplete demo action audit")
	}
	if err := saveDemoStateTx(tx, state, commit.Audit.DemoRunID); err != nil {
		return 0, err
	}
	var runID int64
	if commit.Run != nil {
		runID, err = saveDemoExecutionTx(tx, *commit.Run, commit.Events)
		if err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO demo_audit (demo_run_id, identity, route, action, result, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, commit.Audit.DemoRunID, commit.Audit.Identity, commit.Audit.Route, commit.Audit.Action, commit.Audit.Result, commit.Audit.Status, commit.Audit.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("store: save demo action audit: %w", err)
	}
	if err := pruneDemoRetention(tx, "demo_audit", time.Now().UTC()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit demo current-state action: %w", err)
	}
	return runID, nil
}

func loadDemoStateTx(tx *sql.Tx) (DemoState, error) {
	var payload string
	if err := tx.QueryRow(`SELECT payload FROM demo_state WHERE key = 'demo.state'`).Scan(&payload); err != nil {
		return DemoState{}, fmt.Errorf("store: load demo state: %w", err)
	}
	var state DemoState
	if err := json.Unmarshal([]byte(payload), &state); err != nil {
		return DemoState{}, fmt.Errorf("store: decode demo state: %w", err)
	}
	return state, nil
}

func saveDemoStateTx(tx *sql.Tx, state DemoState, demoRunID string) error {
	if err := validateDemoState(state, state.UpdatedAt); err != nil {
		return fmt.Errorf("store: validate demo state: %w", err)
	}
	state.LastDemoRunID = demoRunID
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("store: encode demo state: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO demo_state (key, payload, updated_at, last_demo_run_id) VALUES ('demo.state', ?, ?, ?) ON CONFLICT(key) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at, last_demo_run_id = excluded.last_demo_run_id`, string(payload), state.UpdatedAt.Format(time.RFC3339Nano), demoRunID); err != nil {
		return fmt.Errorf("store: save demo state: %w", err)
	}
	return nil
}

func saveDemoExecutionTx(tx *sql.Tx, run DemoRun, events []DemoEvent) (int64, error) {
	if run.StartedAt.IsZero() || run.CompletedAt.IsZero() || run.CompletedAt.Before(run.StartedAt) {
		return 0, fmt.Errorf("store: invalid demo execution timestamps")
	}
	canonical := make([]DemoEventRecord, len(events))
	for i, event := range events {
		normalized, err := normalizeDemoEventRecord(event)
		if err != nil {
			return 0, fmt.Errorf("store: demo event %d: %w", i, err)
		}
		canonical[i] = normalized
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return 0, fmt.Errorf("store: encode demo run: %w", err)
	}
	res, err := tx.Exec(`INSERT INTO demo_runs (started_at, completed_at, success, payload, demo_run_id) VALUES (?, ?, ?, ?, ?)`, run.StartedAt.Format(time.RFC3339Nano), run.CompletedAt.Format(time.RFC3339Nano), run.Success, string(payload), run.DemoRunID)
	if err != nil {
		return 0, fmt.Errorf("store: save demo execution run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get demo run id: %w", err)
	}
	run.ID = id
	payload, err = json.Marshal(run)
	if err != nil {
		return 0, fmt.Errorf("store: encode identified demo run: %w", err)
	}
	if _, err := tx.Exec(`UPDATE demo_runs SET payload = ? WHERE id = ?`, string(payload), id); err != nil {
		return 0, fmt.Errorf("store: update demo run payload: %w", err)
	}
	for i := range canonical {
		canonical[i].RunID, canonical[i].DemoRunID = &id, run.DemoRunID
		payload, err := json.Marshal(canonical[i])
		if err != nil {
			return 0, fmt.Errorf("store: encode demo event: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO demo_event_log (run_id, event_key, event_type, created_at, payload, demo_run_id) VALUES (?, ?, ?, ?, ?, ?)`, id, canonical[i].Key, canonical[i].Type, canonical[i].CreatedAt.Format(time.RFC3339Nano), string(payload), canonical[i].DemoRunID); err != nil {
			return 0, fmt.Errorf("store: append demo execution event: %w", err)
		}
	}
	if err := pruneDemoRetention(tx, "demo_event_log", time.Now().UTC()); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM demo_runs WHERE id NOT IN (SELECT id FROM demo_runs ORDER BY id DESC LIMIT 20)`); err != nil {
		return 0, fmt.Errorf("store: prune demo execution runs: %w", err)
	}
	return id, nil
}

func (s *Store) SaveDemoRun(run DemoRun) (int64, error) {
	if run.StartedAt.IsZero() || run.CompletedAt.IsZero() {
		return 0, fmt.Errorf("store: demo run timestamps must not be zero")
	}
	if run.CompletedAt.Before(run.StartedAt) {
		return 0, fmt.Errorf("store: demo run completed_at precedes started_at")
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return 0, fmt.Errorf("store: encode demo run: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin demo run tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO demo_runs (started_at, completed_at, success, payload, demo_run_id) VALUES (?, ?, ?, ?, ?)`,
		run.StartedAt.Format(time.RFC3339Nano), run.CompletedAt.Format(time.RFC3339Nano), run.Success, string(payload), run.DemoRunID,
	)
	if err != nil {
		return 0, fmt.Errorf("store: save demo run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get demo run id: %w", err)
	}
	run.ID = id
	payload, err = json.Marshal(run)
	if err != nil {
		return 0, fmt.Errorf("store: encode identified demo run: %w", err)
	}
	if _, err := tx.Exec(`UPDATE demo_runs SET payload = ? WHERE id = ?`, string(payload), id); err != nil {
		return 0, fmt.Errorf("store: update demo run payload: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM demo_runs WHERE id NOT IN (SELECT id FROM demo_runs ORDER BY id DESC LIMIT 20)`); err != nil {
		return 0, fmt.Errorf("store: prune demo runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit demo run: %w", err)
	}
	return id, nil
}

// SaveDemoExecution stores one run and all of its feed events in a single
// transaction, including both retention prunes. Every event is associated with
// the newly assigned run ID.
func (s *Store) SaveDemoExecution(run DemoRun, events []DemoEvent) (int64, error) {
	if run.StartedAt.IsZero() || run.CompletedAt.IsZero() {
		return 0, fmt.Errorf("store: demo run timestamps must not be zero")
	}
	if run.CompletedAt.Before(run.StartedAt) {
		return 0, fmt.Errorf("store: demo run completed_at precedes started_at")
	}
	canonical := make([]DemoEventRecord, len(events))
	for i, event := range events {
		normalized, err := normalizeDemoEventRecord(event)
		if err != nil {
			return 0, fmt.Errorf("store: demo event %d: %w", i, err)
		}
		canonical[i] = normalized
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return 0, fmt.Errorf("store: encode demo run: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin demo execution tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO demo_runs (started_at, completed_at, success, payload, demo_run_id) VALUES (?, ?, ?, ?, ?)`,
		run.StartedAt.Format(time.RFC3339Nano), run.CompletedAt.Format(time.RFC3339Nano), run.Success, string(payload), run.DemoRunID,
	)
	if err != nil {
		return 0, fmt.Errorf("store: save demo execution run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get demo execution run id: %w", err)
	}
	run.ID = id
	payload, err = json.Marshal(run)
	if err != nil {
		return 0, fmt.Errorf("store: encode identified demo execution run: %w", err)
	}
	if _, err := tx.Exec(`UPDATE demo_runs SET payload = ? WHERE id = ?`, string(payload), id); err != nil {
		return 0, fmt.Errorf("store: update demo execution run payload: %w", err)
	}

	for i := range canonical {
		canonical[i].RunID = &id
		canonical[i].DemoRunID = run.DemoRunID
		payload, err := json.Marshal(canonical[i])
		if err != nil {
			return 0, fmt.Errorf("store: encode demo execution event: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO demo_event_log (run_id, event_key, event_type, created_at, payload, demo_run_id) VALUES (?, ?, ?, ?, ?, ?)`,
			id, canonical[i].Key, canonical[i].Type, canonical[i].CreatedAt.Format(time.RFC3339Nano), string(payload), canonical[i].DemoRunID,
		); err != nil {
			return 0, fmt.Errorf("store: append demo execution event: %w", err)
		}
	}
	if err := pruneDemoRetention(tx, "demo_event_log", time.Now().UTC()); err != nil {
		return 0, fmt.Errorf("store: prune demo execution events: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM demo_runs WHERE id NOT IN (SELECT id FROM demo_runs ORDER BY id DESC LIMIT 20)`); err != nil {
		return 0, fmt.Errorf("store: prune demo execution runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit demo execution: %w", err)
	}
	return id, nil
}

func (s *Store) LatestDemoRun() (DemoRun, bool, error) {
	var run DemoRun
	var id int64
	var startedAt, completedAt, payload, demoRunID string
	var success int
	err := s.db.QueryRow(
		`SELECT id, started_at, completed_at, success, payload, demo_run_id FROM demo_runs ORDER BY id DESC LIMIT 1`,
	).Scan(&id, &startedAt, &completedAt, &success, &payload, &demoRunID)
	if err == sql.ErrNoRows {
		return DemoRun{}, false, nil
	}
	if err != nil {
		return DemoRun{}, false, fmt.Errorf("store: latest demo run: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &run); err != nil {
		return DemoRun{}, false, fmt.Errorf("store: decode demo run: %w", err)
	}
	run.ID = id
	run.DemoRunID = demoRunID
	run.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return DemoRun{}, false, fmt.Errorf("store: parse demo run started_at: %w", err)
	}
	run.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt)
	if err != nil {
		return DemoRun{}, false, fmt.Errorf("store: parse demo run completed_at: %w", err)
	}
	run.Success = success != 0
	return run, true, nil
}

func (s *Store) AppendDemoEvents(events []DemoEvent) error {
	canonical := make([]DemoEventRecord, len(events))
	for i, event := range events {
		normalized, err := normalizeDemoEventRecord(event)
		if err != nil {
			return fmt.Errorf("store: demo event %d: %w", i, err)
		}
		canonical[i] = normalized
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin demo events tx: %w", err)
	}
	defer tx.Rollback()

	for _, event := range canonical {
		var runID any
		if event.RunID != nil {
			runID = *event.RunID
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("store: encode demo event: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO demo_event_log (run_id, event_key, event_type, created_at, payload, demo_run_id) VALUES (?, ?, ?, ?, ?, ?)`,
			runID, event.Key, event.Type, event.CreatedAt.Format(time.RFC3339Nano), string(payload), event.DemoRunID,
		); err != nil {
			return fmt.Errorf("store: append demo event: %w", err)
		}
	}
	if err := pruneDemoRetention(tx, "demo_event_log", time.Now().UTC()); err != nil {
		return fmt.Errorf("store: prune demo events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit demo events: %w", err)
	}
	return nil
}

func (s *Store) ListDemoEvents(limit int) ([]DemoEvent, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, run_id, event_key, event_type, created_at, payload, demo_run_id FROM demo_event_log ORDER BY id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list demo events: %w", err)
	}
	defer rows.Close()

	events := make([]DemoEvent, 0, limit)
	for rows.Next() {
		var event DemoEvent
		var runID sql.NullInt64
		var createdAt, payload, demoRunID string
		var id int64
		var key, eventType string
		if err := rows.Scan(&id, &runID, &key, &eventType, &createdAt, &payload, &demoRunID); err != nil {
			return nil, fmt.Errorf("store: scan demo event: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, fmt.Errorf("store: decode demo event: %w", err)
		}
		event.ID = id
		event.Key = key
		event.Type = eventType
		event.DemoRunID = demoRunID
		if runID.Valid {
			value := runID.Int64
			event.RunID = &value
		}
		if event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("store: parse demo event created_at: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate demo events: %w", err)
	}
	return events, nil
}

func (s *Store) SaveDemoAudit(entry DemoAuditEntry) error {
	if entry.DemoRunID == "" || entry.Identity == "" || entry.Route == "" || entry.Action == "" || entry.Result == "" || entry.CreatedAt.IsZero() {
		return fmt.Errorf("store: incomplete demo audit entry")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin demo audit: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO demo_audit (demo_run_id, identity, route, action, result, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, entry.DemoRunID, entry.Identity, entry.Route, entry.Action, entry.Result, entry.Status, entry.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("store: save demo audit: %w", err)
	}
	if err := pruneDemoRetention(tx, "demo_audit", time.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit demo audit: %w", err)
	}
	return nil
}

func (s *Store) ListDemoAudit(limit int) ([]DemoAuditEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT demo_run_id, identity, route, action, result, status, created_at FROM demo_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list demo audit: %w", err)
	}
	defer rows.Close()
	entries := make([]DemoAuditEntry, 0, limit)
	for rows.Next() {
		var entry DemoAuditEntry
		var created string
		if err := rows.Scan(&entry.DemoRunID, &entry.Identity, &entry.Route, &entry.Action, &entry.Result, &entry.Status, &created); err != nil {
			return nil, fmt.Errorf("store: scan demo audit: %w", err)
		}
		entry.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("store: parse demo audit: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func pruneDemoRetention(tx *sql.Tx, table string, now time.Time) error {
	if table != "demo_event_log" && table != "demo_audit" {
		return fmt.Errorf("store: invalid demo retention table")
	}
	cutoff := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339Nano)
	query := `DELETE FROM ` + table + ` WHERE created_at < ? OR id NOT IN (SELECT id FROM ` + table + ` ORDER BY id DESC LIMIT 1000)`
	if _, err := tx.Exec(query, cutoff); err != nil {
		return fmt.Errorf("store: prune %s: %w", table, err)
	}
	return nil
}

// DemoTargets intersects registered device records with the explicit demo
// allowlist. It never falls back to all devices.
func (s *Store) DemoTargets(allowed []string) ([]Device, error) {
	if len(allowed) == 0 {
		return []Device{}, nil
	}
	placeholders := make([]string, 0, len(allowed))
	args := make([]any, 0, len(allowed))
	for _, id := range allowed {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := s.db.Query(`SELECT device_id, apns_token, widget_token, updated_at FROM devices WHERE device_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: select demo targets: %w", err)
	}
	defer rows.Close()
	byID := make(map[string]Device, len(allowed))
	for rows.Next() {
		var d Device
		var updatedAt string
		if err := rows.Scan(&d.DeviceID, &d.APNsToken, &d.WidgetToken, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: scan demo target: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("store: parse demo target: %w", err)
		}
		d.UpdatedAt = parsed
		byID[d.DeviceID] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate demo targets: %w", err)
	}
	targets := make([]Device, 0, len(byID))
	seen := make(map[string]bool, len(allowed))
	for _, id := range allowed {
		if d, ok := byID[id]; ok && !seen[id] {
			targets = append(targets, d)
			seen[id] = true
		}
	}
	return targets, nil
}

func (s *Store) ensureDemoMigrations() error {
	for _, migration := range []struct{ table, column, definition string }{
		{"demo_state", "last_demo_run_id", "TEXT NOT NULL DEFAULT ''"},
		{"demo_runs", "demo_run_id", "TEXT NOT NULL DEFAULT ''"},
		{"demo_event_log", "demo_run_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		rows, err := s.db.Query(`PRAGMA table_info(` + migration.table + `)`)
		if err != nil {
			return fmt.Errorf("store: inspect %s: %w", migration.table, err)
		}
		found := false
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull int
			var dflt any
			var pk int
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				return err
			}
			if name == migration.column {
				found = true
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if !found {
			if _, err := s.db.Exec(`ALTER TABLE ` + migration.table + ` ADD COLUMN ` + migration.column + ` ` + migration.definition); err != nil {
				return fmt.Errorf("store: migrate %s.%s: %w", migration.table, migration.column, err)
			}
		}
	}
	return nil
}

func normalizeDemoEventRecord(event DemoEventRecord) (DemoEventRecord, error) {
	legacy := event.Key == "" && event.Type == ""
	if event.Key == "" {
		event.Key = event.EventKey
	}
	if event.Type == "" {
		event.Type = event.EventType
		if legacy {
			switch event.Type {
			case "early", "threshold":
				event.Type = "early_threshold"
			case "test":
				event.Type = "test_alert"
			}
		}
	}
	if len(event.Payload) > 0 && !json.Valid(event.Payload) {
		return DemoEventRecord{}, fmt.Errorf("payload must be valid JSON")
	}
	if len(event.Key) < len("demo.") || event.Key[:len("demo.")] != "demo." {
		return DemoEventRecord{}, fmt.Errorf("key %q must start with demo.", event.Key)
	}
	if !allowedDemoEventTypes[event.Type] {
		return DemoEventRecord{}, fmt.Errorf("unsupported type %q", event.Type)
	}
	if event.CreatedAt.IsZero() {
		return DemoEventRecord{}, fmt.Errorf("created_at must not be zero")
	}
	event.EventKey = ""
	event.EventType = ""
	event.Payload = nil
	return event, nil
}
