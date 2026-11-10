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

func parityToolsFixture(t *testing.T, handle func(http.ResponseWriter, *http.Request)) *managerTools {
	t.Helper()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("test-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Project-ID") != "project-one" || r.Header.Get("Authorization") != "Bearer test-credential" {
			t.Error("project/auth scope missing")
		}
		handle(w, r)
	}))
	t.Cleanup(api.Close)
	return &managerTools{api: managerClient{baseURL: api.URL, tokenFile: token, client: api.Client()}}
}
func TestWorkflowToolsExactScopedRoutesAndBodies(t *testing.T) {
	graph := map[string]any{"nodes": []any{}, "edges": []any{}, "custom": json.Number("9007199254740993")}
	for _, tc := range []struct {
		name, method, path string
		args, want         map[string]any
		xml                bool
	}{
		{"list_workflows", "GET", "/api/workflows?limit=200&offset=0&view=summary", nil, nil, false},
		{"create_workflow", "POST", "/api/workflows", map[string]any{"name": "flow", "description": "", "graph": graph}, map[string]any{"name": "flow", "description": "", "graph": graph}, false},
		{"get_workflow", "GET", "/api/workflows/workflow-one", map[string]any{"workflowId": "workflow-one"}, nil, false},
		{"update_workflow", "PUT", "/api/workflows/workflow-one", map[string]any{"workflowId": "workflow-one", "revision": json.Number("4"), "name": "flow", "graph": graph, "description": ""}, map[string]any{"revision": json.Number("4"), "name": "flow", "graph": graph, "description": ""}, false},
		{"delete_workflow", "DELETE", "/api/workflows/workflow-one", map[string]any{"workflowId": "workflow-one"}, nil, false},
		{"import_workflow", "POST", "/api/workflows/import", map[string]any{"name": "imported", "content": "<jmeterTestPlan/>", "description": ""}, map[string]any{"name": "imported", "content": "<jmeterTestPlan/>", "description": ""}, false},
		{"preview_workflow", "POST", "/api/workflows/preview", map[string]any{"graph": graph}, map[string]any{"graph": graph}, true},
		{"publish_workflow", "POST", "/api/workflows/workflow-one/publish", map[string]any{"workflowId": "workflow-one", "revision": json.Number("4")}, map[string]any{"revision": json.Number("4")}, false},
		{"list_workflow_versions", "GET", "/api/workflows/workflow-one/versions?limit=200&offset=0&view=summary", map[string]any{"workflowId": "workflow-one"}, nil, false},
		{"get_workflow_version", "GET", "/api/workflows/workflow-one/versions/4", map[string]any{"workflowId": "workflow-one", "revision": json.Number("4")}, nil, false},
		{"list_workflow_plans", "GET", "/api/workflows/workflow-one/plans", map[string]any{"workflowId": "workflow-one"}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if r.ContentLength > 0 {
					raw, _ := io.ReadAll(r.Body)
					if decodeJSON(raw, &body) != nil {
						t.Error("invalid JSON body")
					}
				}
				if !reflect.DeepEqual(body, tc.want) {
					t.Errorf("body mismatch: %#v != %#v", body, tc.want)
				}
				if tc.xml {
					w.Header().Set("Content-Type", "application/xml")
					io.WriteString(w, "<jmeterTestPlan>保留XML</jmeterTestPlan>")
				} else {
					io.WriteString(w, `{"id":"workflow-one","revision":4}`)
				}
			})
			args := map[string]any{"projectId": "project-one"}
			for k, v := range tc.args {
				args[k] = v
			}
			result, err := m.workflowTool(context.Background(), tc.name, args)
			if err != nil || calls != 1 {
				t.Fatalf("request failed: calls=%d err=%v", calls, err)
			}
			if tc.xml && (result["xml"] != "<jmeterTestPlan>保留XML</jmeterTestPlan>" || result["project_id"] != "project-one") {
				t.Fatal("preview XML/scope changed")
			}
		})
	}
}
func TestWorkflowSchemasRejectInvalidBeforeManagerHTTP(t *testing.T) {
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_workflows", map[string]any{}},
		{"get_workflow", map[string]any{"projectId": "project-one", "workflowId": "../foreign"}},
		{"create_workflow", map[string]any{"projectId": "project-one", "name": "flow", "graph": []any{}}},
		{"update_workflow", map[string]any{"projectId": "project-one", "workflowId": "workflow-one", "name": "flow", "graph": map[string]any{}}},
		{"publish_workflow", map[string]any{"projectId": "project-one", "workflowId": "workflow-one", "revision": 0}},
		{"get_workflow_version", map[string]any{"projectId": "project-one", "workflowId": "workflow-one", "revision": "1/../../other"}},
		{"import_workflow", map[string]any{"projectId": "project-one", "name": "flow", "content": ""}},
	} {
		_, err := m.call(context.Background(), tc.name, tc.args)
		if err == nil || !strings.HasPrefix(err.Error(), "Invalid arguments for ") {
			t.Fatalf("invalid args not rejected by registered schema for %s: %v", tc.name, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid workflow arguments reached HTTP")
	}
	specs := workflowSpecs()
	if len(specs) != 11 {
		t.Fatal("workflow capability count mismatch")
	}
	for _, s := range specs {
		if s.name == "preview_workflow" && !s.readOnly {
			t.Fatal("preview incorrectly classified as a write")
		}
	}
}

// The catalog is the client-visible bound; checking metadata avoids allocating
// a 40 MiB JSON fixture while still detecting a stale 32 MiB import schema.
func TestWorkflowCapacitySchemaMatchesConfiguredMaximum(t *testing.T) {
	for _, spec := range managerSpecs {
		switch spec.name {
		case "create_workflow", "update_workflow", "import_workflow", "preview_workflow":
			if !strings.Contains(spec.description, "默认32MiB") || !strings.Contains(spec.description, "64MiB") {
				t.Errorf("%s does not explain default and configured maximum: %s", spec.name, spec.description)
			}
		}
		if spec.name == "import_workflow" || spec.name == "upload_script" || spec.name == "upload_dataset" {
			content := spec.schema["properties"].(map[string]any)["content"].(map[string]any)
			maximum := 5 * 1024 * 1024
			if spec.name == "import_workflow" {
				maximum = 64 * 1024 * 1024
			}
			if content["maxLength"] != maximum {
				t.Errorf("%s content boundary = %v, want %d", spec.name, content["maxLength"], maximum)
			}
		}
	}
}
