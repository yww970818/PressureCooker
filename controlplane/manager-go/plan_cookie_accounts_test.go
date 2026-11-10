package main

import (
	"strings"
	"testing"
)

func TestPlanCookieAccountDeclaration(t *testing.T) {
	roles := []any{Obj{"name": "sender", "label": "发起人", "purpose": "创建和转交记录", "required": true, "required_fields": []any{"cookie", "token"}}, Obj{"name": "reader", "label": "接收人", "purpose": "转交后回读"}}
	graph := basicWorkflowGraph()
	// The test fixture root is discovered, not coupled to a particular ID.
	for _, n := range arr(graph["nodes"]) {
		if obj(obj(n)["properties"])["component"] == "TestPlan" {
			obj(obj(n)["properties"])["cookie_accounts"] = roles
		}
	}
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planCookieAccountMetadata(content)
	if err != nil || len(got) != 2 || obj(got[0])["purpose"] != "创建和转交记录" {
		t.Fatal(got, err)
	}
	if validateExecutionIdentities(Obj{"cookie_accounts": got}, Obj{}) == nil {
		t.Fatal("historical identity declaration allowed execution")
	}
	if _, err = normalizePlanCookieAccounts([]any{roles[0], roles[0]}); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	if _, err = planCookieAccountMetadata(strings.Replace(content, "创建和转交记录", "", 1)); err == nil {
		t.Fatal("missing purpose accepted")
	}
	legacy, err := planCookieAccountMetadata(`<jmeterTestPlan><hashTree><TestPlan/><hashTree/></hashTree></jmeterTestPlan>`)
	if err != nil || len(legacy) != 0 {
		t.Fatal(legacy, err)
	}
}

func TestPlanReservedAuthenticationMetadataAndExecutionGate(t *testing.T) {
	role := Obj{"name": "client", "label": "客户端", "purpose": "认证请求", "required": false, "auth_type": "bearer", "required_fields": []any{"access_token"}}
	graph := basicWorkflowGraph()
	for _, n := range arr(graph["nodes"]) {
		if obj(obj(n)["properties"])["component"] == "TestPlan" {
			obj(obj(n)["properties"])["cookie_accounts"] = []any{role}
		}
	}
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := planCookieAccountMetadata(content)
	if err != nil || len(roles) != 1 || str(obj(roles[0])["auth_type"]) != "bearer" || strings.Join(stringList(obj(roles[0])["required_fields"]), ",") != "access_token" {
		t.Fatal("reserved metadata lost", err)
	}
	if err = validateExecutionIdentities(Obj{"cookie_accounts": roles}, Obj{}); err == nil || !strings.Contains(err.Error(), "显式转换") {
		t.Fatal("reserved authentication accepted for execution", err)
	}
	for _, invalid := range []any{"", "Cookie", "bad type", 1} {
		bad := clone(role)
		bad["auth_type"] = invalid
		if _, err = normalizePlanCookieAccounts([]any{bad}); err == nil {
			t.Fatal("invalid authentication declaration accepted")
		}
	}
}

func TestScriptIdentityCustomFieldsAndNoImplicitCookie(t *testing.T) {
	roles, err := normalizePlanCookieAccounts([]any{Obj{"name": "client", "label": "客户端", "purpose": "请求", "required": true, "required_fields": []any{"user_id"}, "fields": []any{"user_id", "credential"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = validateCookieAccounts(Obj{"cookie_account.client.user_id": "user", "cookie_account.client.credential": "private"}); err != nil {
		t.Fatal(err)
	}
	if validateExecutionIdentities(Obj{"cookie_accounts": roles}, Obj{}) == nil {
		t.Fatal("historical declaration executed")
	}
	run := Obj{"properties": Obj{"cookie_account.client.user_id": "private-id", "cookie_account.client.opaque": "private-opaque"}}
	text := "private-id private-opaque"
	p := &Platform{}
	public := string(encoded(p.publicRun(clone(run), nil, nil, nil)))
	log, report := publicRunLogText(run, text, false, 100), pressureRedactor(run)(text)
	for _, secret := range []string{"private-id", "private-opaque"} {
		if strings.Contains(public, secret) || strings.Contains(log, secret) || strings.Contains(report, secret) {
			t.Fatal("custom identity field leaked")
		}
	}
}

func TestScriptIdentityRejectsInvalidFieldDeclarations(t *testing.T) {
	for _, fields := range []any{"user_id", []any{"constructor"}, []any{"prototype"}, []any{"__proto__"}, []any{"user_id", "user_id"}, []any{1}, []any{"Bad"}, []any{"not.a.field"}} {
		if _, err := normalizePlanCookieAccounts([]any{Obj{"name": "client", "label": "客户端", "purpose": "请求", "fields": fields}}); err == nil {
			t.Fatal("invalid fields accepted")
		}
	}
	if _, err := normalizePlanCookieAccounts([]any{Obj{"name": "client", "label": "客户端", "purpose": "请求", "fields": []any{"credential"}, "required_fields": []any{"user_id"}}}); err == nil {
		t.Fatal("undeclared required field accepted")
	}
}
