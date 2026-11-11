package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"pressurecooker/execution/protocol"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func runnerObjectPeer(t *testing.T) *httptest.Server {
	var mu sync.Mutex
	objects := map[string][]byte{}
	hashes := map[string]string{}
	types := map[string]string{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := r.URL.Path
		data, exists := objects[key]
		switch r.Method {
		case "PUT":
			if exists {
				w.WriteHeader(412)
				return
			}
			data, _ = io.ReadAll(r.Body)
			objects[key] = data
			hashes[key] = r.Header.Get("X-Amz-Meta-Sha256")
			types[key] = r.Header.Get("Content-Type")
			w.WriteHeader(200)
		case "HEAD", "GET":
			if !exists {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Content-Type", types[key])
			w.Header().Set("X-Amz-Meta-Sha256", hashes[key])
			if r.Method == "GET" {
				_, _ = w.Write(data)
			}
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(s.Close)
	return s
}
func newRunnerFixture(t *testing.T) (*Platform, Obj, Obj) {
	p, _ := mysqlFixture(t)
	peer := runnerObjectPeer(t)
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveBucket = "jmeter-reports"
	p.Config.ArchiveAccessKey = "test-access"
	p.Config.ArchiveSecretKey = "test-secret"
	p.Config.ArchiveTimeout = time.Second
	nodeRedisFixture(t, p)
	plan, e := p.createPlan(Obj{"name": "redis plan", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	environment, e := p.workspaceCreate("environments", Obj{"name": "test", "base_host": "http://fixture:8083", "variables": []any{Obj{"name": "token", "value": "private-token"}, Obj{"name": "custom", "value": "fromenv"}}})
	if e != nil {
		t.Fatal(e)
	}
	return p, plan, environment
}
func TestRunnerTaskSnapshotOutboxAndIdempotency(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "redis", "plan_id": plan["id"], "environment_id": environment["id"], "properties": Obj{"custom": "override"}, "timeout_seconds": 10}
	run, created, e := p.createRun(payload, "once")
	if e != nil || !created {
		t.Fatal(run, created, e)
	}
	id := str(run["id"])
	raw, e := p.get("runs", id)
	if e != nil {
		t.Fatal(e)
	}
	if str(raw["execution_mode"]) != "redis" || obj(raw["properties"])["base_host"] != "http://fixture:8083" || obj(raw["properties"])["custom"] != "override" || obj(raw["properties"])[environmentProperty("custom")] != "override" {
		t.Fatal("effective properties not frozen")
	}
	var stored, payloadCipher string
	if e = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if e = p.DB.QueryRow("SELECT payload FROM task_dispatches WHERE id=?", id).Scan(&payloadCipher); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(stored, "private-token") || strings.Contains(payloadCipher, "private-token") || !strings.HasPrefix(payloadCipher, "gcm1:") {
		t.Fatal("secret persisted in plaintext")
	}
	sealed, e := p.openWorkspace("task_dispatches", id, 1, payloadCipher)
	if e != nil {
		t.Fatal(e)
	}
	var signed protocol.SignedTask
	if e = decodeInto(sealed, &signed); e != nil || protocol.VerifyTask(signed, p.Config.AgentToken) != nil || signed.Task.CallbackURL != "http://manager:8080/api/runner/callback" || signed.Task.Properties["token"] != "private-token" {
		t.Fatal("invalid signed snapshot", e)
	}
	if _, _, e = p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "missing", "plan_id": plan["id"]}, "missing"); e == nil {
		t.Fatal("missing environment accepted")
	}
	updated := clone(environment)
	updated["variables"] = []any{Obj{"name": "custom", "value": "changed"}}
	if _, e = p.workspaceUpdate("environments", str(environment["id"]), updated); e != nil {
		t.Fatal(e)
	}
	again, created, e := p.createRun(payload, "once")
	if e != nil || created || again["id"] != id {
		t.Fatal("idempotency failed after environment edit", e)
	}
	if obj(again["properties"])["custom"] != "override" {
		t.Fatal("snapshot changed")
	}
	payload["timeout_seconds"] = 20
	if _, _, e = p.createRun(payload, "once"); e == nil {
		t.Fatal("changed timeout accepted")
	}
	if e = p.publishDispatches(context.Background()); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = p.DB.QueryRow("SELECT status FROM task_dispatches WHERE id=?", id).Scan(&status); e != nil || status != "PUBLISHED" {
		t.Fatal(status, e)
	}
	q, e := p.runnerQueue()
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	t.Cleanup(func() { /* Only this test's stream/lease is removed by the queue helper below. */ })
	_ = q
}
func TestRunnerCallbackAuthReceiptsAndDedup(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run, _, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "callback", "plan_id": plan["id"], "environment_id": environment["id"]}, "callback")
	if e != nil {
		t.Fatal(e)
	}
	id := str(run["id"])
	started := now()
	_, e = p.updateRun(id, Obj{"status": "RUNNING", "controller_id": "controller-a", "started_at": started})
	if e != nil {
		t.Fatal(e)
	}
	store, e := p.objectStore()
	if e != nil {
		t.Fatal(e)
	}
	prefix, e := rawPrefix(Obj{"id": id, "started_at": started})
	if e != nil {
		t.Fatal(e)
	}
	ref, e := store.Put(context.Background(), prefix+"results.jtl", strings.NewReader("sample\n"), 7, digest([]byte("sample\n")), "text/csv")
	if e != nil {
		t.Fatal(e)
	}
	cb := protocol.Callback{EventID: newID("event-"), TaskID: id, AttemptID: str(run["attempt_id"]), RunnerID: "controller-a", Status: "COMPLETED", StartedAt: started, FinishedAt: now(), Artifacts: []protocol.ObjectRef{ref}, MissingFiles: []string{"jmeter.log", "console.log"}}
	call := func(body Obj, auth bool) int {
		req := httptest.NewRequest("POST", protocol.CallbackPath, bytes.NewReader(encoded(body)))
		if auth {
			protocol.SignRequest(req, encoded(body), "controller-a", p.Config.AgentToken, time.Now())
		}
		rec := httptest.NewRecorder()
		Handler{p}.ServeHTTP(rec, req)
		return rec.Code
	}
	body := dtoObject(cb)
	if got := call(body, false); got != 401 {
		t.Fatal("unauthenticated callback", got)
	}
	bad := clone(body)
	obj(arr(bad["artifacts"])[0])["key"] = "other/run/raw/results.jtl"
	if got := call(bad, true); got != 400 {
		t.Fatal("foreign receipt accepted", got)
	}
	if got := call(body, true); got != 200 {
		t.Fatal("callback", got)
	}
	if got := call(body, true); got != 200 {
		t.Fatal("duplicate", got)
	}
	changed := clone(body)
	changed["exit_code"] = 1
	if got := call(changed, true); got != 409 {
		t.Fatal("mutated event accepted", got)
	}
	stored, e := p.getRun(id)
	if e != nil || stored["status"] != "COMPLETED" || obj(stored["raw_archive"])["status"] != "PARTIAL" || obj(stored["callback_delivery"])["status"] != "DELIVERED" {
		t.Fatal(stored, e)
	}
	var count int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM webhook_deliveries WHERE run_id=?", id).Scan(&count); e != nil || count != 0 {
		t.Fatal("secondary webhook created", count, e)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/runs/%s/artifacts/results.jtl", id), nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("X-Project-ID", p.projectID())
	Handler{p}.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "sample\n" {
		t.Fatal("object proxy", rec.Code, rec.Body.String())
	}
}

func TestRunnerClaimLeaseFencingAndMonotonicHeartbeat(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run, _, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "claim", "plan_id": plan["id"], "environment_id": environment["id"]}, "claim")
	if e != nil {
		t.Fatal(e)
	}
	id := str(run["id"])
	body := Obj{"task_id": id, "attempt_id": run["attempt_id"], "started_at": now(), "lease_token": "controller-a/" + id + "/" + str(run["attempt_id"]) + "/nonce"}
	if _, e = p.runnerClaim(context.Background(), "controller-a", body); e == nil {
		t.Fatal("unowned lease accepted")
	}
	q, e := p.runnerQueue()
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	ok, e := q.Acquire(context.Background(), str(body["lease_token"]))
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer q.Release(context.Background(), str(body["lease_token"]))
	claimed, e := p.runnerClaim(context.Background(), "controller-a", body)
	if e != nil || !boolean(claimed["execute"]) {
		t.Fatal(claimed, e)
	}
	again, e := p.runnerClaim(context.Background(), "controller-a", body)
	if e != nil || boolean(again["execute"]) || again["started_at"] != body["started_at"] {
		t.Fatal("duplicate claim reruns", again, e)
	}
	bad := clone(body)
	bad["attempt_id"] = "stale"
	if _, e = p.runnerHeartbeat("controller-a", bad); e == nil {
		t.Fatal("stale heartbeat accepted")
	}
	body["status"] = "CALLBACK_PENDING"
	body["callback_delivery"] = Obj{"status": "RETRY", "attempts": 2, "last_error": "https://secret?token=x", "next_attempt_at": now()}
	if _, e = p.runnerHeartbeat("controller-a", body); e != nil {
		t.Fatal(e)
	}
	body["status"] = "RUNNING"
	if _, e = p.runnerHeartbeat("controller-a", body); e != nil {
		t.Fatal(e)
	}
	stored, e := p.getRun(id)
	if e != nil || stored["status"] != "CALLBACK_PENDING" || obj(stored["callback_delivery"])["last_error"] != "callback_delivery_failed" {
		t.Fatal(stored, e)
	}
}
func TestRunnerManagedDatasetsOwnershipSnapshotAndSoftDelete(t *testing.T) {
	p, _, environment := newRunnerFixture(t)
	graph := basicWorkflowGraph()
	node := graphNode("csv-data", "CSVDataSet", Obj{"filename": "", "variableNames": "value", "delimiter": ",", "ignoreFirstLine": false, "recycle": true, "stopThread": false})
	obj(node["properties"])["dataset_columns"] = []any{"value"}
	graph["nodes"] = append(arr(graph["nodes"]), node)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "csv-data", 1))
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "data flow", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	wid := str(workflow["id"])
	first, e := p.datasetCreate(context.Background(), wid, "csv-data", "one.csv", strings.NewReader("value\none\n"))
	if e != nil {
		t.Fatal(e)
	}
	second, e := p.datasetCreate(context.Background(), wid, "csv-data", "two.csv", strings.NewReader("value\ntwo\n"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.datasetCreate(context.Background(), wid, "csv-data", "bad.csv", strings.NewReader("other\nwrong\n")); e == nil {
		t.Fatal("stale schema accepted")
	}
	published, e := p.publishWorkflow(wid, Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	plan := obj(published["plan"])
	requirements := arr(plan["dataset_requirements"])
	if len(requirements) != 1 || obj(requirements[0])["node_id"] != "csv-data" || obj(requirements[0])["workflow_id"] != wid {
		t.Fatal("origin missing", requirements)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "dataset run", "plan_id": plan["id"], "environment_id": environment["id"], "dataset_selections": []any{Obj{"node_id": "csv-data", "dataset_ids": []any{first["id"], second["id"]}, "distribution": "shard"}}}
	run, _, e := p.createRun(payload, "files")
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(obj(arr(run["datasets"])[0])["files"])) != 2 {
		t.Fatal("multiple files not frozen")
	}
	_, e = p.DB.Exec("UPDATE workflow_datasets SET deleted=1 WHERE id=?", first["id"])
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.stopRun(str(run["id"]), "now"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.createRun(payload, "newfiles"); e == nil {
		t.Fatal("deleted file selected")
	}
	if _, created, e := p.createRun(payload, "files"); e != nil || created {
		t.Fatal("frozen idempotent retry rejected", e)
	}
	other, e := p.projectCreate(Obj{"name": "other"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.forProject(str(other["id"])).datasetGetQ(p.DB, str(second["id"]), false); e == nil {
		t.Fatal("cross-project file exposed")
	}
	list, e := p.planDatasets(str(plan["id"]))
	if e != nil || len(arr(obj(arr(list["nodes"])[0])["datasets"])) != 1 {
		t.Fatal(list, e)
	}
}
func TestRunnerDatasetHTTPStreamingAliasesBOMAndQueuedStop(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	graph := basicWorkflowGraph()
	node := graphNode("csv-http", "CSVDataSet", Obj{"filename": "", "variableNames": "login_id", "delimiter": ","})
	graph["nodes"] = append(arr(graph["nodes"]), node)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "csv-http", 1))
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "aliases", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	wid := str(workflow["id"])
	content := "\xef\xbb\xbf\"user_id\"\r\n123\r\n"
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, e := form.CreateFormFile("file", "excel.csv")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.WriteString(file, content)
	_ = form.WriteField("node_id", "csv-http")
	_ = form.WriteField("name", "alias.csv")
	_ = form.Close()
	req := httptest.NewRequest("POST", "/api/workflows/"+wid+"/datasets", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("X-Project-ID", p.projectID())
	rec := httptest.NewRecorder()
	Handler{p}.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	dataset, e := decode(rec.Body.Bytes())
	if e != nil || dataset["sha256"] != digest([]byte(content)) || strings.Join(stringList(dataset["columns"]), ",") != "user_id" {
		t.Fatal(dataset, e)
	}
	req = httptest.NewRequest("GET", "/api/datasets/"+str(dataset["id"])+"/preview?limit=1", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	req.Header.Set("X-Project-ID", p.projectID())
	rec = httptest.NewRecorder()
	Handler{p}.ServeHTTP(rec, req)
	preview, e := decode(rec.Body.Bytes())
	if rec.Code != 200 || e != nil || strings.Join(stringList(preview["columns"]), ",") != "user_id" {
		t.Fatal(rec.Code, preview, e)
	}
	published, e := p.publishWorkflow(wid, Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	requirement := obj(arr(obj(published["plan"])["dataset_requirements"])[0])
	if strings.Join(stringList(requirement["columns"]), ",") != "user_id" || strings.Join(stringList(requirement["variable_names"]), ",") != "login_id" {
		t.Fatal("alias/header not separate", requirement)
	}
	run, _, e := p.createRun(Obj{"name": "queued-stop", "cluster_id": clusterID("controller-a"), "plan_id": plan["id"], "environment_id": environment["id"]}, "queued-stop")
	if e != nil {
		t.Fatal(e)
	}
	stopped, e := p.stopRun(str(run["id"]), "now")
	if e != nil || stopped["status"] != "STOPPED" || obj(stopped["raw_archive"])["status"] != "EMPTY" {
		t.Fatal(stopped, e)
	}
	if e = p.publishDispatches(context.Background()); e != nil {
		t.Fatal(e)
	}
	var status string
	if e = p.DB.QueryRow("SELECT status FROM task_dispatches WHERE id=?", run["id"]).Scan(&status); e != nil || status != "CANCELLED" {
		t.Fatal(status, e)
	}
}
func TestRunnerRedisFailureRemainsQueuedAndSafe(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run, _, e := p.createRun(Obj{"name": "redis-down", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "redis-down")
	if e != nil {
		t.Fatal(e)
	}
	p.Config.RedisAddr = "127.0.0.1:1"
	if e = p.publishDispatches(context.Background()); e != nil {
		t.Fatal(e)
	}
	stored, e := p.getRun(str(run["id"]))
	dispatch := obj(stored["dispatch"])
	if e != nil || stored["status"] != "QUEUED" || dispatch["status"] != "PENDING" || dispatch["last_error"] != "redis_unavailable" || number(dispatch["attempts"]) != 1 {
		t.Fatal(stored, e)
	}
}
func TestRunnerPortableTablesRoundTripAndOldBackupCompatibility(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	if _, _, e := p.createRun(Obj{"name": "portable", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "portable"); e != nil {
		t.Fatal(e)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(state["task_dispatches"])) != 1 {
		t.Fatal("outbox absent from export")
	}
	destination, _ := mysqlFixture(t)
	if _, e = destination.DB.Exec("DELETE FROM audit"); e != nil {
		t.Fatal(e)
	}
	if _, e = destination.DB.Exec("DELETE FROM plans"); e != nil {
		t.Fatal(e)
	}
	if e = destination.importState(clone(state)); e != nil {
		t.Fatal("new portable state", e)
	}
	var count int
	if e = destination.DB.QueryRow("SELECT COUNT(*) FROM task_dispatches").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	old, _ := mysqlFixture(t)
	oldState, e := old.exportState()
	if e != nil {
		t.Fatal(e)
	}
	delete(oldState, "workflow_datasets")
	delete(oldState, "task_dispatches")
	delete(oldState, "runner_events")
	target, _ := mysqlFixture(t)
	if _, e = target.DB.Exec("DELETE FROM audit"); e != nil {
		t.Fatal(e)
	}
	if _, e = target.DB.Exec("DELETE FROM plans"); e != nil {
		t.Fatal(e)
	}
	if e = target.importState(oldState); e != nil {
		t.Fatal("old backup missing new tables rejected", e)
	}
}
func TestRunnerLegacyIdempotencyUsesOriginalRequestHash(t *testing.T) {
	p, plan := mysqlFixture(t)
	old := runnerLegacyRun(t, p, plan, "controller-a", []any{"worker-1"})
	if _, e := p.mutateRun(str(old["id"]), func(r Obj) error { delete(r, "cluster_id"); delete(r, "cluster_snapshot"); return nil }); e != nil {
		t.Fatal(e)
	}
	payload := Obj{"name": "legacy", "plan_id": plan["id"], "workers": []any{"worker-1"}, "properties": Obj{}, "notes": ""}
	if _, e := p.DB.Exec("INSERT INTO idempotency VALUES(?,?,?,?)", "run", "pre-redis-key", digest(encoded(payload)), old["id"]); e != nil {
		t.Fatal(e)
	}
	again, created, e := p.createRun(payload, "pre-redis-key")
	if e != nil || created || again["id"] != old["id"] {
		t.Fatal("old optional-fields hash changed", again, e)
	}
	if _, _, e = p.createRun(payload, "fresh-key"); e == nil {
		t.Fatal("legacy retry exception permits new bypass")
	}
}
