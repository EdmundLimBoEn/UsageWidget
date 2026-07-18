package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const demoSchema = `
CREATE TABLE IF NOT EXISTS demo_state (
    key TEXT PRIMARY KEY CHECK (key = 'demo.state'),
    payload TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS demo_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    success INTEGER NOT NULL,
    payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS demo_event_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER,
    event_key TEXT NOT NULL CHECK (event_key LIKE 'demo.%'),
    event_type TEXT NOT NULL,
    created_at TEXT NOT NULL,
    payload TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_demo_event_log_recent ON demo_event_log(id DESC);
CREATE INDEX IF NOT EXISTS idx_demo_event_log_type_recent ON demo_event_log(event_type, id DESC);
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

	// Compatibility fields for the preliminary Task 1 store contract. New code
	// uses Key and Type; these fields are never included in persisted JSON.
	EventKey  string          `json:"-"`
	EventType string          `json:"-"`
	Payload   json.RawMessage `json:"-"`
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
	state := DefaultDemoState(now)
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("store: encode default demo state: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO demo_state (key, payload, updated_at) VALUES ('demo.state', ?, ?) ON CONFLICT(key) DO NOTHING`,
		string(payload), state.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store: seed demo state: %w", err)
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

func (s *Store) SaveDemoState(state DemoState) error {
	if err := validateDemoState(state, state.UpdatedAt); err != nil {
		return fmt.Errorf("store: validate demo state: %w", err)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("store: encode demo state: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO demo_state (key, payload, updated_at) VALUES ('demo.state', ?, ?)
		 ON CONFLICT(key) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		string(payload), state.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store: save demo state: %w", err)
	}
	return nil
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
		`INSERT INTO demo_runs (started_at, completed_at, success, payload) VALUES (?, ?, ?, ?)`,
		run.StartedAt.Format(time.RFC3339Nano), run.CompletedAt.Format(time.RFC3339Nano), run.Success, string(payload),
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
		`INSERT INTO demo_runs (started_at, completed_at, success, payload) VALUES (?, ?, ?, ?)`,
		run.StartedAt.Format(time.RFC3339Nano), run.CompletedAt.Format(time.RFC3339Nano), run.Success, string(payload),
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
		payload, err := json.Marshal(canonical[i])
		if err != nil {
			return 0, fmt.Errorf("store: encode demo execution event: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO demo_event_log (run_id, event_key, event_type, created_at, payload) VALUES (?, ?, ?, ?, ?)`,
			id, canonical[i].Key, canonical[i].Type, canonical[i].CreatedAt.Format(time.RFC3339Nano), string(payload),
		); err != nil {
			return 0, fmt.Errorf("store: append demo execution event: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM demo_event_log WHERE id NOT IN (SELECT id FROM demo_event_log ORDER BY id DESC LIMIT 500)`); err != nil {
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
	var startedAt, completedAt, payload string
	var success int
	err := s.db.QueryRow(
		`SELECT id, started_at, completed_at, success, payload FROM demo_runs ORDER BY id DESC LIMIT 1`,
	).Scan(&id, &startedAt, &completedAt, &success, &payload)
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
			`INSERT INTO demo_event_log (run_id, event_key, event_type, created_at, payload) VALUES (?, ?, ?, ?, ?)`,
			runID, event.Key, event.Type, event.CreatedAt.Format(time.RFC3339Nano), string(payload),
		); err != nil {
			return fmt.Errorf("store: append demo event: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM demo_event_log WHERE id NOT IN (SELECT id FROM demo_event_log ORDER BY id DESC LIMIT 500)`); err != nil {
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
		`SELECT id, run_id, event_key, event_type, created_at, payload FROM demo_event_log ORDER BY id DESC LIMIT ?`,
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
		var createdAt, payload string
		var id int64
		var key, eventType string
		if err := rows.Scan(&id, &runID, &key, &eventType, &createdAt, &payload); err != nil {
			return nil, fmt.Errorf("store: scan demo event: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, fmt.Errorf("store: decode demo event: %w", err)
		}
		event.ID = id
		event.Key = key
		event.Type = eventType
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
