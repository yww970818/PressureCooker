package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProjectsAndCollectionOperationsDiscovered(t *testing.T) {
	paths := obj(openAPI()["paths"])
	for _, path := range []string{"/api/projects", "/api/projects/{id}", "/api/projects/{id}/archive", "/api/api-collections/import", "/api/api-collections/{id}/export", "/api/api-collections/{id}/requests", "/api/api-collections/{id}/debug", "/api/api-collections/{id}/environments", "/api/api-collections/{id}/settings"} {
		if paths[path] == nil {
			t.Errorf("missing API discovery %s", path)
		}
	}
}

func TestMySQLProjectWorkspaceReferencesVersionsAndPortableBackup(t *testing.T) {
	p, defaultPlan := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "隔离项目"})
	if e != nil {
		t.Fatal(e)
	}
	projectID := str(project["id"])
	scoped := p.forProject(projectID)
	collection, e := scoped.workspaceCreate("api_collections", Obj{"name": "集合", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/v1": Obj{"get": Obj{"responses": Obj{"200": Obj{"description": "ok"}}}}}}, "client_state": Obj{"auth": Obj{"secret": "encrypted-only"}}})
	if e != nil {
		t.Fatal(e)
	}
	id := str(collection["id"])
	graph := basicWorkflowGraph()
	props := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	props["api_reference"] = Obj{"collection_id": id, "revision": 1, "path": "/v1", "method": "GET"}
	workflow, e := scoped.workspaceCreate("workflows", Obj{"name": "引用流程", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.forProject(defaultProjectID).workspaceCreate("workflows", Obj{"name": "越界引用", "graph": graph}); e == nil {
		t.Fatal("cross-project reference accepted")
	}
	if _, e = scoped.importWorkflow(Obj{"name": "越界源脚本", "plan_id": defaultPlan["id"]}); e == nil {
		t.Fatal("cross-project source plan accepted")
	}
	published, e := scoped.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	planID := str(published["managerPlanId"])
	hash := obj(published["plan"])["sha256"]
	if _, e = scoped.collectionPatch(id, Obj{"revision": 1, "path": "/v2", "method": "POST", "previous_path": "/v1", "previous_method": "GET", "operation": Obj{"responses": Obj{"200": Obj{"description": "changed"}}}}, "requests"); e != nil {
		t.Fatal(e)
	}
	// The reference retains its exact old revision; publication keeps its config.
	again, e := scoped.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if e != nil || obj(again["plan"])["sha256"] != hash {
		t.Fatal("mutable collection rewrote snapshot", again, e)
	}
	request, _ := workspaceHTTP(t, p)
	for _, test := range []struct {
		method, path string
		body         Obj
	}{
		{"GET", "/api/api-collections/" + id, nil}, {"GET", "/api/api-collections/" + id + "/versions", nil}, {"GET", "/api/api-collections/" + id + "/versions/1", nil}, {"GET", "/api/api-collections/" + id + "/export?format=openapi", nil},
		{"PUT", "/api/api-collections/" + id + "/settings", Obj{"revision": 2, "name": "绕过"}}, {"DELETE", "/api/api-collections/" + id, nil}, {"POST", "/api/api-collections/" + id + "/debug", Obj{"path": "/v2", "method": "POST"}},
		{"GET", "/api/workflows/" + str(workflow["id"]) + "/plans", nil}, {"GET", "/api/workflows/" + str(workflow["id"]) + "/export", nil}, {"POST", "/api/workflows/" + str(workflow["id"]) + "/publish", Obj{"revision": 1}},
		{"GET", "/api/plans/" + planID + "/workflows", nil},
	} {
		code, _, raw := request(test.method, test.path, test.body, map[string]string{"Authorization": "Bearer admin-secret"})
		if code != 404 {
			t.Fatalf("cross-project endpoint %s %s: %d %s", test.method, test.path, code, raw)
		}
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(state["projects"])) != 2 || strings.Contains(string(encoded(state)), "encrypted-only") {
		t.Fatal("portable project or encryption missing")
	}
	dest := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "restored"))
	if e = dest.importState(clone(state)); e != nil {
		t.Fatal(e)
	}
	restored, e := dest.forProject(projectID).workspaceVersions("api_collections", id, 1)
	if e != nil || restored["project_id"] != projectID {
		t.Fatal("version ownership lost in restore", restored, e)
	}
	if _, e = dest.forProject(defaultProjectID).workspaceGet("api_collections", id); e == nil {
		t.Fatal("restored workspace crossed project")
	}
	var before, after string
	p.DB.QueryRow("SELECT payload FROM api_collection_versions WHERE object_id=? AND revision=1", id).Scan(&before)
	dest.DB.QueryRow("SELECT payload FROM api_collection_versions WHERE object_id=? AND revision=1", id).Scan(&after)
	if before != after {
		t.Fatal("portable restore changed historical ciphertext")
	}
}

func TestMySQLLegacyProjectMigrationPreservesCiphertextAndIDs(t *testing.T) {
	p, plan := mysqlFixture(t)
	collection, e := p.workspaceCreate("api_collections", Obj{"name": "迁移集合", "document": Obj{"openapi": "3.1.0", "paths": Obj{}}})
	if e != nil {
		t.Fatal(e)
	}
	id := str(collection["id"])
	legacy := clone(collection)
	delete(legacy, "project_id")
	payload, e := p.sealWorkspace("api_collections", id, 1, legacy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("UPDATE api_collections SET payload=? WHERE id=?", payload, id); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("UPDATE api_collection_versions SET payload=? WHERE object_id=?", payload, id); e != nil {
		t.Fatal(e)
	}
	oldPlan := clone(plan)
	delete(oldPlan, "project_id")
	if _, e = p.DB.Exec("UPDATE plans SET data=? WHERE id=?", string(encoded(oldPlan)), plan["id"]); e != nil {
		t.Fatal(e)
	}
	forgetSchemaHistory(t, p) // This fixture represents an unversioned pre-project database.
	// The old database did not contain the later projection that depends on
	// project_id. Remove that derived column before simulating the old schema.
	if _, e = p.DB.Exec("ALTER TABLE runs DROP COLUMN summary_data"); e != nil {
		t.Fatal(e)
	}
	for _, table := range projectOwnedTables {
		if _, e = p.DB.Exec("ALTER TABLE " + table + " DROP COLUMN project_id"); e != nil {
			t.Fatal(e)
		}
	}
	migrated, e := newPlatform(p.Config)
	if e != nil {
		t.Fatal(e)
	}
	defer migrated.Close()
	var saved string
	if e = migrated.DB.QueryRow("SELECT payload FROM api_collections WHERE id=?", id).Scan(&saved); e != nil || saved != payload {
		t.Fatal("migration rewrote encrypted bytes", e)
	}
	for _, table := range []string{"api_collections", "plans"} {
		var data Obj
		if table == "plans" {
			data, e = migrated.forProject(defaultProjectID).get(table, str(plan["id"]))
		} else {
			data, e = migrated.forProject(defaultProjectID).workspaceGet(table, id)
		}
		if e != nil || data["project_id"] != defaultProjectID {
			t.Fatal("default migration failed", table, data, e)
		}
	}
}

func TestMySQLWorkspaceScopesBeforeLimitAndSchedulerRemainsGlobal(t *testing.T) {
	p, plan := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "分页项目"})
	if e != nil {
		t.Fatal(e)
	}
	scoped := p.forProject(str(project["id"]))
	wanted, e := scoped.workspaceCreate("api_collections", Obj{"name": "应能找到", "document": Obj{"openapi": "3.1.0", "paths": Obj{}}})
	if e != nil {
		t.Fatal(e)
	}
	// Insert 201 newer rows in another project without 201 slow encrypted HTTP calls.
	for i := 0; i < 201; i++ {
		id := newID("collection-")
		data := Obj{"id": id, "project_id": defaultProjectID, "revision": 1, "name": "其他项目", "description": "", "created_at": now(), "updated_at": now(), "document": Obj{}, "client_state": Obj{}}
		payload, e := p.sealWorkspace("api_collections", id, 1, data)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.DB.Exec("INSERT INTO api_collections(id,revision,deleted,created_at,updated_at,payload,project_id) VALUES(?,1,0,?,?,?,?)", id, data["created_at"], data["updated_at"], payload, defaultProjectID); e != nil {
			t.Fatal(e)
		}
	}
	list, e := scoped.workspaceList("api_collections")
	if e != nil || number(list["total"]) != 1 || obj(arr(list["items"])[0])["id"] != wanted["id"] {
		t.Fatal("workspace limited before scoping", list, e)
	}
	runFixture(t, p, plan, "default-global")
	ownPlan, e := scoped.createPlan(Obj{"name": "另一个项目", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	if run, created, e := scoped.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, scoped), "name": "run", "plan_id": ownPlan["id"], "workers": []any{"worker-2"}, "properties": Obj{}, "notes": ""}, "other-global"); e != nil || !created {
		t.Fatal(run, created, e)
	}
	all, e := p.runList(nil)
	if e != nil || len(all) != 2 {
		t.Fatal("scheduler lost project runs", all, e)
	}
	own, e := scoped.runList(nil)
	if e != nil || len(own) != 1 {
		t.Fatal("request mutated scheduler scope", own, e)
	}
}

func TestMySQLPortableRestoreRejectsCrossProjectAssociations(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "恢复校验"})
	if e != nil {
		t.Fatal(e)
	}
	scoped := p.forProject(str(project["id"]))
	plan, e := scoped.createPlan(Obj{"name": "项目脚本", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	runFixture(t, scoped, plan, "restore-relation")
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	row := obj(arr(state["runs"])[0])
	raw, _ := stateData(row["data"])
	run, _ := decode([]byte(raw))
	run["project_id"] = defaultProjectID
	row["project_id"] = defaultProjectID
	row["data"] = string(encoded(run))
	dest := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "invalid-restore"))
	if e = dest.importState(state); e == nil {
		t.Fatal("restore accepted a run referencing a plan from another project")
	}
	var count int
	if e = dest.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&count); e != nil || count != 0 {
		t.Fatal("failed restore left partial rows", count, e)
	}
}

func TestMySQLProjectReportsAndConcurrentRequestScope(t *testing.T) {
	p, defaultPlan := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "并发项目"})
	if e != nil {
		t.Fatal(e)
	}
	projectID := str(project["id"])
	scoped := p.forProject(projectID)
	plan, e := scoped.createPlan(Obj{"name": "隔离脚本", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	run := runFixture(t, scoped, plan, "report-scope")
	report := Obj{"id": "report-project", "run_id": run["id"], "project_id": projectID, "status": "READY", "created_at": now()}
	if _, e = p.DB.Exec("INSERT INTO reports(id,run_id,status,created_at,data,project_id) VALUES(?,?,?,?,?,?)", report["id"], run["id"], report["status"], report["created_at"], string(encoded(report)), projectID); e != nil {
		t.Fatal(e)
	}
	request, _ := workspaceHTTP(t, p)
	for _, suffix := range []string{"", "/download", "/html/index.html", "/analysis", "/summary"} {
		code, _, raw := request("GET", "/api/reports/"+str(report["id"])+suffix, nil, map[string]string{"Authorization": "Bearer admin-secret"})
		if code != 404 {
			t.Fatal("cross-project legacy report leaked", suffix, code, string(raw))
		}
	}
	code, _, raw := request("POST", "/api/runs/"+str(run["id"])+"/reports", Obj{}, map[string]string{"Authorization": "Bearer admin-secret"})
	if code != 404 {
		t.Fatal("retired endpoint exposed cross-project run", code, string(raw))
	}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			projectID, id := defaultProjectID, str(defaultPlan["id"])
			if index%2 == 1 {
				projectID, id = str(project["id"]), str(plan["id"])
			}
			code, _, raw := request("GET", "/api/plans/"+id, nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": projectID})
			value, _ := decode(raw)
			if code != 200 || value["project_id"] != projectID {
				t.Errorf("request scope changed: %d %v", code, value)
			}
		}(i)
	}
	wg.Wait()
	if p.ProjectID != "" {
		t.Fatal("request mutated shared scheduler Platform")
	}
}

func TestMySQLHistoricalImportAndRestoreRetainUnknownPlan(t *testing.T) {
	p, _ := mysqlFixture(t)
	root := t.TempDir()
	legacy := filepath.Join(root, "historical-run")
	if e := os.MkdirAll(filepath.Join(legacy, "html"), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(legacy, "results.jtl"), []byte("timestamp,elapsed\n1,2\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(legacy, "html", "index.html"), []byte("legacy HTML"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := p.importHistory(root)
	if e != nil || number(result["total"]) != 1 {
		t.Fatal("legacy history import failed after project migration", result, e)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	dest := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "old-plan-restore"))
	if e = dest.importState(state); e != nil {
		t.Fatal("restoration rejected original incomplete historical metadata", e)
	}
	run, e := dest.forProject(defaultProjectID).getRun("historical-run")
	if e != nil || run["metadata_incomplete"] != true || len(arr(run["reports"])) != 1 {
		t.Fatal("historical report or metadata lost", run, e)
	}
}

func TestMySQLProjectIdempotencyPreservesMaxUTF8Key(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "长幂等键"})
	if e != nil {
		t.Fatal(e)
	}
	scoped := p.forProject(str(project["id"]))
	plan, e := scoped.createPlan(Obj{"name": "脚本", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, scoped), "name": "队列", "plan_id": plan["id"], "workers": []any{"worker-1"}}
	key := strings.Repeat("😀", 200)
	first, created, e := scoped.createRun(payload, key)
	if e != nil || !created {
		t.Fatal("maximum UTF-8 key rejected after project prefix", first, e)
	}
	second, created, e := scoped.createRun(payload, key)
	if e != nil || created || first["id"] != second["id"] {
		t.Fatal("project idempotency replay failed", second, e)
	}
}

func TestMySQLNativeDownloadProjectQueryIsReadOnlyAndFailClosed(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "原生下载"})
	if e != nil {
		t.Fatal(e)
	}
	projectID := str(project["id"])
	scoped := p.forProject(projectID)
	plan, e := scoped.createPlan(Obj{"name": "下载脚本", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	run := legacyRunFixture(t, scoped, plan, "native-download")
	if _, e = scoped.updateRun(str(run["id"]), Obj{"status": "STOPPED", "finished_at": now()}); e != nil {
		t.Fatal(e)
	}
	rawDir := filepath.Join(p.Config.ResultsDir, str(run["id"]), "raw")
	if e = os.MkdirAll(rawDir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(rawDir, "results.jtl"), []byte("exact-jtl"), 0600); e != nil {
		t.Fatal(e)
	}
	report := Obj{"id": "report-native-query", "run_id": run["id"], "project_id": projectID, "status": "READY", "created_at": now()}
	if _, e = p.DB.Exec("INSERT INTO reports(id,run_id,status,created_at,data,project_id) VALUES(?,?,?,?,?,?)", report["id"], run["id"], report["status"], report["created_at"], string(encoded(report)), projectID); e != nil {
		t.Fatal(e)
	}
	htmlDir := filepath.Join(p.Config.ResultsDir, str(run["id"]), "reports", str(report["id"]), "html")
	if e = os.MkdirAll(htmlDir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(htmlDir, "index.html"), []byte("historical-html"), 0600); e != nil {
		t.Fatal(e)
	}
	request, _ := workspaceHTTP(t, p)
	auth := map[string]string{"Authorization": "Bearer admin-secret"}
	for _, test := range []struct{ path, content string }{{"/api/runs/" + str(run["id"]) + "/artifacts/results.jtl", "exact-jtl"}, {"/api/plans/" + str(plan["id"]) + "/content", "<jmeterTestPlan/>"}, {"/api/reports/" + str(report["id"]) + "/html/", "historical-html"}, {"/api/reports/" + str(report["id"]) + "/download", ""}} {
		code, headers, raw := request("GET", test.path+"?project_id="+projectID, nil, auth)
		if code != 200 || headers.Get("X-Project-ID") != projectID || (test.content != "" && string(raw) != test.content) {
			t.Fatal("native scoped download failed", test.path, code, string(raw))
		}
		if code, _, _ := request("GET", test.path+"?project_id="+defaultProjectID, nil, auth); code != 404 {
			t.Fatal("cross-project query download", test.path, code)
		}
		if code, _, _ := request("GET", test.path+"?project_id="+projectID, nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": defaultProjectID}); code != 400 {
			t.Fatal("conflicting project contexts accepted", test.path, code)
		}
	}
	for _, path := range []string{"/api/plans", "/api/runs", "/api/runs/" + str(run["id"]), "/api/runs/" + str(run["id"]) + "/artifacts"} {
		if code, _, _ := request("GET", path+"?project_id="+projectID, nil, auth); code != 400 {
			t.Fatal("query context accepted on metadata API", path, code)
		}
	}
	if code, _, _ := request("POST", "/api/runs/"+str(run["id"])+"/stop?project_id="+projectID, Obj{}, auth); code != 400 {
		t.Fatal("query project accepted on mutation", code)
	}
	if code, _, _ := request("GET", "/api/plans/"+str(plan["id"])+"/content?project_id="+projectID+"&project_id="+projectID, nil, auth); code != 400 {
		t.Fatal("duplicate query contexts accepted", code)
	}
	if _, e = p.projectUpdate(projectID, Obj{"revision": 1}, true); e != nil {
		t.Fatal(e)
	}
	if code, _, _ := request("GET", "/api/runs/"+str(run["id"])+"/artifacts/results.jtl?project_id="+projectID, nil, auth); code != 200 {
		t.Fatal("archive blocked historical download", code)
	}
}

func TestMySQLProjectsIsolationAndArchive(t *testing.T) {
	p, defaultPlan := mysqlFixture(t)
	request, _ := workspaceHTTP(t, p)
	call := func(project, method, path string, body Obj) (int, Obj) {
		code, _, raw := request(method, path, body, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": project, "Idempotency-Key": "same-key"})
		value, _ := decode(raw)
		return code, value
	}
	code, a := call("", "POST", "/api/projects", Obj{"name": "项目甲"})
	if code != 201 {
		t.Fatalf("project creation: %d %v", code, a)
	}
	code, b := call("", "POST", "/api/projects", Obj{"name": "项目乙"})
	if code != 201 {
		t.Fatal(code, b)
	}
	aid, bid := str(a["id"]), str(b["id"])
	code, plan := call(aid, "POST", "/api/plans", Obj{"name": "私有脚本", "content": "<jmeterTestPlan/>"})
	if code != 201 || plan["project_id"] != aid {
		t.Fatal(code, plan)
	}
	for _, path := range []string{"/api/plans/" + str(plan["id"]), "/api/plans/" + str(plan["id"]) + "/content", "/api/plans/" + str(plan["id"]) + "/graph", "/api/plans/" + str(plan["id"]) + "/workflows"} {
		if code, v := call(bid, "GET", path, nil); code != 404 {
			t.Fatal("cross-project read", path, code, v)
		}
	}
	if code, _ = call(aid, "GET", "/api/plans/"+str(defaultPlan["id"]), nil); code != 404 {
		t.Fatal("default data leaked")
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, p.forProject(aid)), "name": "排队任务", "plan_id": plan["id"], "workers": []any{"worker-1"}}
	code, run := call(aid, "POST", "/api/runs", payload)
	if code != 201 || run["project_id"] != aid {
		t.Fatal(code, run)
	}
	for _, suffix := range []string{"", "/log", "/artifacts"} {
		if c, v := call(bid, "GET", "/api/runs/"+str(run["id"])+suffix, nil); c != 404 {
			t.Fatal("cross run", c, v)
		}
	}
	if c, v := call(bid, "POST", "/api/runs/"+str(run["id"])+"/stop", Obj{}); c != 404 {
		t.Fatal("cross stop", c, v)
	}
	code, defaultRun := call(defaultProjectID, "POST", "/api/runs", Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, p), "name": "默认任务", "plan_id": defaultPlan["id"], "workers": []any{"worker-2"}})
	if code != 201 || defaultRun["id"] == run["id"] {
		t.Fatal("idempotency not isolated", code, defaultRun)
	}
	code, list := call(aid, "GET", "/api/runs?limit=1&offset=0", nil)
	if code != 200 || number(list["total"]) != 1 || obj(arr(list["items"])[0])["id"] != run["id"] {
		t.Fatal("filter after pagination", code, list)
	}
	code, archived := call("", "POST", "/api/projects/"+aid+"/archive", Obj{"revision": 1})
	if code != 200 || archived["archived"] != true {
		t.Fatal(code, archived)
	}
	if c, _ := call(aid, "POST", "/api/runs", payload); c != 409 {
		t.Fatal("archived project accepted run", c)
	}
	if c, v := call(aid, http.MethodGet, "/api/runs/"+str(run["id"]), nil); c != 200 {
		t.Fatal("archive lost history", c, v)
	}
}

// A query on the entry document is not inherited by relative CSS/JS/navigation.
// Project ownership must therefore be part of the report's browser base path.
func TestMySQLHistoricalHTMLKeepsProjectOnRelativeAssets(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "HTML资源隔离"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := str(project["id"])
	p = p.forProject(projectID)
	plan, err := p.createPlan(Obj{"name": "HTML fixture", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	run := legacyRunFixture(t, p, plan, "relative-report")
	report := Obj{"id": "report-relative-assets", "run_id": run["id"], "project_id": projectID, "status": "READY", "created_at": now()}
	if _, err := p.DB.Exec("INSERT INTO reports(id,run_id,status,created_at,data,project_id) VALUES(?,?,?,?,?,?)", report["id"], run["id"], report["status"], report["created_at"], string(encoded(report)), projectID); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(p.Config.ResultsDir, str(run["id"]), "reports", str(report["id"]), "html")
	files := map[string]string{"index.html": `<link href="content/site.css" rel="stylesheet"><a href="content/pages/chart.html">chart</a>`, "content/site.css": `body{background:url(images/chart.png)}`, "content/images/chart.png": "chart-bytes", "content/pages/chart.html": `<a href="../../index.html">home</a>`}
	for name, content := range files {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path string, headers map[string]string) (int, http.Header, []byte) {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		Handler{p}.ServeHTTP(rec, r)
		return rec.Code, rec.Header(), rec.Body.Bytes()
	}
	auth := map[string]string{"Authorization": "Bearer admin-secret"}
	base := "/api/projects/" + projectID + "/reports/report-relative-assets/html/"
	code, headers, _ := request("GET", "/api/reports/report-relative-assets/html/index.html?project_id="+projectID, auth)
	if code != 307 || headers.Get("Location") != base+"index.html" {
		t.Fatalf("entry must redirect to explicit project path: %d %v", code, headers)
	}
	for name, content := range files {
		code, headers, raw := request("GET", base+name, auth)
		if code != 200 || headers.Get("X-Project-ID") != projectID || string(raw) != content {
			t.Fatalf("relative file %s: %d %s", name, code, raw)
		}
		stored, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(stored) != content {
			t.Fatal("historical source changed", name, err)
		}
	}
	for _, entry := range []string{base, strings.TrimSuffix(base, "/")} {
		code, headers, raw := request("GET", entry, auth)
		if strings.HasSuffix(entry, "/") {
			if code != 200 || string(raw) != files["index.html"] {
				t.Fatal(code, string(raw))
			}
		} else if code != 307 || headers.Get("Location") != base {
			t.Fatal(code, headers)
		}
	}
	if code, _, _ := request("GET", base+"content/site.css", nil); code != 401 {
		t.Fatal("asset bypassed authentication", code)
	}
	if code, _, _ := request("GET", base+"content/site.css", map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": "project-other"}); code != 400 {
		t.Fatal("path/header scope conflict", code)
	}
	if code, _, _ := request("GET", base+"index.html?project_id=project-other", auth); code != 400 {
		t.Fatal("path/query scope conflict", code)
	}
	if code, _, _ := request("GET", strings.Replace(base, projectID, defaultProjectID, 1)+"index.html", auth); code != 404 {
		t.Fatal("cross-project asset", code)
	}
	if code, headers, raw := request("HEAD", base+"index.html", auth); code != 200 || headers.Get("Content-Length") != "91" || len(raw) != 0 {
		t.Fatal("HEAD", code, headers, len(raw))
	}
	if _, err := p.projectUpdate(projectID, Obj{"revision": 1}, true); err != nil {
		t.Fatal(err)
	}
	if code, _, raw := request("GET", base+"index.html", auth); code != 200 || string(raw) != files["index.html"] {
		t.Fatal("archived history", code, string(raw))
	}
	if code, _, _ := request("POST", base+"index.html", auth); code < 400 {
		t.Fatal("writable HTML route", code)
	}
}
