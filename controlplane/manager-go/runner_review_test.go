package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"pressurecooker/execution/protocol"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReviewCSVScopesAllowDisjointParentsRejectAncestors(t *testing.T) {
	g := basicWorkflowGraph()
	a := graphNode("loop-a", "LoopController", nil)
	b := graphNode("loop-b", "LoopController", nil)
	ca := graphNode("csv-a", "CSVDataSet", Obj{"variableNames": "id"})
	cb := graphNode("csv-b", "CSVDataSet", Obj{"variableNames": "id"})
	g["nodes"] = append(arr(g["nodes"]), a, b, ca, cb)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "loop-a", 1), graphEdge("group", "loop-b", 2), graphEdge("loop-a", "csv-a", 0), graphEdge("loop-b", "csv-b", 0))
	if _, e := compileWorkflow(g); e != nil {
		t.Fatal("disjoint loops rejected", e)
	}
	edges := arr(g["edges"])
	for _, v := range edges {
		e := obj(v)
		if e["targetNodeId"] == "loop-b" {
			e["sourceNodeId"] = "loop-a"
			obj(e["properties"])["order"] = 1
		}
	}
	if _, e := compileWorkflow(g); e == nil {
		t.Fatal("ancestor CSV collision accepted")
	}
}
func TestReviewGenerationStopsAtConfiguredByteLimit(t *testing.T) {
	body := Obj{"rows": 100000, "columns": []any{Obj{"name": "id", "generator": "constant", "value": strings.Repeat("x", 4096)}}}
	var out bytes.Buffer
	if e := generateDatasetCSVTo(context.Background(), body, ',', &out, 12345); e == nil {
		t.Fatal("generation ignored configured byte limit")
	}
	if out.Len() > 12345 {
		t.Fatal("generation wrote beyond bound", out.Len())
	}
}
func TestReviewUploadReadDeadlineTracksProgress(t *testing.T) {
	for _, stall := range []bool{false, true} {
		t.Run(map[bool]string{false: "progress", true: "idle"}[stall], func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				finish := progressRequestBody(w, r, 80*time.Millisecond)
				defer finish()
				if _, e := io.Copy(io.Discard, r.Body); e != nil {
					w.WriteHeader(408)
					return
				}
				w.WriteHeader(204)
			}))
			server.Config.ReadTimeout = 80 * time.Millisecond
			server.Start()
			defer server.Close()
			reader, writer := io.Pipe()
			defer reader.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				defer writer.Close()
				for i := 0; i < 10; i++ {
					if _, e := writer.Write([]byte("chunk\n")); e != nil {
						return
					}
					delay := 20 * time.Millisecond
					if stall {
						delay = 200 * time.Millisecond
					}
					time.Sleep(delay)
				}
			}()
			req, _ := http.NewRequest("POST", server.URL, reader)
			res, e := (&http.Client{Timeout: 2 * time.Second}).Do(req)
			if e != nil {
				if !stall {
					t.Fatal(e)
				}
			} else {
				res.Body.Close()
				if !stall && res.StatusCode != 204 {
					t.Fatal("progress deadline cut transfer", res.StatusCode)
				}
				if stall && res.StatusCode == 204 {
					t.Fatal("stalled body accepted")
				}
			}
			reader.Close()
			<-done
		})
	}
}
func TestRunnerReviewGenerationValidatesNodeBeforeSpooling(t *testing.T) {
	p, _, _ := newRunnerFixture(t)
	g := basicWorkflowGraph()
	csv := graphNode("csv-gen", "CSVDataSet", Obj{"variableNames": "id"})
	g["nodes"] = append(arr(g["nodes"]), csv)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "csv-gen", 1))
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "gen", "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	p.Config.DatasetMaxBytes = 1024
	body := Obj{"node_id": "missing", "rows": 100000, "columns": []any{Obj{"name": "id", "generator": "constant", "value": strings.Repeat("x", 4096)}}}
	if _, e = p.datasetGenerate(context.Background(), str(workflow["id"]), body); e == nil {
		t.Fatal("missing node accepted")
	}
	if _, e = os.Stat(filepath.Join(p.Config.StateDir, "dataset-cache")); !os.IsNotExist(e) {
		t.Fatal("invalid node generated spool bytes")
	}
	body["node_id"] = "csv-gen"
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.datasetGenerate(context.Background(), str(workflow["id"]), body)
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		var a *APIError
		if !errors.As(err, &a) || a.Status != 413 {
			t.Fatal("configured concurrent generation bound not enforced", err)
		}
	}
	entries, e := os.ReadDir(filepath.Join(p.Config.StateDir, "dataset-cache"))
	if e != nil || len(entries) != 0 {
		t.Fatal("private spool leaked", entries, e)
	}
}
func TestRunnerReviewLostControllerFencedResolutionUnblocksOtherCluster(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	nodeRedisFixture(t, p)
	var workerMu sync.Mutex
	workerIdle := false
	workerCalls, workerResets := 0, 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workerMu.Lock()
		defer workerMu.Unlock()
		if r.URL.Path != "/status" || r.Method != "GET" {
			workerResets++
			w.WriteHeader(409)
			return
		}
		workerCalls++
		state := "running"
		var job any = Obj{"id": "other-or-expired-task"}
		if workerIdle {
			state = "idle"
			job = nil
		}
		sendJSON(w, r, 200, Obj{"id": "worker-1", "healthy": true, "status": state, "job": job, "generation": 2, "boot_id": "worker-boot"})
	}))
	defer peer.Close()
	p.Config.WorkerURLs["worker-1"] = peer.URL
	p.Config.RunnerStaleAfter = time.Second
	p.Config.AgentTimeout = 100 * time.Millisecond
	create := func(controller, key string) Obj {
		run, _, e := p.createRun(Obj{"name": key, "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID(controller), "workers": []any{"worker-1"}}, key)
		if e != nil {
			t.Fatal(e)
		}
		return run
	}
	a := create("controller-a", "lost-a")
	// Before worker pre-occupation was enforced, installations could persist
	// overlapping queued Redis tasks. Seed that historical record directly so
	// this recovery test keeps exercising the older queued-task handoff.
	b := runnerLegacyRun(t, p, plan, "controller-b", []any{"worker-1"})
	b, e := p.updateRun(str(b["id"]), Obj{"execution_mode": "redis", "attempt_id": newID("attempt-"), "worker_snapshot": []protocol.Worker{{ID: "worker-1", AgentURL: peer.URL}}})
	if e != nil {
		t.Fatal(e)
	}
	q, e := p.runnerQueue()
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	stopReports := startRecoveryEpochPublisher(t, q, func(epoch string, sequence int64) protocol.CoreNodeReport {
		report := recoveryWorkerReport(epoch, sequence)
		workerMu.Lock()
		idle := workerIdle
		workerMu.Unlock()
		if !idle {
			report.Workers[0].Status = "running"
			report.Workers[0].Job = &protocol.NodeJob{ID: "other-or-expired-task", Kind: "run", State: "RUNNING"}
		}
		return report
	})
	defer stopReports()
	leaseA := "controller-a/" + str(a["id"]) + "/" + str(a["attempt_id"]) + "/original"
	ok, e := q.Acquire(context.Background(), leaseA)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	started := now()
	if result, e := p.runnerClaim(context.Background(), "controller-a", Obj{"task_id": a["id"], "attempt_id": a["attempt_id"], "lease_token": leaseA, "started_at": started}); e != nil || !boolean(result["execute"]) {
		t.Fatal(result, e)
	}
	if _, e = p.updateRun(str(a["id"]), Obj{"status": "RUNNING", "runner_heartbeat_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)}); e != nil {
		t.Fatal(e)
	}
	s := newScheduler(p, p.Config.ManagerID)
	defer s.close()
	resolve := func() {
		if e := s.tick(); e != nil {
			t.Fatal(e)
		}
	}
	resolve()
	workerMu.Lock()
	calls := workerCalls
	workerMu.Unlock()
	if calls != 0 {
		t.Fatal("live lease owner probed/recovered")
	}
	if e = q.Release(context.Background(), leaseA); e != nil {
		t.Fatal(e)
	}
	leaseB := "controller-b/" + str(b["id"]) + "/" + str(b["attempt_id"]) + "/newer"
	ok, e = q.Acquire(context.Background(), leaseB)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	resolve()
	stored, e := p.get("runs", str(a["id"]))
	if e != nil || stored["status"] != "RUNNING" {
		t.Fatal("newer lease disturbed", stored, e)
	}
	_ = q.Release(context.Background(), leaseB)
	resolve()
	stored, e = p.get("runs", str(a["id"]))
	if e != nil || stored["status"] != "RUNNING" {
		t.Fatal("busy/unknown worker considered safe", stored, e)
	}
	workerMu.Lock()
	workerIdle = true
	workerMu.Unlock()
	resolve()
	stored, e = p.get("runs", str(a["id"]))
	if e != nil || stored["status"] != "INTERRUPTED" || !boolean(stored["runner_reconciled"]) {
		t.Fatal("lost A kept worker reservation", stored, e)
	}
	resumed := "controller-a/" + str(a["id"]) + "/" + str(a["attempt_id"]) + "/resumed"
	ok, e = q.Acquire(context.Background(), resumed)
	if e != nil || !ok {
		t.Fatal("recovery lease not released", ok, e)
	}
	pending, e := p.runnerClaim(context.Background(), "controller-a", Obj{"task_id": a["id"], "attempt_id": a["attempt_id"], "lease_token": resumed})
	if e != nil || boolean(pending["execute"]) || !boolean(pending["result_required"]) {
		t.Fatal("local results would be prematurely acknowledged", pending, e)
	}
	_ = q.Release(context.Background(), resumed)
	ok, e = q.Acquire(context.Background(), leaseB)
	if e != nil || !ok {
		t.Fatal("recovery lease not released", ok, e)
	}
	defer q.Release(context.Background(), leaseB)
	claim, e := p.runnerClaim(context.Background(), "controller-b", Obj{"task_id": b["id"], "attempt_id": b["attempt_id"], "lease_token": leaseB, "started_at": now()})
	if e != nil || !boolean(claim["execute"]) {
		t.Fatal("healthy B blocked", claim, e)
	}
	store, e := p.objectStore()
	if e != nil {
		t.Fatal(e)
	}
	prefix, _ := rawPrefix(Obj{"id": a["id"], "started_at": started})
	ref, e := store.Put(context.Background(), prefix+"results.jtl", strings.NewReader("late\n"), 5, digest([]byte("late\n")), "text/csv")
	if e != nil {
		t.Fatal(e)
	}
	event := dtoObject(protocol.Callback{EventID: newID("event-"), TaskID: str(a["id"]), AttemptID: str(a["attempt_id"]), RunnerID: "controller-a", Status: "COMPLETED", StartedAt: started, FinishedAt: now(), Artifacts: []protocol.ObjectRef{ref}})
	if _, e = p.runnerCallback(context.Background(), "controller-a", event); e != nil {
		t.Fatal("late original receipt not acknowledged", e)
	}
	stored, e = p.getRun(str(a["id"]))
	if e != nil || stored["status"] != "INTERRUPTED" || len(arr(obj(stored["raw_archive"])["files"])) != 1 {
		t.Fatal("late files lost or terminal overwritten", stored, e)
	}
	other, e := p.getRun(str(b["id"]))
	if e != nil || other["status"] != "PREPARING" {
		t.Fatal("late A disturbed B", other, e)
	}
	workerMu.Lock()
	resets := workerResets
	calls = workerCalls
	workerMu.Unlock()
	if resets != 0 {
		t.Fatal("manager stale actor reset a worker", resets)
	}
	if calls != 0 {
		t.Fatal("manager recovered through an active worker probe", calls)
	}
}
func TestRunnerReviewEffectiveTaskDefaultsReachLinkedDetachedAndProducerScopes(t *testing.T) {
	p, _, _ := newRunnerFixture(t)
	environment, e := p.workspaceCreate("environments", Obj{"name": "effective", "base_host": "http://fixture:8083", "variables": []any{Obj{"name": "tenant", "value": "old"}, Obj{"name": "model_id", "value": "old-model"}}})
	if e != nil {
		t.Fatal(e)
	}
	other, e := p.workspaceCreate("environments", Obj{"name": "other-defaults", "base_host": "http://fixture:8083", "variables": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	plain, e := p.workspaceCreate("api_collections", collectionOverrideBody(str(other["id"]), "unused"))
	if e != nil {
		t.Fatal(e)
	}
	plainCurrent := clone(plain)
	obj(obj(plainCurrent["client_state"])[collectionOverridesKey])[str(other["id"])] = Obj{"variables": []any{Obj{"name": "tenant", "value": "only-other-env"}}}
	plain, e = p.workspaceUpdate("api_collections", str(plain["id"]), plainCurrent)
	if e != nil {
		t.Fatal(e)
	}
	overrideBody := collectionOverrideBody(str(environment["id"]), "unused")
	obj(obj(overrideBody["client_state"])[collectionOverridesKey])[str(environment["id"])] = Obj{"variables": []any{Obj{"name": "tenant", "value": "collection"}}}
	explicit, e := p.workspaceCreate("api_collections", overrideBody)
	if e != nil {
		t.Fatal(e)
	}
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"tenant"}
	linked := findGraphNode(g, "HTTPSamplerProxy")
	obj(linked["properties"])["config"] = Obj{"path": "/ping/${tenant}"}
	obj(linked["properties"])["api_reference"] = Obj{"collection_id": plain["id"], "revision": plain["revision"], "path": "/ping", "method": "get"}
	detached := graphNode("detached", "HTTPSamplerProxy", Obj{"path": "/ping/${tenant}"})
	extract := graphNode("extract-tenant", "JSONPostProcessor", Obj{"referenceName": "tenant", "jsonPathExpr": "$.tenant", "defaultValue": "missing"})
	dynamic := clone(linked)
	dynamic["id"] = "dynamic"
	groupB := graphNode("group-b", "ThreadGroup", nil)
	override := clone(linked)
	override["id"] = "explicit"
	obj(override["properties"])["api_reference"] = Obj{"collection_id": explicit["id"], "revision": 1, "path": "/ping", "method": "get"}
	g["nodes"] = append(arr(g["nodes"]), detached, extract, dynamic, groupB, override)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "detached", 1), graphEdge("detached", "extract-tenant", 0), graphEdge("group", "dynamic", 2), graphEdge("root", "group-b", 1), graphEdge("group-b", "explicit", 0))
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "defaults", "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	published, e := p.publishWorkflow(str(workflow["id"]), Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	run, _, e := p.createRun(Obj{"name": "effective", "cluster_id": clusterID("controller-a"), "plan_id": obj(published["plan"])["id"], "environment_id": environment["id"], "properties": Obj{"tenant": "new", "model": "lm", "model_id": "m1", "modeSelector": "fast"}}, "effective")
	if e != nil {
		t.Fatal(e)
	}
	var payload string
	if e = p.DB.QueryRow("SELECT payload FROM task_dispatches WHERE id=?", run["id"]).Scan(&payload); e != nil {
		t.Fatal(e)
	}
	data, e := p.openWorkspace("task_dispatches", str(run["id"]), 1, payload)
	if e != nil {
		t.Fatal(e)
	}
	var signed protocol.SignedTask
	if e = decodeInto(data, &signed); e != nil {
		t.Fatal(e)
	}
	props := signed.Task.Properties
	for _, key := range []string{"tenant", environmentProperty("tenant"), apiEnvironmentProperty("http", "tenant")} {
		if props[key] != "new" {
			t.Fatal("effective defaults mismatch", key, props[key])
		}
	}
	if props[apiEnvironmentProperty("explicit", "tenant")] != "collection" {
		t.Fatal("explicit collection override lost")
	}
	if _, exists := props[apiEnvironmentProperty("dynamic", "tenant")]; exists {
		t.Fatal("producer collision became static scopedprop")
	}
	for _, key := range []string{"model", "model_id", "modeSelector"} {
		if props[key] == "" {
			t.Fatal("legitimate mode-prefixed key rejected", key)
		}
	}
}
