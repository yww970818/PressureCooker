package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthoringBoundaryRejectsTraversalAndSymlinkImports(t *testing.T) {
	root := t.TempDir()
	plans := filepath.Join(root, "plans")
	if err := os.Mkdir(plans, 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(plans, "safe.jmx")
	outside := filepath.Join(root, "credential")
	for _, path := range []string{inside, outside} {
		if err := os.WriteFile(path, []byte("<jmeterTestPlan/>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(plans, "escape.jmx")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{{"planId": "../../credential"}, {"planId": 7}, {"filePath": outside}, {"filePath": link}, {"filePath": plans}} {
		name := "get_test_plan"
		if _, ok := args["filePath"]; ok {
			name = "import_test_plan"
		}
		if err := validateAuthoring(name, args, []string{plans}); err == nil {
			t.Fatalf("accepted unsafe %s arguments %v", name, args)
		}
	}
	if err := validateAuthoring("get_test_plan", map[string]any{"planId": "plan_abcdef01"}, []string{plans}); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthoring("import_test_plan", map[string]any{"filePath": inside}, []string{plans}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalExecutionAndUnknownUpstreamToolsAreNeverProxied(t *testing.T) {
	for _, name := range []string{"execute_test_plan", "stop_execution", "get_execution_report", "find_breaking_point", "add_backend_listener", "add_aggregate_report_listener", "add_summary_report_listener", "add_view_results_tree_listener", "new_future_execution_tool"} {
		if allowedAuthoring(name) {
			t.Fatalf("unsafe upstream tool %q allowed", name)
		}
	}
	for _, name := range []string{"create_test_plan", "list_test_plans", "add_thread_group", "get_test_plan_xml", "import_test_plan", "update_element"} {
		if !allowedAuthoring(name) {
			t.Fatalf("authoring tool %q omitted", name)
		}
	}
}

func TestHTTPBearerAndBodyLimit(t *testing.T) {
	handler := secureHandler("secret-test-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "too large", 413)
			return
		}
		w.Write(body)
	}))
	for _, auth := range []string{"", "Bearer wrong", "secret-test-token"} {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.Header.Set("Authorization", auth)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != 401 || strings.Contains(res.Body.String(), "secret-test-token") {
			t.Fatalf("authentication response %d %s", res.Code, res.Body)
		}
	}
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(strings.Repeat("x", 34*1024*1024)))
	req.Header.Set("Authorization", "Bearer secret-test-token")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != 413 {
		t.Fatalf("oversize request accepted: %d", res.Code)
	}
}

func TestManagerRequestUsesTokenAndIdempotencyWithoutResponseSecretLeak(t *testing.T) {
	root := t.TempDir()
	tokenFile := filepath.Join(root, "token")
	if err := os.WriteFile(tokenFile, []byte(" private-test-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var seen map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-token" || r.Header.Get("Idempotency-Key") != "stable-one" {
			t.Errorf("missing credential or idempotency header")
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Error(err)
		}
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"private-test-token"}`)
	}))
	defer api.Close()
	apiClient := managerClient{baseURL: api.URL, tokenFile: tokenFile, client: api.Client()}
	_, err := apiClient.requestProject(context.Background(), http.MethodPost, "/api/runs", map[string]any{"name": "internal"}, "stable-one", "project-one")
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "private-test-token") {
		t.Fatalf("unsanitized error: %v", err)
	}
	if seen["name"] != "internal" {
		t.Fatalf("wrong API payload: %v", seen)
	}
}
