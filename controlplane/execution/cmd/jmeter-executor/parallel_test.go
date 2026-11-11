package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

func executorTestQueue(t *testing.T) *queue.Client {
	t.Helper()
	if os.Getenv("JMETER_EXECUTION_IT") != "1" {
		t.Skip("enable isolated Redis integration")
	}
	password, err := os.ReadFile(env("JMETER_REDIS_PASSWORD_FILE", "../../../../runtime/local-secrets/redis-password"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := queue.Config{Addr: "127.0.0.1:26379", Password: strings.TrimSpace(string(password)), Prefix: "jmeter:parallel-it:" + uuid()}
	q, err := queue.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.Ping(context.Background()); err != nil {
		q.Close()
		t.Fatalf("local isolated Redis unavailable: %v", err)
	}
	t.Cleanup(func() {
		defer q.Close()
		admin := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password})
		defer admin.Close()
		var cursor uint64
		for {
			keys, next, err := admin.Scan(context.Background(), cursor, cfg.Prefix+":*", 100).Result()
			if err != nil {
				t.Error("clean local Redis fixture")
				return
			}
			if len(keys) > 0 {
				if err := admin.Del(context.Background(), keys...).Err(); err != nil {
					t.Error("remove local Redis fixture")
				}
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return q
}

// Serial dispatch blocks the second claim behind the first; dispatching the
// same pending message twice would perform a second claim while it is live.
func TestCoreLoopDispatchesIndependentTasksAndSuppressesDuplicates(t *testing.T) {
	q := executorTestQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const token = "parallel-test-signing-key"
	entered := make(chan string, 10)
	release := make(chan struct{})
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/runner/claim" {
			respond(w, 200, map[string]bool{"accepted": true})
			return
		}
		var claim struct {
			TaskID string `json:"task_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&claim)
		entered <- claim.TaskID
		select {
		case <-release:
		case <-req.Context().Done():
		}
		respond(w, 200, map[string]any{"execute": false, "status": "COMPLETED", "result_required": false})
	}))
	defer manager.Close()
	defer close(release)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	s := &service{q: q, http: manager.Client(), c: config{id: "core", role: "controller", cluster: "parallel", token: token, state: base}}
	r := &runner{s: s}
	if err := q.EnsureGroup(ctx, "parallel"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run-a", "run-b", "run-a"} {
		task := protocol.Task{TaskID: id, AttemptID: "attempt", ControllerID: "core", ClusterID: "parallel", CallbackURL: manager.URL, Workers: []protocol.Worker{{ID: "worker-" + id}}}
		signed, _ := protocol.SignTask(task, token)
		if _, err := q.Publish(ctx, signed); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() { r.loop(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Core loop failed to drain dispatched tasks on shutdown")
		}
	}()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-entered:
			if seen[id] {
				t.Fatal("duplicate message reclaimed a live task")
			}
			seen[id] = true
		case <-time.After(3 * time.Second):
			t.Fatal("independent task could not claim while the first claim is blocked")
		}
	}
	if r.status()["status"] != "running" {
		t.Fatal("Core status omitted active dispatched tasks")
	}
	select {
	case <-entered:
		t.Fatal("duplicate message claimed a task twice")
	case <-time.After(100 * time.Millisecond):
	}
}

// A cancellation must terminate only its own process group. Both live Core
// JVMs must also receive different official JMeter UDP stop endpoints.
func TestConcurrentProcessesUseIndependentStopPortsAndCancellation(t *testing.T) {
	t.Setenv("JMETER_TEST_FAKE_JAVA", "1")
	t.Setenv("JMETER_TEST_WAIT_JAVA", "1")
	base, _ := filepath.EvalSymlinks(t.TempDir())
	binary, _ := os.Executable()
	s := &service{c: config{work: base, java: binary}}
	contexts := make([]context.Context, 2)
	cancels := make([]context.CancelFunc, 2)
	done := []chan error{make(chan error, 1), make(chan error, 1)}
	for i, id := range []string{"first", "second"} {
		contexts[i], cancels[i] = context.WithCancel(context.Background())
		defer cancels[i]()
		if err := directory(filepath.Join(s.taskDir(id), "raw")); err != nil {
			t.Fatal(err)
		}
		go func(i int, id string) {
			taskService := &service{c: config{work: base, java: binary, runtimeSlot: i + 1}}
			_, err := taskService.runProcess(contexts[i], protocol.Task{TaskID: id, Workers: []protocol.Worker{{RMIHost: "worker-" + id}}}, filepath.Join(s.taskDir(id), "raw"))
			done[i] <- err
		}(i, id)
	}
	ports := []string{}
	for _, id := range []string{"first", "second"} {
		var args []string
		deadline := time.Now().Add(5 * time.Second)
		for {
			if load(filepath.Join(s.taskDir(id), "argv.json"), &args) == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("fake JVM did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "-Jjmeterengine.nongui.port=") {
				ports = append(ports, strings.TrimPrefix(arg, "-Jjmeterengine.nongui.port="))
			}
		}
	}
	cancels[0]()
	select {
	case err := <-done[0]:
		if err == nil {
			t.Fatal("cancelled process succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("first process was not killed")
	}
	select {
	case <-done[1]:
		t.Fatal("cancelling first task terminated its peer")
	case <-time.After(100 * time.Millisecond):
	}
	cancels[1]()
	select {
	case <-done[1]:
	case <-time.After(4 * time.Second):
		t.Fatal("second process was not killed")
	}
	if len(ports) != 2 || ports[0] == ports[1] {
		t.Fatalf("concurrent tasks share a JMeter stop port: %v", ports)
	}
}

func TestCoreStopsOnlyNamedTaskAndUsesItsShutdownPort(t *testing.T) {
	root := &runner{s: &service{}}
	children := make([]*runner, 2)
	contexts := make([]context.Context, 2)
	listeners := make([]net.PacketConn, 2)
	for i, id := range []string{"first", "second"} {
		child, release := root.reserve(queue.Message{SignedTask: protocol.SignedTask{Task: protocol.Task{TaskID: id}}})
		if child == nil {
			t.Fatal("could not reserve independent Core task")
		}
		defer release()
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		contexts[i], children[i] = ctx, child
		child.active = &activeJob{id: id, attempt: "attempt", state: "RUNNING", cancel: cancel}
		listener, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(child.stopPort())))
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		listeners[i] = listener
		defer func() {
			if child.active.stopTimer != nil {
				child.active.stopTimer.Stop()
			}
		}()
	}
	if root.stopMode("first", "foreign-attempt", true) {
		t.Fatal("foreign attempt stopped active task")
	}
	if !root.stopMode("first", "attempt", true) {
		t.Fatal("root did not route graceful stop to selected task")
	}
	_ = listeners[0].SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 32)
	n, _, err := listeners[0].ReadFrom(buf)
	if err != nil || string(buf[:n]) != "Shutdown" {
		t.Fatal("graceful stop did not reach its task's JMeter port", err)
	}
	_ = listeners[1].SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := listeners[1].ReadFrom(buf); err == nil {
		t.Fatal("graceful stop reached another task")
	}
	if !root.stopMode("first", "attempt", false) || context.Cause(contexts[0]) != errStop {
		t.Fatal("force stop was not routed to selected task")
	}
	if contexts[1].Err() != nil || children[1].status()["status"] != "running" {
		t.Fatal("stopping one task changed its peer")
	}
}

func TestWorkerRejectsValidLeaseForUnrelatedWorker(t *testing.T) {
	q := executorTestQueue(t)
	ctx := context.Background()
	owner := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "core", LeaseToken: queue.Token("core", "task", "attempt", "nonce")}
	if ok, err := q.Acquire(ctx, owner.LeaseToken, "worker-allowed"); err != nil || !ok {
		t.Fatal("could not acquire worker fixture lease")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	s := &service{q: q, c: config{id: "worker-unrelated", work: base, state: base}}
	w := &worker{s: s, rec: engineRecord{Generation: 1, Ready: true}, proc: &process{done: make(chan struct{})}}
	if _, err := w.prepare(ctx, owner); err == nil {
		t.Fatal("worker prepared under a valid lease that does not cover itself")
	}
	if w.rec.Owner != nil || w.rec.Prepared {
		t.Fatal("rejected lease changed worker ownership")
	}
	if _, err := os.Stat(s.taskDir("task")); !os.IsNotExist(err) {
		t.Fatal("rejected lease wrote task files")
	}
	// A compatible global lease remains accepted by old callers and workers.
	if err := q.Release(ctx, owner.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if ok, err := q.Acquire(ctx, owner.LeaseToken); err != nil || !ok {
		t.Fatal("could not acquire legacy global lease")
	}
	if _, err := w.prepare(ctx, owner); err != nil {
		t.Fatal("legacy global lease lost compatibility", err)
	}
}

func TestCoreParallelRunsReleaseAndStopOnlyTheirOwnWorkers(t *testing.T) {
	q := executorTestQueue(t)
	t.Setenv("JMETER_TEST_FAKE_JAVA", "1")
	t.Setenv("JMETER_TEST_WAIT_JAVA", "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	binary, _ := os.Executable()
	const token = "parallel-execution-signing-key"
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/runner/claim" {
			respond(w, 200, map[string]any{"execute": true, "status": "PREPARING"})
			return
		}
		respond(w, 200, map[string]bool{"accepted": true})
	}))
	defer manager.Close()
	s := &service{q: q, s: mockStore(t), http: manager.Client(), c: config{id: "core", role: "controller", cluster: "parallel-runs", state: filepath.Join(base, "state"), results: filepath.Join(base, "results"), work: filepath.Join(base, "work"), java: binary, token: token}}
	r := &runner{s: s}
	resets := [2]atomic.Int32{}
	for i, id := range []string{"first", "second"} {
		worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/prepare" {
				respond(w, 200, map[string]int{"generation": 1})
				return
			}
			var a assignment
			_ = json.NewDecoder(req.Body).Decode(&a)
			resets[i].Add(1)
			respond(w, 200, map[string]any{"state": "COMPLETED", "operation_id": a.OperationID, "generation": 2})
		}))
		defer worker.Close()
		task := protocol.Task{Version: 1, TaskID: id, AttemptID: "attempt-" + id, ProjectID: "project", ClusterID: "parallel-runs", ControllerID: "core", Workers: []protocol.Worker{{ID: "worker-" + id, AgentURL: worker.URL, RMIHost: "worker-" + id}}, TimeoutSeconds: 60, CallbackURL: manager.URL, Properties: map[string]string{"s": "secret value"}, Plan: seed(t, s.s, id+".jmx", `<jmeterTestPlan><hashTree><TestPlan/><hashTree/></hashTree></jmeterTestPlan>`)}
		signed, err := protocol.SignTask(task, token)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.Publish(ctx, signed); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() { r.loop(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Core did not drain live processes on shutdown")
		}
	}()
	await := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatal("parallel Core execution did not reach expected state")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, id := range []string{"first", "second"} {
		await(func() bool { _, err := os.Stat(filepath.Join(s.taskDir(id), "argv.json")); return err == nil })
	}
	if !r.stopMode("first", "attempt-first", false) {
		t.Fatal("could not stop first parallel task")
	}
	await(func() bool {
		var j journal
		return load(r.file("first"), &j) == nil && j.Stage == "UPLOADING"
	})
	if resets[0].Load() != 1 || resets[1].Load() != 0 {
		t.Fatal("first task cleanup reset a peer worker")
	}
	var first, second journal
	if load(r.file("first"), &first) != nil || first.Outcome != "STOPPED" || load(r.file("second"), &second) != nil || second.Stage != "STARTED" {
		t.Fatal("stopping first task changed the peer execution journal")
	}
	if first.RuntimeSlot == second.RuntimeSlot || first.RuntimeSlot == 0 || second.RuntimeSlot == 0 {
		t.Fatal("parallel Core tasks did not receive separate runtime identities")
	}
	if ok, err := q.OwnsWorkers(ctx, second.Lease, "worker-second"); err != nil || !ok {
		t.Fatal("first task cleanup released its peer lease")
	}
	await(func() bool { ok, _ := q.Acquire(ctx, "next-first", "worker-first"); return ok })
	if ok, err := q.Acquire(ctx, "overlap-second", "worker-second"); err != nil || ok {
		t.Fatal("running peer worker became available")
	}
	if !r.stopMode("second", "attempt-second", false) {
		t.Fatal("peer process stopped when first task finished")
	}
	await(func() bool {
		var j journal
		return load(r.file("second"), &j) == nil && j.Stage == "UPLOADING" && j.Outcome == "STOPPED"
	})
}
