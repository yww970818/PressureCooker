package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"unicode/utf8"

	"os"
	"path/filepath"
	"pressurecooker/execution/protocol"
	"strings"
	"testing"
	"time"
)

func TestDuplicateResetEvidenceBindsOwner(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 7, OperationID: "reset-attempt"}
	state := map[string]any{"Generation": 8, "LastReset": "reset-attempt", "LastResetOwner": a}
	w := &worker{s: &service{c: config{id: "worker", state: base}}}
	encoded, _ := json.Marshal(state)
	if err := json.Unmarshal(encoded, &w.rec); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"owner": a, "evidence": map[string]any{"node_id": "worker", "task_id": "task", "attempt_id": "attempt", "generation": 7, "status": "COLLECTED", "logs": []any{map[string]any{"name": "jmeter.log", "content": "original\n", "status": "COLLECTED", "size": 9, "line_from": 1, "line_to": 1}}}}
	digest := sha256.Sum256([]byte(a.LeaseToken))
	if err := save(filepath.Join(base, "node-evidence", "task", "attempt", hex.EncodeToString(digest[:])+"-7.json"), evidence); err != nil {
		t.Fatal(err)
	}
	got, err := w.reset(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(got)
	if !strings.Contains(string(data), `"content":"original\n"`) {
		t.Fatalf("duplicate reset lost durable evidence: %s", data)
	}
	foreign := a
	foreign.TaskID = "other"
	foreign.LeaseToken = "runner/other/attempt/lease"
	got, err = w.reset(context.Background(), foreign)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(got)
	if strings.Contains(string(data), "original") || got["operation_id"] != "superseded" {
		t.Fatalf("duplicate operation crossed owner identity: %s", data)
	}
	foreign = a
	foreign.Generation = 6
	got, err = w.reset(context.Background(), foreign)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(got)
	if strings.Contains(string(data), "original") {
		t.Fatal("generation mismatch leaked saved evidence")
	}
}

func TestRestartCollectsBeforeLogsAreCleared(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(base, "work", "engine")
	if err := directory(engine); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(engine, "jmeter.log")
	if err := os.WriteFile(log, []byte("startup\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 3}
	w := &worker{s: &service{c: config{id: "worker", state: filepath.Join(base, "state"), work: filepath.Join(base, "work"), java: "/nonexistent/fake-java"}}, rec: engineRecord{Generation: 3, Owner: &a}}
	w.rec.PreparedAt = "2026-11-07T01:00:00Z"
	w.rec.LogCursors = engineLogCursors(engine)
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("task failure\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = w.restart(context.Background(), "reset-attempt"); err == nil {
		t.Fatal("missing Java unexpectedly launched")
	}
	saved := w.savedEvidence(a)
	if saved == nil || saved.Status != "COLLECTED" || len(saved.Logs) != 2 {
		t.Fatalf("evidence missing before failed restart: %+v", saved)
	}
	if saved.Logs[0].Content != "task failure\n" || saved.Logs[0].LineFrom != 2 || saved.Logs[0].LineTo != 2 {
		t.Fatalf("scope includes startup or wrong line range: %+v", saved.Logs[0])
	}
	if saved.Logs[1].Status != "MISSING" {
		t.Fatal("absent console log not explicit")
	}
	if strings.Contains(saved.Logs[0].Content, "startup") {
		t.Fatal("startup evidence crossed task scope")
	}
	if err = w.restart(context.Background(), "reset-attempt"); err == nil {
		t.Fatal("missing Java unexpectedly launched")
	}
	saved = w.savedEvidence(a)
	if saved == nil || saved.Generation != 3 || saved.Logs[0].Content != "task failure\n" {
		t.Fatalf("restart retry replaced evidence: %+v", saved)
	}
}

func TestNodeLogMissingTruncationAndUTF8(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(base, "jmeter.log")
	if err := os.WriteFile(name, []byte("startup\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cursors := engineLogCursors(base)
	text := strings.Repeat("日志\n", 10000)
	f, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(text)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	got := collectNodeLog(name, "jmeter.log", cursors["jmeter.log"])
	if got.Status != "COLLECTED" || !got.Truncated || len(got.Content) > 32768 || got.Size != int64(len(text)) || got.LineFrom != 2 || got.LineTo <= 2 {
		t.Fatalf("bad bounded log: %+v", got)
	}
	b, err := json.Marshal(got)
	if err != nil || strings.Contains(string(b), `\ufffd`) {
		t.Fatal("truncation corrupted UTF-8")
	}
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	got = collectNodeLog(name, "jmeter.log", cursors["jmeter.log"])
	if got.Status != "MISSING" || got.Content != "" {
		t.Fatal("missing log was collected")
	}
	if err = os.WriteFile(name, []byte("unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got = collectNodeLog(name, "jmeter.log", cursors["jmeter.log"])
	if got.Status != "MISSING" {
		t.Fatal("replacement log crossed generation boundary")
	}
}

func TestResetSelectedJournalsOrderedEvidenceAndMissing(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workers := []protocol.Worker{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("worker-%d", i)
		sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var a assignment
			if json.NewDecoder(req.Body).Decode(&a) != nil || a.TaskID != "task" || a.AttemptID != "attempt" || a.Generation != 7 {
				t.Error("wrong reset identity")
			}
			evidence := map[string]any{"node_id": id, "task_id": "task", "attempt_id": "attempt", "generation": 7, "status": "COLLECTED", "logs": []any{map[string]any{"name": "jmeter.log", "content": "safe", "status": "COLLECTED", "size": 4, "line_from": 1, "line_to": 1}}}
			if i == 1 {
				evidence["task_id"] = "foreign"
			}
			response := map[string]any{"state": "COMPLETED", "operation_id": "reset-attempt", "generation": 8, "node_evidence": evidence}
			if i == 2 {
				delete(response, "node_evidence")
			}
			respond(w, 200, response)
		}))
		defer sv.Close()
		workers = append(workers, protocol.Worker{ID: id, AgentURL: sv.URL})
	}
	r := &runner{s: &service{c: config{id: "runner", state: base, token: "test-key"}, http: &http.Client{}}}
	j := &journal{Task: protocol.Task{TaskID: "task", AttemptID: "attempt", Workers: workers}, Lease: "runner/task/attempt/lease", Outcome: "FAILED", Generations: map[string]int64{"worker-0": 7, "worker-1": 7, "worker-2": 7}}
	if err := r.resetSelected(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if j.Outcome != "FAILED" || len(j.Generations) != 0 || len(j.NodeEvidence) != 3 {
		t.Fatalf("cleanup changed outcome or lost evidence: %+v", j)
	}
	for i, e := range j.NodeEvidence {
		if e.NodeID != fmt.Sprintf("worker-%d", i) || e.TaskID != "task" || e.AttemptID != "attempt" {
			t.Fatalf("bad selected order/identity: %+v", j.NodeEvidence)
		}
		want := "MISSING"
		if i == 0 {
			want = "COLLECTED"
		}
		if e.Status != want {
			t.Fatalf("worker %d: %s", i, e.Status)
		}
	}
	var disk journal
	if err := load(r.file("task"), &disk); err != nil || !reflect.DeepEqual(disk.NodeEvidence, j.NodeEvidence) {
		t.Fatal("evidence did not survive journal reload")
	}
}

func TestResetSelectedRequiresCorrelatedOperationAndGeneration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		expected    int64
		operation   string
		generation  any
		accepted    bool
		interrupted bool
	}{
		{name: "matching reset", expected: 7, operation: "reset-attempt", generation: 8, accepted: true},
		{name: "missing operation", expected: 7, generation: 8},
		{name: "foreign operation", expected: 7, operation: "reset-other", generation: 8},
		{name: "missing generation", expected: 7, operation: "reset-attempt"},
		{name: "zero generation", expected: 7, operation: "reset-attempt", generation: 0},
		{name: "negative generation", expected: 7, operation: "reset-attempt", generation: -1},
		{name: "string generation", expected: 7, operation: "reset-attempt", generation: "8"},
		{name: "fractional generation", expected: 7, operation: "reset-attempt", generation: 8.5},
		{name: "unchanged generation", expected: 7, operation: "reset-attempt", generation: 7},
		{name: "older generation", expected: 7, operation: "reset-attempt", generation: 6},
		{name: "later restart cannot impersonate reset", expected: 7, operation: "reset-attempt", generation: 9},
		{name: "superseded successor", expected: 7, operation: "superseded", generation: 8, accepted: true, interrupted: true},
		{name: "superseded later successor", expected: 7, operation: "superseded", generation: 10, accepted: true, interrupted: true},
		{name: "superseded unchanged generation", expected: 7, operation: "superseded", generation: 7},
		{name: "superseded older generation", expected: 7, operation: "superseded", generation: 6},
		{name: "superseded missing generation", expected: 7, operation: "superseded"},
		{name: "lost prepare response", operation: "reset-attempt", generation: 8, accepted: true},
		{name: "lost prepare missing generation", operation: "reset-attempt"},
		{name: "lost prepare superseded", operation: "superseded", generation: 8, accepted: true, interrupted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var a assignment
				if json.NewDecoder(req.Body).Decode(&a) != nil || a.OperationID != "reset-attempt" || a.Generation != tc.expected || a.LeaseToken != "runner/task/attempt/lease" {
					t.Error("reset request lost its immutable ownership")
				}
				response := map[string]any{"state": "COMPLETED", "operation_id": tc.operation, "node_evidence": protocol.NodeEvidence{NodeID: "worker", TaskID: "task", AttemptID: "attempt", Generation: 7, Status: "MISSING"}}
				if tc.generation != nil {
					response["generation"] = tc.generation
				}
				respond(w, 200, response)
			}))
			defer sv.Close()
			r := &runner{s: &service{c: config{id: "runner", state: base}, http: sv.Client()}}
			j := &journal{Task: protocol.Task{TaskID: "task", AttemptID: "attempt", Workers: []protocol.Worker{{ID: "worker", AgentURL: sv.URL}}}, Lease: "runner/task/attempt/lease", Outcome: "COMPLETED", Generations: map[string]int64{"worker": tc.expected}}
			err = r.resetSelected(context.Background(), j)
			if tc.accepted {
				if err != nil || len(j.Generations) != 0 || len(j.NodeEvidence) != 1 || j.NodeEvidence[0].Generation != 7 {
					t.Fatalf("valid reset did not retain old-generation evidence: err=%v journal=%+v", err, j)
				}
			} else if err == nil || len(j.Generations) != 1 || j.Generations["worker"] != tc.expected || len(j.NodeEvidence) != 0 {
				t.Fatalf("unconfirmed reset cleared ownership: err=%v journal=%+v", err, j)
			}
			want := "COMPLETED"
			if tc.interrupted {
				want = "INTERRUPTED"
			}
			if j.Outcome != want {
				t.Fatalf("outcome=%s want=%s", j.Outcome, want)
			}
			var saved journal
			if err := load(r.file("task"), &saved); err != nil || !reflect.DeepEqual(saved.Generations, j.Generations) || saved.Outcome != want {
				t.Fatalf("reset decision was not durably retained: err=%v saved=%+v", err, saved)
			}
		})
	}
}

func TestUploadBoundsEvidenceWithinCallbackAndRawContract(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &runner{s: &service{c: config{id: "runner", state: filepath.Join(base, "state"), results: filepath.Join(base, "results")}, s: mockStore(t)}}
	j := &journal{Task: protocol.Task{TaskID: "task", AttemptID: "attempt", CallbackURL: "http://manager"}, Started: "2026-11-07T00:00:00Z", Finished: "2026-11-07T00:01:00Z", Outcome: "FAILED"}
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("worker-%d", i)
		j.Task.Workers = append(j.Task.Workers, protocol.Worker{ID: id})
		j.NodeEvidence = append(j.NodeEvidence, protocol.NodeEvidence{NodeID: id, TaskID: "task", AttemptID: "attempt", Generation: 7, Status: "COLLECTED", Logs: []protocol.NodeLog{
			{Name: "jmeter.log", Status: "COLLECTED", Content: strings.Repeat("<日志>\x00\n", 5000), Size: 100000, LineFrom: 1, LineTo: 5000},
			{Name: "console.log", Status: "COLLECTED", Content: strings.Repeat("failure\n", 5000), Size: 35000, LineFrom: 1, LineTo: 5000},
		}})
	}
	if err := directory(r.raw("task")); err != nil {
		t.Fatal(err)
	}
	if err := r.upload(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(j.Callback) > 4<<20 {
		t.Fatalf("callback escaped past maximum: %d", len(j.Callback))
	}
	var cb protocol.Callback
	if err := json.Unmarshal(j.Callback, &cb); err != nil {
		t.Fatal(err)
	}
	if len(cb.NodeEvidence) != 64 || len(cb.Artifacts) != 0 || !reflect.DeepEqual(cb.MissingFiles, []string{"results.jtl", "jmeter.log", "console.log"}) {
		t.Fatal("evidence changed fixed raw-file contract")
	}
	size := 0
	truncated := 0
	for _, e := range cb.NodeEvidence {
		for _, l := range e.Logs {
			size += len(l.Content)
			if l.Truncated {
				truncated++
			}
			if len(l.Content) > 32768 || !utf8.ValidString(l.Content) {
				t.Fatal("invalid per-log bound/UTF8")
			}
		}
	}
	if size > 2<<20 || truncated == 0 {
		t.Fatalf("aggregate content unbounded: size=%d truncated=%d", size, truncated)
	}
	b, _ := json.Marshal(cb.NodeEvidence)
	if len(b) > 3<<20 {
		t.Fatal("escaped evidence unbounded")
	}
}

func TestSavedEvidenceKeepsEarlierGeneration(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &worker{s: &service{c: config{id: "worker", state: filepath.Join(base, "state"), work: filepath.Join(base, "work")}}}
	engine := filepath.Join(base, "work", "engine")
	if err = directory(engine); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(engine, "jmeter.log")
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 3}
	for _, generation := range []int64{3, 4} {
		if err = os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		cursor := engineLogCursors(engine)
		if err = os.WriteFile(path, []byte(fmt.Sprintf("generation-%d\n", generation)), 0600); err != nil {
			t.Fatal(err)
		}
		owner := a
		owner.Generation = generation
		w.captureEvidence(owner, "2026-11-07T00:00:00Z", cursor)
	}
	saved := w.savedEvidence(a)
	if saved == nil || saved.Logs[0].Content != "generation-3\n" {
		t.Fatalf("new generation replaced earlier receipt: %+v", saved)
	}
}

func TestResetSelectedHandlesWorkerRestartReceiptsConservatively(t *testing.T) {
	for _, tc := range []struct {
		name, operation string
		generation      int64
		pending         bool
	}{
		{name: "old idempotent receipt after another restart", operation: "reset-attempt", generation: 9, pending: true},
		{name: "failed restart already retired the old generation", generation: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			owner := &assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 7}
			ws := &service{c: config{id: "worker", role: "worker", state: base, token: "test-key"}}
			ws.worker = &worker{s: ws, rec: engineRecord{Generation: tc.generation, Ready: false, Owner: owner, LastReset: tc.operation, LastResetOwner: owner}}
			sv := httptest.NewServer(http.HandlerFunc(ws.handler))
			defer sv.Close()
			r := &runner{s: &service{c: config{id: "runner", state: base, token: "test-key"}, http: sv.Client()}}
			j := &journal{Task: protocol.Task{TaskID: "task", AttemptID: "attempt", Workers: []protocol.Worker{{ID: "worker", AgentURL: sv.URL}}}, Lease: owner.LeaseToken, Outcome: "COMPLETED", Generations: map[string]int64{"worker": 7}}
			err = r.resetSelected(context.Background(), j)
			if tc.pending {
				if err == nil || len(j.Generations) != 1 || j.Outcome != "COMPLETED" {
					t.Fatalf("later engine restart impersonated our reset: err=%v journal=%+v", err, j)
				}
			} else if err != nil || len(j.Generations) != 0 || j.Outcome != "INTERRUPTED" {
				t.Fatalf("superseded generation was reported completed: err=%v journal=%+v", err, j)
			}
			// Neither an idempotent reply nor superseded cleanup starts an engine,
			// changes its owner, or fabricates readiness after a failed restart.
			if ws.worker.rec.Generation != tc.generation || ws.worker.rec.Owner != owner || ws.worker.rec.Ready || ws.worker.status()["healthy"] != false || ws.worker.proc != nil {
				t.Fatal("reset receipt changed an unhealthy successor engine")
			}
		})
	}
}

func TestInvalidReceiptBecomesMissingWithoutBlockingReset(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"state": "COMPLETED", "operation_id": "reset-attempt", "generation": 8, "node_evidence": protocol.NodeEvidence{NodeID: "worker", TaskID: "task", AttemptID: "attempt", Generation: 7, Status: "COLLECTED", PreparedAt: "not-a-time", Logs: []protocol.NodeLog{{Name: "jmeter.log", Status: "COLLECTED", Content: "unsafe", Size: 6, LineFrom: 1, LineTo: 1}}}})
	}))
	defer sv.Close()
	r := &runner{s: &service{c: config{id: "runner", state: base}, http: &http.Client{}}}
	j := &journal{Task: protocol.Task{TaskID: "task", AttemptID: "attempt", Workers: []protocol.Worker{{ID: "worker", AgentURL: sv.URL}}}, Generations: map[string]int64{"worker": 7}, Outcome: "FAILED"}
	if err = r.resetSelected(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(j.NodeEvidence) != 1 || j.NodeEvidence[0].Status != "MISSING" || len(j.Generations) != 0 {
		t.Fatal("malformed evidence blocked cleanup or poisoned callback")
	}
}

func TestRestartStopsWriterBeforeEvidenceCapture(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "work", "engine")
	if err = directory(dir); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "jmeter.log"), []byte("startup\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := launch(config{java: "/bin/sh"}, []string{"-c", `trap 'echo final-task-line >> jmeter.log; exit 0' TERM; touch ready; while :; do sleep 1; done`}, dir, filepath.Join(dir, "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.kill()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 3}
	w := &worker{s: &service{c: config{id: "worker", state: filepath.Join(base, "state"), work: filepath.Join(base, "work"), java: "/nonexistent/fake-java"}}, proc: p, rec: engineRecord{Generation: 3, Owner: &a, PreparedAt: "2026-11-07T01:00:00Z", LogCursors: engineLogCursors(dir)}}
	if err = w.restart(context.Background(), "reset-attempt"); err == nil {
		t.Fatal("missing Java launched")
	}
	got := w.savedEvidence(a)
	if p.alive() || got == nil || !strings.Contains(got.Logs[0].Content, "final-task-line") {
		t.Fatalf("captured before writer exit: %+v", got)
	}
}

func TestEvidencePersistenceFailureDoesNotBlockClearingLogs(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "work", "engine")
	state := filepath.Join(base, "state")
	if err = directory(dir); err != nil {
		t.Fatal(err)
	}
	if err = directory(state); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(state, "node-evidence"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "jmeter.log")
	if err = os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cursors := engineLogCursors(dir)
	if err = os.WriteFile(path, []byte("task line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := assignment{TaskID: "task", AttemptID: "attempt", RunnerID: "runner", LeaseToken: "runner/task/attempt/lease", Generation: 3}
	w := &worker{s: &service{c: config{id: "worker", state: state, work: filepath.Join(base, "work"), java: "/nonexistent/fake-java"}}, rec: engineRecord{Generation: 3, Owner: &a, PreparedAt: "2026-11-07T01:00:00Z", LogCursors: cursors}}
	if err = w.restart(context.Background(), "reset-attempt"); err == nil {
		t.Fatal("missing Java launched")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != 0 {
		t.Fatal("evidence persistence blocked safe reset")
	}
	if w.savedEvidence(a) != nil {
		t.Fatal("failed save reported collected evidence")
	}
}

func TestBinaryLogRemainsValidBoundedText(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "jmeter.log")
	if err = os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cursor := engineLogCursors(base)["jmeter.log"]
	if err = os.WriteFile(path, []byte{0xff, '\n'}, 0600); err != nil {
		t.Fatal(err)
	}
	log := collectNodeLog(path, "jmeter.log", cursor)
	if log.Status != "COLLECTED" || !utf8.ValidString(log.Content) || int64(len(log.Content)) > log.Size || !log.Truncated {
		t.Fatalf("invalid bytes produced unusable evidence: %+v", log)
	}
}
