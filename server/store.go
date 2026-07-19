package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
);
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
CREATE TABLE IF NOT EXISTS poll_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	polled_at TEXT NOT NULL,
	success INTEGER NOT NULL,
	snapshot_changed INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS window_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_id TEXT NOT NULL,
	window_id TEXT NOT NULL,
	reset_epoch TEXT NOT NULL,
	sampled_at TEXT NOT NULL,
	used_percent REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS window_samples_lookup
	ON window_samples(window_id, reset_epoch, sampled_at);
CREATE TABLE IF NOT EXISTS alert_rules (
	provider_id TEXT NOT NULL,
	window_id TEXT NOT NULL DEFAULT '',
	rule_json TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY(provider_id, window_id)
);
CREATE TABLE IF NOT EXISTS alert_delivery_state (
	window_id TEXT NOT NULL,
	reset_epoch TEXT NOT NULL,
	policy_fingerprint TEXT NOT NULL,
	last_delivered_at TEXT NOT NULL,
	PRIMARY KEY(window_id, reset_epoch, policy_fingerprint)
);
CREATE TABLE IF NOT EXISTS device_test_results (
	device_id TEXT PRIMARY KEY,
	attempted_at TEXT NOT NULL,
	alert_attempted INTEGER NOT NULL,
	alert_accepted INTEGER NOT NULL,
	widget_attempted INTEGER NOT NULL,
	widget_accepted INTEGER NOT NULL,
	detail TEXT NOT NULL DEFAULT ''
);
`

const CurrentSchemaVersion = 1

var defaultSettings = map[string]string{
	"poll_interval_minutes":           "5",
	"provider_order":                  `["codex","claude","grok"]`,
	"hidden_providers":                `[]`,
	"demo_provider_enabled":           "false",
	"notifications_enabled":           "true",
	"early_threshold_pct":             "10",
	"danger_threshold_pct":            "10",
	"default_repeat_interval_minutes": "0",
	"quiet_hours":                     `{"enabled":false,"startMinute":1320,"endMinute":420,"timeZone":"UTC"}`,
}

func OpenStore(dbPath string) (*Store, error) {
	// Snapshots and device push tokens are sensitive. Ensure ordinary filesystem
	// database paths are never created with a process-default world-readable mode.
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file:") {
		file, err := os.OpenFile(dbPath, os.O_CREATE, 0600)
		if err != nil {
			return nil, fmt.Errorf("store: create db securely: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("store: close db file: %w", err)
		}
		if err := os.Chmod(dbPath, 0600); err != nil {
			return nil, fmt.Errorf("store: secure db permissions: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: open db: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("store: begin migrations: %w", err)
	}
	if _, err := tx.Exec(schema); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	if _, err := tx.Exec(demoSchema); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("store: apply demo schema: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?) ON CONFLICT(version) DO NOTHING`, CurrentSchemaVersion, time.Now().UTC().Format(time.RFC3339)); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("store: record schema migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: commit migrations: %w", err)
	}
	s := &Store{db: db}
	if err := s.seedDefaultSettings(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.seedDefaultDemoState(time.Now().UTC()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) SchemaVersion() (int, error) {
	var version int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return version, nil
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

func (s *Store) ListAlertOverrides() ([]AlertOverride, error) {
	rows, err := s.db.Query(`SELECT provider_id, window_id, rule_json FROM alert_rules ORDER BY provider_id, window_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list alert rules: %w", err)
	}
	defer rows.Close()
	var out []AlertOverride
	for rows.Next() {
		var providerID, windowID, raw string
		if err := rows.Scan(&providerID, &windowID, &raw); err != nil {
			return nil, fmt.Errorf("store: scan alert rule: %w", err)
		}
		var rule AlertRule
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			return nil, fmt.Errorf("store: decode alert rule: %w", err)
		}
		o := AlertOverride{ProviderID: providerID, Rule: rule}
		if windowID != "" {
			o.WindowID = &windowID
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceAlertOverrides(overrides []AlertOverride) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin alert rules: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM alert_rules`); err != nil {
		return fmt.Errorf("store: clear alert rules: %w", err)
	}
	for _, override := range overrides {
		windowID := ""
		if override.WindowID != nil {
			windowID = *override.WindowID
		}
		raw, _ := json.Marshal(override.Rule)
		if _, err := tx.Exec(`INSERT INTO alert_rules(provider_id,window_id,rule_json,updated_at) VALUES(?,?,?,?)`, override.ProviderID, windowID, string(raw), time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("store: insert alert rule: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) DangerDeliveryDue(windowID, resetEpoch, fingerprint string, now time.Time, interval time.Duration) (bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT last_delivered_at FROM alert_delivery_state WHERE window_id=? AND reset_epoch=? AND policy_fingerprint=?`, windowID, resetEpoch, fingerprint).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("store: read danger delivery: %w", err)
	}
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err == nil {
		last, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return false, fmt.Errorf("store: parse danger delivery: %w", parseErr)
		}
		if interval <= 0 || now.Sub(last) < interval {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) RecordDangerDelivery(windowID, resetEpoch, fingerprint string, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO alert_delivery_state(window_id,reset_epoch,policy_fingerprint,last_delivered_at) VALUES(?,?,?,?) ON CONFLICT(window_id,reset_epoch,policy_fingerprint) DO UPDATE SET last_delivered_at=excluded.last_delivered_at`, windowID, resetEpoch, fingerprint, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: record danger delivery: %w", err)
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

// SaveSnapshotWithForecasts records eligible history and the enriched latest
// snapshot in one transaction. A failed write therefore cannot leave samples
// ahead of the snapshot that produced them.
func (s *Store) SaveSnapshotWithForecasts(snap *Snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin snapshot forecast tx: %w", err)
	}
	defer tx.Rollback()

	if snap.Stale {
		clearForecasts(snap)
	} else {
		for pi := range snap.Providers {
			p := &snap.Providers[pi]
			if p.ID == "demo" || p.Stale || p.Error != "" {
				for wi := range p.Windows {
					p.Windows[wi].Forecast = nil
				}
				continue
			}
			for wi := range p.Windows {
				w := &p.Windows[wi]
				if w.ResetsAt == nil {
					continue
				}
				epoch := w.ResetsAt.UTC().Format(time.RFC3339)
				if _, err := tx.Exec(`INSERT INTO window_samples(provider_id, window_id, reset_epoch, sampled_at, used_percent) VALUES(?,?,?,?,?)`,
					p.ID, w.ID, epoch, snap.FetchedAt.UTC().Format(time.RFC3339Nano), w.UsedPercent); err != nil {
					return fmt.Errorf("store: insert window sample: %w", err)
				}
				forecast, err := forecastWindowTx(tx, w.ID, epoch, *w.ResetsAt, w.UsedPercent, snap.FetchedAt)
				if err != nil {
					return err
				}
				w.Forecast = forecast
				if _, err := tx.Exec(`DELETE FROM window_samples WHERE window_id=? AND id NOT IN (SELECT id FROM window_samples WHERE window_id=? ORDER BY sampled_at DESC, id DESC LIMIT 500)`, w.ID, w.ID); err != nil {
					return fmt.Errorf("store: cap window samples: %w", err)
				}
			}
		}
		cutoff := snap.FetchedAt.Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`DELETE FROM window_samples WHERE sampled_at < ?`, cutoff); err != nil {
			return fmt.Errorf("store: prune window samples: %w", err)
		}
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("store: marshal enriched snapshot: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO snapshots(fetched_at,payload) VALUES(?,?)`, snap.FetchedAt.UTC().Format(time.RFC3339), string(payload)); err != nil {
		return fmt.Errorf("store: insert enriched snapshot: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM snapshots WHERE id NOT IN (SELECT id FROM snapshots ORDER BY id DESC LIMIT 1)`); err != nil {
		return fmt.Errorf("store: prune snapshots: %w", err)
	}
	return tx.Commit()
}

func clearForecasts(snap *Snapshot) {
	for pi := range snap.Providers {
		for wi := range snap.Providers[pi].Windows {
			snap.Providers[pi].Windows[wi].Forecast = nil
		}
	}
}

func (s *Store) SavePollOutcome(result PollResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin poll outcome: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO poll_runs (polled_at, success, snapshot_changed, duration_ms, error) VALUES (?, ?, ?, ?, ?)`,
		result.PolledAt.Format(time.RFC3339Nano), result.Success, result.SnapshotChanged, result.DurationMS, truncateDiagnostic(result.Error, 240),
	); err != nil {
		return fmt.Errorf("store: save poll outcome: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM poll_runs WHERE id NOT IN (SELECT id FROM poll_runs ORDER BY id DESC LIMIT 50)`); err != nil {
		return fmt.Errorf("store: prune poll outcomes: %w", err)
	}
	return tx.Commit()
}

func (s *Store) LatestPollOutcome() (PollResult, bool, error) {
	var result PollResult
	var polledAt string
	err := s.db.QueryRow(
		`SELECT polled_at, success, snapshot_changed, duration_ms, error FROM poll_runs ORDER BY id DESC LIMIT 1`,
	).Scan(&polledAt, &result.Success, &result.SnapshotChanged, &result.DurationMS, &result.Error)
	if err == sql.ErrNoRows {
		return PollResult{}, false, nil
	}
	if err != nil {
		return PollResult{}, false, fmt.Errorf("store: latest poll outcome: %w", err)
	}
	result.PolledAt, err = time.Parse(time.RFC3339Nano, polledAt)
	if err != nil {
		return PollResult{}, false, fmt.Errorf("store: parse poll outcome: %w", err)
	}
	return result, true, nil
}

func (s *Store) RecentPollOutcomes(limit int) ([]PollResult, error) {
	if limit < 1 || limit > 50 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT polled_at, success, snapshot_changed, duration_ms, error FROM (SELECT * FROM poll_runs ORDER BY id DESC LIMIT ?) ORDER BY id ASC`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: recent poll outcomes: %w", err)
	}
	defer rows.Close()
	var results []PollResult
	for rows.Next() {
		var result PollResult
		var polledAt string
		if err := rows.Scan(&polledAt, &result.Success, &result.SnapshotChanged, &result.DurationMS, &result.Error); err != nil {
			return nil, fmt.Errorf("store: scan poll outcome: %w", err)
		}
		result.PolledAt, err = time.Parse(time.RFC3339Nano, polledAt)
		if err != nil {
			return nil, fmt.Errorf("store: parse poll outcome: %w", err)
		}
		results = append(results, result)
	}
	return results, rows.Err()
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

type DeviceTestResult struct {
	DeviceID        string
	AttemptedAt     time.Time
	AlertAttempted  bool
	AlertAccepted   bool
	WidgetAttempted bool
	WidgetAccepted  bool
	Detail          string
}

func (s *Store) SaveDeviceTestResult(result DeviceTestResult) error {
	_, err := s.db.Exec(`INSERT INTO device_test_results(device_id,attempted_at,alert_attempted,alert_accepted,widget_attempted,widget_accepted,detail) VALUES(?,?,?,?,?,?,?) ON CONFLICT(device_id) DO UPDATE SET attempted_at=excluded.attempted_at,alert_attempted=excluded.alert_attempted,alert_accepted=excluded.alert_accepted,widget_attempted=excluded.widget_attempted,widget_accepted=excluded.widget_accepted,detail=excluded.detail`, result.DeviceID, result.AttemptedAt.UTC().Format(time.RFC3339Nano), result.AlertAttempted, result.AlertAccepted, result.WidgetAttempted, result.WidgetAccepted, truncateDiagnostic(result.Detail, 240))
	if err != nil {
		return fmt.Errorf("store: save device test result: %w", err)
	}
	return nil
}

func (s *Store) LatestDeviceTestResult(deviceID string) (DeviceTestResult, bool, error) {
	var result DeviceTestResult
	var raw string
	err := s.db.QueryRow(`SELECT device_id,attempted_at,alert_attempted,alert_accepted,widget_attempted,widget_accepted,detail FROM device_test_results WHERE device_id=?`, deviceID).Scan(&result.DeviceID, &raw, &result.AlertAttempted, &result.AlertAccepted, &result.WidgetAttempted, &result.WidgetAccepted, &result.Detail)
	if err == sql.ErrNoRows {
		return DeviceTestResult{}, false, nil
	}
	if err != nil {
		return DeviceTestResult{}, false, fmt.Errorf("store: latest device test: %w", err)
	}
	result.AttemptedAt, err = time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return DeviceTestResult{}, false, fmt.Errorf("store: parse device test: %w", err)
	}
	return result, true, nil
}

func (s *Store) GetDevice(deviceID string) (Device, bool, error) {
	var d Device
	var updatedAtStr string
	err := s.db.QueryRow(
		`SELECT device_id, apns_token, widget_token, updated_at FROM devices WHERE device_id = ?`,
		deviceID,
	).Scan(&d.DeviceID, &d.APNsToken, &d.WidgetToken, &updatedAtStr)
	if err == sql.ErrNoRows {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, fmt.Errorf("store: get device: %w", err)
	}
	d.UpdatedAt, err = time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return Device{}, false, fmt.Errorf("store: parse device updated_at: %w", err)
	}
	return d, true, nil
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

func (s *Store) DeleteDevice(deviceID string) error {
	_, err := s.db.Exec(`DELETE FROM devices WHERE device_id = ?`, deviceID)
	if err != nil {
		return fmt.Errorf("store: delete device: %w", err)
	}
	return nil
}

func (s *Store) ClearAPNsToken(deviceID string) error {
	_, err := s.db.Exec(`UPDATE devices SET apns_token = '', updated_at = ? WHERE device_id = ?`, time.Now().UTC().Format(time.RFC3339), deviceID)
	if err != nil {
		return fmt.Errorf("store: clear APNs token: %w", err)
	}
	return nil
}

func (s *Store) ClearWidgetToken(deviceID string) error {
	_, err := s.db.Exec(`UPDATE devices SET widget_token = '', updated_at = ? WHERE device_id = ?`, time.Now().UTC().Format(time.RFC3339), deviceID)
	if err != nil {
		return fmt.Errorf("store: clear widget token: %w", err)
	}
	return nil
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
