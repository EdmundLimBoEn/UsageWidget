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

type DemoRun struct {
	ID          int64           `json:"id"`
	StartedAt   time.Time       `json:"startedAt"`
	CompletedAt time.Time       `json:"completedAt"`
	Success     bool            `json:"success"`
	Payload     json.RawMessage `json:"payload"`
}

type DemoEvent struct {
	ID        int64           `json:"id"`
	RunID     *int64          `json:"runId,omitempty"`
	EventKey  string          `json:"eventKey"`
	EventType string          `json:"eventType"`
	CreatedAt time.Time       `json:"createdAt"`
	Payload   json.RawMessage `json:"payload"`
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
	if !json.Valid(run.Payload) {
		return 0, fmt.Errorf("store: demo run payload must be valid JSON")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin demo run tx: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(
		`INSERT INTO demo_runs (started_at, completed_at, success, payload) VALUES (?, ?, ?, ?)`,
		run.StartedAt.Format(time.RFC3339Nano), run.CompletedAt.Format(time.RFC3339Nano), run.Success, string(run.Payload),
	)
	if err != nil {
		return 0, fmt.Errorf("store: save demo run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get demo run id: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM demo_runs WHERE id NOT IN (SELECT id FROM demo_runs ORDER BY id DESC LIMIT 20)`); err != nil {
		return 0, fmt.Errorf("store: prune demo runs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit demo run: %w", err)
	}
	return id, nil
}

func (s *Store) LatestDemoRun() (DemoRun, bool, error) {
	var run DemoRun
	var startedAt, completedAt, payload string
	var success int
	err := s.db.QueryRow(
		`SELECT id, started_at, completed_at, success, payload FROM demo_runs ORDER BY id DESC LIMIT 1`,
	).Scan(&run.ID, &startedAt, &completedAt, &success, &payload)
	if err == sql.ErrNoRows {
		return DemoRun{}, false, nil
	}
	if err != nil {
		return DemoRun{}, false, fmt.Errorf("store: latest demo run: %w", err)
	}
	if run.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return DemoRun{}, false, fmt.Errorf("store: parse demo run started_at: %w", err)
	}
	if run.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt); err != nil {
		return DemoRun{}, false, fmt.Errorf("store: parse demo run completed_at: %w", err)
	}
	run.Success = success != 0
	run.Payload = json.RawMessage(payload)
	return run, true, nil
}

func (s *Store) AppendDemoEvents(events []DemoEvent) error {
	for i, event := range events {
		if !json.Valid(event.Payload) {
			return fmt.Errorf("store: demo event %d payload must be valid JSON", i)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin demo events tx: %w", err)
	}
	defer tx.Rollback()

	for _, event := range events {
		var runID any
		if event.RunID != nil {
			runID = *event.RunID
		}
		if _, err := tx.Exec(
			`INSERT INTO demo_event_log (run_id, event_key, event_type, created_at, payload) VALUES (?, ?, ?, ?, ?)`,
			runID, event.EventKey, event.EventType, event.CreatedAt.Format(time.RFC3339Nano), string(event.Payload),
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
		if err := rows.Scan(&event.ID, &runID, &event.EventKey, &event.EventType, &createdAt, &payload); err != nil {
			return nil, fmt.Errorf("store: scan demo event: %w", err)
		}
		if runID.Valid {
			value := runID.Int64
			event.RunID = &value
		}
		if event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("store: parse demo event created_at: %w", err)
		}
		event.Payload = json.RawMessage(payload)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate demo events: %w", err)
	}
	return events, nil
}
