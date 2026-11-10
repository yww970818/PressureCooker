package main

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestMySQLPageReadModelsKeepListsIndependentOfLargeBodies(t *testing.T) {
	p, _ := mysqlFixture(t)
	w, e := p.workspaceCreate("workflows", Obj{"name": "大流程摘要", "graph": basicWorkflowGraph()})
	if e != nil {
		t.Fatal(e)
	}
	id := str(w["id"])
	if _, e = p.DB.Exec("UPDATE workflows SET payload='intentionally-unreadable' WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	list, e := p.workspaceSummaryList("workflows", url.Values{"limit": {"1"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(list["items"])) != 1 || obj(arr(list["items"])[0])["name"] != "大流程摘要" || strings.Contains(string(encoded(list)), "graph") {
		t.Fatalf("invalid metadata list: %v", e)
	}
	versions, e := p.workspaceVersionSummaries("workflows", id, url.Values{})
	if e != nil || len(arr(versions["items"])) != 1 {
		t.Fatalf("versions: %v", e)
	}
	if _, e = p.workspaceGet("workflows", id); e == nil {
		t.Fatal("full detail silently replaced by summary")
	}
}

func TestMySQLReadModelBackfillIsResumableAndKeepsCiphertext(t *testing.T) {
	p, _ := mysqlFixture(t)
	w, e := p.workspaceCreate("workflows", Obj{"name": "回填", "graph": basicWorkflowGraph()})
	if e != nil {
		t.Fatal(e)
	}
	id := str(w["id"])
	var before string
	if e = p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", id).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("UPDATE workflows SET summary_payload=NULL WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e = p.rebuildPageReadModels(1); e != nil {
			t.Fatal(e)
		}
	}
	var after, summary string
	if e = p.DB.QueryRow("SELECT payload,summary_payload FROM workflows WHERE id=?", id).Scan(&after, &summary); e != nil {
		t.Fatal(e)
	}
	if after != before || summary == "" {
		t.Fatal("backfill changed authored ciphertext or missed summary")
	}
}

func TestMySQLReadModelBackfillsEmbeddedDashboardWithoutLosingLegacyFields(t *testing.T) {
	p, run, cb := pressureFixture(t)
	report := pressureComplete(t, p, run, cb)
	id := str(report["id"])
	if e := p.loadDashboardScopes(id, obj(report["dashboard"])); e != nil {
		t.Fatal(e)
	}
	d := obj(report["dashboard"])
	delete(d, "external_scopes")
	delete(d, "scope_count")
	first := obj(arr(d["scopes"])[0])
	first["future_extra_field"] = Obj{"preserve": true}
	if _, e := p.DB.Exec("UPDATE pressure_reports SET data=? WHERE id=?", string(encoded(report)), id); e != nil {
		t.Fatal(e)
	}
	if _, e := p.DB.Exec("DELETE FROM pressure_dashboard_scopes WHERE report_id=?", id); e != nil {
		t.Fatal(e)
	}
	if _, e := p.rebuildPageReadModels(1); e != nil {
		t.Fatal(e)
	}
	got, e := p.pressureAnalytics(id, httptest.NewRequest("GET", "/?series_limit=2", nil))
	if e != nil {
		t.Fatal(e)
	}
	if obj(obj(got["scope"])["future_extra_field"])["preserve"] != true {
		t.Fatal("legacy scope field lost")
	}
	full, e := p.pressureGet(id)
	if e != nil {
		t.Fatal(e)
	}
	if obj(arr(obj(full["dashboard"])["scopes"])[0])["future_extra_field"] == nil {
		t.Fatal("original embedded evidence removed")
	}
}

func TestMySQLRunSummaryDoesNotReadFullSnapshotsAndRelatedReports(t *testing.T) {
	p, _ := mysqlFixture(t)
	id := newID("run-")
	runListFixture(t, p, id, "大历史任务", "SUCCEEDED", now(), "notes", false)
	var reports []any
	for i := 0; i < 4; i++ {
		m := Obj{"id": newID("report-"), "project_id": p.projectID(), "run_id": id, "status": "READY", "summary": Obj{"sample_count": 1000000}, "legacy_details": strings.Repeat("large-body-", 100000)}
		reports = append(reports, m)
		if _, e := p.DB.Exec("INSERT INTO reports(id,project_id,run_id,status,created_at,data) VALUES(?,?,?,'READY',?,?)", m["id"], p.projectID(), id, now(), string(encoded(m))); e != nil {
			t.Fatal(e)
		}
	}
	full, e := p.listRuns("", "", 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(obj(arr(full["items"])[0])["reports"])) != 4 {
		t.Fatal("legacy full-list contract lost")
	}
	heads, e := p.listRunSummaries(context.Background(), "", "", 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(encoded(heads)) > 8192 || strings.Contains(string(encoded(heads)), "large-body") || obj(arr(heads["items"])[0])["properties"] != nil || obj(arr(heads["items"])[0])["reports"] != nil {
		t.Fatal("task row read heavy/private detail")
	}
}
func TestMySQLNormalizedDashboardImportRejectsIncompleteOrForeignScopes(t *testing.T) {
	p, run, cb := pressureFixture(t)
	pressureComplete(t, p, run, cb)
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"missing", "partial", "foreign"} {
		t.Run(kind, func(t *testing.T) {
			copy := clone(state)
			scopes := arr(copy["pressure_dashboard_scopes"])
			if len(scopes) < 2 {
				t.Fatal("insufficient scope fixture")
			}
			switch kind {
			case "missing":
				delete(copy, "pressure_dashboard_scopes")
			case "partial":
				copy["pressure_dashboard_scopes"] = scopes[1:]
			case "foreign":
				obj(scopes[0])["project_id"] = "project-other"
			}
			dest := emptyWorkspacePlatform(t, p, t.TempDir())
			if e := dest.importState(copy); e == nil {
				t.Fatal("accepted incomplete/cross-owner normalized report")
			}
		})
	}
}

type interleavedCSVRead struct {
	db    *Database
	after func()
	fired bool
}

func (q *interleavedCSVRead) advance() {
	if !q.fired {
		q.fired = true
		q.after()
	}
}
func (q *interleavedCSVRead) QueryRow(sql string, args ...any) *sql.Row {
	row := q.db.QueryRow(sql, args...)
	q.advance()
	return row
}
func (q *interleavedCSVRead) Query(sql string, args ...any) (*sql.Rows, error) {
	rows, e := q.db.Query(sql, args...)
	if e == nil {
		q.advance()
	}
	return rows, e
}
func TestMySQLCSVProjectionSnapshotSurvivesConcurrentSave(t *testing.T) {
	p, _ := mysqlFixture(t)
	graph := basicWorkflowGraph()
	csv := graphNode("csv-one", "CSVDataSet", nil)
	obj(obj(csv["properties"])["config"])["variableNames"] = "account"
	graph["nodes"] = append(arr(graph["nodes"]), csv)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "csv-one", 1))
	w, e := p.workspaceCreate("workflows", Obj{"name": "并发CSV", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	next := clone(graph)
	for _, n := range arr(next["nodes"]) {
		if obj(n)["id"] == "csv-one" {
			obj(n)["id"] = "csv-two"
		}
	}
	for _, n := range arr(next["edges"]) {
		if obj(n)["targetNodeId"] == "csv-one" {
			obj(n)["targetNodeId"] = "csv-two"
		}
	}
	q := &interleavedCSVRead{db: p.DB, after: func() {
		if _, e := p.workspaceUpdate("workflows", str(w["id"]), Obj{"name": "并发CSV", "revision": 1, "graph": next}); e != nil {
			t.Fatal(e)
		}
	}}
	snapshot, e := p.readWorkflowCSVSnapshot(q, str(w["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if snapshot.revision != 1 || len(snapshot.nodes) != 1 || snapshot.nodes[0].id != "csv-one" {
		t.Fatal("mixed header and node revisions", snapshot.revision, len(snapshot.nodes))
	}
}
