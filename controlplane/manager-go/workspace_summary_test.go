package main

import (
	"strings"
	"testing"
)

func TestWorkspaceSummaryDoesNotReadOrReturnLargeSensitiveBodies(t *testing.T) {
	nodes := []any{}
	body := strings.Repeat("source-token-not-for-list", 170)
	for i := 0; i < 5000; i++ {
		nodes = append(nodes, Obj{"properties": Obj{"component": "HTTPSamplerProxy", "raw_xml": body, "config": Obj{"cookie": "private-cookie"}}})
	}
	input := Obj{"id": "workflow-one", "project_id": "project-one", "name": "大流程", "description": strings.Repeat("说明", 1000), "revision": 3, "graph": Obj{"nodes": nodes, "edges": []any{}}, "client_state": Obj{"cookie": "private-cookie"}}
	summary := workspaceSummary("workflows", input)
	if summary["node_count"] != 5000 || summary["request_count"] != 5000 || summary["summary"] != true {
		t.Fatal("missing small metadata counts")
	}
	raw := string(encoded(summary))
	if len(raw) > 4096 || strings.Contains(raw, "private-cookie") || strings.Contains(raw, "source-token") || summary["graph"] != nil || summary["client_state"] != nil {
		t.Fatal("full body or credentials leaked to list")
	}
	if str(input["description"]) != strings.Repeat("说明", 1000) {
		t.Fatal("summary mutated source")
	}
	collection := workspaceSummary("api_collections", Obj{"name": "集合", "document": Obj{"paths": Obj{"/a": Obj{"get": Obj{"headers": Obj{"Cookie": "private-cookie"}}, "post": Obj{}, "parameters": []any{}}}}, "client_state": Obj{"variables": []any{Obj{"name": "token", "value": "private-token"}}}})
	if collection["request_count"] != 2 || strings.Contains(string(encoded(collection)), "private-token") || collection["document"] != nil {
		t.Fatal("collection summary not isolated")
	}
}

func TestWorkspaceSummaryEncryptionIsBoundToSummaryDomain(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "fixture-key"}}
	data := Obj{"id": "workflow-one", "name": "名称", "revision": 1, "graph": Obj{"nodes": []any{}, "edges": []any{}}}
	encrypted, e := p.sealWorkspaceSummary("workflows", "workflow-one", 1, data)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(encrypted, "名称") {
		t.Fatal("plaintext metadata persisted")
	}
	opened, e := p.openWorkspaceSummary("workflows", "workflow-one", 1, encrypted)
	if e != nil || opened["name"] != "名称" {
		t.Fatal(e)
	}
	if _, e = p.openWorkspace("workflows", "workflow-one", 1, encrypted); e == nil {
		t.Fatal("summary ciphertext can replace full object")
	}
	if _, e = p.openWorkspaceSummary("workflows", "workflow-other", 1, encrypted); e == nil {
		t.Fatal("summary crossed object identity")
	}
}
