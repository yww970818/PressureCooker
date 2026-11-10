package main

import (
	"context"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestInterfacePluginsDiscovered(t *testing.T) {
	paths := obj(openAPI()["paths"])
	if obj(paths["/api/interface-plugins"])["get"] == nil || obj(paths["/api/interface-plugins/{id}"])["put"] == nil {
		t.Fatal("project plugin API missing")
	}
}

func createTestInterfacePlugin(t *testing.T, p *Platform, id string) {
	t.Helper()
	d := customConfigFixture()
	d["id"] = id
	if _, err := p.createInterfacePlugin(context.Background(), Obj{"revision": 0, "definition": d, "installed": false}); err != nil {
		t.Fatal(err)
	}
}

func TestMySQLInterfacePluginsProjectIsolationCASAndPersistence(t *testing.T) {
	p, _ := mysqlFixture(t)
	a, err := p.projectCreate(Obj{"name": "插件项目甲"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.projectCreate(Obj{"name": "插件项目乙"})
	if err != nil {
		t.Fatal(err)
	}
	aid, bid := str(a["id"]), str(b["id"])
	a["other_setting"] = Obj{"keep": "original"}
	if _, err = p.DB.Exec("UPDATE projects SET data=? WHERE id=?", string(encoded(a)), aid); err != nil {
		t.Fatal(err)
	}
	request, _ := workspaceHTTP(t, p)
	call := func(project, method, path string, body Obj) (int, Obj) {
		t.Helper()
		code, _, raw := request(method, path, body, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": project})
		value, err := decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		return code, value
	}
	assertEmpty := func(project string) {
		t.Helper()
		code, value := call(project, "GET", "/api/interface-plugins", nil)
		if code != 200 || len(arr(value["items"])) != 0 || number(value["revision"]) != 0 {
			t.Fatal("fresh project catalog not empty", code, value)
		}
	}
	assertEmpty(aid)
	assertEmpty(bid)
	assertEmpty(defaultProjectID)
	createTestInterfacePlugin(t, p.forProject(aid), "custom-message")
	assertList := func(code int, value Obj, revision int, installed bool) {
		t.Helper()
		items := arr(value["items"])
		if code != 200 || value["project_id"] != aid || number(value["revision"]) != float64(revision) || len(items) != 1 {
			t.Fatal("unexpected catalog", code, value)
		}
		plugin := obj(items[0])
		if plugin["id"] != "custom-message" || plugin["renderer"] != "declarative" || plugin["installed"] != installed || !reflect.DeepEqual(arr(plugin["capabilities"]), []any{"parse", "render"}) {
			t.Fatal("unsafe plugin metadata", plugin)
		}
	}
	code, value := call(aid, "PUT", "/api/interface-plugins/custom-message", Obj{"revision": 1, "installed": true})
	assertList(code, value, 2, true)
	assertEmpty(bid)
	if code, value := call(aid, "PUT", "/api/interface-plugins/custom-message", Obj{"revision": 1, "installed": false}); code != 409 || obj(value["error"])["code"] != "revision_conflict" {
		t.Fatal("stale revision accepted", code, value)
	}
	code, value = call(aid, "PUT", "/api/interface-plugins/custom-message", Obj{"revision": 2, "installed": true})
	assertList(code, value, 2, true)
	project, err := p.projectGetQ(p.DB, aid, false)
	if err != nil || number(project["revision"]) != 1 || obj(project["other_setting"])["keep"] != "original" || project["interface_plugins"] != nil {
		t.Fatal("metadata or plugin revision changed", project, err)
	}
	if _, err = p.projectUpdate(aid, Obj{"revision": 1, "name": "新项目名称"}, false); err != nil {
		t.Fatal(err)
	}
	code, value = call(aid, "PUT", "/api/interface-plugins/custom-message", Obj{"revision": 2, "installed": false})
	assertList(code, value, 3, false)
	project, err = p.projectGetQ(p.DB, aid, false)
	if err != nil || project["name"] != "新项目名称" || number(project["revision"]) != 2 || obj(project["other_setting"])["keep"] != "original" {
		t.Fatal("independent metadata update lost", project, err)
	}
	code, value = call(aid, "GET", "/api/interface-plugins", nil)
	assertList(code, value, 3, false)
}

func TestMySQLInterfacePluginsRejectInvalidArchivedAndUnauthorizedWrites(t *testing.T) {
	p, _ := mysqlFixture(t)
	createTestInterfacePlugin(t, p, "custom-message")
	request, call := workspaceHTTP(t, p)
	const path = "/api/interface-plugins/custom-message"
	for _, method := range []string{"GET", "PUT"} {
		url := path
		if method == "GET" {
			url = "/api/interface-plugins"
		}
		if code, _, _ := request(method, url, Obj{"revision": 1, "installed": true}, nil); code != 401 {
			t.Fatal("auth bypass", method, code)
		}
	}
	sid, login, err := p.login("admin-secret")
	if err != nil {
		t.Fatal(err)
	}
	session := map[string]string{"Cookie": "jmeter_session=" + sid}
	if code, _, _ := request("PUT", path, Obj{"revision": 1, "installed": true}, session); code != 403 {
		t.Fatal("CSRF bypass", code)
	}
	session["X-CSRF-Token"] = str(login["csrf_token"])
	if code, _, raw := request("PUT", path, Obj{"revision": 1, "installed": true}, session); code != 200 {
		t.Fatal("authenticated write denied", code, string(raw))
	}
	for _, body := range []Obj{{"installed": false}, {"revision": -1, "installed": false}, {"revision": 1.5, "installed": false}, {"revision": "2", "installed": false}, {"revision": 2}, {"revision": 2, "installed": "false"}, {"revision": 2, "installed": nil}, {"revision": 2, "installed": false, "url": "https://example.invalid/plugin.js"}} {
		if code, value := call("PUT", path, body); code != 400 {
			t.Fatal("invalid mutation accepted", code, value)
		}
	}
	if code, value := call("PUT", "/api/interface-plugins/unknown-plugin", Obj{"revision": 2, "installed": true}); code != 404 {
		t.Fatal("unknown plugin accepted", code, value)
	}
	for _, tc := range []struct{ method, path string }{{"PUT", "/api/interface-plugins"}, {"POST", path}, {"GET", path}, {"PUT", path + "/extra"}} {
		if code, value := call(tc.method, tc.path, Obj{"revision": 2, "installed": true}); code != 405 {
			t.Fatal("unsupported route accepted", tc, code, value)
		}
	}
	if code, _, _ := request("GET", "/api/interface-plugins", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": "project-missing"}); code != 404 {
		t.Fatal("missing project accepted", code)
	}
	if code, value := call("GET", "/api/interface-plugins?project_id=project-missing", nil); code != 400 {
		t.Fatal("query scope accepted", code, value)
	}
	if _, err = p.projectUpdate(defaultProjectID, Obj{"revision": 1}, true); err != nil {
		t.Fatal(err)
	}
	if code, value := call("GET", "/api/interface-plugins", nil); code != 200 || obj(arr(value["items"])[0])["installed"] != true {
		t.Fatal("archived catalog not readable", code, value)
	}
	for _, installed := range []bool{true, false} {
		if code, value := call("PUT", path, Obj{"revision": 2, "installed": installed}); code != 409 || obj(value["error"])["code"] != "project_archived" {
			t.Fatal("archived write accepted", code, value)
		}
	}
}

func TestMySQLInterfacePluginsConcurrentCASHasOneWinner(t *testing.T) {
	p, _ := mysqlFixture(t)
	createTestInterfacePlugin(t, p, "custom-message")
	request, _ := workspaceHTTP(t, p)
	start := make(chan struct{})
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			code, _, _ := request(http.MethodPut, "/api/interface-plugins/custom-message", Obj{"revision": 1, "installed": true}, map[string]string{"Authorization": "Bearer admin-secret"})
			codes <- code
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	got := []int{}
	for code := range codes {
		got = append(got, code)
	}
	sort.Ints(got)
	if !reflect.DeepEqual(got, []int{200, 409}) {
		t.Fatal("concurrent writes bypassed lock", got)
	}
}

func TestMySQLInterfacePluginReadPreservesUnknownLegacyInstallations(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectGetQ(p.DB, defaultProjectID, false)
	if err != nil {
		t.Fatal(err)
	}
	project["interface_plugins"] = Obj{"revision": 7, "installed": []any{"retired-display"}, "definitions": Obj{}}
	original := string(encoded(project))
	if _, err = p.DB.Exec("UPDATE projects SET data=? WHERE id=?", original, defaultProjectID); err != nil {
		t.Fatal(err)
	}
	if err = p.transaction(func(tx *Tx) error { return p.convertExtensionRegistriesQ(tx.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
	value, err := p.listInterfacePlugins(context.Background())
	if err != nil || len(arr(value["items"])) != 0 || number(value["revision"]) != 7 {
		t.Fatal("legacy reference was activated", value, err)
	}
	var stored string
	if err = p.DB.QueryRow("SELECT data FROM projects WHERE id=?", defaultProjectID).Scan(&stored); err != nil || stored != original {
		t.Fatal("read rewrote historical plugin state", err)
	}
	if _, err = p.getInterfacePluginDefinition(context.Background(), "retired-display"); err == nil {
		t.Fatal("unknown definition became available")
	}
}

func TestUnavailablePluginInstallationsCanBeExplicitlyRemoved(t *testing.T) {
	state := Obj{"revision": 7, "installed": []any{"retired-display", "another-retired"}, "definitions": Obj{}}
	snapshot := interfacePluginSnapshot("project-a", state)
	if len(arr(snapshot["unavailable"])) != 2 || len(arr(snapshot["items"])) != 0 {
		t.Fatal("unavailable references hidden", snapshot)
	}
	if changed, err := setInterfacePluginInstalled(state, "retired-display", true); err == nil || changed {
		t.Fatal("unavailable plugin installed")
	}
	if changed, err := removeInterfacePlugin(state, "retired-display"); err != nil || !changed || !reflect.DeepEqual(arr(state["installed"]), []any{"another-retired"}) {
		t.Fatal("unavailable reference not removed", err)
	}
	if _, err := removeInterfacePlugin(state, "retired-display"); err == nil {
		t.Fatal("missing reference deleted")
	}
	if changed, err := setInterfacePluginInstalled(state, "another-retired", false); err != nil || !changed || len(arr(state["installed"])) != 0 {
		t.Fatal("unavailable reference not uninstalled", err)
	}
	if changed, err := setInterfacePluginInstalled(state, "absent", false); err == nil || changed {
		t.Fatal("absent reference uninstalled")
	}
}

func TestMySQLUnavailablePluginCleanupGuardsAndProjectIsolation(t *testing.T) {
	p, _ := mysqlFixture(t)
	other, err := p.projectCreate(Obj{"name": "另一个插件项目"})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(projectID string) {
		t.Helper()
		project, err := p.projectGetQ(p.DB, projectID, false)
		if err != nil {
			t.Fatal(err)
		}
		project["interface_plugins"] = Obj{"revision": 7, "installed": []any{"retired-display", "another-retired"}, "definitions": Obj{}}
		if _, err = p.DB.Exec("UPDATE projects SET data=? WHERE id=?", string(encoded(project)), projectID); err != nil {
			t.Fatal(err)
		}
	}
	seed(p.projectID())
	seed(str(other["id"]))
	if err = p.transaction(func(tx *Tx) error { return p.convertExtensionRegistriesQ(tx.Context(), tx) }); err != nil {
		t.Fatal(err)
	}
	_, call := workspaceHTTP(t, p)
	path := "/api/interface-plugins/retired-display"
	if code, value := call("DELETE", path, Obj{"revision": 6}); code != 409 || obj(value["error"])["code"] != "revision_conflict" {
		t.Fatal("stale cleanup accepted", code, value)
	}
	if code, value := call("PUT", path, Obj{"revision": 7, "installed": true}); code != 404 {
		t.Fatal("unavailable plugin installed", code, value)
	}
	if code, value := call("DELETE", path, Obj{"revision": 7}); code != 200 || number(value["revision"]) != 8 || !reflect.DeepEqual(arr(value["unavailable"]), []any{"another-retired"}) {
		t.Fatal("explicit cleanup failed", code, value)
	}
	untouched, err := p.forProject(str(other["id"])).listInterfacePlugins(context.Background())
	if err != nil || number(untouched["revision"]) != 7 || len(arr(untouched["unavailable"])) != 2 {
		t.Fatal("cleanup escaped project", untouched, err)
	}
	if _, err = p.projectUpdate(p.projectID(), Obj{"revision": 1}, true); err != nil {
		t.Fatal(err)
	}
	if code, value := call("DELETE", "/api/interface-plugins/another-retired", Obj{"revision": 8}); code != 409 || obj(value["error"])["code"] != "project_archived" {
		t.Fatal("archived cleanup accepted", code, value)
	}
}
