package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

func nodeReportFixture(id, role string) protocol.NodeReport {
	connected := true
	return protocol.NodeReport{Version: 1, ID: id, Role: role, DisplayName: id, BootID: "0123456789abcdef0123456789abcdef", BootStartedAtMS: time.Now().Add(-time.Minute).UnixMilli(), Sequence: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), QueueConnected: &connected, Healthy: true, Status: "idle", Generation: 1, Machine: protocol.NodeMachine{OS: "linux", Arch: "amd64", ResourceScope: "unknown"}}
}
func nodeRedisFixture(t *testing.T, p *Platform) {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("local isolated Redis binary unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	command := exec.Command(binary, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--protected-mode", "yes", "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	p.Config.RedisAddr = "127.0.0.1:" + strconv.Itoa(port)
	p.Config.RedisPassword = ""
	p.Config.RedisPrefix = "pc-node-test:" + newID("")
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err = q.Ping(ctx)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated Redis did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
func TestNodeInventoryUsesCoreReportsAndNeverProbes(t *testing.T) {
	var probes atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probes.Add(1); w.WriteHeader(500) }))
	defer trap.Close()
	p := &Platform{Config: Config{ControllerURLs: map[string]string{"controller-a": trap.URL, "controller-b": trap.URL}, WorkerURLs: map[string]string{"worker-1": trap.URL, "worker-2": trap.URL}}}
	nodeRedisFixture(t, p)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	report := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{nodeReportFixture("worker-1", "worker")}, ObservationEpoch: protocol.NodeObservationEpoch("recovery")}
	report.DisplayName = "PressureCooker-Core-1"
	if ok, err := q.StoreNodeReport(context.Background(), report, time.Now().UnixMilli()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	controllers, workers, err := p.nodeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(controllers) != 2 || len(workers) != 2 || probes.Load() != 0 {
		t.Fatal(controllers, workers, probes.Load())
	}
	core := obj(controllers[0])
	worker := obj(workers[0])
	missing := obj(workers[1])
	if !boolean(core["healthy"]) || core["display_name"] != "PressureCooker-Core-1" || core["status_source"] != "core_report" {
		t.Fatal(core)
	}
	if !boolean(worker["healthy"]) || worker["reported_by"] != "controller-a" || worker["observation_epoch"] != report.ObservationEpoch {
		t.Fatal(worker)
	}
	if boolean(missing["healthy"]) || missing["status"] != "unreported" || missing["last_reported_at"] != nil {
		t.Fatal(missing)
	}
	report.Sequence++
	if ok, err := q.StoreNodeReport(context.Background(), report, time.Now().Add(-30*time.Second).UnixMilli()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	controllers, workers, err = p.nodeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []Obj{obj(controllers[0]), obj(workers[0])} {
		if boolean(row["healthy"]) || row["registration_status"] != "stale" || row["status"] != "report_timeout" {
			t.Fatal(row)
		}
	}
	if probes.Load() != 0 {
		t.Fatal("manager probed a node")
	}
}
func TestNodeInventoryUsesFreshOtherCoreEvidenceAndQueueFailure(t *testing.T) {
	p := &Platform{Config: Config{ControllerURLs: map[string]string{"controller-a": "http://unused-a", "controller-b": "http://unused-b"}, WorkerURLs: map[string]string{"worker-1": "http://unused-worker"}}}
	nodeRedisFixture(t, p)
	q, _ := p.runnerQueue()
	defer q.Close()
	first := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{nodeReportFixture("worker-1", "worker")}, ObservationEpoch: protocol.NodeObservationEpoch("old")}
	q.StoreNodeReport(context.Background(), first, time.Now().Add(-30*time.Second).UnixMilli())
	second := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-b", "controller"), Workers: []protocol.NodeReport{nodeReportFixture("worker-1", "worker")}, ObservationEpoch: protocol.NodeObservationEpoch("new")}
	second.QueueConnected = new(bool)
	if ok, err := q.StoreNodeReport(context.Background(), second, time.Now().UnixMilli()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cores, workers, err := p.nodeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if boolean(obj(cores[1])["healthy"]) || obj(cores[1])["status"] != "queue_disconnected" {
		t.Fatal(cores)
	}
	if obj(workers[0])["reported_by"] != "controller-b" || obj(workers[0])["observation_epoch"] != second.ObservationEpoch {
		t.Fatal(workers)
	}
}
func TestNodeReportAuthenticationRejectsSpoofedIdentityBeforeStorage(t *testing.T) {
	p := &Platform{Config: Config{AgentToken: "node-agent-secret", ControllerURLs: map[string]string{"controller-a": "http://unused"}, WorkerURLs: map[string]string{"worker-1": "http://unused"}}}
	for _, test := range []struct {
		name, signer, id, role, path string
		sign                         bool
		want                         int
	}{
		{"unsigned", "controller-a", "controller-a", "controller", "/api/nodes/report", false, 401},
		{"mismatch", "controller-a", "controller-b", "controller", "/api/nodes/report", true, 403},
		{"unknown", "other", "other", "controller", "/api/nodes/report", true, 403},
		{"worker direct", "worker-1", "worker-1", "worker", "/api/nodes/report", true, 403},
		{"query", "controller-a", "controller-a", "controller", "/api/nodes/report?x=1", true, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := protocol.CoreNodeReport{NodeReport: nodeReportFixture(test.id, test.role)}
			data := encoded(report)
			request := httptest.NewRequest("POST", test.path, bytes.NewReader(data))
			if test.sign {
				protocol.SignRequest(request, data, test.signer, p.Config.AgentToken, time.Now())
			}
			response := httptest.NewRecorder()
			Handler{p}.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}

func TestMySQLCoreRegistrationAcknowledgesConfiguredInventoryAndKeepsOrdering(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	p.Config.DefaultClusterWorkers, _ = parseDefaultClusterWorkers(`{"controller-a":["worker-1","worker-2"],"controller-b":["worker-3","worker-4","worker-5","worker-6"]}`, true, p.Config.ControllerURLs, p.Config.WorkerURLs)
	report := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{nodeReportFixture("worker-1", "worker")}, ObservationEpoch: protocol.NodeObservationEpoch("")}
	call := func(raw []byte) (int, Obj) {
		request := httptest.NewRequest("POST", "/api/nodes/report", bytes.NewReader(raw))
		protocol.SignRequest(request, raw, report.ID, p.Config.AgentToken, time.Now())
		response := httptest.NewRecorder()
		Handler{p}.ServeHTTP(response, request)
		result, err := decode(response.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return response.Code, result
	}
	status, response := call(encoded(report))
	if status != 200 || !boolean(response["accepted"]) || len(arr(response["workers"])) != 6 || len(arr(response["cluster_worker_ids"])) != 2 || response["observation_epoch"] != protocol.NodeObservationEpoch("") {
		t.Fatal(status, response)
	}
	q, _ := p.runnerQueue()
	defer q.Close()
	stored, _ := q.NodeReports(context.Background(), []string{report.ID})
	stamp := stored[report.ID].ReceivedAtMS
	status, response = call(encoded(report))
	if status != 200 || boolean(response["accepted"]) {
		t.Fatal(status, response)
	}
	stored, _ = q.NodeReports(context.Background(), []string{report.ID})
	if stored[report.ID].ReceivedAtMS != stamp {
		t.Fatal("replay refreshed heartbeat")
	}
	before := report.BootStartedAtMS
	report.BootID = "1123456789abcdef0123456789abcdef"
	report.BootStartedAtMS = before + 1
	report.Sequence = 1
	status, response = call(encoded(report))
	if status != 200 || !boolean(response["accepted"]) {
		t.Fatal(status, response)
	}
	raw, _ := decode(encoded(report))
	raw["agent_url"] = "http://attacker.invalid"
	status, _ = call(encoded(raw))
	if status != 400 {
		t.Fatal(status)
	}
	report.Workers[0].ID = "unknown-worker"
	status, _ = call(encoded(report))
	if status != 403 {
		t.Fatal(status)
	}
}

func TestNodeInventoryDoesNotFreshenOldWorkerObservationWithCoreHeartbeat(t *testing.T) {
	p := &Platform{Config: Config{ControllerURLs: map[string]string{"controller-a": "http://unused"}, WorkerURLs: map[string]string{"worker-1": "http://unused"}}}
	nodeRedisFixture(t, p)
	q, _ := p.runnerQueue()
	defer q.Close()
	worker := nodeReportFixture("worker-1", "worker")
	worker.ObservedAt = time.Now().Add(-35 * time.Second).UTC().Format(time.RFC3339Nano)
	report := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{worker}, ObservationEpoch: protocol.NodeObservationEpoch("")}
	if ok, err := q.StoreNodeReport(context.Background(), report, time.Now().UnixMilli()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	cores, workers, err := p.nodeInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !boolean(obj(cores[0])["healthy"]) {
		t.Fatal(cores)
	}
	if boolean(obj(workers[0])["healthy"]) || obj(workers[0])["registration_status"] != "stale" {
		t.Fatal(workers)
	}
}

func TestNodeInventoryPrefersFreshObservationsAndKeepsLatestOfflineEvidence(t *testing.T) {
	p := &Platform{Config: Config{ControllerURLs: map[string]string{"controller-a": "http://unused-a", "controller-b": "http://unused-b"}, WorkerURLs: map[string]string{"worker-1": "http://unused"}}}
	nodeRedisFixture(t, p)
	q, _ := p.runnerQueue()
	defer q.Close()
	at := time.Now()
	oldWorker := nodeReportFixture("worker-1", "worker")
	oldWorker.ObservedAt = at.Add(-35 * time.Second).UTC().Format(time.RFC3339Nano)
	a := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{oldWorker}, ObservationEpoch: protocol.NodeObservationEpoch("a")}
	b := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-b", "controller"), Workers: []protocol.NodeReport{nodeReportFixture("worker-1", "worker")}, ObservationEpoch: protocol.NodeObservationEpoch("b")}
	for _, entry := range []struct {
		report   protocol.CoreNodeReport
		received int64
	}{{b, at.Add(-time.Second).UnixMilli()}, {a, at.UnixMilli()}} {
		if ok, err := q.StoreNodeReport(context.Background(), entry.report, entry.received); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	_, workers, err := p.nodeInventory(context.Background())
	if err != nil || obj(workers[0])["reported_by"] != "controller-b" || !boolean(obj(workers[0])["healthy"]) {
		t.Fatal(workers, err)
	}
	a.Sequence++
	a.Workers[0].ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	a.Workers[0].Status = "unreachable"
	a.Workers[0].Healthy = false
	if ok, err := q.StoreNodeReport(context.Background(), a, time.Now().UnixMilli()); err != nil || !ok {
		t.Fatal(ok, err)
	}
	_, workers, err = p.nodeInventory(context.Background())
	if err != nil || obj(workers[0])["reported_by"] != "controller-a" || boolean(obj(workers[0])["healthy"]) || obj(workers[0])["status"] != "unreachable" {
		t.Fatal(workers, err)
	}
}

func TestMySQLCoreClusterKeepsOwnWorkerViewAndHidesPrivateProjection(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	p.Config.DefaultClusterWorkers, _ = parseDefaultClusterWorkers(`{"controller-a":["worker-1","worker-2"],"controller-b":["worker-3","worker-4","worker-5","worker-6"]}`, true, p.Config.ControllerURLs, p.Config.WorkerURLs)
	q, _ := p.runnerQueue()
	defer q.Close()
	a := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-a", "controller"), Workers: []protocol.NodeReport{nodeReportFixture("worker-1", "worker"), nodeReportFixture("worker-2", "worker")}, ObservationEpoch: protocol.NodeObservationEpoch("")}
	a.Workers[0].Status = "unreachable"
	a.Workers[0].Healthy = false
	b := protocol.CoreNodeReport{NodeReport: nodeReportFixture("controller-b", "controller"), ObservationEpoch: protocol.NodeObservationEpoch("")}
	for _, id := range sortedKeys(p.Config.WorkerURLs) {
		b.Workers = append(b.Workers, nodeReportFixture(id, "worker"))
	}
	for index, r := range []protocol.CoreNodeReport{a, b} {
		if ok, err := q.StoreNodeReport(context.Background(), r, time.Now().UnixMilli()+int64(index)); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	state, err := p.cluster()
	if err != nil {
		t.Fatal(err)
	}
	group := obj(arr(state["clusters"])[0])
	rows := arr(group["workers"])
	if len(rows) != 2 || obj(rows[0])["reported_by"] != "controller-a" || boolean(obj(rows[0])["healthy"]) || obj(rows[0])["status"] != "unreachable" {
		t.Fatal(group)
	}
	if !boolean(obj(arr(state["workers"])[0])["healthy"]) {
		t.Fatal(state["workers"])
	}
	if bytes.Contains(encoded(state), []byte("_worker_statuses")) {
		t.Fatal("private projection serialized")
	}
}
