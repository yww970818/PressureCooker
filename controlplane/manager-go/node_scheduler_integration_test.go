package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/protocol"
)

var schedulerReportPublishers sync.Map // *Platform -> test-only report publisher
var recoveryReportBootAt = time.Now().Add(-time.Minute).UnixMilli()

type schedulerPeerReports struct {
	p        *Platform
	peers    []*agentPeer
	sequence int64
	bootAt   int64
}

func peerNodeReport(peer *agentPeer) protocol.NodeReport {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	r := nodeReportFixture(peer.id, peer.role)
	r.Generation = int64(peer.generation)
	if !peer.available {
		r.Status, r.Healthy, r.Generation = "unreachable", false, 0
		return r
	}
	if len(peer.job) > 0 {
		r.Job = &protocol.NodeJob{ID: str(peer.job["id"]), Kind: "run", State: str(peer.job["state"])}
		if r.Job.State == "RUNNING" {
			r.Status = "running"
		}
	}
	if peer.lastReset != "" {
		op := obj(peer.resets[peer.lastReset])
		r.ResetOperation = &protocol.NodeResetOperation{ID: str(op["id"]), State: str(op["state"]), ToGeneration: int64(peer.generation)}
		if r.ResetOperation.State == "RESETTING" {
			r.ResetOperation.State = "RUNNING"
		}
	}
	return r
}

func (f *schedulerPeerReports) publish(t *testing.T) {
	t.Helper()
	q, err := f.p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	epoch, err := q.NodeObservationEpoch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.sequence++
	for _, core := range f.peers[:2] {
		r := peerNodeReport(core)
		r.Sequence, r.BootStartedAtMS = f.sequence, f.bootAt
		received := time.Now()
		if !r.Healthy {
			// A stopped Core sends nothing; age its previous snapshot instead of
			// discovering its loss through a manager-initiated HTTP request.
			received = received.Add(-30 * time.Second)
			r.Status, r.Healthy, r.Generation = "idle", true, 1
			r.ObservedAt = received.UTC().Format(time.RFC3339Nano)
		}
		report := protocol.CoreNodeReport{NodeReport: r, ObservationEpoch: epoch}
		for _, worker := range f.peers[2:] {
			report.Workers = append(report.Workers, peerNodeReport(worker))
		}
		if ok, err := q.StoreNodeReport(context.Background(), report, received.UnixMilli()); err != nil || !ok {
			t.Fatal("publish synthetic Core snapshot", ok, err)
		}
	}
}

func publishSchedulerReports(t *testing.T, p *Platform) {
	t.Helper()
	if value, ok := schedulerReportPublishers.Load(p); ok {
		value.(*schedulerPeerReports).publish(t)
	}
}

type schedulerStatusTrap struct{ calls *atomic.Int32 }

func (s schedulerStatusTrap) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/status" {
		s.calls.Add(1)
		return nil, errors.New("manager status probe forbidden")
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestReportedRecoveryWorkersRequireCurrentEpochAndPhysicalIdle(t *testing.T) {
	epoch := protocol.NodeObservationEpoch("new recovery lease")
	workers := []protocol.Worker{{ID: "worker-1"}, {ID: "worker-2"}}
	row := func(id string) Obj {
		return Obj{"id": id, "healthy": true, "status": "idle", "generation": 2, "job": nil, "observation_epoch": epoch, "reported_by": "controller-b"}
	}
	for _, test := range []struct {
		name   string
		change func([]any)
		want   bool
	}{
		{"other Core fresh evidence", func([]any) {}, true},
		{"old epoch", func(v []any) { obj(v[0])["observation_epoch"] = protocol.NodeObservationEpoch("expired lease") }, false},
		{"expired", func(v []any) { obj(v[0])["healthy"] = false; obj(v[0])["status"] = "report_timeout" }, false},
		{"missing worker", func(v []any) { obj(v[0])["id"] = "worker-other" }, false},
		{"unknown generation", func(v []any) { obj(v[0])["generation"] = 0 }, false},
		{"busy", func(v []any) { obj(v[0])["status"] = "running" }, false},
		{"stale job", func(v []any) { obj(v[0])["job"] = Obj{"id": "old"} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := []any{row("worker-1"), row("worker-2")}
			test.change(rows)
			if got := reportedRecoveryWorkersReady(workers, rows, epoch); got != test.want {
				t.Fatal(got)
			}
		})
	}
}

func schedulerReportPublishersValue(t *testing.T, p *Platform) *schedulerPeerReports {
	t.Helper()
	value, ok := schedulerReportPublishers.Load(p)
	if !ok {
		t.Fatal("missing synthetic Core report publisher")
	}
	return value.(*schedulerPeerReports)
}

func registeredRecoveryRun(t *testing.T, p *Platform, plan, environment Obj) Obj {
	t.Helper()
	run, _, err := p.createRun(Obj{"name": "cached recovery", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a"), "workers": []any{"worker-1"}}, newID("recovery-test-"))
	if err != nil {
		t.Fatal(err)
	}
	run, err = p.updateRun(str(run["id"]), Obj{"status": "RUNNING", "runner_lease_token": "expired-owner", "runner_heartbeat_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func recoveryWorkerReport(epoch string, sequence int64) protocol.CoreNodeReport {
	core := nodeReportFixture("controller-b", "controller")
	core.Sequence = sequence
	core.BootStartedAtMS = recoveryReportBootAt
	worker := nodeReportFixture("worker-1", "worker")
	// Probe timestamps can precede the manager's clock. The opaque lease epoch
	// proves causality; received-at freshness is enforced by nodeInventory.
	worker.ObservedAt = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	return protocol.CoreNodeReport{NodeReport: core, Workers: []protocol.NodeReport{worker}, ObservationEpoch: epoch}
}

func startRecoveryEpochPublisher(t *testing.T, q interface {
	NodeObservationEpoch(context.Context) (string, error)
	StoreNodeReport(context.Context, protocol.CoreNodeReport, int64) (bool, error)
}, report func(string, int64) protocol.CoreNodeReport) func() {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		var sequence int64 = 10
		last := ""
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-time.After(10 * time.Millisecond):
			}
			epoch, err := q.NodeObservationEpoch(context.Background())
			if err != nil {
				done <- err
				return
			}
			if epoch == protocol.NodeObservationEpoch("") || epoch == last {
				continue
			}
			sequence++
			ok, err := q.StoreNodeReport(context.Background(), report(epoch, sequence), time.Now().UnixMilli())
			if err != nil || !ok {
				done <- fmt.Errorf("publish epoch snapshot accepted=%v: %w", ok, err)
				return
			}
			last = epoch
		}
	}()
	return func() {
		close(stop)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestReportedRedisRecoveryWaitsForNewLeaseEpoch(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	nodeRedisFixture(t, p)
	run := registeredRecoveryRun(t, p, plan, environment)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if ok, err := q.StoreNodeReport(context.Background(), recoveryWorkerReport(protocol.NodeObservationEpoch("expired-owner"), 1), time.Now().UnixMilli()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	s := newScheduler(p, p.Config.ManagerID)
	defer s.close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	err = s.resolveRedisOwnerContext(ctx, run)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("old cached epoch resolved owner", err)
	}
	stored, err := p.get("runs", str(run["id"]))
	if err != nil || stored["status"] != "RUNNING" {
		t.Fatal(stored, err)
	}
	stop := startRecoveryEpochPublisher(t, q, recoveryWorkerReport)
	defer stop()
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = s.resolveRedisOwnerContext(ctx, run); err != nil {
		t.Fatal(err)
	}
	stored, err = p.get("runs", str(run["id"]))
	if err != nil || stored["status"] != "INTERRUPTED" || !boolean(stored["runner_reconciled"]) {
		t.Fatal(stored, err)
	}
	epoch, err := q.NodeObservationEpoch(context.Background())
	if err != nil || epoch != protocol.NodeObservationEpoch("") {
		t.Fatal("recovery lease was not released", err)
	}
}

func TestReportedRedisRecoveryDeadlineCancelsSQLRowLock(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	nodeRedisFixture(t, p)
	run := registeredRecoveryRun(t, p, plan, environment)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	stop := startRecoveryEpochPublisher(t, q, recoveryWorkerReport)
	defer stop()
	lock, err := p.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if err = lockClusterConfigurations(lock); err != nil {
		t.Fatal(err)
	}
	s := newScheduler(p, p.Config.ManagerID)
	defer s.close()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.resolveRedisOwnerContext(ctx, run) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		lock.Rollback()
		<-done
		t.Fatal("recovery ignored context while waiting for SQL row lock")
	}
	stored, err := p.get("runs", str(run["id"]))
	if err != nil || stored["status"] != "RUNNING" {
		t.Fatal(stored, err)
	}
}

func TestReportedSchedulerTickReturnsRecoveryCancellation(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	nodeRedisFixture(t, p)
	_ = registeredRecoveryRun(t, p, plan, environment)
	p.Config.RunnerStaleAfter = time.Second
	s := newScheduler(p, p.Config.ManagerID)
	defer s.close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := s.tickContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("scheduler swallowed recovery cancellation", err)
	}
}

func TestReportedRedisRecoveryFencesChangedLeaseAndTask(t *testing.T) {
	for _, change := range []string{"lease", "heartbeat", "attempt", "recorded owner"} {
		t.Run(change, func(t *testing.T) {
			p, plan, environment := newRunnerFixture(t)
			nodeRedisFixture(t, p)
			run := registeredRecoveryRun(t, p, plan, environment)
			q, err := p.runnerQueue()
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			stop := startRecoveryEpochPublisher(t, q, recoveryWorkerReport)
			defer stop()
			lock, err := p.DB.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback()
			if err = lockClusterConfigurations(lock); err != nil {
				t.Fatal(err)
			}
			s := newScheduler(p, p.Config.ManagerID)
			defer s.close()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.resolveRedisOwnerContext(ctx, run) }()
			deadline := time.Now().Add(2 * time.Second)
			for p.DB.Stats().InUse < 2 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if p.DB.Stats().InUse < 2 {
				lock.Rollback()
				<-done
				t.Fatal("recovery never reached the fenced SQL transaction")
			}
			var replacement *redis.Client
			if change == "lease" {
				replacement = redis.NewClient(&redis.Options{Addr: p.Config.RedisAddr, Password: p.Config.RedisPassword, DB: p.Config.RedisDB})
				defer replacement.Close()
				if err = replacement.Set(context.Background(), p.Config.RedisPrefix+":lease", "replacement-owner", time.Minute).Err(); err != nil {
					t.Fatal(err)
				}
			} else {
				update := Obj{}
				switch change {
				case "heartbeat":
					update["runner_heartbeat_at"] = now()
				case "attempt":
					update["attempt_id"] = newID("attempt-")
				case "recorded owner":
					update["runner_lease_token"] = "replacement-recorded-owner"
				}
				if _, err = p.updateRun(str(run["id"]), update); err != nil {
					t.Fatal(err)
				}
			}
			lock.Rollback()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			stored, err := p.get("runs", str(run["id"]))
			if err != nil || stored["status"] != "RUNNING" || boolean(stored["runner_reconciled"]) {
				t.Fatal("recovery cleared changed ownership evidence", stored, err)
			}
			if replacement != nil {
				value, err := replacement.Get(context.Background(), p.Config.RedisPrefix+":lease").Result()
				if err != nil || value != "replacement-owner" {
					t.Fatal("recovery released another owner's lease", err)
				}
			}
		})
	}
}
