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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOldExecutionToolCannotBeAdvertisedOrCalled(t *testing.T) {
	for _, spec := range managerSpecs {
		if spec.name == "execute_test_plan" {
			t.Fatal("obsolete execution tool is advertised")
		}
	}
	if _, err := (managerTools{}).call(context.Background(), "execute_test_plan", map[string]any{"projectId": "project-one", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "old", "idempotencyKey": "key"}); err == nil {
		t.Fatal("obsolete execution tool is callable")
	}
}

func TestBusinessToolsRequireExplicitProject(t *testing.T) {
	for _, spec := range managerSpecs {
		if spec.name == "list_projects" || spec.name == "create_project" {
			continue
		}
		props := spec.schema["properties"].(map[string]any)
		if props["projectId"] == nil {
			t.Errorf("%s has no project schema", spec.name)
			continue
		}
		required := spec.schema["required"].([]string)
		found := false
		for _, key := range required {
			if key == "projectId" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s projectId is optional", spec.name)
		}
	}
}

func TestProjectAndCollectionToolsUseScopedManagerContracts(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	const project = "project-one"
	operation := map[string]any{"summary": "保真请求", "requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"example": map[string]any{"x": "${value}"}}}}}
	document := map[string]any{"openapi": "3.1.0", "paths": map[string]any{"/orders": map[string]any{"post": operation}}}
	for _, tc := range []struct {
		name, method, path string
		args, body         map[string]any
	}{
		{"list_projects", "GET", "/api/projects", map[string]any{}, nil},
		{"create_project", "POST", "/api/projects", map[string]any{"name": "第二项目", "description": "描述"}, map[string]any{"name": "第二项目", "description": "描述"}},
		{"get_project", "GET", "/api/projects/" + project, nil, nil},
		{"update_project", "PUT", "/api/projects/" + project, map[string]any{"revision": 1, "name": "新名称", "description": "说明"}, map[string]any{"revision": 1, "name": "新名称", "description": "说明"}},
		{"archive_project", "POST", "/api/projects/" + project + "/archive", map[string]any{"revision": 1}, map[string]any{"revision": 1}},
		{"upload_script", "POST", "/api/plans", map[string]any{"name": "精确脚本", "content": "<jmeterTestPlan/>", "description": "上传"}, map[string]any{"name": "精确脚本", "content": "<jmeterTestPlan/>", "description": "上传"}},
		{"list_manager_plans", "GET", "/api/plans?limit=200&offset=0&view=summary", nil, nil},
		{"list_api_collections", "GET", "/api/api-collections?limit=200&offset=0&view=summary", nil, nil},
		{"create_api_collection", "POST", "/api/api-collections", map[string]any{"name": "集合", "document": document, "client_state": map[string]any{"auth": "secret"}}, map[string]any{"name": "集合", "document": document, "client_state": map[string]any{"auth": "secret"}}},
		{"get_api_collection", "GET", "/api/api-collections/api-one", map[string]any{"collectionId": "api-one"}, nil},
		{"update_api_collection", "PUT", "/api/api-collections/api-one", map[string]any{"collectionId": "api-one", "name": "集合", "document": document, "revision": 2}, map[string]any{"name": "集合", "document": document, "revision": 2}},
		{"delete_api_collection", "DELETE", "/api/api-collections/api-one", map[string]any{"collectionId": "api-one"}, nil},
		{"import_api_collection", "POST", "/api/api-collections/import", map[string]any{"format": "curl", "content": "curl 'https://example.test?a=1&a=2'", "name": "导入"}, map[string]any{"format": "curl", "content": "curl 'https://example.test?a=1&a=2'", "name": "导入"}},
		{"create_api_request", "PUT", "/api/api-collections/api-one/requests", map[string]any{"collectionId": "api-one", "revision": 2, "path": "/orders", "method": "POST", "operation": operation}, map[string]any{"revision": 2, "path": "/orders", "method": "POST", "operation": operation}},
		{"update_api_request", "PUT", "/api/api-collections/api-one/requests", map[string]any{"collectionId": "api-one", "revision": 2, "path": "/new", "method": "POST", "operation": operation, "previous_path": "/orders", "previous_method": "GET"}, map[string]any{"revision": 2, "path": "/new", "method": "POST", "operation": operation, "previous_path": "/orders", "previous_method": "GET"}},
		{"debug_api_request", "POST", "/api/api-collections/api-one/debug", map[string]any{"collectionId": "api-one", "path": "/orders", "method": "POST", "target_source": "document_server", "variables": map[string]any{"token": "secret"}}, map[string]any{"path": "/orders", "method": "POST", "target_source": "document_server", "variables": map[string]any{"token": "secret"}}},
		{"list_api_collection_environments", "GET", "/api/api-collections/api-one/collection-environments", map[string]any{"collectionId": "api-one"}, nil},
		{"get_api_collection_environment", "GET", "/api/api-collections/api-one/collection-environments/collection-env-one", map[string]any{"collectionId": "api-one", "collectionEnvironmentId": "collection-env-one"}, nil},
		{"create_api_collection_environment", "POST", "/api/api-collections/api-one/collection-environments", map[string]any{"collectionId": "api-one", "revision": 2, "name": "独立", "base_host": "https://example.test"}, map[string]any{"revision": 2, "name": "独立", "base_host": "https://example.test"}},
		{"update_api_collection_environment", "PUT", "/api/api-collections/api-one/collection-environments/collection-env-one", map[string]any{"collectionId": "api-one", "collectionEnvironmentId": "collection-env-one", "revision": 3, "name": "更新", "base_host": "https://new.test"}, map[string]any{"revision": 3, "name": "更新", "base_host": "https://new.test"}},
		{"delete_api_collection_environment", "DELETE", "/api/api-collections/api-one/collection-environments/collection-env-one", map[string]any{"collectionId": "api-one", "collectionEnvironmentId": "collection-env-one", "revision": 4}, map[string]any{"revision": 4}},
		{"set_api_environments", "PUT", "/api/api-collections/api-one/environments", map[string]any{"collectionId": "api-one", "revision": 2, "environmentId": "environment-0123456789abcdef0123456789abcdef", "variables": []any{map[string]any{"name": "token", "value": ""}}}, map[string]any{"revision": 2, "environment_id": "environment-0123456789abcdef0123456789abcdef", "variables": []any{map[string]any{"name": "token", "value": ""}}}},
		{"update_api_collection_settings", "PUT", "/api/api-collections/api-one/settings", map[string]any{"collectionId": "api-one", "revision": 2, "name": "设置", "servers": []any{map[string]any{"url": "https://example.test"}}, "client_state": map[string]any{"proxy": "master"}}, map[string]any{"revision": 2, "name": "设置", "servers": []any{map[string]any{"url": "https://example.test"}}, "client_state": map[string]any{"proxy": "master"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("route %s %s; want %s %s", r.Method, r.URL, tc.method, tc.path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing manager auth")
				}
				if tc.name != "list_projects" && tc.name != "create_project" && r.Header.Get("X-Project-ID") != project {
					t.Error("missing project header")
				}
				if (tc.name == "list_projects" || tc.name == "create_project") && r.Header.Get("X-Project-ID") != "" {
					t.Error("global project tools must not invent a default project")
				}
				var got map[string]any
				if r.ContentLength > 0 {
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
				}
				wantRaw, _ := json.Marshal(tc.body)
				var want map[string]any
				json.Unmarshal(wantRaw, &want)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("payload %#v; want %#v", got, want)
				}
				io.WriteString(w, `{"id":"api-one","project_id":"project-one","revision":3}`)
			}))
			defer api.Close()
			args := map[string]any{}
			for k, v := range tc.args {
				args[k] = v
			}
			if tc.name != "list_projects" && tc.name != "create_project" {
				args["projectId"] = project
			}
			value, err := (managerTools{api: managerClient{baseURL: api.URL, tokenFile: token}}).call(context.Background(), tc.name, args)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || value.(map[string]any)["project_id"] != project {
				t.Fatalf("unexpected result %v, calls %d", value, calls)
			}
		})
	}
}

func TestDownloadsPreserveExactSourceAndProject(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, content string
		args                map[string]any
	}{
		{"download_script", "/api/plans/plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/content", "<?xml version=\"1.0\"?>\r\n<jmeterTestPlan><!--中文原始版本--></jmeterTestPlan>\n", map[string]any{"managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		{"download_script", "/api/workflows/flow-one/export", "<jmeterTestPlan>\n<!--当前流程-->\n</jmeterTestPlan>", map[string]any{"workflowId": "flow-one"}},
		{"export_api_collection", "/api/api-collections/api-one/export?format=postman", "{\n  \"info\": {\"name\": \"原文\"}\n}\n", map[string]any{"collectionId": "api-one", "format": "postman"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.RequestURI() != tc.path || r.Header.Get("X-Project-ID") != "project-one" {
					t.Errorf("wrong scoped download %s %s", r.Method, r.URL)
				}
				io.WriteString(w, tc.content)
			}))
			defer api.Close()
			tc.args["projectId"] = "project-one"
			value, err := (managerTools{api: managerClient{baseURL: api.URL, tokenFile: token}}).call(context.Background(), tc.name, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if value.(map[string]any)["content"] != tc.content {
				t.Fatalf("source changed: %#v", value)
			}
		})
	}
}

func TestTaskReadStopLogAndArtifactCallsCarryProject(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	const run = "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, tc := range []struct {
		name, path string
		args       map[string]any
	}{
		{"get_cluster_status", "/api/cluster", nil},
		{"list_executions", "/api/runs?limit=10&search=demo", map[string]any{"limit": 10, "search": "demo"}},
		{"get_execution_status", "/api/runs/" + run, map[string]any{"executionId": run}},
		{"stop_execution", "/api/runs/" + run + "/stop", map[string]any{"executionId": run, "mode": "now"}},
		{"get_execution_log", "/api/runs/" + run + "/log?tail=200", map[string]any{"executionId": run}},
		{"get_execution_log", "/api/runs/" + run + "/log?tail=200", map[string]any{"executionId": run, "tail": json.Number("2e2")}},
		{"list_execution_artifacts", "/api/runs/" + run + "/artifacts", map[string]any{"executionId": run}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RequestURI() != tc.path || r.Header.Get("X-Project-ID") != "project-one" {
					t.Errorf("unscoped query %s", r.URL)
				}
				io.WriteString(w, `{"ok":true}`)
			}))
			defer api.Close()
			args := map[string]any{"projectId": "project-one"}
			for k, v := range tc.args {
				args[k] = v
			}
			if _, err := (managerTools{api: managerClient{baseURL: api.URL, tokenFile: token}}).call(context.Background(), tc.name, args); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectToolsRejectMissingOrUnsafeArgumentsBeforeHTTP(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"download_script", map[string]any{}},
		{"download_script", map[string]any{"managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "workflowId": "flow-one"}},
		{"get_api_collection", map[string]any{"collectionId": "../escape"}},
		{"update_api_collection_settings", map[string]any{"collectionId": "api-one", "revision": 0}},
		{"create_api_request", map[string]any{"collectionId": "api-one", "revision": 1, "path": "/orders", "method": "SHELL", "operation": map[string]any{}}},
		{"import_api_collection", map[string]any{"format": "executable", "content": "run me"}},
		{"set_api_environments", map[string]any{"collectionId": "api-one", "revision": 1, "environments": "{}"}},
		{"get_cluster_status", map[string]any{"projectId": "../escape"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.args["projectId"] == nil {
				tc.args["projectId"] = "project-one"
			}
			if _, err := (managerTools{}).call(context.Background(), tc.name, tc.args); err == nil {
				t.Fatalf("accepted invalid arguments %v", tc.args)
			}
		})
	}
}

func TestMCPCollectionDocumentDoesNotRoundJSONNumbers(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	const exact = "900719925474099312345"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), exact) {
			t.Errorf("document integer changed: %s", raw)
		}
		io.WriteString(w, `{"id":"api-one","document":{"example":`+exact+`}}`)
	}))
	defer api.Close()
	client := scopedProxyFixture(t, t.TempDir(), managerClient{baseURL: api.URL, tokenFile: token})
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_api_collection", Arguments: json.RawMessage(`{"projectId":"project-one","name":"整数保真","document":{"openapi":"3.1.0","example":` + exact + `}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool failed %v", result.Content)
	}
	if !strings.Contains(result.Content[0].(*mcp.TextContent).Text, exact) {
		t.Fatalf("response integer changed %v", result.Content)
	}
}
