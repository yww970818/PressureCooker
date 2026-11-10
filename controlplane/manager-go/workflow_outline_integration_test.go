package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestMySQLWorkflowOutlineReadsExactRevisionAndIsProjectScoped(t *testing.T) {
	p, _ := mysqlFixture(t)
	graph := basicWorkflowGraph()
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = "revision one body"
	created, e := p.workspaceCreate("workflows", Obj{"name": "lazy revision", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	id := str(created["id"])
	read := func(path, project string, status int) Obj {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/workflows/"+id+path, nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		r.Header.Set("X-Project-ID", project)
		out := httptest.NewRecorder()
		Handler{p}.ServeHTTP(out, r)
		if out.Code != status {
			t.Fatalf("read status %d wanted %d", out.Code, status)
		}
		var value Obj
		if e := json.Unmarshal(out.Body.Bytes(), &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	outline := read("?view=outline", defaultProjectID, 200)
	if number(outline["revision"]) != 1 || obj(findGraphNode(obj(outline["graph"]), "HTTPSamplerProxy")["properties"])["editor_lazy"] != true {
		t.Fatal("outline is not pinned to a complete revision")
	}
	update := clone(created)
	obj(obj(findGraphNode(obj(update["graph"]), "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = "revision two body"
	if _, e := p.workspaceUpdate("workflows", id, update); e != nil {
		t.Fatal(e)
	}
	var ciphertext string
	if e := p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", id).Scan(&ciphertext); e != nil {
		t.Fatal(e)
	}
	nodes := read("/nodes?revision=1&ids=http", defaultProjectID, 200)
	if str(nodes["workflow_id"]) != id || number(nodes["revision"]) != 1 || len(arr(nodes["nodes"])) != 1 || obj(obj(obj(arr(nodes["nodes"])[0])["properties"])["config"])["bodyJson"] != "revision one body" {
		t.Fatal("node details did not retain the requested revision")
	}
	full := read("/versions/1?view=editor", defaultProjectID, 200)
	if number(full["revision"]) != 1 || obj(obj(findGraphNode(obj(full["graph"]), "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] != "revision one body" {
		t.Fatal("full details did not retain the requested revision")
	}
	current := read("", defaultProjectID, 200)
	if number(current["revision"]) != 2 || obj(obj(findGraphNode(obj(current["graph"]), "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] != "revision two body" {
		t.Fatal("normal API changed")
	}
	read("/nodes?revision=1&ids=http", "project-other", 404)
	read("/versions/1?view=editor", "project-other", 404)
	read("?view=outline", "project-other", 404)
	read("/nodes?ids=http", defaultProjectID, 400)
	read("/nodes?revision=1&ids=missing", defaultProjectID, 404)
	var after string
	if e := p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", id).Scan(&after); e != nil || after != ciphertext {
		t.Fatal("detail reads changed persisted payload", e)
	}
}
