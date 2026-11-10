package main

import (
	"context"
	"strings"
	"testing"
)

func TestMonitoringExplicitConversionPreservesHistoryAndReplay(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	written, err := p.writePressureMonitoring(id, "convert-fixture", monitoringRequest())
	if err != nil {
		t.Fatal(err)
	}
	full, err := monitoringReadQ(p.DB, p.projectID(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	delete(full, "write_result")
	raw := string(encoded(full))
	var key, request string
	if err = p.DB.QueryRow("SELECT key_hash,request_hash FROM monitoring_revisions WHERE report_id=?", id).Scan(&key, &request); err != nil {
		t.Fatal(err)
	}
	for _, table := range monitoringTables {
		if _, err = p.DB.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.DB.Exec("INSERT INTO pressure_report_metrics(id,project_id,report_id,created_at,kind,position,metric_key,label,data,origin,schema_version) VALUES(?,?,?,'2026-11-10T00:00:00Z','monitoring_snapshot',1,?,?,?,'external','1')", "legacy-monitoring", p.projectID(), id, key, request, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pressureMonitoring(id, 0); err == nil || !strings.Contains(err.Error(), "convert-monitoring") {
		t.Fatal("implicit history conversion", err)
	}
	p.DB.SetMaxOpenConns(1)
	result, err := p.convertMonitoring()
	if err != nil || number(result["converted"]) != 1 {
		t.Fatal(result, err)
	}
	result, err = p.convertMonitoring()
	if err != nil || number(result["converted"]) != 0 {
		t.Fatal("conversion not resumable", result, err)
	}
	var after, source string
	if err = p.DB.QueryRow("SELECT data FROM pressure_report_metrics WHERE id='legacy-monitoring'").Scan(&after); err != nil || after != raw {
		t.Fatal("historical bytes changed", err)
	}
	if err = p.DB.QueryRow("SELECT source_hash FROM monitoring_revisions WHERE report_id=?", id).Scan(&source); err != nil || source != digest([]byte(raw)) {
		t.Fatal("source fingerprint changed", err)
	}
	restored, err := monitoringReadQ(p.DB, p.projectID(), id, 1)
	if err != nil || string(encoded(restored)) != raw {
		t.Fatal("reconstructed revision changed", err)
	}
	replay, err := p.writePressureMonitoring(id, "convert-fixture", monitoringRequest())
	if err != nil || string(encoded(replay)) != string(encoded(written)) {
		t.Fatal("historical replay changed", err)
	}
	other, err := p.projectCreate(Obj{"name": "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.forProject(str(other["id"])).pressureMonitoring(id, 1); err == nil {
		t.Fatal("cross-project revision exposed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.forRequest(ctx, p.projectID()).convertMonitoring(); err == nil {
		t.Fatal("conversion ignored cancellation")
	}
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	row := obj(arr(state["monitoring_revisions"])[0])
	row["source_hash"] = strings.Repeat("0", 64)
	dest := emptyWorkspacePlatform(t, p, t.TempDir())
	if err = dest.importState(state); err == nil {
		t.Fatal("mismatched conversion source fingerprint imported")
	}
	if _, err = p.DB.Exec("UPDATE pressure_report_metrics SET data=? WHERE id='legacy-monitoring'", string(encoded(Obj{"revision": 1}))); err != nil {
		t.Fatal(err)
	}
	if _, err = p.convertMonitoring(); err == nil {
		t.Fatal("changed source accepted")
	}
}

func TestMonitoringRestoreRejectsBrokenEvidenceAndMemberships(t *testing.T) {
	p, _, report := monitoringFixture(t)
	if _, err := p.writePressureMonitoring(str(report["id"]), "restore-integrity", monitoringRequest()); err != nil {
		t.Fatal(err)
	}
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(Obj)
	}{
		{"summary", func(s Obj) {
			row := obj(arr(s["monitoring_revisions"])[0])
			metadata, _ := decode([]byte(str(row["data"])))
			obj(metadata["monitoring_summary"])["instance_count"] = 999
			row["data"] = string(encoded(metadata))
		}},
		{"receipt", func(s Obj) {
			row := obj(arr(s["monitoring_revisions"])[0])
			metadata, _ := decode([]byte(str(row["data"])))
			obj(metadata["write_result"])["report_id"] = "wrong-report"
			row["data"] = string(encoded(metadata))
		}},
		{"request_hash", func(s Obj) { obj(arr(s["monitoring_revisions"])[0])["request_hash"] = "invalid" }},
		{"hash", func(s Obj) { obj(arr(s["monitoring_evidence"])[0])["sha256"] = strings.Repeat("0", 64) }},
		{"membership", func(s Obj) { obj(arr(s["monitoring_memberships"])[0])["evidence_id"] = "missing-evidence" }},
		{"revision", func(s Obj) {
			row := obj(arr(s["monitoring_revisions"])[0])
			metadata, _ := decode([]byte(str(row["data"])))
			metadata["project_id"] = "wrong-project"
			row["data"] = string(encoded(metadata))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := clone(state)
			test.mutate(copy)
			dest := emptyWorkspacePlatform(t, p, t.TempDir())
			if err := dest.importState(copy); err == nil {
				t.Fatal("corrupt normalized monitoring imported")
			}
		})
	}
}
