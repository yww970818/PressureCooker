package main

import (
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
