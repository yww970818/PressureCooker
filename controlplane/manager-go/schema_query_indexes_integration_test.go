package main

import (
	"context"
	"testing"

	"pressurecooker/manager/internal/schema"
)

func assertDirectoryQueryIndexes(t *testing.T, db *Database) {
	t.Helper()
	for _, index := range []struct{ table, name, columns string }{
		{"workflow_datasets", "dataset_project_time", "project_id,deleted,created_at,id"},
		{"workflow_datasets", "dataset_workflow_time", "project_id,workflow_id,deleted,created_at,id"},
		{"pressure_reports", "report_project_time", "project_id,created_at,version,id"},
	} {
		var columns string
		err := db.QueryRow("SELECT COALESCE(GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ','),'') FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", db.Prefix+index.table, index.name).Scan(&columns)
		if err != nil || columns != index.columns {
			t.Fatalf("%s.%s columns=%q, want %q: %v", index.table, index.name, columns, index.columns, err)
		}
	}
}

// A fresh startup and a v6 upgrade with one already-created index must reach
// the same physical indexes without rewriting any authored JSON or history.
func TestMySQLDirectoryQueryIndexesUpgradeAndResume(t *testing.T) {
	p, _ := mysqlFixture(t)
	assertDirectoryQueryIndexes(t, p.DB)
	catalog, err := schema.Catalog()
	if err != nil || len(catalog) < 7 {
		t.Fatal("missing query index migration", err)
	}
	prefix := p.DB.Prefix + "upgrade_"
	t.Cleanup(func() {
		for _, table := range append(append(append([]string{}, portableTables...), derivedTables...), "sessions", "instances", "schema_migrations") {
			if _, err := p.DB.DB.Exec("DROP TABLE IF EXISTS `" + prefix + table + "`"); err != nil {
				t.Error("isolated upgrade table cleanup", err)
			}
		}
	})
	ctx := context.Background()
	if err = schema.Run(ctx, p.DB.DB, prefix, catalog[:6]); err != nil {
		t.Fatal(err)
	}
	db := &Database{DB: p.DB.DB, Prefix: prefix}
	checksums := []string{}
	for version := 1; version <= 6; version++ {
		var checksum string
		if err = db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=?", version).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		checksums = append(checksums, checksum)
	}
	const datasetJSON = `{"id":"dataset-before","name":"untouched CSV metadata"}`
	const reportJSON = `{"id":"pressure-before","status":"READY","metrics":{"sample_count":7}}`
	if _, err = db.Exec("INSERT INTO workflow_datasets(id,project_id,workflow_id,node_id,created_at,data) VALUES(?,?,?,?,?,?)", "dataset-before", defaultProjectID, "workflow-before", "csv-before", "2026-11-09T00:00:00.000Z", datasetJSON); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,1,'READY',?,?,?)", "pressure-before", defaultProjectID, "run-before", "2026-11-09T00:00:00.000Z", digest([]byte("pressure-before")), reportJSON); err != nil {
		t.Fatal(err)
	}
	// Simulate a process stopping after the first DDL committed. The migration
	// must adopt that physical index rather than fail or create it twice.
	if _, err = db.Exec("CREATE INDEX dataset_project_time ON workflow_datasets(project_id,deleted,created_at,id)"); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err = schema.Apply(ctx, p.DB.DB, prefix); err != nil {
			t.Fatal(err)
		}
		assertDirectoryQueryIndexes(t, db)
		for version, want := range checksums {
			var checksum string
			if err = db.QueryRow("SELECT checksum FROM schema_migrations WHERE version=? AND state='APPLIED'", version+1).Scan(&checksum); err != nil || checksum != want {
				t.Fatalf("migration %d history changed: %v", version+1, err)
			}
		}
		for _, row := range []struct{ table, id, want string }{{"workflow_datasets", "dataset-before", datasetJSON}, {"pressure_reports", "pressure-before", reportJSON}} {
			var raw string
			if err = db.QueryRow("SELECT data FROM "+row.table+" WHERE id=?", row.id).Scan(&raw); err != nil || raw != row.want {
				t.Fatalf("%s JSON changed after upgrade attempt %d: %v", row.table, attempt+1, err)
			}
		}
		var count int
		if err = db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE state='APPLIED'").Scan(&count); err != nil || count != len(catalog) {
			t.Fatalf("applied migration count=%d, want %d: %v", count, len(catalog), err)
		}
	}
}
