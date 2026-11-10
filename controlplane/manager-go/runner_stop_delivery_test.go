package main

import (
	"bytes"
	"net/http/httptest"
	"pressurecooker/execution/protocol"
	"testing"
	"time"
)

func TestRunnerStopAfterExecutionPreservesReportedOutcome(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	for _, tc := range []struct {
		delivery, result string
		exit             int
	}{{"UPLOADING", "COMPLETED", 0}, {"CALLBACK_PENDING", "FAILED", 1}} {
		t.Run(tc.delivery, func(t *testing.T) {
			run, _, e := p.createRun(Obj{"name": tc.delivery, "plan_id": plan["id"], "cluster_id": clusterID("controller-a"), "environment_id": environment["id"]}, "delivery-stop-"+tc.delivery)
			if e != nil {
				t.Fatal(e)
			}
			id := str(run["id"])
			started := now()
			if _, e = p.updateRun(id, Obj{"status": tc.delivery, "started_at": started, "controller_id": "controller-a"}); e != nil {
				t.Fatal(e)
			}
			req := httptest.NewRequest("POST", "/api/runs/"+id+"/stop", bytes.NewReader(encoded(Obj{"mode": "now"})))
			req.Header.Set("Authorization", "Bearer admin-secret")
			req.Header.Set("X-Project-ID", p.projectID())
			rec := httptest.NewRecorder()
			Handler{p}.ServeHTTP(rec, req)
			if rec.Code != 202 {
				t.Fatal("delivery stop did not return unchanged run", rec.Code)
			}
			stored, e := p.get("runs", id)
			if e != nil {
				t.Fatal(e)
			}
			if str(stored["status"]) != tc.delivery || str(stored["stop_mode"]) != "" || stored["stop_requested_at"] != nil {
				t.Error("delivery-only stop wrote execution intent")
			}
			cb := dtoObject(protocol.Callback{EventID: newID("event-"), TaskID: id, AttemptID: str(run["attempt_id"]), RunnerID: "controller-a", Status: tc.result, ExitCode: tc.exit, StartedAt: started, FinishedAt: now(), MissingFiles: rawFiles})
			raw := encoded(cb)
			req = httptest.NewRequest("POST", protocol.CallbackPath, bytes.NewReader(raw))
			protocol.SignRequest(req, raw, "controller-a", p.Config.AgentToken, time.Now())
			rec = httptest.NewRecorder()
			Handler{p}.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatal("original authenticated callback rejected", rec.Code, rec.Body.String())
			}
			finished, e := p.getRun(id)
			if e != nil || finished["status"] != tc.result || int(number(finished["exit_code"])) != tc.exit {
				t.Errorf("stop changed original reported outcome: want %s, got %v, err %v", tc.result, finished["status"], e)
			}
		})
	}
}
