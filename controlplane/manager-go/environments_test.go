package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func boundWorkflowGraph() Obj {
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"token", "term"}
	c := obj(obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])["config"])
	c["protocol"] = "https"
	c["domain"] = "old.invalid"
	c["port"] = "9443"
	c["path"] = "/users/${id}?q=${term}"
	c["bodyJson"] = `{"token":"${token}","host":"${base_host}"}`
	return g
}

func TestEnvironmentBindingCompileAndReimport(t *testing.T) {
	g := boundWorkflowGraph()
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"${__P(jmeter.env.protocol,)}", "${__P(jmeter.env.host,)}", "${__P(jmeter.env.port,)}", "${__P(jmeter.env.base_path,)}", "${__P(jmeter.env.base_host,)}", "jmeter.env.var."} {
		if !strings.Contains(content, want) {
			t.Errorf("missing runtime binding %s", want)
		}
	}
	if strings.Contains(content, "old.invalid") || strings.Contains(content, ">9443<") {
		t.Fatal("environment preview address persisted", content)
	}
	imported, e := importWorkflowGraph(content)
	if e != nil {
		t.Fatal(e)
	}
	if imported["environment_binding"] != "base_host" {
		t.Fatal("binding lost on import", imported)
	}
	cfg := obj(obj(findGraphNode(imported, "HTTPSamplerProxy")["properties"])["config"])
	if cfg["path"] != "/users/${id}?q=${term}" {
		t.Fatal("own request path changed", cfg)
	}
	again, e := compileWorkflow(imported)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(again, "${__P(jmeter.env.base_path,)}") != 1 {
		t.Fatal("duplicated base path", again)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	p := &Platform{Config: Config{PlansDir: root}}
	if e := os.MkdirAll(filepath.Join(p.Config.PlansDir, "snapshots"), 0755); e != nil {
		t.Fatal(e)
	}
	plan, e := p.preparePlan(Obj{"name": "bound", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	if plan["requires_environment"] != true {
		t.Fatal("plan binding not detected", plan)
	}
	fixed, e := p.preparePlan(Obj{"name": "fixed", "content": "<jmeterTestPlan/>"})
	if e != nil {
		t.Fatal(e)
	}
	if fixed["requires_environment"] != false {
		t.Fatal("fixed plan requires environment", fixed)
	}
}

func TestMySQLProjectEnvironmentCRUDSnapshotAndIdempotency(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	code, env := call("POST", "/api/environments", Obj{"name": "测试", "base_host": "https://[2001:db8::1]:8443/api/v1/", "variables": []any{Obj{"name": "token", "value": "environment-secret"}, Obj{"name": "term", "value": "值"}}})
	if code != 201 {
		t.Fatalf("create env: %d %s", code, encoded(env))
	}
	id := str(env["id"])
	var raw string
	if e := p.DB.QueryRow("SELECT payload FROM environments WHERE id=?", id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(raw, "environment-secret") || !strings.HasPrefix(raw, "gcm1:") {
		t.Fatal("environment not encrypted")
	}
	code, list := call("GET", "/api/environments", nil)
	if code != 200 || number(list["total"]) != 1 {
		t.Fatal(code, list)
	}
	other, e := p.projectCreate(Obj{"name": "other"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.forProject(str(other["id"])).workspaceGet("environments", id); e == nil {
		t.Fatal("cross project environment readable")
	}
	content, e := compileWorkflow(boundWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	plan, e := p.createPlan(Obj{"name": "bound", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "bound run", "plan_id": plan["id"], "workers": []any{"worker-1", "worker-2", "worker-3", "worker-4", "worker-5", "worker-6"}}
	if _, _, e = p.createRun(payload, "missing-env"); e == nil {
		t.Fatal("bound plan accepts missing environment")
	}
	payload["environment_id"] = id
	run, created, e := p.createRun(payload, "env-once")
	if e != nil || !created {
		t.Fatal(run, created, e)
	}
	snapshot := obj(run["environment_snapshot"])
	if snapshot["id"] != id || snapshot["name"] != "测试" || number(snapshot["revision"]) != 1 || snapshot["host"] != "2001:db8::1" || snapshot["base_path"] != "/api/v1" {
		t.Fatal("incomplete snapshot", snapshot)
	}
	internal, e := p.get("runs", str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	props := obj(internal["properties"])
	if props["jmeter.env.port"] != "8443" || props["jmeter.env.protocol"] != "https" || props["jmeter.env.var."+digest([]byte("token"))[:16]] != "environment-secret" {
		t.Fatal("runtime environment missing", props)
	}
	invalid := clone(payload)
	invalid["properties"] = Obj{"jmeter.env.host": "override"}
	if _, _, e = p.createRun(invalid, "override"); e == nil {
		t.Fatal("runtime properties overridable")
	}
	code, updated := call("PUT", "/api/environments/"+id, Obj{"name": "new", "base_host": "http://new.internal:8080/base", "variables": []any{}, "revision": 1})
	if code != 200 || number(updated["revision"]) != 2 {
		t.Fatal(code, updated)
	}
	if code, _ = call("PUT", "/api/environments/"+id, Obj{"name": "stale", "base_host": "http://stale.internal", "variables": []any{}, "revision": 1}); code != 409 {
		t.Fatal("stale env update accepted", code)
	}
	if code, _ = call("DELETE", "/api/environments/"+id, nil); code != 200 {
		t.Fatal(code)
	}
	retried, created, e := p.createRun(payload, "env-once")
	if e != nil || created || retried["id"] != run["id"] || obj(retried["environment_snapshot"])["base_host"] != snapshot["base_host"] {
		t.Fatal("retry did not reuse original snapshot", retried, created, e)
	}
	if _, _, e = p.createRun(payload, "new-after-delete"); e == nil {
		t.Fatal("deleted environment accepted")
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(state["environments"])) != 1 {
		t.Fatal("environment not portable")
	}
}

// The Redis task protocol replaces the retired HTTP agent's 128-property,
// UTF-16 and 128KiB budgets. Preserve the current public input limits and test
// the actual signed queue snapshot instead of sending new tasks to /runs.
func TestMySQLProjectEnvironmentRuntimePropertyBudget(t *testing.T) {
	p, plan := mysqlFixture(t)
	variables := []any{}
	props := Obj{}
	for i := 0; i < 100; i++ {
		variables = append(variables, Obj{"name": fmt.Sprintf("variable_%03d", i), "value": fmt.Sprintf("value-%03d", i)})
		props[fmt.Sprintf("custom_%03d", i)] = "user-value"
	}
	environment, e := p.workspaceCreate("environments", Obj{"name": "budget", "base_host": "https://budget.internal/base", "variables": variables})
	if e != nil {
		t.Fatal(e)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "budget", "plan_id": plan["id"], "environment_id": environment["id"], "properties": props}
	run, created, e := p.createRun(payload, "budget-boundary")
	if e != nil || !created || len(obj(run["properties"])) != 307 {
		t.Fatal("100 user keys plus 100 environment values and aliases must fit", created, e)
	}
	overflow := clone(payload)
	obj(overflow["properties"])["extra"] = "overflow"
	if _, created, e = p.createRun(overflow, "budget-overflow"); e == nil || created {
		t.Fatal("101 user keys accepted", e)
	}
	for _, table := range []string{"runs", "idempotency", "task_dispatches"} {
		var count int
		if e = p.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil || count != 1 {
			t.Fatalf("rejected submission persisted %s: count=%d error=%v", table, count, e)
		}
	}
	delete(obj(overflow["properties"]), "extra")
	original, created, e := p.createRun(overflow, "budget-overflow")
	if e != nil || !created {
		t.Fatal("rejected submission consumed its key", e)
	}
	if _, e = p.workspaceUpdate("environments", str(environment["id"]), Obj{"revision": environment["revision"], "name": "changed", "base_host": "https://changed.internal", "variables": variables[:99]}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceDelete("environments", str(environment["id"])); e != nil {
		t.Fatal(e)
	}
	retry, created, e := p.createRun(overflow, "budget-overflow")
	if e != nil || created || retry["id"] != original["id"] || string(encoded(retry["environment_snapshot"])) != string(encoded(original["environment_snapshot"])) || len(obj(retry["properties"])) != 307 {
		t.Fatal("retry changed the frozen environment", e)
	}
}

func TestMySQLProjectEnvironmentRuntimeValueLimits(t *testing.T) {
	p, plan := mysqlFixture(t)
	environmentID := fixtureEnvironmentID(t, p)
	for _, runes := range []int{4096, 4097} {
		payload := Obj{"name": fmt.Sprint(runes), "plan_id": plan["id"], "cluster_id": clusterID("controller-a"), "environment_id": environmentID, "properties": Obj{"emoji": strings.Repeat("😀", runes)}}
		_, created, e := p.createRun(payload, fmt.Sprint(runes))
		if runes == 4096 && (e != nil || !created) {
			t.Fatal("4096 Unicode characters rejected", e)
		}
		if runes == 4097 && (e == nil || created) {
			t.Fatal("4097 Unicode characters accepted")
		}
		_, e = p.workspaceCreate("environments", Obj{"name": fmt.Sprint(runes), "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "emoji", "value": strings.Repeat("😀", runes)}}})
		if (runes == 4096) != (e == nil) {
			t.Fatalf("environment variable boundary %d: %v", runes, e)
		}
	}
	prefix := "https://fixture.invalid/"
	for _, size := range []int{8192, 8193} {
		_, e := p.workspaceCreate("environments", Obj{"name": fmt.Sprint(size), "base_host": prefix + strings.Repeat("a", size-len(prefix))})
		if (size == 8192) != (e == nil) {
			t.Fatalf("base_host byte boundary %d: %v", size, e)
		}
	}
}

func TestMySQLProjectEnvironmentRuntimePayloadByteLimit(t *testing.T) {
	p, plan := mysqlFixture(t)
	environmentID := fixtureEnvironmentID(t, p)
	props := Obj{}
	for i := 0; i < 40; i++ {
		props[fmt.Sprintf("pad_%02d", i)] = strings.Repeat("a", 4096)
	}
	payload := Obj{"name": "queue budget", "plan_id": plan["id"], "cluster_id": clusterID("controller-a"), "environment_id": environmentID, "properties": props}
	run, created, e := p.createRun(payload, "above-retired-http-budget")
	if e != nil || !created {
		t.Fatal("valid Redis task above 128KiB rejected", e)
	}
	task := frozenDispatchTask(t, p, str(run["id"]))
	if len(encoded(task)) <= 128*1024 || task.Properties["pad_00"] != props["pad_00"] {
		t.Fatal("large properties were not preserved in the signed snapshot")
	}
	variables := []any{}
	largeProps := Obj{}
	for i := 0; i < 100; i++ {
		variables = append(variables, Obj{"name": fmt.Sprintf("env_%03d", i), "value": strings.Repeat("😀", 4096)})
		largeProps[fmt.Sprintf("user_%03d", i)] = strings.Repeat("😀", 4096)
	}
	environment, e := p.workspaceCreate("environments", Obj{"name": "large queue snapshot", "base_host": "https://fixture.invalid", "variables": variables})
	if e != nil {
		t.Fatal(e)
	}
	oversized := Obj{"name": "oversized", "plan_id": plan["id"], "cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "properties": largeProps}
	if _, created, e = p.createRun(oversized, "queue-overflow"); e == nil || created {
		t.Fatal("task snapshot above 4MiB accepted")
	} else if a, ok := e.(*APIError); !ok || a.Status != 413 || a.Code != "task_payload_too_large" {
		t.Fatal("wrong snapshot overflow error", e)
	}
	oversized["properties"] = Obj{}
	if _, created, e = p.createRun(oversized, "queue-overflow"); e != nil || !created {
		t.Fatal("rejected queue snapshot consumed its key", e)
	}
}

func TestEnvironmentBindingImportedRuntimeReferencesCannotBeDisabled(t *testing.T) {
	content, e := compileWorkflow(boundWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	for _, remove := range []bool{false, true} {
		graph, e := importWorkflowGraph(content)
		if e != nil {
			t.Fatal(e)
		}
		graph["environment_binding"] = ""
		if remove {
			delete(graph, "environment_binding")
		}
		if _, e = compileWorkflow(graph); e == nil {
			t.Fatal("disabled binding silently retained runtime references")
		} else if apiErr, ok := e.(*APIError); !ok || apiErr.Code != "invalid_environment_binding" || !strings.Contains(apiErr.Message, "固定") {
			t.Fatal("disable binding must explain fixed address is required", e)
		}
	}
	fixed := boundWorkflowGraph()
	fixed["environment_binding"] = ""
	if content, e = compileWorkflow(fixed); e != nil || strings.Contains(content, "${__P(jmeter.env.") || !strings.Contains(content, "old.invalid") {
		t.Fatal("fixed graph address must remain usable", e)
	}
}

func TestMySQLProjectEnvironmentDebugUsesHostVariablesAndSecurity(t *testing.T) {
	p, _ := mysqlFixture(t)
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v2/users/42" || r.URL.Query().Get("q") != "override" || r.Header.Get("Authorization") != "Bearer env-secret" {
			t.Errorf("debug context lost: %s %s", r.URL.String(), r.Header.Get("Authorization"))
		}
		w.Write([]byte("environment-ok"))
	}))
	defer target.Close()
	p.Config.proxyTestResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.123.0.1")}}, nil
	}
	p.Config.proxyTestDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	env, e := p.workspaceCreate("environments", Obj{"name": "debug", "base_host": "http://selected.internal:8080/api/v2", "variables": []any{Obj{"name": "term", "value": "env-value"}, Obj{"name": "token", "value": "env-secret"}}})
	if e != nil {
		t.Fatal(e)
	}
	c, e := p.workspaceCreate("api_collections", Obj{"name": "debug", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://ignored.invalid/old"}}, "security": []any{Obj{"bearer": []any{}}}, "components": Obj{"securitySchemes": Obj{"bearer": Obj{"type": "http", "scheme": "bearer", "x-scalar-secret-token": "{{token}}"}}}, "paths": Obj{"/users/{id}": Obj{"get": Obj{"parameters": []any{Obj{"in": "path", "name": "id", "required": true, "example": "42"}, Obj{"in": "query", "name": "q", "example": "{{term}}"}}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, p)
	code, result := call("POST", "/api/api-collections/"+str(c["id"])+"/debug", Obj{"target_source": "project_environment", "path": "/users/{id}", "method": "GET", "environment_id": env["id"], "variables": Obj{"term": "override"}})
	if code != 200 || result["body"] != "environment-ok" || calls != 1 {
		t.Fatal(code, result, calls)
	}
}

func TestEnvironmentBindingEditsAndLegacyVariables(t *testing.T) {
	g := boundWorkflowGraph()
	legacy := graphNode("legacy-env", "Arguments", Obj{"variables": []any{Obj{"name": "token", "value": "old-preview-secret"}, Obj{"name": "base_host", "value": "http://old.invalid/v1"}}})
	obj(legacy["properties"])["http_environment"] = true
	authored := graphNode("authored-vars", "Arguments", Obj{"variables": []any{Obj{"name": "token", "value": "authored-token"}}})
	g["nodes"] = append(arr(g["nodes"]), legacy, authored)
	g["edges"] = append(arr(g["edges"]), graphEdge("http", "legacy-env", 0), graphEdge("group", "authored-vars", 1))
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(content, "old-preview-secret") || !strings.Contains(content, "authored-token") {
		t.Fatal("legacy value persisted or authored variable overwritten", content)
	}
	imported, e := importWorkflowGraph(content)
	if e != nil {
		t.Fatal(e)
	}
	obj(obj(findGraphNode(imported, "HTTPSamplerProxy")["properties"])["config"])["path"] = "/edited/${id}?q=${term}"
	edited, e := compileWorkflow(imported)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(edited, "/edited/${id}?q=${term}") || strings.Contains(edited, "/users/${id}") {
		t.Fatal("stale metadata replaced edited path", edited)
	}
}

func TestEnvironmentBaseValidation(t *testing.T) {
	for _, value := range []string{"https://[2001:db8::1]:8443/api/v1/", "http://example.internal", "http://example.internal:8080/a%2Fb"} {
		if _, e := validateWorkspace("environments", Obj{"name": "env", "base_host": value}); e != nil {
			t.Errorf("valid base rejected %s: %v", value, e)
		}
	}
	for _, value := range []string{"/relative", "ftp://host", "http://u:p@host", "http://host?x=1", "http://host#part", "http://host:0", "http://host:65536", "http://2001:db8::1", "http://host:"} {
		if _, e := validateWorkspace("environments", Obj{"name": "env", "base_host": value}); e == nil {
			t.Errorf("invalid base accepted %s", value)
		}
	}
	for _, vars := range []any{[]any{Obj{"name": "base_host", "value": "shadow"}}, []any{Obj{"name": "a", "value": "1"}, Obj{"name": "a", "value": "2"}}, Obj{"a": "b"}} {
		if _, e := validateWorkspace("environments", Obj{"name": "env", "base_host": "http://host", "variables": vars}); e == nil {
			t.Error("invalid environment variables accepted", vars)
		}
	}
}

func TestEnvironmentBindingsDiscoveredAndExternalReferencesImported(t *testing.T) {
	for _, path := range []string{"/api/environments", "/api/environments/{id}"} {
		if obj(openAPI()["paths"])[path] == nil {
			t.Error("environment endpoint not discovered", path)
		}
	}
	g := boundWorkflowGraph()
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	roots, e := parseXMLFragment(content)
	if e != nil {
		t.Fatal(e)
	}
	var strip func(*xmlElement)
	strip = func(node *xmlElement) {
		node.removeProp(environmentBindingProp)
		node.removeProp(environmentVariablesProp)
		for _, child := range node.children() {
			strip(child)
		}
	}
	strip(roots[0])
	external := elementXML(roots[0])
	imported, e := importWorkflowGraph(external)
	if e != nil {
		t.Fatal(e)
	}
	if imported["environment_binding"] != "base_host" {
		t.Fatal("runtime property binding not recognized without metadata")
	}
	if len(arr(imported["environment_variables"])) != 2 {
		t.Fatal("runtime UDV variable names lost", imported["environment_variables"])
	}
}

func TestMySQLProjectEnvironmentsArchiveCrossProjectAndRestore(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "environment-scope"})
	if e != nil {
		t.Fatal(e)
	}
	scoped := p.forProject(str(project["id"]))
	env, e := scoped.workspaceCreate("environments", Obj{"name": "test", "base_host": "http://env.internal:8080/base", "variables": []any{Obj{"name": "token", "value": "test-secret"}, Obj{"name": "term", "value": "one"}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.workspaceGet("environments", str(env["id"])); e == nil {
		t.Fatal("default project read foreign env")
	}
	content, e := compileWorkflow(boundWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	plan, e := scoped.createPlan(Obj{"name": "bound", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "scoped-run", "plan_id": plan["id"], "workers": []any{"worker-1"}, "environment_id": env["id"]}
	run, _, e := scoped.createRun(payload, "scope-run")
	if e != nil {
		t.Fatal(e)
	}
	env2, e := scoped.workspaceCreate("environments", Obj{"name": "prod", "base_host": "https://prod.internal:9443/v2", "variables": []any{Obj{"name": "token", "value": "prod-secret"}, Obj{"name": "term", "value": "two"}}})
	if e != nil {
		t.Fatal(e)
	}
	payload2 := clone(payload)
	payload2["environment_id"] = env2["id"]
	if _, _, e = scoped.createRun(payload2, "scope-run"); e == nil {
		t.Fatal("idempotency accepted changed environment")
	}
	run2, _, e := scoped.createRun(payload2, "scope-run-2")
	if e != nil {
		t.Fatal(e)
	}
	if run2["plan_sha256"] != run["plan_sha256"] || obj(run2["environment_snapshot"])["base_host"] == obj(run["environment_snapshot"])["base_host"] {
		t.Fatal("same script did not retain two addresses")
	}
	foreign, e := p.workspaceCreate("environments", Obj{"name": "foreign", "base_host": "http://foreign.internal"})
	if e != nil {
		t.Fatal(e)
	}
	foreignPayload := clone(payload)
	foreignPayload["environment_id"] = foreign["id"]
	if _, _, e = scoped.createRun(foreignPayload, "foreign"); e == nil {
		t.Fatal("cross-project environment accepted")
	}
	if _, e = p.projectUpdate(str(project["id"]), Obj{"revision": 1}, true); e != nil {
		t.Fatal(e)
	}
	if _, _, e = scoped.createRun(payload, "after-archive"); e == nil {
		t.Fatal("archived run accepted")
	}
	if _, e = scoped.workspaceCreate("environments", Obj{"name": "archived", "base_host": "http://archived.internal"}); e == nil {
		t.Fatal("archived create accepted")
	}
	if _, e = scoped.workspaceUpdate("environments", str(env["id"]), Obj{"name": "archived", "base_host": "http://archived.internal", "revision": 1}); e == nil {
		t.Fatal("archived update accepted")
	}
	if _, e = scoped.workspaceDelete("environments", str(env["id"])); e == nil {
		t.Fatal("archived delete accepted")
	}
	if _, e = scoped.workspaceGet("environments", str(env["id"])); e != nil {
		t.Fatal("archived read rejected", e)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	dest := emptyWorkspacePlatform(t, p, t.TempDir())
	if e = dest.importState(state); e != nil {
		t.Fatal(e)
	}
	restored, e := dest.forProject(str(project["id"])).workspaceGet("environments", str(env["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if string(encoded(restored)) != string(encoded(env)) {
		t.Fatal("environment restore changed", restored)
	}
	restoredRun, e := dest.forProject(str(project["id"])).getRun(str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if obj(restoredRun["environment_snapshot"])["base_host"] != "http://env.internal:8080/base" {
		t.Fatal("restored run lost snapshot")
	}
}

func TestMySQLEnvironmentQueueUsesSubmissionSnapshot(t *testing.T) {
	p, _ := mysqlFixture(t)
	env, e := p.workspaceCreate("environments", Obj{"name": "before", "base_host": "http://before.internal:8080/base", "variables": []any{Obj{"name": "token", "value": "before-secret"}, Obj{"name": "term", "value": "before-term"}}})
	if e != nil {
		t.Fatal(e)
	}
	content, e := compileWorkflow(boundWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	plan, e := p.createPlan(Obj{"name": "bound", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	run, _, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "queue", "plan_id": plan["id"], "workers": []any{"worker-1"}, "environment_id": env["id"]}, "queue-snapshot")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceUpdate("environments", str(env["id"]), Obj{"revision": 1, "name": "after", "base_host": "http://after.internal:9090/new", "variables": []any{}}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceDelete("environments", str(env["id"])); e != nil {
		t.Fatal(e)
	}
	task := frozenDispatchTask(t, p, str(run["id"]))
	props := task.Properties
	if props["jmeter.env.host"] != "before.internal" || props["jmeter.env.port"] != "8080" || props["jmeter.env.base_path"] != "/base" || props[environmentProperty("token")] != "before-secret" {
		t.Fatal("queue snapshot re-read changed environment")
	}

}

func TestEnvironmentBindingLegacyBasePathIsAppliedOnce(t *testing.T) {
	g := boundWorkflowGraph()
	c := obj(obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])["config"])
	c["path"] = "/preview/v1/users/42?q=keep"
	local := graphNode("legacy", "Arguments", Obj{"variables": []any{Obj{"name": "base_host", "value": "http://old.invalid:8080/preview/v1"}, Obj{"name": "token", "value": "old-secret"}}})
	obj(local["properties"])["http_environment"] = true
	g["nodes"] = append(arr(g["nodes"]), local)
	g["edges"] = append(arr(g["edges"]), graphEdge("http", "legacy", 0))
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(content, "/preview/v1/users") || !strings.Contains(content, environmentBasePathRef+"/users/42?q=keep") {
		t.Fatal("legacy preview base path duplicated", content)
	}
}

func TestMySQLProjectEnvironmentSensitiveValuesRedactedInHistoryAndLog(t *testing.T) {
	p, plan := mysqlFixture(t)
	peer := runnerObjectPeer(t)
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveBucket = "test-environment-secrets"
	p.Config.ArchiveAccessKey = "test-access"
	p.Config.ArchiveSecretKey = "test-secret"
	env, e := p.workspaceCreate("environments", Obj{"name": "secrets", "base_host": "https://secret-test.internal", "variables": []any{Obj{"name": "api_token", "value": "sensitive-env-token"}, Obj{"name": "password", "value": "sensitive-env-password"}, Obj{"name": "term", "value": "visible-term"}}})
	if e != nil {
		t.Fatal(e)
	}
	run, _, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "secrets", "plan_id": plan["id"], "workers": []any{"worker-1"}, "environment_id": env["id"]}, "sensitive-env")
	if e != nil {
		t.Fatal(e)
	}
	internal, e := p.get("runs", str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if obj(internal["properties"])[environmentProperty("api_token")] != "sensitive-env-token" {
		t.Fatal("scheduler lost original secret")
	}
	for _, name := range []string{"api_token", "password"} {
		if obj(run["properties"])[environmentProperty(name)] != "***" {
			t.Errorf("hashed property not redacted: %s", name)
		}
	}
	for _, value := range arr(obj(run["environment_snapshot"])["variables"]) {
		pair := obj(value)
		if sensitiveRE.MatchString(str(pair["name"])) && pair["value"] != "***" {
			t.Error("snapshot secret not redacted", pair["name"])
		}
	}
	if obj(run["properties"])[environmentProperty("term")] != "visible-term" {
		t.Error("non-sensitive property hidden")
	}
	// Current Redis runs read archived logs from immutable object receipts.
	// Keep this privacy fixture local and terminal; no publisher is started.
	logText := "token=sensitive-env-token password=sensitive-env-password term=visible-term\n"
	store, e := p.objectStore()
	if e != nil {
		t.Fatal(e)
	}
	ref, e := store.Put(context.Background(), "runs/"+str(run["id"])+"/console.log", strings.NewReader(logText), int64(len(logText)), digest([]byte(logText)), "text/plain")
	if e != nil {
		t.Fatal(e)
	}
	ref.Name = "console.log"
	if _, e = p.updateRun(str(run["id"]), Obj{"status": "COMPLETED", "finished_at": now(), "raw_archive": Obj{"files": []any{dtoObject(ref)}}}); e != nil {
		t.Fatal(e)
	}
	log, e := p.runLog(str(run["id"]), 200)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(str(log["text"]), "sensitive-env-") || !strings.Contains(str(log["text"]), "visible-term") {
		t.Fatal("environment log redaction failed", log)
	}
	list, e := p.listRuns("", "", 20, 0)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded(list)), "sensitive-env-token") || strings.Contains(string(encoded(list)), "sensitive-env-password") {
		t.Fatal("history list leaks secret")
	}
}

func TestMySQLProjectEnvironmentNamesUniqueConcurrentAndSoftDelete(t *testing.T) {
	p, _ := mysqlFixture(t)
	create := func(name string) (Obj, error) {
		return p.workspaceCreate("environments", Obj{"name": name, "base_host": "https://unique.internal"})
	}
	results := make(chan error, 2)
	ids := make(chan string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, e := create("same")
			if e == nil {
				ids <- str(env["id"])
			}
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	close(ids)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if a, ok := e.(*APIError); ok && a.Status == 409 {
			conflict++
		} else {
			t.Errorf("unexpected error: %v", e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("same-name race must create one row: success=%d conflict=%d", success, conflict)
	}
	id := ""
	for next := range ids {
		id = next
	}
	other, e := create("other")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceUpdate("environments", str(other["id"]), Obj{"name": "same", "base_host": "https://unique.internal", "revision": 1}); e == nil {
		t.Fatal("rename to existing name accepted")
	}
	if _, e = p.workspaceUpdate("environments", id, Obj{"name": "same", "base_host": "https://updated.internal", "revision": 1}); e != nil {
		t.Fatal("own name update rejected", e)
	}
	project, e := p.projectCreate(Obj{"name": "other-project"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.forProject(str(project["id"])).workspaceCreate("environments", Obj{"name": "same", "base_host": "https://unique.internal"}); e != nil {
		t.Fatal("cross-project name rejected", e)
	}
	if _, e = p.workspaceDelete("environments", id); e != nil {
		t.Fatal(e)
	}
	if _, e = create("same"); e != nil {
		t.Fatal("soft deletion did not release name", e)
	}
}

func TestMySQLProjectEnvironmentRunStorageEncryptedAndRestore(t *testing.T) {
	p, plan := mysqlFixture(t)
	env, e := p.workspaceCreate("environments", Obj{"name": "encrypted-run", "base_host": "https://private-environment.internal/api", "variables": []any{Obj{"name": "token", "value": "sealed-run-secret"}}})
	if e != nil {
		t.Fatal(e)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "sealed", "plan_id": plan["id"], "workers": []any{"worker-1"}, "environment_id": env["id"], "properties": Obj{"threads": "2"}}
	run, _, e := p.createRun(payload, "sealed-run")
	if e != nil {
		t.Fatal(e)
	}
	id := str(run["id"])
	assertEncrypted := func() {
		t.Helper()
		var raw string
		if e := p.DB.QueryRow("SELECT data FROM runs WHERE id=?", id).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(raw, "sealed-run-secret") || strings.Contains(raw, "private-environment.internal") || !strings.Contains(raw, "gcm1:") {
			t.Fatal("run environment persisted in plaintext")
		}
	}
	assertEncrypted()
	internal, e := p.get("runs", id)
	if e != nil || obj(internal["properties"])[environmentProperty("token")] != "sealed-run-secret" || obj(internal["properties"])["threads"] != "2" {
		t.Fatal("internal run did not decrypt", e)
	}
	list, e := p.runList(nil)
	if e != nil || obj(obj(list[0])["properties"])[environmentProperty("token")] != "sealed-run-secret" {
		t.Fatal("scheduler list did not decrypt", e)
	}
	if _, e = p.updateRun(id, Obj{"status": "PREPARING"}); e != nil {
		t.Fatal(e)
	}
	assertEncrypted()
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded(state)), "sealed-run-secret") {
		t.Fatal("portable state leaked environment secret")
	}
	archive := filepath.Join(t.TempDir(), "archive")
	if _, e = p.backup(archive); e != nil {
		t.Fatal(e)
	}
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	if _, e = restoreArchive(archive, restoredRoot); e != nil {
		t.Fatal(e)
	}
	restoredState, e := loadJSONFile(filepath.Join(restoredRoot, "state", "manager.json"))
	if e != nil {
		t.Fatal(e)
	}
	dest := emptyWorkspacePlatform(t, p, restoredRoot)
	if e = dest.importState(restoredState); e != nil {
		t.Fatal(e)
	}
	restored, e := dest.get("runs", id)
	if e != nil || obj(restored["properties"])[environmentProperty("token")] != "sealed-run-secret" || restored["status"] != "INTERRUPTED" {
		t.Fatal("restore broke encrypted environment", e)
	}
	wrong := *p
	wrong.Config.SessionKey = "wrong-session-key"
	if _, e = wrong.get("runs", id); e == nil {
		t.Fatal("wrong key silently loaded run")
	}
	if _, e = wrong.runList(nil); e == nil {
		t.Fatal("wrong key silently listed run")
	}
	if _, e = wrong.updateRun(id, Obj{"status": "RUNNING"}); e == nil {
		t.Fatal("wrong key silently mutated run")
	}
	if _, e = p.workspaceDelete("environments", str(env["id"])); e != nil {
		t.Fatal(e)
	}
	again, created, e := p.createRun(payload, "sealed-run")
	if e != nil || created || again["id"] != id {
		t.Fatal("encrypted idempotent replay failed", created, e)
	}
	// Plain legacy environment runs remain readable and become sealed on mutation.
	internal["status"] = "QUEUED"
	if _, e = p.DB.Exec("UPDATE runs SET data=? WHERE id=?", string(encoded(internal)), id); e != nil {
		t.Fatal(e)
	}
	if old, e := p.get("runs", id); e != nil || obj(old["properties"])[environmentProperty("token")] != "sealed-run-secret" {
		t.Fatal("old plaintext run incompatible", e)
	}
	if _, e = p.updateRun(id, Obj{"status": "RUNNING"}); e != nil {
		t.Fatal(e)
	}
	assertEncrypted()
}

func TestEnvironmentRunStorageAuthenticationAndFixedCompatibility(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "run-storage-test-key"}}
	original := Obj{"id": "run-auth", "project_id": "project-auth", "environment_id": "environment-auth", "status": "QUEUED", "properties": Obj{"threads": "2", "jmeter.env.host": "private.internal"}, "environment_snapshot": Obj{"id": "environment-auth", "project_id": "project-auth", "name": "private", "variables": []any{Obj{"name": "token", "value": "private-token"}}}}
	stored, e := p.runForStorage(original)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded(stored)), "private.internal") || strings.Contains(string(encoded(stored)), "private-token") {
		t.Fatal("environment storage is plaintext")
	}
	if obj(original["properties"])["jmeter.env.host"] != "private.internal" {
		t.Fatal("storage sealing mutated active run")
	}
	decoded, e := p.openStoredRun(clone(stored))
	if e != nil || string(encoded(decoded)) != string(encoded(original)) {
		t.Fatal("storage roundtrip lost run", e)
	}
	for _, field := range []string{"id", "project_id", "environment_id"} {
		tampered := clone(stored)
		tampered[field] = "tampered"
		if _, e = p.openStoredRun(tampered); e == nil {
			t.Error("run storage context not authenticated", field)
		}
	}
	malformed := clone(stored)
	malformed[runEnvironmentEnvelope] = ""
	if _, e = p.openStoredRun(malformed); e == nil {
		t.Error("empty envelope accepted")
	}
	fixed := Obj{"id": "fixed", "properties": Obj{"threads": "2"}, "status": "QUEUED"}
	saved, e := p.runForStorage(fixed)
	if e != nil || string(encoded(saved)) != string(encoded(fixed)) {
		t.Fatal("fixed legacy storage changed", e)
	}
}
