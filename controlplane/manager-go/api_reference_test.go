package main

import (
	"database/sql"
	"testing"
)

type countedReferenceQueries struct {
	queryer
	reads int
}

func (q *countedReferenceQueries) QueryRow(query string, args ...any) *sql.Row {
	q.reads++
	return q.queryer.QueryRow(query, args...)
}

func TestMySQLAPIReferenceReusesVersionsWithinOneValidation(t *testing.T) {
	p, _ := mysqlFixture(t)
	environmentID := fixtureEnvironmentID(t, p)
	body := func(name string) Obj {
		return Obj{"name": "source", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/ping": Obj{"get": Obj{"responses": Obj{"200": Obj{"description": "ok"}}}}}}, "client_state": Obj{collectionOverridesKey: Obj{environmentID: Obj{"variables": []any{Obj{"name": name, "value": "fixture"}}}}}}
	}
	collection, err := p.workspaceCreate("api_collections", body("first"))
	if err != nil {
		t.Fatal(err)
	}
	update := body("second")
	update["revision"] = 1
	if _, err = p.workspaceUpdate("api_collections", str(collection["id"]), update); err != nil {
		t.Fatal(err)
	}
	graph := Obj{"nodes": []any{}}
	for i := 0; i < 20; i++ {
		node := graphNode(newID("node-"), "HTTPSamplerProxy", nil)
		obj(node["properties"])["api_reference"] = Obj{"collection_id": collection["id"], "revision": 1 + i%2, "path": "/ping", "method": "GET"}
		graph["nodes"] = append(arr(graph["nodes"]), node)
	}
	q := &countedReferenceQueries{queryer: p.DB}
	if err = p.validateAPIReferences(q, graph); err != nil {
		t.Fatal(err)
	}
	if q.reads != 3 {
		t.Fatalf("repeated collection/revisions caused %d reads, want 3", q.reads)
	}
	for i, raw := range arr(graph["nodes"]) {
		want := "first"
		if i%2 == 1 {
			want = "second"
		}
		names := arr(obj(obj(raw)["properties"])["api_environment_variables"])
		if len(names) != 1 || names[0] != want {
			t.Fatal("source revisions aliased", names)
		}
	}
	props := obj(obj(arr(graph["nodes"])[19])["properties"])
	ref := obj(props["api_reference"])
	ref["path"] = "/missing"
	if err = p.validateAPIReferences(p.DB, graph); err == nil {
		t.Fatal("cached version skipped operation validation")
	}
	ref["path"] = "/ping"
	ref["revision"] = 3
	if err = p.validateAPIReferences(p.DB, graph); err == nil {
		t.Fatal("missing revision accepted")
	}
	ref["revision"] = 2
	props["component"] = "WebSocketSampler"
	if err = p.validateAPIReferences(p.DB, graph); err == nil {
		t.Fatal("cached version skipped protocol validation")
	}
	props["component"] = "HTTPSamplerProxy"
	if err = p.forProject("project-other").validateAPIReferences(p.DB, graph); err == nil {
		t.Fatal("source collection leaked across projects")
	}
	if _, err = p.workspaceDelete("api_collections", str(collection["id"])); err != nil {
		t.Fatal(err)
	}
	if err = p.validateAPIReferences(p.DB, graph); err == nil {
		t.Fatal("cache survived collection deletion between calls")
	}
}

func TestAPIReferenceNullMeansNoReference(t *testing.T) {
	p := &Platform{}
	for _, component := range []string{"HTTPSamplerProxy", "ThreadGroup"} {
		graph := basicWorkflowGraph()
		props := obj(findGraphNode(graph, component)["properties"])
		props["api_reference"] = nil
		if e := p.validateAPIReferences(nil, graph); e != nil {
			t.Errorf("null reference must be ignored on %s: %v", component, e)
		}
	}
	for _, value := range []any{"invalid", 42, Obj{}} {
		graph := basicWorkflowGraph()
		obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["api_reference"] = value
		if e := p.validateAPIReferences(nil, graph); e == nil {
			t.Error("non-null invalid reference accepted", value)
		}
	}
	graph := basicWorkflowGraph()
	obj(findGraphNode(graph, "ThreadGroup")["properties"])["api_reference"] = Obj{"collection_id": "collection-invalid", "revision": 1, "path": "/ping", "method": "get"}
	if e := p.validateAPIReferences(nil, graph); e == nil {
		t.Error("non-HTTP reference accepted")
	}
}

func TestMySQLAPIReferenceStandaloneAndRemovalSave(t *testing.T) {
	p, _ := mysqlFixture(t)
	graph := basicWorkflowGraph()
	props := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	props["api_reference"] = nil
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "standalone", "graph": graph})
	if e != nil {
		t.Fatal("standalone request cannot save", e)
	}
	collection, e := p.workspaceCreate("api_collections", Obj{"name": "reference", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/ping": Obj{"get": Obj{"responses": Obj{"200": Obj{"description": "ok"}}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	props["api_reference"] = Obj{"collection_id": collection["id"], "revision": 1, "path": "/ping", "method": "get"}
	workflow, e = p.workspaceUpdate("workflows", str(workflow["id"]), Obj{"name": "referenced", "revision": workflow["revision"], "graph": graph})
	if e != nil {
		t.Fatal("valid reference cannot save", e)
	}
	props["api_reference"] = nil
	workflow, e = p.workspaceUpdate("workflows", str(workflow["id"]), Obj{"name": "unlinked", "revision": workflow["revision"], "graph": graph})
	if e != nil {
		t.Fatal("removing reference cannot save", e)
	}
	if obj(findGraphNode(obj(workflow["graph"]), "HTTPSamplerProxy")["properties"])["api_reference"] != nil {
		t.Fatal("removed reference returned")
	}
	for _, value := range []any{"invalid", Obj{}} {
		props["api_reference"] = value
		if _, e = p.workspaceUpdate("workflows", str(workflow["id"]), Obj{"name": "invalid", "revision": workflow["revision"], "graph": graph}); e == nil {
			t.Error("invalid non-null reference saved", value)
		}
	}
	props["api_reference"] = nil
	obj(findGraphNode(graph, "ThreadGroup")["properties"])["api_reference"] = Obj{"collection_id": collection["id"], "revision": 1, "path": "/ping", "method": "get"}
	if _, e = p.workspaceUpdate("workflows", str(workflow["id"]), Obj{"name": "non-http", "revision": workflow["revision"], "graph": graph}); e == nil {
		t.Error("non-HTTP reference saved")
	}
}
