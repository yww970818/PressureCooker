package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

func TestWorkerSubmissionReservesGloballyAndReplaysIdempotently(t *testing.T) {
	p, plan := mysqlFixture(t)
	environment := fixtureEnvironmentID(t, p)
	payload := Obj{"cluster_id": clusterID("controller-a"), "environment_id": environment, "name": "first", "plan_id": plan["id"], "workers": []any{"worker-1"}}
	first, created, err := p.createRun(payload, "reserved")
	if err != nil || !created {
		t.Fatal("first submission", created, err)
	}
	replayed, created, err := p.createRun(payload, "reserved")
	if err != nil || created || replayed["id"] != first["id"] {
		t.Fatal("idempotent replay", created, err)
	}
	conflict := clone(payload)
	conflict["cluster_id"] = clusterID("controller-b")
	if _, _, err = p.createRun(conflict, "overlap"); err == nil {
		t.Fatal("overlapping worker was accepted in another cluster")
	} else {
		var problem *APIError
		if !errors.As(err, &problem) || problem.Status != 409 || problem.Code != "worker_busy" || !strings.Contains(problem.Message, "worker-1") {
			t.Fatal("expected worker conflict", err)
		}
	}
	conflict["workers"] = []any{"worker-2"}
	if _, created, err = p.createRun(conflict, "disjoint"); err != nil || !created {
		t.Fatal("independent worker blocked", err)
	}
	project, err := p.projectCreate(Obj{"name": "other"})
	if err != nil {
		t.Fatal(err)
	}
	other := p.forProject(str(project["id"]))
	otherPlan, err := other.createPlan(Obj{"name": "other", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := Obj{"cluster_id": clusterID("controller-b"), "environment_id": fixtureEnvironmentID(t, other), "name": "other", "plan_id": otherPlan["id"], "workers": []any{"worker-1"}}
	if _, _, err = other.createRun(foreign, "other-project"); err == nil {
		t.Fatal("cross-project worker conflict accepted")
	}
	groups, _, err := p.attachClusterStatusQ(p.DB, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		for _, value := range arr(obj(group)["workers"]) {
			worker := obj(value)
			if str(worker["id"]) == "worker-1" && !boolean(worker["occupied"]) {
				t.Fatal("queued reservation missing from node projection")
			}
			if _, exposed := worker["occupied_task"]; exposed {
				t.Fatal("foreign task metadata exposed")
			}
		}
	}
}

func TestWorkerSubmissionOccupancyLifecycle(t *testing.T) {
	for _, status := range []string{"QUEUED", "PREPARING", "RUNNING", "STOPPING", "RECOVERING", "UPLOADING", "CALLBACK_PENDING", "COMPLETED", "FAILED", "STOPPED", "INTERRUPTED"} {
		t.Run(status, func(t *testing.T) {
			p, plan := mysqlFixture(t)
			first := runFixture(t, p, plan, "first")
			first["status"] = status
			if err := p.transaction(func(tx *Tx) error { return p.saveRun(tx, first) }); err != nil {
				t.Fatal(err)
			}
			_, _, err := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, p), "name": "next", "plan_id": plan["id"], "workers": []any{"worker-1"}}, "next")
			occupied := status == "QUEUED" || status == "PREPARING" || status == "RUNNING" || status == "STOPPING" || status == "RECOVERING"
			if (err != nil) != occupied {
				t.Fatalf("status %s: expected occupied=%v, got %v", status, occupied, err)
			}
		})
	}
}

func TestWorkerSubmissionConcurrentOverlapOnlyOneSucceeds(t *testing.T) {
	p, plan := mysqlFixture(t)
	payload := Obj{"cluster_id": clusterID("controller-a"), "environment_id": fixtureEnvironmentID(t, p), "name": "concurrent", "plan_id": plan["id"], "workers": []any{"worker-1"}}
	project, err := p.projectCreate(Obj{"name": "concurrent project"})
	if err != nil {
		t.Fatal(err)
	}
	other := p.forProject(str(project["id"]))
	otherPlan, err := other.createPlan(Obj{"name": "concurrent plan", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	otherPayload := Obj{"cluster_id": clusterID("controller-b"), "environment_id": fixtureEnvironmentID(t, other), "name": "concurrent other", "plan_id": otherPlan["id"], "workers": []any{"worker-1"}}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, task := range []struct {
		scope   *Platform
		payload Obj
		key     string
	}{{p, payload, "a"}, {other, otherPayload, "b"}} {
		group.Add(1)
		go func(scope *Platform, payload Obj, key string) {
			defer group.Done()
			<-start
			_, _, err := scope.createRun(payload, key)
			results <- err
		}(task.scope, task.payload, task.key)
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			var problem *APIError
			if !errors.As(err, &problem) || problem.Code != "worker_busy" {
				t.Fatal(err)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("overlapping concurrent submissions succeeded %d times", successes)
	}
	var count int
	if err := p.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM task_dispatches").Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected submission left dispatch", count, err)
	}
}

func TestWorkerClaimsAllowDisjointRunsAndRejectForeignWorkerLease(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	for _, worker := range []string{"worker-1", "worker-2", "worker-3"} {
		run, _, err := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "name": worker, "plan_id": plan["id"], "workers": []any{worker}}, worker)
		if err != nil {
			t.Fatal(err)
		}
		lease := queue.Token("controller-a", str(run["id"]), str(run["attempt_id"]), "fixture")
		leaseWorker := worker
		if worker == "worker-3" {
			leaseWorker = "worker-4"
		}
		if owned, err := q.Acquire(context.Background(), lease, leaseWorker); err != nil || !owned {
			t.Fatal("acquire worker lease", owned, err)
		}
		defer q.Release(context.Background(), lease)
		claim, err := p.runnerClaim(context.Background(), "controller-a", Obj{"task_id": run["id"], "attempt_id": run["attempt_id"], "lease_token": lease})
		if worker == "worker-3" {
			var problem *APIError
			if !errors.As(err, &problem) || problem.Code != "lease_not_owned" {
				t.Fatal("foreign worker lease accepted", claim, err)
			}
		} else if err != nil || !boolean(claim["execute"]) {
			t.Fatal("independent execution blocked", claim, err)
		}
	}
}

func TestWorkerRecoveryDoesNotWaitForUnrelatedRunningLease(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	run := registeredRecoveryRun(t, p, plan, environment)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	other := queue.Token("controller-b", "other-task", "other-attempt", "fixture")
	if owned, err := q.Acquire(context.Background(), other, "worker-2"); err != nil || !owned {
		t.Fatal("unrelated lease", owned, err)
	}
	defer q.Release(context.Background(), other)
	stop := startRecoveryEpochPublisher(t, q, recoveryWorkerReport)
	defer stop()
	scheduler := newScheduler(p, p.Config.ManagerID)
	defer scheduler.close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = scheduler.resolveRedisOwnerContext(ctx, run); err != nil {
		t.Fatal(err)
	}
	stored, err := p.get("runs", str(run["id"]))
	if err != nil || stored["status"] != "INTERRUPTED" {
		t.Fatal("unrelated task prevented recovery", stored["status"], err)
	}
	if owned, err := q.LeaseOwned(context.Background(), other); err != nil || !owned {
		t.Fatal("recovery disturbed unrelated task", owned, err)
	}
}

func TestWorkerClaimCanDrainPreexistingOverlappingQueue(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	first, _, err := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "name": "old queue", "plan_id": plan["id"], "workers": []any{"worker-1"}}, "first")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a task already queued before worker reservation was introduced.
	old := clone(first)
	old["id"], old["attempt_id"] = newID("run-"), newID("attempt-")
	if err = p.transaction(func(tx *Tx) error { return p.saveRun(tx, old) }); err != nil {
		t.Fatal(err)
	}
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	lease := queue.Token("controller-a", str(first["id"]), str(first["attempt_id"]), "fixture")
	if owned, err := q.Acquire(context.Background(), lease, "worker-1"); err != nil || !owned {
		t.Fatal(owned, err)
	}
	defer q.Release(context.Background(), lease)
	claim, err := p.runnerClaim(context.Background(), "controller-a", Obj{"task_id": first["id"], "attempt_id": first["attempt_id"], "lease_token": lease})
	if err != nil || !boolean(claim["execute"]) {
		t.Fatal("old overlapping queue cannot make progress", claim, err)
	}
}

func TestWorkerRecoveryRotatesPastAnUnreachableTask(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	first := registeredRecoveryRun(t, p, plan, environment)
	second, _, err := p.createRun(Obj{"cluster_id": clusterID("controller-a"), "environment_id": environment["id"], "name": "second stale", "plan_id": plan["id"], "workers": []any{"worker-2"}}, "second-stale")
	if err != nil {
		t.Fatal(err)
	}
	second, err = p.updateRun(str(second["id"]), Obj{"status": "RUNNING", "runner_lease_token": "expired-second", "runner_heartbeat_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	stop := startRecoveryEpochPublisher(t, q, func(epoch string, sequence int64) protocol.CoreNodeReport {
		report := recoveryWorkerReport(epoch, sequence)
		report.Workers[0].Status = "running"
		report.Workers[0].Job = &protocol.NodeJob{ID: str(first["id"]), Kind: "run", State: "RUNNING"}
		report.Workers = append(report.Workers, nodeReportFixture("worker-2", "worker"))
		return report
	})
	defer stop()
	p.Config.RunnerStaleAfter = time.Second
	scheduler := newScheduler(p, p.Config.ManagerID)
	defer scheduler.close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	err = scheduler.reconcileRedisRunsContext(ctx, []any{first, second})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("first unreachable task should wait", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = scheduler.reconcileRedisRunsContext(ctx, []any{first, second})
	stored, err := p.get("runs", str(second["id"]))
	if err != nil || stored["status"] != "INTERRUPTED" {
		t.Fatal("unreachable first task starved independent recovery", stored["status"], err)
	}
}
