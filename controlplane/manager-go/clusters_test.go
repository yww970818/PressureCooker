package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMySQLClusterRunWithoutDefaultProject(t *testing.T) {
	p, fixturePlan := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "single-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("DELETE FROM plans WHERE id=?", fixturePlan["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("DELETE FROM projects WHERE id=?", defaultProjectID); err != nil {
		t.Fatal(err)
	}
	projectID := str(project["id"])
	scoped := p.forRequest(context.Background(), projectID)
	plan, err := scoped.createPlan(Obj{"name": "isolated plan", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	id := clusterID("controller-b")
	workers := []any{"worker-3", "worker-4", "worker-5", "worker-6"}
	if _, err = scoped.updateCluster(id, Obj{"revision": 0, "name": "four workers", "worker_ids": workers}); err != nil {
		t.Fatalf("cluster update without the default project failed: %v", err)
	}
	input := Obj{"name": "isolated task", "plan_id": plan["id"], "cluster_id": id, "environment_id": fixtureEnvironmentID(t, scoped), "workers": workers}
	run, created, err := scoped.createRun(input, "no-default-project")
	if err != nil || !created {
		t.Fatalf("task creation failed: created=%t err=%v", created, err)
	}
	if str(run["project_id"]) != projectID || !bytes.Equal(encoded(run["workers"]), encoded(workers)) || str(obj(run["cluster_snapshot"])["controller_id"]) != "controller-b" {
		t.Fatal("task did not preserve the requested project and worker set")
	}
	var count int
	if err = p.DB.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected only the existing project: count=%d err=%v", count, err)
	}
}

func TestMySQLClusterManagement(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1", "controller-b": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1", "worker-2": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1", "worker-2"}, "controller-b": {"worker-1", "worker-2"}}
	groups, e := p.clusterConfigurations()
	if e != nil || len(groups) != 2 {
		t.Fatal(groups, e)
	}
	a := obj(groups[0])
	b := obj(groups[1])
	id := str(a["id"])
	if len(arr(a["worker_ids"])) != 2 || number(a["revision"]) != 0 || boolean(a["configured"]) {
		t.Fatal(a)
	}
	var count int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM clusters").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	saved, e := p.updateCluster(id, Obj{"revision": 0, "name": "  第一集群  ", "worker_ids": []any{"worker-1"}})
	if e != nil || saved["name"] != "第一集群" || number(saved["revision"]) != 1 {
		t.Fatal(saved, e)
	}
	if _, e = p.updateCluster(id, Obj{"revision": 0, "name": "stale"}); e == nil {
		t.Fatal("stale edit accepted")
	}
	for _, body := range []Obj{{"revision": 1, "name": " "}, {"revision": 1, "name": "x", "worker_ids": []any{"worker-7"}}, {"revision": 1, "name": "x", "worker_ids": []any{"worker-1", "worker-1"}}} {
		if _, e = p.updateCluster(id, body); e == nil {
			t.Fatal("invalid edit", body)
		}
	}
	scoped := *p
	scoped.ProjectID = "other-project"
	groups, e = scoped.clusterConfigurations()
	if e != nil || obj(groups[0])["name"] != "第一集群" || len(arr(obj(groups[1])["worker_ids"])) != 2 {
		t.Fatal(groups, e)
	}
	delete(p.Config.ControllerURLs, "controller-a")
	groups, e = p.clusterConfigurations()
	if e != nil || len(groups) != 2 || boolean(obj(groups[0])["registered"]) || obj(groups[0])["name"] != "第一集群" {
		t.Fatal(groups, e)
	}
	if _, e = p.updateCluster(id, Obj{"revision": 1, "name": "removed"}); e == nil {
		t.Fatal("removed controller editable")
	}
	_ = b
}

func TestMySQLClusterFirstSaveRaceAndPortable(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1"}, "controller-b": {"worker-1"}}
	id := clusterID("controller-a")
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, e := p.updateCluster(id, Obj{"revision": 0, "name": name})
			outcomes <- e
		}(name)
	}
	wg.Wait()
	close(outcomes)
	success := 0
	for e := range outcomes {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("OCC race", success)
	}
	state, e := p.exportState()
	if e != nil || len(arr(state["clusters"])) != 1 {
		t.Fatal(e)
	}
	dest, _ := mysqlFixture(t)
	dest.Config.ControllerURLs = map[string]string{}
	dest.Config.WorkerURLs = map[string]string{}
	for _, table := range portableTables {
		if table != "projects" {
			if _, e = dest.DB.Exec("DELETE FROM " + table); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = dest.importState(state); e != nil {
		t.Fatal(e)
	}
	groups, e := dest.clusterConfigurations()
	if e != nil || len(groups) != 1 || !boolean(obj(groups[0])["configured"]) {
		t.Fatal(groups, e)
	}
	delete(state, "clusters")
	older, _ := mysqlFixture(t)
	for _, table := range portableTables {
		if table != "projects" {
			if _, e = older.DB.Exec("DELETE FROM " + table); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = older.importState(state); e != nil {
		t.Fatal("old archive", e)
	}
}

func TestMySQLClusterRunSelectionSnapshotAndRetry(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1", "controller-b": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1", "worker-2": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1", "worker-2"}, "controller-b": {"worker-1", "worker-2"}}
	body := Obj{"name": "selected", "plan_id": plan["id"], "environment_id": fixtureEnvironmentID(t, p)}
	if _, _, e := p.createRun(body, "missing"); e == nil {
		t.Fatal("missing cluster accepted")
	}
	body["cluster_id"] = clusterID("controller-a")
	run, created, e := p.createRun(body, "stable")
	if e != nil || !created || len(arr(run["workers"])) != 2 || obj(run["cluster_snapshot"])["controller_id"] != "controller-a" {
		t.Fatal(run, created, e)
	}
	id := str(body["cluster_id"])
	if _, e = p.updateCluster(id, Obj{"revision": 0, "name": "renamed", "worker_ids": []any{"worker-1"}}); e == nil {
		t.Fatal("queued mapping edit")
	}
	if _, e = p.updateCluster(id, Obj{"revision": 0, "name": "renamed"}); e != nil {
		t.Fatal("queued rename", e)
	}
	retry, created, e := p.createRun(body, "stable")
	if e != nil || created || obj(retry["cluster_snapshot"])["name"] != obj(run["cluster_snapshot"])["name"] {
		t.Fatal(retry, created, e)
	}
	if _, e = p.updateRun(str(run["id"]), Obj{"status": "COMPLETED"}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.updateCluster(id, Obj{"revision": 1, "name": "smaller", "worker_ids": []any{"worker-1"}}); e != nil {
		t.Fatal(e)
	}
	retry, created, e = p.createRun(body, "stable")
	if e != nil || created || len(arr(retry["workers"])) != 2 {
		t.Fatal("retry defaults changed", retry, created, e)
	}
	body["workers"] = []any{"worker-2"}
	if _, _, e = p.createRun(body, "foreign"); e == nil {
		t.Fatal("foreign worker accepted")
	}
	body["cluster_id"] = clusterID("controller-b")
	if _, _, e = p.createRun(body, "stable"); e == nil {
		t.Fatal("cluster idempotency omitted")
	}
}

func TestMySQLClusterStatusAndHTTPAuth(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1", "controller-b": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1"}, "controller-b": {"worker-1"}}
	status, e := p.cluster()
	if e != nil || len(arr(status["clusters"])) != 2 || len(arr(obj(arr(status["clusters"])[0])["shared_worker_ids"])) != 1 {
		t.Fatal(status, e)
	}
	sid, session, e := p.login("admin-secret")
	if e != nil {
		t.Fatal(e)
	}
	invoke := func(cookie, csrf string) int {
		r := httptest.NewRequest("PUT", "/api/clusters/"+clusterID("controller-a"), bytes.NewReader(encoded(Obj{"revision": 0, "name": "saved"})))
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "jmeter_session", Value: cookie})
		}
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		Handler{p}.ServeHTTP(w, r)
		return w.Code
	}
	if code := invoke("", ""); code != 401 {
		t.Fatal(code)
	}
	if code := invoke(sid, ""); code != 403 {
		t.Fatal(code)
	}
	if code := invoke(sid, str(session["csrf_token"])); code != 200 {
		t.Fatal(code)
	}
}

func TestClusterCLIRequiresSelectionAndOmitsDefaultWorkers(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("test-token"), 0600); e != nil {
		t.Fatal(e)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body Obj
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if body["cluster_id"] != "cluster-test" {
			t.Error(body)
		}
		if _, present := body["workers"]; present {
			t.Error("default workers supplied", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"run-test"}`))
	}))
	defer server.Close()
	base := []string{"api", "start", "plan-test", "--name", "test", "--key", "stable", "--url", server.URL, "--token-file", token, "--environment", "environment-test", "--project", defaultProjectID}
	if e := runCLI(base); e == nil {
		t.Fatal("missing cluster accepted")
	}
	if requests != 0 {
		t.Fatal("unselected task reached server")
	}
	if e := runCLI(append(base, "--cluster", "cluster-test")); e != nil {
		t.Fatal(e)
	}
	if requests != 1 {
		t.Fatal(requests)
	}
}

func TestMySQLClusterCallbackConfigurationAndFreeze(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1"}, "controller-b": {"worker-1"}}
	id := clusterID("controller-a")
	if _, e := p.updateCluster(id, Obj{"revision": 0, "name": "callback", "callback_url": "http://127.0.0.1:1/finished"}); e == nil {
		t.Fatal("arbitrary business webhook accepted as runner callback")
	}
	saved, e := p.updateCluster(id, Obj{"revision": 0, "name": "callback", "callback_url": " http://127.0.0.1:1 "})
	if e != nil || saved["callback_url"] != "http://127.0.0.1:1/api/runner/callback" {
		t.Fatal(saved, e)
	}
	run, _, e := p.createRun(Obj{"name": "frozen", "plan_id": plan["id"], "cluster_id": id, "environment_id": fixtureEnvironmentID(t, p)}, "callback")
	if e != nil {
		t.Fatal(e)
	}
	stored, e := p.get("runs", str(run["id"]))
	if e != nil || obj(stored["cluster_snapshot"])["callback_url"] != "http://127.0.0.1:1/api/runner/callback" {
		t.Fatal(stored, e)
	}
	saved, e = p.updateCluster(id, Obj{"revision": 1, "name": "cleared", "callback_url": ""})
	if e != nil || saved["callback_url"] != p.Config.RunnerCallbackURL {
		t.Fatal(saved, e)
	}
	stored, e = p.get("runs", str(run["id"]))
	if e != nil || obj(stored["cluster_snapshot"])["callback_url"] != "http://127.0.0.1:1/api/runner/callback" {
		t.Fatal("snapshot changed", stored, e)
	}
	if _, e = p.updateCluster(id, Obj{"revision": 2, "name": "bad", "callback_url": "file:///etc/passwd"}); e == nil {
		t.Fatal("invalid callback accepted")
	}
	if _, e = p.updateCluster(id, Obj{"revision": 2, "name": "bad", "callback_url": true}); e == nil {
		t.Fatal("invalid callback type")
	}
}

func TestMySQLClusterLegacyIdempotentRetry(t *testing.T) {
	p, plan := mysqlFixture(t)
	historical := legacyRunFixture(t, p, plan, "historical-seed")
	payload := Obj{"name": "run", "plan_id": plan["id"], "workers": []any{"worker-1"}, "properties": Obj{}, "notes": ""}
	hash := digest(encoded(payload))
	if _, e := p.DB.Exec("INSERT INTO idempotency VALUES(?,?,?,?)", "run", "pre-cluster-key", hash, historical["id"]); e != nil {
		t.Fatal(e)
	}
	retry, created, e := p.createRun(payload, "pre-cluster-key")
	if e != nil || created || retry["id"] != historical["id"] || len(obj(retry["cluster_snapshot"])) != 0 {
		t.Fatal(retry, created, e)
	}
	if _, _, e = p.createRun(payload, "fresh-without-cluster"); e == nil {
		t.Fatal("legacy retry rule bypassed strict new submission")
	}
}

func TestMySQLClusterMappingAndSubmitSerialize(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1", "worker-2": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1", "worker-2"}, "controller-b": {"worker-1", "worker-2"}}
	id := clusterID("controller-a")
	start := make(chan struct{})
	environmentID := fixtureEnvironmentID(t, p)
	type outcome struct {
		run Obj
		err error
	}
	created := make(chan outcome, 1)
	edited := make(chan error, 1)
	go func() {
		<-start
		run, _, e := p.createRun(Obj{"name": "concurrent", "plan_id": plan["id"], "cluster_id": id, "environment_id": environmentID}, "concurrent")
		created <- outcome{run, e}
	}()
	go func() {
		<-start
		_, e := p.updateCluster(id, Obj{"revision": 0, "name": "concurrent", "worker_ids": []any{"worker-1"}})
		edited <- e
	}()
	close(start)
	result := <-created
	editErr := <-edited
	if result.err != nil {
		t.Fatal(result.err)
	}
	snapshot := obj(result.run["cluster_snapshot"])
	if editErr == nil {
		if number(snapshot["revision"]) != 1 || len(arr(result.run["workers"])) != 1 {
			t.Fatal("mixed snapshot", result.run)
		}
	} else {
		if number(snapshot["revision"]) != 0 || len(arr(result.run["workers"])) != 2 {
			t.Fatal("mixed snapshot", result.run, editErr)
		}
	}
}

func TestMySQLClusterEmptyMappingUnassignedAndOfflineWorkers(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"controller-a": "http://127.0.0.1:1", "controller-b": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1", "worker-2": "http://127.0.0.1:1"}
	p.Config.DefaultClusterWorkers = map[string][]string{"controller-a": {"worker-1", "worker-2"}, "controller-b": {"worker-1", "worker-2"}}
	a, b := clusterID("controller-a"), clusterID("controller-b")
	if _, e := p.updateCluster(a, Obj{"revision": 0, "name": "empty", "worker_ids": []any{}}); e != nil {
		t.Fatal(e)
	}
	if _, e := p.updateCluster(b, Obj{"revision": 0, "name": "bound", "worker_ids": []any{"worker-1"}}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := p.createRun(Obj{"name": "empty", "plan_id": plan["id"], "cluster_id": a}, "empty"); e == nil {
		t.Fatal("empty cluster queued")
	}
	groups, unassigned, e := p.attachClusterStatus([]any{}, []any{Obj{"id": "worker-1"}, Obj{"id": "worker-2"}})
	if e != nil || len(unassigned) != 1 || obj(unassigned[0])["id"] != "worker-2" || len(arr(obj(groups[0])["worker_ids"])) != 0 {
		t.Fatal(groups, unassigned, e)
	}
	delete(p.Config.WorkerURLs, "worker-1")
	groups, _, e = p.attachClusterStatus([]any{}, []any{Obj{"id": "worker-2"}})
	if e != nil || obj(arr(obj(groups[1])["workers"])[0])["id"] != "worker-1" || boolean(obj(arr(obj(groups[1])["workers"])[0])["healthy"]) {
		t.Fatal(groups, e)
	}
	if _, _, e = p.createRun(Obj{"name": "removed", "plan_id": plan["id"], "cluster_id": b}, "removed"); e == nil {
		t.Fatal("removed worker queued")
	}
	delete(p.Config.ControllerURLs, "controller-b")
	if _, _, e = p.createRun(Obj{"name": "removed controller", "plan_id": plan["id"], "cluster_id": b, "workers": []any{"worker-1"}}, "removed-controller"); e == nil {
		t.Fatal("removed controller queued")
	}
}
