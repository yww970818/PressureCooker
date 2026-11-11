package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugQueryAuthenticationPreservesServerQuery(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/items" {
			t.Errorf("server and operation paths were not combined: %s", r.URL.Path)
		}
		if r.URL.Query().Get("tenant") != "synthetic" || r.URL.Query().Get("access_key") != "synthetic-key" {
			t.Errorf("server query or authentication lost: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	u, _ := url.Parse(target.URL)
	collection := Obj{"document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "http://service.internal:" + u.Port() + "/api?tenant=synthetic"}}, "paths": Obj{"/items": Obj{"get": Obj{}}},
		"security": []any{Obj{"key": []any{}}}, "components": Obj{"securitySchemes": Obj{"key": Obj{"type": "apiKey", "in": "query", "name": "access_key", "x-scalar-secret-token": "synthetic-key"}}}}}
	p := auditDebugPlatform(t, target, collection)
	if _, err := p.collectionDebug(context.Background(), str(collection["id"]), Obj{"target_source": "document_server", "path": "/items", "method": "get"}); err != nil {
		t.Fatal(err)
	}
}

func TestPostmanExportCombinesServerPathAndQuery(t *testing.T) {
	collection := Obj{"name": "synthetic", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "https://example.invalid/api?tenant=synthetic&tag=a&tag=b"}}, "paths": Obj{"/items": Obj{"get": Obj{"parameters": []any{Obj{"in": "query", "name": "page", "example": "2"}}}}}}}
	exported, err := exportPostman(collection)
	if err != nil {
		t.Fatal(err)
	}
	request := obj(obj(arr(exported["item"])[0])["request"])
	u, err := url.Parse(str(obj(request["url"])["raw"]))
	if err != nil || u.Path != "/api/items" || u.Query().Get("tenant") != "synthetic" || u.Query().Get("page") != "2" || len(u.Query()["tag"]) != 2 {
		t.Fatal("Postman export changed the target path or query", u, err)
	}
	imported, err := parseCollectionImport(Obj{"format": "postman", "content": exported})
	if err != nil {
		t.Fatal(err)
	}
	document := obj(imported["document"])
	operation := obj(obj(obj(document["paths"])["/api/items"])["get"])
	values := url.Values{}
	for _, raw := range arr(operation["parameters"]) {
		parameter := obj(raw)
		if parameter["in"] == "query" {
			value, err := parameterValue(parameter)
			if err != nil {
				t.Fatal(err)
			}
			values.Add(str(parameter["name"]), value)
		}
	}
	if values.Get("tenant") != "synthetic" || values.Get("page") != "2" || len(values["tag"]) != 2 {
		t.Fatal("Postman roundtrip lost query values", values)
	}
}

func TestDebugExplicitAuthorizationStillRequiresAPIKeys(t *testing.T) {
	for _, location := range []string{"header", "query"} {
		t.Run(location, func(t *testing.T) {
			document := Obj{"security": []any{Obj{"bearer": []any{}, "key": []any{}}}, "components": Obj{"securitySchemes": Obj{
				"bearer": Obj{"type": "http", "scheme": "bearer", "x-scalar-secret-token": "{{unused}}"},
				"key":    Obj{"type": "apiKey", "in": location, "name": "X-API-Key", "x-scalar-secret-token": "{{key}}"},
			}}}
			headers := http.Header{"Authorization": {"Bearer explicit-credential"}}
			query := []string{}
			if err := applyDebugSecurity(document, Obj{}, headers, &query, map[string]string{"key": "synthetic+key"}); err != nil {
				t.Fatal(err)
			}
			if headers.Get("Authorization") != "Bearer explicit-credential" {
				t.Fatal("explicit Authorization was replaced")
			}
			if location == "header" && headers.Get("X-API-Key") != "synthetic+key" {
				t.Fatal("AND authentication lost its API key header")
			}
			if location == "query" && (len(query) != 1 || query[0] != "X-API-Key=synthetic%2Bkey") {
				t.Fatal("AND authentication lost its API key query parameter", query)
			}
			delete(obj(obj(obj(document["components"])["securitySchemes"])["key"]), "x-scalar-secret-token")
			if err := applyDebugSecurity(document, Obj{}, headers, &query, nil); err == nil {
				t.Fatal("explicit Authorization bypassed a missing required API key")
			}
		})
	}
}

func TestPlanUploadRejectsNonJMeterXMLBeforeSaving(t *testing.T) {
	for _, content := range []string{"<html/>", "<TestPlan/>", "<wrapper><jmeterTestPlan/></wrapper>", `<jmeterTestPlan xmlns="urn:other"/>`} {
		t.Run(content, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(filepath.Join(root, "snapshots"), 0700); err != nil {
				t.Fatal(err)
			}
			p := &Platform{Config: Config{PlansDir: root}}
			if _, err = p.preparePlan(Obj{"name": "invalid upload", "content": content}); err == nil {
				t.Fatal("non-JMeter XML accepted as an executable plan")
			}
			files, err := os.ReadDir(filepath.Join(root, "snapshots"))
			if err != nil || len(files) != 0 {
				t.Fatal("invalid plan left a snapshot", err)
			}
		})
	}
}
