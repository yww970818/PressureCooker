package main

import (
	"strings"
	"testing"
)

func TestVersionedScriptIdentitiesTypedValuesAndHistoricalConversion(t *testing.T) {
	declaration := Obj{"schema_version": 1, "identities": []any{Obj{"name": "client", "label": "客户端", "purpose": "脚本使用", "required": true, "fields": []any{Obj{"name": "access", "type": "string", "secret": true, "required": true}, Obj{"name": "enabled", "type": "boolean", "secret": false, "required": false}, Obj{"name": "count", "type": "number", "secret": false, "required": false}}}}}
	normalized, err := normalizeScriptIdentities(declaration)
	if err != nil {
		t.Fatal(err)
	}
	plan := Obj{"script_identities": normalized}
	properties := Obj{"script_identity.client.access": "private", "script_identity.client.enabled": "false", "script_identity.client.count": "0"}
	if err = validateExecutionIdentities(plan, properties); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"script_identity.client.access": "", "script_identity.client.enabled": "yes", "script_identity.client.count": "NaN", "script_identity.client.other": "unknown"} {
		bad := clone(properties)
		bad[key] = value
		if validateExecutionIdentities(plan, bad) == nil {
			t.Fatal("invalid field accepted", key)
		}
	}
	historical := `<jmeterTestPlan><hashTree><TestPlan><stringProp name="jmeter.cookie_accounts">[{"name":"client","label":"客户端","purpose":"请求","required":true,"fields":["access"],"required_fields":["access"]}]</stringProp></TestPlan><hashTree><JSR223Sampler><stringProp name="script">props.get('cookie_account.client.access')</stringProp></JSR223Sampler><hashTree/></hashTree></hashTree></jmeterTestPlan>`
	hash := digest([]byte(historical))
	converted, err := convertScriptIdentitySource(historical)
	if err != nil {
		t.Fatal(err)
	}
	if digest([]byte(historical)) != hash || strings.Contains(converted, cookieAccountPrefix) || !strings.Contains(converted, scriptIdentityPrefix) {
		t.Fatal("conversion modified original or left executable old prefix")
	}
	metadata, err := scriptIdentityMetadata(converted)
	if err != nil || len(arr(metadata["identities"])) != 1 {
		t.Fatal(metadata, err)
	}
	if roles, err := planCookieAccountMetadata(converted); err != nil || len(roles) != 0 {
		t.Fatal("conversion kept active old declaration", err)
	}
	if validateExecutionIdentities(Obj{"cookie_accounts": []any{Obj{}}}, Obj{}) == nil {
		t.Fatal("old script executes without explicit publication")
	}
}

func TestScriptIdentitySchemaRejectsTyposAndUnknownFields(t *testing.T) {
	valid := Obj{"schema_version": 1, "identities": []any{Obj{"name": "client", "label": "客户端", "purpose": "请求", "required": false, "fields": []any{Obj{"name": "value", "type": "string", "secret": true, "required": false}}}}}
	for _, mutate := range []func(Obj){
		func(d Obj) { d["identity"] = []any{} },
		func(d Obj) { obj(arr(d["identities"])[0])["auth_type"] = "cookie" },
		func(d Obj) { obj(arr(obj(arr(d["identities"])[0])["fields"])[0])["secrect"] = true },
	} {
		d := clone(valid)
		mutate(d)
		if _, err := normalizeScriptIdentities(d); err == nil {
			t.Fatal("unknown identity schema field dropped")
		}
	}
}

func TestScriptIdentityDeclarationSurvivesWorkflowRoundTrip(t *testing.T) {
	declaration := Obj{"schema_version": 1, "identities": []any{Obj{"name": "client", "label": "客户端", "purpose": "请求", "required": true, "fields": []any{Obj{"name": "credential", "type": "string", "secret": true, "required": true}}}}}
	graph := basicWorkflowGraph()
	obj(findGraphNode(graph, "TestPlan")["properties"])["script_identities"] = declaration
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := importWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	obj(findGraphNode(imported, "HTTPSamplerProxy")["properties"])["name"] = "编辑后的请求"
	recompiled, err := compileWorkflow(imported)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := scriptIdentityMetadata(recompiled)
	if err != nil || string(encoded(metadata)) != string(encoded(declaration)) {
		t.Fatal("identity declaration lost in workflow roundtrip", err)
	}
	if err = validateExecutionIdentities(Obj{"script_identities": metadata}, Obj{}); err == nil {
		t.Fatal("import bypassed declared identity requirement")
	}
}
