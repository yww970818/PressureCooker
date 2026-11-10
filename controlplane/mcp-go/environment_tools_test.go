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
	"strings"
	"testing"
)

func TestProjectEnvironmentToolsUseScopedCRUDContracts(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	const environment = `{"id":"environment-cccccccccccccccccccccccccccccccc","project_id":"project-one","name":"测试","base_host":"https://[::1]:8443/api","variables":[{"name":"token","value":"secret"}],"revision":3,"created_at":"2026-11-05T00:00:00Z","updated_at":"2026-11-05T01:00:00Z"}`
	variables := []any{map[string]any{"name": "token", "value": "secret"}}
	for _, tc := range []struct {
		name, method, path string
		args, body         map[string]any
		response           string
	}{
		{"list_project_environments", "GET", "/api/environments", nil, nil, `{"items":[` + environment + `],"total":1,"project_id":"project-one"}`},
		{"create_project_environment", "POST", "/api/environments", map[string]any{"name": "测试", "base_host": "https://[::1]:8443/api", "variables": variables}, map[string]any{"name": "测试", "base_host": "https://[::1]:8443/api", "variables": variables}, environment},
		{"get_project_environment", "GET", "/api/environments/environment-cccccccccccccccccccccccccccccccc", map[string]any{"environmentId": "environment-cccccccccccccccccccccccccccccccc"}, nil, environment},
		{"update_project_environment", "PUT", "/api/environments/environment-cccccccccccccccccccccccccccccccc", map[string]any{"environmentId": "environment-cccccccccccccccccccccccccccccccc", "revision": 2, "name": "测试", "base_host": "https://[::1]:8443/api", "variables": variables}, map[string]any{"revision": 2, "name": "测试", "base_host": "https://[::1]:8443/api", "variables": variables}, environment},
		{"delete_project_environment", "DELETE", "/api/environments/environment-cccccccccccccccccccccccccccccccc", map[string]any{"environmentId": "environment-cccccccccccccccccccccccccccccccc"}, nil, `{"deleted":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path || r.Header.Get("X-Project-ID") != "project-one" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected scoped request %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if r.ContentLength > 0 {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
				}
				wantRaw, _ := json.Marshal(tc.body)
				var want map[string]any
				json.Unmarshal(wantRaw, &want)
				if !reflect.DeepEqual(body, want) {
					t.Errorf("body %#v; want %#v", body, want)
				}
				io.WriteString(w, tc.response)
			}))
			defer api.Close()
			args := map[string]any{"projectId": "project-one"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := (managerTools{api: managerClient{baseURL: api.URL, tokenFile: token}}).call(context.Background(), tc.name, args)
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := decodeJSON([]byte(tc.response), &want); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !reflect.DeepEqual(result, want) {
				t.Fatalf("result %#v; want %#v, calls=%d", result, want, calls)
			}
		})
	}
}

func TestProjectEnvironmentToolsRejectInvalidArgumentsBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_project_environments", map[string]any{}},
		{"create_project_environment", map[string]any{"projectId": "project-one", "name": "测试"}},
		{"get_project_environment", map[string]any{"projectId": "project-one", "environmentId": "../escape"}},
		{"update_project_environment", map[string]any{"projectId": "project-one", "environmentId": "environment-cccccccccccccccccccccccccccccccc", "name": "测试", "base_host": "https://example.test", "variables": []any{}}},
		{"update_project_environment", map[string]any{"projectId": "project-one", "environmentId": "environment-cccccccccccccccccccccccccccccccc", "revision": 0, "name": "测试", "base_host": "https://example.test", "variables": []any{}}},
		{"create_project_environment", map[string]any{"projectId": "project-one", "name": "测试", "base_host": "https://example.test", "variables": map[string]any{"token": "value"}}},
		{"create_project_environment", map[string]any{"projectId": "project-one", "name": "测试", "base_host": "https://example.test", "variables": []any{map[string]any{"name": "token", "value": 1}}}},
		{"create_project_environment", map[string]any{"projectId": "project-one", "name": "测试", "base_host": "file:///tmp/target", "variables": []any{}}},
		{"create_project_environment", map[string]any{"projectId": "project-one", "name": "测试", "base_host": "https://example.test", "variables": []any{map[string]any{"name": "base_host", "value": "https://other.test"}}}},
		{"submit_console_task", map[string]any{"clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "projectId": "project-one", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "测试", "idempotencyKey": "stable", "environmentId": "../escape"}},
		{"debug_api_request", map[string]any{"projectId": "project-one", "collectionId": "api-one", "path": "/orders", "method": "GET", "environmentId": ""}},
		{"delete_project_environment", map[string]any{"environmentId": "environment-cccccccccccccccccccccccccccccccc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := (managerTools{}).call(context.Background(), tc.name, tc.args); err == nil || !strings.HasPrefix(err.Error(), "Invalid arguments for ") {
				t.Fatalf("arguments were not rejected by validation: %#v, error=%v", tc.args, err)
			}
		})
	}
}

func TestTaskEnvironmentForwardingKeepsIdempotencyAndSnapshot(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/runs" || r.Method != "POST" || r.Header.Get("X-Project-ID") != "project-one" || r.Header.Get("Idempotency-Key") != "stable-environment-run" {
			t.Errorf("wrong scoped submission %s %s", r.Method, r.URL)
		}
		if body["environment_id"] != "environment-cccccccccccccccccccccccccccccccc" || body["environmentId"] != nil || body["projectId"] != nil {
			t.Errorf("environment was not mapped: %#v", body)
		}
		io.WriteString(w, `{"id":"run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","environment_id":"environment-cccccccccccccccccccccccccccccccc","environment_snapshot":{"id":"environment-cccccccccccccccccccccccccccccccc","name":"测试","revision":2,"base_host":"https://example.test","variables":[{"name":"token","value":"secret"}]}}`)
	}))
	defer api.Close()
	args := map[string]any{"clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "projectId": "project-one", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "测试环境运行", "idempotencyKey": "stable-environment-run", "environmentId": "environment-cccccccccccccccccccccccccccccccc"}
	for i := 0; i < 2; i++ {
		result, err := (managerTools{api: managerClient{baseURL: api.URL, tokenFile: token}}).call(context.Background(), "submit_console_task", args)
		if err != nil {
			t.Fatal(err)
		}
		run := result.(map[string]any)
		snapshot := run["environment_snapshot"].(map[string]any)
		if run["executionId"] != "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || run["environment_id"] != "environment-cccccccccccccccccccccccccccccccc" || snapshot["revision"] != json.Number("2") || snapshot["base_host"] != "https://example.test" {
			t.Fatalf("lost run snapshot %#v", run)
		}
	}
	if calls != 2 {
		t.Fatalf("idempotent retry was not forwarded: %d calls", calls)
	}
}

func TestDebugProjectEnvironmentMapsIDAndPreservesLegacyEnvironment(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/api-collections/api-one/debug" || r.Method != "POST" || r.Header.Get("X-Project-ID") != "project-one" {
			t.Errorf("wrong scoped debug %s %s", r.Method, r.URL)
		}
		if body["environment_id"] != "environment-cccccccccccccccccccccccccccccccc" || body["target_source"] != "project_environment" || body["environmentId"] != nil || body["collectionId"] != nil || body["projectId"] != nil {
			t.Errorf("wrong environment mapping %#v", body)
		}
		io.WriteString(w, `{"status":200,"body":"ok"}`)
	}))
	defer api.Close()
	args := map[string]any{"projectId": "project-one", "collectionId": "api-one", "path": "/orders", "method": "GET", "environmentId": "environment-cccccccccccccccccccccccccccccccc", "target_source": "project_environment", "variables": map[string]any{"token": "local"}}
	if _, err := (managerTools{api: managerClient{baseURL: api.URL, tokenFile: token}}).call(context.Background(), "debug_api_request", args); err != nil {
		t.Fatal(err)
	}
}
