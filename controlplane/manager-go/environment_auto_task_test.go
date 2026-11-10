package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func autoTaskScope(p *Platform, ip string, ms int64) *Platform {
	return p.forRequest(withEnvironmentOperation(context.Background(), newEnvironmentOperation(ip, time.UnixMilli(ms))), p.projectID())
}

func TestMySQLAutomaticTaskFreezeRetryCopyAndCollectionClock(t *testing.T) {
	p, _ := mysqlFixture(t)
	environment, err := p.workspaceCreate("environments", Obj{"name": "automatic task", "base_host": "https://fixture.invalid", "variables": []any{Obj{"name": "source_field", "value": "{{$auto.client_ip}}"}, Obj{"name": "project_time", "value": "{{$auto.timestamp_ms}}"}, Obj{"name": "manual_project", "value": "{{$auto.client_ip}}"}}})
	if err != nil {
		t.Fatal(err)
	}
	body := collectionOverrideBody(str(environment["id"]), "unused")
	obj(obj(body["client_state"])[collectionOverridesKey])[str(environment["id"])] = Obj{"variables": []any{Obj{"name": "source_field", "value": "{{$auto.client_ip}}"}, Obj{"name": "collection_time", "value": "{{$auto.timestamp_ms}}"}, Obj{"name": "task_time", "value": "{{$auto.timestamp_ms}}"}}}
	collection, err := p.workspaceCreate("api_collections", body)
	if err != nil {
		t.Fatal(err)
	}
	graph := basicWorkflowGraph()
	graph["environment_binding"] = "base_host"
	graph["environment_variables"] = []any{"source_field", "project_time", "manual_project"}
	props := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	props["config"] = Obj{"method": "GET", "path": "/ping?i=${source_field}&p=${project_time}&c=${collection_time}&t=${task_time}&m=${manual_project}"}
	props["api_reference"] = Obj{"collection_id": collection["id"], "revision": 1, "path": "/ping", "method": "get"}
	workflow, err := p.workspaceCreate("workflows", Obj{"name": "automatic bound task", "graph": graph})
	if err != nil {
		t.Fatal(err)
	}
	published, err := p.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if err != nil {
		t.Fatal(err)
	}
	payload := Obj{"name": "automatic freeze", "plan_id": obj(published["plan"])["id"], "cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "properties": Obj{"task_time": "{{$auto.timestamp_ms}}", "manual_project": "manual"}, "environment_snapshot": Obj{"client_ip": "forged", "timestamp_ms": "1"}}
	firstScope := autoTaskScope(p, "198.51.100.9", 1794212345678)
	run, created, err := firstScope.createRun(payload, "automatic-once")
	if err != nil || !created {
		t.Fatal("automatic task creation failed", err)
	}
	internal, err := p.get("runs", str(run["id"]))
	if err != nil {
		t.Fatal(err)
	}
	runtime := obj(internal["properties"])
	snapshot := obj(internal["environment_snapshot"])
	for _, key := range []string{"project_time", "task_time", apiEnvironmentProperty("http", "collection_time"), apiEnvironmentProperty("http", "task_time")} {
		if runtime[key] != "1794212345678" {
			t.Fatal("one task used multiple operation clocks", key, runtime[key])
		}
	}
	if runtime["source_field"] != "198.51.100.9" || runtime[apiEnvironmentProperty("http", "source_field")] != "198.51.100.9" || runtime["manual_project"] != "manual" {
		t.Fatal("source/override precedence changed", runtime)
	}
	if obj(snapshot["task_property_rules"])["task_time"] != "{{$auto.timestamp_ms}}" {
		t.Fatal("task rule provenance missing")
	}
	for _, raw := range arr(snapshot["api_overrides"]) {
		row := obj(raw)
		if row["generation_rule"] == nil {
			t.Fatal("collection auto provenance missing", row)
		}
	}
	dispatch := frozenDispatchTask(t, p, str(run["id"]))
	for key, value := range dispatch.Properties {
		if value != str(runtime[key]) || strings.Contains(value, "$auto") {
			t.Fatal("dispatch differed from frozen operation", key)
		}
	}
	secondScope := autoTaskScope(p, "203.0.113.10", 1794212349999)
	retry, created, err := secondScope.createRun(payload, "automatic-once")
	if err != nil || created || retry["id"] != run["id"] {
		t.Fatal("retry regenerated a task", err)
	}
	replayed, _ := p.get("runs", str(run["id"]))
	if string(encoded(replayed["environment_snapshot"])) != string(encoded(snapshot)) {
		t.Fatal("retry rewrote frozen snapshot")
	}
	draft := runCopyDraft(internal)
	if obj(draft["properties"])["task_time"] != "{{$auto.timestamp_ms}}" {
		t.Fatal("copy did not restore authored explicit auto rule")
	}
	copy, _, err := secondScope.createRun(draft, "automatic-copy")
	if err != nil {
		t.Fatal(err)
	}
	copied, _ := p.get("runs", str(copy["id"]))
	if obj(copied["properties"])["source_field"] != "203.0.113.10" || obj(copied["properties"])["task_time"] != "1794212349999" {
		t.Fatal("normal copy reused old operation")
	}
	draft["copy_environment_snapshot"] = true
	historical, _, err := secondScope.createRun(draft, "automatic-historical-copy")
	if err != nil {
		t.Fatal(err)
	}
	history, _ := p.get("runs", str(historical["id"]))
	if obj(history["properties"])["source_field"] != "198.51.100.9" || obj(history["properties"])["project_time"] != "1794212345678" || obj(history["properties"])["task_time"] != "1794212349999" {
		t.Fatal("historical project snapshot/explicit fresh rule semantic changed", history["properties"])
	}
	stored, _ := p.workspaceGet("environments", str(environment["id"]))
	if obj(arr(stored["variables"])[0])["value"] != "{{$auto.client_ip}}" || number(stored["revision"]) != 1 {
		t.Fatal("runtime generation changed saved project definition")
	}
	var raw string
	if err = p.DB.QueryRow("SELECT data FROM runs WHERE id=?", run["id"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "198.51.100.9") || strings.Contains(raw, "1794212345678") {
		t.Fatal("frozen auto values escaped existing encrypted run envelope")
	}
}

func TestMySQLManagedArgumentsFreezeMaskBudgetAndCopy(t *testing.T) {
	p, _ := mysqlFixture(t)
	environment, err := p.workspaceCreate("environments", Obj{"name": "scalar defaults", "base_host": "https://fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	graph := managedGenerationGraph()
	arg := findGraphNode(graph, "Arguments")
	pairs := arr(obj(obj(arg["properties"])["config"])["variables"])
	obj(pairs[2])["name"] = "secret_token"
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = "${address_value}|${clock_value}|${secret_token}"
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.createPlan(Obj{"name": "managed scalar", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	if len(arr(plan["argument_generation_rules"])) != 3 {
		t.Fatal("published plan omitted managed declarations")
	}
	payload := Obj{"name": "managed scalar task", "plan_id": plan["id"], "cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "properties": Obj{"clock_value": "{{$auto.timestamp_ms}}"}}
	first := autoTaskScope(p, "198.51.100.7", 1794212345678)
	run, _, err := first.createRun(payload, "managed-scalar")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := p.get("runs", str(run["id"]))
	if err != nil {
		t.Fatal(err)
	}
	runtime := obj(internal["properties"])
	ipKey := argumentGenerationProperty("managed-values", "address_value")
	timeKey := argumentGenerationProperty("managed-values", "clock_value")
	secretKey := argumentGenerationProperty("managed-values", "secret_token")
	if runtime[ipKey] != "198.51.100.7" || runtime[timeKey] != "1794212345678" || len(str(runtime[secretKey])) != 16 {
		t.Fatal("Arguments were not frozen", runtime)
	}
	if obj(run["properties"])[secretKey] != "***" {
		t.Fatal("new opaque prefix lost original-name public masking")
	}
	if redacted := pressureRedactor(internal)("echo=" + str(runtime[secretKey])); strings.Contains(redacted, str(runtime[secretKey])) {
		t.Fatal("report/log redactor missed managed token")
	}
	table, err := p.runParameterTable(str(run["id"]), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range arr(table["properties"]) {
		if row := obj(raw); row["name"] == secretKey && row["value"] != "***" {
			t.Fatal("parameter table revealed generated secret")
		}
	}
	dispatch := frozenDispatchTask(t, p, str(run["id"]))
	if dispatch.Properties[secretKey] != str(runtime[secretKey]) || strings.Contains(string(encoded(dispatch.Properties)), "$auto") || strings.Contains(string(encoded(dispatch.Properties)), "$random") {
		t.Fatal("dispatch carried unresolved/changed generators")
	}
	second := autoTaskScope(p, "203.0.113.4", 1794212349999)
	copy, _, err := second.createRun(runCopyDraft(internal), "managed-copy")
	if err != nil {
		t.Fatal(err)
	}
	copied, _ := p.get("runs", str(copy["id"]))
	if obj(copied["properties"])[ipKey] != "203.0.113.4" || obj(copied["properties"])[timeKey] != "1794212349999" {
		t.Fatal("new managed copy reused prior operation")
	}
	large := clone(internal)
	obj(large["properties"])[secretKey] = strings.Repeat("x", 4*1024*1024)
	err = p.transaction(func(tx *Tx) error { return p.enqueueDispatch(tx, large, plan) })
	status, code, _ := requestFailure(err)
	if status != 413 || code != "task_payload_too_large" {
		t.Fatal("scalar opaque value bypassed normal signed task budget", status, code, err)
	}
	bad := clone(payload)
	obj(bad["properties"])[ipKey] = "forged"
	if _, _, err = first.createRun(bad, "managed-reserved-key"); err == nil {
		t.Fatal("caller could override a generated internal property")
	}
	if _, _, err = p.createRun(payload, "managed-unknown-source"); err == nil {
		t.Fatal("unknown task source invented client IP")
	}
}
