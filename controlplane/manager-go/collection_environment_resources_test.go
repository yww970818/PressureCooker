package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func resourceCollection(rows any) Obj {
	return Obj{"name": "resources", "document": Obj{"openapi": "3.1.0", "paths": Obj{}}, "client_state": Obj{"collection_environments": rows}}
}
func TestCollectionEnvironmentResourceValidation(t *testing.T) {
	valid := Obj{"id": "collection-env-a", "name": "A", "base_host": "https://example.test/base", "variables": []any{Obj{"name": "empty", "value": ""}}}
	for _, test := range []struct {
		name string
		rows any
	}{
		{"not array", Obj{}}, {"missing base", []any{Obj{"id": "collection-env-a", "name": "A", "variables": []any{}}}},
		{"duplicate name", []any{valid, Obj{"id": "collection-env-b", "name": "A", "base_host": "https://b.test", "variables": []any{}}}},
		{"invalid address", []any{Obj{"id": "collection-env-a", "name": "A", "base_host": "ftp://bad.test", "variables": []any{}}}},
		{"reserved base variable", []any{Obj{"id": "collection-env-a", "name": "A", "base_host": "https://a.test", "variables": []any{Obj{"name": "base_host", "value": "https://b.test"}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateWorkspace("api_collections", resourceCollection(test.rows)); err == nil {
				t.Fatal("invalid collection environment accepted")
			}
		})
	}
	if _, err := validateWorkspace("api_collections", resourceCollection([]any{valid})); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionEnvironmentResourceInheritance(t *testing.T) {
	parent := Obj{"id": "parent-a", "revision": 2, "base_host": "https://parent.test/base", "variables": []any{Obj{"name": "token", "value": "parent"}, Obj{"name": "empty", "value": "parent"}}}
	resource := Obj{"id": "collection-env-a", "name": "A", "parent_environment_id": "parent-a", "variables": []any{Obj{"name": "empty", "value": ""}}}
	effective, e := resolveCollectionEnvironment(resource, parent, Obj{"token": "task", "empty": "task"}, false)
	if e != nil {
		t.Fatal(e)
	}
	vars, _ := environmentVariables(effective)
	if effective["base_host"] != "https://parent.test/base" || vars["token"] != "task" || vars["empty"] != "" {
		t.Fatalf("bad merge: %v", effective)
	}
	effective, e = resolveCollectionEnvironment(resource, parent, Obj{"empty": "debug", "base_host": "https://forbidden.test"}, true)
	vars, _ = environmentVariables(effective)
	if e != nil || vars["empty"] != "debug" || effective["base_host"] != "https://parent.test/base" {
		t.Fatal("debug precedence", effective, e)
	}
	resource["base_host"] = "https://custom.test"
	effective, e = resolveCollectionEnvironment(resource, parent, nil, false)
	if e != nil || effective["base_host"] != "https://custom.test" {
		t.Fatal(e, effective)
	}
	if _, e = resolveCollectionEnvironment(resource, nil, nil, false); e == nil {
		t.Fatal("deleted parent silently replaced")
	}
}
func TestCollectionEnvironmentResourceLegacyProjectionAndTombstone(t *testing.T) {
	c := collectionOverrideBody("parent-a", "")
	parents := []any{Obj{"id": "parent-a", "name": "QA"}}
	rows := collectionEnvironmentResources(c, parents)
	if len(rows) != 1 || obj(rows[0])["id"] != "legacy-parent-a" {
		t.Fatal(rows)
	}
	obj(c["client_state"])[collectionEnvironmentsKey] = []any{}
	if len(collectionEnvironmentResources(c, parents)) != 0 {
		t.Fatal("deleted legacy entry resurrected")
	}
}
func TestMySQLCollectionEnvironmentResourceCRUD(t *testing.T) {
	p, _ := mysqlFixture(t)
	parent, e := p.workspaceCreate("environments", Obj{"name": "Parent", "base_host": "https://parent.test", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	c, e := p.workspaceCreate("api_collections", resourceCollection([]any{}))
	if e != nil {
		t.Fatal(e)
	}
	id := str(c["id"])
	receipt, e := p.collectionEnvironmentResourcesRequest(id, "", "POST", Obj{"revision": 1, "name": "Child", "parent_environment_id": parent["id"], "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	envID := str(obj(receipt["environment"])["id"])
	if envID == "" || envID == str(parent["id"]) {
		t.Fatal("unstable resource identity")
	}
	if _, e = p.collectionEnvironmentResourcesRequest(id, envID, "PUT", Obj{"revision": 1, "name": "stale", "base_host": "https://a.test"}); e == nil {
		t.Fatal("stale write accepted")
	}
	if _, e = p.workspaceDelete("environments", str(parent["id"])); e != nil {
		t.Fatal(e)
	}
	got, e := p.collectionEnvironmentResourcesRequest(id, envID, "GET", nil)
	if e != nil || got["available"] != false {
		t.Fatal("missing parent not retained", got, e)
	}
	receipt, e = p.collectionEnvironmentResourcesRequest(id, envID, "PUT", Obj{"revision": 2, "name": "Renamed", "parent_environment_id": parent["id"], "variables": []any{}})
	if e != nil {
		t.Fatal("unchanged missing parent rejected", e)
	}
	if _, e = p.collectionEnvironmentResourcesRequest(id, envID, "DELETE", Obj{"revision": obj(receipt["collection"])["revision"]}); e != nil {
		t.Fatal(e)
	}
	got, e = p.collectionEnvironmentResourcesRequest(id, "", "GET", nil)
	if e != nil || len(arr(got["items"])) != 0 {
		t.Fatal(got, e)
	}
}

func TestMySQLCollectionEnvironmentResourceScopeImportAndArchive(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, e := p.projectCreate(Obj{"name": "Environment isolation"})
	if e != nil {
		t.Fatal(e)
	}
	scoped := p.forProject(str(project["id"]))
	parent, e := scoped.workspaceCreate("environments", Obj{"name": "Foreign parent", "base_host": "https://foreign.test", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	body := resourceCollection([]any{Obj{"id": "child", "name": "Child", "parent_environment_id": parent["id"], "variables": []any{}}})
	if _, e = p.workspaceCreate("api_collections", body); e == nil {
		t.Fatal("cross-project parent accepted")
	}
	imported, e := p.workspaceCreate("api_collections", body, true)
	if e != nil {
		t.Fatal("unavailable imported parent prevented import", e)
	}
	if _, e = p.resolveCollectionEnvironmentQ(p.DB, imported, "child", nil, false); e == nil {
		t.Fatal("foreign imported parent executed")
	}
	if _, e = p.workspaceUpdate("api_collections", str(imported["id"]), imported); e != nil {
		t.Fatal("unchanged imported parent blocked save", e)
	}
	c, e := scoped.workspaceCreate("api_collections", resourceCollection([]any{}))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.collectionEnvironmentResourcesRequest(str(c["id"]), "", "GET", nil); e == nil {
		t.Fatal("foreign collection readable")
	}
	if _, e = p.projectUpdate(str(project["id"]), Obj{"revision": 1}, true); e != nil {
		t.Fatal(e)
	}
	if _, e = scoped.collectionEnvironmentResourcesRequest(str(c["id"]), "", "POST", Obj{"revision": 1, "name": "blocked", "base_host": "https://blocked.test", "variables": []any{}}); e == nil {
		t.Fatal("archived project resource created")
	}
	if _, e = scoped.collectionEnvironmentResourcesRequest(str(c["id"]), "", "GET", nil); e != nil {
		t.Fatal("archived project unreadable", e)
	}
}

func TestCollectionEnvironmentNodeBindingWithoutProjectEnvironment(t *testing.T) {
	graph := basicWorkflowGraph()
	node := findGraphNode(graph, "HTTPSamplerProxy")
	props := obj(node["properties"])
	props["config"] = Obj{"method": "POST", "path": "/ping", "bodyJson": "${token}"}
	props["collection_environment_reference"] = Obj{"collection_id": "collection-a", "revision": 2, "collection_environment_id": "collection-env-a"}
	content, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(content, apiEnvironmentAddressProperty(str(node["id"]), "host")) || !strings.Contains(content, apiEnvironmentProperty(str(node["id"]), "token")) {
		t.Fatal("node environment address/variable not independently bound")
	}
	metadata, e := workflowCollectionMetadata(content)
	if e != nil || len(metadata) != 1 || obj(obj(metadata[0])["environment_source"])["collection_environment_id"] != "collection-env-a" {
		t.Fatal("independent environment provenance lost", metadata, e)
	}
	imported, e := importWorkflowGraph(content)
	if e != nil {
		t.Fatal(e)
	}
	if obj(obj(findGraphNode(imported, "HTTPSamplerProxy")["properties"])["collection_environment_reference"])["collection_environment_id"] != "collection-env-a" {
		t.Fatal("environment binding lost on import")
	}
}

func TestCollectionEnvironmentProjectRequirementProof(t *testing.T) {
	graph := basicWorkflowGraph()
	node := findGraphNode(graph, "HTTPSamplerProxy")
	obj(node["properties"])["collection_environment_reference"] = Obj{"collection_id": "collection-a", "revision": 1, "collection_environment_id": "collection-env-a"}
	content, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	bindings, e := workflowCollectionMetadata(content)
	if e != nil {
		t.Fatal(e)
	}
	if requiresProjectEnvironment(content, bindings) {
		t.Fatal("fully bound native workflow still requires task environment")
	}
	for _, extra := range []string{
		`<UnknownSampler testclass="UnknownSampler"/>`,
		`<JSR223Sampler testclass="JSR223Sampler"><stringProp name="script">props.get('region')</stringProp></JSR223Sampler>`,
		`<ConstantTimer testclass="ConstantTimer"><stringProp name="ConstantTimer.delay">${delay}</stringProp></ConstantTimer>`,
	} {
		if !requiresProjectEnvironment(strings.Replace(content, "</jmeterTestPlan>", extra+"</jmeterTestPlan>", 1), bindings) {
			t.Fatalf("unproven remaining dependency accepted: %s", extra)
		}
	}
	second := clone(node)
	second["id"] = "other-http"
	delete(obj(second["properties"]), "collection_environment_reference")
	graph["nodes"] = append(arr(graph["nodes"]), second)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "other-http", 1))
	content, e = compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	bindings, _ = workflowCollectionMetadata(content)
	if !requiresProjectEnvironment(content, bindings) {
		t.Fatal("unbound request incorrectly proven independent")
	}
	if !requiresProjectEnvironment(`<jmeterTestPlan><hashTree><TestPlan/><hashTree><UnknownSampler testclass="UnknownSampler"/></hashTree></hashTree></jmeterTestPlan>`, []any{}) {
		t.Fatal("legacy script optional environment")
	}
}

func TestCollectionEnvironmentDebugSelectionAndExclusivity(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Value") != "debug" {
			t.Error("explicit debug override not applied")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	c := resourceCollection([]any{Obj{"id": "env-debug", "name": "Debug", "base_host": "http://service.internal:" + u.Port(), "variables": []any{Obj{"name": "value", "value": "collection"}}}})
	obj(c["document"])["paths"] = Obj{"/": Obj{"get": Obj{"parameters": []any{Obj{"name": "X-Value", "in": "header", "schema": Obj{"type": "string", "default": "{{value}}"}}}}}}
	p := auditDebugPlatform(t, target, c)
	body := Obj{"target_source": "collection_environment", "collection_environment_id": "env-debug", "path": "/", "method": "get", "variables": Obj{"value": "debug"}}
	if _, e := p.collectionDebug(context.Background(), str(c["id"]), body); e != nil {
		t.Fatal(e)
	}
	for _, change := range []Obj{{"environment_id": "other"}, {"collection_environment_id": "other"}, {"target_source": "document_server"}, {"target_source": "project_environment", "environment_id": "other"}} {
		invalid := clone(body)
		for key, value := range change {
			invalid[key] = value
		}
		if _, e := p.collectionDebug(context.Background(), str(c["id"]), invalid); e == nil {
			t.Fatal("invalid target combination accepted", change)
		}
	}
	if calls != 1 {
		t.Fatal("invalid targets sent requests", calls)
	}
}

func TestCollectionEnvironmentDetachedWebSocketTaskAddress(t *testing.T) {
	_, properties, e := environmentSnapshot(Obj{"id": "parent", "base_host": "https://parent.test/base", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	if properties["jmeter.env.ws_base_host"] != "wss://parent.test/base" {
		t.Fatal("detached WS target cannot resolve task address")
	}
}
func TestMySQLCollectionEnvironmentResourceRuntimeSnapshot(t *testing.T) {
	p, _ := mysqlFixture(t)
	parent, e := p.workspaceCreate("environments", Obj{"name": "Parent", "base_host": "https://parent.test/base", "variables": []any{Obj{"name": "token", "value": "parent-secret"}, Obj{"name": "region", "value": "parent"}}})
	if e != nil {
		t.Fatal(e)
	}
	collection, e := p.workspaceCreate("api_collections", resourceCollection([]any{Obj{"id": "collection-env-a", "name": "A", "parent_environment_id": parent["id"], "variables": []any{Obj{"name": "token", "value": "collection-secret"}}}, Obj{"id": "collection-env-b", "name": "B", "base_host": "http://other.test:8080/alternate", "variables": []any{Obj{"name": "token", "value": "other-secret"}, Obj{"name": "region", "value": "other"}}}}))
	if e != nil {
		t.Fatal(e)
	}
	graph := basicWorkflowGraph()
	first := findGraphNode(graph, "HTTPSamplerProxy")
	obj(first["properties"])["config"] = Obj{"method": "POST", "path": "/ping", "bodyJson": "${token}|${region}"}
	obj(first["properties"])["collection_environment_reference"] = Obj{"collection_id": collection["id"], "revision": 1, "collection_environment_id": "collection-env-a"}
	second := clone(first)
	second["id"] = "http-two"
	obj(obj(second["properties"])["collection_environment_reference"])["collection_environment_id"] = "collection-env-b"
	graph["nodes"] = append(arr(graph["nodes"]), second)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", "http-two", 1))
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "isolated", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	published, e := p.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	plan := obj(published["plan"])
	if plan["requires_project_environment"] != false {
		t.Fatal("not proven independent", plan)
	}
	payload := Obj{"cluster_id": clusterID("controller-a"), "name": "collection only", "plan_id": plan["id"], "workers": []any{"worker-1"}, "properties": Obj{"region": "task", "token": "task-secret"}}
	run, _, e := p.createRun(payload, "collection-only-one")
	if e != nil {
		t.Fatal(e)
	}
	internal, e := p.get("runs", str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	runtime := obj(internal["properties"])
	if runtime[apiEnvironmentAddressProperty(str(first["id"]), "host")] != "parent.test" || runtime[apiEnvironmentAddressProperty("http-two", "host")] != "other.test" || runtime[apiEnvironmentProperty(str(first["id"]), "token")] != "collection-secret" || runtime[apiEnvironmentProperty(str(first["id"]), "region")] != "task" {
		t.Fatal("incorrect isolated target precedence")
	}
	if len(arr(obj(internal["environment_snapshot"])["collection_environments"])) != 2 {
		t.Fatal("node snapshots missing")
	}
	public, e := p.getRun(str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(encoded(public)), "collection-secret") || strings.Contains(string(encoded(public)), "other-secret") {
		t.Fatal("collection secret leaked")
	}
	parent["base_host"] = "https://new-parent.test/new"
	if _, e = p.workspaceUpdate("environments", str(parent["id"]), parent); e != nil {
		t.Fatal(e)
	}
	if _, e = p.stopRun(str(run["id"]), "now"); e != nil {
		t.Fatal(e)
	}
	next, _, e := p.createRun(payload, "collection-only-two")
	if e != nil {
		t.Fatal(e)
	}
	nextInternal, _ := p.get("runs", str(next["id"]))
	if obj(nextInternal["properties"])[apiEnvironmentAddressProperty(str(first["id"]), "host")] != "new-parent.test" {
		t.Fatal("parent not continuously inherited")
	}
	retry, created, e := p.createRun(payload, "collection-only-one")
	if e != nil || created || retry["id"] != run["id"] {
		t.Fatal("idempotency snapshot changed", e)
	}
	copied := clone(payload)
	copied["copy_source_run_id"] = run["id"]
	copied["copy_environment_snapshot"] = true
	if _, e = p.stopRun(str(next["id"]), "now"); e != nil {
		t.Fatal(e)
	}
	copyRun, _, e := p.createRun(copied, "collection-copy")
	if e != nil {
		t.Fatal(e)
	}
	copyInternal, _ := p.get("runs", str(copyRun["id"]))
	if obj(copyInternal["properties"])[apiEnvironmentAddressProperty(str(first["id"]), "host")] != "parent.test" {
		t.Fatal("copy did not preserve frozen parent")
	}
	if _, e = p.workspaceDelete("environments", str(parent["id"])); e != nil {
		t.Fatal(e)
	}
	if _, e = p.stopRun(str(copyRun["id"]), "now"); e != nil {
		t.Fatal(e)
	}
	if _, _, e = p.createRun(payload, "collection-deleted-parent"); e == nil {
		t.Fatal("deleted parent used for new task")
	}
}

func TestCollectionEnvironmentWebSocketAddressBinding(t *testing.T) {
	graph := basicWorkflowGraph()
	node := findGraphNode(graph, "HTTPSamplerProxy")
	props := obj(node["properties"])
	props["component"] = "WebSocketSampler"
	props["config"] = Obj{"action": "connect", "connectionId": "channel", "url": "wss://old.test/socket/{socket_id}", "socketio": true, "handshakeUrl": "https://old.test/socket.io/1/", "timeoutMs": "1000"}
	props["collection_environment_reference"] = Obj{"collection_id": "collection-a", "revision": 1, "collection_environment_id": "collection-env-a"}
	content, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	roots, e := parseXMLFragment(content)
	if e != nil {
		t.Fatal(e)
	}
	var config string
	var walk func(*xmlElement)
	walk = func(root *xmlElement) {
		if websocketComponent(root) {
			config = websocketConfigText(root.prop(websocketConfigProp))
		}
		for _, child := range root.children() {
			walk(child)
		}
	}
	for _, root := range roots {
		walk(root)
	}
	if strings.Contains(config, "old.test") || !strings.Contains(config, apiEnvironmentAddressProperty(str(node["id"]), "ws_base_host")) {
		t.Fatal("WS address does not use selected collection target", config)
	}
	bindings, e := workflowCollectionMetadata(content)
	if e != nil || requiresProjectEnvironment(content, bindings) {
		t.Fatal("WS full binding not proven independent", e)
	}
}
