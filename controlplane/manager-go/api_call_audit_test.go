package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Only persistence is stubbed: import, frozen workspace decoding, request
// construction and DNS-pinned HTTP transport all execute the real code.
type auditDebugRecord struct{ id, payload string }

var auditDebugRecords sync.Map

type auditDebugDriver struct{}
type auditDebugConn struct{ record auditDebugRecord }

func (auditDebugDriver) Open(name string) (driver.Conn, error) {
	v, ok := auditDebugRecords.Load(name)
	if !ok {
		return nil, fmt.Errorf("missing audit fixture")
	}
	return &auditDebugConn{v.(auditDebugRecord)}, nil
}
func (c *auditDebugConn) Prepare(string) (driver.Stmt, error) { return nil, io.EOF }
func (c *auditDebugConn) Close() error                        { return nil }
func (c *auditDebugConn) Begin() (driver.Tx, error)           { return nil, io.EOF }
func (c *auditDebugConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "FROM api_collections"):
		rows := &previewRows{columns: []string{"revision", "payload", "project_id"}}
		if len(args) == 2 && args[0].Value == c.record.id && args[1].Value == defaultProjectID {
			rows.values = []driver.Value{int64(1), c.record.payload, defaultProjectID}
		}
		return rows, nil
	case strings.Contains(query, "FROM projects"):
		return &previewRows{columns: []string{"data"}, values: []driver.Value{string(encoded(Obj{"id": defaultProjectID, "archived": false}))}}, nil
	}
	return nil, fmt.Errorf("unexpected audit fixture query")
}
func init() { sql.Register("api-debug-audit", auditDebugDriver{}) }
func auditDebugPlatform(t *testing.T, target *httptest.Server, collection Obj) *Platform {
	t.Helper()
	p := proxyPlatform(t, target)
	collection["id"] = "collection-call-audit"
	collection["revision"] = 1
	collection["project_id"] = defaultProjectID
	payload, e := p.sealWorkspace("api_collections", str(collection["id"]), 1, collection)
	if e != nil {
		t.Fatal(e)
	}
	key := newID("audit-")
	auditDebugRecords.Store(key, auditDebugRecord{str(collection["id"]), payload})
	db, e := sql.Open("api-debug-audit", key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(); auditDebugRecords.Delete(key) })
	p.DB = &Database{DB: db}
	return p
}

func TestCollectionDebugRetainsImportedRepeatedHeadersAndWireBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if got := r.Header.Values("X-Repeat"); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
			t.Errorf("repeated case-insensitive header values changed: %v", got)
		}
		if got := r.Header.Values("Cookie"); !reflect.DeepEqual(got, []string{"sid=synthetic-first", "token=synthetic-second"}) {
			t.Errorf("imported business Cookie fields changed: %v", got)
		}
		if r.Header.Get("Referer") != "https://example.invalid/ref" || r.Header.Get("User-Agent") != "audit-agent" {
			t.Error("business header lost")
		}
		if r.Method != "POST" || r.URL.RawQuery != "tag=one&tag=two" || string(raw) != "a=one&a=two+three" {
			t.Error("query/form bytes changed", r.Method, r.URL.RawQuery, string(raw))
		}
		w.WriteHeader(401)
		io.WriteString(w, `{"code":8001,"message":"synthetic authentication error"}`)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	collection, e := parseCollectionImport(Obj{"format": "curl", "content": `curl 'http://service.internal:` + u.Port() + `/request?tag=one&tag=two' -H 'X-Repeat: one' -H 'x-repeat: two' -H 'X-Repeat: three' -H 'Cookie: sid=synthetic-first' -H 'Cookie: token=synthetic-second' -H 'Referer: https://example.invalid/ref' -A 'audit-agent' --data-raw 'a=one&a=two+three'`})
	if e != nil {
		t.Fatal(e)
	}
	p := auditDebugPlatform(t, target, collection)
	result, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "POST"})
	if e != nil {
		t.Fatal(e)
	}
	if number(result["status"]) != 401 || result["body"] != `{"code":8001,"message":"synthetic authentication error"}` {
		t.Fatal("business HTTP/status/body changed", result)
	}
}

func TestCollectionDebugOperationHeadersOverridePathCaseInsensitively(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Values("X-Scoped"); !reflect.DeepEqual(got, []string{"child-one", "child-two"}) {
			t.Errorf("operation headers did not replace path headers in order: %v", got)
		}
		if r.Header.Get("X-Inherited") != "keep" {
			t.Error("unrelated path header lost")
		}
		if r.Method != "HEAD" {
			t.Errorf("HEAD changed to %s", r.Method)
		}
		w.Header().Set("Content-Length", "123")
		w.WriteHeader(204)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	header := func(name, value string) Obj { return Obj{"in": "header", "name": name, "example": value} }
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port()}}, "paths": Obj{"/request": Obj{"parameters": []any{header("X-Scoped", "parent-one"), header("x-scoped", "parent-two"), header("X-Inherited", "keep")}, "head": Obj{"parameters": []any{header("x-scoped", "child-one"), header("x-scoped", "child-two")}}}}}}
	p := auditDebugPlatform(t, target, collection)
	result, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "HEAD"})
	if e != nil {
		t.Fatal(e)
	}
	if number(result["status"]) != 204 || result["body"] != "" {
		t.Fatal("HEAD response changed", result)
	}
}

func TestCollectionDebugBasePathEnvironmentAndJSONWireText(t *testing.T) {
	raw := "{ \n \"duplicate\": 1e2, \"duplicate\": 2 }"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/base/v1/users/42" || r.URL.RawQuery != "tag=one&tag=two" || string(body) != raw || r.Header.Get("Content-Type") != "application/json" {
			t.Error("base path, environment, repeated query or JSON wire bytes changed", r.URL, string(body))
		}
		w.WriteHeader(200)
		io.WriteString(w, `{"code":8001}`)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	operation := Obj{"parameters": []any{Obj{"in": "path", "name": "id", "required": true, "example": "42"}, Obj{"in": "query", "name": "tag", "example": "one"}, Obj{"in": "query", "name": "tag", "example": "two"}}}
	rawImportBody(operation, raw, "application/json")
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port() + "/base"}}, "paths": Obj{"/{{version}}/users/{id}": Obj{"post": operation}}, "x-scalar-environments": Obj{"test": Obj{"variables": []any{Obj{"name": "version", "value": "v1"}}}}}, "client_state": Obj{"active_environment": "test"}}
	p := auditDebugPlatform(t, target, collection)
	result, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/{{version}}/users/{id}", "method": "POST", "variables": Obj{"version": "v1"}})
	if e != nil {
		t.Fatal(e)
	}
	if number(result["status"]) != 200 || result["body"] != `{"code":8001}` {
		t.Fatal("business error payload was rewritten", result)
	}
}

func TestOperationBodyKeepsNativeSerializedAndEditedFormPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		example Obj
		want    string
	}{
		{"serialized-import", Obj{"serializedValue": "a=one&a=two+three", "dataValue": "stale-data", "value": "stale-value"}, "a=one&a=two+three"},
		{"edited-data", Obj{"dataValue": "a=edited+value&a=second", "value": "stale-value"}, "a=edited+value&a=second"},
		{"edited-value", Obj{"value": "a=final%2Bvalue"}, "a=final%2Bvalue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation := Obj{"requestBody": Obj{"content": Obj{"application/x-www-form-urlencoded": Obj{"examples": Obj{"default": tc.example}}}}}
			text, media, e := operationBody(operation)
			if e != nil || text != tc.want || media != "application/x-www-form-urlencoded" {
				t.Fatal("form example precedence or bytes changed", text, media, e)
			}
		})
	}
}

func TestCollectionDebugSkipsDisabledAndEmptyOptionalParameters(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "empty=&edited=new&zero=0&flag=false&schema_default=declared" {
			t.Errorf("effective parameter selection changed: %s", r.URL.RawQuery)
		}
		if _, ok := r.Header["X-Off"]; ok {
			t.Error("disabled header sent")
		}
		if _, ok := r.Header["X-Empty"]; ok {
			t.Error("empty optional header sent")
		}
		if r.Header.Get("Cookie") != "" {
			t.Error("disabled or empty optional cookie sent")
		}
		io.WriteString(w, "selected-only")
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	authored := func(location, name string, value any, disabled any) Obj {
		ex := Obj{"value": value}
		if disabled != nil {
			ex["x-disabled"] = disabled
		}
		return Obj{"in": location, "name": name, "examples": Obj{"default": ex}}
	}
	edited := authored("query", "edited", "new", false)
	edited["example"] = "stale"
	params := []any{
		authored("query", "callqueue", "wrong-branch", true), authored("header", "X-Off", "off", true), authored("cookie", "offcookie", "off", true),
		authored("query", "q-empty", "", nil), authored("header", "X-Empty", "", nil), authored("cookie", "emptycookie", "", nil),
		authored("query", "empty", "", false), edited, authored("query", "zero", 0, nil), authored("query", "flag", false, nil),
		Obj{"in": "query", "name": "schema_default", "schema": Obj{"default": "declared"}},
	}
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port()}}, "paths": Obj{"/request": Obj{"get": Obj{"parameters": params}}}}}
	p := auditDebugPlatform(t, target, collection)
	result, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "GET"})
	if e != nil {
		t.Fatal("inactive optional parameters must not block debug", e)
	}
	if result["body"] != "selected-only" {
		t.Fatal(result)
	}
}

func TestCollectionDebugDoesNotInventOptionalSchemaValues(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("invented optional query", r.URL.RawQuery)
		}
		io.WriteString(w, "no-guesses")
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port()}}, "paths": Obj{"/request": Obj{"get": Obj{"parameters": []any{Obj{"in": "query", "name": "suggestion", "schema": Obj{"enum": []any{"not-authored"}}}, Obj{"in": "query", "name": "empty-schema", "schema": Obj{"type": "string"}}}}}}}}
	p := auditDebugPlatform(t, target, collection)
	result, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "GET"})
	if e != nil {
		t.Fatal("empty optional schema should not block a request", e)
	}
	if result["body"] != "no-guesses" {
		t.Fatal(result)
	}
}

func TestCollectionDebugDisabledPathAndMissingRequiredStayUnbound(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	for _, tc := range []struct {
		name, path, code string
		param            Obj
	}{
		{"disabled-path", "/request/{id}", "parameter_value_required", Obj{"in": "path", "name": "id", "required": true, "examples": Obj{"default": Obj{"value": "42", "x-disabled": true}}}},
		{"required-no-value", "/request", "parameter_value_required", Obj{"in": "query", "name": "required", "required": true, "schema": Obj{"type": "string"}}},
		{"active-connection-stays-forbidden", "/request", "unsupported_operation", Obj{"in": "header", "name": "Connection", "examples": Obj{"default": Obj{"value": "keep-alive", "x-disabled": false}}}},
		{"content-parameter-stays-explicitly-unsupported", "/request", "unsupported_operation", Obj{"in": "query", "name": "encoded", "content": Obj{"application/json": Obj{"example": "authored"}}}},
		{"unknown-location-stays-unsupported", "/request", "unsupported_operation", Obj{"in": "unknown", "name": "invalid", "examples": Obj{"default": Obj{"value": "off", "x-disabled": true}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port()}}, "paths": Obj{tc.path: Obj{"get": Obj{"parameters": []any{tc.param}}}}}}
			p := auditDebugPlatform(t, target, collection)
			_, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": tc.path, "method": "GET"})
			var a *APIError
			if !errors.As(e, &a) || a.Status != 400 || a.Code != tc.code {
				t.Fatalf("unsafe or unbound parameter reached execution: %v", e)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("unbound request sent %d target calls", calls)
	}
}

func TestCollectionDebugEditedURLEncodedFormRows(t *testing.T) {
	want := "tag=one&tag=+two%2B%25%7E*+%E4%B8%96%E7%95%8C&empty=&message=a%3Db%0Ac&token=env%26%3D%2B"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != want {
			t.Errorf("native form rows wire text changed: got %q want %q", string(raw), want)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("form media type changed")
		}
		io.WriteString(w, "form-ok")
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	rows := []any{Obj{"name": "tag", "value": "one"}, Obj{"name": "ignored", "value": "no", "isDisabled": true}, Obj{"name": "tag", "value": " two+%~* 世界"}, Obj{"name": "empty", "value": ""}, Obj{"name": "message", "value": "a=b\nc"}, Obj{"name": "", "value": "blank name skipped"}, Obj{"name": "token", "value": "{{token}}"}}
	operation := Obj{"requestBody": Obj{"content": Obj{"application/x-www-form-urlencoded": Obj{"examples": Obj{"default": Obj{"value": rows}}}}}}
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port()}}, "paths": Obj{"/request": Obj{"post": operation}}, "x-scalar-environments": Obj{"test": Obj{"variables": []any{Obj{"name": "token", "value": "env&=+"}}}}}, "client_state": Obj{"active_environment": "test"}}
	p := auditDebugPlatform(t, target, collection)
	result, e := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/request", "method": "POST", "variables": Obj{"token": "env&=+"}})
	if e != nil {
		t.Fatal("native edited form rows rejected", e)
	}
	if result["body"] != "form-ok" {
		t.Fatal(result)
	}
	for _, media := range []string{"text/plain", "multipart/form-data"} {
		bad := Obj{"requestBody": Obj{"content": Obj{media: Obj{"examples": Obj{"default": Obj{"value": rows}}}}}}
		if _, _, e := operationBody(bad); e == nil {
			t.Fatalf("guessed native row encoding for %s", media)
		}
	}
}

func TestPostmanExportDoesNotEncodeNativeFormTemplatesAsRawBody(t *testing.T) {
	operation := Obj{"requestBody": Obj{"content": Obj{"application/x-www-form-urlencoded": Obj{"examples": Obj{"default": Obj{"value": []any{Obj{"name": "token", "value": "{{token}}"}}}}}}}}
	collection := Obj{"name": "template form", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "https://example.invalid"}}, "paths": Obj{"/request": Obj{"post": operation}}}}
	_, e := exportPostman(collection)
	var a *APIError
	if !errors.As(e, &a) || a.Status != 400 || a.Code != "unsupported_operation" {
		t.Fatalf("export must not silently percent-encode native form environment templates: %v", e)
	}
}
