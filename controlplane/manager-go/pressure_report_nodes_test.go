package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/reporting"
)

func TestPressureNodeEvidenceValidationAndRedaction(t *testing.T) {
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{"worker-1"}, "properties": Obj{"token": "private-node-token"}}
	base := protocol.NodeEvidence{NodeID: "worker-1", TaskID: "run-1", AttemptID: "attempt-1", Generation: 2, Status: "COLLECTED", Logs: []protocol.NodeLog{{Name: "jmeter.log", Content: "ERROR token=private-node-token", Status: "COLLECTED", LineFrom: 1, LineTo: 1, Size: 30}}}
	got, e := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{base})
	if e != nil || len(got) != 1 || strings.Contains(got[0].Logs[0].Content, "private-node-token") || base.Logs[0].Content != "ERROR token=private-node-token" {
		t.Fatal("validation must redact a copy", e)
	}
	for _, mutate := range []func(*protocol.NodeEvidence){
		func(v *protocol.NodeEvidence) { v.NodeID = "worker-other" },
		func(v *protocol.NodeEvidence) { v.TaskID = "run-other" },
		func(v *protocol.NodeEvidence) { v.AttemptID = "attempt-other" },
		func(v *protocol.NodeEvidence) { v.Generation = 0 },
		func(v *protocol.NodeEvidence) { v.Status = "MISSING" },
		func(v *protocol.NodeEvidence) {
			v.Logs = []protocol.NodeLog{{Name: "jmeter.log", Status: "COLLECTED", Content: "ERROR text", Size: 1}}
		},
		func(v *protocol.NodeEvidence) { v.Logs = []protocol.NodeLog{{Name: "../../secret", Status: "MISSING"}} },
		func(v *protocol.NodeEvidence) {
			v.Logs = []protocol.NodeLog{{Name: "jmeter.log", Status: "COLLECTED", Content: strings.Repeat("x", (32<<10)+1)}}
		},
	} {
		v := base
		mutate(&v)
		if _, e := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{v}); e == nil {
			t.Fatal("invalid node evidence accepted")
		}
	}
	if _, e := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{base, base}); e == nil {
		t.Fatal("duplicate node evidence accepted")
	}
}

func TestPressureClusterIssuesKeepsNodeAndStepScope(t *testing.T) {
	items := []reporting.Issue{{NodeID: "worker-1", StepKey: "step-a", Count: 1, Message: "timeout"}, {NodeID: "worker-2", StepKey: "step-a", Count: 1, Message: "timeout"}, {NodeID: "worker-1", StepKey: "step-b", Count: 1, Message: "timeout"}, {NodeID: "worker-1", StepKey: "step-a", Count: 2, Message: "timeout"}}
	got := pressureClusterIssues(items)
	if len(got) != 3 || got[0].Count != 3 {
		t.Fatal("node/step failures merged", got)
	}
}

func TestPressureNodeLogStatusAndScope(t *testing.T) {
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{"worker-1"}, "node_evidence": []protocol.NodeEvidence{{NodeID: "worker-1", Generation: 2, Status: "COLLECTED", Logs: []protocol.NodeLog{{Name: "jmeter.log", Status: "COLLECTED", Content: "INFO ok", Truncated: true, Size: 100}, {Name: "console.log", Status: "COLLECTED"}}}}}
	a := reporting.Analysis{Issues: []reporting.Issue{{SourceKind: "jtl", SourceName: "results.jtl", Category: "sample_failure", NodeID: "worker-1", StepKey: "step-1", Message: "timeout", Count: 1}}}
	nodes, logs := pressureNodeDetailData("report-1", a, run)
	if len(nodes) != 1 || obj(nodes[0])["log_status"] != "PARTIAL" || len(logs) != 3 || obj(logs[0])["step_key"] != "" || obj(logs[2])["step_key"] != "step-1" {
		t.Fatal(nodes, logs)
	}
}

func TestPressureNodeDetailFiltersAndLegacyIsolation(t *testing.T) {
	csv := "timeStamp,elapsed,label,success,responseMessage,engine_id\n1793926800000,100,login,true,OK,worker-1\n1793926801000,300,login,false,timeout,worker-2\n1793926802000,200,query,true,OK,worker-2\n"
	p, run, cb := pressureFixtureWith(t, map[string]string{"results.jtl": csv})
	run["workers"] = []any{"worker-1", "worker-2", "worker-3"}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	cb["node_evidence"] = []protocol.NodeEvidence{{NodeID: "worker-2", TaskID: str(run["id"]), AttemptID: str(run["attempt_id"]), Generation: 2, Status: "COLLECTED", Logs: []protocol.NodeLog{{Name: "jmeter.log", Content: "ERROR token=fixture-private-value", Status: "COLLECTED", LineFrom: 1, LineTo: 1, Size: 50}}}}
	r := pressureComplete(t, p, run, cb)
	if r["status"] != "READY" || number(r["node_detail_version"]) != 1 {
		t.Fatal(r["status"], r["error"])
	}
	id := str(r["id"])
	read := func(kind, query string) Obj {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/pressure-reports/"+id+"/"+kind+"?"+query, nil)
		var got Obj
		var e error
		if kind == "steps" || kind == "issues" {
			got, e = p.pressureChildren(id, kind, req)
		} else {
			got, e = p.pressureNodeChildren(id, kind, req)
		}
		if e != nil {
			t.Fatal(e)
		}
		return got
	}
	nodes := read("nodes", "")
	if number(nodes["total"]) != 3 {
		t.Fatal(nodes)
	}
	for _, v := range arr(nodes["items"]) {
		n := obj(v)
		if n["node_id"] == "worker-3" && (n["status"] != "MISSING" || obj(n["metrics"])["success_rate"] != nil) {
			t.Fatal(n)
		}
	}
	global := read("steps", "limit=100")
	if number(global["total"]) != 2 {
		t.Fatal("default steps must stay global", global)
	}
	steps := read("steps", "node_id=worker-2&limit=100")
	if number(steps["total"]) != 2 {
		t.Fatal(steps)
	}
	var key string
	for _, v := range arr(steps["items"]) {
		if obj(v)["label"] == "login" {
			key = str(obj(v)["step_key"])
		}
	}
	issues := read("issues", "node_id=worker-2&step_key="+key)
	if number(issues["total"]) != 1 || number(obj(arr(issues["items"])[0])["count"]) != 1 {
		t.Fatal(issues)
	}
	logs := read("logs", "node_id=worker-2&step_key="+key)
	if number(logs["total"]) != 1 || obj(arr(logs["items"])[0])["scope"] != "step" {
		t.Fatal("worker log falsely attributed to step", logs)
	}
	allLogs := read("logs", "node_id=worker-2&limit=100")
	if strings.Contains(string(encoded(allLogs)), "fixture-private-value") {
		t.Fatal("node logs leaked")
	}
	for _, kind := range []string{"nodes", "logs"} {
		if _, e := p.forProject("project-other").pressureNodeChildren(id, kind, httptest.NewRequest("GET", "/", nil)); e == nil {
			t.Fatal("cross project nodes/logs readable")
		}
	}
	stored, _ := p.get("runs", str(run["id"]))
	if strings.Contains(string(encoded(stored["node_evidence"])), "fixture-private-value") {
		t.Fatal("persisted evidence not redacted")
	}
	conflict := clone(cb)
	conflict["node_evidence"] = []any{}
	if _, e := p.runnerCallback(context.Background(), "controller-a", conflict); e == nil {
		t.Fatal("raw duplicate conflict lost after redaction")
	}
	if number(read("steps", "node_id=worker-2%27%20OR%201%3D1--")["total"]) != 0 {
		t.Fatal("node filter was not parameterized")
	}
	page := read("logs", "node_id=worker-2&limit=1&offset=1")
	if len(arr(page["items"])) != 1 || number(page["total"]) < 2 {
		t.Fatal("scoped log pagination", page)
	}
	delete(r, "node_detail_version")
	delete(r, "nodes")
	delete(r, "node_logs")
	if _, e := p.DB.Exec("UPDATE pressure_reports SET data=? WHERE id=?", string(encoded(r)), id); e != nil {
		t.Fatal(e)
	}
	legacy := read("nodes", "")
	if number(legacy["detail_version"]) != 0 || number(legacy["total"]) != 0 {
		t.Fatal("old report compatibility", legacy)
	}
}
