package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Tests without an HTTP request use the same context-aware production reader.
func (p *Platform) listRuns(status, search string, limit, offset int) (Obj, error) {
	return p.listRunsContext(context.Background(), status, search, limit, offset)
}

// Persist real history shapes without dispatching a task or starting a scheduler.
func runListFixture(t *testing.T, p *Platform, id, name, status, created string, notes any, redis bool) Obj {
	t.Helper()
	run := Obj{"id": id, "project_id": p.projectID(), "name": name, "notes": notes, "status": status, "created_at": created, "workers": []any{"worker-1"}, "properties": Obj{"cookie": "private-list-cookie", "threads": "8"}, "cluster_snapshot": Obj{"id": "cluster-history", "name": "冻结集群", "controller_id": "controller-a", "callback_url": "http://fixture.invalid/callback?token=private-callback"}}
	if redis {
		run["execution_mode"] = "redis"
	}
	if err := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); err != nil {
		t.Fatal(err)
	}
	if redis {
		if _, err := p.DB.Exec("INSERT INTO task_dispatches(id,project_id,status,created_at,payload,last_error) VALUES(?,?,?,?,?,?)", id, p.projectID(), "PUBLISHED", created, "unused-test-payload", ""); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

func listRunIDs(t *testing.T, p *Platform, status, search string, limit, offset, total int, want ...string) {
	t.Helper()
	page, err := p.listRuns(status, search, limit, offset)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, item := range arr(page["items"]) {
		got = append(got, str(obj(item)["id"]))
	}
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) || int(number(page["total"])) != total || page["project_id"] != p.projectID() {
		t.Fatalf("status=%q search=%q limit=%d offset=%d: ids=%v total=%v project=%v, want ids=%v total=%d project=%s", status, search, limit, offset, got, page["total"], page["project_id"], want, total, p.projectID())
	}
	if _, ok := page["items"].([]any); !ok {
		t.Fatal("items must remain a JSON array, including empty pages")
	}
}

func TestMySQLRunListFiltersBeforePaginationAndOrdersTies(t *testing.T) {
	p, _ := mysqlFixture(t)
	runListFixture(t, p, "run-a", "Alpha", "COMPLETED", "2026-11-06T01:00:00.000Z", "first", false)
	runListFixture(t, p, "run-b", "BETA", "COMPLETED", "2026-11-06T01:00:00.000Z", "Alpha in notes", true)
	runListFixture(t, p, "run-c", "Gamma", "RUNNING", "2026-11-06T02:00:00.000Z", "", true)
	project, err := p.projectCreate(Obj{"name": "另一个项目"})
	if err != nil {
		t.Fatal(err)
	}
	other := p.forProject(str(project["id"]))
	runListFixture(t, other, "run-foreign", "Alpha", "COMPLETED", "2026-11-06T03:00:00.000Z", "", true)
	listRunIDs(t, p, "", "", 1, 0, 3, "run-c") // An unscoped platform still lists only the default project.
	listRunIDs(t, p, "", "", 1, 1, 3, "run-b")
	listRunIDs(t, p, "", "", 1, 2, 3, "run-a")
	listRunIDs(t, p, "", "", 20, 3, 3)
	listRunIDs(t, p, "COMPLETED", "aLpHa", 1, 1, 2, "run-a")
	listRunIDs(t, p, "RUNNING", "alpha", 20, 0, 0)
	listRunIDs(t, p, "running", "", 20, 0, 0)
	listRunIDs(t, p, "UNKNOWN", "", 20, 0, 0)
	listRunIDs(t, other, "COMPLETED", "ALPHA", 20, 0, 1, "run-foreign")
}

func TestMySQLRunListSearchUsesLiteralCaseInsensitiveSubstrings(t *testing.T) {
	p, _ := mysqlFixture(t)
	rows := []struct {
		id, name string
		notes    any
	}{
		{"run-percent", "100% Ready", nil},
		{"run-underscore", "name_with_underscore", ""},
		{"run-slash", `C:\fixture`, ""},
		{"run-accent", "Éclair", "中文备注"},
		{"run-deseret", "𐐀", "supplementary Unicode"},
		{"run-plain", "eclair", ""},
		{"run-injection", "' OR 1=1 --", ""},
		{"run-legacy", "legacy", Obj{"old": "metadata"}},
	}
	for _, row := range rows {
		runListFixture(t, p, row.id, row.name, "COMPLETED", "2026-11-06T01:00:00.000Z", row.notes, false)
	}
	for _, tc := range []struct{ search, id string }{{"%", "run-percent"}, {"_", "run-underscore"}, {`\`, "run-slash"}, {"ÉCLAIR", "run-accent"}, {"ECLAIR", "run-plain"}, {"中文", "run-accent"}, {"𐐀", "run-deseret"}, {"𐐨", "run-deseret"}, {"' OR 1=1 --", "run-injection"}, {"RUN-PERCENT 100%", "run-percent"}} {
		t.Run(tc.search, func(t *testing.T) { listRunIDs(t, p, "", tc.search, 20, 0, 1, tc.id) })
	}
	listRunIDs(t, p, "", "null", 20, 0, 0)
	listRunIDs(t, p, "", "metadata", 20, 0, 0)
}

func TestMySQLRunListOnlyOpensSelectedPage(t *testing.T) {
	p, _ := mysqlFixture(t)
	runListFixture(t, p, "run-new", "new", "COMPLETED", "2026-11-06T02:00:00.000Z", "", true)
	runListFixture(t, p, "run-old", "old", "FAILED", "2026-11-06T01:00:00.000Z", "", true)
	if _, err := p.DB.Exec("UPDATE runs SET data=JSON_SET(data,'$.environment_envelope',?) WHERE id=?", "broken-ciphertext", "run-old"); err != nil {
		t.Fatal(err)
	}
	listRunIDs(t, p, "", "", 1, 0, 2, "run-new")
	listRunIDs(t, p, "COMPLETED", "", 20, 0, 1, "run-new")
	listRunIDs(t, p, "", "new", 20, 0, 1, "run-new")
	listRunIDs(t, p, "", "", 1, 2, 2)
	if _, err := p.listRuns("FAILED", "", 20, 0); err == nil {
		t.Fatal("selected corrupt history must still report its decryption failure")
	}
}

func TestMySQLRunListBatchesRelationsAndPreservesPublicDetail(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	p.DB.SetMaxIdleConns(1)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("run-page-%02d", i)
		run := runListFixture(t, p, id, "page", "COMPLETED", "2026-11-06T01:00:00.000Z", "", i%2 == 0)
		for j, status := range []string{"READY", "FAILED", "READY"} {
			report := Obj{"id": fmt.Sprintf("report-%02d-%d", i, j), "project_id": p.projectID(), "run_id": id, "status": status, "created_at": fmt.Sprintf("2026-11-06T01:00:0%d.000Z", j), "summary": Obj{"version": j}, "validation_pending": true}
			if _, err := p.DB.Exec("INSERT INTO reports(id,run_id,status,created_at,data,project_id) VALUES(?,?,?,?,?,?)", report["id"], id, status, report["created_at"], string(encoded(report)), p.projectID()); err != nil {
				t.Fatal(err)
			}
		}
		if str(run["execution_mode"]) != "redis" {
			if _, err := p.DB.Exec("INSERT INTO webhook_deliveries(event_id,run_id,target_url,payload,status,attempts,next_attempt_at,lease_token,lease_until,last_status_code,last_error,created_at) VALUES(?,?,?,?,?,0,?,'',0,0,?,?)", "event-"+id, id, "http://fixture.invalid", "{}", "PENDING", epoch()+30, "unsafe receiver detail", now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	questions := func() int {
		t.Helper()
		var name string
		var n int
		if err := p.DB.QueryRow("SHOW SESSION STATUS LIKE 'Questions'").Scan(&name, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := questions()
	if _, err := p.listRuns("", "", 1, 0); err != nil {
		t.Fatal(err)
	}
	one := questions() - before
	before = questions()
	page, err := p.listRuns("", "", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	many := questions() - before
	if many > one+2 || many > 12 {
		t.Fatalf("list SQL grows with page size: one task=%d statements, twelve tasks=%d", one, many)
	}
	if len(arr(page["items"])) != 12 {
		t.Fatal("missing page tasks")
	}
	for _, v := range arr(page["items"]) {
		item := obj(v)
		detail, err := p.getRun(str(item["id"]))
		if err != nil || string(encoded(item)) != string(encoded(detail)) {
			t.Fatalf("list and detail projection differ for %s: %v", item["id"], err)
		}
		public := string(encoded(item))
		if strings.Contains(public, "private-") || strings.Contains(public, "validation_pending") || strings.Contains(public, "unsafe receiver detail") || obj(item["properties"])["threads"] != "8" || obj(item["properties"])["cookie"] != "***" || number(obj(item["summary"])["version"]) != 2 {
			t.Fatalf("public metadata or redaction changed for %s", item["id"])
		}
		if str(item["execution_mode"]) != "redis" && obj(item["webhook_delivery"])["last_error"] != "request_failed" {
			t.Fatal("unclassified legacy webhook error was not sanitized")
		}
	}
	t.Logf("list statements: one task=%d; twelve tasks=%d", one, many)
}

func TestMySQLRunListIndexesUpgradeAndRepeatedStartup(t *testing.T) {
	p, _ := mysqlFixture(t)
	indexes := map[string]string{"runs_project_time": "project_id,created_at,id", "runs_project_status_time": "project_id,status,created_at,id"}
	check := func() {
		t.Helper()
		for name, expected := range indexes {
			var got string
			if err := p.DB.QueryRow("SELECT COALESCE(GROUP_CONCAT(column_name ORDER BY seq_in_index),'') FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", p.DB.Prefix+"runs", name).Scan(&got); err != nil || got != expected {
				t.Fatalf("index %s: got %q, want %q (%v)", name, got, expected, err)
			}
		}
	}
	check()
	forgetSchemaHistory(t, p) // Simulate the old unversioned schema before index adoption.
	for name := range indexes {
		if _, err := p.DB.Exec("DROP INDEX " + name + " ON runs"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		restarted, err := newPlatform(p.Config)
		if err != nil {
			t.Fatal(err)
		}
		restarted.Close()
		check()
	}
}

func TestRunListRejectsInvalidPagination(t *testing.T) {
	for _, tc := range []struct{ limit, offset int }{{0, 0}, {201, 0}, {20, -1}} {
		if _, err := (&Platform{}).listRuns("", "", tc.limit, tc.offset); err == nil {
			t.Fatalf("accepted limit=%d offset=%d", tc.limit, tc.offset)
		}
	}
}
