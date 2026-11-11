package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityCapacitySubmission(t *testing.T) {
	properties := map[string]any{}
	for i := 1; i <= 50; i++ {
		properties[fmt.Sprintf("script_identity.actor_%02d.access", i)] = fmt.Sprintf("synthetic-%d", i)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/runs" || r.Header.Get("X-Project-ID") != "synthetic-project" || r.Header.Get("Idempotency-Key") != "stable-capacity" || body["environment_id"] != "environment-cccccccccccccccccccccccccccccccc" || body["cluster_id"] != "cluster-aaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Error("binding changed")
		}
		got := body["properties"].(map[string]any)
		if len(got) != 50 || got["script_identity.actor_50.access"] != "synthetic-50" {
			t.Error("identity truncation")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("synthetic-token"), 0600); err != nil {
		t.Fatal(err)
	}
	client := managerTools{api: managerClient{baseURL: server.URL, tokenFile: token, client: server.Client()}}
	args := map[string]any{"projectId": "synthetic-project", "clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "environmentId": "environment-cccccccccccccccccccccccccccccccc", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "Synthetic identities", "idempotencyKey": "stable-capacity", "properties": properties}
	for i := 0; i < 2; i++ {
		out, err := client.call(context.Background(), "submit_console_task", args)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(out)
		if len(b) > 500 {
			t.Fatal("response unexpectedly includes credentials")
		}
	}
	if calls != 2 {
		t.Fatal("missing stable retries")
	}
}
