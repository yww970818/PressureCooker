package main

import (
	"fmt"
	"strings"
	"testing"
)

func collectionOverrideBody(id, value string) Obj {
	return Obj{"name": "collection", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/ping": Obj{"get": Obj{"responses": Obj{}}}}}, "client_state": Obj{"project_environment_overrides": Obj{id: Obj{"variables": []any{Obj{"name": "token", "value": value}}}}}}
}
func TestMySQLCollectionEnvironmentDeletionAndStaleRestore(t *testing.T) {
	p, _ := mysqlFixture(t)
	env, e := p.workspaceCreate("environments", Obj{"name": "test", "base_host": "http://internal.test", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	id := str(env["id"])
	a, e := p.workspaceCreate("api_collections", collectionOverrideBody(id, ""))
	if e != nil {
		t.Fatal(e)
	}
	b, e := p.workspaceCreate("api_collections", collectionOverrideBody(id, "secret"))
	if e != nil {
		t.Fatal(e)
	}
	old := clone(a)
	if _, e = p.workspaceDelete("environments", id); e != nil {
		t.Fatal(e)
	}
	for _, c := range []Obj{a, b} {
		current, e := p.workspaceGet("api_collections", str(c["id"]))
		if e != nil {
			t.Fatal(e)
		}
		if number(current["revision"]) != 2 || len(obj(obj(current["client_state"])["project_environment_overrides"])) != 0 {
			t.Fatalf("current override survived deletion: %s", encoded(current))
		}
		old["revision"] = current["revision"]
	}
	if _, e = p.workspaceUpdate("api_collections", str(a["id"]), old); e == nil {
		t.Fatal("historical override restored deleted environment")
	}
	env2, e := p.workspaceCreate("environments", Obj{"name": "test", "base_host": "http://new.internal", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	if env2["id"] == id {
		t.Fatal("environment id reused")
	}
}
func TestCollectionOverrideValidation(t *testing.T) {
	for _, bad := range []any{nil, []any{}, Obj{"environment-test": Obj{"variables": []any{Obj{"name": "base_host", "value": "http://bad"}}}}, Obj{"environment-test": Obj{"variables": []any{Obj{"name": "token", "value": 1}}}}} {
		body := collectionOverrideBody("environment-test", "")
		obj(body["client_state"])["project_environment_overrides"] = bad
		if _, e := validateWorkspace("api_collections", body); e == nil {
			t.Errorf("invalid overrides accepted: %s", encoded(Obj{"bad": bad}))
		}
	}
}
func TestCollectionBindingCompileRoundtrip(t *testing.T) {
	g := boundWorkflowGraph()
	props := obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])
	props["api_reference"] = Obj{"collection_id": "collection-one", "revision": 1, "path": "/ping", "method": "get"}
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(content, "jmeter.env.api.") || !strings.Contains(content, "jmeter.api_environment") {
		t.Fatal("collection scope metadata absent", content)
	}
	imported, e := importWorkflowGraph(content)
	if e != nil {
		t.Fatal(e)
	}
	if obj(obj(findGraphNode(imported, "HTTPSamplerProxy")["properties"])["api_reference"])["collection_id"] != "collection-one" {
		t.Fatal("reference lost")
	}
	again, e := compileWorkflow(imported)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Count(again, "jmeter.api_environment") != 1 {
		t.Fatal("metadata lost or duplicated")
	}
}

func TestMySQLCollectionEnvironmentIsolationAndRollback(t *testing.T) {
	p, _ := mysqlFixture(t)
	env, e := p.workspaceCreate("environments", Obj{"name": "test", "base_host": "http://internal.test"})
	if e != nil {
		t.Fatal(e)
	}
	id := str(env["id"])
	c, e := p.workspaceCreate("api_collections", collectionOverrideBody(id, "original"))
	if e != nil {
		t.Fatal(e)
	}
	project, e := p.projectCreate(Obj{"name": "other"})
	if e != nil {
		t.Fatal(e)
	}
	other := p.forProject(str(project["id"]))
	if _, e = other.workspaceCreate("api_collections", collectionOverrideBody(id, "foreign")); e == nil {
		t.Fatal("foreign environment accepted")
	}
	foreignEnv, e := other.workspaceCreate("environments", Obj{"name": "test", "base_host": "http://other.test"})
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := other.workspaceCreate("api_collections", collectionOverrideBody(str(foreignEnv["id"]), "other"))
	if e != nil {
		t.Fatal(e)
	}
	// Force the append-only version write to fail after the current row update.
	_, e = p.DB.Exec("INSERT INTO api_collection_versions(id,object_id,revision,created_at,payload) VALUES(?,?,?,?,?)", str(c["id"])+"-r2", c["id"], 2, now(), "test-conflict")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceDelete("environments", id); e == nil {
		t.Fatal("failed version insertion did not abort deletion")
	}
	if _, e = p.workspaceGet("environments", id); e != nil {
		t.Fatal("environment deleted despite rollback", e)
	}
	current, e := p.workspaceGet("api_collections", str(c["id"]))
	if e != nil || number(current["revision"]) != 1 {
		t.Fatal("collection update not rolled back", e, current)
	}
	if _, e = p.DB.Exec("DELETE FROM api_collection_versions WHERE object_id=? AND revision=2", c["id"]); e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceDelete("environments", id); e != nil {
		t.Fatal(e)
	}
	current, e = other.workspaceGet("api_collections", str(foreign["id"]))
	if e != nil || number(current["revision"]) != 1 {
		t.Fatal("other project collection modified", e, current)
	}
	versions, e := p.workspaceVersions("api_collections", str(c["id"]), 0)
	if e != nil || len(arr(versions["items"])) != 2 {
		t.Fatal("cleanup version missing", e, versions)
	}
}
func TestMySQLCollectionBindingRuntimeRevisionAndScopes(t *testing.T) {
	p, _ := mysqlFixture(t)
	envA, e := p.workspaceCreate("environments", Obj{"name": "A", "base_host": "http://a.internal/base", "variables": []any{Obj{"name": "token", "value": "project-token"}}})
	if e != nil {
		t.Fatal(e)
	}
	envB, e := p.workspaceCreate("environments", Obj{"name": "B", "base_host": "http://b.internal/other", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	collections := []Obj{}
	for i := 0; i < 2; i++ {
		body := collectionOverrideBody(str(envA["id"]), "")
		overrides := obj(obj(body["client_state"])[collectionOverridesKey])
		overrides[str(envA["id"])] = Obj{"variables": []any{Obj{"name": "token", "value": fmt.Sprintf("a-secret-%d", i)}, Obj{"name": "local_only", "value": ""}}}
		overrides[str(envB["id"])] = Obj{"variables": []any{Obj{"name": "token", "value": fmt.Sprintf("b-secret-%d", i)}, Obj{"name": "local_only", "value": fmt.Sprintf("local-%d", i)}}}
		c, e := p.workspaceCreate("api_collections", body)
		if e != nil {
			t.Fatal(e)
		}
		collections = append(collections, c)
	}
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"token", "local_only"}
	first := findGraphNode(g, "HTTPSamplerProxy")
	obj(first["properties"])["config"] = Obj{"method": "POST", "path": "/ping", "bodyJson": "${token}|${local_only}"}
	obj(first["properties"])["api_reference"] = Obj{"collection_id": collections[0]["id"], "revision": 1, "path": "/ping", "method": "get"}
	second := clone(first)
	second["id"] = "http-two"
	obj(second["properties"])["api_reference"] = Obj{"collection_id": collections[1]["id"], "revision": 1, "path": "/ping", "method": "get"}
	g["nodes"] = append(arr(g["nodes"]), second)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "http-two", 1))
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "two collections", "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	published, e := p.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	plan := obj(published["plan"])
	if len(arr(plan["environment_variables"])) != 0 || len(arr(plan["api_environment_bindings"])) != 2 {
		t.Fatal("scoped keys incorrectly require project values", plan)
	}
	for i, env := range []Obj{envA, envB} {
		run, _, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "scope-run", "plan_id": plan["id"], "workers": []any{"worker-1"}, "environment_id": env["id"]}, fmt.Sprintf("scoped-%d", i))
		if e != nil {
			t.Fatal(e)
		}
		for index, nodeID := range []string{str(first["id"]), "http-two"} {
			want := fmt.Sprintf("a-secret-%d", index)
			local := ""
			if i == 1 {
				want = fmt.Sprintf("b-secret-%d", index)
				local = fmt.Sprintf("local-%d", index)
			}
			internal, err := p.get("runs", str(run["id"]))
			if err != nil {
				t.Fatal(err)
			}
			props := obj(internal["properties"])
			if props[apiEnvironmentProperty(nodeID, "token")] != want || props[apiEnvironmentProperty(nodeID, "local_only")] != local {
				t.Fatal("scope merge wrong", props)
			}
		}
		public, e := p.getRun(str(run["id"]))
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(encoded(public)), "a-secret-") || strings.Contains(string(encoded(public)), "b-secret-") {
			t.Fatal("scoped secret leaked", public)
		}
		var raw string
		if e = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", run["id"]).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(raw, "secret-") || strings.Contains(raw, "local-0") {
			t.Fatal("run scope snapshot not encrypted")
		}
	}
	changed := clone(collections[0])
	obj(obj(changed["client_state"])[collectionOverridesKey])[str(envB["id"])] = Obj{"variables": []any{Obj{"name": "token", "value": "changed"}}}
	if _, e = p.workspaceUpdate("api_collections", str(changed["id"]), changed); e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceDelete("api_collections", str(collections[1]["id"])); e != nil {
		t.Fatal(e)
	}
	run, _, e := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "name": "frozen", "plan_id": plan["id"], "workers": []any{"worker-1"}, "environment_id": envB["id"]}, "frozen-history")
	if e != nil {
		t.Fatal("published source changed after collection update/delete", e)
	}
	internal, e := p.get("runs", str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if obj(internal["properties"])[apiEnvironmentProperty(str(first["id"]), "token")] != "b-secret-0" {
		t.Fatal("publication did not freeze source revision")
	}
}

func reviewCollectionGraph() Obj {
	g := boundWorkflowGraph()
	props := obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])
	props["api_reference"] = Obj{"collection_id": "collection-one", "revision": 1, "path": "/ping", "method": "get"}
	props["api_environment_variables"] = []any{"token", "term"}
	return g
}
func TestCollectionBindingImportedDetach(t *testing.T) {
	g := reviewCollectionGraph()
	c, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	imported, e := importWorkflowGraph(c)
	if e != nil {
		t.Fatal(e)
	}
	delete(obj(findGraphNode(imported, "HTTPSamplerProxy")["properties"]), "api_reference")
	c, e = compileWorkflow(imported)
	if e != nil {
		t.Fatal(e)
	}
	b, e := workflowCollectionMetadata(c)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) != 0 {
		t.Fatalf("detached plan has %d historical bindings: %s", len(b), encoded(b))
	}
	if strings.Contains(c, "jmeter.env.api.") {
		t.Fatal("detached plan still has scoped lookups")
	}
	_, names := workflowEnvironmentMetadata(c)
	if strings.Join(names, ",") != "term,token" {
		t.Fatal("detached templates lost project variable requirements", names)
	}
}
func TestCollectionBindingEscapedName(t *testing.T) {
	for _, name := range []string{"A&B", "A<B>", "A\"B"} {
		t.Run(name, func(t *testing.T) {
			g := reviewCollectionGraph()
			props := obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])
			g["environment_variables"] = []any{name}
			props["api_environment_variables"] = []any{name}
			obj(props["config"])["bodyJson"] = "${" + name + "}"
			if !validEnvironmentName(name) {
				t.Fatal("unexpected invalid name")
			}
			content, e := compileWorkflow(g)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(content, apiEnvironmentReference("http", name)) {
				t.Fatal("valid XML text name not scoped", name)
			}
			restored, e := importWorkflowGraph(content)
			if e != nil {
				t.Fatal(e)
			}
			if obj(obj(findGraphNode(restored, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] != "${"+name+"}" {
				t.Fatal("escaped template lost on import", name)
			}
		})
	}
}

func TestMySQLCollectionEnvironmentListIncludesOldestID(t *testing.T) {
	p, _ := mysqlFixture(t)
	placeholders := []string{}
	args := []any{}
	for i := 0; i < 201; i++ {
		id := fmt.Sprintf("environment-list-%03d", i)
		data := Obj{"id": id, "project_id": p.projectID(), "revision": 1, "name": fmt.Sprintf("env-%03d", i), "base_host": "http://test.internal", "variables": []any{}, "created_at": now(), "updated_at": now()}
		payload, e := p.sealWorkspace("environments", id, 1, data)
		if e != nil {
			t.Fatal(e)
		}
		placeholders = append(placeholders, "(?,1,0,?,?,?,?)")
		args = append(args, id, data["created_at"], data["updated_at"], payload, p.projectID())
	}
	if _, e := p.DB.Exec("INSERT INTO environments(id,revision,deleted,created_at,updated_at,payload,project_id) VALUES"+strings.Join(placeholders, ","), args...); e != nil {
		t.Fatal(e)
	}
	listed, e := p.workspaceList("environments")
	if e != nil {
		t.Fatal(e)
	}
	if len(arr(listed["items"])) != 201 {
		t.Fatalf("incomplete environment list: %d", len(arr(listed["items"])))
	}
	body := collectionOverrideBody("environment-list-000", "oldest-valid")
	collection, e := p.workspaceCreate("api_collections", body)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceUpdate("api_collections", str(collection["id"]), collection); e != nil {
		t.Fatal("valid oldest environment override rejected", e)
	}
}

func TestCollectionBindingDetachedCandidatesUseOnlyExecutableTemplates(t *testing.T) {
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	props := obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])
	props["api_environment_variables"] = []any{"unused_token"}
	props["config"] = Obj{"path": "/constant", "method": "GET"}
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	_, names := workflowEnvironmentMetadata(content)
	if len(names) != 0 {
		t.Error("constant detached node requires unused collection key", names)
	}
	// One hundred and one detached nodes all use the same original variable name;
	// unused per-collection candidates and duplicate uses must not consume budget.
	nodes := arr(g["nodes"])
	edges := arr(g["edges"])
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("detached-%03d", i)
		node := graphNode(id, "HTTPSamplerProxy", Obj{"method": "POST", "path": "/constant", "bodyJson": "${A&B}|${A&B}"})
		obj(node["properties"])["api_environment_variables"] = []any{"A&B", "unused_token", fmt.Sprintf("unused_%d", i)}
		nodes = append(nodes, node)
		edges = append(edges, graphEdge("group", id, i+1))
	}
	g["nodes"] = nodes
	g["edges"] = edges
	g["environment_variables"] = []any{"explicit_global"}
	content, e = compileWorkflow(g)
	if e != nil {
		t.Fatal("duplicate detached uses incorrectly exceed budget", e)
	}
	_, names = workflowEnvironmentMetadata(content)
	if strings.Join(names, ",") != "A&B,explicit_global" {
		t.Fatal("wrong original project requirements", names)
	}
	bindings, e := workflowCollectionMetadata(content)
	if e != nil || len(bindings) != 0 {
		t.Fatal("detached nodes retained collection source", e, bindings)
	}
}
