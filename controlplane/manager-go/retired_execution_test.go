package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"pressurecooker/execution/queue"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMySQLRetiredExecutionNeverDispatchesOrRewritesHistory(t *testing.T) {
	p, plan := mysqlFixture(t)
	nodeRedisFixture(t, p)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer server.Close()
	p.Config.ControllerURLs["controller-a"] = server.URL
	run := legacyRunFixture(t, p, plan, "retired")
	before, err := p.get("runs", str(run["id"]))
	if err != nil {
		t.Fatal(err)
	}
	s := newScheduler(p, p.Config.ManagerID)
	defer s.close()
	if err = s.tickContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := p.get("runs", str(run["id"]))
	if err != nil || string(encoded(before)) != string(encoded(after)) || calls.Load() != 0 {
		t.Fatal("retired execution was contacted or history rewritten", err, calls.Load())
	}
	if _, err = p.stopRun(str(run["id"]), "now"); err == nil {
		t.Fatal("retired execution accepts remote stop requests")
	}
}

func TestMySQLRetirementRequiresEvidenceAndPreservesPrivateHistory(t *testing.T) {
	p, plan := mysqlFixture(t)
	nodeRedisFixture(t, p)
	p.Config.WorkerURLs = map[string]string{"worker-1": "http://127.0.0.1:1"}
	run := legacyRunFixture(t, p, plan, "retirement")
	id := str(run["id"])
	before, err := p.get("runs", id)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.retiredExecutions()
	if err != nil {
		t.Fatal(err)
	}
	item := obj(arr(listing["items"])[0])
	fingerprint := str(item["fingerprint"])
	if len(item) != 4 || fingerprint == "" {
		t.Fatal("retirement audit exposed private fields", listing)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.retireExecution(ctx, id, fingerprint); err == nil {
		t.Fatal("cancelled retirement accepted")
	}
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	stop := startRecoveryEpochPublisher(t, q, recoveryWorkerReport)
	defer stop()
	if _, err = p.retireExecution(context.Background(), id, strings.Repeat("0", 64)); err == nil {
		t.Fatal("stale fingerprint accepted")
	}
	result, err := p.retireExecution(context.Background(), id, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "INTERRUPTED" {
		t.Fatal(result)
	}
	after, err := p.get("runs", id)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"status", "finished_at", "retirement"} {
		delete(after, k)
		delete(before, k)
	}
	if string(encoded(after)) != string(encoded(before)) {
		t.Fatal("retirement changed original evidence")
	}
	if _, err = p.retireExecution(context.Background(), id, fingerprint); err != nil {
		t.Fatal("repeat retirement is not idempotent", err)
	}
	var count int
	if err = p.DB.QueryRow("SELECT COUNT(*) FROM audit WHERE action='execution.retire' AND object_id=?", id).Scan(&count); err != nil || count != 1 {
		t.Fatal("retirement audit is not unique", count, err)
	}
}

func TestMySQLRetiredActiveRecordDoesNotBlockCurrentClaim(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	old := legacyRunFixture(t, p, plan, "active-retired")
	if _, err := p.updateRun(str(old["id"]), Obj{"status": "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	run, _, err := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "plan_id": plan["id"], "name": "current", "properties": Obj{}}, "current-claim")
	if err != nil {
		t.Fatal(err)
	}
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	token := queue.Token("controller-a", str(run["id"]), str(run["attempt_id"]), "current")
	if ok, err := q.Acquire(context.Background(), token); err != nil || !ok {
		t.Fatal(ok, err)
	}
	result, err := p.runnerClaim(context.Background(), "controller-a", Obj{"task_id": run["id"], "attempt_id": run["attempt_id"], "lease_token": token})
	if err != nil || result["execute"] != true {
		t.Fatal("old execution blocked current owner", result, err)
	}
}
