package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/naming"
	"github.com/yaodao0yaodao/proxylens/internal/subscription"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB   *sql.DB
	Path string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) Backup() ([]byte, error) {
	if _, err := s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return nil, err
	}
	return os.ReadFile(s.Path)
}

func (s *Store) migrate() error {
	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		var legacy int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='nodes'`).Scan(&legacy); err != nil {
			return err
		}
		if legacy != 0 {
			if err := s.migrateLegacyToV2(); err != nil {
				return err
			}
			if err := s.migrateV6ToV7(); err != nil {
				return err
			}
			if err := s.migrateV7ToV8(); err != nil {
				return err
			}
			if err := s.migrateV8ToV9(); err != nil {
				return err
			}
			if err := s.migrateV9ToV10(); err != nil {
				return err
			}
			if err := s.migrateV10ToV11(); err != nil {
				return err
			}
			if err := s.migrateV11ToV12(); err != nil {
				return err
			}
			return s.migrateV12ToV13()
		}
	}
	if version > 13 {
		return fmt.Errorf("database schema version %d is newer than supported version 13", version)
	}
	if version == 2 {
		if err := s.migrateV2ToV3(); err != nil {
			return err
		}
		version = 3
	}
	if version == 3 {
		if err := s.migrateV3ToV4(); err != nil {
			return err
		}
		version = 4
	}
	if version == 4 {
		if err := s.migrateV4ToV5(); err != nil {
			return err
		}
		version = 5
	}
	if version == 5 {
		if err := s.migrateV5ToV6(); err != nil {
			return err
		}
		version = 6
	}
	if version == 6 {
		if err := s.migrateV6ToV7(); err != nil {
			return err
		}
		version = 7
	}
	if version == 7 {
		if err := s.migrateV7ToV8(); err != nil {
			return err
		}
		version = 8
	}
	if version == 8 {
		if err := s.migrateV8ToV9(); err != nil {
			return err
		}
		version = 9
	}
	if version == 9 {
		if err := s.migrateV9ToV10(); err != nil {
			return err
		}
		version = 10
	}
	if version == 10 {
		if err := s.migrateV10ToV11(); err != nil {
			return err
		}
		version = 11
	}
	if version == 11 {
		if err := s.migrateV11ToV12(); err != nil {
			return err
		}
		version = 12
	}
	if version == 12 {
		return s.migrateV12ToV13()
	}
	if version == 13 {
		return nil
	}
	const schema = `CREATE TABLE tasks (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, subscription_url TEXT NOT NULL,
 subscription_ua TEXT NOT NULL DEFAULT 'clash.meta', publish_token TEXT NOT NULL UNIQUE,
 enabled INTEGER NOT NULL DEFAULT 1, last_subscription_at TEXT, last_rules_at TEXT,
 last_sfa_at TEXT, last_carton_at TEXT, last_detection_at TEXT, last_error TEXT NOT NULL DEFAULT '',
 subscription_upload INTEGER NOT NULL DEFAULT 0, subscription_download INTEGER NOT NULL DEFAULT 0,
 subscription_total INTEGER NOT NULL DEFAULT 0, subscription_expire TEXT, subscription_info_at TEXT,
 subscription_attempt_at TEXT, subscription_failed_since TEXT,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE nodes (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 number INTEGER NOT NULL DEFAULT 0, protocol TEXT NOT NULL, server TEXT NOT NULL, port INTEGER NOT NULL,
 original_name TEXT NOT NULL, display_name TEXT NOT NULL, multiplier REAL NOT NULL DEFAULT 1,
 asn TEXT NOT NULL DEFAULT '', asn_server TEXT NOT NULL DEFAULT '', exit_ip TEXT NOT NULL DEFAULT '', country_code TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '',
 config_json TEXT NOT NULL, config_revision TEXT NOT NULL DEFAULT '', added_at TEXT NOT NULL, modified_at TEXT NOT NULL, removed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_nodes_task_active ON nodes(task_id, removed_at);
CREATE UNIQUE INDEX idx_nodes_country_number ON nodes(country_code,number) WHERE country_code<>'' AND number>0;
CREATE TABLE country_sequences (country_code TEXT PRIMARY KEY, last_number INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS node_fields (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, field_name TEXT NOT NULL,
 value_json TEXT NOT NULL, added_at TEXT NOT NULL, modified_at TEXT NOT NULL, removed_at TEXT,
	 PRIMARY KEY(node_id, field_name, added_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_node_fields_current ON node_fields(node_id,field_name) WHERE removed_at IS NULL;
CREATE TABLE IF NOT EXISTS notices (
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, ordinal INTEGER NOT NULL, name TEXT NOT NULL,
 PRIMARY KEY(task_id, ordinal)
);
CREATE TABLE IF NOT EXISTS measurements (
 id INTEGER PRIMARY KEY AUTOINCREMENT, node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 kind TEXT NOT NULL, available INTEGER, latency_ms REAL, direct_latency_ms REAL,
 exit_ip TEXT NOT NULL DEFAULT '', exit_error TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', config_revision TEXT NOT NULL DEFAULT '', tested_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_measurements_node_time ON measurements(node_id, tested_at);
CREATE TABLE IF NOT EXISTS qualities (
 node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE, availability REAL NOT NULL,
 availability_raw REAL NOT NULL, samples INTEGER NOT NULL, unavailable_by_hour_json TEXT NOT NULL,
 average_latency_ms REAL NOT NULL, priority REAL NOT NULL, calculated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS measurement_hourly (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, hour TEXT NOT NULL, config_revision TEXT NOT NULL,
 available_count INTEGER NOT NULL, sample_count INTEGER NOT NULL, latency_sum REAL NOT NULL, latency_count INTEGER NOT NULL,
 direct_latency_sum REAL NOT NULL, direct_latency_count INTEGER NOT NULL, PRIMARY KEY(node_id,hour,config_revision)
);
CREATE INDEX IF NOT EXISTS idx_measurement_hourly_node_time ON measurement_hourly(node_id,hour);
CREATE TABLE IF NOT EXISTS quality_snapshots (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, calculated_at TEXT NOT NULL,
 availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,
 average_latency_ms REAL NOT NULL,priority REAL NOT NULL,
 PRIMARY KEY(node_id,calculated_at)
);
CREATE TABLE IF NOT EXISTS quality_daily (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, day TEXT NOT NULL,
 availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,
 average_latency_ms REAL NOT NULL,priority REAL NOT NULL,
 PRIMARY KEY(node_id,day)
);
CREATE TABLE IF NOT EXISTS quality_fields (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE, field_name TEXT NOT NULL,
 value_json TEXT NOT NULL, calculated_at TEXT NOT NULL, PRIMARY KEY(node_id,field_name)
);
CREATE TABLE IF NOT EXISTS identity_conflicts (
 id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 incoming_id TEXT NOT NULL, incoming_name TEXT NOT NULL, candidate_ids_json TEXT NOT NULL,
 reason TEXT NOT NULL, detected_at TEXT NOT NULL, resolved_at TEXT
);
CREATE TABLE IF NOT EXISTS artifacts (
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, kind TEXT NOT NULL, content BLOB NOT NULL,
 sha256 TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(task_id, kind)
);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
PRAGMA user_version=13;
`
	_, err := s.DB.Exec(schema)
	return err
}

// migrateLegacyToV2 performs the one deliberate renumbering allowed by v0.2.
// Known exits are numbered independently per country in stable added/id order;
// unknown exits remain zero until their first successful geo observation.
func (s *Store) migrateLegacyToV2() error {
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE nodes_v2 (id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, number INTEGER NOT NULL DEFAULT 0, protocol TEXT NOT NULL, server TEXT NOT NULL, port INTEGER NOT NULL, original_name TEXT NOT NULL, display_name TEXT NOT NULL, multiplier REAL NOT NULL DEFAULT 1, asn TEXT NOT NULL DEFAULT '', asn_server TEXT NOT NULL DEFAULT '', exit_ip TEXT NOT NULL DEFAULT '', country_code TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '', config_json TEXT NOT NULL, added_at TEXT NOT NULL, modified_at TEXT NOT NULL, removed_at TEXT)`,
		`INSERT INTO nodes_v2 SELECT id,task_id,CASE WHEN country_code='' THEN 0 ELSE ROW_NUMBER() OVER (PARTITION BY upper(country_code) ORDER BY added_at,id) END,protocol,server,port,original_name,display_name,multiplier,asn,'',exit_ip,upper(country_code),country,config_json,added_at,modified_at,removed_at FROM nodes`,
		`DROP TABLE nodes`, `ALTER TABLE nodes_v2 RENAME TO nodes`,
		`CREATE INDEX idx_nodes_task_active ON nodes(task_id,removed_at)`,
		`CREATE UNIQUE INDEX idx_nodes_country_number ON nodes(country_code,number) WHERE country_code<>'' AND number>0`,
		`CREATE TABLE country_sequences (country_code TEXT PRIMARY KEY,last_number INTEGER NOT NULL)`,
		`INSERT INTO country_sequences SELECT country_code,MAX(number) FROM nodes WHERE country_code<>'' GROUP BY country_code`,
		`ALTER TABLE qualities ADD COLUMN stability REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN subscription_upload INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN subscription_download INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN subscription_total INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE tasks ADD COLUMN subscription_expire TEXT`,
		`ALTER TABLE tasks ADD COLUMN subscription_info_at TEXT`,
		`ALTER TABLE measurements ADD COLUMN billed_bytes INTEGER NOT NULL DEFAULT 0`,
		`UPDATE measurements SET billed_bytes=CAST(ROUND(download_bytes*COALESCE((SELECT multiplier FROM nodes WHERE nodes.id=measurements.node_id),1)) AS INTEGER)`,
		`CREATE TABLE quality_fields (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,field_name TEXT NOT NULL,value_json TEXT NOT NULL,calculated_at TEXT NOT NULL,PRIMARY KEY(node_id,field_name))`,
		`CREATE TABLE identity_conflicts (id INTEGER PRIMARY KEY AUTOINCREMENT,task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,incoming_id TEXT NOT NULL,incoming_name TEXT NOT NULL,candidate_ids_json TEXT NOT NULL,reason TEXT NOT NULL,detected_at TEXT NOT NULL,resolved_at TEXT)`,
		`PRAGMA user_version=6`,
	}
	for _, stmt := range stmts {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v2: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) migrateV3ToV4() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{`ALTER TABLE tasks ADD COLUMN subscription_upload INTEGER NOT NULL DEFAULT 0`, `ALTER TABLE tasks ADD COLUMN subscription_download INTEGER NOT NULL DEFAULT 0`, `ALTER TABLE tasks ADD COLUMN subscription_total INTEGER NOT NULL DEFAULT 0`, `ALTER TABLE tasks ADD COLUMN subscription_expire TEXT`, `ALTER TABLE tasks ADD COLUMN subscription_info_at TEXT`, `PRAGMA user_version=4`} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v4: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) migrateV4ToV5() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE measurements ADD COLUMN billed_bytes INTEGER NOT NULL DEFAULT 0`,
		`UPDATE measurements SET billed_bytes=CAST(ROUND(download_bytes*COALESCE((SELECT multiplier FROM nodes WHERE nodes.id=measurements.node_id),1)) AS INTEGER)`,
		`PRAGMA user_version=5`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v5: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) migrateV5ToV6() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE nodes ADD COLUMN asn_server TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE quality_fields (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,field_name TEXT NOT NULL,value_json TEXT NOT NULL,calculated_at TEXT NOT NULL,PRIMARY KEY(node_id,field_name))`,
		`CREATE TABLE identity_conflicts (id INTEGER PRIMARY KEY AUTOINCREMENT,task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,incoming_id TEXT NOT NULL,incoming_name TEXT NOT NULL,candidate_ids_json TEXT NOT NULL,reason TEXT NOT NULL,detected_at TEXT NOT NULL,resolved_at TEXT)`,
		`PRAGMA user_version=6`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v6: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV6ToV7 removes the obsolete aggregate stability value. Speed and
// latency stability remain independent inputs to priority, while the Web UI
// presents speed stability as the single, user-facing stability metric.
func (s *Store) migrateV6ToV7() error {
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE qualities_v7 (node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,unavailable_by_hour_json TEXT NOT NULL,average_speed_bps REAL NOT NULL,speed_stability REAL NOT NULL,average_latency_ms REAL NOT NULL,latency_stability REAL NOT NULL,priority REAL NOT NULL,calculated_at TEXT NOT NULL)`,
		`INSERT INTO qualities_v7(node_id,availability,availability_raw,samples,unavailable_by_hour_json,average_speed_bps,speed_stability,average_latency_ms,latency_stability,priority,calculated_at) SELECT node_id,availability,availability_raw,samples,unavailable_by_hour_json,average_speed_bps,speed_stability,average_latency_ms,latency_stability,priority,calculated_at FROM qualities`,
		`DROP TABLE qualities`,
		`ALTER TABLE qualities_v7 RENAME TO qualities`,
		`DELETE FROM quality_fields WHERE field_name='stability'`,
		`PRAGMA user_version=7`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v7: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV7ToV8 changes latency from a cold HTTPS transaction to a calibrated
// steady-state measurement. Old latency rows and their derived values are not
// comparable, so discard them while preserving availability and speed data.
func (s *Store) migrateV7ToV8() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE measurements ADD COLUMN direct_latency_ms REAL`,
		`DELETE FROM measurements WHERE kind='latency'`,
		`UPDATE qualities SET average_latency_ms=0,latency_stability=0`,
		`DELETE FROM quality_fields WHERE field_name IN ('latency','latency_stability')`,
		`UPDATE tasks SET last_rules_at=NULL`,
		`PRAGMA user_version=8`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v8: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV8ToV9 replaces independent availability/latency schedules with one
// cycle observation and introduces bounded raw, hourly and quality history.
// The old probe series is not comparable with the 800 ms usability gate.
func (s *Store) migrateV8ToV9() error {
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`ALTER TABLE tasks ADD COLUMN last_detection_at TEXT`,
		`ALTER TABLE nodes ADD COLUMN config_revision TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS node_fields (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,field_name TEXT NOT NULL,value_json TEXT NOT NULL,added_at TEXT NOT NULL,modified_at TEXT NOT NULL,removed_at TEXT,PRIMARY KEY(node_id,field_name))`,
		`ALTER TABLE measurements ADD COLUMN exit_error TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE measurements ADD COLUMN config_revision TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE measurement_hourly (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,hour TEXT NOT NULL,config_revision TEXT NOT NULL,available_count INTEGER NOT NULL,sample_count INTEGER NOT NULL,latency_sum REAL NOT NULL,latency_count INTEGER NOT NULL,direct_latency_sum REAL NOT NULL,direct_latency_count INTEGER NOT NULL,speed_sum REAL NOT NULL,speed_count INTEGER NOT NULL,download_bytes INTEGER NOT NULL,billed_bytes INTEGER NOT NULL,PRIMARY KEY(node_id,hour,config_revision))`,
		`CREATE INDEX idx_measurement_hourly_node_time ON measurement_hourly(node_id,hour)`,
		`CREATE TABLE qualities_v9 (node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,unavailable_by_hour_json TEXT NOT NULL,average_speed_bps REAL NOT NULL,average_latency_ms REAL NOT NULL,priority REAL NOT NULL,download_priority REAL NOT NULL,latency_priority REAL NOT NULL,calculated_at TEXT NOT NULL)`,
		`DROP TABLE qualities`,
		`ALTER TABLE qualities_v9 RENAME TO qualities`,
		`CREATE TABLE quality_snapshots (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,calculated_at TEXT NOT NULL,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,average_speed_bps REAL NOT NULL,average_latency_ms REAL NOT NULL,priority REAL NOT NULL,download_priority REAL NOT NULL,latency_priority REAL NOT NULL,PRIMARY KEY(node_id,calculated_at))`,
		`CREATE TABLE quality_daily (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,day TEXT NOT NULL,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,average_speed_bps REAL NOT NULL,average_latency_ms REAL NOT NULL,priority REAL NOT NULL,download_priority REAL NOT NULL,latency_priority REAL NOT NULL,PRIMARY KEY(node_id,day))`,
		`DELETE FROM measurements`,
		`DELETE FROM quality_fields`,
		`DELETE FROM node_fields WHERE field_name NOT IN ('original_name','country','multiplier')`,
		`UPDATE tasks SET last_rules_at=NULL,last_detection_at=NULL`,
		`PRAGMA user_version=9`,
	}
	for _, stmt := range stmts {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v9: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV9ToV10 makes the three semantic node fields append-only history.
// Connection material remains current-only in nodes.config_json.
func (s *Store) migrateV9ToV10() error {
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE node_fields_v10 (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,field_name TEXT NOT NULL,value_json TEXT NOT NULL,added_at TEXT NOT NULL,modified_at TEXT NOT NULL,removed_at TEXT,PRIMARY KEY(node_id,field_name,added_at))`,
		`INSERT INTO node_fields_v10 SELECT node_id,field_name,value_json,added_at,modified_at,removed_at FROM node_fields WHERE field_name IN ('original_name','country','multiplier')`,
		`DROP TABLE node_fields`,
		`ALTER TABLE node_fields_v10 RENAME TO node_fields`,
		`CREATE UNIQUE INDEX idx_node_fields_current ON node_fields(node_id,field_name) WHERE removed_at IS NULL`,
		`PRAGMA user_version=10`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v10: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV10ToV11 remembers failed speed attempts after raw rows are rolled up,
// preventing an unsuccessful initial probe from bypassing the daily budget on
// every future cycle.
func (s *Store) migrateV10ToV11() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE measurement_hourly ADD COLUMN speed_attempt_count INTEGER NOT NULL DEFAULT 0`,
		`UPDATE measurement_hourly SET speed_attempt_count=speed_count`,
		`PRAGMA user_version=11`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v11: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV11ToV12 separates subscription attempts from successful refreshes
// so failed refreshes never overwrite the last successful refresh time.
func (s *Store) migrateV11ToV12() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`ALTER TABLE tasks ADD COLUMN subscription_attempt_at TEXT`,
		`ALTER TABLE tasks ADD COLUMN subscription_failed_since TEXT`,
		`PRAGMA user_version=12`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v12: %w", err)
		}
	}
	return tx.Commit()
}

// migrateV12ToV13 removes active speed probing and every persisted value
// derived from it. Availability/latency history is preserved.
func (s *Store) migrateV12ToV13() error {
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY,value TEXT NOT NULL)`,
		`CREATE TABLE measurements_v13 (id INTEGER PRIMARY KEY AUTOINCREMENT,node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,kind TEXT NOT NULL,available INTEGER,latency_ms REAL,direct_latency_ms REAL,exit_ip TEXT NOT NULL DEFAULT '',exit_error TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',config_revision TEXT NOT NULL DEFAULT '',tested_at TEXT NOT NULL)`,
		`INSERT INTO measurements_v13(id,node_id,kind,available,latency_ms,direct_latency_ms,exit_ip,exit_error,error,config_revision,tested_at) SELECT id,node_id,kind,available,latency_ms,direct_latency_ms,exit_ip,exit_error,error,config_revision,tested_at FROM measurements WHERE kind<>'speed'`,
		`DROP TABLE measurements`, `ALTER TABLE measurements_v13 RENAME TO measurements`,
		`CREATE INDEX idx_measurements_node_time ON measurements(node_id,tested_at)`,
		`CREATE TABLE measurement_hourly_v13 (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,hour TEXT NOT NULL,config_revision TEXT NOT NULL,available_count INTEGER NOT NULL,sample_count INTEGER NOT NULL,latency_sum REAL NOT NULL,latency_count INTEGER NOT NULL,direct_latency_sum REAL NOT NULL,direct_latency_count INTEGER NOT NULL,PRIMARY KEY(node_id,hour,config_revision))`,
		`INSERT INTO measurement_hourly_v13 SELECT node_id,hour,config_revision,available_count,sample_count,latency_sum,latency_count,direct_latency_sum,direct_latency_count FROM measurement_hourly`,
		`DROP TABLE measurement_hourly`, `ALTER TABLE measurement_hourly_v13 RENAME TO measurement_hourly`,
		`CREATE INDEX idx_measurement_hourly_node_time ON measurement_hourly(node_id,hour)`,
		`CREATE TABLE qualities_v13 (node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,unavailable_by_hour_json TEXT NOT NULL,average_latency_ms REAL NOT NULL,priority REAL NOT NULL,calculated_at TEXT NOT NULL)`,
		// The former priority included speed. Keep its source availability and
		// latency values, but force a startup recalculation before generation.
		`INSERT INTO qualities_v13 SELECT node_id,availability,availability_raw,samples,unavailable_by_hour_json,average_latency_ms,0,calculated_at FROM qualities`,
		`DROP TABLE qualities`, `ALTER TABLE qualities_v13 RENAME TO qualities`,
		`CREATE TABLE quality_snapshots_v13 (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,calculated_at TEXT NOT NULL,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,average_latency_ms REAL NOT NULL,priority REAL NOT NULL,PRIMARY KEY(node_id,calculated_at))`,
		`DROP TABLE quality_snapshots`, `ALTER TABLE quality_snapshots_v13 RENAME TO quality_snapshots`,
		`CREATE TABLE quality_daily_v13 (node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,day TEXT NOT NULL,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,average_latency_ms REAL NOT NULL,priority REAL NOT NULL,PRIMARY KEY(node_id,day))`,
		`DROP TABLE quality_daily`, `ALTER TABLE quality_daily_v13 RENAME TO quality_daily`,
		`DELETE FROM quality_fields WHERE field_name IN ('speed','average_speed_bps','speed_stability','download_priority','latency_priority','priority')`,
		`PRAGMA user_version=13`,
	}
	for _, stmt := range stmts {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v13: %w", err)
		}
	}
	type settingValue struct{ key, value string }
	rows, err := tx.Query(`SELECT key,value FROM settings WHERE key='schedule' OR key LIKE 'task_settings:%'`)
	if err != nil {
		return fmt.Errorf("read settings during schema v13 migration: %w", err)
	}
	var settings []settingValue
	for rows.Next() {
		var item settingValue
		if err = rows.Scan(&item.key, &item.value); err != nil {
			rows.Close()
			return err
		}
		settings = append(settings, item)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, item := range settings {
		var value map[string]any
		if json.Unmarshal([]byte(item.value), &value) != nil {
			continue
		}
		delete(value, "daily_budget_mib")
		delete(value, "speed_sample_mib")
		raw, _ := json.Marshal(value)
		if _, err = tx.Exec(`UPDATE settings SET value=? WHERE key=?`, string(raw), item.key); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Rebuilding the speed-bearing tables leaves their pages on SQLite's
	// freelist. Compact once during this destructive migration so removed
	// throughput samples do not continue occupying persistent router storage.
	if _, err = s.DB.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("compact schema v13: %w", err)
	}
	return nil
}

func (s *Store) migrateV2ToV3() error {
	if _, err := s.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer s.DB.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`CREATE TABLE nodes_v3 (id TEXT PRIMARY KEY,task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,number INTEGER NOT NULL DEFAULT 0,protocol TEXT NOT NULL,server TEXT NOT NULL,port INTEGER NOT NULL,original_name TEXT NOT NULL,display_name TEXT NOT NULL,multiplier REAL NOT NULL DEFAULT 1,asn TEXT NOT NULL DEFAULT '',exit_ip TEXT NOT NULL DEFAULT '',country_code TEXT NOT NULL DEFAULT '',country TEXT NOT NULL DEFAULT '',config_json TEXT NOT NULL,added_at TEXT NOT NULL,modified_at TEXT NOT NULL,removed_at TEXT)`,
		`INSERT INTO nodes_v3 SELECT * FROM nodes`, `DROP TABLE nodes`, `ALTER TABLE nodes_v3 RENAME TO nodes`, `CREATE INDEX idx_nodes_task_active ON nodes(task_id,removed_at)`, `CREATE UNIQUE INDEX idx_nodes_country_number ON nodes(country_code,number) WHERE country_code<>'' AND number>0`, `PRAGMA user_version=3`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v3: %w", err)
		}
	}
	return tx.Commit()
}

func nowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func parseTime(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, v.String)
	return t
}
func nullTime(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t := parseTime(v)
	return &t
}

func (s *Store) PutSetting(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v, err
}
func (s *Store) BumpCounter(ctx context.Context, key string) (int64, error) {
	var value int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO settings(key,value) VALUES(?, '1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1 RETURNING CAST(value AS INTEGER)`, key).Scan(&value)
	return value, err
}
func (s *Store) SetTaskError(ctx context.Context, id, message string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET last_error=?,updated_at=? WHERE id=?`, message, nowText(), id)
	return err
}

func (s *Store) CreateTask(ctx context.Context, t *model.Task) error {
	if t.ID == "" || t.SubscriptionURL == "" || t.PublishToken == "" {
		return errors.New("id, subscription URL and publish token are required")
	}
	now := nowText()
	if t.SubscriptionUA == "" {
		t.SubscriptionUA = "clash.meta"
	}
	if strings.TrimSpace(t.Name) != "" {
		t.Name = s.UniqueTaskName(ctx, t.Name, t.ID)
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, now)
	t.UpdatedAt = t.CreatedAt
	t.Enabled = true
	_, err := s.DB.ExecContext(ctx, `INSERT INTO tasks(id,name,subscription_url,subscription_ua,publish_token,enabled,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, t.ID, t.Name, t.SubscriptionURL, t.SubscriptionUA, t.PublishToken, now, now)
	return err
}

func (s *Store) UpdateTask(ctx context.Context, t model.Task) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET name=?,subscription_url=?,subscription_ua=?,enabled=?,updated_at=? WHERE id=?`, t.Name, t.SubscriptionURL, t.SubscriptionUA, t.Enabled, nowText(), t.ID)
	return err
}
func (s *Store) UpdateTaskName(ctx context.Context, id, name string) error {
	name = s.UniqueTaskName(ctx, name, id)
	_, e := s.DB.ExecContext(ctx, `UPDATE tasks SET name=?,updated_at=? WHERE id=?`, name, nowText(), id)
	return e
}

func (s *Store) UniqueTaskName(ctx context.Context, desired, excludeID string) string {
	desired = strings.TrimSpace(desired)
	if desired == "" {
		desired = "未命名订阅"
	}
	for suffix := 1; ; suffix++ {
		candidate := desired
		if suffix > 1 {
			candidate = fmt.Sprintf("%s (%d)", desired, suffix)
		}
		var count int
		_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE lower(name)=lower(?) AND id<>?`, candidate, excludeID).Scan(&count)
		if count == 0 {
			return candidate
		}
	}
}
func (s *Store) SetTaskEnabled(ctx context.Context, id string, enabled bool) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE tasks SET enabled=?,updated_at=? WHERE id=?`, enabled, nowText(), id)
	return e
}
func (s *Store) RotatePublishToken(ctx context.Context, id, token string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET publish_token=?,updated_at=? WHERE id=?`, token, nowText(), id)
	return err
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN (?,?)`, "task_settings:"+id, "availability_cycle:"+id); err != nil {
		return err
	}
	return tx.Commit()
}

func scanTask(scanner interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var enabled int
	var created, updated string
	var ls, attempt, failedSince, lr, lf, lc, ld, expire, infoAt sql.NullString
	err := scanner.Scan(&t.ID, &t.Name, &t.SubscriptionURL, &t.SubscriptionUA, &t.PublishToken, &enabled, &ls, &attempt, &failedSince, &lr, &lf, &lc, &ld, &t.LastError, &t.SubscriptionUpload, &t.SubscriptionDownload, &t.SubscriptionTotal, &expire, &infoAt, &created, &updated)
	if err != nil {
		return t, err
	}
	t.Enabled = enabled != 0
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	t.LastSubscriptionAt = parseTime(ls)
	t.SubscriptionAttemptAt = parseTime(attempt)
	t.SubscriptionFailedSince = parseTime(failedSince)
	t.LastRulesAt = parseTime(lr)
	t.LastSFAAt = parseTime(lf)
	t.LastCartonAt = parseTime(lc)
	t.LastDetectionAt = parseTime(ld)
	t.SubscriptionExpire = parseTime(expire)
	t.SubscriptionInfoAt = parseTime(infoAt)
	return t, nil
}

const taskColumns = `id,name,subscription_url,subscription_ua,publish_token,enabled,last_subscription_at,subscription_attempt_at,subscription_failed_since,last_rules_at,last_sfa_at,last_carton_at,last_detection_at,last_error,subscription_upload,subscription_download,subscription_total,subscription_expire,subscription_info_at,created_at,updated_at`

func (s *Store) UpdateSubscriptionInfo(ctx context.Context, id string, m subscription.Metadata) error {
	var expire any
	if !m.Expire.IsZero() {
		expire = m.Expire.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET subscription_upload=?,subscription_download=?,subscription_total=?,subscription_expire=?,subscription_info_at=?,updated_at=? WHERE id=?`, m.Upload, m.Download, m.Total, expire, m.CollectedAt.UTC().Format(time.RFC3339Nano), nowText(), id)
	return err
}

func (s *Store) Task(ctx context.Context, id string) (model.Task, error) {
	return scanTask(s.DB.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=?`, id))
}
func (s *Store) TaskByToken(ctx context.Context, token string) (model.Task, error) {
	// Pausing a task stops its scheduled probes and updates, but the last known-good
	// subscription and rule sets must remain available to already configured clients.
	return scanTask(s.DB.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE publish_token=?`, token))
}
func (s *Store) Tasks(ctx context.Context) ([]model.Task, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks ORDER BY created_at`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetTaskRun(ctx context.Context, id, kind, runErr string) error {
	cols := map[string]string{"subscription": "last_subscription_at", "rules": "last_rules_at", "sfa": "last_sfa_at", "carton": "last_carton_at", "detection": "last_detection_at"}
	col, ok := cols[kind]
	if !ok {
		return fmt.Errorf("invalid run kind %q", kind)
	}
	if kind == "subscription" && runErr == "" {
		_, e := s.DB.ExecContext(ctx, `UPDATE tasks SET last_subscription_at=?,subscription_attempt_at=?,subscription_failed_since=NULL,last_error='',updated_at=? WHERE id=?`, nowText(), nowText(), nowText(), id)
		return e
	}
	_, e := s.DB.ExecContext(ctx, `UPDATE tasks SET `+col+`=?,last_error=?,updated_at=? WHERE id=?`, nowText(), runErr, nowText(), id)
	return e
}

func (s *Store) MarkSubscriptionAttempt(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET subscription_attempt_at=?,updated_at=? WHERE id=?`, nowText(), nowText(), id)
	return err
}

func (s *Store) MarkSubscriptionFailure(ctx context.Context, id, message string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET subscription_attempt_at=?,subscription_failed_since=COALESCE(subscription_failed_since,?),last_error=?,updated_at=? WHERE id=?`, nowText(), nowText(), message, nowText(), id)
	return err
}
func (s *Store) SetTaskRunKeepError(ctx context.Context, id, kind string) error {
	cols := map[string]string{"subscription": "last_subscription_at", "rules": "last_rules_at", "sfa": "last_sfa_at", "carton": "last_carton_at", "detection": "last_detection_at"}
	if strings.HasPrefix(kind, "sfa-") {
		kind = "sfa"
	}
	if strings.HasPrefix(kind, "carton-") {
		kind = "carton"
	}
	col, ok := cols[kind]
	if !ok {
		return fmt.Errorf("invalid run kind %q", kind)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE tasks SET `+col+`=?,updated_at=? WHERE id=?`, nowText(), nowText(), id)
	return err
}

func (s *Store) UpsertNodes(ctx context.Context, taskID string, nodes []model.Node, notices []model.Notice) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := nowText()
	type identityRow struct {
		id, protocol, name, continuity string
		port                           int
	}
	var identities []identityRow
	rows, e := tx.QueryContext(ctx, `SELECT id,protocol,port,original_name,config_json FROM nodes WHERE task_id=? AND removed_at IS NULL`, taskID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var row identityRow
		var raw string
		if e = rows.Scan(&row.id, &row.protocol, &row.port, &row.name, &raw); e != nil {
			rows.Close()
			return e
		}
		var config map[string]any
		_ = json.Unmarshal([]byte(raw), &config)
		row.continuity = nodeContinuity(row.protocol, row.port, config)
		identities = append(identities, row)
	}
	rows.Close()
	continuityIDs, continuityNames, continuityCounts := map[string]string{}, map[string]string{}, map[string]int{}
	continuityCandidates, nameCandidates := map[string][]string{}, map[string][]string{}
	nameIDs, nameCounts := map[string]string{}, map[string]int{}
	for _, row := range identities {
		continuityCounts[row.continuity]++
		continuityIDs[row.continuity] = row.id
		continuityCandidates[row.continuity] = append(continuityCandidates[row.continuity], row.id)
		continuityNames[row.continuity] = strings.ToLower(subscription.SanitizeName(row.name))
		nameKey := row.protocol + "\x00" + strings.ToLower(subscription.SanitizeName(row.name))
		nameCounts[nameKey]++
		nameIDs[nameKey] = row.id
		nameCandidates[nameKey] = append(nameCandidates[nameKey], row.id)
	}
	// Keep the newest removed record for an unambiguous protocol/name. This is
	// the resurrection path when an older parser used an endpoint-derived ID
	// and the endpoint changed while the node was absent.
	historicalNameIDs := map[string]string{}
	rows, e = tx.QueryContext(ctx, `SELECT id,protocol,original_name FROM nodes WHERE task_id=? AND removed_at IS NOT NULL ORDER BY modified_at DESC,id`, taskID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, protocol, name string
		if e = rows.Scan(&id, &protocol, &name); e != nil {
			rows.Close()
			return e
		}
		key := protocol + "\x00" + strings.ToLower(subscription.SanitizeName(name))
		if _, exists := historicalNameIDs[key]; !exists {
			historicalNameIDs[key] = id
		}
	}
	rows.Close()
	incomingNameCounts := map[string]int{}
	for _, node := range nodes {
		nameKey := node.Protocol + "\x00" + strings.ToLower(subscription.SanitizeName(node.OriginalName))
		incomingNameCounts[nameKey]++
	}
	seen := map[string]bool{}
	for i := range nodes {
		n := &nodes[i]
		n.ConfigRevision = configRevision(n.Config)
		raw, _ := json.Marshal(n.Config)
		var existingID string
		e = tx.QueryRowContext(ctx, `SELECT id FROM nodes WHERE task_id=? AND id=?`, taskID, n.ID).Scan(&existingID)
		if errors.Is(e, sql.ErrNoRows) {
			continuity := nodeContinuity(n.Protocol, n.Port, n.Config)
			incomingName := strings.ToLower(subscription.SanitizeName(n.OriginalName))
			if continuityCounts[continuity] == 1 && continuityNames[continuity] == incomingName {
				existingID, e = continuityIDs[continuity], nil
			} else {
				nameKey := n.Protocol + "\x00" + strings.ToLower(subscription.SanitizeName(n.OriginalName))
				if nameCounts[nameKey] == 1 {
					existingID, e = nameIDs[nameKey], nil
				} else if incomingNameCounts[nameKey] == 1 && historicalNameIDs[nameKey] != "" && !seen[historicalNameIDs[nameKey]] {
					existingID, e = historicalNameIDs[nameKey], nil
				}
			}
			if existingID == "" {
				var candidates []string
				reason := ""
				nameKey := n.Protocol + "\x00" + incomingName
				if continuityCounts[continuity] > 1 {
					candidates, reason = append([]string{}, continuityCandidates[continuity]...), "ambiguous continuity key"
				} else if nameCounts[nameKey] > 1 {
					candidates, reason = append([]string{}, nameCandidates[nameKey]...), "ambiguous protocol and source name"
				}
				if reason != "" {
					filtered := candidates[:0]
					seenCandidate := map[string]bool{n.ID: true}
					for _, candidate := range candidates {
						if candidate != "" && !seenCandidate[candidate] {
							filtered = append(filtered, candidate)
							seenCandidate[candidate] = true
						}
					}
					candidates = filtered
					candidateJSON, _ := json.Marshal(candidates)
					if _, conflictErr := tx.ExecContext(ctx, `INSERT INTO identity_conflicts(task_id,incoming_id,incoming_name,candidate_ids_json,reason,detected_at) SELECT ?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM identity_conflicts WHERE task_id=? AND incoming_id=? AND resolved_at IS NULL)`, taskID, n.ID, n.OriginalName, string(candidateJSON), reason, now, taskID, n.ID); conflictErr != nil {
						return conflictErr
					}
				}
			}
		}
		if e == nil && existingID != "" {
			n.ID = existingID
			seen[existingID] = true
			// Subscription refreshes connection/name/cost fields only. Preserve
			// observed exit metadata and the permanent per-country number.
			var oldServer string
			e = tx.QueryRowContext(ctx, `SELECT number,asn,asn_server,exit_ip,country_code,country,server FROM nodes WHERE id=?`, n.ID).Scan(&n.Number, &n.ASN, &n.ASNServer, &n.ExitIP, &n.CountryCode, &n.Country, &oldServer)
			if e != nil {
				return e
			}
			if oldServer != n.Server {
				n.ASN, n.ASNServer = "", ""
			}
			n.DisplayName = naming.DisplayName(n.CountryCode, n.Country, n.Number, n.Multiplier)
			_, e = tx.ExecContext(ctx, `UPDATE nodes SET protocol=?,server=?,port=?,original_name=?,display_name=?,multiplier=?,asn=?,asn_server=?,config_json=?,config_revision=?,modified_at=CASE WHEN protocol<>? OR server<>? OR port<>? OR original_name<>? OR display_name<>? OR multiplier<>? OR config_json<>? OR removed_at IS NOT NULL THEN ? ELSE modified_at END,removed_at=NULL WHERE id=?`, n.Protocol, n.Server, n.Port, n.OriginalName, n.DisplayName, n.Multiplier, n.ASN, n.ASNServer, string(raw), n.ConfigRevision, n.Protocol, n.Server, n.Port, n.OriginalName, n.DisplayName, n.Multiplier, string(raw), now, n.ID)
		} else if errors.Is(e, sql.ErrNoRows) {
			n.Number = 0 // a number is allocated only after the actual exit country is known
			_, e = tx.ExecContext(ctx, `INSERT INTO nodes(id,task_id,number,protocol,server,port,original_name,display_name,multiplier,config_json,config_revision,added_at,modified_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, n.ID, taskID, n.Number, n.Protocol, n.Server, n.Port, n.OriginalName, n.DisplayName, n.Multiplier, string(raw), n.ConfigRevision, now, now)
			seen[n.ID] = true
		}
		if e != nil {
			return e
		}
		if e = upsertFields(ctx, tx, *n, now); e != nil {
			return e
		}
	}
	rows, e = tx.QueryContext(ctx, `SELECT id FROM nodes WHERE task_id=? AND removed_at IS NULL`, taskID)
	if e != nil {
		return e
	}
	var removed []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		if !seen[id] {
			removed = append(removed, id)
		}
	}
	rows.Close()
	for _, id := range removed {
		if _, e = tx.ExecContext(ctx, `UPDATE nodes SET removed_at=?,modified_at=? WHERE id=?`, now, now, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE node_fields SET removed_at=? WHERE node_id=? AND removed_at IS NULL`, now, id); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM notices WHERE task_id=?`, taskID); e != nil {
		return e
	}
	for i, n := range notices {
		if _, e = tx.ExecContext(ctx, `INSERT INTO notices(task_id,ordinal,name) VALUES(?,?,?)`, taskID, i, n.Name); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// MergeRotatedNodeHistory repairs data created by older endpoint-based IDs.
// A merge is allowed only when a server-independent continuity key has exactly
// one active node and every historical member has the same normalized source
// name and protocol. Ambiguous groups are deliberately left untouched.
func (s *Store) MergeRotatedNodeHistory(ctx context.Context, taskID string) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type candidate struct {
		id, protocol, name, continuity string
		port                           int
		active                         bool
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,protocol,port,original_name,config_json,removed_at FROM nodes WHERE task_id=?`, taskID)
	if err != nil {
		return 0, err
	}
	groups := map[string][]candidate{}
	for rows.Next() {
		var item candidate
		var raw string
		var removed sql.NullString
		if err = rows.Scan(&item.id, &item.protocol, &item.port, &item.name, &raw, &removed); err != nil {
			rows.Close()
			return 0, err
		}
		var config map[string]any
		_ = json.Unmarshal([]byte(raw), &config)
		item.continuity = nodeContinuity(item.protocol, item.port, config)
		item.active = !removed.Valid
		groups[item.continuity] = append(groups[item.continuity], item)
	}
	rows.Close()
	var moved int64
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		activeID, activeCount := "", 0
		name := strings.ToLower(subscription.SanitizeName(group[0].name))
		protocol := group[0].protocol
		unambiguous := name != ""
		for _, item := range group {
			if item.protocol != protocol || strings.ToLower(subscription.SanitizeName(item.name)) != name {
				unambiguous = false
			}
			if item.active {
				activeID, activeCount = item.id, activeCount+1
			}
		}
		if !unambiguous || activeCount != 1 {
			continue
		}
		for _, item := range group {
			if item.id == activeID {
				continue
			}
			if updateErr := mergeArchivedSeries(ctx, tx, activeID, item.id); updateErr != nil {
				return moved, updateErr
			}
			result, updateErr := tx.ExecContext(ctx, `UPDATE measurements SET node_id=? WHERE node_id=?`, activeID, item.id)
			if updateErr != nil {
				return moved, updateErr
			}
			count, _ := result.RowsAffected()
			moved += count
			if count > 0 {
				if _, updateErr = tx.ExecContext(ctx, `DELETE FROM qualities WHERE node_id IN (?,?)`, activeID, item.id); updateErr != nil {
					return moved, updateErr
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return moved, nil
}

func (s *Store) IdentityConflicts(ctx context.Context, taskID string) ([]model.IdentityConflict, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,task_id,incoming_id,incoming_name,candidate_ids_json,reason,detected_at,resolved_at FROM identity_conflicts WHERE task_id=? ORDER BY resolved_at IS NOT NULL,detected_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]model.IdentityConflict, 0)
	for rows.Next() {
		var item model.IdentityConflict
		var candidates, detected string
		var resolved sql.NullString
		if err = rows.Scan(&item.ID, &item.TaskID, &item.IncomingID, &item.IncomingName, &candidates, &item.Reason, &detected, &resolved); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(candidates), &item.CandidateIDs)
		item.DetectedAt, _ = time.Parse(time.RFC3339Nano, detected)
		item.ResolvedAt = nullTime(resolved)
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	loadCandidate := func(nodeID string) (model.IdentityCandidate, error) {
		var candidate model.IdentityCandidate
		var added, modified string
		var removed sql.NullString
		err := s.DB.QueryRowContext(ctx, `SELECT id,number,protocol,display_name,original_name,server,port,multiplier,asn,exit_ip,country_code,country,added_at,modified_at,removed_at FROM nodes WHERE task_id=? AND id=?`, taskID, nodeID).Scan(
			&candidate.ID, &candidate.Number, &candidate.Protocol, &candidate.DisplayName, &candidate.OriginalName,
			&candidate.Server, &candidate.Port, &candidate.Multiplier, &candidate.ASN, &candidate.ExitIP,
			&candidate.CountryCode, &candidate.Country, &added, &modified, &removed,
		)
		if err != nil {
			return candidate, err
		}
		candidate.AddedAt, _ = time.Parse(time.RFC3339Nano, added)
		candidate.ModifiedAt, _ = time.Parse(time.RFC3339Nano, modified)
		candidate.RemovedAt = nullTime(removed)
		candidate.Removed = removed.Valid
		return candidate, nil
	}
	for i := range out {
		incoming, queryErr := loadCandidate(out[i].IncomingID)
		if queryErr == nil {
			out[i].Incoming = &incoming
		} else if !errors.Is(queryErr, sql.ErrNoRows) {
			return nil, queryErr
		}
		for _, candidateID := range out[i].CandidateIDs {
			candidate, candidateErr := loadCandidate(candidateID)
			if candidateErr == nil {
				out[i].Candidates = append(out[i].Candidates, candidate)
			} else if !errors.Is(candidateErr, sql.ErrNoRows) {
				return nil, candidateErr
			}
		}
	}
	return out, nil
}

func (s *Store) ResolveIdentityConflict(ctx context.Context, taskID string, conflictID int64) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE identity_conflicts SET resolved_at=? WHERE id=? AND task_id=? AND resolved_at IS NULL`, nowText(), conflictID, taskID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return errors.New("identity conflict not found or already resolved")
	}
	return nil
}

// ResolveInitialBatchIdentityConflicts removes false alerts emitted by v0.2.5
// when several distinct nodes in the same first subscription shared credentials.
// If the incoming record and every candidate were created in the exact batch
// that emitted the alert, there is no older history to inherit.
func (s *Store) ResolveInitialBatchIdentityConflicts(ctx context.Context, taskID string) (int64, error) {
	conflicts, err := s.IdentityConflicts(ctx, taskID)
	if err != nil {
		return 0, err
	}
	var resolved int64
	for _, conflict := range conflicts {
		if conflict.ResolvedAt != nil || conflict.Reason != "ambiguous continuity key" {
			continue
		}
		detected := conflict.DetectedAt.UTC().Format(time.RFC3339Nano)
		ids := append([]string{conflict.IncomingID}, conflict.CandidateIDs...)
		initialBatch := len(ids) > 1
		for _, id := range ids {
			var added string
			if err = s.DB.QueryRowContext(ctx, `SELECT added_at FROM nodes WHERE task_id=? AND id=?`, taskID, id).Scan(&added); err != nil || added != detected {
				initialBatch = false
				break
			}
		}
		if initialBatch {
			if err = s.ResolveIdentityConflict(ctx, taskID, conflict.ID); err != nil {
				return resolved, err
			}
			resolved++
		}
	}
	return resolved, nil
}

// MergeNodes moves historical measurements to the selected permanent node ID.
// Both node records and their field history remain retained for auditability.
func (s *Store) MergeNodes(ctx context.Context, taskID, targetID, sourceID string) error {
	if targetID == "" || sourceID == "" || targetID == sourceID {
		return errors.New("distinct target_id and source_id are required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE task_id=? AND id IN (?,?)`, taskID, targetID, sourceID).Scan(&count); err != nil {
		return err
	}
	if count != 2 {
		return errors.New("both nodes must belong to the task")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE measurements SET node_id=? WHERE node_id=?`, targetID, sourceID); err != nil {
		return err
	}
	if err = mergeArchivedSeries(ctx, tx, targetID, sourceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM qualities WHERE node_id IN (?,?)`, targetID, sourceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM quality_fields WHERE node_id IN (?,?)`, targetID, sourceID); err != nil {
		return err
	}
	now := nowText()
	if _, err = tx.ExecContext(ctx, `UPDATE nodes SET removed_at=COALESCE(removed_at,?),modified_at=? WHERE id=?`, now, now, sourceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE node_fields SET removed_at=COALESCE(removed_at,?) WHERE node_id=?`, now, sourceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_conflicts SET resolved_at=? WHERE task_id=? AND resolved_at IS NULL AND (incoming_id=? OR candidate_ids_json LIKE ? OR candidate_ids_json LIKE ?)`, now, taskID, sourceID, "%\""+sourceID+"\"%", "%\""+targetID+"\"%"); err != nil {
		return err
	}
	return tx.Commit()
}

// mergeArchivedSeries moves compacted observations before a historical node is
// merged. Hourly collisions are combined; quality collisions keep the already
// selected target node's snapshot.
func mergeArchivedSeries(ctx context.Context, tx *sql.Tx, targetID, sourceID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO measurement_hourly(node_id,hour,config_revision,available_count,sample_count,latency_sum,latency_count,direct_latency_sum,direct_latency_count)
		SELECT ?,hour,config_revision,available_count,sample_count,latency_sum,latency_count,direct_latency_sum,direct_latency_count FROM measurement_hourly WHERE node_id=?
		ON CONFLICT(node_id,hour,config_revision) DO UPDATE SET available_count=available_count+excluded.available_count,sample_count=sample_count+excluded.sample_count,latency_sum=latency_sum+excluded.latency_sum,latency_count=latency_count+excluded.latency_count,direct_latency_sum=direct_latency_sum+excluded.direct_latency_sum,direct_latency_count=direct_latency_count+excluded.direct_latency_count`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM measurement_hourly WHERE node_id=?`, sourceID); err != nil {
		return err
	}
	for _, table := range []string{"quality_snapshots", "quality_daily"} {
		columns := "calculated_at,availability,availability_raw,samples,average_latency_ms,priority"
		if table == "quality_daily" {
			columns = "day,availability,availability_raw,samples,average_latency_ms,priority"
		}
		query := fmt.Sprintf(`INSERT OR IGNORE INTO %s(node_id,%s) SELECT ?,%s FROM %s WHERE node_id=?`, table, columns, columns, table)
		if _, err := tx.ExecContext(ctx, query, targetID, sourceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE node_id=?`, sourceID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) QualityCoverageComplete(ctx context.Context, taskID string) (bool, error) {
	var nodes, qualities int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE task_id=? AND removed_at IS NULL`, taskID).Scan(&nodes); err != nil {
		return false, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM qualities q JOIN nodes n ON n.id=q.node_id WHERE n.task_id=? AND n.removed_at IS NULL`, taskID).Scan(&qualities); err != nil {
		return false, err
	}
	return nodes == qualities, nil
}

func nodeContinuity(protocol string, port int, config map[string]any) string {
	copyConfig := make(map[string]any, len(config)+2)
	for key, value := range config {
		copyConfig[key] = value
	}
	if strings.TrimSpace(fmt.Sprint(copyConfig["type"])) == "" || fmt.Sprint(copyConfig["type"]) == "<nil>" {
		copyConfig["type"] = protocol
	}
	if value, ok := copyConfig["port"]; !ok || fmt.Sprint(value) == "0" || fmt.Sprint(value) == "<nil>" {
		copyConfig["port"] = port
	}
	return subscription.ContinuityKey(copyConfig)
}

func configRevision(config map[string]any) string {
	copyConfig := make(map[string]any, len(config))
	for key, value := range config {
		if key != "name" {
			copyConfig[key] = value
		}
	}
	raw, _ := json.Marshal(copyConfig)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

func upsertFields(ctx context.Context, tx *sql.Tx, n model.Node, now string) error {
	fields := map[string]any{"original_name": n.OriginalName, "multiplier": n.Multiplier, "country": map[string]string{"code": n.CountryCode, "name": n.Country}}
	for name, value := range fields {
		if err := putHistoricalField(ctx, tx, n.ID, name, value, now); err != nil {
			return err
		}
	}
	return nil
}

func putHistoricalField(ctx context.Context, tx *sql.Tx, nodeID, name string, value any, now string) error {
	raw, _ := json.Marshal(value)
	var old string
	err := tx.QueryRowContext(ctx, `SELECT value_json FROM node_fields WHERE node_id=? AND field_name=? AND removed_at IS NULL`, nodeID, name).Scan(&old)
	if err == nil && old == string(raw) {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE node_fields SET removed_at=? WHERE node_id=? AND field_name=? AND removed_at IS NULL`, now, nodeID, name); err != nil {
			return err
		}
	}
	addedAt := now
	for {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_fields WHERE node_id=? AND field_name=? AND added_at=?`, nodeID, name, addedAt).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			break
		}
		at, parseErr := time.Parse(time.RFC3339Nano, addedAt)
		if parseErr != nil {
			return parseErr
		}
		addedAt = at.Add(time.Nanosecond).UTC().Format(time.RFC3339Nano)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO node_fields(node_id,field_name,value_json,added_at,modified_at,removed_at) VALUES(?,?,?,?,?,NULL)`, nodeID, name, string(raw), addedAt, now)
	return err
}

func scanNode(scanner interface{ Scan(...any) error }) (model.Node, error) {
	var n model.Node
	var raw, added, modified string
	var removed sql.NullString
	e := scanner.Scan(&n.ID, &n.TaskID, &n.Number, &n.Protocol, &n.Server, &n.Port, &n.OriginalName, &n.DisplayName, &n.Multiplier, &n.ASN, &n.ASNServer, &n.ExitIP, &n.CountryCode, &n.Country, &raw, &n.ConfigRevision, &added, &modified, &removed)
	if e != nil {
		return n, e
	}
	json.Unmarshal([]byte(raw), &n.Config)
	n.AddedAt, _ = time.Parse(time.RFC3339Nano, added)
	n.ModifiedAt, _ = time.Parse(time.RFC3339Nano, modified)
	n.RemovedAt = nullTime(removed)
	return n, nil
}

const nodeColumns = `id,task_id,number,protocol,server,port,original_name,display_name,multiplier,asn,asn_server,exit_ip,country_code,country,config_json,config_revision,added_at,modified_at,removed_at`

func (s *Store) Nodes(ctx context.Context, taskID string, includeRemoved bool) ([]model.Node, error) {
	q := `SELECT ` + nodeColumns + ` FROM nodes WHERE task_id=?`
	if !includeRemoved {
		q += ` AND removed_at IS NULL`
	}
	q += ` ORDER BY country_code,number,added_at`
	rows, e := s.DB.QueryContext(ctx, q, taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		n, e := scanNode(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (s *Store) Notices(ctx context.Context, taskID string) ([]model.Notice, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT name FROM notices WHERE task_id=? ORDER BY ordinal`, taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Notice
	for rows.Next() {
		var n model.Notice
		if e = rows.Scan(&n.Name); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) UpdateNodeGeo(ctx context.Context, id, asn, asnServer, exitIP, countryCode, country, displayName string) error {
	code := strings.ToUpper(strings.TrimSpace(countryCode))
	if code == "" {
		return errors.New("cannot number node with unknown exit country")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var oldCode string
	var number int64
	var multiplier float64
	if e = tx.QueryRowContext(ctx, `SELECT country_code,number,multiplier FROM nodes WHERE id=?`, id).Scan(&oldCode, &number, &multiplier); e != nil {
		return e
	}
	if oldCode != code || number <= 0 {
		if _, e = tx.ExecContext(ctx, `INSERT INTO country_sequences(country_code,last_number) VALUES(?,1) ON CONFLICT(country_code) DO UPDATE SET last_number=last_number+1`, code); e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT last_number FROM country_sequences WHERE country_code=?`, code).Scan(&number); e != nil {
			return e
		}
	}
	displayName = naming.DisplayName(code, country, number, multiplier)
	now := nowText()
	if _, e = tx.ExecContext(ctx, `UPDATE nodes SET number=?,asn=?,asn_server=?,exit_ip=?,country_code=?,country=?,display_name=?,modified_at=? WHERE id=?`, number, asn, asnServer, exitIP, code, country, displayName, now, id); e != nil {
		return e
	}
	if e = putHistoricalField(ctx, tx, id, "country", map[string]string{"code": code, "name": country}, now); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) UpdateNodeDisplayName(ctx context.Context, id, displayName string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := nowText()
	if _, e = tx.ExecContext(ctx, `UPDATE nodes SET display_name=?,modified_at=? WHERE id=? AND display_name<>?`, displayName, now, id, displayName); e != nil {
		return e
	}
	return tx.Commit()
}

// UpdateNodeMultiplier repairs a multiplier derived from the source name and
// keeps both the current node row and field-level history in sync.
func (s *Store) UpdateNodeMultiplier(ctx context.Context, id string, multiplier float64, displayName string) error {
	if multiplier <= 0 {
		return errors.New("node multiplier must be positive")
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := nowText()
	if _, e = tx.ExecContext(ctx, `UPDATE nodes SET multiplier=?,display_name=?,modified_at=? WHERE id=? AND (multiplier<>? OR display_name<>?)`, multiplier, displayName, now, id, multiplier, displayName); e != nil {
		return e
	}
	if e = putHistoricalField(ctx, tx, id, "multiplier", multiplier, now); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) AddMeasurement(ctx context.Context, m model.Measurement) error {
	var av any
	if m.Available != nil {
		if *m.Available {
			av = 1
		} else {
			av = 0
		}
	}
	_, e := s.DB.ExecContext(ctx, `INSERT INTO measurements(node_id,kind,available,latency_ms,direct_latency_ms,exit_ip,exit_error,error,config_revision,tested_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.NodeID, m.Kind, av, m.LatencyMS, m.DirectLatencyMS, m.ExitIP, m.ExitError, m.Error, m.ConfigRevision, m.TestedAt.UTC().Format(time.RFC3339Nano))
	return e
}
func (s *Store) Measurements(ctx context.Context, nodeID string, since time.Time) ([]model.Measurement, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,node_id,kind,available,latency_ms,direct_latency_ms,exit_ip,exit_error,error,config_revision,tested_at FROM measurements WHERE node_id=? AND tested_at>=? ORDER BY tested_at`, nodeID, since.UTC().Format(time.RFC3339Nano))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Measurement
	for rows.Next() {
		var m model.Measurement
		var av sql.NullInt64
		var lat, directLat sql.NullFloat64
		var tested string
		if e = rows.Scan(&m.ID, &m.NodeID, &m.Kind, &av, &lat, &directLat, &m.ExitIP, &m.ExitError, &m.Error, &m.ConfigRevision, &tested); e != nil {
			return nil, e
		}
		if av.Valid {
			v := av.Int64 != 0
			m.Available = &v
		}
		if lat.Valid {
			v := lat.Float64
			m.LatencyMS = &v
		}
		if directLat.Valid {
			v := directLat.Float64
			m.DirectLatencyMS = &v
		}
		m.TestedAt, _ = time.Parse(time.RFC3339Nano, tested)
		out = append(out, m)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	hourly, e := s.DB.QueryContext(ctx, `SELECT hour,config_revision,available_count,sample_count,latency_sum,latency_count,direct_latency_sum,direct_latency_count FROM measurement_hourly WHERE node_id=? AND hour>=? ORDER BY hour`, nodeID, since.UTC().Format("2006-01-02T15:00:00Z"))
	if e != nil {
		return nil, e
	}
	defer hourly.Close()
	for hourly.Next() {
		var hour, revision string
		var availableCount, sampleCount, latencyCount, directCount int
		var latencySum, directSum float64
		if e = hourly.Scan(&hour, &revision, &availableCount, &sampleCount, &latencySum, &latencyCount, &directSum, &directCount); e != nil {
			return nil, e
		}
		at, _ := time.Parse(time.RFC3339, hour)
		if sampleCount > 0 {
			available := availableCount == sampleCount
			m := model.Measurement{NodeID: nodeID, Kind: "cycle", Available: &available, SampleCount: sampleCount, AvailableCount: availableCount, ConfigRevision: revision, TestedAt: at}
			out = append(out, m)
		}
		if latencyCount > 0 {
			v := latencySum / float64(latencyCount)
			m := model.Measurement{NodeID: nodeID, Kind: "latency", LatencyMS: &v, SampleCount: latencyCount, ConfigRevision: revision, TestedAt: at}
			if directCount > 0 {
				d := directSum / float64(directCount)
				m.DirectLatencyMS = &d
			}
			out = append(out, m)
		}
	}
	return out, hourly.Err()
}

// RollupMeasurements retains full-resolution observations for 48 hours and
// compact hourly summaries for 90 days.
func (s *Store) RollupMeasurements(ctx context.Context, rawBefore, hourlyBefore time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cut := rawBefore.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO measurement_hourly(node_id,hour,config_revision,available_count,sample_count,latency_sum,latency_count,direct_latency_sum,direct_latency_count)
	SELECT node_id,strftime('%Y-%m-%dT%H:00:00Z',tested_at),config_revision,
	SUM(CASE WHEN kind='cycle' AND available=1 THEN 1 ELSE 0 END),SUM(CASE WHEN kind='cycle' AND available IS NOT NULL THEN 1 ELSE 0 END),
	SUM(CASE WHEN kind='cycle' AND latency_ms IS NOT NULL THEN latency_ms ELSE 0 END),SUM(CASE WHEN kind='cycle' AND latency_ms IS NOT NULL THEN 1 ELSE 0 END),
	SUM(CASE WHEN kind='cycle' AND direct_latency_ms IS NOT NULL THEN direct_latency_ms ELSE 0 END),SUM(CASE WHEN kind='cycle' AND direct_latency_ms IS NOT NULL THEN 1 ELSE 0 END)
	FROM measurements WHERE tested_at<? GROUP BY node_id,strftime('%Y-%m-%dT%H:00:00Z',tested_at),config_revision
	ON CONFLICT(node_id,hour,config_revision) DO UPDATE SET available_count=available_count+excluded.available_count,sample_count=sample_count+excluded.sample_count,latency_sum=latency_sum+excluded.latency_sum,latency_count=latency_count+excluded.latency_count,direct_latency_sum=direct_latency_sum+excluded.direct_latency_sum,direct_latency_count=direct_latency_count+excluded.direct_latency_count`, cut)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM measurements WHERE tested_at<?`, cut); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM measurement_hourly WHERE hour<?`, hourlyBefore.UTC().Format("2006-01-02T15:00:00Z")); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) LatestMeasurementAt(ctx context.Context, taskID, kind string) (time.Time, error) {
	var v sql.NullString
	e := s.DB.QueryRowContext(ctx, `SELECT MAX(m.tested_at) FROM measurements m JOIN nodes n ON n.id=m.node_id WHERE n.task_id=? AND m.kind=?`, taskID, kind).Scan(&v)
	return parseTime(v), e
}

func (s *Store) DeleteMeasurementsByKind(ctx context.Context, kind string) (int64, error) {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM measurements WHERE kind=?`, kind)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
func (s *Store) PutQuality(ctx context.Context, q model.Quality) error {
	raw, _ := json.Marshal(q.UnavailableByHour)
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	at := q.CalculatedAt.UTC().Format(time.RFC3339Nano)
	if _, e = tx.ExecContext(ctx, `INSERT INTO qualities(node_id,availability,availability_raw,samples,unavailable_by_hour_json,average_latency_ms,priority,calculated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET availability=excluded.availability,availability_raw=excluded.availability_raw,samples=excluded.samples,unavailable_by_hour_json=excluded.unavailable_by_hour_json,average_latency_ms=excluded.average_latency_ms,priority=excluded.priority,calculated_at=excluded.calculated_at`, q.NodeID, q.Availability, q.AvailabilityRaw, q.Samples, string(raw), q.AverageLatencyMS, q.Priority, at); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO quality_snapshots(node_id,calculated_at,availability,availability_raw,samples,average_latency_ms,priority) VALUES(?,?,?,?,?,?,?)`, q.NodeID, at, q.Availability, q.AvailabilityRaw, q.Samples, q.AverageLatencyMS, q.Priority); e != nil {
		return e
	}
	fields := map[string]any{
		"availability":        map[string]any{"value": q.Availability, "raw": q.AvailabilityRaw, "samples": q.Samples},
		"unavailable_by_hour": q.UnavailableByHour,
		"latency":             q.AverageLatencyMS,
		"priority":            q.Priority,
	}
	for name, value := range fields {
		valueJSON, _ := json.Marshal(value)
		if _, e = tx.ExecContext(ctx, `INSERT INTO quality_fields(node_id,field_name,value_json,calculated_at) VALUES(?,?,?,?) ON CONFLICT(node_id,field_name) DO UPDATE SET value_json=excluded.value_json,calculated_at=excluded.calculated_at`, q.NodeID, name, string(valueJSON), at); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) PutDailyQuality(ctx context.Context, q model.Quality, day time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO quality_daily(node_id,day,availability,availability_raw,samples,average_latency_ms,priority) VALUES(?,?,?,?,?,?,?) ON CONFLICT(node_id,day) DO UPDATE SET availability=excluded.availability,availability_raw=excluded.availability_raw,samples=excluded.samples,average_latency_ms=excluded.average_latency_ms,priority=excluded.priority`, q.NodeID, day.UTC().Format("2006-01-02"), q.Availability, q.AvailabilityRaw, q.Samples, q.AverageLatencyMS, q.Priority)
	return err
}
func (s *Store) PruneQualityHistory(ctx context.Context, snapshotBefore, dailyBefore time.Time) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM quality_snapshots WHERE calculated_at<?`, snapshotBefore.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM quality_daily WHERE day<?`, dailyBefore.UTC().Format("2006-01-02"))
	return err
}
func (s *Store) Qualities(ctx context.Context, taskID string) (map[string]model.Quality, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT q.node_id,q.availability,q.availability_raw,q.samples,q.unavailable_by_hour_json,q.average_latency_ms,q.priority,q.calculated_at FROM qualities q JOIN nodes n ON n.id=q.node_id WHERE n.task_id=? AND n.removed_at IS NULL`, taskID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]model.Quality{}
	for rows.Next() {
		var q model.Quality
		var raw, at string
		if e = rows.Scan(&q.NodeID, &q.Availability, &q.AvailabilityRaw, &q.Samples, &raw, &q.AverageLatencyMS, &q.Priority, &at); e != nil {
			return nil, e
		}
		json.Unmarshal([]byte(raw), &q.UnavailableByHour)
		q.CalculatedAt, _ = time.Parse(time.RFC3339Nano, at)
		out[q.NodeID] = q
	}
	return out, rows.Err()
}
func (s *Store) PutArtifact(ctx context.Context, taskID, kind, sha string, content []byte) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO artifacts(task_id,kind,content,sha256,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(task_id,kind) DO UPDATE SET content=excluded.content,sha256=excluded.sha256,updated_at=excluded.updated_at`, taskID, kind, content, sha, nowText())
	return e
}
func (s *Store) Artifact(ctx context.Context, taskID, kind string) ([]byte, time.Time, error) {
	var b []byte
	var at string
	e := s.DB.QueryRowContext(ctx, `SELECT content,updated_at FROM artifacts WHERE task_id=? AND kind=?`, taskID, kind).Scan(&b, &at)
	t, _ := time.Parse(time.RFC3339Nano, at)
	return b, t, e
}

func (s *Store) Compact(ctx context.Context, maxBytes int64) error {
	if maxBytes <= 0 {
		return nil
	}
	for {
		if _, e := s.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); e != nil {
			return e
		}
		var pages, size int64
		if e := s.DB.QueryRowContext(ctx, `SELECT page_count,page_size FROM pragma_page_count(),pragma_page_size()`).Scan(&pages, &size); e != nil {
			return e
		}
		if pages*size <= maxBytes {
			return nil
		}
		var table string
		var count int64
		// Derived high-frequency snapshots are cheapest to lose. Preserve raw
		// observations until every compact representation has been exhausted.
		for _, candidate := range []string{"quality_snapshots", "measurement_hourly", "quality_daily", "measurements"} {
			if e := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+candidate).Scan(&count); e != nil {
				return e
			}
			if count > 0 {
				table = candidate
				break
			}
		}
		if table == "" {
			return fmt.Errorf("database base data is %d bytes, above configured limit %d", pages*size, maxBytes)
		}
		batch := count / 5
		if batch < 1 {
			batch = 1
		}
		order, selector := "hour", "rowid"
		if table == "quality_snapshots" {
			order = "calculated_at"
		} else if table == "quality_daily" {
			order = "day"
		} else if table == "measurements" {
			order, selector = "tested_at,id", "id"
		}
		query := fmt.Sprintf(`DELETE FROM %s WHERE %s IN (SELECT %s FROM %s ORDER BY %s LIMIT ?)`, table, selector, selector, table, order)
		if _, e := s.DB.ExecContext(ctx, query, batch); e != nil {
			return e
		}
		if _, e := s.DB.ExecContext(ctx, `VACUUM`); e != nil {
			return e
		}
	}
}
