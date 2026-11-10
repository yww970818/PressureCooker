package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in UI fixture: isolated SQL prefix and in-process object storage; no
// scheduler, runner, JMeter process or business HTTP sampler is started.
func TestRunParameterBrowserFixture(t *testing.T) {
	dir := os.Getenv("PC_RUN_PARAMETER_BROWSER")
	if dir == "" {
		t.Skip("set PC_RUN_PARAMETER_BROWSER to a private absolute directory")
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("browser record directory must be absolute")
	}
	p, _, dataset := onlineDatasetFixture(t)
	p.Config.FrontendDir = filepath.Join("..", "frontend", "dist")
	plan, err := p.createPlan(Obj{"name": "参数表验收脚本", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.workspaceCreate("environments", Obj{"name": "测试环境", "base_host": "https://before.environment.invalid", "variables": []any{Obj{"name": "token", "value": "browser-environment-secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := p.createRun(Obj{"name": "环境与实际参数验收", "cluster_id": clusterID("controller-a"), "plan_id": plan["id"], "environment_id": env["id"], "workers": []any{"worker-1"}, "properties": Obj{"cookie": "browser-task-cookie", "threads": "2", "payload": "第一行\n second=value "}}, "browser-parameter-fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.updateRun(str(run["id"]), Obj{"status": "COMPLETED", "datasets": []any{Obj{"node_id": "csv-online", "node_name": "业务数据", "columns": []any{"id", "name"}, "variable_names": []any{"requestId", "requestName"}, "delimiter": ";", "distribution": "copy", "files": []any{dataset["object"]}}}, "dataset_selections": []any{Obj{"node_id": "csv-online", "dataset_ids": []any{dataset["id"]}, "distribution": "copy"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.workspaceUpdate("environments", str(env["id"]), Obj{"revision": env["revision"], "name": "测试环境", "base_host": "https://current.environment.invalid", "variables": []any{Obj{"name": "token", "value": "current-environment-secret"}}}); err != nil {
		t.Fatal(err)
	}
	_, call := workspaceHTTP(t, p)
	if code, _ := call("DELETE", "/api/datasets/"+str(dataset["id"]), nil); code != 200 {
		t.Fatal("fixture soft-delete", code)
	}
	server := httptest.NewServer(Handler{p})
	defer server.Close()
	ready := Obj{"base_url": server.URL, "run_id": run["id"], "project_id": p.projectID(), "token": p.Config.AdminToken}
	if err = os.WriteFile(filepath.Join(dir, "ready.json"), encoded(ready), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("run parameter browser fixture ready; execution disabled")
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(filepath.Join(dir, "stop")); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
