package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityCapacityFreezeDispatchAndRedaction(t *testing.T) {
	properties := Obj{}
	for i := 1; i <= 64; i++ {
		properties[fmt.Sprintf("script_identity.actor_%02d.cookie", i)] = fmt.Sprintf("sid=synthetic-secret-%02d; session=synthetic-part-%02d", i, i)
	}
	if err := validateCookieAccounts(properties); err != nil {
		t.Fatal(err)
	}
	run := Obj{"properties": properties}
	log := string(encoded(properties)) + strings.Repeat("x", 256*1024) + string(encoded(properties))
	for _, public := range []string{publicRunLogText(run, log, false, 300000), pressureRedactor(run)(log)} {
		for _, i := range []int{21, 50, 64} {
			for _, prefix := range []string{"synthetic-secret-", "synthetic-part-"} {
				if strings.Contains(public, fmt.Sprintf("%s%02d", prefix, i)) {
					t.Fatal("tail secret leaked")
				}
			}
		}
	}
	p, _ := mysqlFixture(t)
	d := capacityDeclaration(64, 1)
	g := basicWorkflowGraph()
	obj(findGraphNode(g, "TestPlan")["properties"])["script_identities"] = d
	content, err := compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.createPlan(Obj{"name": "Synthetic capacity", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	props := Obj{}
	for i := 1; i <= 64; i++ {
		props[fmt.Sprintf("script_identity.actor_%02d.value_01", i)] = fmt.Sprintf("synthetic-secret-%02d", i)
	}
	run, _, err = p.createRun(Obj{"name": "capacity", "plan_id": plan["id"], "environment_id": fixtureEnvironmentID(t, p), "cluster_id": clusterID("controller-a"), "properties": props}, "identity-capacity")
	if err != nil {
		t.Fatal(err)
	}
	task := frozenDispatchTask(t, p, str(run["id"]))
	for k, v := range props {
		if task.Properties[k] != v || obj(run["properties"])[k] != "***" {
			t.Fatal("dispatch truncation or public leak")
		}
	}
}

func TestIdentityCapacityLegacyConversionIsolation(t *testing.T) {
	source := `<jmeterTestPlan><hashTree><TestPlan><stringProp name="jmeter.cookie_accounts">[{"name":"client","label":"Client","purpose":"Request","required":true,"fields":["access"],"required_fields":["access"]}]</stringProp></TestPlan><hashTree><JSR223Sampler><stringProp name="script">props.get('cookie_account.client.access')</stringProp></JSR223Sampler><hashTree/></hashTree></hashTree></jmeterTestPlan>`
	root := t.TempDir()
	src := filepath.Join(root, "source.jmx")
	dst := filepath.Join(root, "converted.jmx")
	if err := os.WriteFile(src, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runCLI([]string{"convert-script-identities", src, dst}); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(src)
	converted, _ := os.ReadFile(dst)
	if string(original) != source || strings.Contains(string(converted), "cookie_account.") {
		t.Fatal("source or conversion isolation")
	}
	if err := runCLI([]string{"convert-script-identities", src, dst}); err == nil {
		t.Fatal("overwrote destination")
	}
	d, err := scriptIdentityMetadata(string(converted))
	if err != nil {
		t.Fatal(err)
	}
	if validateExecutionIdentities(Obj{"script_identities": d}, Obj{"cookie_account.client.access": "historical"}) == nil {
		t.Fatal("old credential auto migrated")
	}
	if validateExecutionIdentities(Obj{"script_identities": d}, Obj{}) == nil {
		t.Fatal("new required secret filled implicitly")
	}
}

func TestIdentityCapacityPropertyBudget(t *testing.T) {
	p, _ := mysqlFixture(t)
	d := capacityDeclaration(64, 2)
	g := basicWorkflowGraph()
	obj(findGraphNode(g, "TestPlan")["properties"])["script_identities"] = d
	content, err := compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.createPlan(Obj{"name": "Synthetic budget", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	// Both fields are optional here so property count is isolated from activation.
	for _, v := range arr(d["identities"]) {
		for _, f := range arr(obj(v)["fields"]) {
			obj(f)["required"] = false
		}
	}
	obj(findGraphNode(g, "TestPlan")["properties"])["script_identities"] = d
	content, _ = compileWorkflow(g)
	plan, err = p.createPlan(Obj{"name": "Optional budget", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		identities, ordinary, fields int
		ok                           bool
	}{{50, 50, 1, true}, {50, 51, 1, false}, {64, 36, 1, true}, {64, 0, 2, false}} {
		props := Obj{}
		for i := 1; i <= tc.identities; i++ {
			for j := 1; j <= tc.fields; j++ {
				props[fmt.Sprintf("script_identity.actor_%02d.value_%02d", i, j)] = "synthetic"
			}
		}
		for i := 0; i < tc.ordinary; i++ {
			props[fmt.Sprintf("parameter_%d", i)] = "value"
		}
		run, _, err := p.createRun(Obj{"name": "budget", "plan_id": plan["id"], "environment_id": fixtureEnvironmentID(t, p), "cluster_id": clusterID("controller-a"), "properties": props}, fmt.Sprintf("budget-%d-%d-%d", tc.identities, tc.ordinary, tc.fields))
		if (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, err)
		}
		if err == nil {
			if _, err = p.stopRun(str(run["id"]), "now"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
