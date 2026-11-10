package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func workspaceHTTP(t *testing.T, p *Platform) (func(string, string, Obj, map[string]string) (int, http.Header, []byte), func(string, string, Obj) (int, Obj)) {
	t.Helper()
	server := httptest.NewServer(Handler{p})
	t.Cleanup(server.Close)
	request := func(method, path string, body Obj, headers map[string]string) (int, http.Header, []byte) {
		t.Helper()
		var input io.Reader
		if body != nil {
			input = bytes.NewReader(encoded(body))
		}
		r, e := http.NewRequest(method, server.URL+path, input)
		if e != nil {
			t.Fatal(e)
		}
		// Test clients explicitly select a project, like production clients.
		if headers != nil && !strings.Contains(path, "project_id=") {
			r.Header.Set("X-Project-ID", p.projectID())
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, response.Header, raw
	}
	bearer := func(method, path string, body Obj) (int, Obj) {
		status, _, raw := request(method, path, body, map[string]string{"Authorization": "Bearer admin-secret"})
		m, e := decode(raw)
		if e != nil {
			t.Fatal(string(raw), e)
		}
		return status, m
	}
	return request, bearer
}
func TestMySQLAPICollectionsEncryptedCRUDOCCAndVersionHistory(t *testing.T) {
	p, _ := mysqlFixture(t)
	request, call := workspaceHTTP(t, p)
	path := "/api/api-collections"
	doc := Obj{"openapi": "3.1.0", "info": Obj{"title": "集合", "version": "1.0"}, "servers": []any{Obj{"url": "http://internal.example/${environment}"}}, "paths": Obj{"/request": Obj{"post": Obj{"requestBody": Obj{"content": Obj{"application/json": Obj{"schema": Obj{"$ref": "#/components/schemas/Complex"}, "examples": Obj{"nested": Obj{"value": Obj{"keep": []any{1, true, nil, "中文"}}}}}}}}}}, "components": Obj{"schemas": Obj{"Complex": Obj{"oneOf": []any{Obj{"type": "object", "additionalProperties": true}, Obj{"type": "null"}}}}, "securitySchemes": Obj{"bearer": Obj{"type": "http", "scheme": "bearer"}}}, "x-scalar-environments": Obj{"env": Obj{"variables": Obj{"url": "private"}}}, "x-unknown-extension": Obj{"nested": []any{Obj{"anything": "preserve"}}}}
	client := Obj{"auth": Obj{"selectedSecurityScheme": "bearer", "http": Obj{"bearer": Obj{"token": "secret-business-credential"}}}, "active_environment": "env", "request_mode": "master"}
	body := Obj{"name": "API集合", "description": "完整文档", "document": doc, "client_state": client}
	if status, _, _ := request("GET", path, nil, nil); status != 401 {
		t.Fatal("anonymous collection list", status)
	}
	sid, login, e := p.login("admin-secret")
	if e != nil {
		t.Fatal(e)
	}
	session := map[string]string{"Cookie": "jmeter_session=" + sid}
	if status, _, _ := request("POST", path, body, session); status != 403 {
		t.Fatal("missing collection CSRF", status)
	}
	session["X-CSRF-Token"] = str(login["csrf_token"])
	status, _, raw := request("POST", path, body, session)
	created, e := decode(raw)
	if e != nil || status != 201 || number(created["revision"]) != 1 {
		t.Fatal(status, string(raw), e)
	}
	id := str(created["id"])
	if string(encoded(created["document"])) != string(encoded(doc)) || string(encoded(created["client_state"])) != string(encoded(client)) {
		t.Fatal("document/client_state flattened")
	}
	var encrypted string
	if e = p.DB.QueryRow("SELECT payload FROM api_collections WHERE id=?", id).Scan(&encrypted); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(encrypted, "gcm1:") || strings.Contains(encrypted, "secret-business") || strings.Contains(encrypted, "openapi") {
		t.Fatal("document persisted unencrypted")
	}
	update := clone(body)
	update["revision"] = 1
	update["name"] = "新名称"
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _ := request("PUT", path+"/"+id, update, map[string]string{"Authorization": "Bearer admin-secret"})
			results <- code
		}()
	}
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for result := range results {
		if result == 200 {
			ok++
		} else if result == 409 {
			conflict++
		} else {
			t.Fatal("unexpected OCC response", result)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("concurrent save overwritten", ok, conflict)
	}
	status, versions := call("GET", path+"/"+id+"/versions", nil)
	if status != 200 || number(versions["total"]) != 2 {
		t.Fatal(status, versions)
	}
	status, old := call("GET", path+"/"+id+"/versions/1", nil)
	if status != 200 || old["name"] != "API集合" || string(encoded(old["client_state"])) != string(encoded(client)) {
		t.Fatal("historical document/auth changed", old)
	}
	update["revision"] = 2
	delete(update, "client_state")
	status, saved := call("PUT", path+"/"+id, update)
	if status != 200 || string(encoded(saved["client_state"])) != string(encoded(client)) {
		t.Fatal("omitted client_state lost saved auth", status, saved)
	}
	if status, _ := call("DELETE", path+"/"+id, nil); status != 200 {
		t.Fatal(status)
	}
	if status, _ := call("GET", path+"/"+id, nil); status != 404 {
		t.Fatal("soft deleted collection readable", status)
	}
	if status, list := call("GET", path, nil); status != 200 || number(list["total"]) != 0 {
		t.Fatal(status, list)
	}
	var deleted int
	if e = p.DB.QueryRow("SELECT deleted FROM api_collections WHERE id=?", id).Scan(&deleted); e != nil || deleted != 1 {
		t.Fatal("not a soft delete", deleted, e)
	}
}
func TestMySQLWorkflowCRUDImportExportPublishAndRevisionGuard(t *testing.T) {
	p, _ := mysqlFixture(t)
	request, call := workspaceHTTP(t, p)
	graph := basicWorkflowGraph()
	sampler := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	sampler["config"] = Obj{"domain": "smoke-target", "port": "8080", "path": "/jmeter-smoke"}
	body := Obj{"name": "工作流", "description": "不发送请求，仅保存与发布", "graph": graph}
	if status, _, _ := request("POST", "/api/workflows", body, nil); status != 401 {
		t.Fatal("anonymous workflow create", status)
	}
	status, created := call("POST", "/api/workflows", body)
	if status != 201 || number(created["revision"]) != 1 {
		t.Fatal(status, created)
	}
	id := str(created["id"])
	status, _, exported := request("GET", "/api/workflows/"+id+"/export", nil, map[string]string{"Authorization": "Bearer admin-secret"})
	if status != 200 || !strings.Contains(string(exported), "smoke-target") {
		t.Fatal(status, string(exported))
	}
	body["revision"] = 1
	body["name"] = "已更新工作流"
	status, updated := call("PUT", "/api/workflows/"+id, body)
	if status != 200 || number(updated["revision"]) != 2 {
		t.Fatal(status, updated)
	}
	if status, _ = call("POST", "/api/workflows/"+id+"/publish", Obj{"revision": 1}); status != 409 {
		t.Fatal("stale workflow published", status)
	}
	status, published := call("POST", "/api/workflows/"+id+"/publish", Obj{"revision": 2})
	if status != 201 || str(published["managerPlanId"]) == "" {
		t.Fatal(status, published)
	}
	plan, e := p.get("plans", str(published["managerPlanId"]))
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := readLimitedFile(filepath.Join(p.Config.PlansDir, "snapshots", str(plan["sha256"])+".jmx"), workspaceLimit)
	if e != nil || digest(snapshot) != plan["sha256"] {
		t.Fatal("publication not immutable", e)
	}
	runs, e := p.runList(nil)
	if e != nil || len(runs) != 0 {
		t.Fatal("publishing unexpectedly executed a task", runs, e)
	}
	for _, importBody := range []Obj{{"name": "XML导入", "content": string(exported)}, {"name": "计划导入", "plan_id": plan["id"]}} {
		status, imported := call("POST", "/api/workflows/import", importBody)
		if status != 201 || len(arr(obj(imported["graph"])["nodes"])) != 3 {
			t.Fatal(status, imported)
		}
	}
	if status, _ = call("POST", "/api/workflows/import", Obj{"name": "不允许网络导入", "url": "http://no-network.invalid/plan.jmx"}); status != 400 {
		t.Fatal(status)
	}
	if status, _ = call("PUT", "/api/workflows/"+id, body); status != 409 {
		t.Fatal("stale update accepted", status)
	}
	if status, _ = call("DELETE", "/api/workflows/"+id, nil); status != 200 {
		t.Fatal(status)
	}
	if status, _ = call("GET", "/api/workflows/"+id, nil); status != 404 {
		t.Fatal(status)
	}
}
func emptyWorkspacePlatform(t *testing.T, source *Platform, root string) *Platform {
	t.Helper()
	c := source.Config
	c.MySQLTablePrefix = "jt_" + newID("")[:16] + "_"
	c.StateDir = filepath.Join(root, "state")
	c.PlansDir = filepath.Join(root, "plans")
	c.ResultsDir = filepath.Join(root, "results")
	p, e := newPlatform(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		for _, table := range append(append(append([]string{}, portableTables...), derivedTables...), "sessions", "instances", "schema_migrations") {
			p.DB.DB.Exec("DROP TABLE IF EXISTS " + c.MySQLTablePrefix + table)
		}
		p.Close()
	})
	return p
}
func TestMySQLWorkspaceBackupRestoreAndOldStateCompatibility(t *testing.T) {
	p, _ := mysqlFixture(t)
	collection, e := p.workspaceCreate("api_collections", Obj{"name": "备份集合", "document": Obj{"openapi": "3.1.0", "x-scalar-unknown": Obj{"schema": []any{1, true, "raw"}}}, "client_state": Obj{"auth": Obj{"token": "encrypted-backup-secret"}, "request_mode": "direct"}})
	if e != nil {
		t.Fatal(e)
	}
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "备份工作流", "graph": basicWorkflowGraph()})
	if e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(t.TempDir(), "archive")
	if _, e = p.backup(archive); e != nil {
		t.Fatal(e)
	}
	state, e := loadJSONFile(filepath.Join(archive, "state", "manager.json"))
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(state["api_collections"])) != 1 || len(arr(state["workflow_versions"])) != 1 || strings.Contains(string(encoded(state)), "encrypted-backup-secret") {
		t.Fatal("new tables missing or backup plaintext")
	}
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	if _, e = restoreArchive(archive, restoredRoot); e != nil {
		t.Fatal(e)
	}
	restored := emptyWorkspacePlatform(t, p, restoredRoot)
	restoredState, e := loadJSONFile(filepath.Join(restoredRoot, "state", "manager.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = restored.importState(restoredState); e != nil {
		t.Fatal(e)
	}
	saved, e := restored.workspaceGet("api_collections", str(collection["id"]))
	if e != nil || string(encoded(saved)) != string(encoded(collection)) {
		t.Fatal("encrypted collection failed restore", saved, e)
	}
	saved, e = restored.workspaceGet("workflows", str(workflow["id"]))
	if e != nil || string(encoded(saved)) != string(encoded(workflow)) {
		t.Fatal("workflow failed restore", saved, e)
	}
	old := clone(state)
	for _, table := range workspaceTables {
		delete(old, table)
	}
	legacyDestination := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "legacy"))
	if e = legacyDestination.importState(old); e != nil {
		t.Fatal("old state rejected", e)
	}
	list, e := legacyDestination.workspaceList("api_collections")
	if e != nil || number(list["total"]) != 0 {
		t.Fatal("old backup created new workspace rows", list, e)
	}
}
