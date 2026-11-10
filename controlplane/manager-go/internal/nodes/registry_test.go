package nodes

import (
	"context"
	"errors"
	"io"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

var inventoryTime = time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)

func nodeReport(id, role string, observed time.Time) protocol.NodeReport {
	connected := true
	return protocol.NodeReport{Version: 1, ID: id, Role: role, DisplayName: "  " + id + "  ", BootID: "0123456789abcdef0123456789abcdef", BootStartedAtMS: inventoryTime.Add(-time.Minute).UnixMilli(), Sequence: 1, ObservedAt: observed.Format(time.RFC3339Nano), QueueConnected: &connected, Healthy: true, Status: "idle", Generation: 7, Machine: protocol.NodeMachine{OS: "linux", Arch: "amd64", ResourceScope: "unknown"}}
}

func coreReport(id string, observed time.Time, workers ...protocol.NodeReport) protocol.CoreNodeReport {
	return protocol.CoreNodeReport{NodeReport: nodeReport(id, "controller", observed), Workers: workers, ObservationEpoch: protocol.NodeObservationEpoch(id)}
}

func inventoryConfig() Configuration {
	return Configuration{Controllers: []ConfiguredNode{{ID: "controller-b", DisplayName: "Core B", ReportEnabled: true}, {ID: "controller-a", DisplayName: "Core A", ReportEnabled: true}}, Workers: []ConfiguredNode{{ID: "worker-2", DisplayName: "Worker 2", ReportEnabled: true}, {ID: "worker-1", DisplayName: "Worker 1", ReportEnabled: true}}}
}

// Catches accidentally treating absent/invalid stored data as live evidence or
// letting a report add a node outside the server-owned inventory.
func TestProjectMissingInvalidAndConfiguredInventory(t *testing.T) {
	bad := coreReport("controller-a", inventoryTime)
	bad.Sequence = 0
	got := Project(inventoryConfig(), map[string]queue.StoredNodeReport{
		"controller-a": {Report: bad, ReceivedAtMS: inventoryTime.UnixMilli()},
		"unknown":      {Report: coreReport("unknown", inventoryTime), ReceivedAtMS: inventoryTime.UnixMilli()},
	}, inventoryTime)
	if len(got.Controllers) != 2 || len(got.Workers) != 2 || got.Controllers[0].ID != "controller-a" || got.Workers[0].ID != "worker-1" {
		t.Fatalf("configured/sorted inventory: %+v", got)
	}
	for _, n := range append(got.Controllers, got.Workers...) {
		if n.Report != nil || n.Healthy || n.Status != "unreported" || n.Registration != Unregistered || n.Source != NoReport || n.ReceivedAtMS != 0 {
			t.Fatalf("missing node acquired report evidence: %+v", n)
		}
	}
	if got.Controllers[0].Reason != "core_missing" || got.Workers[0].Reason != "worker_missing" || got.Controllers[0].DisplayName != "Core A" {
		t.Fatalf("missing identity: %+v", got)
	}
}

// Catches queue-disconnected overriding timeout, freshness off-by-one, and
// manager clock skew accidentally freshening future receive timestamps.
func TestProjectCoreFreshnessAndQueueDisconnection(t *testing.T) {
	for _, tc := range []struct {
		name         string
		age          time.Duration
		connected    bool
		status       string
		registration RegistrationStatus
	}{
		{"fresh", 20 * time.Second, true, "idle", Registered},
		{"stale", 20*time.Second + time.Millisecond, true, "report_timeout", Stale},
		{"queue down", 0, false, "queue_disconnected", Registered},
		{"stale queue down", 21 * time.Second, false, "report_timeout", Stale},
		{"future tolerance", -5 * time.Second, true, "idle", Registered},
		{"future receipt", -5*time.Second - time.Millisecond, true, "report_timeout", Stale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := coreReport("controller-a", inventoryTime)
			report.QueueConnected = &tc.connected
			got := Project(inventoryConfig(), map[string]queue.StoredNodeReport{"controller-a": {Report: report, ReceivedAtMS: inventoryTime.Add(-tc.age).UnixMilli()}}, inventoryTime).Controllers[0]
			if got.Status != tc.status || got.Registration != tc.registration || got.DisplayName != "controller-a" || got.Report == nil || got.Report.Status != "idle" || got.ObservationEpoch != protocol.NodeObservationEpoch("controller-a") {
				t.Fatalf("projection: %+v", got)
			}
			if got.Healthy != (tc.status == "idle") {
				t.Fatalf("health: %+v", got)
			}
		})
	}
}

// Catches replacing relative Core/worker age with manager-vs-worker wall time,
// or ignoring time spent between receipt and inventory rendering.
func TestProjectWorkerUsesRelativeSampleAndReceiptAge(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		skew, sampleAge, receiptAge time.Duration
		stale                       bool
	}{
		{"core clock ahead", 90 * time.Second, 0, 0, false},
		{"core clock behind", -90 * time.Second, 0, 0, false},
		{"old sample", 0, 21 * time.Second, 0, true},
		{"receipt compounds age", 0, 12 * time.Second, 9 * time.Second, true},
		{"boundary", 0, 12 * time.Second, 8 * time.Second, false},
		{"worker ahead", 0, -10 * time.Second, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coreAt := inventoryTime.Add(tc.skew)
			report := coreReport("controller-a", coreAt, nodeReport("worker-1", "worker", coreAt.Add(-tc.sampleAge)))
			got := Project(inventoryConfig(), map[string]queue.StoredNodeReport{"controller-a": {Report: report, ReceivedAtMS: inventoryTime.Add(-tc.receiptAge).UnixMilli()}}, inventoryTime).Workers[0]
			if (got.Registration == Stale) != tc.stale {
				t.Fatalf("relative freshness: %+v", got)
			}
			if tc.stale && (got.Reason != "worker_observation_stale" || got.Status != "report_timeout" || got.Healthy) {
				t.Fatalf("stale evidence: %+v", got)
			}
		})
	}
}

// Catches health-based selection hiding newer unreachable evidence, and stale
// evidence replacing a fresh observation from another Core. Own-Core evidence
// must remain separate from the global worker selection.
func TestProjectSelectsFreshThenNewestAndPreservesCoreEvidence(t *testing.T) {
	a := coreReport("controller-a", inventoryTime, nodeReport("worker-1", "worker", inventoryTime.Add(-35*time.Second)))
	b := coreReport("controller-b", inventoryTime, nodeReport("worker-1", "worker", inventoryTime))
	reports := map[string]queue.StoredNodeReport{"controller-a": {Report: a, ReceivedAtMS: inventoryTime.UnixMilli()}, "controller-b": {Report: b, ReceivedAtMS: inventoryTime.Add(-time.Second).UnixMilli()}}
	got := Project(inventoryConfig(), reports, inventoryTime)
	if got.Workers[0].ReportedBy != "controller-b" || got.Controllers[0].WorkerStatuses["worker-1"].Registration != Stale {
		t.Fatalf("fresh priority or Core evidence: %+v", got)
	}
	a.Workers[0].ObservedAt = inventoryTime.Format(time.RFC3339Nano)
	a.Workers[0].Status, a.Workers[0].Healthy = "unreachable", false
	reports["controller-a"] = queue.StoredNodeReport{Report: a, ReceivedAtMS: inventoryTime.UnixMilli()}
	got = Project(inventoryConfig(), reports, inventoryTime)
	worker := got.Workers[0]
	if worker.ReportedBy != "controller-a" || worker.Healthy || worker.Status != "unreachable" || worker.Registration != Registered || worker.ObservationEpoch != a.ObservationEpoch {
		t.Fatalf("newest unreachable evidence lost: %+v", worker)
	}
	if !got.Controllers[1].WorkerStatuses["worker-1"].Healthy {
		t.Fatalf("global selection overwrote Core-owned evidence: %+v", got.Controllers[1])
	}
}

func TestProjectDoesNotActivateDisabledOrUnconfiguredWorkers(t *testing.T) {
	config := inventoryConfig()
	config.Workers[1].ReportEnabled = false
	report := coreReport("controller-a", inventoryTime, nodeReport("worker-1", "worker", inventoryTime), nodeReport("worker-other", "worker", inventoryTime))
	got := Project(config, map[string]queue.StoredNodeReport{"controller-a": {Report: report, ReceivedAtMS: inventoryTime.UnixMilli()}}, inventoryTime)
	if got.Workers[0].Registration != Unregistered || len(got.Controllers[0].WorkerStatuses) != 0 || len(got.Workers) != 2 {
		t.Fatalf("report expanded configured membership: %+v", got)
	}
}

func privateRedis(t *testing.T) (*queue.Client, *redis.Client) {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("isolated Redis binary unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	command := exec.Command(binary, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--protected-mode", "yes", "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	addr := "127.0.0.1:" + strconv.Itoa(port)
	db := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { db.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for db.Ping(context.Background()).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("private Redis did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	q, err := queue.New(queue.Config{Addr: addr, Prefix: "node-registry"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return q, db
}

// Uses real atomic report storage: duplicate registration must not refresh the
// receipt/TTL and the current observation epoch must accompany the response.
func TestRegistryRegistrationPreservesOrderingEpochRetentionAndCancellation(t *testing.T) {
	q, db := privateRedis(t)
	registry := Registry{Store: q}
	report := coreReport("controller-a", inventoryTime, nodeReport("worker-1", "worker", inventoryTime))
	result, err := registry.Register(context.Background(), report, inventoryTime.UnixMilli())
	if err != nil || !result.Accepted || result.ObservationEpoch != protocol.NodeObservationEpoch("") {
		t.Fatalf("initial registration: %+v %v", result, err)
	}
	if err = db.Set(context.Background(), "node-registry:lease", "private-lease", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	replay, err := registry.Register(context.Background(), report, inventoryTime.Add(time.Second).UnixMilli())
	if err != nil || replay.Accepted || replay.ObservationEpoch != protocol.NodeObservationEpoch("private-lease") {
		t.Fatalf("replayed registration: %+v %v", replay, err)
	}
	inventory, err := registry.Inventory(context.Background(), inventoryConfig(), func() time.Time { return inventoryTime })
	if err != nil || inventory.Controllers[0].ReceivedAtMS != inventoryTime.UnixMilli() {
		t.Fatalf("replay refreshed receipt: %+v %v", inventory, err)
	}
	ttl, err := db.PTTL(context.Background(), "node-registry:nodes:reports:controller-a").Result()
	if err != nil || ttl < 23*time.Hour || ttl > 24*time.Hour {
		t.Fatalf("report retention %v %v", ttl, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = registry.Inventory(ctx, inventoryConfig(), func() time.Time { return inventoryTime }); !errors.Is(err, context.Canceled) {
		t.Fatalf("inventory cancellation: %v", err)
	}
	report.Sequence++
	if _, err = registry.Register(ctx, report, inventoryTime.UnixMilli()); !errors.Is(err, context.Canceled) {
		t.Fatalf("registration cancellation: %v", err)
	}
}
