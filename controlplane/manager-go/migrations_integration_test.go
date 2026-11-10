package main

import (
	"context"
	"database/sql"
	"errors"
	"pressurecooker/manager/internal/schema"
	"strings"
	"testing"
	"time"
)

func forgetSchemaHistory(t *testing.T, p *Platform) {
	t.Helper()
	if _, err := p.DB.DB.Exec("DROP TABLE IF EXISTS " + p.DB.Prefix + "schema_migrations"); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLStartupRecordsVersionedSchema(t *testing.T) {
	p, _ := mysqlFixture(t)
	catalog, err := schema.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.DB.DB.QueryRow("SELECT COUNT(*) FROM " + p.DB.Prefix + "schema_migrations WHERE state='APPLIED'").Scan(&count); err != nil || count != len(catalog) {
		t.Fatalf("startup did not record current schema versions: count=%d want=%d err=%v", count, len(catalog), err)
	}
	restarted, err := newPlatform(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = restarted.DB.DB.QueryRow("SELECT COUNT(*) FROM " + p.DB.Prefix + "schema_migrations WHERE state='APPLIED'").Scan(&count); err != nil || count != len(catalog) {
		t.Fatal("restart changed migration history", err)
	}
}

func TestMySQLLegacyReportUpgradePreservesJSONAndExistingProjections(t *testing.T) {
	p, _ := mysqlFixture(t)
	id := newID("pressure-")
	raw := `{"id":"` + id + `","status":"READY","verdict_status":"PASS","metrics":{"sample_count":17,"success_count":15,"rps":2.5,"p50_ms":8.25,"duration_seconds":6.8}}`
	if _, err := p.DB.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,rps,data) VALUES(?,?,?,?,?,?,?,?,?)", id, defaultProjectID, newID("run-"), 1, "READY", now(), digest([]byte(id)), 123, raw); err != nil {
		t.Fatal(err)
	}
	child := newID("evidence-")
	childRaw := `{"sample_kind":"http","sample_count":17,"failure_count":2,"p95_ms":25.5}`
	if _, err := p.DB.Exec("INSERT INTO pressure_report_steps(id,project_id,report_id,created_at,position,sample_kind,sample_count,failure_count,data) VALUES(?,?,?,?,?,?,?,?,?)", child, defaultProjectID, id, now(), 0, "http", 17, 2, childRaw); err != nil {
		t.Fatal(err)
	}
	emptyID := newID("pressure-")
	emptyRaw := `{"id":"` + emptyID + `","status":"FAILED","metrics":{"sample_count":null,"p50_ms":null}}`
	if _, err := p.DB.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,?,?,?,?,?)", emptyID, defaultProjectID, newID("run-"), 1, "FAILED", now(), digest([]byte(emptyID)), emptyRaw); err != nil {
		t.Fatal(err)
	}
	forgetSchemaHistory(t, p)
	for table, columns := range map[string][]string{"pressure_reports": {"sample_count", "success_count", "p50_ms", "verdict_status"}, "pressure_report_steps": {"failure_count", "p95_ms"}} {
		for _, column := range columns {
			if _, err := p.DB.Exec("ALTER TABLE " + table + " DROP COLUMN " + column); err != nil {
				t.Fatal(err)
			}
		}
	}
	upgraded, err := newPlatform(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var saved, verdict string
	var sample, success int
	var p50, rps float64
	if err = upgraded.DB.QueryRow("SELECT data,sample_count,success_count,p50_ms,rps,verdict_status FROM pressure_reports WHERE id=?", id).Scan(&saved, &sample, &success, &p50, &rps, &verdict); err != nil {
		t.Fatalf("legacy report columns not upgraded: %v", err)
	}
	if saved != raw || sample != 17 || success != 15 || p50 != 8.25 || rps != 123 || verdict != "PASS" {
		t.Fatal("migration changed JSON/existing projection or failed JSON backfill")
	}
	var failures int
	var p95 float64
	if err = upgraded.DB.QueryRow("SELECT data,failure_count,p95_ms FROM pressure_report_steps WHERE id=?", child).Scan(&saved, &failures, &p95); err != nil || saved != childRaw || failures != 2 || p95 != 25.5 {
		t.Fatalf("child projection upgrade failed: %v", err)
	}
	var absent sql.NullFloat64
	if err = upgraded.DB.QueryRow("SELECT data,sample_count,p50_ms,verdict_status FROM pressure_reports WHERE id=?", emptyID).Scan(&saved, &sample, &absent, &verdict); err != nil || saved != emptyRaw || sample != 0 || absent.Valid || verdict != "NOT_SET" {
		t.Fatalf("missing metric semantics changed: %v", err)
	}
	state, err := upgraded.exportState()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state["schema_migrations"]; exists {
		t.Fatal("runtime schema history leaked into portable business backup")
	}
}

func schemaFixtureNamespace(t *testing.T, p *Platform) string {
	t.Helper()
	prefix := p.DB.Prefix + newID("")[:6] + "_"
	t.Cleanup(func() {
		for _, table := range []string{"probe", "schema_migrations"} {
			if _, err := p.DB.DB.Exec("DROP TABLE IF EXISTS `" + prefix + table + "`"); err != nil {
				t.Error("schema fixture cleanup", err)
			}
		}
	})
	return prefix
}
func schemaProbeCatalog() []schema.Migration {
	return []schema.Migration{
		{Version: 1, Name: "probe_table", Statements: []string{"CREATE TABLE IF NOT EXISTS `{{prefix}}probe`(id INT PRIMARY KEY,data LONGTEXT NOT NULL,executions INT NOT NULL DEFAULT 0) ENGINE=InnoDB", `INSERT IGNORE INTO ` + "`{{prefix}}probe`" + `(id,data) VALUES(1,'{"count":7}')`}},
		{Version: 2, Name: "once", Statements: []string{"UPDATE `{{prefix}}probe` SET executions=executions+1", "SELECT SLEEP(0.1)"}},
	}
}
func TestMySQLMigrationConcurrentStartupUsesOneConnectionPerProcess(t *testing.T) {
	p, _ := mysqlFixture(t)
	prefix := schemaFixtureNamespace(t, p)
	other, err := openMySQL(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	p.DB.SetMaxOpenConns(1)
	other.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failures := make(chan error, 2)
	for _, db := range []*sql.DB{p.DB.DB, other.DB} {
		go func(db *sql.DB) { failures <- schema.Run(ctx, db, prefix, schemaProbeCatalog()) }(db)
	}
	for range 2 {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	if err = schema.Run(ctx, p.DB.DB, prefix, schemaProbeCatalog()); err != nil {
		t.Fatal(err)
	}
	var count, executions int
	if err = p.DB.DB.QueryRow("SELECT executions FROM `" + prefix + "probe` WHERE id=1").Scan(&executions); err != nil || executions != 1 {
		t.Fatalf("migration replayed across startup: executions=%d err=%v", executions, err)
	}
	if err = p.DB.DB.QueryRow("SELECT COUNT(*) FROM `" + prefix + "schema_migrations` WHERE state='APPLIED'").Scan(&count); err != nil || count != 2 {
		t.Fatal("concurrent schema history", count, err)
	}
}
func TestMySQLMigrationHistoryRejectsDriftAndFutureVersions(t *testing.T) {
	p, _ := mysqlFixture(t)
	var checksum string
	if err := p.DB.QueryRow("SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.Exec("UPDATE schema_migrations SET checksum=? WHERE version=1", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateSchema(context.Background()); err == nil || !strings.Contains(err.Error(), "stage=checksum") {
		t.Fatalf("definition drift accepted: %v", err)
	}
	if _, err := p.DB.Exec("UPDATE schema_migrations SET checksum=? WHERE version=1", checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.Exec("INSERT INTO schema_migrations(version,name,checksum,state,plan) VALUES(99,'future',?,'APPLIED','[]')", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateSchema(context.Background()); err == nil || !strings.Contains(err.Error(), "stage=history") {
		t.Fatalf("unknown schema version accepted: %v", err)
	}
	if _, err := p.DB.Exec("DELETE FROM schema_migrations WHERE version=99"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.Exec("DELETE FROM schema_migrations WHERE version=2"); err != nil {
		t.Fatal(err)
	}
	if err := p.migrateSchema(context.Background()); err == nil || !strings.Contains(err.Error(), "stage=history") {
		t.Fatalf("schema history gap accepted: %v", err)
	}
}
func TestMySQLMigrationLockWaitCancellationAndNamespaceIsolation(t *testing.T) {
	p, _ := mysqlFixture(t)
	prefix := schemaFixtureNamespace(t, p)
	independent := schemaFixtureNamespace(t, p)
	owner, err := p.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	var database string
	if err = owner.QueryRowContext(context.Background(), "SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatal(err)
	}
	lock := "pressurecooker-schema:" + digest([]byte(database + "\x00" + prefix))[:40]
	var owned int
	if err = owner.QueryRowContext(context.Background(), "SELECT GET_LOCK(?,0)", lock).Scan(&owned); err != nil || owned != 1 {
		t.Fatal("fixture lock", err)
	}
	defer owner.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lock)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = schema.Run(ctx, p.DB.DB, prefix, schemaProbeCatalog())
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 350*time.Millisecond {
		t.Fatalf("migration ignored lock deadline: elapsed=%s err=%v", time.Since(start), err)
	}
	independentCtx, finish := context.WithTimeout(context.Background(), time.Second)
	defer finish()
	if err = schema.Run(independentCtx, p.DB.DB, independent, schemaProbeCatalog()); err != nil {
		t.Fatalf("other table prefix blocked by unrelated schema lock: %v", err)
	}
	if _, err = owner.ExecContext(context.Background(), "SELECT RELEASE_LOCK(?)", lock); err != nil {
		t.Fatal(err)
	}
	if err = schema.Run(context.Background(), p.DB.DB, prefix, schemaProbeCatalog()); err != nil {
		t.Fatal("wait cancellation leaked schema lock", err)
	}
}
func TestMySQLMigrationResumesOriginalColumnPlanAfterDDLAndCancellation(t *testing.T) {
	p, _ := mysqlFixture(t)
	prefix := schemaFixtureNamespace(t, p)
	catalog := schemaProbeCatalog()[:1]
	if err := schema.Run(context.Background(), p.DB.DB, prefix, catalog); err != nil {
		t.Fatal(err)
	}
	catalog = append(catalog, schema.Migration{Version: 2, Name: "backfill", Columns: []schema.Column{{Table: "probe", Name: "amount", Definition: "BIGINT NOT NULL DEFAULT 0", Backfill: "SLEEP(1)+JSON_EXTRACT(data,'$.count')"}}})
	p.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := schema.Run(ctx, p.DB.DB, prefix, catalog)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected interrupted backfill: %v", err)
	}
	var state, plan string
	if err = p.DB.DB.QueryRow("SELECT state,plan FROM `"+prefix+"schema_migrations` WHERE version=2").Scan(&state, &plan); err != nil || state != "APPLYING" || !strings.Contains(plan, "probe.amount") {
		t.Fatalf("lost original partial-DDL plan: %s %s %v", state, plan, err)
	}
	retry, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	if err = schema.Run(retry, p.DB.DB, prefix, catalog); err != nil {
		t.Fatal("resume after partial DDL", err)
	}
	var amount int
	var data string
	if err = p.DB.DB.QueryRow("SELECT amount,data FROM `"+prefix+"probe` WHERE id=1").Scan(&amount, &data); err != nil || amount != 7 || data != `{"count":7}` {
		t.Fatalf("existing column skipped required recovered backfill: %d %s %v", amount, data, err)
	}
	if _, err = p.DB.DB.Exec("UPDATE `" + prefix + "probe` SET amount=99"); err != nil {
		t.Fatal(err)
	}
	if err = schema.Run(retry, p.DB.DB, prefix, catalog); err != nil {
		t.Fatal(err)
	}
	if err = p.DB.DB.QueryRow("SELECT amount FROM `" + prefix + "probe` WHERE id=1").Scan(&amount); err != nil || amount != 99 {
		t.Fatal("completed migration replayed backfill", err)
	}
}

func TestMySQLLegacyReportUpgradeAllProjectionColumns(t *testing.T) {
	p, _ := mysqlFixture(t)
	catalog, err := schema.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	drops := map[string][]string{}
	for _, column := range catalog[2].Columns {
		drops[column.Table] = append(drops[column.Table], "DROP COLUMN `"+column.Name+"`")
	}
	forgetSchemaHistory(t, p)
	for table, clauses := range drops {
		if _, err = p.DB.Exec("ALTER TABLE " + table + " " + strings.Join(clauses, ",")); err != nil {
			t.Fatal(err)
		}
	}
	id := newID("pressure-")
	reportRaw := `{"id":"` + id + `","status":"READY","verdict_status":null,"verdict":{"status":"PASS"},"metrics":{"sample_count":17,"rps":2.5}}`
	if _, err = p.DB.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,?,?,?,?,?)", id, defaultProjectID, newID("run-"), 1, "READY", now(), digest([]byte(id)), reportRaw); err != nil {
		t.Fatal(err)
	}
	children := map[string]string{
		"sources":  `{"name":"results.jtl","status":"VERIFIED","bucket":"reports","object_key":null,"key":"originals/raw/results.jtl","sha256":"abc","size":1024,"valid_rows":17,"invalid_rows":0}`,
		"steps":    `{"sample_kind":"http","sample_count":17,"failure_count":2,"p95_ms":25.5}`,
		"timeline": `{"timestamp_ms":1700000000000,"width_ms":1000,"sample_count":17,"rps":17,"p95_ms":25.5}`,
		"issues":   `{"source_kind":"log","source_name":"jmeter.log","category":"engine","level":"WARN","message":"历史证据行","first_at":1700000000000,"line_from":2,"line_to":3}`,
		"metrics":  `{"metric_key":null,"key":"rps","origin":"jtl","schema_version":"1"}`,
		"findings": `{"origin":"rules","level":"info","title":"样本结果"}`,
	}
	ids := map[string]string{}
	for kind, raw := range children {
		ids[kind] = newID("evidence-")
		if _, err = p.DB.Exec("INSERT INTO pressure_report_"+kind+"(id,project_id,report_id,created_at,position,data) VALUES(?,?,?,?,?,?)", ids[kind], defaultProjectID, id, now(), 0, raw); err != nil {
			t.Fatal(err)
		}
	}
	upgraded, err := newPlatform(p.Config)
	if err != nil {
		t.Fatal("full legacy projection upgrade failed", err)
	}
	defer upgraded.Close()
	var value string
	if err = upgraded.DB.QueryRow("SELECT verdict_status FROM pressure_reports WHERE id=?", id).Scan(&value); err != nil || value != "PASS" {
		t.Fatalf("null legacy verdict did not fall back to nested result: %q %v", value, err)
	}
	for kind, raw := range children {
		if err = upgraded.DB.QueryRow("SELECT data FROM pressure_report_"+kind+" WHERE id=?", ids[kind]).Scan(&value); err != nil || value != raw {
			t.Fatalf("legacy %s JSON changed: %v", kind, err)
		}
	}
	for _, tc := range []struct{ table, column, id, want string }{
		{"sources", "object_key", ids["sources"], "originals/raw/results.jtl"},
		{"metrics", "metric_key", ids["metrics"], "rps"},
		{"issues", "message", ids["issues"], "历史证据行"},
		{"findings", "title", ids["findings"], "样本结果"},
	} {
		if err = upgraded.DB.QueryRow("SELECT "+tc.column+" FROM pressure_report_"+tc.table+" WHERE id=?", tc.id).Scan(&value); err != nil || value != tc.want {
			t.Fatalf("legacy %s.%s backfill: %q %v", tc.table, tc.column, value, err)
		}
	}
	for table := range drops {
		rows, e := upgraded.DB.Query("SELECT " + strings.Join(tableColumns[table], ",") + " FROM " + table + " LIMIT 0")
		if e != nil {
			t.Fatalf("missing projection in %s: %v", table, e)
		}
		rows.Close()
	}
}

func TestMySQLRestoreFixtureCleansSchemaHistory(t *testing.T) {
	source, _ := mysqlFixture(t)
	var prefix string
	t.Run("temporary destination", func(t *testing.T) {
		destination := emptyWorkspacePlatform(t, source, t.TempDir())
		prefix = destination.DB.Prefix
	})
	var count int
	if err := source.DB.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?", prefix+"schema_migrations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("restore fixture left schema history after cleanup: count=%d err=%v", count, err)
	}
}
