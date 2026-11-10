package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"pressurecooker/manager/reporting"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Synthetic local evidence only. No scheduler, real worker, or SUT is contacted.
func TestPlatformPerformanceAcceptance(t *testing.T) {
	if os.Getenv("JMETER_PERFORMANCE_ACCEPTANCE") != "1" {
		t.Skip("explicit 5000/1M/10k acceptance")
	}
	p, _ := mysqlFixture(t)
	content, err := compileWorkflow(basicWorkflowGraph())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.createPlan(Obj{"name": "辅助健康检查（验收夹具）", "content": content}); err != nil {
		t.Fatal(err)
	}
	results := Obj{"scale": Obj{"workflow_nodes": 5000, "raw_samples": 1000000, "task_history": 10000}}
	started := time.Now()
	graph := capacityGraph(5000)
	for i, v := range arr(graph["nodes"]) {
		node := obj(v)
		node["x"] = 880
		node["y"] = 100 + i*114
		if i == 0 {
			node["x"], node["y"] = 320, 100
		} else if i == 1 {
			node["x"], node["y"] = 600, 100
		}
		props := obj(obj(v)["properties"])
		if props["component"] == "HTTPSamplerProxy" {
			config := obj(props["config"])
			if config == nil {
				config = Obj{}
				props["config"] = config
			}
			config["bodyJson"] = strings.Repeat("synthetic", 450)
		}
	}
	w, e := p.workspaceCreate("workflows", Obj{"name": "验收专用 · 5000节点流程", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	results["workflow_create_ms"] = time.Since(started).Milliseconds()
	results["workflow_id"] = w["id"]
	measure := func(name string, fn func() (Obj, error)) {
		t.Helper()
		times := []int64{}
		size := 0
		for i := 0; i < 5; i++ {
			start := time.Now()
			v, e := fn()
			if e != nil {
				t.Fatal(e)
			}
			times = append(times, time.Since(start).Microseconds())
			size = len(encoded(v))
		}
		results[name] = Obj{"elapsed_us": times, "json_bytes": size}
	}
	measure("workflow_list", func() (Obj, error) { return p.workspaceSummaryList("workflows", url.Values{}) })
	file, e := os.CreateTemp(t.TempDir(), "million-*.jtl")
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 1<<20)
	fmt.Fprintln(writer, "timeStamp,elapsed,label,success,responseCode,responseMessage,bytes,sentBytes,engine_id,Latency,Connect,allThreads,sample_kind")
	for i := 0; i < 1000000; i++ {
		ok, code, msg := true, "200", "OK"
		if i%100 < 9 {
			ok, code, msg = false, "500", "HTTP failure"
		} else if i%100 == 9 {
			ok, code, msg = false, "Non HTTP response code: java.net.SocketTimeoutException", "Read timed out"
		}
		fmt.Fprintf(writer, "%d,%d,step-%03d,%t,%s,%s,1024,128,worker-%d,12,2,100,ordinary\n", int64(1794000000000)+int64(i)*900000/1000000, 20+i%980, i%200, ok, code, msg, (i/200)%6+1)
	}
	if e = writer.Flush(); e != nil {
		t.Fatal(e)
	}
	if _, e = file.Seek(0, 0); e != nil {
		t.Fatal(e)
	}
	var peak atomic.Uint64
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak.Load() {
					peak.Store(m.HeapAlloc)
				}
			case <-done:
				return
			}
		}
	}()
	started = time.Now()
	a, e := reporting.ParseJTL(context.Background(), file, reporting.ParseOptions{AdaptiveDashboard: true, ScopeConfirmed: true, ExpectedNodes: []string{"worker-1", "worker-2", "worker-3", "worker-4", "worker-5", "worker-6"}})
	close(done)
	if e != nil {
		t.Fatal(e)
	}
	results["million_parse"] = Obj{"elapsed_ms": time.Since(started).Milliseconds(), "peak_heap_bytes": peak.Load(), "sample_count": a.Metrics.SampleCount, "success_count": a.Metrics.SuccessCount, "failure_count": a.Metrics.FailureCount, "granularity_ms": a.Dashboard.Options.GranularityMS, "scopes": len(a.Dashboard.Scopes)}
	if a.Metrics.SampleCount != 1000000 || a.Metrics.SuccessCount != 900000 || a.Metrics.FailureCount != 100000 {
		t.Fatal("million-sample accounting mismatch")
	}
	id := newID("pressure-")
	results["report_id"] = id
	e = p.transaction(func(tx *Tx) error {
		header, e := p.storeDashboard(tx, id, p.projectID(), a.Dashboard)
		if e != nil {
			return e
		}
		report := Obj{"id": id, "project_id": p.projectID(), "run_id": "synthetic-million", "run_name": "验收专用 · 百万原始请求", "status": "READY", "version": 1, "dashboard": header, "nodes": a.Nodes, "metrics": a.Metrics, "quality": a.Quality, "timeline": a.Timeline, "created_at": now()}
		_, e = tx.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,1,'READY',?,?,?)", id, p.projectID(), "synthetic-million", now(), digest([]byte(id)), string(encoded(report)))
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	measure("report_summary", func() (Obj, error) { return p.pressureRead(id, true) })
	measure("report_analytics", func() (Obj, error) {
		return p.pressureAnalytics(id, httptest.NewRequest("GET", "/analytics?series_limit=20", nil))
	})
	started = time.Now()
	e = p.transaction(func(tx *Tx) error {
		for i := 0; i < 10000; i++ {
			run := Obj{"id": fmt.Sprintf("run-perf-%05d", i), "project_id": p.projectID(), "name": fmt.Sprintf("验收任务 %05d", i), "status": "SUCCEEDED", "created_at": time.Unix(1794000000+int64(i), 0).UTC().Format(time.RFC3339), "notes": "synthetic history only", "properties": Obj{"threads": "1"}, "workers": []any{"worker-1"}}
			if i >= 9980 {
				properties := obj(run["properties"])
				for key := 0; key < 64; key++ {
					properties[fmt.Sprintf("bulk_%02d", key)] = strings.Repeat("synthetic-parameter-", 100)
				}
				for reportIndex := 0; reportIndex < 2; reportIndex++ {
					legacy := Obj{"id": newID("report-"), "project_id": p.projectID(), "run_id": run["id"], "status": "READY", "legacy_details": strings.Repeat("synthetic-report-", 30000)}
					if _, err := tx.Exec("INSERT INTO reports(id,project_id,run_id,status,created_at,data) VALUES(?,?,?,'READY',?,?)", legacy["id"], p.projectID(), run["id"], now(), string(encoded(legacy))); err != nil {
						return err
					}
				}
			}
			if e := p.saveRun(tx, run); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	results["history_seed_ms"] = time.Since(started).Milliseconds()
	measure("history_first_page", func() (Obj, error) { return p.listRunSummaries(context.Background(), "", "", 20, 0) })
	measure("history_last_page", func() (Obj, error) { return p.listRunSummaries(context.Background(), "", "", 20, 9980) })
	measure("history_search", func() (Obj, error) { return p.listRunSummaries(context.Background(), "", "00999", 20, 0) })
	if output := os.Getenv("JMETER_PERFORMANCE_OUTPUT"); output != "" {
		if e = os.WriteFile(output, encoded(results), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("local acceptance: 5000 nodes, 1M samples, 10000 history; parse=%v", results["million_parse"])
	if os.Getenv("JMETER_PERFORMANCE_HOLD") == "1" {
		p.Config.FrontendDir = filepath.Join("..", "frontend", "dist")
		server := httptest.NewServer(Handler{p})
		defer server.Close()
		results["server_url"] = server.URL
		if output := os.Getenv("JMETER_PERFORMANCE_OUTPUT"); output != "" {
			if e = os.WriteFile(output, encoded(results), 0600); e != nil {
				t.Fatal(e)
			}
		}
		t.Logf("synthetic browser fixture ready: %s", server.URL)
		for i := 0; i < 3600; i++ {
			if _, e = os.Stat(os.Getenv("JMETER_PERFORMANCE_OUTPUT") + ".stop"); e == nil {
				break
			}
			time.Sleep(time.Second)
		}
	}
}
