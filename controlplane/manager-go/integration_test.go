package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func mysqlFixture(t *testing.T) (*Platform, Obj) {
	t.Helper()
	if os.Getenv("JMETER_GO_INTEGRATION") != "1" {
		t.Skip("set JMETER_GO_INTEGRATION=1 with provisioned MySQL file credentials")
	}
	c, e := loadConfig()
	if e != nil {
		t.Fatal(e)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	c.StateDir = filepath.Join(root, "state")
	c.PlansDir = filepath.Join(root, "plans")
	c.ResultsDir = filepath.Join(root, "results")
	c.FrontendDir = filepath.Join(root, "frontend")
	c.AdminToken = "admin-secret"
	c.SessionKey = "session-secret"
	c.AgentToken = "agent-secret"
	c.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1", "controller-b": "http://127.0.0.1:1"}
	c.WorkerURLs = map[string]string{}
	for i := 1; i <= 6; i++ {
		c.WorkerURLs[fmt.Sprintf("worker-%d", i)] = "http://127.0.0.1:1"
	}
	// Shared membership in this fixture is an explicit deployment choice.
	c.DefaultClusterWorkers = map[string][]string{"controller-a": sortedKeys(c.WorkerURLs), "controller-b": sortedKeys(c.WorkerURLs)}
	c.ManagerID = newID("test-")
	c.MySQLTablePrefix = "jt_" + newID("")[:16] + "_"
	c.AgentTimeout = time.Second
	c.Tick = 10 * time.Millisecond
	c.ArchiveEndpoint = runnerObjectPeer(t).URL
	c.ArchiveBucket = "jmeter-reports"
	c.ArchiveAccessKey = "fixture-access"
	c.ArchiveSecretKey = "fixture-secret"
	p, e := newPlatform(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for _, table := range append(append(append([]string{}, portableTables...), derivedTables...), "sessions", "instances", "schema_migrations") {
			if _, e := p.DB.DB.Exec("DROP TABLE IF EXISTS " + c.MySQLTablePrefix + table); e != nil {
				t.Errorf("isolated table cleanup: %v", e)
			}
		}
		p.Close()
	})
	plan, e := p.createPlan(Obj{"name": "测试 <>&\u2028", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	return p, plan
}
func runFixture(t *testing.T, p *Platform, plan Obj, key string) Obj {
	t.Helper()
	r, created, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, p), "name": "run", "plan_id": plan["id"], "workers": []any{"worker-1"}, "properties": Obj{}, "notes": ""}, key)
	if e != nil || !created {
		t.Fatal(r, created, e)
	}
	return r
}
func fixtureEnvironmentID(t *testing.T, p *Platform) string {
	t.Helper()
	environment, e := p.workspaceCreate("environments", Obj{"name": newID("fixture-"), "base_host": "http://fixture.invalid"})
	if e != nil {
		t.Fatal(e)
	}
	return str(environment["id"])
}
func frozenDispatchTask(t *testing.T, p *Platform, id string) protocol.Task {
	t.Helper()
	var payload string
	if e := p.DB.QueryRow("SELECT payload FROM task_dispatches WHERE id=?", id).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	opened, e := p.openWorkspace("task_dispatches", id, 1, payload)
	if e != nil {
		t.Fatal(e)
	}
	var signed protocol.SignedTask
	if e = decodeInto(opened, &signed); e != nil || protocol.VerifyTask(signed, p.Config.AgentToken) != nil {
		t.Fatal("invalid frozen task signature", e)
	}
	return signed.Task
}
func TestMySQLAuthAPIIdempotency(t *testing.T) {
	p, plan := mysqlFixture(t)
	httpServer := httptest.NewServer(Handler{p})
	defer httpServer.Close()
	request := func(path string, body Obj, headers map[string]string) (int, http.Header, []byte) {
		t.Helper()
		method := "GET"
		var input io.Reader
		if body != nil {
			method = "POST"
			input = bytes.NewReader(encoded(body))
		}
		req, e := http.NewRequest(method, httpServer.URL+path, input)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("X-Project-ID", p.projectID())
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, b
	}
	if code, _, _ := request("/api/runs", nil, nil); code != 401 {
		t.Fatal(code)
	}
	code, headers, b := request("/api/login", Obj{"token": "admin-secret"}, nil)
	if code != 200 || !strings.Contains(headers.Get("Set-Cookie"), "HttpOnly") || !strings.Contains(headers.Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatal(code, headers)
	}
	login, e := decode(b)
	if e != nil {
		t.Fatal(e)
	}
	cookie := strings.Split(headers.Get("Set-Cookie"), ";")[0]
	session := map[string]string{"Cookie": cookie}
	if code, _, _ := request("/api/plans", Obj{"name": "x", "content": "<x/>"}, session); code != 403 {
		t.Fatal(code)
	}
	session["X-CSRF-Token"] = str(login["csrf_token"])
	if code, _, _ := request("/api/plans", Obj{"name": "x", "content": "<jmeterTestPlan/>"}, session); code != 201 {
		t.Fatal(code)
	}
	if code, _, _ := request("/api/logout", Obj{}, session); code != 200 {
		t.Fatal(code)
	}
	if code, _, _ := request("/api/session", nil, session); code != 401 {
		t.Fatal(code)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, p), "name": "unicode <>&\u2028", "plan_id": plan["id"], "workers": []any{"worker-1"}, "properties": Obj{"api_token": "do-not-leak", "x": "值"}}
	auth := map[string]string{"Authorization": "Bearer admin-secret", "Idempotency-Key": "once"}
	code, _, body := request("/api/runs", payload, auth)
	if code != 201 {
		t.Fatal(code, string(body))
	}
	first, e := decode(body)
	if e != nil {
		t.Fatal(e)
	}
	if str(obj(first["properties"])["api_token"]) != "***" {
		t.Fatal("secret not redacted")
	}
	if code, _, body = request("/api/runs", payload, auth); code != 200 {
		t.Fatal(code, string(body))
	}
	payload["name"] = "changed"
	if code, _, _ = request("/api/runs", payload, auth); code != 409 {
		t.Fatal(code)
	}
	if code, _, _ = request("/api/runs/"+str(first["id"])+"/artifacts", nil, auth); code != 409 {
		t.Fatal(code)
	}
	if code, _, _ = request("/api/runs/"+str(first["id"])+"/reports", Obj{}, auth); code != 410 {
		t.Fatal(code)
	}
	if code, _, _ = request("/api/compare?runs=x,y", nil, auth); code != 410 {
		t.Fatal(code)
	}
	// A second connection attaches to the same durable idempotency and session data.
	p2, e := newPlatform(p.Config)
	if e != nil {
		t.Fatal(e)
	}
	defer p2.Close()
	payload["name"] = "unicode <>&\u2028"
	again, created, e := p2.createRun(payload, "once")
	if e != nil || created || again["id"] != first["id"] {
		t.Fatal(again, created, e)
	}
	if _, e = p.stopRun(str(first["id"]), "now"); e != nil {
		t.Fatal(e)
	}
	// SQLite TEXT keys retain trailing spaces; MySQL must compare their exact UTF-8 bytes.
	keyRuns := map[string]string{}
	for _, exactKey := range []string{"exact", "exact ", "exact  ", "中文", "中文 "} {
		r, created, e := p.createRun(payload, exactKey)
		if e != nil || !created {
			t.Fatalf("distinct key %q collided: %v %v", exactKey, created, e)
		}
		keyRuns[exactKey] = str(r["id"])
		if _, e = p.stopRun(str(r["id"]), "now"); e != nil {
			t.Fatal(e)
		}
	}
	unique := map[string]bool{}
	for exactKey, id := range keyRuns {
		if unique[id] {
			t.Fatalf("key %q collided", exactKey)
		}
		unique[id] = true
		r, created, e := p.createRun(payload, exactKey)
		if e != nil || created || str(r["id"]) != id {
			t.Fatalf("exact key %q not durable: %v %v", exactKey, created, e)
		}
	}
	// Concurrent same-key requests resolve to one row instead of a duplicate-key 500.
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, _, e := p.createRun(payload, "parallel")
			if e != nil {
				errs <- e
			} else {
				ids <- str(r["id"])
			}
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		t.Error(e)
	}
	id := ""
	for next := range ids {
		if id == "" {
			id = next
		}
		if next != id {
			t.Fatal("duplicate run")
		}
	}
}

type agentPeer struct {
	server                                  *httptest.Server
	mu                                      sync.Mutex
	id, role                                string
	generation                              int
	job                                     Obj
	jobs                                    map[string]Obj
	resets                                  map[string]Obj
	lastReset                               string
	starts, recoveries                      int
	available, resetAuto, rebootBeforeReset bool
	resetRequests                           []Obj
	gateEntered                             chan struct{}
	gateRelease                             chan struct{}
	gateFail                                bool
}

func newAgentPeer(t *testing.T, id, role string) *agentPeer {
	a := &agentPeer{id: id, role: role, generation: 1, jobs: map[string]Obj{}, resets: map[string]Obj{}, available: true, resetAuto: true}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer agent-secret" {
			w.WriteHeader(401)
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if !a.available {
			w.WriteHeader(503)
			return
		}
		m := Obj{}
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			m, _ = decode(raw)
		}
		var result Obj
		status := 200
		path := r.URL.Path
		switch {
		case path == "/status":
			state := "idle"
			if str(a.job["state"]) == "RUNNING" {
				state = "running"
			}
			var reset any
			if a.lastReset != "" {
				reset = a.resets[a.lastReset]
			}
			result = Obj{"id": a.id, "role": a.role, "healthy": true, "status": state, "generation": a.generation, "job": a.job, "reset_operation": reset}
		case strings.HasPrefix(path, "/jobs/"):
			if a.gateEntered != nil {
				entered, release, fail := a.gateEntered, a.gateRelease, a.gateFail
				a.mu.Unlock()
				close(entered)
				<-release
				a.mu.Lock()
				if fail {
					w.WriteHeader(503)
					return
				}
			}
			result = a.jobs[strings.TrimPrefix(path, "/jobs/")]
			if result == nil {
				status = 404
			}
		case path == "/runs":
			id := str(m["id"])
			result = a.jobs[id]
			if result == nil {
				result = Obj{"id": id, "state": "RUNNING", "started_at": "2026-11-05T15:59:59.999999999Z"}
				a.jobs[id] = result
				a.starts++
			}
			a.job = result
			status = 202
		case strings.HasSuffix(path, "/stop"):
			a.job["state"] = "STOPPED"
			result = a.job
		case path == "/reset":
			a.resetRequests = append(a.resetRequests, clone(m))
			if a.rebootBeforeReset {
				a.generation++
				a.rebootBeforeReset = false
			}
			op := str(m["operation_id"])
			reset := a.resets[op]
			if reset == nil {
				if int(number(m["expected_generation"])) != a.generation {
					w.WriteHeader(409)
					return
				}
				reset = Obj{"id": op, "state": "RESETTING"}
				a.resets[op] = reset
				a.lastReset = op
				if a.resetAuto {
					a.generation++
					reset["state"] = "COMPLETED"
				}
			}
			result = Obj{"state": "RESETTING", "generation": a.generation}
			status = 202
		case path == "/recover":
			a.recoveries++
			result = Obj{"cleared": true}
		default:
			status = 404
		}
		raw := encoded(result)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(raw)
	}))
	t.Cleanup(a.server.Close)
	return a
}
func schedulerFixture(t *testing.T, p *Platform) (*Scheduler, []*agentPeer) {
	t.Helper()
	nodeRedisFixture(t, p)
	peers := []*agentPeer{newAgentPeer(t, "controller-a", "controller"), newAgentPeer(t, "controller-b", "controller")}
	for i := 1; i <= 6; i++ {
		peers = append(peers, newAgentPeer(t, fmt.Sprintf("worker-%d", i), "worker"))
	}
	for i, a := range peers {
		if i < 2 {
			p.Config.ControllerURLs[a.id] = a.server.URL
		} else {
			p.Config.WorkerURLs[a.id] = a.server.URL
		}
	}
	schedulerReportPublishers.Store(p, &schedulerPeerReports{p: p, peers: peers, bootAt: time.Now().Add(-time.Minute).UnixMilli()})
	t.Cleanup(func() { schedulerReportPublishers.Delete(p) })
	publishSchedulerReports(t, p)
	s := newScheduler(p, p.Config.ManagerID)
	t.Cleanup(s.close)
	return s, peers
}
func tick(t *testing.T, s *Scheduler) {
	t.Helper()
	publishSchedulerReports(t, s.P)
	if e := s.tick(); e != nil {
		t.Fatal(e)
	}
}

func TestMySQLRawReceiptBackupImport(t *testing.T) {
	p, plan := mysqlFixture(t)
	s3 := newS3Peer(t)
	p.Config.ArchiveEndpoint = s3.server.URL
	p.Config.ArchiveBucket = "jmeter-reports"
	p.Config.ArchiveRegion = "us-east-1"
	p.Config.ArchiveAccessKey = "s3-access"
	p.Config.ArchiveSecretKey = "s3-secret"
	p.Config.ArchiveTimeout = time.Second
	run := legacyRunFixture(t, p, plan, "raw")
	id := str(run["id"])
	if _, e := p.updateRun(id, Obj{"started_at": "2026-11-05T16:00:00.000000001Z"}); e != nil {
		t.Fatal(e)
	}
	for _, name := range rawFiles {
		path := filepath.Join(p.Config.ResultsDir, id, "raw", name)
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte(name+"\r\nsecret=unchanged\x00"), 0644)
	}
	if _, e := p.updateRun(id, Obj{"status": "STOPPED", "finished_at": now()}); e != nil {
		t.Fatal(e)
	}
	if e := p.archiveRun(id); e != nil {
		t.Fatal(e)
	}
	r, _ := p.getRun(id)
	receipt := obj(r["raw_archive"])
	if receipt["status"] != "ARCHIVED" || receipt["prefix"] != "2026-11-06/"+id+"/raw/" || len(arr(receipt["files"])) != 3 {
		t.Fatal(receipt)
	}
	listing, e := p.listArtifacts(id)
	if e != nil || number(listing["total"]) != 3 {
		t.Fatal(listing, e)
	}
	if e = p.archiveRun(id); e != nil {
		t.Fatal(e)
	}
	if len(s3.puts) != 3 {
		t.Fatal("archive duplicated files")
	}
	backupRoot := filepath.Join(t.TempDir(), "backup")
	if _, e = p.backup(backupRoot); e != nil {
		t.Fatal(e)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if _, e = restoreArchive(backupRoot, restored); e != nil {
		t.Fatal(e)
	}
	state, e := loadJSONFile(filepath.Join(restored, "state", "manager.json"))
	if e != nil {
		t.Fatal(e)
	}
	rows := arr(state["runs"])
	raw, e := stateData(obj(rows[0])["data"])
	if e != nil {
		t.Fatal(e)
	}
	saved, e := decode([]byte(raw))
	if e != nil || obj(saved["raw_archive"])["status"] != "PENDING" {
		t.Fatal(saved, e)
	}
	if e = p.importState(state); e == nil {
		t.Fatal("nonempty import destination accepted")
	}
	manifest, e := loadJSONFile(filepath.Join(backupRoot, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	for rel := range obj(manifest["files"]) {
		if strings.Contains(rel, "secret") || strings.Contains(rel, "session") {
			t.Fatal("credential in backup")
		}
	}
	if e = os.WriteFile(filepath.Join(backupRoot, "plans", "snapshots", str(plan["sha256"])+".jmx"), []byte("tampered"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = restoreArchive(backupRoot, filepath.Join(t.TempDir(), "corrupt")); e == nil {
		t.Fatal("checksum corruption accepted")
	}
}
func TestMySQLXMLAndLegacyReady(t *testing.T) {
	p, plan := mysqlFixture(t)
	for _, content := range []string{"<!DOCTYPE a><a/>", "<!ENTITY a 'x'><a/>", "<unclosed>", "<a/><b/>", "not xml"} {
		if _, e := p.createPlan(Obj{"name": "bad", "content": content}); e == nil {
			t.Errorf("unsafe XML accepted: %q", content)
		}
	}
	run := legacyRunFixture(t, p, plan, "legacy")
	id := str(run["id"])
	for _, state := range []string{"READY", "ANALYZING"} {
		report := Obj{"id": "legacy-" + strings.ToLower(state), "run_id": id, "status": state, "created_at": now(), "summary": nil}
		if _, e := p.DB.Exec("INSERT INTO reports(id,run_id,status,created_at,data) VALUES(?,?,?,?,?)", report["id"], id, state, report["created_at"], string(encoded(report))); e != nil {
			t.Fatal(e)
		}
	}
	p2, e := newPlatform(p.Config)
	if e != nil {
		t.Fatal(e)
	}
	defer p2.Close()
	ready, _ := p2.report("legacy-ready")
	pending, _ := p2.report("legacy-analyzing")
	if ready["status"] != "READY" || pending["status"] != "FAILED" {
		t.Fatal(ready, pending)
	}
	r, _ := p2.getRun(id)
	if obj(r["latest_report"])["id"] != "legacy-ready" {
		t.Fatal(r)
	}
	_ = json.Valid
}

// Historical runs lack a cluster snapshot. Seed their persisted shape to retain
// coverage of the pre-cluster fallback/recovery contract without a server bypass.
func legacyRunFixture(t *testing.T, p *Platform, plan Obj, key string) Obj {
	return legacyClusterRunFixture(t, p, plan, "", key)
}

// Legacy HTTP scheduling is tested with persisted historical rows. New submissions
// always use Redis and must not be silently converted into the retired protocol.
func legacyClusterRunFixture(t *testing.T, p *Platform, plan Obj, cluster, key string) Obj {
	t.Helper()
	run := Obj{"id": newID("run-"), "project_id": p.projectID(), "name": "run", "status": "QUEUED", "created_at": now(), "plan_id": plan["id"], "plan_name": plan["name"], "plan_sha256": plan["sha256"], "workers": []any{"worker-1"}, "properties": Obj{}, "notes": "", "raw_archive": nil}
	e := p.transaction(func(tx *Tx) error {
		if cluster != "" {
			snapshot, workers, e := p.selectClusterRunQ(tx, cluster, nil, false)
			if e != nil {
				return e
			}
			run["cluster_id"], run["cluster_snapshot"], run["workers"] = cluster, snapshot, workers
		}
		return p.saveRun(tx, run)
	})
	if e != nil {
		t.Fatal(e)
	}
	return run
}
