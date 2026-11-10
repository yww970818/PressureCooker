package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"pressurecooker/execution/objectstore"
	"pressurecooker/execution/protocol"
	"pressurecooker/execution/queue"
)

func TestMain(m *testing.M) {
	if os.Getenv("JMETER_TEST_FAKE_JAVA") == "1" && len(os.Args) > 1 && os.Args[1] == "-Djava.awt.headless=true" {
		os.Exit(fakeJava())
	}
	os.Exit(m.Run())
}
func fakeJava() int {
	flag := func(name string) string {
		for i, arg := range os.Args {
			if arg == name && i+1 < len(os.Args) {
				return os.Args[i+1]
			}
		}
		return ""
	}
	if err := os.WriteFile("argv.json", body(os.Args[1:]), 0600); err != nil {
		return 2
	}
	if os.Getenv("JMETER_TEST_WAIT_JAVA") == "1" {
		time.Sleep(time.Minute)
		return 3
	}
	p, e := os.ReadFile("task.properties")
	if e != nil || !bytes.Contains(p, []byte("secret\\ value")) {
		return 4
	}
	if e = os.WriteFile(flag("-l"), []byte("timeStamp,label,success\n123,test,true\n"), 0600); e != nil {
		return 5
	}
	if e = os.WriteFile(flag("-j"), []byte("engine completed\n"), 0600); e != nil {
		return 6
	}
	fmt.Println("ran fixture")
	return 0
}
func TestPatchOnlyOwnedCSVAndProperties(t *testing.T) {
	const before = `<?xml version="1.0"?><jmeterTestPlan><other>literal &amp; CDATA</other><CSVDataSet><stringProp name="filename">orig&amp;.csv</stringProp><stringProp name="jmeter.platform.node_id">nodeA</stringProp></CSVDataSet><CSVDataSet><stringProp name="filename">legacy.csv</stringProp></CSVDataSet></jmeterTestPlan>`
	got, e := patchPlan([]byte(before), map[string]string{"nodeA": "/work/tasks/id/datasets/nodeA/data.csv"})
	want := strings.Replace(before, "orig&amp;.csv", "/work/tasks/id/datasets/nodeA/data.csv", 1)
	if e != nil || string(got) != want {
		t.Fatal("unrelated plan bytes changed")
	}
	for _, paths := range []map[string]string{{}, {"nodeB": "wrong"}} {
		if _, e = patchPlan([]byte(before), paths); e == nil {
			t.Fatal("unknown marker accepted")
		}
	}
	got = props(map[string]string{"k:=!": " 世界😀\n\\t"})
	if string(got) != "k\\:\\=\\!=\\ \\u4E16\\u754C\\uD83D\\uDE00\\n\\\\t\n" {
		t.Fatalf("incorrect Java escaping %q", got)
	}
}
func TestProcessCancellationKillsOwnedGroup(t *testing.T) {
	t.Setenv("JMETER_TEST_FAKE_JAVA", "1")
	t.Setenv("JMETER_TEST_WAIT_JAVA", "1")
	base, _ := filepath.EvalSymlinks(t.TempDir())
	binary, _ := os.Executable()
	s := service{c: config{work: base, java: binary}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := s.taskDir("run")
	raw := filepath.Join(dir, "raw")
	if e := directory(raw); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := s.runProcess(ctx, protocol.Task{TaskID: "run", Properties: map[string]string{"queue_visitors": "2"}, Workers: []protocol.Worker{{RMIHost: "one"}}}, raw)
		done <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(dir, "argv.json")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("JVM fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancelled process succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("child was not killed")
	}
}
func TestFixtureRequiresExtractedTokenAndResolvedCSV(t *testing.T) {
	for _, c := range []struct {
		path, token string
		code        int
	}{{"/login", "", 200}, {"/jmeter-smoke", "", 200}, {"/check?id=001", "wrong", 401}, {"/check?id=%24%7Bid%7D", "Bearer fixture-token", 422}, {"/check?id=001", "Bearer fixture-token", 200}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", c.path, nil)
		r.Header.Set("Authorization", c.token)
		fixture(w, r)
		if w.Code != c.code {
			t.Fatal(c.path, w.Code)
		}
	}
}
func TestWorkerRejectsStaleResetAndCleanup(t *testing.T) {
	current := assignment{TaskID: "new", AttemptID: "att", RunnerID: "runner", LeaseToken: "new", OperationID: "op-new", Generation: 8}
	old := assignment{TaskID: "old", AttemptID: "att", RunnerID: "runner", LeaseToken: "old", OperationID: "op-old", Generation: 7}
	w := worker{s: &service{}, rec: engineRecord{Generation: 8, Owner: &current}}
	res, e := w.reset(context.Background(), old)
	if e != nil || res["operation_id"] != "superseded" || w.rec.Owner.TaskID != "new" {
		t.Fatal("stale reset altered active worker")
	}
	if _, e = w.reset(context.Background(), assignment{OperationID: "unowned"}); e == nil {
		t.Fatal("legacy reset overrode assignment")
	}
	if e = w.cleanup(current); e == nil {
		t.Fatal("deleted active files")
	}
}

type memoryObject struct {
	b         []byte
	sha, mime string
}

func mockStore(t *testing.T) *objectstore.Client {
	t.Helper()
	data := map[string]memoryObject{}
	var mu sync.Mutex
	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		k := r.URL.Path
		o, ok := data[k]
		switch r.Method {
		case "PUT":
			if ok {
				w.WriteHeader(412)
				return
			}
			b, _ := io.ReadAll(r.Body)
			h := sha256.Sum256(b)
			sha := hex.EncodeToString(h[:])
			if sha != r.Header.Get("X-Amz-Content-Sha256") {
				w.WriteHeader(400)
				return
			}
			data[k] = memoryObject{b, sha, r.Header.Get("Content-Type")}
			w.WriteHeader(200)
		case "HEAD", "GET":
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(o.b)))
			w.Header().Set("Content-Type", o.mime)
			w.Header().Set("X-Amz-Meta-Sha256", o.sha)
			if r.Method == "GET" {
				w.Write(o.b)
			}
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(sv.Close)
	s, e := objectstore.New(objectstore.Config{Endpoint: sv.URL, Bucket: "test", AccessKey: "key", SecretKey: "secret"})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func seed(t *testing.T, s *objectstore.Client, key, text string) protocol.ObjectRef {
	t.Helper()
	h := sha256.Sum256([]byte(text))
	r, e := s.Put(context.Background(), key, strings.NewReader(text), int64(len(text)), hex.EncodeToString(h[:]), "text/plain")
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestCopyMergeHeaderAndQuotedUTF8(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	s := service{c: config{work: base}, s: mockStore(t)}
	d := protocol.DatasetInput{NodeID: "node", Columns: []string{"id", "name"}, Delimiter: ",", Distribution: "copy", Files: []protocol.ObjectRef{seed(t, s.s, "one.csv", "\uFEFF\"id\",name\r\n1,\"姓,名\"\r\n"), seed(t, s.s, "two.csv", "id,name\n2,\"line\r\nquoted \"\"text\"\"\"")}}
	paths, e := s.merge(context.Background(), protocol.Task{TaskID: "run"}, d)
	if e != nil || len(paths) != 1 {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(paths[0])
	if !bytes.Contains(b, []byte("line\r\nquoted")) {
		t.Fatal("quoted CRLF changed")
	}
	rows, e := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if e != nil || !reflect.DeepEqual(rows, [][]string{{"id", "name"}, {"1", "姓,名"}, {"2", "line\nquoted \"text\""}}) {
		t.Fatal("merge changed fields or duplicated header")
	}
	d.Files = []protocol.ObjectRef{seed(t, s.s, "bad.csv", "wrong,header\n1,foo\n")}
	if _, e = s.merge(context.Background(), protocol.Task{TaskID: "another"}, d); e == nil {
		t.Fatal("wrong header accepted")
	}
}
func TestRunnerJournalReclaimWorkerShardsAndRetry(t *testing.T) {
	if os.Getenv("JMETER_EXECUTION_IT") != "1" {
		t.Skip("enable isolated Redis integration")
	}
	t.Setenv("JMETER_TEST_FAKE_JAVA", "1")
	ctx := context.Background()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	bin, _ := os.Executable()
	token := "test-signing-credential"
	const cluster = "cluster-test"
	password, e := os.ReadFile("../../../../runtime/local-secrets/redis-password")
	if e != nil {
		t.Fatal(e)
	}
	cfg := queue.Config{Addr: "127.0.0.1:26379", Password: strings.TrimSpace(string(password)), Prefix: "jmeter:it:" + uuid()}
	q, e := queue.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	admin := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password})
	defer admin.Close()
	stream := cfg.Prefix + ":cluster:" + cluster + ":tasks"
	defer admin.Del(ctx, stream, cfg.Prefix+":lease")
	if e = q.EnsureGroup(ctx, cluster); e != nil {
		t.Fatal(e)
	}
	s := service{s: mockStore(t), q: q, http: &http.Client{Timeout: 5 * time.Second}, c: config{id: "runner", role: "controller", cluster: cluster, state: filepath.Join(base, "state"), results: filepath.Join(base, "results"), work: filepath.Join(base, "controller"), java: bin, token: token}}
	r := &runner{s: &s}
	var claims atomic.Int32
	var callbacks atomic.Int32
	var accepted atomic.Bool
	var mu sync.Mutex
	var received [][]byte
	manager := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		id, e := protocol.VerifyRequest(req, b, token, time.Now())
		if e != nil || id != "runner" || req.Header.Get("Authorization") != "" {
			t.Error("unsigned or bearer request to callback")
			reject(w, 401)
			return
		}
		switch req.URL.Path {
		case "/api/runner/claim":
			claims.Add(1)
			if accepted.Load() {
				respond(w, 200, map[string]any{"execute": false, "status": "INTERRUPTED", "result_required": true})
			} else {
				respond(w, 200, map[string]any{"execute": true, "status": "PREPARING"})
			}
		case "/api/runner/heartbeat":
			respond(w, 200, map[string]bool{"accepted": true})
		case protocol.CallbackPath:
			mu.Lock()
			received = append(received, append([]byte{}, b...))
			mu.Unlock()
			if callbacks.Add(1) == 1 {
				reject(w, 503)
				return
			}
			accepted.Store(true)
			respond(w, 200, map[string]bool{"accepted": true})
		}
	}))
	defer manager.Close()
	var applied atomic.Int32
	var resets atomic.Int32
	var rejectResets atomic.Bool
	var resetFailures atomic.Int32
	var invalidResetReceipts atomic.Bool
	var invalidResetReplies atomic.Int32
	var cleanups atomic.Int32
	workers := []protocol.Worker{}
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("worker-%d", i+1)
		ws := &service{s: s.s, q: q, c: config{id: id, role: "worker", work: filepath.Join(base, id), state: filepath.Join(base, id, "state"), token: token}}
		ws.worker = &worker{s: ws, rec: engineRecord{Generation: 1, Ready: true}, proc: &process{done: make(chan struct{})}}
		sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/reset" {
				if rejectResets.Load() {
					resetFailures.Add(1)
					reject(w, 503)
					return
				}
				if invalidResetReceipts.Load() {
					ws.worker.mu.Lock()
					generation := ws.worker.rec.Generation + 1
					ws.worker.mu.Unlock()
					invalidResetReplies.Add(1)
					respond(w, 200, map[string]any{"state": "COMPLETED", "operation_id": "unrelated-operation", "generation": generation})
					return
				}
				b, _ := io.ReadAll(req.Body)
				var a assignment
				json.Unmarshal(b, &a)
				ws.worker.mu.Lock()
				if !sameOwner(ws.worker.rec.Owner, &a) {
					t.Error("reset lacks matching owner")
				}
				ws.worker.rec.Owner = nil
				ws.worker.rec.Generation++
				generation := ws.worker.rec.Generation
				ws.worker.mu.Unlock()
				resets.Add(1)
				respond(w, 200, map[string]any{"state": "COMPLETED", "operation_id": a.OperationID, "generation": generation})
				return
			}
			res := httptest.NewRecorder()
			ws.handler(res, req)
			if res.Code == 200 && req.URL.Path == "/prepare" {
				ws.worker.mu.Lock()
				taskID := ws.worker.rec.Owner.TaskID
				ws.worker.mu.Unlock()
				b, e := os.ReadFile(ws.csvPath(taskID, "node"))
				want := fmt.Sprintf("id\n%d\n%d\n", i+1, i+3)
				if e != nil || string(b) != want {
					t.Error("selected worker did not download its shard")
				}
				applied.Add(1)
			}
			if res.Code == 200 && req.URL.Path == "/cleanup" {
				cleanups.Add(1)
			}
			w.WriteHeader(res.Code)
			w.Write(res.Body.Bytes())
		}))
		defer sv.Close()
		workers = append(workers, protocol.Worker{ID: id, AgentURL: sv.URL, RMIHost: id})
	}
	task := protocol.Task{Version: 1, TaskID: "run", AttemptID: "attempt", ProjectID: "project", ClusterID: cluster, ControllerID: "runner", Workers: workers, Plan: seed(t, s.s, "plan.jmx", `<jmeterTestPlan><hashTree><TestPlan/><hashTree><CSVDataSet><stringProp name="filename">old</stringProp><stringProp name="jmeter.platform.node_id">node</stringProp></CSVDataSet><hashTree/></hashTree></hashTree></jmeterTestPlan>`), Properties: map[string]string{"s": "secret value"}, Datasets: []protocol.DatasetInput{{NodeID: "node", Columns: []string{"id"}, VariableNames: []string{"id"}, Delimiter: ",", Distribution: "shard", Files: []protocol.ObjectRef{seed(t, s.s, "data1.csv", "id\n1\n2\n"), seed(t, s.s, "data2.csv", "id\n3\n4\n")}}}, TimeoutSeconds: 30, CallbackURL: manager.URL}
	dispatch := func(task protocol.Task) queue.Message {
		signed, _ := protocol.SignTask(task, token)
		if _, e := q.Publish(ctx, signed); e != nil {
			t.Fatal(e)
		}
		m, e := q.Read(ctx, cluster, "test")
		if e != nil || len(m) != 1 {
			t.Fatal("read dispatch")
		}
		return m[0]
	}
	message := dispatch(task)
	r.handle(ctx, message)
	var j journal
	if e = load(r.file("run"), &j); e != nil || j.Outcome != "COMPLETED" || j.Stage != "UPLOADING" || applied.Load() != 2 || resets.Load() != 2 {
		t.Fatalf("did not execute and clean selected workers: stage=%s result=%s applied=%d reset=%d err=%v", j.Stage, j.Outcome, applied.Load(), resets.Load(), e)
	}
	args, _ := os.ReadFile(filepath.Join(s.taskDir("run"), "argv.json"))
	if bytes.Contains(args, []byte("secret")) || !bytes.Contains(args, []byte("-G")) || !bytes.Contains(args, []byte("worker-1,worker-2")) {
		t.Fatal("incorrect JVM arguments")
	}
	if ok, e := q.Acquire(ctx, "next-job"); e != nil || !ok {
		t.Fatal("pending delivery holds execution lease")
	}
	q.Release(ctx, "next-job")
	restarted := &runner{s: &s}
	restarted.deliver(ctx, &j)
	if j.Stage != "CALLBACK_PENDING" || callbacks.Load() != 1 || j.Next.IsZero() {
		t.Fatal("failed callback not retained")
	}
	if admin.XPending(ctx, stream, queue.Group).Val().Count != 1 {
		t.Fatal("message acknowledged before receipt")
	}
	restarted.handle(ctx, message)
	if claims.Load() != 1 {
		t.Fatal("callback-pending job claimed or executed twice")
	}
	var disk journal
	if load(r.file("run"), &disk) != nil {
		t.Fatal("missing retry journal")
	}
	restarted.deliver(ctx, &disk)
	if callbacks.Load() != 2 || cleanups.Load() != 2 || admin.XPending(ctx, stream, queue.Group).Val().Count != 0 {
		t.Fatal("delivery retry/cleanup/ACK failed")
	}
	mu.Lock()
	if !bytes.Equal(received[0], received[1]) || bytes.Contains(received[1], []byte("secret value")) {
		t.Error("callback changed or leaked properties")
	}
	var cb protocol.Callback
	json.Unmarshal(received[1], &cb)
	mu.Unlock()
	if len(cb.Artifacts) != 3 || len(cb.InputArtifacts) != 2 || len(cb.MissingFiles) != 0 {
		t.Fatal("invalid receipts")
	}
	for _, ref := range cb.Artifacts {
		if _, e = s.s.Head(ctx, ref); e != nil || !strings.HasSuffix(ref.URL, "/api/runs/run/artifacts/"+ref.Name+"?project_id=project") {
			t.Fatal("invalid raw file URL or SHA")
		}
	}
	// Simulate a crash after the STARTED fsync. The console already claimed this
	// attempt and no second JMeter process may start, even after auto-claim.
	task.TaskID = "uncertain"
	task.AttemptID = "attempt2"
	message = dispatch(task)
	prior := journal{Task: task, Stage: "STARTED", Lease: "old", Started: timestamp(), MessageID: message.ID}
	if e = r.persist(&prior); e != nil {
		t.Fatal(e)
	}
	restarted.handle(ctx, message)
	var after journal
	load(r.file(task.TaskID), &after)
	if after.Outcome != "INTERRUPTED" || after.Stage != "UPLOADING" || applied.Load() != 2 {
		t.Fatal("uncertain launch reran instead of interrupting")
	}
	if _, e = os.Stat(filepath.Join(s.taskDir(task.TaskID), "argv.json")); !os.IsNotExist(e) {
		t.Fatal("interrupted JVM actually launched")
	}
	// Cancel three independent live fake JVMs. Worker cleanup must run even when
	// the task deadline or lease context is already cancelled.
	t.Setenv("JMETER_TEST_WAIT_JAVA", "1")
	await := func(f func() bool) {
		t.Helper()
		end := time.Now().Add(7 * time.Second)
		for !f() {
			if time.Now().After(end) {
				t.Fatal("timed out waiting for execution stage")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, mode := range []string{"stop", "timeout", "fence"} {
		accepted.Store(false)
		task.TaskID = "run-" + mode
		task.AttemptID = "att-" + mode
		task.TimeoutSeconds = 30
		if mode == "timeout" {
			task.TimeoutSeconds = 10
		}
		m := dispatch(task)
		done := make(chan struct{})
		go func() { restarted.handle(ctx, m); close(done) }()
		await(func() bool { _, e := os.Stat(filepath.Join(s.taskDir(task.TaskID), "argv.json")); return e == nil })
		var started journal
		if e := load(r.file(task.TaskID), &started); e != nil {
			t.Fatal(e)
		}
		want := "FAILED"
		switch mode {
		case "stop":
			want = "STOPPED"
			rejectResets.Store(true)
			if !restarted.stop(task.TaskID) {
				t.Fatal("running JVM could not be stopped")
			}
			await(func() bool { return resetFailures.Load() > 0 })
			if ok, _ := q.LeaseOwned(ctx, started.Lease); !ok {
				t.Fatal("released lease while reset failed")
			}
			var waiting journal
			load(r.file(task.TaskID), &waiting)
			if waiting.Stage != "RESETTING" || len(waiting.Generations) == 0 {
				t.Fatal("reset failure marked clean")
			}
			invalidResetReceipts.Store(true)
			rejectResets.Store(false)
			await(func() bool { return invalidResetReplies.Load() > 0 })
			if ok, _ := q.LeaseOwned(ctx, started.Lease); !ok {
				t.Fatal("released lease for an unrelated reset receipt")
			}
			if e := load(r.file(task.TaskID), &waiting); e != nil || waiting.Stage != "RESETTING" || len(waiting.Generations) == 0 {
				t.Fatal("uncorrelated reset receipt allowed delivery to start")
			}
			invalidResetReceipts.Store(false)
		case "fence":
			want = "INTERRUPTED"
			if e := q.Release(ctx, started.Lease); e != nil {
				t.Fatal(e)
			}
			if ok, e := q.Acquire(ctx, "new-lease"); !ok || e != nil {
				t.Fatal("failed to simulate replacement")
			}
		}
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("cancelled execution did not clean up")
		}
		var ended journal
		load(r.file(task.TaskID), &ended)
		if ended.Outcome != want || ended.Stage != "UPLOADING" || len(ended.Generations) != 0 {
			t.Fatalf("%s: stage=%s outcome=%s pending=%d", mode, ended.Stage, ended.Outcome, len(ended.Generations))
		}
		if mode == "fence" {
			if ok, _ := q.LeaseOwned(ctx, "new-lease"); !ok {
				t.Fatal("stale runner released successor's lease")
			}
			q.Release(ctx, "new-lease")
		}
	}

}
