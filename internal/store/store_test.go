package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/model"
	"github.com/yaodao0yaodao/proxylens/internal/subscription"
	_ "modernc.org/sqlite"
)

func TestCountryLocalNumbersNeverReused(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	create := func(id, token string) {
		task := model.Task{ID: id, Name: id, SubscriptionURL: "https://example.com", PublishToken: token}
		if err := st.CreateTask(ctx, &task); err != nil {
			t.Fatal(err)
		}
	}
	put := func(task, id, server string) {
		if err := st.UpsertNodes(ctx, task, []model.Node{{ID: id, Protocol: "ss", Server: server, Port: 443, OriginalName: id, DisplayName: "未知", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	create("t1", "one")
	put("t1", "n1", "one.example")
	if err = st.UpdateNodeGeo(ctx, "n1", "", "", "1.1.1.1", "JP", "Japan", ""); err != nil {
		t.Fatal(err)
	}
	put("t1", "n2", "two.example")
	if err = st.UpdateNodeGeo(ctx, "n2", "", "", "2.2.2.2", "US", "United States", ""); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteTask(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	create("t2", "two")
	put("t2", "n3", "three.example")
	if err = st.UpdateNodeGeo(ctx, "n3", "", "", "3.3.3.3", "JP", "Japan", ""); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.Nodes(ctx, "t2", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Number != 2 {
		t.Fatalf("JP number was reused: %+v", nodes)
	}
	put("t2", "n4", "four.example")
	if err = st.UpdateNodeGeo(ctx, "n4", "", "", "4.4.4.4", "CN", "China", ""); err != nil {
		t.Fatal(err)
	}
	nodes, err = st.Nodes(ctx, "t2", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == "n4" && n.Number != 1 {
			t.Fatalf("CN did not start at 1: %+v", n)
		}
	}
}

func TestLegacySchemaMigrationPreservesDurableDataAndRenumbersByCountry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE tasks(id TEXT PRIMARY KEY,name TEXT,subscription_url TEXT,subscription_ua TEXT,publish_token TEXT,enabled INTEGER,last_subscription_at TEXT,last_rules_at TEXT,last_sfa_at TEXT,last_carton_at TEXT,last_error TEXT,created_at TEXT,updated_at TEXT);
CREATE TABLE nodes(id TEXT PRIMARY KEY,task_id TEXT,number INTEGER UNIQUE,protocol TEXT,server TEXT,port INTEGER,original_name TEXT,display_name TEXT,multiplier REAL,asn TEXT,exit_ip TEXT,country_code TEXT,country TEXT,config_json TEXT,added_at TEXT,modified_at TEXT,removed_at TEXT,UNIQUE(task_id,protocol,server,port));
CREATE TABLE node_fields(node_id TEXT,field_name TEXT,value_json TEXT,added_at TEXT,modified_at TEXT,removed_at TEXT,PRIMARY KEY(node_id,field_name));
CREATE TABLE notices(task_id TEXT,ordinal INTEGER,name TEXT,PRIMARY KEY(task_id,ordinal));
CREATE TABLE measurements(id INTEGER PRIMARY KEY,node_id TEXT,kind TEXT,available INTEGER,latency_ms REAL,speed_bps REAL,download_bytes INTEGER,exit_ip TEXT,error TEXT,tested_at TEXT);
CREATE TABLE qualities(node_id TEXT PRIMARY KEY,availability REAL,availability_raw REAL,samples INTEGER,unavailable_by_hour_json TEXT,average_speed_bps REAL,speed_stability REAL,average_latency_ms REAL,latency_stability REAL,priority REAL,calculated_at TEXT);
CREATE TABLE artifacts(task_id TEXT,kind TEXT,content BLOB,sha256 TEXT,updated_at TEXT,PRIMARY KEY(task_id,kind)); CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT);`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO tasks VALUES('t','n','u','ua','tok',1,NULL,NULL,NULL,NULL,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'); INSERT INTO nodes VALUES('a','t',8,'ss','a',1,'a','a',1,'','','JP','Japan','{}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',NULL),('b','t',20,'ss','b',2,'b','b',1,'','','JP','Japan','{}','2026-01-02T00:00:00Z','2026-01-02T00:00:00Z',NULL),('u','t',21,'ss','u',3,'u','u',1,'','','','Unknown','{}','2026-01-03T00:00:00Z','2026-01-03T00:00:00Z',NULL); INSERT INTO measurements VALUES(1,'a','availability',1,NULL,NULL,0,'','','2026-01-01T00:00:00Z'); INSERT INTO qualities VALUES('a',1,1,1,'{}',0,0,0,0,1,'2026-01-01T00:00:00Z'); INSERT INTO artifacts VALUES('t','sfa',X'01','x','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	nodes, err := st.Nodes(context.Background(), "t", false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"a": 1, "b": 2, "u": 0}
	for _, n := range nodes {
		if n.Number != want[n.ID] {
			t.Fatalf("node %s number=%d want=%d", n.ID, n.Number, want[n.ID])
		}
	}
	var count int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM measurements`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("incompatible pre-cycle measurements were retained: %d %v", count, err)
	}
	if _, _, err = st.Artifact(context.Background(), "t", "sfa"); err != nil {
		t.Fatalf("artifact lost: %v", err)
	}
	var table string
	err = st.DB.QueryRow(`PRAGMA foreign_key_check`).Scan(&table)
	if err != sql.ErrNoRows {
		t.Fatalf("foreign-key integrity after migration: table=%q err=%v", table, err)
	}
}

func TestV6MigrationToCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v6.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE tasks(id TEXT PRIMARY KEY,last_rules_at TEXT);
CREATE TABLE nodes(id TEXT PRIMARY KEY);
CREATE TABLE measurements(id INTEGER PRIMARY KEY,node_id TEXT,kind TEXT,available INTEGER,latency_ms REAL,speed_bps REAL,download_bytes INTEGER,billed_bytes INTEGER,exit_ip TEXT,error TEXT,tested_at TEXT);
CREATE TABLE qualities(node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,availability REAL NOT NULL,availability_raw REAL NOT NULL,samples INTEGER NOT NULL,unavailable_by_hour_json TEXT NOT NULL,average_speed_bps REAL NOT NULL,speed_stability REAL NOT NULL,average_latency_ms REAL NOT NULL,latency_stability REAL NOT NULL,stability REAL NOT NULL,priority REAL NOT NULL,calculated_at TEXT NOT NULL);
CREATE TABLE quality_fields(node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,field_name TEXT NOT NULL,value_json TEXT NOT NULL,calculated_at TEXT NOT NULL,PRIMARY KEY(node_id,field_name));
INSERT INTO tasks VALUES('t','2026-01-01T00:00:00Z');
INSERT INTO nodes VALUES('n');
INSERT INTO qualities VALUES('n',.8,.9,12,'{}',1048576,.75,80,.85,.79,66,'2026-01-01T00:00:00Z');
INSERT INTO quality_fields VALUES('n','stability','.79','2026-01-01T00:00:00Z');
INSERT INTO quality_fields VALUES('n','latency','80','2026-01-01T00:00:00Z');
INSERT INTO measurements VALUES(1,'n','latency',NULL,145,NULL,0,0,'','','2026-01-01T00:00:00Z');
PRAGMA user_version=6;`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var version int
	if err = st.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	var obsolete int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('qualities') WHERE name IN ('stability','speed_stability','latency_stability','average_speed_bps','download_priority','latency_priority')`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("obsolete stability columns remain: count=%d err=%v", obsolete, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM qualities`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("incompatible pre-cycle quality remained: count=%d err=%v", obsolete, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM measurements WHERE kind='latency'`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("obsolete latency measurements remain: count=%d err=%v", obsolete, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('measurements') WHERE name='direct_latency_ms'`).Scan(&obsolete); err != nil || obsolete != 1 {
		t.Fatalf("direct latency column missing: count=%d err=%v", obsolete, err)
	}
}

func TestV12MigrationRemovesSpeedDataAndSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v12.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE tasks(id TEXT PRIMARY KEY);
CREATE TABLE nodes(id TEXT PRIMARY KEY,task_id TEXT);
CREATE TABLE measurements(id INTEGER PRIMARY KEY AUTOINCREMENT,node_id TEXT,kind TEXT,available INTEGER,latency_ms REAL,direct_latency_ms REAL,speed_bps REAL,download_bytes INTEGER NOT NULL DEFAULT 0,billed_bytes INTEGER NOT NULL DEFAULT 0,exit_ip TEXT NOT NULL DEFAULT '',exit_error TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',config_revision TEXT NOT NULL DEFAULT '',tested_at TEXT NOT NULL);
CREATE TABLE measurement_hourly(node_id TEXT,hour TEXT,config_revision TEXT,available_count INTEGER,sample_count INTEGER,latency_sum REAL,latency_count INTEGER,direct_latency_sum REAL,direct_latency_count INTEGER,speed_sum REAL,speed_count INTEGER,speed_attempt_count INTEGER,download_bytes INTEGER,billed_bytes INTEGER,PRIMARY KEY(node_id,hour,config_revision));
CREATE TABLE qualities(node_id TEXT PRIMARY KEY,availability REAL,availability_raw REAL,samples INTEGER,unavailable_by_hour_json TEXT,average_speed_bps REAL,average_latency_ms REAL,priority REAL,download_priority REAL,latency_priority REAL,calculated_at TEXT);
CREATE TABLE quality_snapshots(node_id TEXT,calculated_at TEXT,availability REAL,availability_raw REAL,samples INTEGER,average_speed_bps REAL,average_latency_ms REAL,priority REAL,download_priority REAL,latency_priority REAL,PRIMARY KEY(node_id,calculated_at));
CREATE TABLE quality_daily(node_id TEXT,day TEXT,availability REAL,availability_raw REAL,samples INTEGER,average_speed_bps REAL,average_latency_ms REAL,priority REAL,download_priority REAL,latency_priority REAL,PRIMARY KEY(node_id,day));
CREATE TABLE quality_fields(node_id TEXT,field_name TEXT,value_json TEXT,calculated_at TEXT,PRIMARY KEY(node_id,field_name));
CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT INTO tasks VALUES('t'); INSERT INTO nodes VALUES('n','t');
INSERT INTO measurements(node_id,kind,speed_bps,tested_at) VALUES('n','speed',10485760,'2026-01-01T00:00:00Z');
INSERT INTO measurements(node_id,kind,available,latency_ms,tested_at) VALUES('n','cycle',1,80,'2026-01-01T00:01:00Z');
INSERT INTO measurement_hourly VALUES('n','2026-01-01T00:00:00Z','r',1,1,80,1,50,1,10485760,1,1,8388608,8388608);
INSERT INTO qualities VALUES('n',.9,.95,10,'{}',10485760,80,70,75,72,'2026-01-01T00:00:00Z');
INSERT INTO quality_snapshots VALUES('n','2026-01-01T00:00:00Z',.9,.95,10,10485760,80,70,75,72);
INSERT INTO quality_daily VALUES('n','2026-01-01',.9,.95,10,10485760,80,70,75,72);
INSERT INTO quality_fields VALUES('n','speed','10485760','2026-01-01T00:00:00Z');
INSERT INTO settings VALUES('schedule','{"detection_minutes":30,"daily_budget_mib":100}'),('task_settings:t','{"speed_sample_mib":8,"daily_budget_mib":100,"google_play_mode":"stable"}');
PRAGMA user_version=12;`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var version, count int
	if err = st.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 13 {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM measurements`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("speed rows were not removed: count=%d err=%v", count, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('measurements') WHERE name='speed_bps'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("speed measurement column remains: count=%d err=%v", count, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('qualities') WHERE name IN ('average_speed_bps','download_priority','latency_priority')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("speed-derived quality columns remain: count=%d err=%v", count, err)
	}
	var priority float64
	if err = st.DB.QueryRow(`SELECT priority FROM qualities WHERE node_id='n'`).Scan(&priority); err != nil || priority != 0 {
		t.Fatalf("speed-derived current priority remains: priority=%v err=%v", priority, err)
	}
	if err = st.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM quality_snapshots)+(SELECT COUNT(*) FROM quality_daily)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("speed-derived priority history remains: count=%d err=%v", count, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM quality_fields`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("speed-derived quality fields remain: count=%d err=%v", count, err)
	}
	schedule, _ := st.Setting(context.Background(), "schedule")
	taskSettings, _ := st.Setting(context.Background(), "task_settings:t")
	if schedule != `{"detection_minutes":30}` || taskSettings != `{"google_play_mode":"stable"}` {
		t.Fatalf("speed settings not removed: schedule=%s task=%s", schedule, taskSettings)
	}
}

func TestSubscriptionFailureDoesNotOverwriteLastSuccess(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "failure.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	task := model.Task{ID: "t", SubscriptionURL: "https://example.com", PublishToken: "token"}
	if err = st.CreateTask(ctx, &task); err != nil {
		t.Fatal(err)
	}
	if err = st.SetTaskRun(ctx, task.ID, "subscription", ""); err != nil {
		t.Fatal(err)
	}
	success, err := st.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if err = st.MarkSubscriptionFailure(ctx, task.ID, "network failed"); err != nil {
		t.Fatal(err)
	}
	failed, err := st.Task(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !failed.LastSubscriptionAt.Equal(success.LastSubscriptionAt) || failed.SubscriptionFailedSince.IsZero() || failed.LastError != "network failed" {
		t.Fatalf("failure timestamps incorrect: before=%+v after=%+v", success, failed)
	}
	if err = st.SetTaskRun(ctx, task.ID, "subscription", ""); err != nil {
		t.Fatal(err)
	}
	recovered, _ := st.Task(ctx, task.ID)
	if !recovered.SubscriptionFailedSince.IsZero() || recovered.LastError != "" || !recovered.LastSubscriptionAt.After(success.LastSubscriptionAt) {
		t.Fatalf("successful refresh did not clear failure state: %+v", recovered)
	}
}

func TestUnknownExitGetsNoNumber(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := model.Task{ID: "t", SubscriptionURL: "https://example.com", PublishToken: "token"}
	if err = st.CreateTask(ctx, &task); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertNodes(ctx, "t", []model.Node{{ID: "n", Protocol: "ss", Server: "x", Port: 1, OriginalName: "raw", Multiplier: 1, Config: map[string]any{}}}, nil); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.Nodes(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].Number != 0 {
		t.Fatalf("unknown exit numbered %d", nodes[0].Number)
	}
}

func TestPausedTaskSubscriptionRemainsPublishable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := model.Task{ID: "t", SubscriptionURL: "https://example.com", PublishToken: "token"}
	if err = st.CreateTask(ctx, &task); err != nil {
		t.Fatal(err)
	}
	if err = st.SetTaskEnabled(ctx, task.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := st.TaskByToken(ctx, task.PublishToken)
	if err != nil || got.ID != task.ID || got.Enabled {
		t.Fatalf("paused task not publishable: task=%+v err=%v", got, err)
	}
}

func TestRemovedNodeCanReviveWithHistoryAndNewEndpointID(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := model.Task{ID: "t", SubscriptionURL: "https://example.com", PublishToken: "token"}
	if err = st.CreateTask(ctx, &task); err != nil {
		t.Fatal(err)
	}
	old := model.Node{ID: "old-id", Protocol: "ss", Server: "old.example", Port: 443, OriginalName: "日本专线 1x", Multiplier: 1, Config: map[string]any{"method": "aes-128-gcm", "password": "x"}}
	if err = st.UpsertNodes(ctx, "t", []model.Node{old}, nil); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = st.AddMeasurement(ctx, model.Measurement{NodeID: old.ID, Kind: "availability", Available: &yes, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertNodes(ctx, "t", nil, nil); err != nil {
		t.Fatal(err)
	}
	revived := old
	revived.ID, revived.Server, revived.Multiplier = "new-endpoint-id", "new.example", 0.5
	if err = st.UpsertNodes(ctx, "t", []model.Node{revived}, nil); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.Nodes(ctx, "t", false)
	if err != nil || len(nodes) != 1 || nodes[0].ID != old.ID || nodes[0].Server != "new.example" || nodes[0].Multiplier != 0.5 || nodes[0].RemovedAt != nil {
		t.Fatalf("node did not revive in place: nodes=%+v err=%v", nodes, err)
	}
	measurements, err := st.Measurements(ctx, old.ID, time.Time{})
	if err != nil || len(measurements) != 1 {
		t.Fatalf("revived history missing: %d %v", len(measurements), err)
	}
}

func TestUpsertPreservesNodeAcrossRotatingServer(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := model.Task{ID: "t", SubscriptionURL: "https://example.com", PublishToken: "token"}
	if err = st.CreateTask(ctx, &task); err != nil {
		t.Fatal(err)
	}
	oldNodes, _, err := subscription.Parse("t", []byte("proxies:\n  - {name: 香港 01, type: anytls, server: old.example, port: 27002, sni: old.example, password: shared}\n"))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an existing database created by the old endpoint-based ID logic.
	oldNodes[0].ID = "legacy-endpoint-id"
	originalID := oldNodes[0].ID
	if err = st.UpsertNodes(ctx, "t", oldNodes, nil); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = st.AddMeasurement(ctx, model.Measurement{NodeID: originalID, Kind: "availability", Available: &yes, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	newNodes, _, err := subscription.Parse("t", []byte("proxies:\n  - {name: 香港 01, type: anytls, server: new.example, port: 27002, sni: new.example, password: shared}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err = st.UpsertNodes(ctx, "t", newNodes, nil); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.Nodes(ctx, "t", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != originalID || nodes[0].Server != "new.example" {
		t.Fatalf("rotating server recreated node or left stale endpoint: %+v", nodes)
	}
	measurements, err := st.Measurements(ctx, originalID, time.Time{})
	if err != nil || len(measurements) != 1 {
		t.Fatalf("history was not preserved: %d %v", len(measurements), err)
	}
}

func TestMergeRotatedNodeHistoryOnlyForUnambiguousGroup(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task := model.Task{ID: "t", SubscriptionURL: "https://example.com", PublishToken: "token"}
	if err = st.CreateTask(ctx, &task); err != nil {
		t.Fatal(err)
	}
	oldConfig := `{"name":"香港 01","type":"anytls","server":"old.example","port":27002,"sni":"old.example","password":"shared"}`
	newConfig := `{"name":"香港 01","type":"anytls","server":"new.example","port":27002,"sni":"new.example","password":"shared"}`
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = st.DB.Exec(`INSERT INTO nodes(id,task_id,number,protocol,server,port,original_name,display_name,multiplier,config_json,added_at,modified_at,removed_at) VALUES('old','t',0,'anytls','old.example',27002,'香港 01','',1,?,?,?,?),('active','t',0,'anytls','new.example',27002,'香港 01','',1,?,?,?,NULL)`, oldConfig, now, now, now, newConfig, now, now); err != nil {
		t.Fatal(err)
	}
	yes := true
	if err = st.AddMeasurement(ctx, model.Measurement{NodeID: "old", Kind: "availability", Available: &yes, TestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = st.AddMeasurement(ctx, model.Measurement{NodeID: "old", Kind: "cycle", Available: &yes, TestedAt: time.Now().Add(-72 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err = st.RollupMeasurements(ctx, time.Now().Add(-48*time.Hour), time.Now().Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	moved, err := st.MergeRotatedNodeHistory(ctx, "t")
	if err != nil || moved != 1 {
		t.Fatalf("merge=%d err=%v", moved, err)
	}
	measurements, err := st.Measurements(ctx, "active", time.Time{})
	if err != nil || len(measurements) != 2 {
		t.Fatalf("merged history missing: %d %v", len(measurements), err)
	}
	if err = st.PutQuality(ctx, model.Quality{NodeID: "active", CalculatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if moved, err = st.MergeRotatedNodeHistory(ctx, "t"); err != nil || moved != 0 {
		t.Fatalf("second merge is not idempotent: moved=%d err=%v", moved, err)
	}
	complete, err := st.QualityCoverageComplete(ctx, "t")
	if err != nil || !complete {
		t.Fatalf("idempotent merge removed current quality: complete=%v err=%v", complete, err)
	}
}

func TestOnlySemanticFieldsKeepHistoryAndServerChangeInvalidatesASN(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateTask(ctx, &model.Task{ID: "t-fields", SubscriptionURL: "https://example.com", PublishToken: "fields-token"}); err != nil {
		t.Fatal(err)
	}
	node := model.Node{ID: "node-fields", Protocol: "vless", Server: "old.example", Port: 443, OriginalName: "日本 01", Multiplier: 1, Config: map[string]any{"type": "vless", "server": "old.example", "port": 443, "uuid": "a", "tls": map[string]any{"enabled": true, "server_name": "old.example"}}}
	if err = st.UpsertNodes(ctx, "t-fields", []model.Node{node}, nil); err != nil {
		t.Fatal(err)
	}
	var oldRevision string
	if err = st.DB.QueryRow(`SELECT config_revision FROM nodes WHERE id=?`, node.ID).Scan(&oldRevision); err != nil || oldRevision == "" {
		t.Fatalf("initial config revision missing: %q %v", oldRevision, err)
	}
	if err = st.UpdateNodeGeo(ctx, node.ID, "AS64500", "old.example", "1.1.1.1", "JP", "Japan", ""); err != nil {
		t.Fatal(err)
	}
	node.Server = "new.example"
	node.Config["server"] = "new.example"
	delete(node.Config, "uuid")
	if err = st.UpsertNodes(ctx, "t-fields", []model.Node{node}, nil); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.Nodes(ctx, "t-fields", false)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes=%v err=%v", nodes, err)
	}
	if nodes[0].ASN != "" || nodes[0].ASNServer != "" {
		t.Fatalf("ASN was not invalidated after server change: %+v", nodes[0])
	}
	if nodes[0].ConfigRevision == "" || nodes[0].ConfigRevision == oldRevision {
		t.Fatalf("connection revision did not change: old=%q new=%q", oldRevision, nodes[0].ConfigRevision)
	}
	var fields int
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM node_fields WHERE node_id=? AND field_name NOT IN ('original_name','country','multiplier')`, node.ID).Scan(&fields); err != nil || fields != 0 {
		t.Fatalf("connection fields incorrectly retained as history: count=%d err=%v", fields, err)
	}
	var originalHistory int
	node.OriginalName = "日本 优化"
	if err = st.UpsertNodes(ctx, "t-fields", []model.Node{node}, nil); err != nil {
		t.Fatal(err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM node_fields WHERE node_id=? AND field_name='original_name'`, node.ID).Scan(&originalHistory); err != nil || originalHistory != 2 {
		t.Fatalf("source-name history was overwritten: count=%d err=%v", originalHistory, err)
	}
	if err = st.DB.QueryRow(`SELECT COUNT(*) FROM node_fields WHERE node_id=? AND field_name='original_name' AND removed_at IS NULL`, node.ID).Scan(&fields); err != nil || fields != 1 {
		t.Fatalf("source-name history has invalid active rows: count=%d err=%v", fields, err)
	}
}

func TestAmbiguousIdentityRequiresExplicitMerge(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.CreateTask(ctx, &model.Task{ID: "t-conflict", SubscriptionURL: "https://example.com", PublishToken: "conflict-token"}); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"type": "vless", "port": 443, "uuid": "shared"}
	nodes := []model.Node{{ID: "incoming-a", Protocol: "vless", Server: "a.example", Port: 443, OriginalName: "日本 A", Multiplier: 1, Config: config}, {ID: "incoming-b", Protocol: "vless", Server: "b.example", Port: 443, OriginalName: "日本 B", Multiplier: 1, Config: config}}
	if err = st.UpsertNodes(ctx, "t-conflict", nodes, nil); err != nil {
		t.Fatal(err)
	}
	conflicts, err := st.IdentityConflicts(ctx, "t-conflict")
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("same-batch distinct nodes were incorrectly marked ambiguous: %+v err=%v", conflicts, err)
	}
	renamed := model.Node{ID: "incoming-renamed", Protocol: "vless", Server: "new.example", Port: 443, OriginalName: "日本 新名称", Multiplier: 1, Config: config}
	if err = st.UpsertNodes(ctx, "t-conflict", []model.Node{renamed}, nil); err != nil {
		t.Fatal(err)
	}
	conflicts, err = st.IdentityConflicts(ctx, "t-conflict")
	if err != nil || len(conflicts) != 1 || len(conflicts[0].Candidates) != 2 {
		t.Fatalf("conflicts=%+v err=%v", conflicts, err)
	}
	for _, candidate := range conflicts[0].Candidates {
		if candidate.Protocol != "vless" || candidate.Server == "" || candidate.Port != 443 || candidate.OriginalName == "" || candidate.AddedAt.IsZero() || candidate.ModifiedAt.IsZero() {
			t.Fatalf("identity candidate is missing full safe details: %+v", candidate)
		}
	}
	if err = st.MergeNodes(ctx, "t-conflict", "incoming-a", "incoming-renamed"); err != nil {
		t.Fatal(err)
	}
	conflicts, err = st.IdentityConflicts(ctx, "t-conflict")
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range conflicts {
		if conflict.ResolvedAt == nil {
			t.Fatalf("unresolved conflict after merge: %+v", conflict)
		}
	}
}
