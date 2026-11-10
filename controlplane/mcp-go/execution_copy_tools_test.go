package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCopiedExecutionSubmissionKeepsSourceSnapshotChoiceAndNewKey(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	seen := []map[string]any{}
	keys := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/runs" || r.Header.Get("X-Project-ID") != "project-one" {
			t.Error("copy submission scope changed")
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		seen = append(seen, body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		io.WriteString(w, `{"id":"run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	}))
	defer server.Close()
	m := managerTools{api: managerClient{baseURL: server.URL, tokenFile: token, client: server.Client()}}
	args := map[string]any{"projectId": "project-one", "clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "environmentId": "environment-cccccccccccccccccccccccccccccccc", "name": "copy", "idempotencyKey": "new-copy-task", "copySourceExecutionId": "run-dddddddddddddddddddddddddddddddd", "copyEnvironmentSnapshot": true}
	if _, e := m.call(context.Background(), "submit_console_task", args); e != nil {
		t.Fatal(e)
	}
	args["copyEnvironmentSnapshot"] = false
	if _, e := m.call(context.Background(), "submit_console_task", args); e != nil {
		t.Fatal(e)
	}
	for i, body := range seen {
		if body["copy_source_run_id"] != args["copySourceExecutionId"] || body["copy_environment_snapshot"] != (i == 0) || body["copySourceExecutionId"] != nil {
			t.Fatal("copy choices not mapped", body)
		}
		if keys[i] != "new-copy-task" {
			t.Fatal("copy task idempotency changed")
		}
	}
	delete(args, "copySourceExecutionId")
	args["copyEnvironmentSnapshot"] = true
	if _, e := m.call(context.Background(), "submit_console_task", args); e == nil || len(seen) != 2 {
		t.Fatal("historical snapshot without source reached API")
	}
}
