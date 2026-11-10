package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"pressurecooker/execution/protocol"
)

func TestMySQLPlanArchiveLibraryFiltersAndPreservesHistory(t *testing.T) {
	p, first := mysqlFixture(t)
	request, call := workspaceHTTP(t, p)
	second, err := p.createPlan(Obj{"name": first["name"], "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	temporary, err := p.createPlan(Obj{"name": "临时复验", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	pid := str(temporary["id"])
	run := runFixture(t, p, temporary, "archive-history")
	rid := str(run["id"])
	started := now()
	if _, err = p.updateRun(rid, Obj{"status": "RUNNING", "controller_id": "controller-a", "started_at": started}); err != nil {
		t.Fatal(err)
	}
	store, err := p.objectStore()
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := rawPrefix(Obj{"id": rid, "started_at": started})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), prefix+"results.jtl", strings.NewReader("exact-jtl"), 9, digest([]byte("exact-jtl")), "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	callback := protocol.Callback{EventID: newID("event-"), TaskID: rid, AttemptID: str(run["attempt_id"]), RunnerID: "controller-a", Status: "COMPLETED", StartedAt: started, FinishedAt: now(), Artifacts: []protocol.ObjectRef{ref}, MissingFiles: []string{"jmeter.log", "console.log"}}
	if _, err = p.runnerCallback(context.Background(), "controller-a", dtoObject(callback)); err != nil {
		t.Fatal(err)
	}
	var planBefore, summaryBefore, runBefore, dispatchBefore string
	if err = p.DB.QueryRow("SELECT data,summary_payload FROM plans WHERE id=?", pid).Scan(&planBefore, &summaryBefore); err != nil {
		t.Fatal(err)
	}
	if err = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", rid).Scan(&runBefore); err != nil {
		t.Fatal(err)
	}
	if err = p.DB.QueryRow("SELECT payload FROM task_dispatches WHERE id=?", rid).Scan(&dispatchBefore); err != nil {
		t.Fatal(err)
	}
	archive := "/api/plans/" + pid + "/archive"
	for i := 0; i < 2; i++ {
		code, value := call("POST", archive, Obj{"archived": true})
		if code != 200 || value["archived"] != true || value["project_id"] != defaultProjectID {
			t.Fatalf("archive: %d %s", code, encoded(value))
		}
	}
	for _, view := range []string{"", "?view=summary"} {
		code, value := call("GET", "/api/plans"+view, nil)
		if code != 200 || number(value["total"]) != 2 || len(arr(value["items"])) != 2 {
			t.Fatalf("archive list %s: %d %s", view, code, encoded(value))
		}
		items := arr(value["items"])
		if obj(items[0])["id"] != second["id"] || obj(items[1])["id"] != first["id"] {
			t.Fatal("canonical same-name versions disappeared", value)
		}
		separator := "?"
		if view != "" {
			separator = "&"
		}
		code, value = call("GET", "/api/plans"+view+separator+"include_archived=true&limit=1&offset=0", nil)
		if code != 200 || number(value["total"]) != 3 || len(arr(value["items"])) != 1 || obj(arr(value["items"])[0])["id"] != pid || obj(arr(value["items"])[0])["archived"] != true {
			t.Fatal("include/pagination", code, value)
		}
		code, value = call("GET", "/api/plans"+view+separator+"limit=1&offset=1", nil)
		if code != 200 || number(value["total"]) != 2 || len(arr(value["items"])) != 1 || obj(arr(value["items"])[0])["id"] != first["id"] {
			t.Fatal("filtered count/pagination", code, value)
		}
	}
	code, value := call("GET", "/api/plans/"+pid, nil)
	if code != 200 || string(encoded(value)) != string(encoded(clone(temporary))) {
		t.Fatalf("exact metadata changed %d\ngot %s\nwant %s", code, encoded(value), encoded(clone(temporary)))
	}
	for _, v := range []struct{ path, content string }{{"/api/plans/" + pid + "/content", "<jmeterTestPlan/>"}, {"/api/runs/" + rid + "/artifacts/results.jtl", "exact-jtl"}} {
		code, _, raw := request("GET", v.path, nil, map[string]string{"Authorization": "Bearer admin-secret"})
		if code != 200 || string(raw) != v.content {
			t.Fatal("original unavailable", v.path, code, string(raw))
		}
	}
	if code, value = call("GET", "/api/runs/"+rid, nil); code != 200 || value["plan_id"] != pid {
		t.Fatal("history unavailable", code, value)
	}
	if code, value = call("GET", "/api/runs/"+rid+"/copy-config", nil); code != 200 || value["plan_id"] != pid {
		t.Fatal("copy lost exact archived reference", code, value)
	}
	copied, created, err := p.createRun(Obj{"name": "copy archived history", "plan_id": pid, "cluster_id": run["cluster_id"], "environment_id": run["environment_id"], "copy_source_run_id": rid}, "copy-archived-history")
	if err != nil || !created || copied["plan_id"] != pid || copied["plan_sha256"] != temporary["sha256"] {
		t.Fatal("archived exact plan cannot be reused", copied, created, err)
	}
	var planAfter, summaryAfter, runAfter, dispatchAfter string
	if err = p.DB.QueryRow("SELECT data,summary_payload FROM plans WHERE id=?", pid).Scan(&planAfter, &summaryAfter); err != nil {
		t.Fatal(err)
	}
	if err = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", rid).Scan(&runAfter); err != nil {
		t.Fatal(err)
	}
	if err = p.DB.QueryRow("SELECT payload FROM task_dispatches WHERE id=?", rid).Scan(&dispatchAfter); err != nil {
		t.Fatal(err)
	}
	if planBefore != planAfter || summaryBefore != summaryAfter || runBefore != runAfter || dispatchBefore != dispatchAfter {
		t.Fatal("archive rewrote immutable payload or history")
	}
	var count int
	if err = p.DB.QueryRow("SELECT COUNT(*) FROM audit WHERE action='plan.archive' AND object_id=?", pid).Scan(&count); err != nil || count != 1 {
		t.Fatal("archive audit is not transactional/idempotent", count, err)
	}
	for i := 0; i < 2; i++ {
		if code, value = call("POST", archive, Obj{"archived": false}); code != 200 || value["archived"] != false {
			t.Fatal("restore", code, value)
		}
	}
	if code, value = call("GET", "/api/plans?view=summary", nil); code != 200 || number(value["total"]) != 3 {
		t.Fatal("restore absent", code, value)
	}
	if err = p.DB.QueryRow("SELECT COUNT(*) FROM audit WHERE action='plan.restore' AND object_id=?", pid).Scan(&count); err != nil || count != 1 {
		t.Fatal("restore audit", count, err)
	}
}

func TestMySQLPlanArchiveValidationProjectAndCSRF(t *testing.T) {
	p, plan := mysqlFixture(t)
	request, call := workspaceHTTP(t, p)
	path := "/api/plans/" + str(plan["id"]) + "/archive"
	for _, body := range []Obj{{}, {"archived": nil}, {"archived": "true"}, {"archived": 1}} {
		if code, value := call("POST", path, body); code != 400 {
			t.Fatal("non-boolean accepted", code, value)
		}
	}
	for _, query := range []string{"include_archived=1", "include_archived=", "include_archived=true&include_archived=false", "limit=0", "offset=-1"} {
		for _, view := range []string{"", "&view=summary"} {
			if code, value := call("GET", "/api/plans?"+query+view, nil); code != 400 {
				t.Fatal("invalid list option", query, code, value)
			}
		}
	}
	if code, value := call("POST", "/api/plans/missing/archive", Obj{"archived": true}); code != 404 {
		t.Fatal("missing accepted", code, value)
	}
	project, err := p.projectCreate(Obj{"name": "other"})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])}
	if code, _, raw := request("POST", path, Obj{"archived": true}, headers); code != 404 {
		t.Fatal("cross-project archive", code, string(raw))
	}
	for _, view := range []string{"", "&view=summary"} {
		code, _, raw := request("GET", "/api/plans?include_archived=true"+view, nil, headers)
		value, _ := decode(raw)
		if code != 200 || number(value["total"]) != 0 {
			t.Fatal("cross-project list", code, string(raw))
		}
	}
	if code, _, _ := request("POST", path, Obj{"archived": true}, nil); code != 401 {
		t.Fatal("anonymous mutation", code)
	}
	sid, login, err := p.login("admin-secret")
	if err != nil {
		t.Fatal(err)
	}
	session := map[string]string{"Cookie": "jmeter_session=" + sid}
	if code, _, _ := request("POST", path, Obj{"archived": true}, session); code != http.StatusForbidden {
		t.Fatal("CSRF bypass", code)
	}
	session["X-CSRF-Token"] = str(login["csrf_token"])
	if code, _, raw := request("POST", path, Obj{"archived": true}, session); code != 200 {
		t.Fatal("CSRF mutation", code, string(raw))
	}
	if _, err = p.projectUpdate(defaultProjectID, Obj{"revision": 1}, true); err != nil {
		t.Fatal(err)
	}
	if code, value := call("POST", path, Obj{"archived": false}); code != 409 {
		t.Fatal("archived project mutation", code, value)
	}
}

func TestMySQLPlanArchiveBackupAndLegacyImport(t *testing.T) {
	p, plan := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	if code, value := call("POST", "/api/plans/"+str(plan["id"])+"/archive", Obj{"archived": true}); code != 200 {
		t.Fatal(code, value)
	}
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	if len(arr(state["plan_library_state"])) != 1 {
		t.Fatal("backup omitted library state")
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "restored"))
	if err = restored.importState(clone(state)); err != nil {
		t.Fatal(err)
	}
	_, restoredCall := workspaceHTTP(t, restored)
	if code, value := restoredCall("GET", "/api/plans?view=summary", nil); code != 200 || number(value["total"]) != 0 {
		t.Fatal("archive lost in restore", code, value)
	}
	legacy := clone(state)
	delete(legacy, "plan_library_state")
	old := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "legacy"))
	if err = old.importState(legacy); err != nil {
		t.Fatal("legacy backup rejected", err)
	}
	_, oldCall := workspaceHTTP(t, old)
	if code, value := oldCall("GET", "/api/plans?view=summary", nil); code != 200 || number(value["total"]) != 1 {
		t.Fatal("legacy plans hidden", code, value)
	}
	forged := clone(state)
	obj(arr(forged["plan_library_state"])[0])["project_id"] = "project-missing"
	bad := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "bad"))
	if err = bad.importState(forged); err == nil {
		t.Fatal("import accepted cross-project archive metadata")
	}
}
