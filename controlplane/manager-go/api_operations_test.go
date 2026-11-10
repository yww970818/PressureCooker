package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMySQLCollectionImportOperationsAndDebug(t *testing.T) {
	p, _ := mysqlFixture(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Project-ID") != "" || r.Header.Get("X-CSRF-Token") != "" || r.Header.Get("Authorization") == "Bearer admin-secret" {
			t.Error("admin context leaked to debug target")
		}
		if r.URL.Query().Get("who") != "世界" {
			t.Error("query mapping", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("debug-ok"))
	}))
	defer target.Close()
	p.Config.proxyTestResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.123.0.1")}}, nil
	}
	p.Config.proxyTestDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	_, call := workspaceHTTP(t, p)
	code, collection := call("POST", "/api/api-collections/import", Obj{"format": "curl", "name": "导入请求", "content": `curl 'http://safe.internal/example?who=%E4%B8%96%E7%95%8C&tag=a&tag=b' -H 'X-Test: a quoted value' -u 'user:pass' --data-raw '{"literal":"$(touch /never-created)"}'`})
	if code != 201 {
		t.Fatalf("curl import: %d %v", code, collection)
	}
	doc := obj(collection["document"])
	op := obj(obj(obj(doc["paths"])["/example"])["post"])
	if len(arr(op["parameters"])) != 5 || !strings.Contains(string(encoded(op)), "$(touch /never-created)") {
		t.Fatal("curl parameters or literal body lost", op)
	}
	id := str(collection["id"])
	code, export := call("GET", "/api/api-collections/"+id+"/export?format=postman", nil)
	if code != 200 || len(arr(export["item"])) != 1 {
		t.Fatal("postman export", code, export)
	}
	code, roundtrip := call("POST", "/api/api-collections/import", Obj{"format": "postman", "content": string(encoded(export))})
	if code != 201 || len(obj(obj(roundtrip["document"])["paths"])) != 1 {
		t.Fatal("postman reimport", code, roundtrip)
	}
	code, collection = call("PUT", "/api/api-collections/"+id+"/requests", Obj{"revision": 1, "path": "/example", "method": "GET", "previous_path": "/example", "previous_method": "POST", "operation": Obj{"parameters": []any{Obj{"in": "query", "name": "who", "schema": Obj{"type": "string"}, "example": "{{person}}"}}}})
	if code != 200 || number(collection["revision"]) != 2 {
		t.Fatal("request save", code, collection)
	}
	code, environment := call("POST", "/api/environments", Obj{"name": "local", "base_host": "http://safe.internal"})
	if code != 201 {
		t.Fatal(code, environment)
	}
	code, collection = call("PUT", "/api/api-collections/"+id+"/environments", Obj{"revision": 2, "environment_id": environment["id"], "variables": []any{Obj{"name": "person", "value": "世界"}}})
	if code != 200 || number(collection["revision"]) != 3 {
		t.Fatal("environment override save", code, collection)
	}
	if len(obj(obj(collection["client_state"])[collectionOverridesKey])) != 1 {
		t.Fatal("override not stored by ID", collection)
	}
	code, debug := call("POST", "/api/api-collections/"+id+"/debug", Obj{"target_source": "project_environment", "path": "/example", "method": "GET", "environment_id": environment["id"]})
	if code != 200 || number(debug["status"]) != 200 || debug["body"] != "debug-ok" {
		t.Fatal("debug", code, debug)
	}
	code, collection = call("PUT", "/api/api-collections/"+id+"/settings", Obj{"revision": 3, "name": "改名"})
	if code != 200 || collection["name"] != "改名" || number(collection["revision"]) != 4 {
		t.Fatal("settings", code, collection)
	}
	if code, _ := call("PUT", "/api/api-collections/"+id+"/settings", Obj{"revision": 3, "name": "过期"}); code != 409 {
		t.Fatal("stale patch accepted", code)
	}
	for _, body := range []Obj{{"format": "curl", "content": "curl http://example.invalid; touch /never-created"}, {"format": "postman", "content": "{}"}, {"format": "apifox", "content": "{\"apifoxProject\":{\"name\":\"native\"}}"}} {
		if code, v := call("POST", "/api/api-collections/import", body); code != 400 {
			t.Fatal("unsafe/unsupported import accepted", code, v)
		}
	}
}

func TestMySQLDebugExpandsEnvironmentBeforeOpenAPIPathParameters(t *testing.T) {
	p, _ := mysqlFixture(t)
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/users/42" {
			t.Errorf("path expansion changed target: %s", r.URL.Path)
		}
		w.Write([]byte("path-ok"))
	}))
	defer target.Close()
	p.Config.proxyTestResolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.123.0.1")}}, nil
	}
	p.Config.proxyTestDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}
	collection, e := p.workspaceCreate("api_collections", Obj{"name": "路径变量", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://safe.internal"}}, "paths": Obj{"/{{version}}/users/{id}": Obj{"get": Obj{"parameters": []any{Obj{"in": "path", "name": "id", "required": true, "example": "42"}}}}}, "x-scalar-environments": Obj{"test": Obj{"variables": []any{Obj{"name": "version", "value": "v1"}}}}}, "client_state": Obj{"active_environment": "test"}})
	if e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, p)
	code, result := call("POST", "/api/api-collections/"+str(collection["id"])+"/debug", Obj{"target_source": "document_server", "path": "/{{version}}/users/{id}", "method": "GET", "variables": Obj{"version": "v1"}})
	if code != 200 || result["body"] != "path-ok" || calls != 1 {
		t.Fatal("environment path was mistaken for an unresolved OpenAPI parameter", code, result, calls)
	}
	code, result = call("POST", "/api/api-collections/"+str(collection["id"])+"/debug", Obj{"target_source": "document_server", "path": "/{{version}}/users/{id}", "method": "GET", "variables": Obj{"version": "v1", "unrelated": "x"}})
	if code != 200 || calls != 2 {
		t.Fatal("explicit variable context failed", code, result, calls)
	}
}

func TestMySQLDebugRequiresExplicitTargetAndIgnoresLegacyEnvironment(t *testing.T) {
	p, _ := mysqlFixture(t)
	c, err := p.workspaceCreate("api_collections", Obj{"name": "explicit target", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://document.invalid"}}, "paths": Obj{"/health": Obj{"get": Obj{}}}, "x-scalar-environments": Obj{"old": Obj{"variables": Obj{"token": "private"}}}}, "client_state": Obj{"active_environment": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []Obj{
		{"path": "/health", "method": "GET"},
		{"path": "/health", "method": "GET", "target_source": "document_server", "environment": "old"},
		{"path": "/health", "method": "GET", "target_source": "project_environment"},
		{"path": "/health", "method": "GET", "target_source": "document_server", "environment_id": "environment-other"},
	} {
		if _, err = p.collectionDebug(context.Background(), str(c["id"]), body); err == nil {
			t.Fatal("ambiguous target accepted", body)
		}
	}
}
