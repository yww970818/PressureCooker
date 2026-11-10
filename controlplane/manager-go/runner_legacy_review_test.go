package main

import (
	"testing"
)

func runnerLegacyRun(t *testing.T, p *Platform, plan Obj, controller string, workers []any) Obj {
	t.Helper()
	run := Obj{"id": newID("run-"), "project_id": p.projectID(), "status": "QUEUED", "created_at": now(), "name": "legacy", "plan_id": plan["id"], "plan_sha256": plan["sha256"], "properties": Obj{}, "workers": workers, "cluster_id": clusterID(controller), "cluster_snapshot": Obj{"id": clusterID(controller), "controller_id": controller, "worker_ids": workers}}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	return run
}

func TestRunnerGlobalQueueCountsDoNotReadPrivateHistory(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, _ = schedulerFixture(t, p)
	other, e := p.projectCreate(Obj{"name": "B"})
	if e != nil {
		t.Fatal(e)
	}
	for _, status := range []string{"QUEUED", "RUNNING", "CALLBACK_PENDING", "UPLOADING"} {
		id := newID("run-")
		if _, e = p.DB.Exec("INSERT INTO runs(id,status,created_at,data,project_id) VALUES(?,?,?,?,?)", id, status, now(), `{"execution_mode":"redis","environment_envelope":"intentionally-unreadable"}`, defaultProjectID); e != nil {
			t.Fatal(e)
		}
	}
	response, e := p.forProject(str(other["id"])).cluster()
	if e != nil {
		t.Fatal("counts opened private run envelope", e)
	}
	counts := obj(response["queue"])
	if number(counts["queued"]) != 1 || number(counts["running"]) != 1 {
		t.Fatal("counts not global / physical", counts)
	}
}
