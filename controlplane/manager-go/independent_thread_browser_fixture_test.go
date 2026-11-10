package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"pressurecooker/execution/protocol"
)

// Explicit opt-in local fixture: actual non-network JMeter observations pass
// through callback, object verification, report storage and the ordinary UI.
// It never starts the scheduler or an execution agent.
func TestIndependentThreadBrowserFixture(t *testing.T) {
	record := os.Getenv("PC_THREAD_BROWSER_RECORD")
	if record == "" {
		t.Skip("set PC_THREAD_BROWSER_RECORD for isolated report browser fixture")
	}
	record, err := filepath.Abs(record)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(record, "normal", "thread-observations.csv"))
	if err != nil {
		t.Fatal(err)
	}
	jtl, err := os.ReadFile(filepath.Join(record, "normal", "results.jtl"))
	if err != nil {
		t.Fatal(err)
	}
	header, err := csv.NewReader(bytes.NewReader(raw)).Read()
	if err != nil || len(header) != 6 {
		t.Fatal("capture header", err)
	}
	generation, err := strconv.ParseInt(header[4], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	e := protocol.NodeEvidence{TaskID: header[1], AttemptID: header[2], NodeID: header[3], Generation: generation, Status: "COLLECTED", Logs: []protocol.NodeLog{}, ThreadObservationsStatus: "ARCHIVED"}
	var points []protocol.ThreadObservation
	complete, err := protocol.ReadThreadObservations(bytes.NewReader(raw), e, func(p protocol.ThreadObservation) error { points = append(points, p); return nil })
	if err != nil || !complete || len(points) < 5 {
		t.Fatal("actual source incomplete", complete, err)
	}
	stamp := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }
	e.PreparedAt = stamp(points[0].TimestampMS - 1000)
	e.FinishedAt = stamp(points[len(points)-1].TimestampMS + 1000)
	t.Setenv("JMETER_GO_INTEGRATION", "1")
	p, plan := mysqlFixture(t)
	peer := runnerObjectPeer(t)
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveBucket = "jmeter-reports"
	p.Config.ArchiveAccessKey = "test"
	p.Config.ArchiveSecretKey = "test-secret"
	p.Config.ArchiveTimeout = time.Second
	p.Config.FrontendDir = filepath.Join("..", "frontend", "dist")
	run := Obj{"id": e.TaskID, "project_id": defaultProjectID, "name": "QA-每秒独立线程采集-实际等待脚本", "status": "RUNNING", "execution_mode": "redis", "attempt_id": e.AttemptID, "cluster_snapshot": Obj{"controller_id": "controller-a"}, "plan_id": plan["id"], "plan_sha256": plan["sha256"], "plan_name": "官方Java SleepTest隔离脚本", "created_at": e.PreparedAt, "started_at": stamp(points[0].TimestampMS), "workers": []any{e.NodeID}, "properties": Obj{"sampleresult.timestamp.start": "false"}}
	if err = p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); err != nil {
		t.Fatal(err)
	}
	store, err := p.objectStore()
	if err != nil {
		t.Fatal(err)
	}
	sum := digest(raw)
	ref, err := store.Put(context.Background(), protocol.ThreadObservationsKey(e, sum), bytes.NewReader(raw), int64(len(raw)), sum, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	ref.Name = "thread-observations.csv"
	ref.URL = "s3://" + ref.Bucket + "/" + ref.Key
	e.ThreadObservations = &ref
	prefix, _ := rawPrefix(run)
	refs := []protocol.ObjectRef{}
	for name, data := range map[string][]byte{"results.jtl": jtl, "jmeter.log": []byte("isolated official JMeter fixture\n"), "console.log": []byte("one sleeping sampler; no network requests\n")} {
		ref, err := store.Put(context.Background(), prefix+name, bytes.NewReader(data), int64(len(data)), digest(data), "text/plain")
		if err != nil {
			t.Fatal(err)
		}
		ref.Name = name
		refs = append(refs, ref)
	}
	cb := dtoObject(protocol.Callback{EventID: newID("event-"), TaskID: e.TaskID, AttemptID: e.AttemptID, RunnerID: "controller-a", Status: "COMPLETED", StartedAt: str(run["started_at"]), FinishedAt: e.FinishedAt, Artifacts: refs, NodeEvidence: []protocol.NodeEvidence{e}})
	report := pressureComplete(t, p, run, cb)
	if report["status"] != "READY" || number(obj(report["metrics"])["sample_count"]) != 1 {
		t.Fatal("report fixture failed", report["status"], report["error"])
	}
	server := httptest.NewServer(Handler{p})
	defer server.Close()
	stop := filepath.Join(record, "local-browser.stop")
	_ = os.Remove(stop)
	if err = os.WriteFile(filepath.Join(record, "local-browser-token"), []byte(p.Config.AdminToken), 0600); err != nil {
		t.Fatal(err)
	}
	ready := Obj{"base_url": server.URL, "project_id": defaultProjectID, "project_name": "默认项目", "run_id": e.TaskID, "report_id": report["id"], "version": report["version"], "run_name": report["run_name"], "raw_observations": len(points), "actual_request_samples": 1, "source": "actual official JMeter SleepTest; observation timestamps/counts unchanged"}
	if err = os.WriteFile(filepath.Join(record, "local-browser.json"), encoded(ready), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("independent thread browser fixture ready: %d observations / one request", len(points)))
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("browser fixture timeout")
		case <-ticker.C:
			if _, err := os.Stat(stop); err == nil {
				return
			}
		}
	}
}
