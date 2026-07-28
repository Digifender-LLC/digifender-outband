// Package telemetry stores BMC samples in SQLite and serves latest snapshots.
package telemetry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"outband/internal/bmc"
)

type hostSnap struct {
	info        *bmc.MCInfo
	power       *bmc.PowerStatus
	sensors     []bmc.Sensor
	sel         []bmc.SELEntry
	lastError   string
	lastSuccess time.Time
	warm        bool
}

// Store is a SQLite-backed telemetry history plus in-memory latest snapshots.
type Store struct {
	db *sql.DB

	mu    sync.RWMutex
	hosts map[string]*hostSnap
}

// Sample is one numeric sensor reading at a point in time.
type Sample struct {
	TS     int64   `json:"ts"`
	Sensor string  `json:"sensor"`
	Kind   string  `json:"kind"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Status string  `json:"status"`
}

// Meta is collector health surfaced to the UI.
type Meta struct {
	LastError   string    `json:"last_error"`
	LastSuccess time.Time `json:"last_success"`
	Warm        bool      `json:"warm"`
}

// Open creates or opens the SQLite database under dataDir.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "outband.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, hosts: make(map[string]*hostSnap)}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS schema_version (
  version INTEGER NOT NULL
);
`); err != nil {
		return err
	}

	var ver int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&ver)
	if err != nil {
		return err
	}

	if ver < 1 {
		// Fresh install or pre-host_id schema: rebuild tables with host_id.
		if err := s.migrateToV1(); err != nil {
			return err
		}
		if _, err := s.db.Exec(`DELETE FROM schema_version; INSERT INTO schema_version(version) VALUES(1)`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateToV1() error {
	// Detect legacy tables without host_id and drop them (history wipe is acceptable).
	var samplesSQL string
	_ = s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='samples'`).Scan(&samplesSQL)
	if samplesSQL != "" && !strings.Contains(samplesSQL, "host_id") {
		_, _ = s.db.Exec(`DROP TABLE IF EXISTS samples; DROP TABLE IF EXISTS power; DROP TABLE IF EXISTS snapshots; DROP TABLE IF EXISTS meta`)
	}

	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS samples (
  host_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  sensor TEXT NOT NULL,
  kind TEXT NOT NULL,
  value REAL NOT NULL,
  unit TEXT NOT NULL,
  status TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS samples_host_sensor_ts ON samples(host_id, sensor, ts);
CREATE INDEX IF NOT EXISTS samples_host_ts ON samples(host_id, ts);

CREATE TABLE IF NOT EXISTS power (
  host_id TEXT NOT NULL,
  ts INTEGER NOT NULL,
  is_on INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS power_host_ts ON power(host_id, ts);

CREATE TABLE IF NOT EXISTS meta (
  host_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (host_id, key)
);

CREATE TABLE IF NOT EXISTS snapshots (
  host_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  ts INTEGER NOT NULL,
  payload TEXT NOT NULL,
  PRIMARY KEY (host_id, kind)
);
`)
	return err
}

func (s *Store) snap(hostID string) *hostSnap {
	h, ok := s.hosts[hostID]
	if !ok {
		h = &hostSnap{}
		s.hosts[hostID] = h
	}
	return h
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Meta returns collector status for a host.
func (s *Store) Meta(hostID string) Meta {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hosts[hostID]
	if h == nil {
		return Meta{}
	}
	return Meta{
		LastError:   h.lastError,
		LastSuccess: h.lastSuccess,
		Warm:        h.warm,
	}
}

// LatestMCInfo returns the last polled MC info (may be nil while warming).
func (s *Store) LatestMCInfo(hostID string) *bmc.MCInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hosts[hostID]
	if h == nil || h.info == nil {
		return nil
	}
	cp := *h.info
	return &cp
}

// LatestPower returns the last polled power status.
func (s *Store) LatestPower(hostID string) *bmc.PowerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hosts[hostID]
	if h == nil || h.power == nil {
		return nil
	}
	cp := *h.power
	return &cp
}

// LatestSensors returns the last polled sensor list.
func (s *Store) LatestSensors(hostID string) []bmc.Sensor {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hosts[hostID]
	if h == nil {
		return nil
	}
	return append([]bmc.Sensor(nil), h.sensors...)
}

// LatestSEL returns the last polled SEL entries.
func (s *Store) LatestSEL(hostID string) []bmc.SELEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.hosts[hostID]
	if h == nil {
		return nil
	}
	return append([]bmc.SELEntry(nil), h.sel...)
}

// SetError records a collector failure for a host.
func (s *Store) SetError(hostID, msg string) {
	s.mu.Lock()
	h := s.snap(hostID)
	h.lastError = msg
	s.mu.Unlock()
	_, _ = s.db.Exec(`INSERT INTO meta(host_id,key,value) VALUES(?,?,?)
		ON CONFLICT(host_id,key) DO UPDATE SET value=excluded.value`, hostID, "last_error", msg)
}

// RecordMCInfo updates the latest MC info snapshot.
func (s *Store) RecordMCInfo(hostID string, info *bmc.MCInfo) error {
	s.mu.Lock()
	h := s.snap(hostID)
	h.info = info
	h.touchSuccess()
	s.mu.Unlock()
	return s.writeSnapshot(hostID, "mcinfo", info)
}

// RecordPower writes a power sample and updates latest.
func (s *Store) RecordPower(hostID string, ps *bmc.PowerStatus) error {
	ts := time.Now().Unix()
	on := 0
	if ps.IsOn {
		on = 1
	}
	if _, err := s.db.Exec(`INSERT INTO power(host_id, ts, is_on) VALUES(?,?,?)`, hostID, ts, on); err != nil {
		return err
	}
	s.mu.Lock()
	h := s.snap(hostID)
	h.power = ps
	h.touchSuccess()
	s.mu.Unlock()
	return s.writeSnapshot(hostID, "power", ps)
}

// RecordSensors writes numeric samples and updates latest.
func (s *Store) RecordSensors(hostID string, sensors []bmc.Sensor) error {
	ts := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO samples(host_id, ts, sensor, kind, value, unit, status) VALUES(?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, sn := range sensors {
		kind := sensorKind(sn.Type)
		if v, ok := parseSensorValue(sn.Value); ok && sn.Present {
			if _, err := stmt.Exec(hostID, ts, sn.Name, kind, v, sn.Unit, sn.Status); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	s.mu.Lock()
	h := s.snap(hostID)
	h.sensors = append([]bmc.Sensor(nil), sensors...)
	h.touchSuccess()
	s.mu.Unlock()
	return s.writeSnapshot(hostID, "sensors", sensors)
}

// RecordSEL updates the latest SEL snapshot.
func (s *Store) RecordSEL(hostID string, entries []bmc.SELEntry) error {
	s.mu.Lock()
	h := s.snap(hostID)
	h.sel = append([]bmc.SELEntry(nil), entries...)
	h.touchSuccess()
	s.mu.Unlock()
	return s.writeSnapshot(hostID, "sel", entries)
}

func (h *hostSnap) touchSuccess() {
	h.lastSuccess = time.Now()
	h.lastError = ""
	h.warm = true
}

func (s *Store) writeSnapshot(hostID, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO snapshots(host_id, kind, ts, payload) VALUES(?,?,?,?)
		ON CONFLICT(host_id, kind) DO UPDATE SET ts=excluded.ts, payload=excluded.payload`,
		hostID, kind, time.Now().Unix(), string(b))
	return err
}

// QuerySamples returns history for one sensor between from and to (unix seconds).
func (s *Store) QuerySamples(hostID, sensor string, from, to int64) ([]Sample, error) {
	rows, err := s.db.Query(`
SELECT ts, sensor, kind, value, unit, status
FROM samples
WHERE host_id = ? AND sensor = ? AND ts >= ? AND ts <= ?
ORDER BY ts ASC`, hostID, sensor, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var sm Sample
		if err := rows.Scan(&sm.TS, &sm.Sensor, &sm.Kind, &sm.Value, &sm.Unit, &sm.Status); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// ListSensorNames returns distinct sensor names that have samples in the window.
func (s *Store) ListSensorNames(hostID string, from, to int64) ([]string, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT sensor FROM samples
WHERE host_id = ? AND ts >= ? AND ts <= ?
ORDER BY sensor`, hostID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Prune deletes samples and power rows older than retention.
func (s *Store) Prune(retention time.Duration) error {
	cut := time.Now().Add(-retention).Unix()
	if _, err := s.db.Exec(`DELETE FROM samples WHERE ts < ?`, cut); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM power WHERE ts < ?`, cut); err != nil {
		return err
	}
	return nil
}

// LoadSnapshots hydrates in-memory latest for one host from DB.
func (s *Store) LoadSnapshots(hostID string) error {
	rows, err := s.db.Query(`SELECT kind, ts, payload FROM snapshots WHERE host_id = ?`, hostID)
	if err != nil {
		return err
	}
	defer rows.Close()

	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.snap(hostID)
	for rows.Next() {
		var kind string
		var ts int64
		var payload string
		if err := rows.Scan(&kind, &ts, &payload); err != nil {
			return err
		}
		switch kind {
		case "mcinfo":
			var info bmc.MCInfo
			if json.Unmarshal([]byte(payload), &info) == nil {
				// Migrate legacy JSON that used IPMIVersion.
				if info.ProtocolVersion == "" {
					var legacy struct {
						IPMIVersion string `json:"IPMIVersion"`
					}
					if json.Unmarshal([]byte(payload), &legacy) == nil {
						info.ProtocolVersion = legacy.IPMIVersion
					}
				}
				h.info = &info
				h.warm = true
				h.lastSuccess = time.Unix(ts, 0)
			}
		case "power":
			var ps bmc.PowerStatus
			if json.Unmarshal([]byte(payload), &ps) == nil {
				h.power = &ps
				h.warm = true
				h.lastSuccess = time.Unix(ts, 0)
			}
		case "sensors":
			var sensors []bmc.Sensor
			if json.Unmarshal([]byte(payload), &sensors) == nil {
				h.sensors = sensors
				h.warm = true
				h.lastSuccess = time.Unix(ts, 0)
			}
		case "sel":
			var entries []bmc.SELEntry
			if json.Unmarshal([]byte(payload), &entries) == nil {
				h.sel = entries
				h.warm = true
				h.lastSuccess = time.Unix(ts, 0)
			}
		}
	}
	return rows.Err()
}
