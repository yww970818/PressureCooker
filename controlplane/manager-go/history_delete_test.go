package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Removing the deletion transaction would leave these rows, allow stale saves,
// and admit mixed-project/active selections.
func historyDeleteRequest(t *testing.T, p *Platform, method, path string, body Obj, headers map[string]string) (int, Obj) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded(body)))
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("X-Project-ID", p.projectID())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, req)
	value, _ := decode(w.Body.Bytes())
	return w.Code, value
}
func syntheticHistoryRun(t *testing.T, p *Platform, status string) Obj {
	t.Helper()
	run := Obj{"id": newID("run-"), "project_id": p.projectID(), "name": "synthetic history", "status": status, "created_at": "2026-11-09T01:00:00Z", "started_at": "2026-11-09T01:00:00Z", "execution_mode": "redis", "properties": Obj{}}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	return run
}
func TestHistoryDeleteAPIAtomicScopeAndClear(t *testing.T) {
	p, _ := mysqlFixture(t)
	done := syntheticHistoryRun(t, p, "COMPLETED")
	active := syntheticHistoryRun(t, p, "RUNNING")
	foreign := syntheticHistoryRun(t, p.forProject("project-foreign"), "FAILED")
	for _, ids := range [][]any{{done["id"], active["id"]}, {done["id"], foreign["id"]}, {done["id"], "missing"}} {
		code, _ := historyDeleteRequest(t, p, "POST", "/api/runs/delete", Obj{"ids": ids}, nil)
		if code != 409 && code != 404 {
			t.Fatalf("invalid batch status=%d", code)
		}
		if _, e := p.get("runs", str(done["id"])); e != nil {
			t.Fatal("atomic batch deleted valid member", e)
		}
	}
	for _, body := range []Obj{{"ids": []any{}}, {"ids": []any{12}}, {"ids": []any{done["id"], done["id"]}}} {
		if code, _ := historyDeleteRequest(t, p, "POST", "/api/runs/delete", body, nil); code != 400 {
			t.Fatal("invalid ids accepted", code)
		}
	}
	ids := []any{}
	for i := 0; i < 101; i++ {
		ids = append(ids, newID("id-"))
	}
	if code, _ := historyDeleteRequest(t, p, "POST", "/api/runs/delete", Obj{"ids": ids}, nil); code != 400 {
		t.Fatal("oversize ids", code)
	}
	if code, _ := historyDeleteRequest(t, p, "POST", "/api/runs/clear", Obj{}, nil); code != 400 {
		t.Fatal("clear missing confirm", code)
	}
	code, result := historyDeleteRequest(t, p, "POST", "/api/runs/clear?search=no-match&status=FAILED&limit=1", Obj{"confirm": true}, nil)
	if code != 200 || number(result["deleted_count"]) != 1 || number(result["skipped_count"]) != 1 || number(result["cleanup_pending"]) != 1 {
		t.Fatalf("clear=%d %s", code, encoded(result))
	}
	if _, e := p.get("runs", str(active["id"])); e != nil {
		t.Fatal("active removed", e)
	}
	if _, e := p.get("runs", str(foreign["id"])); e != nil {
		t.Fatal("foreign removed", e)
	}
	if _, e := p.updateRun(str(done["id"]), Obj{"raw_archive": Obj{}}); e == nil {
		t.Fatal("stale update resurrected run")
	}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, done) }); e == nil {
		t.Fatal("stale upsert resurrected run")
	}
	code, _ = historyDeleteRequest(t, p, "DELETE", "/api/runs/"+str(active["id"]), nil, map[string]string{"Authorization": ""})
	if code != 401 {
		t.Fatal("unauthenticated delete", code)
	}
	project, e := p.projectGetQ(p.DB, p.projectID(), false)
	if e != nil {
		t.Fatal(e)
	}
	project["archived"] = true
	if _, e = p.DB.Exec("UPDATE projects SET data=? WHERE id=?", string(encoded(project)), p.projectID()); e != nil {
		t.Fatal(e)
	}
	if code, _ = historyDeleteRequest(t, p, "POST", "/api/runs/clear", Obj{"confirm": true}, nil); code != 409 {
		t.Fatal("archived delete", code)
	}
}

func TestHistoryDeleteReportLeaseAndRunCascade(t *testing.T) {
	p, run, cb := pressureFixture(t)
	ready := pressureComplete(t, p, run, cb)
	pending, e := p.generatePressure(str(run["id"]), "delete-processing", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	claim, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(claim, ok, e)
	}
	var originals int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&originals); e != nil {
		t.Fatal(e)
	}
	code, result := historyDeleteRequest(t, p, "DELETE", "/api/pressure-reports/"+str(pending["id"]), nil, nil)
	if code != 200 || number(result["deleted_count"]) != 1 || number(result["cleanup_pending"]) != 0 {
		t.Fatalf("report delete=%d %s", code, encoded(result))
	}
	p.workPressure(context.Background(), claim)
	p.failPressure(claim, "late write")
	if _, e = p.pressureGet(str(pending["id"])); e == nil {
		t.Fatal("worker resurrected report")
	}
	if _, e = p.pressureGet(str(ready["id"])); e != nil {
		t.Fatal("other report deleted", e)
	}
	if _, e = p.get("runs", str(run["id"])); e != nil {
		t.Fatal("report deletion removed task", e)
	}
	for _, table := range append(append([]string{}, pressureTables...), "pressure_dashboard_scopes") {
		var n int
		query := "SELECT COUNT(*) FROM " + table + " WHERE report_id=?"
		if table == "pressure_reports" || table == "pressure_report_jobs" {
			query = "SELECT COUNT(*) FROM " + table + " WHERE id=?"
		}
		if e = p.DB.QueryRow(query, pending["id"]).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	if _, e = p.DB.Exec("INSERT INTO reports(id,run_id,status,created_at,data,project_id) VALUES(?,?,'READY',?,'{}',?)", "legacy-history", run["id"], now(), p.projectID()); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("INSERT INTO task_dispatches(id,project_id,status,created_at,attempts,payload) VALUES(?,?,'PENDING',?,1,'{}')", run["id"], p.projectID(), now()); e != nil {
		t.Fatal(e)
	}
	for _, value := range []struct{ kind, id string }{{"run", str(run["id"])}, {"report", "legacy-history"}} {
		if _, e = p.DB.Exec("INSERT INTO idempotency(kind,`key`,digest,object_id) VALUES(?,?,?,?)", value.kind, newID("key-"), digest(nil), value.id); e != nil {
			t.Fatal(e)
		}
	}
	legacy := clone(run)
	legacy["cluster_snapshot"] = Obj{"callback_url": "https://fixture.invalid/callback"}
	if e = p.transaction(func(tx *Tx) error {
		_, err := tx.Exec("INSERT INTO webhook_deliveries(event_id,run_id,target_url,payload,status,attempts,next_attempt_at,lease_token,lease_until,last_status_code,last_error,created_at) VALUES(?,?,'','{}','PENDING',0,0,'',0,0,'',?)", newID("event-"), legacy["id"], now())
		return err
	}); e != nil {
		t.Fatal(e)
	}
	code, result = historyDeleteRequest(t, p, "POST", "/api/runs/delete", Obj{"ids": []any{run["id"]}}, nil)
	if code != 200 || number(result["deleted_count"]) != 1 {
		t.Fatalf("cascade=%d %s", code, encoded(result))
	}
	if state, e := p.finishDispatch(context.Background(), dispatchClaim{ID: str(run["id"]), Attempt: 1}, ""); e != nil || state != "" {
		t.Fatal("late dispatcher recreated intent", state, e)
	}
	for _, table := range append(append([]string{}, pressureTables...), "pressure_dashboard_scopes", "runs", "reports", "runner_events", "idempotency", "task_dispatches", "webhook_deliveries") {
		var n int
		if e = p.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}

func TestHistoryDeleteCleanupRetryAndPathProtection(t *testing.T) {
	p, _ := mysqlFixture(t)
	run := syntheticHistoryRun(t, p, "FAILED")
	dir := filepath.Join(p.Config.ResultsDir, str(run["id"]))
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "raw.txt"), []byte("history"), 0600); e != nil {
		t.Fatal(e)
	}
	shared := filepath.Join(p.Config.PlansDir, "keep.jmx")
	if e := os.WriteFile(shared, []byte("shared"), 0600); e != nil {
		t.Fatal(e)
	}
	var requests atomic.Int64
	var fail atomic.Bool
	fail.Store(true)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>2026-11-09/` + str(run["id"]) + `/raw/results.jtl</Key></Contents></ListBucketResult>`))
			return
		}
		w.WriteHeader(204)
	}))
	defer peer.Close()
	p.Config.ArchiveEndpoint = peer.URL
	code, _ := historyDeleteRequest(t, p, "DELETE", "/api/runs/"+str(run["id"]), nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if _, e := p.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	if e := p.cleanupHistory(context.Background()); e == nil {
		t.Fatal("storage failure not retried")
	}
	var n int
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups").Scan(&n); e != nil || n != 1 {
		t.Fatal("lost durable cleanup", n, e)
	}
	if _, e := p.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	fail.Store(false)
	// A response containing another task's key must never authorize its deletion.
	if e := p.cleanupHistory(context.Background()); e == nil {
		t.Fatal("out-of-prefix object accepted")
	}
	if requests.Load() == 0 {
		t.Fatal("cleanup never attempted object store")
	}
	if _, e := os.Stat(shared); e != nil {
		t.Fatal("shared plan removed", e)
	}
	// Corrupt portable intent stays pending and never traverses outside results.
	if _, e := p.DB.Exec("UPDATE artifact_cleanups SET data=?,next_attempt_at=0", string(encoded(Obj{"id": "../plans", "prefixes": []any{"plans/"}}))); e != nil {
		t.Fatal(e)
	}
	if e := p.cleanupHistory(context.Background()); e == nil {
		t.Fatal("unsafe cleanup accepted")
	}
	if _, e := os.Stat(shared); e != nil {
		t.Fatal("unsafe cleanup removed shared plan", e)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(state["artifact_cleanups"])) != 1 || len(arr(state["deleted_runs"])) != 1 {
		t.Fatal("cleanup/tombstone missing portable backup")
	}
	if strings.Contains(string(encoded(state["artifact_cleanups"])), p.Config.ArchiveSecretKey) {
		t.Fatal("cleanup contains secret")
	}
}

func TestHistoryDeleteReportPreviousProjectionAndClear(t *testing.T) {
	p, run, cb := pressureFixture(t)
	first := pressureComplete(t, p, run, cb)
	second, e := p.generatePressure(str(run["id"]), "previous-second", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	c, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(c, ok, e)
	}
	p.workPressure(context.Background(), c)
	third, e := p.generatePressure(str(run["id"]), "previous-third", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	before, e := p.pressureGet(str(third["id"]))
	if e != nil {
		t.Fatal(e)
	}
	code, _ := historyDeleteRequest(t, p, "POST", "/api/pressure-reports/delete", Obj{"ids": []any{second["id"]}}, nil)
	if code != 200 {
		t.Fatal(code)
	}
	after, e := p.pressureGet(str(third["id"]))
	if e != nil || string(encoded(before)) != string(encoded(after)) {
		t.Fatal("remaining report rewritten", e)
	}
	visible, e := p.pressureRead(str(third["id"]), false)
	if e != nil || visible["previous_ready_id"] != first["id"] {
		t.Fatal("dangling previous report", visible["previous_ready_id"], e)
	}
	list, e := p.pressureList(httptest.NewRequest("GET", "/api/pressure-reports", nil))
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range arr(list["items"]) {
		item := obj(v)
		if item["id"] == third["id"] && item["previous_ready_id"] != first["id"] {
			t.Fatal("list has dangling report", item["previous_ready_id"])
		}
	}
	code, result := historyDeleteRequest(t, p, "POST", "/api/pressure-reports/clear?status=FAILED&search=no-match&limit=1", Obj{"confirm": true}, nil)
	if code != 200 || number(result["deleted_count"]) != 2 || number(result["skipped_count"]) != 0 {
		t.Fatalf("report clear=%d %s", code, encoded(result))
	}
	if _, e = p.get("runs", str(run["id"])); e != nil {
		t.Fatal("report clear removed task", e)
	}
}

func TestHistoryDeleteCleanupSuccessAndLateProducerResweep(t *testing.T) {
	p, _ := mysqlFixture(t)
	run := syntheticHistoryRun(t, p, "COMPLETED")
	run["attempt_id"] = "attempt-history"
	run["cluster_snapshot"] = Obj{"controller_id": "controller-a"}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(p.Config.ResultsDir, str(run["id"]))
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(p.Config.PlansDir, "shared.jmx")
	if e := os.WriteFile(outside, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(directory, "untrusted-child")); e != nil {
		t.Fatal(e)
	}
	keys := map[string]bool{"2026-11-09/" + str(run["id"]) + "/raw/results.jtl": true, "task-inputs/" + p.projectID() + "/" + str(run["id"]) + "/worker-1/shard.csv": true}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			prefix := r.URL.Query().Get("prefix")
			xml := "<ListBucketResult><IsTruncated>false</IsTruncated>"
			for key := range keys {
				if strings.HasPrefix(key, prefix) {
					xml += "<Contents><Key>" + key + "</Key></Contents>"
				}
			}
			w.Write([]byte(xml + "</ListBucketResult>"))
			return
		}
		if r.Method == "DELETE" {
			delete(keys, strings.TrimPrefix(r.URL.Path, "/jmeter-reports/"))
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(405)
	}))
	defer peer.Close()
	p.Config.ArchiveEndpoint = peer.URL
	code, _ := historyDeleteRequest(t, p, "DELETE", "/api/runs/"+str(run["id"]), nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if _, e := p.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	if e := p.cleanupHistory(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(keys) != 0 {
		t.Fatal("objects not removed", keys)
	}
	if _, e := os.Stat(directory); !os.IsNotExist(e) {
		t.Fatal("task directory not removed", e)
	}
	if _, e := os.Stat(outside); e != nil {
		t.Fatal("followed child symlink", e)
	}
	var n int
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups").Scan(&n); e != nil || n != 0 {
		t.Fatal("cleanup not completed", n, e)
	}
	body := Obj{"task_id": run["id"], "attempt_id": "attempt-history", "status": "RUNNING"}
	result, e := p.runnerHeartbeat("controller-a", body)
	if e != nil || result["stop"] != true || result["deleted"] != true {
		t.Fatal("late heartbeat not acknowledged", result, e)
	}
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups").Scan(&n); e != nil || n != 1 {
		t.Fatal("late producer did not resweep", n, e)
	}
	if _, e = p.runnerCallback(context.Background(), "controller-b", body); e == nil {
		t.Fatal("wrong controller acknowledged")
	}
	body["attempt_id"] = "other-attempt"
	if _, e = p.runnerHeartbeat("controller-a", body); e == nil {
		t.Fatal("wrong attempt acknowledged")
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	// A new empty target restores deletion fences and pending physical cleanup.
	root, _ := filepath.EvalSymlinks(t.TempDir())
	config := p.Config
	config.MySQLTablePrefix = "jt_" + newID("")[:16] + "_"
	config.StateDir = filepath.Join(root, "state")
	config.PlansDir = filepath.Join(root, "plans")
	config.ResultsDir = filepath.Join(root, "results")
	restored, e := newPlatform(config)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	defer func() {
		for _, table := range append(append(append([]string{}, portableTables...), derivedTables...), "sessions", "instances", "schema_migrations") {
			if _, e := restored.DB.DB.Exec("DROP TABLE IF EXISTS " + config.MySQLTablePrefix + table); e != nil {
				t.Error(e)
			}
		}
	}()
	unsafeState := clone(state)
	unsafeRow := obj(arr(unsafeState["artifact_cleanups"])[0])
	unsafeData, _ := decode([]byte(str(unsafeRow["data"])))
	unsafeData["prefixes"] = []any{"plans/"}
	unsafeRow["data"] = string(encoded(unsafeData))
	if e = restored.importState(unsafeState); e == nil {
		t.Fatal("unsafe cleanup scope imported")
	}
	if e = restored.importState(state); e != nil {
		t.Fatal("portable history restore", e)
	}
	if e = restored.transaction(func(tx *Tx) error { return restored.saveRun(tx, run) }); e == nil {
		t.Fatal("restored tombstone did not fence stale save")
	}
	if e = restored.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups WHERE lease_token='' AND lease_until=0").Scan(&n); e != nil || n != 1 {
		t.Fatal("cleanup lease not reset after restore", n, e)
	}
}

func TestHistoryDeleteCookieCSRFAndArchivedReports(t *testing.T) {
	p, _ := mysqlFixture(t)
	run := syntheticHistoryRun(t, p, "STOPPED")
	sid, login, e := p.login("admin-secret")
	if e != nil {
		t.Fatal(e)
	}
	request := func(csrf string) int {
		req := httptest.NewRequest("DELETE", "/api/runs/"+str(run["id"]), nil)
		req.Header.Set("X-Project-ID", p.projectID())
		req.AddCookie(&http.Cookie{Name: "jmeter_session", Value: sid})
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, req)
		return w.Code
	}
	if status := request(""); status != 403 {
		t.Fatal("cookie mutation without CSRF", status)
	}
	if status := request(str(login["csrf_token"])); status != 200 {
		t.Fatal("cookie mutation with CSRF", status)
	}
	project, e := p.projectGetQ(p.DB, p.projectID(), false)
	if e != nil {
		t.Fatal(e)
	}
	project["archived"] = true
	if _, e = p.DB.Exec("UPDATE projects SET data=? WHERE id=?", string(encoded(project)), p.projectID()); e != nil {
		t.Fatal(e)
	}
	if code, _ := historyDeleteRequest(t, p, "POST", "/api/pressure-reports/clear", Obj{"confirm": true}, nil); code != 409 {
		t.Fatal("archived report clear", code)
	}
}

func TestHistoryDeleteDiscovery(t *testing.T) {
	paths := obj(openAPI()["paths"])
	for _, resource := range []string{"runs", "pressure-reports"} {
		for _, action := range []struct{ suffix, method string }{{"{id}", "delete"}, {"delete", "post"}, {"clear", "post"}} {
			operation := obj(obj(paths["/api/"+resource+"/"+action.suffix])[action.method])
			if len(operation) == 0 {
				t.Fatal("missing deletion discovery", resource, action)
			}
			schema := obj(obj(obj(obj(obj(operation["responses"])["200"])["content"])["application/json"])["schema"])
			for _, field := range []string{"deleted_count", "skipped_count", "cleanup_pending"} {
				if obj(obj(schema["properties"])[field])["type"] != "integer" {
					t.Fatal("missing deletion result", field)
				}
			}
		}
	}
}

func TestHistoryDeleteWaitsForArchiveUploadBeforeSweeping(t *testing.T) {
	p, _ := mysqlFixture(t)
	run := syntheticHistoryRun(t, p, "COMPLETED")
	run["execution_mode"] = "legacy"
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(p.Config.ResultsDir, str(run["id"]), "raw")
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(directory, "results.jtl"), []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	uploading := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var objects sync.Map
	var lists atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/jmeter-reports/")
		switch r.Method {
		case "HEAD":
			w.WriteHeader(404)
		case "PUT":
			close(uploading)
			<-release
			objects.Store(key, true)
			w.WriteHeader(200)
		case "GET":
			lists.Add(1)
			xml := "<ListBucketResult><IsTruncated>false</IsTruncated>"
			objects.Range(func(k, v any) bool {
				if strings.HasPrefix(k.(string), r.URL.Query().Get("prefix")) {
					xml += "<Contents><Key>" + k.(string) + "</Key></Contents>"
				}
				return true
			})
			w.Write([]byte(xml + "</ListBucketResult>"))
		case "DELETE":
			objects.Delete(key)
			w.WriteHeader(204)
		}
	}))
	defer peer.Close()
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveTimeout = time.Second
	archived := make(chan error, 1)
	go func() { archived <- p.archiveRun(str(run["id"])) }()
	select {
	case <-uploading:
	case <-time.After(3 * time.Second):
		t.Fatal("archive upload did not start")
	}
	code, _ := historyDeleteRequest(t, p, "DELETE", "/api/runs/"+str(run["id"]), nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if _, e := p.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	cleaned := make(chan error, 1)
	go func() { cleaned <- p.cleanupHistory(context.Background()) }()
	select {
	case e := <-cleaned:
		t.Fatal("cleanup passed live upload lock", e)
	case <-time.After(75 * time.Millisecond):
	}
	if lists.Load() != 0 {
		t.Fatal("cleanup listed before uploader finished")
	}
	once.Do(func() { close(release) })
	if e := <-archived; e == nil {
		t.Fatal("stale archive save accepted")
	}
	if e := <-cleaned; e != nil {
		t.Fatal(e)
	}
	objects.Range(func(k, v any) bool { t.Error("late upload object survived", k); return true })
	if _, e := p.get("runs", str(run["id"])); e == nil {
		t.Fatal("late archive save resurrected run")
	}
}

// A FIFO destination pauses real HTML copying after the run commit. This
// deterministically exposes deletion between run insertion and legacy report
// insertion without adding production test hooks or scheduling real tasks.
func TestHistoryDeleteImportCannotWriteAfterDeletion(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.Config.ArchiveEndpoint = ""
	id := newID("legacy-race-")
	source := t.TempDir()
	html := filepath.Join(source, id, "html")
	if e := os.MkdirAll(html, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source, id, "run.meta"), encoded(Obj{"status": "COMPLETED", "created_at": "2026-11-09T00:00:00Z"}), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(html, "index.html"), []byte("synthetic legacy HTML"), 0600); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(p.Config.ResultsDir, id, "legacy-html")
	if e := os.MkdirAll(target, 0700); e != nil {
		t.Fatal(e)
	}
	fifo := filepath.Join(target, "index.html")
	if e := syscall.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	imported := make(chan error, 1)
	go func() { _, e := p.importHistory(source); imported <- e }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var n int
		if e := p.DB.QueryRow("SELECT COUNT(*) FROM runs WHERE id=?", id).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("import did not commit its synthetic run")
		}
		time.Sleep(time.Millisecond)
	}
	locks := filepath.Join(p.Config.StateDir, "raw-archive-locks")
	if e := os.MkdirAll(locks, 0700); e != nil {
		t.Fatal(e)
	}
	lock, e := os.OpenFile(filepath.Join(locks, id+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	lockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if lockErr == nil {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	}
	lock.Close()
	code, _ := historyDeleteRequest(t, p, "DELETE", "/api/runs/"+id, nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	reader, e := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	select {
	case <-imported:
	case <-time.After(3 * time.Second):
		t.Fatal("paused import did not finish")
	}
	var orphan int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM reports WHERE run_id=?", id).Scan(&orphan); e != nil {
		t.Fatal(e)
	}
	if lockErr != syscall.EWOULDBLOCK && lockErr != syscall.EAGAIN {
		t.Error("import did not hold shared cleanup lock while copying")
	}
	if orphan != 0 {
		t.Error("deleted run gained an orphan legacy report", orphan)
	}
	if _, e = p.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	if e = p.cleanupHistory(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(p.Config.ResultsDir, id)); !os.IsNotExist(e) {
		t.Fatal("deleted import files survived cleanup", e)
	}
	if _, e = p.importHistory(source); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(p.Config.ResultsDir, id)); !os.IsNotExist(e) {
		t.Fatal("tombstoned import recreated files", e)
	}
}

func TestHistoryDeleteRemoteCleanupSurvivesMissingStoreAfterRestore(t *testing.T) {
	p, _ := mysqlFixture(t)
	run := syntheticHistoryRun(t, p, "FAILED")
	code, _ := historyDeleteRequest(t, p, "DELETE", "/api/runs/"+str(run["id"]), nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	// Old pending intents have no flag. Restore must interpret them conservatively.
	for _, table := range []string{"deleted_runs", "artifact_cleanups"} {
		row := obj(arr(state[table])[0])
		data, _ := decode([]byte(str(row["data"])))
		delete(data, "remote_required")
		row["data"] = string(encoded(data))
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	config := p.Config
	config.ArchiveEndpoint = ""
	config.MySQLTablePrefix = "jt_" + newID("")[:16] + "_"
	config.StateDir = filepath.Join(root, "state")
	config.PlansDir = filepath.Join(root, "plans")
	config.ResultsDir = filepath.Join(root, "results")
	restored, e := newPlatform(config)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	defer func() {
		for _, table := range append(append(append([]string{}, portableTables...), derivedTables...), "sessions", "instances", "schema_migrations") {
			if _, e := restored.DB.DB.Exec("DROP TABLE IF EXISTS " + config.MySQLTablePrefix + table); e != nil {
				t.Error(e)
			}
		}
	}()
	if e = restored.importState(state); e != nil {
		t.Fatal(e)
	}
	if e = restored.cleanupHistory(context.Background()); e == nil {
		t.Error("missing remote store completed restored remote cleanup")
	}
	var pending int
	if e = restored.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups").Scan(&pending); e != nil || pending != 1 {
		t.Fatal("restored remote cleanup intent lost", pending, e)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>"))
	}))
	defer peer.Close()
	restored.Config.ArchiveEndpoint = peer.URL
	if _, e = restored.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	if e = restored.cleanupHistory(context.Background()); e != nil {
		t.Fatal("restored remote work did not retry when store returned", e)
	}
	if e = restored.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups").Scan(&pending); e != nil || pending != 0 {
		t.Fatal("restored remote cleanup did not finish", pending, e)
	}
	// A locally created task with no configured store or remote execution/evidence
	// may finish local cleanup even while object storage is unavailable.
	local := p.forProject(p.projectID())
	local.Config.ArchiveEndpoint = ""
	localRun := syntheticHistoryRun(t, local, "STOPPED")
	localRun["execution_mode"] = "legacy"
	if e = local.transaction(func(tx *Tx) error { return local.saveRun(tx, localRun) }); e != nil {
		t.Fatal(e)
	}
	code, _ = historyDeleteRequest(t, local, "DELETE", "/api/runs/"+str(localRun["id"]), nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if _, e = local.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0 WHERE id=?", localRun["id"]); e != nil {
		t.Fatal(e)
	}
	// The other task is not due and must stay pending while local-only work succeeds.
	if e = local.cleanupHistory(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = local.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups WHERE id=?", localRun["id"]).Scan(&pending); e != nil || pending != 0 {
		t.Fatal("local-only cleanup blocked", pending, e)
	}
}

func TestHistoryDeleteDisabledArchiveRemainsLocalOnly(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.Config.ArchiveEndpoint = ""
	run := syntheticHistoryRun(t, p, "COMPLETED")
	run["execution_mode"] = "legacy"
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(p.Config.ResultsDir, str(run["id"]), "raw")
	if e := os.MkdirAll(directory, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(directory, "results.jtl"), []byte("synthetic local raw data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := p.archiveRun(str(run["id"])); e != nil {
		t.Fatal(e)
	}
	archived, e := p.get("runs", str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	receipt := obj(archived["raw_archive"])
	if receipt["status"] != "DISABLED" || obj(arr(receipt["files"])[0])["status"] != "DISABLED" || str(obj(arr(receipt["files"])[0])["object_key"]) == "" {
		t.Fatal("fixture must exercise real disabled archive placeholders", receipt)
	}
	code, _ := historyDeleteRequest(t, p, "DELETE", "/api/runs/"+str(run["id"]), nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	if _, e = p.DB.Exec("UPDATE artifact_cleanups SET next_attempt_at=0"); e != nil {
		t.Fatal(e)
	}
	if e = p.cleanupHistory(context.Background()); e != nil {
		t.Fatal("disabled archive incorrectly required remote cleanup", e)
	}
	var pending int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM artifact_cleanups WHERE id=?", run["id"]).Scan(&pending); e != nil || pending != 0 {
		t.Fatal("disabled archive cleanup stayed pending", pending, e)
	}
	if _, e = os.Stat(filepath.Dir(directory)); !os.IsNotExist(e) {
		t.Fatal("disabled archive local files survived cleanup", e)
	}
	// Ambiguous receipts and actual remote refs must still retain remote work.
	for _, file := range []Obj{
		{"status": "ARCHIVED", "object_key": "2026-11-09/" + str(run["id"]) + "/raw/results.jtl"},
		{"status": "DISABLED", "key": "2026-11-09/" + str(run["id"]) + "/raw/results.jtl", "bucket": "jmeter-reports"},
		{"object_key": "2026-11-09/" + str(run["id"]) + "/raw/results.jtl"},
	} {
		candidate := clone(run)
		candidate["raw_archive"] = Obj{"status": "DISABLED", "files": []any{file}}
		if historyCleanupIntent(candidate, str(run["id"]), p.projectID(), false)["remote_required"] != true {
			t.Fatal("remote evidence incorrectly treated as local", file)
		}
	}
}
