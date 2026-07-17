package server

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	fetched_at TEXT NOT NULL,
	payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
	device_id TEXT PRIMARY KEY,
	apns_token TEXT NOT NULL DEFAULT '',
	widget_token TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
	event_key TEXT PRIMARY KEY,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS window_state (
	window_id TEXT PRIMARY KEY,
	used_percent REAL NOT NULL,
	resets_at TEXT,
	credits_available INTEGER,
	updated_at TEXT NOT NULL
);
`

var defaultSettings = map[string]string{
	"poll_interval_minutes": "5",
	"provider_order":        `["codex","claude","grok"]`,
	"hidden_providers":      `[]`,
	"notifications_enabled": "true",
	"early_threshold_pct":   "10",
	"danger_threshold_pct":  "10",
}

func OpenStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	s := &Store{db: db}
	if err := s.seedDefaultSettings(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) seedDefaultSettings() error {
	for key, value := range defaultSettings {
		_, err := s.db.Exec(
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
			key, value,
		)
		if err != nil {
			return fmt.Errorf("store: seed setting %s: %w", key, err)
		}
	}
	return nil
}

func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("store: get setting %s: %w", key, err)
	}
	return value, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("store: set setting %s: %w", key, err)
	}
	return nil
}

func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("store: list settings: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: scan setting: %w", err)
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) SaveSnapshot(fetchedAt time.Time, payload []byte) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO snapshots (fetched_at, payload) VALUES (?, ?)`, fetchedAt.Format(time.RFC3339), string(payload)); err != nil {
		return fmt.Errorf("store: insert snapshot: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM snapshots WHERE id NOT IN (SELECT id FROM snapshots ORDER BY id DESC LIMIT 1)`); err != nil {
		return fmt.Errorf("store: prune snapshots: %w", err)
	}
	return tx.Commit()
}

func (s *Store) LatestSnapshot() (fetchedAt time.Time, payload []byte, ok bool, err error) {
	var fetchedAtStr, payloadStr string
	row := s.db.QueryRow(`SELECT fetched_at, payload FROM snapshots ORDER BY id DESC LIMIT 1`)
	if scanErr := row.Scan(&fetchedAtStr, &payloadStr); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return time.Time{}, nil, false, nil
		}
		return time.Time{}, nil, false, fmt.Errorf("store: latest snapshot: %w", scanErr)
	}
	parsed, parseErr := time.Parse(time.RFC3339, fetchedAtStr)
	if parseErr != nil {
		return time.Time{}, nil, false, fmt.Errorf("store: parse fetched_at: %w", parseErr)
	}
	return parsed, []byte(payloadStr), true, nil
}

func (s *Store) UpsertDevice(deviceID, apnsToken, widgetToken string) error {
	_, err := s.db.Exec(
		`INSERT INTO devices (device_id, apns_token, widget_token, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET apns_token = excluded.apns_token, widget_token = excluded.widget_token, updated_at = excluded.updated_at`,
		deviceID, apnsToken, widgetToken, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("store: upsert device: %w", err)
	}
	return nil
}

type Device struct {
	DeviceID    string
	APNsToken   string
	WidgetToken string
	UpdatedAt   time.Time
}

func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT device_id, apns_token, widget_token, updated_at FROM devices`)
	if err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	defer rows.Close()

	var devices []Device
	for rows.Next() {
		var d Device
		var updatedAtStr string
		if err := rows.Scan(&d.DeviceID, &d.APNsToken, &d.WidgetToken, &updatedAtStr); err != nil {
			return nil, fmt.Errorf("store: scan device: %w", err)
		}
		d.UpdatedAt, err = time.Parse(time.RFC3339, updatedAtStr)
		if err != nil {
			return nil, fmt.Errorf("store: parse device updated_at: %w", err)
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (s *Store) EventNotified(eventKey string) (bool, error) {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM events WHERE event_key = ?`, eventKey).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: check event: %w", err)
	}
	return true, nil
}

func (s *Store) RecordEvent(eventKey string) error {
	_, err := s.db.Exec(
		`INSERT INTO events (event_key, created_at) VALUES (?, ?) ON CONFLICT(event_key) DO NOTHING`,
		eventKey, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("store: record event: %w", err)
	}
	return nil
}

type WindowState struct {
	WindowID         string
	UsedPercent      float64
	ResetsAt         *time.Time
	CreditsAvailable *int
	UpdatedAt        time.Time
}

func (s *Store) GetWindowState(windowID string) (WindowState, bool, error) {
	var ws WindowState
	var resetsAtStr sql.NullString
	var creditsAvailable sql.NullInt64
	var updatedAtStr string

	err := s.db.QueryRow(
		`SELECT window_id, used_percent, resets_at, credits_available, updated_at FROM window_state WHERE window_id = ?`,
		windowID,
	).Scan(&ws.WindowID, &ws.UsedPercent, &resetsAtStr, &creditsAvailable, &updatedAtStr)
	if err == sql.ErrNoRows {
		return WindowState{}, false, nil
	}
	if err != nil {
		return WindowState{}, false, fmt.Errorf("store: get window state: %w", err)
	}

	if resetsAtStr.Valid {
		t, parseErr := time.Parse(time.RFC3339, resetsAtStr.String)
		if parseErr != nil {
			return WindowState{}, false, fmt.Errorf("store: parse window resets_at: %w", parseErr)
		}
		ws.ResetsAt = &t
	}
	if creditsAvailable.Valid {
		v := int(creditsAvailable.Int64)
		ws.CreditsAvailable = &v
	}
	ws.UpdatedAt, err = time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return WindowState{}, false, fmt.Errorf("store: parse window updated_at: %w", err)
	}
	return ws, true, nil
}

func (s *Store) SetWindowState(ws WindowState) error {
	var resetsAtStr any
	if ws.ResetsAt != nil {
		resetsAtStr = ws.ResetsAt.Format(time.RFC3339)
	}
	var creditsAvailable any
	if ws.CreditsAvailable != nil {
		creditsAvailable = *ws.CreditsAvailable
	}

	_, err := s.db.Exec(
		`INSERT INTO window_state (window_id, used_percent, resets_at, credits_available, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(window_id) DO UPDATE SET used_percent = excluded.used_percent, resets_at = excluded.resets_at,
			credits_available = excluded.credits_available, updated_at = excluded.updated_at`,
		ws.WindowID, ws.UsedPercent, resetsAtStr, creditsAvailable, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("store: set window state: %w", err)
	}
	return nil
}
