package main

import (
	"context"
	"strings"
	"testing"
)

func customConfigFixture() Obj {
	return Obj{"kind": "config", "name": "自定义消息", "version": "1.0.0", "protocols": []any{"websocket"}, "parser": Obj{"format": "json", "rootPath": "$.messages", "fields": Obj{"text": "$.text", "sender": "$['sender-name']"}}, "presentation": Obj{"layout": "chat", "accent": "#3b82f6"}}
}

func TestCustomInterfacePluginValidationAndSafePaths(t *testing.T) {
	definition, e := validateInterfacePluginDefinition(customConfigFixture(), "custom-test")
	if e != nil || definition["id"] != "custom-test" {
		t.Fatal(definition, e)
	}
	for _, path := range []string{"$", "$.body.items[12].text", "$[\"body.text\"][0]['message']"} {
		if !validInterfacePluginPath(path) {
			t.Fatalf("safe path rejected: %s", path)
		}
	}
	for _, path := range []string{"$.constructor", "$['__proto__']", "$[\"prototype\"]", "$..text", "$[*]", "$.items[99999]", "$.a()", "$.a;alert(1)"} {
		if validInterfacePluginPath(path) {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
	for _, mutate := range []func(Obj){
		func(d Obj) { d["url"] = "https://example.invalid/plugin.js" },
		func(d Obj) { d["capabilities"] = []any{"send"} },
		func(d Obj) { d["kind"] = "builtin" },
		func(d Obj) { d["name"] = 1 },
		func(d Obj) { d["protocols"] = []any{"http"} },
		func(d Obj) { obj(d["parser"])["rootPath"] = "$.__proto__" },
		func(d Obj) { obj(d["presentation"])["accent"] = "url(https://example.invalid)" },
		func(d Obj) { d["code"] = Obj{"language": "javascript", "source": "function parse(){}"} },
	} {
		d := customConfigFixture()
		mutate(d)
		if _, e := validateInterfacePluginDefinition(d, "custom-test"); e == nil {
			t.Fatal("invalid definition accepted", d)
		}
	}
	code := Obj{"kind": "code", "name": "代码消息", "version": "1.0.0", "protocols": []any{"websocket"}, "code": Obj{"language": "javascript", "source": "function parse(frame){return [{kind:'text',text:frame.data}]}"}}
	if _, e := validateInterfacePluginDefinition(code, "custom-code"); e != nil {
		t.Fatal(e)
	}
	obj(code["code"])["source"] = strings.Repeat("x", 64<<10)
	if _, e := validateInterfacePluginDefinition(code, "custom-code"); e == nil {
		t.Fatal("unbounded source accepted")
	}
}

func TestCustomInterfacePluginSealingAndMetadata(t *testing.T) {
	p := &Platform{Config: Config{SessionKey: "fixture-only"}}
	d, e := validateInterfacePluginDefinition(Obj{"kind": "code", "name": "private parser", "version": "1.0.0", "protocols": []any{"websocket"}, "code": Obj{"language": "javascript", "source": "function parse(){return [{kind:'text',text:'private-source'}]}"}}, "custom-code")
	if e != nil {
		t.Fatal(e)
	}
	record, e := p.sealInterfacePluginDefinition("project-a", d)
	if e != nil || strings.Contains(string(encoded(record)), "private-source") {
		t.Fatal("source not sealed", e)
	}
	opened, e := p.openInterfacePluginDefinition("project-a", "custom-code", record)
	if e != nil || obj(opened["code"])["source"] != obj(d["code"])["source"] {
		t.Fatal("sealed source lost", e)
	}
	if _, e := p.openInterfacePluginDefinition("project-b", "custom-code", record); e == nil {
		t.Fatal("cross-project payload accepted")
	}
	snapshot := interfacePluginSnapshot("project-a", Obj{"revision": 3, "installed": []any{"custom-code"}, "definitions": Obj{"custom-code": record}})
	raw := string(encoded(snapshot))
	if strings.Contains(raw, "private-source") || strings.Contains(raw, "payload") || !strings.Contains(raw, "content_sha256") || !strings.Contains(raw, "sandbox-code") {
		t.Fatal("unsafe metadata", raw)
	}
}

func TestMySQLCustomInterfacePluginsLifecycleIsolationAndCAS(t *testing.T) {
	p, _ := mysqlFixture(t)
	ctx := context.Background()
	created, e := p.createInterfacePlugin(ctx, Obj{"revision": 0, "definition": customConfigFixture(), "installed": true})
	if e != nil || number(created["revision"]) != 1 {
		t.Fatal(created, e)
	}
	id := str(created["created_id"])
	read, e := p.getInterfacePluginDefinition(ctx, id)
	if e != nil || obj(read["definition"])["kind"] != "config" {
		t.Fatal(read, e)
	}
	if read["content_sha256"] != interfacePluginMetadata(obj(read["definition"]))["content_sha256"] {
		t.Fatal("definition metadata hash missing or stale")
	}
	other, e := p.projectCreate(Obj{"name": "other"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.forProject(str(other["id"])).getInterfacePluginDefinition(ctx, id); e == nil {
		t.Fatal("cross-project definition visible")
	}
	d := obj(read["definition"])
	d["name"] = "changed"
	if _, e := p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 0, "definition": d}); e == nil {
		t.Fatal("stale edit accepted")
	}
	if _, e := p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 1, "definition": d}); e == nil || !strings.Contains(e.Error(), "增加版本号") {
		t.Fatal("mutable version accepted", e)
	}
	d["version"] = "1.0.1"
	updated, e := p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 1, "definition": d})
	if e != nil || number(updated["revision"]) != 2 {
		t.Fatal(updated, e)
	}
	if _, e := p.deleteInterfacePlugin(ctx, "retired-display", Obj{"revision": 2}); e == nil {
		t.Fatal("unknown plugin deleted")
	}
	deleted, e := p.deleteInterfacePlugin(ctx, id, Obj{"revision": 2})
	if e != nil || number(deleted["revision"]) != 3 || len(arr(deleted["items"])) != 0 {
		t.Fatal(deleted, e)
	}
	if _, e := p.getInterfacePluginDefinition(ctx, id); e == nil {
		t.Fatal("deleted source readable")
	}
	_, call := workspaceHTTP(t, p)
	code, created := call("POST", "/api/interface-plugins", Obj{"revision": 3, "definition": customConfigFixture(), "installed": true})
	if code != 200 || number(created["revision"]) != 4 {
		t.Fatal("HTTP create", code, created)
	}
	id = str(created["created_id"])
	if code, value := call("GET", "/api/interface-plugins/"+id+"/definition", nil); code != 200 || obj(value["definition"])["id"] != id {
		t.Fatal("HTTP definition", code, value)
	}
	if code, value := call("DELETE", "/api/interface-plugins/"+id, Obj{"revision": 3}); code != 409 || obj(value["error"])["code"] != "revision_conflict" {
		t.Fatal("stale HTTP delete", code, value)
	}
	if code, value := call("DELETE", "/api/interface-plugins/"+id, Obj{"revision": 4}); code != 200 || number(value["revision"]) != 5 {
		t.Fatal("HTTP DELETE body not decoded", code, value)
	}
	created, e = p.createInterfacePlugin(ctx, Obj{"revision": 5, "definition": customConfigFixture(), "installed": true})
	if e != nil {
		t.Fatal(e)
	}
	id = str(created["created_id"])
	// Reserved RPC metadata can be authored and read, but none of the current
	// debug, workflow-reference, or runtime binding paths may execute it as HTTP.
	collection, e := p.workspaceCreate("api_collections", Obj{"name": "reserved gRPC", "document": Obj{"openapi": "3.1.0", "paths": Obj{"/example.Greeter/SayHello": Obj{"post": Obj{"x-pressure-protocol": Obj{"protocol": "grpc", "serialization": "protobuf", "rpc": Obj{"service": "example.Greeter", "method": "SayHello"}}}}}}})
	if e != nil {
		t.Fatal("reserved collection authoring failed", e)
	}
	ref := Obj{"collection_id": collection["id"], "revision": 1, "path": "/example.Greeter/SayHello", "method": "post"}
	graph := basicWorkflowGraph()
	obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["api_reference"] = ref
	for _, check := range []func() error{
		func() error {
			_, e := p.collectionDebug(ctx, str(collection["id"]), Obj{"target_source": "document_server", "path": ref["path"], "method": "POST"})
			return e
		},
		func() error { return p.validateAPIReferences(p.DB, graph) },
		func() error {
			_, e := p.collectionRuntimeProperties(p.DB, Obj{"api_environment_bindings": []any{Obj{"node_id": "request", "source": ref}}}, Obj{}, Obj{})
			return e
		},
	} {
		if e := check(); e == nil {
			t.Fatal("reserved operation reached execution")
		} else if _, code, _ := requestFailure(e); code != "interface_protocol_reserved" {
			t.Fatal("wrong execution rejection", e)
		}
	}
	if _, e := p.projectUpdate(defaultProjectID, Obj{"revision": 1}, true); e != nil {
		t.Fatal(e)
	}
	for _, write := range []func() (Obj, error){
		func() (Obj, error) {
			return p.createInterfacePlugin(ctx, Obj{"revision": 6, "definition": customConfigFixture(), "installed": false})
		},
		func() (Obj, error) {
			return p.saveInterfacePluginDefinition(ctx, id, Obj{"revision": 6, "definition": customConfigFixture()})
		},
		func() (Obj, error) { return p.deleteInterfacePlugin(ctx, id, Obj{"revision": 6}) },
	} {
		if _, e := write(); e == nil {
			t.Fatal("archived custom plugin changed")
		}
	}
	if _, e := p.getInterfacePluginDefinition(ctx, id); e != nil {
		t.Fatal("archived custom plugin unreadable", e)
	}
}

func TestInterfacePluginsStartEmptyAndIgnoreUnknownInstalledReferences(t *testing.T) {
	project := Obj{"interface_plugins": Obj{"revision": 7, "installed": []any{"retired-display"}, "definitions": Obj{}}}
	before := string(encoded(project))
	state, err := interfacePluginState(project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := interfacePluginSnapshot("project-a", state)
	if len(arr(snapshot["items"])) != 0 || number(snapshot["revision"]) != 7 {
		t.Fatal("unknown installations became available plugins", snapshot)
	}
	if string(encoded(project)) != before {
		t.Fatal("plugin read rewrote stored data")
	}
	fresh, err := interfacePluginState(Obj{})
	if err != nil || len(arr(interfacePluginSnapshot("project-new", fresh)["items"])) != 0 {
		t.Fatal("new project has preinstalled implementations", err)
	}
}
