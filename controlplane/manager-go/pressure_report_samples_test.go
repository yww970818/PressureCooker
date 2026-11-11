package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"pressurecooker/execution/protocol"
	"pressurecooker/manager/reporting"
)

func TestPressureRequestEvidenceFinalEncodedBudgetIncludesLogs(t *testing.T) {
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{}}
	entries := []protocol.NodeEvidence{}
	for i := 0; i < 33; i++ {
		node := fmt.Sprintf("worker-%d", i)
		run["workers"] = append(arr(run["workers"]), node)
		entries = append(entries, protocol.NodeEvidence{NodeID: node, TaskID: "run-1", AttemptID: "attempt-1", Generation: 1, Status: "COLLECTED", Logs: []protocol.NodeLog{{Name: "jmeter.log", Status: "COLLECTED", Content: strings.Repeat("\\", 32<<10), Size: 32 << 10}}})
	}
	got, e := pressureValidateNodeEvidence(run, entries)
	if e != nil {
		t.Fatal(e)
	}
	if len(encoded(got)) > protocol.MaxNodeEvidenceBytes {
		t.Fatal("encoded log escaping exceeded final evidence bound")
	}
}

func TestPressureRequestSampleReceiptBindsRawIdentityAndRejectsMalformedPayload(t *testing.T) {
	run := Obj{"id": "run-1", "attempt_id": "attempt-1", "workers": []any{"worker-1"}, "properties": Obj{"token": "fixture-private-value"}}
	label := "request fixture-private-value"
	base := Obj{"node_id": "worker-1", "task_id": "run-1", "attempt_id": "attempt-1", "generation": 2, "status": "COLLECTED", "logs": []any{}, "request_samples_captured": true, "request_samples": []any{Obj{"step_key": "untrusted-key", "label": label, "sample_kind": "unknown", "timestamp_ms": 1000, "elapsed_ms": 10, "success": true, "request_params": "{\"visible\":\"yes\",\"token\":\"fixture-private-value\"}", "response_body": "fixture-private-value", "assertions_captured": true, "assertions": []any{}}}}
	decodeEvidence := func(value Obj) protocol.NodeEvidence {
		t.Helper()
		var v protocol.NodeEvidence
		if e := json.Unmarshal(encoded(value), &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	got, e := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{decodeEvidence(base)})
	if e != nil || len(got) != 1 || len(got[0].RequestSamples) != 1 {
		t.Fatal(got, e)
	}
	if got[0].RequestSamples[0].StepKey != reporting.StableStepKey("unknown", label) || strings.Contains(string(encoded(got)), "fixture-private-value") {
		t.Fatal("identity was derived after masking, or receipt leaked credentials")
	}
	for _, mutate := range []func(Obj){
		func(v Obj) { obj(arr(v["request_samples"])[0])["label"] = "" },
		func(v Obj) { v["request_samples_captured"] = false },
		func(v Obj) { obj(arr(v["request_samples"])[0])["sample_kind"] = "invented" },
		func(v Obj) { obj(arr(v["request_samples"])[0])["timestamp_ms"] = -1 },
		func(v Obj) { obj(arr(v["request_samples"])[0])["response_body"] = strings.Repeat("x", 8193) },
		func(v Obj) {
			obj(arr(v["request_samples"])[0])["assertions_captured"] = false
			obj(arr(v["request_samples"])[0])["assertions"] = []any{Obj{"name": "assert", "failure": true}}
		},
		func(v Obj) { v["request_samples"] = make([]any, 65) },
	} {
		bad := clone(base)
		mutate(bad)
		if _, e := pressureValidateNodeEvidence(run, []protocol.NodeEvidence{decodeEvidence(bad)}); e == nil {
			t.Fatal("malformed sample receipt accepted")
		}
	}
}

func TestPressureRequestSamplesScopedImmutableAndRedacted(t *testing.T) {
	csv := "timeStamp,elapsed,label,success,responseMessage,engine_id\n1793926800000,100,submit,true,OK,worker-1\n1793926801000,300,submit,false,assertion failed,worker-2\n1793926802000,200,query,true,OK,worker-2\n"
	p, run, cb := pressureFixtureWith(t, map[string]string{"results.jtl": csv})
	run["workers"] = []any{"worker-1", "worker-2"}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	sample := func(label string, ts int64, elapsed int, success bool, body any, captured bool, assertions []any) Obj {
		return Obj{"label": label, "sample_kind": "unknown", "timestamp_ms": ts, "elapsed_ms": elapsed, "success": success, "method": "POST", "url": "https://svc.test/query?token=fixture-private-value", "request_params": "{\"filter\":\"open\",\"token\":\"fixture-private-value\"}", "response_code": "200", "response_body": body, "assertions_captured": captured, "assertions": assertions}
	}
	envelope := func(node string, samples []any) Obj {
		return Obj{"node_id": node, "task_id": run["id"], "attempt_id": run["attempt_id"], "generation": 2, "status": "COLLECTED", "logs": []any{}, "request_samples_captured": true, "request_samples": samples}
	}
	cb["node_evidence"] = []any{
		envelope("worker-1", []any{sample("submit", 1793926800000, 100, true, "{\"result\":\"sample-response-one\",\"authorization\":\"fixture-private-value\"}", true, []any{Obj{"name": "status equals 200", "failure": false, "error": false, "message": ""}})}),
		envelope("worker-2", []any{sample("submit", 1793926801000, 300, false, "sample-response-two", true, []any{Obj{"name": "business assertion", "failure": true, "error": false, "message": "token=fixture-private-value expected ready"}}), sample("query", 1793926802000, 200, true, "", true, []any{})}),
	}
	r := pressureComplete(t, p, run, cb)
	if r["status"] != "READY" {
		t.Fatal(r["error"])
	}
	id := str(r["id"])
	read := func(query string) Obj {
		t.Helper()
		v, e := p.pressureRequestSamples(id, httptest.NewRequest("GET", "/api/pressure-reports/"+id+"/samples?"+query, nil))
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	steps, e := p.pressureChildren(id, "steps", httptest.NewRequest("GET", "/?limit=100", nil))
	if e != nil {
		t.Fatal(e)
	}
	key, queryKey := "", ""
	for _, v := range arr(steps["items"]) {
		if obj(v)["label"] == "submit" {
			key = str(obj(v)["step_key"])
		} else {
			queryKey = str(obj(v)["step_key"])
		}
	}
	page := read("step_key=" + key + "&limit=1")
	if number(page["detail_version"]) != 1 || number(page["total"]) != 2 || len(arr(page["items"])) != 1 {
		t.Fatal(page)
	}
	next := read("step_key=" + key + "&limit=1&offset=1")
	if obj(arr(next["items"])[0])["id"] == obj(arr(page["items"])[0])["id"] {
		t.Fatal("sample pagination repeated a row")
	}
	scoped := read("step_key=" + key + "&node_id=worker-2")
	if number(scoped["total"]) != 1 {
		t.Fatal(scoped)
	}
	row := obj(arr(scoped["items"])[0])
	assertion := obj(arr(row["assertions"])[0])
	if row["response_body"] != "sample-response-two" || row["success"] != false || assertion["failure"] != true || !strings.Contains(str(row["request_params"]), "open") {
		t.Fatal("lost actual sample evidence", row)
	}
	empty := read("step_key=" + queryKey + "&node_id=worker-2")
	if obj(arr(empty["items"])[0])["response_body"] != "" || obj(arr(empty["items"])[0])["assertions_captured"] != true {
		t.Fatal("captured-empty lost", empty)
	}
	if number(read("step_key=" + key + "&node_id=worker-other")["total"]) != 0 || number(read("step_key=absent")["total"]) != 0 {
		t.Fatal("scope isolation lost")
	}
	if _, e := p.pressureRequestSamples(id, httptest.NewRequest("GET", "/", nil)); e == nil {
		t.Fatal("unscoped samples readable")
	}
	if _, e := p.pressureRequestSamples(id, httptest.NewRequest("GET", "/?step_key="+key+"&limit=0", nil)); e == nil {
		t.Fatal("invalid sample pagination accepted")
	}
	if _, e := p.forProject("project-other").pressureRequestSamples(id, httptest.NewRequest("GET", "/?step_key="+key, nil)); e == nil {
		t.Fatal("cross-project samples readable")
	}
	stored, _ := p.get("runs", str(run["id"]))
	if strings.Contains(string(encoded(stored["node_evidence"])), "fixture-private-value") {
		t.Fatal("stored callback sample leaked credential")
	}
	if strings.Contains(string(encoded(r["request_samples"])), "fixture-private-value") {
		t.Fatal("report sample leaked credential")
	}
	summary, e := p.pressureRead(id, true)
	if e != nil {
		t.Fatal(e)
	}
	if summary["request_samples"] != nil || strings.Contains(string(encoded(pressurePublicReport(r))), "sample-response-two") {
		t.Fatal("sample body embedded in public report")
	}
	delete(r, "request_samples")
	delete(r, "request_samples_version")
	if _, e = p.DB.Exec("UPDATE pressure_reports SET data=? WHERE id=?", string(encoded(r)), id); e != nil {
		t.Fatal(e)
	}
	legacy := read("step_key=" + key)
	if number(legacy["detail_version"]) != 0 || number(legacy["total"]) != 0 {
		t.Fatal("legacy sample state fabricated", legacy)
	}
}
