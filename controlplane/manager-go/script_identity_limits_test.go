package main

import (
	"fmt"
	"html"
	"reflect"
	"strings"
	"testing"
)

func capacityDeclaration(n, fields int) Obj {
	rows := []any{}
	for i := 1; i <= n; i++ {
		fs := []any{}
		for j := 1; j <= fields; j++ {
			fs = append(fs, Obj{"name": fmt.Sprintf("value_%02d", j), "type": "string", "secret": true, "required": true})
		}
		rows = append(rows, Obj{"name": fmt.Sprintf("actor_%02d", i), "label": "Actor", "purpose": "Synthetic request identity", "required": false, "fields": fs})
	}
	return Obj{"schema_version": 1, "identities": rows}
}

func TestScriptIdentityCountAndFieldBoundaries(t *testing.T) {
	for _, n := range []int{20, 21, 50, 64, 65} {
		_, err := normalizeScriptIdentities(capacityDeclaration(n, 1))
		if (err == nil) != (n <= 64) {
			t.Fatalf("count %d: %v", n, err)
		}
	}
	for _, n := range []int{20, 21} {
		_, err := normalizeScriptIdentities(capacityDeclaration(1, n))
		if (err == nil) != (n <= 20) {
			t.Fatalf("fields %d: %v", n, err)
		}
	}
	for _, mutate := range []func(Obj){
		func(d Obj) { arr(d["identities"])[1] = arr(d["identities"])[0] },
		func(d Obj) { r := obj(arr(d["identities"])[0]); arr(r["fields"])[1] = arr(r["fields"])[0] },
		func(d Obj) { obj(arr(obj(arr(d["identities"])[0])["fields"])[0])["type"] = "object" },
	} {
		d := capacityDeclaration(2, 2)
		mutate(d)
		if _, err := normalizeScriptIdentities(d); err == nil {
			t.Fatal("invalid declaration accepted")
		}
	}
}

func TestScriptIdentityOptionalActivationAndMetadataSize(t *testing.T) {
	d := capacityDeclaration(64, 2)
	if err := validateExecutionIdentities(Obj{"script_identities": d}, Obj{}); err != nil {
		t.Fatal(err)
	}
	p := Obj{"script_identity.actor_64.value_01": "synthetic"}
	if validateExecutionIdentities(Obj{"script_identities": d}, p) == nil {
		t.Fatal("activated tail identity lacks second required field")
	}
	p["script_identity.actor_64.value_02"] = "synthetic2"
	if err := validateExecutionIdentities(Obj{"script_identities": d}, p); err != nil {
		t.Fatal(err)
	}
	if err := validateCookieAccounts(p); err != nil {
		t.Fatal(err)
	}
	// Each individual text is legal; only the encoded root budget is exceeded.
	d = capacityDeclaration(40, 1)
	for _, v := range arr(d["identities"]) {
		obj(v)["purpose"] = strings.Repeat("界", 450)
	}
	for _, v := range arr(d["identities"]) {
		r := obj(v)
		need := (64 << 10) - len(encoded(d))
		room := 1999 - len(str(r["purpose"]))
		if need < room {
			room = need
		}
		if room > 0 {
			r["purpose"] = str(r["purpose"]) + strings.Repeat("x", room)
		}
	}
	row := obj(arr(d["identities"])[39])
	if len(encoded(d)) != 64<<10 {
		t.Fatal("fixture boundary")
	}
	for _, over := range []bool{false, true} {
		if over {
			row["purpose"] = str(row["purpose"]) + "x"
		}
		_, normErr := normalizeScriptIdentities(d)
		g := basicWorkflowGraph()
		obj(findGraphNode(g, "TestPlan")["properties"])["script_identities"] = d
		_, compileErr := compileWorkflow(g)
		content := `<jmeterTestPlan><hashTree><TestPlan><stringProp name="pressurecooker.script_identities">` + html.EscapeString(string(encoded(d))) + `</stringProp></TestPlan><hashTree/></hashTree></jmeterTestPlan>`
		_, readErr := scriptIdentityMetadata(content)
		_, importErr := importWorkflowGraph(content)
		for name, err := range map[string]error{"normalize": normErr, "compile": compileErr, "read": readErr, "import": importErr} {
			if (err != nil) != over {
				t.Fatalf("%s over=%t bytes=%d: %v", name, over, len(encoded(d)), err)
			}
		}
	}
}

func TestScriptIdentity64WorkflowRoundTrip(t *testing.T) {
	d := capacityDeclaration(64, 1)
	g := basicWorkflowGraph()
	obj(findGraphNode(g, "TestPlan")["properties"])["script_identities"] = d
	content, err := compileWorkflow(g)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := importWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	content, err = compileWorkflow(imported)
	if err != nil {
		t.Fatal(err)
	}
	got, err := scriptIdentityMetadata(content)
	if err != nil || !reflect.DeepEqual(got, d) {
		t.Fatal("64 identities changed", err)
	}
	p := Obj{}
	for i := 1; i <= 64; i++ {
		p[fmt.Sprintf("script_identity.actor_%02d.value_01", i)] = "synthetic"
	}
	if err := validateCookieAccounts(p); err != nil {
		t.Fatal(err)
	}
}
