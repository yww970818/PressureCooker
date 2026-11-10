package main

import (
	"strings"
	"testing"
)

func TestCookieAccountValidationAndPrivacy(t *testing.T) {
	props := Obj{"cookie_account.actor.cookie": "sid=actor-private; session=actor-session", "cookie_account.actor.token": "actor-token", "cookie_account.actor.user_id": "9001", "cookie_account.recipient.cookie": "sid=recipient-private", "cookie_account.recipient.token": "recipient-token"}
	if err := validateCookieAccounts(props); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Obj{
		{"cookie_account.1bad.cookie": "value"},
		{"cookie_account.actor.constructor": "value"},
		{"cookie_account.actor.cookie": "sid=one\r\nInjected: yes"},
	} {
		if validateCookieAccounts(bad) == nil {
			t.Fatal("invalid account accepted")
		}
	}
	for _, properties := range []Obj{{"cookie_account.actor.token": "orphan"}, {"cookie_account.actor.cookie": ""}} {
		if validateExecutionIdentities(Obj{}, properties) == nil {
			t.Fatal("undeclared identity accepted")
		}
	}
	run := Obj{"properties": props}
	for key := range props {
		if !sensitiveRunProperties(run)[key] {
			t.Fatal("identity not sensitive")
		}
	}
	text := "actor-private actor-session actor-token recipient-private recipient-token 9001"
	public := publicRunLogText(run, text, false, 100)
	report := pressureRedactor(run)(text)
	for _, secret := range []string{"actor-private", "actor-session", "actor-token", "recipient-private", "recipient-token", "9001"} {
		if strings.Contains(public, secret) || strings.Contains(report, secret) {
			t.Fatal("identity credential leaked")
		}
	}
	if validateCookieAccounts(Obj{"cookie": "ordinary\nmultiline"}) != nil {
		t.Fatal("ordinary script property rejected")
	}
}

func TestCookieAccountFrozenDispatchAndCopy(t *testing.T) {
	p, plan := mysqlFixture(t)
	environment := fixtureEnvironmentID(t, p)
	properties := Obj{"cookie_account.actor.cookie": "sid=private-actor", "cookie_account.actor.token": "private-actor-token", "cookie_account.actor.user_id": "901", "cookie_account.recipient.cookie": "sid=private-recipient", "cookie_account.recipient.token": "private-recipient-token", "cookie_account.recipient.user_id": "902"}
	declarations := []any{Obj{"name": "actor", "label": "发起", "purpose": "请求", "fields": []any{"cookie", "token", "user_id"}, "required_fields": []any{"cookie"}}, Obj{"name": "recipient", "label": "接收", "purpose": "回读", "fields": []any{"cookie", "token", "user_id"}, "required_fields": []any{"cookie"}}}
	content := `<jmeterTestPlan><hashTree><TestPlan><stringProp name="jmeter.cookie_accounts">` + string(encoded(declarations)) + `</stringProp></TestPlan><hashTree/></hashTree></jmeterTestPlan>`
	oldPlan, err := p.createPlan(Obj{"name": "old declaration", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.createRun(Obj{"name": "old", "plan_id": oldPlan["id"], "environment_id": environment, "cluster_id": clusterID("controller-a"), "properties": properties}, "old"); err == nil {
		t.Fatal("old identity script executed")
	}
	converted, err := convertScriptIdentitySource(content)
	if err != nil {
		t.Fatal(err)
	}
	migratedProperties := Obj{}
	for key, value := range properties {
		migratedProperties[strings.Replace(key, cookieAccountPrefix, scriptIdentityPrefix, 1)] = value
	}
	properties = migratedProperties
	plan, err = p.createPlan(Obj{"name": "declared", "content": converted})
	if err != nil {
		t.Fatal(err)
	}
	payload := Obj{"name": "Cookie roles", "plan_id": plan["id"], "environment_id": environment, "cluster_id": clusterID("controller-a"), "properties": properties}
	run, created, err := p.createRun(payload, "cookie-roles")
	if err != nil || !created {
		t.Fatal(err)
	}
	id := str(run["id"])
	task := frozenDispatchTask(t, p, id)
	for key, value := range properties {
		if task.Properties[key] != value {
			t.Fatal("identity changed in signed dispatch")
		}
		if obj(run["properties"])[key] != "***" {
			t.Fatal("public run leaked identity")
		}
	}
	original, err := p.get("runs", id)
	if err != nil {
		t.Fatal(err)
	}
	copied := obj(runCopyDraft(original)["properties"])
	for key, value := range properties {
		if copied[key] != value {
			t.Fatal("copy lost identity")
		}
	}
	for _, query := range []string{"SELECT data FROM runs WHERE id=?", "SELECT payload FROM task_dispatches WHERE id=?"} {
		var stored string
		if err = p.DB.QueryRow(query, id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stored, "private-actor") || strings.Contains(stored, "private-recipient") {
			t.Fatal("plaintext credential persisted")
		}
	}
	again, created, err := p.createRun(payload, "cookie-roles")
	if err != nil || created || again["id"] != run["id"] {
		t.Fatal("idempotent retry failed", err)
	}
	bad := clone(payload)
	bad["properties"] = Obj{"cookie_account.actor.token": "orphan"}
	if _, _, err = p.createRun(bad, "invalid-cookie-role"); err == nil {
		t.Fatal("orphan identity accepted")
	}
}
