package main

import (
	"strings"
	"testing"
)

func TestEnvironmentNameTrimMatchesEditor(t *testing.T) {
	for _, name := range []string{" test ", "\t test\n", " " + strings.Repeat("名", 200) + " "} {
		value, e := validateEnvironment(Obj{"name": name, "base_host": "https://trim.internal"})
		if e != nil || value["name"] != strings.TrimSpace(name) {
			t.Fatalf("environment name was not trimmed: error=%v", e)
		}
	}
}

func TestMySQLProjectEnvironmentNameTrimIncludesLegacyRows(t *testing.T) {
	p, _ := mysqlFixture(t)
	create := func(name string) (Obj, error) {
		return p.workspaceCreate("environments", Obj{"name": name, "base_host": "https://trim.internal"})
	}
	env, e := create(" test ")
	if e != nil {
		t.Fatal(e)
	}
	if env["name"] != "test" {
		t.Error("create retained whitespace")
	}
	if _, e = create("test"); e == nil {
		t.Fatal("trimmed duplicate accepted")
	}
	updated, e := p.workspaceUpdate("environments", str(env["id"]), Obj{"name": " renamed ", "base_host": "https://trim.internal", "revision": 1})
	if e != nil || updated["name"] != "renamed" {
		t.Fatal("rename not trimmed", e)
	}
	legacy, e := create("history")
	if e != nil {
		t.Fatal(e)
	}
	legacy["name"] = " history "
	sealed, e := p.sealWorkspace("environments", str(legacy["id"]), 1, legacy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("UPDATE environments SET payload=? WHERE id=?", sealed, legacy["id"]); e != nil {
		t.Fatal(e)
	}
	if _, e = create("history"); e == nil {
		t.Fatal("legacy whitespace name did not reserve canonical name")
	}
}
