package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestCurlImportQuotesRepeatedQueryAndShellIsNeverExecuted(t *testing.T) {
	input := `curl "https://example.invalid/path?tag=one&tag=two" -H 'X-Quoted: a b' --json '{"quote":"a\"b", "literal":"$(touch /never-created)", "newline":"\n"}'`
	collection, e := parseCollectionImport(Obj{"format": "curl", "content": input})
	if e != nil {
		t.Fatal(e)
	}
	op := obj(obj(obj(obj(collection["document"])["paths"])["/path"])["post"])
	params := arr(op["parameters"])
	if len(params) != 4 || obj(params[0])["example"] != "one" || obj(params[1])["example"] != "two" {
		t.Fatal("repeated query or header lost", params)
	}
	raw, _, e := operationBody(op)
	if e != nil || !strings.Contains(raw, "$(touch /never-created)") || !strings.Contains(raw, `\n`) {
		t.Fatal("quoted body changed", raw, e)
	}
	for _, input := range []string{"curl https://example.invalid | sh", "curl https://example.invalid; touch /never-created", "curl $(printf https://example.invalid)", "curl 'https://example.invalid' --data @/etc/passwd", "curl 'https://example.invalid' -F file=@/etc/passwd", "curl 'https://example.invalid'\nwhoami"} {
		if _, e := parseCollectionImport(Obj{"format": "curl", "content": input}); e == nil {
			t.Errorf("unsafe/lossy curl accepted %q", input)
		}
	}
}

func TestDebugRejectsUnmappedParameterAndMissingCredentials(t *testing.T) {
	for _, param := range []Obj{{"name": "id", "in": "path", "style": "matrix", "example": "42"}, {"name": "q", "in": "query", "allowReserved": true, "example": "a&b"}, {"name": "optional", "in": "query", "schema": Obj{"type": "string"}}} {
		if _, e := parameterValue(param); e == nil {
			t.Fatal("guessed unmapped parameter serialization", param)
		}
	}
	document := Obj{"security": []any{Obj{"basic": []any{}}}, "components": Obj{"securitySchemes": Obj{"basic": Obj{"type": "http", "scheme": "basic"}}}}
	query := []string{}
	if e := applyDebugSecurity(document, Obj{}, http.Header{}, &query, map[string]string{}); e == nil {
		t.Fatal("sent empty Basic credentials without configuration")
	}
}

func TestImportedJSONBodiesRetainExactWireTextAcrossPostman(t *testing.T) {
	for _, raw := range []string{`"hello"`, `""`, `"a\nb"`, `null`, `900719925474099312345`, "{ \n \"a\": 1e2, \"a\": 2 }"} {
		collection, e := parseCollectionImport(Obj{"format": "curl", "content": "curl https://example.invalid/body --json '" + raw + "'"})
		if e != nil {
			t.Fatal(e)
		}
		operation := obj(obj(obj(obj(collection["document"])["paths"])["/body"])["post"])
		got, media, e := operationBody(operation)
		if e != nil || media != "application/json" || got != raw {
			t.Fatalf("import changed JSON bytes: want %q got %q (%v)", raw, got, e)
		}
		example := obj(obj(obj(obj(operation["requestBody"])["content"])["application/json"])["examples"])
		if obj(example["default"])["serializedValue"] != raw {
			t.Fatal("Scalar native serialized example missing", example)
		}
		exported, e := exportPostman(collection)
		if e != nil {
			t.Fatal(e)
		}
		postmanBody := obj(obj(obj(arr(exported["item"])[0])["request"])["body"])
		if postmanBody["raw"] != raw {
			t.Fatal("Postman export changed JSON bytes", postmanBody)
		}
		imported, e := parseCollectionImport(Obj{"format": "postman", "content": exported})
		if e != nil {
			t.Fatal(e)
		}
		operation = obj(obj(obj(obj(imported["document"])["paths"])["/body"])["post"])
		got, _, e = operationBody(operation)
		if e != nil || got != raw {
			t.Fatal("Postman roundtrip changed body", got, e)
		}
	}
}

func TestImportCanonicalizesScalarVariableMapsWithoutLosingArrayMetadata(t *testing.T) {
	metadataVariables := []any{Obj{"name": "token", "value": "private-value", "isSecret": true, "description": "保留说明", "x-extra": Obj{"id": "original"}}}
	environments := Obj{"map": Obj{"color": "#008889", "variables": Obj{"baseUrl": "http://smoke-target:8080", "batch": "project-validation"}}, "array": Obj{"variables": metadataVariables, "x-extra": "environment-metadata"}}
	document := Obj{"openapi": "3.1.0", "info": Obj{"title": "环境兼容", "version": "1"}, "paths": Obj{}, "x-scalar-environments": environments}
	collection, e := parseCollectionImport(Obj{"format": "openapi", "content": document})
	if e != nil {
		t.Fatal(e)
	}
	normalized := obj(obj(collection["document"])["x-scalar-environments"])
	variables, ok := obj(normalized["map"])["variables"].([]any)
	if !ok || len(variables) != 2 {
		t.Fatal("native Scalar needs iterable variable arrays", normalized)
	}
	if obj(variables[0])["name"] != "baseUrl" || obj(variables[0])["value"] != "http://smoke-target:8080" || obj(normalized["map"])["color"] != "#008889" {
		t.Fatal("map conversion lost environment semantics", normalized)
	}
	if string(encoded(obj(normalized["array"])["variables"])) != string(encoded(metadataVariables)) || obj(normalized["array"])["x-extra"] != "environment-metadata" {
		t.Fatal("existing array metadata changed", normalized)
	}
	if _, ok := obj(environments["map"])["variables"].(map[string]any); !ok {
		t.Fatal("normalization mutated caller source")
	}
}

func TestNativeObjectEnvironmentValuesPreserveMetadataAndResolveDefaults(t *testing.T) {
	value := Obj{"default": "${token}", "description": "保留说明", "x-secret": true, "x-extra": Obj{"opaque": "retain"}}
	array := []any{Obj{"name": "documented", "value": value, "x-entry": "keep"}, Obj{"name": "empty", "value": Obj{"default": "", "description": "显式空值"}}}
	environments := Obj{"native": Obj{"variables": array, "color": "#123456"}, "legacy": Obj{"variables": Obj{"documented": value}, "description": "旧格式"}}
	collection, e := parseCollectionImport(Obj{"format": "openapi", "content": Obj{"openapi": "3.1.0", "info": Obj{"title": "原生对象值", "version": "1"}, "servers": []any{Obj{"url": "https://example.invalid"}}, "paths": Obj{"/ping": Obj{"get": Obj{"responses": Obj{}}}}, "x-scalar-environments": environments, "x-jmeter-client-state": Obj{"active_environment": "native"}}})
	if e != nil {
		t.Fatal("native Scalar object value rejected", e)
	}
	normalized := obj(obj(collection["document"])["x-scalar-environments"])
	if string(encoded(obj(normalized["native"])["variables"])) != string(encoded(array)) {
		t.Fatal("native object metadata flattened", normalized)
	}
	legacy := arr(obj(normalized["legacy"])["variables"])
	if len(legacy) != 1 || string(encoded(obj(legacy[0])["value"])) != string(encoded(value)) {
		t.Fatal("legacy object value metadata flattened", normalized)
	}
	variables, e := environmentVariables(obj(normalized["native"]))
	if e != nil || variables["documented"] != "${token}" || variables["empty"] != "" {
		t.Fatal("debug must resolve object.default without erasing explicit empty", variables, e)
	}
	exported, e := exportPostman(collection)
	if e != nil {
		t.Fatal("native environment prevented export", e)
	}
	imported, e := parseCollectionImport(Obj{"format": "postman", "content": exported})
	if e != nil {
		t.Fatal(e)
	}
	if string(encoded(obj(imported["document"])["x-scalar-environments"])) != string(encoded(normalized)) {
		t.Fatal("export/import changed environment value metadata", imported)
	}
	if _, e := canonicalScalarEnvironments(Obj{"bad": Obj{"variables": []any{Obj{"name": "secret", "value": Obj{"description": "no configured default"}}}}}); e == nil {
		t.Fatal("missing secret default silently became blank")
	}
}

func TestCurlDoubleQuotedBackslashAndAuthVariablesSurvive(t *testing.T) {
	collection, e := parseCollectionImport(Obj{"format": "curl", "content": `curl 'https://example.invalid' -u '{{username}}:{{password}}' --data-raw "a\q"`})
	if e != nil {
		t.Fatal(e)
	}
	op := obj(obj(obj(obj(collection["document"])["paths"])["/"])["post"])
	body, _, e := operationBody(op)
	if e != nil || body != `a\q` {
		t.Fatal("shell double quote changed literal backslash", body, e)
	}
	exported, e := exportPostman(collection)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(encoded(exported)), "{{username}}") || !strings.Contains(string(encoded(exported)), "{{password}}") {
		t.Fatal("auth variables lost before environment expansion", exported)
	}
}

func TestPostmanNestedImportRetainsVariablesAuthAndRejectsLoss(t *testing.T) {
	postman := Obj{"info": Obj{"name": "变量集合", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"}, "variable": []any{Obj{"key": "baseUrl", "value": "https://example.invalid"}}, "auth": Obj{"type": "bearer", "bearer": []any{Obj{"key": "token", "value": "{{token}}"}}}, "item": []any{Obj{"name": "目录", "item": []any{Obj{"name": "查询", "request": Obj{"method": "GET", "url": Obj{"raw": "{{baseUrl}}/users?x=1&x=2", "query": []any{Obj{"key": "x", "value": "1"}, Obj{"key": "x", "value": "2"}}}}}}}}}
	collection, e := parseCollectionImport(Obj{"format": "apifox", "content": string(encoded(postman))})
	if e != nil {
		t.Fatal(e)
	}
	doc := obj(collection["document"])
	op := obj(obj(obj(doc["paths"])["/users"])["get"])
	if len(arr(op["parameters"])) != 3 || str(arr(op["tags"])[0]) != "目录" || obj(collection["client_state"])["active_environment"] != "imported" {
		t.Fatal("Postman structure lost", collection)
	}
	postman["event"] = []any{Obj{"listen": "prerequest", "script": Obj{"exec": []any{"pm.variables.set('x', 'y')"}}}}
	if _, e := parseCollectionImport(Obj{"format": "postman", "content": postman}); e == nil {
		t.Fatal("silently discarded Postman executable script")
	}
}

func TestOpenAPIExportPathParametersRemainStableAcrossMethods(t *testing.T) {
	operation := func() Obj {
		return Obj{"parameters": []any{Obj{"name": "id", "in": "path", "required": true, "example": "42"}}, "responses": Obj{"200": Obj{"description": "OK"}}}
	}
	collection := Obj{"name": "路径", "document": Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "https://example.invalid"}}, "paths": Obj{"/users/{id}": Obj{"get": operation(), "delete": operation()}}}}
	exported, e := exportPostman(collection)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range arr(exported["item"]) {
		request := obj(obj(v)["request"])
		raw := str(obj(request["url"])["raw"])
		if raw != "https://example.invalid/users/:id" {
			t.Fatal(raw)
		}
		if len(arr(obj(request["url"])["variable"])) != 1 {
			t.Fatal("path variable example lost", request)
		}
	}
	if _, e := parseCollectionImport(Obj{"format": "postman", "content": exported}); e != nil {
		t.Fatal(e)
	}
}

func TestPostmanExportPreservesScalarAuthAndAllEnvironments(t *testing.T) {
	document := Obj{"openapi": "3.1.0", "servers": []any{Obj{"url": "https://example.invalid"}}, "paths": Obj{"/request": Obj{"get": Obj{"responses": Obj{"200": Obj{"description": "ok"}}}}}, "security": []any{Obj{"bearer": []any{}}}, "components": Obj{"securitySchemes": Obj{"bearer": Obj{"type": "http", "scheme": "bearer"}}}, "x-scalar-environments": Obj{"a": Obj{"variables": []any{Obj{"name": "token", "value": "one"}}}, "b": Obj{"variables": []any{Obj{"name": "token", "value": "two"}}}}}
	client := Obj{"active_environment": "a", "auth": Obj{"collection": Obj{"secrets": Obj{"bearer": Obj{"type": "http", "x-scalar-secret-token": "{{token}}"}}, "selected": Obj{}}}}
	exported, e := exportPostman(Obj{"name": "认证", "document": document, "client_state": client})
	if e != nil {
		t.Fatal(e)
	}
	auth := obj(obj(obj(arr(exported["item"])[0])["request"])["auth"])
	if obj(arr(auth["bearer"])[0])["value"] != "{{token}}" {
		t.Fatal("Scalar auth not exported", auth)
	}
	imported, e := parseCollectionImport(Obj{"format": "postman", "content": exported})
	if e != nil {
		t.Fatal(e)
	}
	if string(encoded(obj(imported["document"])["x-scalar-environments"])) != string(encoded(document["x-scalar-environments"])) {
		t.Fatal("inactive environments lost", imported)
	}
}
