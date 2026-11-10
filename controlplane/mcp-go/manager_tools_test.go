package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConsoleTaskValidatesBeforeSubmittingAndKeepsStablePayload(t *testing.T) {
	root := t.TempDir()
	token := filepath.Join(root, "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	var bodies []map[string]any
	var keys []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Project-ID") != "project-one" {
			t.Errorf("project header missing")
		}
		if r.URL.Path != "/api/runs" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","status":"QUEUED"}`)
	}))
	defer api.Close()
	tools := managerTools{api: managerClient{baseURL: api.URL, tokenFile: token, client: api.Client()}}
	valid := map[string]any{"environmentId": "environment-cccccccccccccccccccccccccccccccc", "timeoutSeconds": 3600, "datasetSelections": []any{}, "clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "projectId": "project-one", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": " internal ", "idempotencyKey": " stable-key ", "properties": map[string]any{"loops": "5"}, "notes": "console parity", "reportTargets": map[string]any{"min_rps": float64(25), "max_p95_ms": float64(500)}}
	for _, bad := range []map[string]any{
		{"managerPlanId": valid["managerPlanId"], "name": "missing project", "idempotencyKey": "key"},
		{"managerPlanId": valid["managerPlanId"], "name": "no key"},
		{"managerPlanId": valid["managerPlanId"], "name": "missing cluster", "idempotencyKey": "key"},
		{"managerPlanId": valid["managerPlanId"], "name": "empty key", "idempotencyKey": " "},
		{"managerPlanId": "../../escape", "name": "x", "idempotencyKey": "key"},
		{"managerPlanId": valid["managerPlanId"], "name": "x", "idempotencyKey": "key", "workers": []any{"worker-1", "worker-1"}},
	} {
		if bad["name"] != "missing cluster" {
			bad["clusterId"] = valid["clusterId"]
		}
		if bad["name"] != "missing project" {
			bad["projectId"] = "project-one"
		}
		if _, err := tools.call(context.Background(), "submit_console_task", bad); err == nil {
			t.Fatalf("accepted invalid submission %v", bad)
		}
	}
	if len(bodies) != 0 {
		t.Fatal("invalid submission reached manager")
	}
	for i := 0; i < 2; i++ {
		result, err := tools.call(context.Background(), "submit_console_task", valid)
		if err != nil {
			t.Fatal(err)
		}
		if result.(map[string]any)["executionId"] != "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
			t.Fatalf("missing execution ID: %v", result)
		}
	}
	if !reflect.DeepEqual(bodies[0], bodies[1]) || keys[0] != "stable-key" || keys[1] != "stable-key" {
		t.Fatalf("retry changed submission: %v %v", bodies, keys)
	}
	if !reflect.DeepEqual(bodies[0]["report_targets"], valid["reportTargets"]) {
		t.Fatal("report targets not forwarded", bodies[0])
	}
	if _, present := bodies[0]["workers"]; present {
		t.Fatal("omitted workers must remain server default", bodies[0])
	}
	if bodies[0]["environment_id"] != valid["environmentId"] || bodies[0]["timeout_seconds"] != float64(3600) || len(bodies[0]["dataset_selections"].([]any)) != 0 {
		t.Fatal("new task fields missing", bodies[0])
	}
	if bodies[0]["cluster_id"] != valid["clusterId"] || bodies[0]["plan_id"] != valid["managerPlanId"] || bodies[0]["name"] != "internal" || bodies[0]["notes"] != "console parity" || !reflect.DeepEqual(bodies[0]["properties"], valid["properties"]) {
		t.Fatalf("wrong execution payload %v", bodies[0])
	}
	valid["workers"] = []any{"load-node-a", "region_b.worker-12"}
	valid["idempotencyKey"] = "selected-workers"
	if _, err := tools.call(context.Background(), "submit_console_task", valid); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bodies[2]["workers"], valid["workers"]) || keys[2] != "selected-workers" {
		t.Fatalf("console worker selection changed: %v", bodies[2])
	}
}

func TestReadAndStopArgumentsRejectUnsafeIDsAndUnboundedLogs(t *testing.T) {
	tools := managerTools{}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_execution_status", map[string]any{"executionId": "../secret"}},
		{"stop_execution", map[string]any{"executionId": "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "mode": "delete"}},
		{"get_execution_log", map[string]any{"executionId": "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "tail": 2001}},
		{"list_executions", map[string]any{"limit": 201}},
	} {
		tc.args["projectId"] = "project-one"
		if _, err := tools.call(context.Background(), tc.name, tc.args); err == nil {
			t.Fatalf("accepted invalid %s arguments", tc.name)
		}
	}
}
