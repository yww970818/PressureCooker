package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func scopedProxyFixture(t *testing.T, workspace string, api managerClient) *mcp.ClientSession {
	t.Helper()
	if api.baseURL == "" {
		// Authoring tests still cross the real manager HTTP boundary. Only its
		// external storage dependency is represented by an active-project peer.
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/projects/") {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/api/projects/"), "archived": false})
		}))
		t.Cleanup(peer.Close)
		token := filepath.Join(t.TempDir(), "manager-token")
		if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
			t.Fatal(err)
		}
		api = managerClient{baseURL: peer.URL, tokenFile: token}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	script, err := filepath.Abs("../mcp/node_modules/jmeter-mcp-server/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(script); err != nil {
		t.Skip("pinned upstream unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	upstream, err := connectUpstream(ctx, node, script, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upstream.Close() })
	proxy, err := newProxyServer(ctx, upstream, api, []string{workspace})
	if err != nil {
		t.Fatal(err)
	}
	ct, st := mcp.NewInMemoryTransports()
	ss, err := proxy.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "project-tests", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callProjectTool(t *testing.T, client *mcp.ClientSession, name string, args map[string]any, wantError bool) any {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("%s result error=%v; want %v: %#v", name, result.IsError, wantError, result.Content)
	}
	if wantError {
		return nil
	}
	var value any
	if err = json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestUpstreamDraftsCannotLeakAcrossProjectListReadEditOrPublication(t *testing.T) {
	workspace := t.TempDir()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("token"), 0600); err != nil {
		t.Fatal(err)
	}
	publications := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/projects/") {
			project := strings.TrimPrefix(r.URL.Path, "/api/projects/")
			if r.Header.Get("X-Project-ID") != project {
				t.Error("unscoped project check")
			}
			json.NewEncoder(w).Encode(map[string]any{"id": project, "archived": false})
			return
		}
		publications++
		if r.Header.Get("X-Project-ID") != "project-one" {
			t.Error("unscoped publish")
		}
		io.WriteString(w, `{"id":"plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	}))
	defer api.Close()
	client := scopedProxyFixture(t, workspace, managerClient{baseURL: api.URL, tokenFile: token})
	list, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name == "execute_test_plan" {
			t.Fatal("obsolete direct execute advertised")
		}
		if !allowedAuthoring(tool.Name) {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		var schema map[string]any
		json.Unmarshal(raw, &schema)
		if schema["properties"].(map[string]any)["projectId"] == nil {
			t.Fatalf("%s missing project schema", tool.Name)
		}
	}
	callProjectTool(t, client, "create_test_plan", map[string]any{"name": "no project"}, true)
	plan := callProjectTool(t, client, "create_test_plan", map[string]any{"projectId": "project-one", "name": "项目一私有草稿"}, false).(map[string]any)
	planID := plan["planId"]
	other := callProjectTool(t, client, "list_test_plans", map[string]any{"projectId": "project-two"}, false).([]any)
	if len(other) != 0 {
		t.Fatalf("cross-project list leaked %v", other)
	}
	own := callProjectTool(t, client, "list_test_plans", map[string]any{"projectId": "project-one"}, false).([]any)
	if len(own) != 1 || own[0].(map[string]any)["planId"] != planID {
		t.Fatalf("own draft missing %v", own)
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_test_plan", map[string]any{}}, {"get_test_plan_xml", map[string]any{}},
		{"rename_element", map[string]any{"nodeId": plan["rootNodeId"], "name": "串项目修改"}},
		{"publish_test_plan", map[string]any{}},
	} {
		tc.args["projectId"] = "project-two"
		tc.args["planId"] = planID
		callProjectTool(t, client, tc.name, tc.args, true)
	}
	if publications != 0 {
		t.Fatal("cross-project publish reached manager")
	}
	callProjectTool(t, client, "publish_test_plan", map[string]any{"projectId": "project-one", "planId": planID}, false)
	if publications != 1 {
		t.Fatal("same-project publish missing")
	}
	client.Close()
	restarted := scopedProxyFixture(t, workspace, managerClient{})
	callProjectTool(t, restarted, "get_test_plan", map[string]any{"projectId": "project-two", "planId": planID}, true)
	read := callProjectTool(t, restarted, "get_test_plan", map[string]any{"projectId": "project-one", "planId": planID}, false).(map[string]any)
	if read["name"] != "项目一私有草稿" {
		t.Fatalf("restart lost exact owner/draft %v", read)
	}
}

func TestImportUsesOnlyProjectOwnedStagingDirectory(t *testing.T) {
	workspace := t.TempDir()
	client := scopedProxyFixture(t, workspace, managerClient{})
	plan := callProjectTool(t, client, "create_test_plan", map[string]any{"projectId": "project-one", "name": "上传源文件"}, false).(map[string]any)
	xml := callProjectTool(t, client, "get_test_plan_xml", map[string]any{"projectId": "project-one", "planId": plan["planId"]}, false).(map[string]any)["xml"].(string)
	owned := filepath.Join(workspace, "imports", "project-one", "owned.jmx")
	if err := os.MkdirAll(filepath.Dir(owned), 0700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(workspace, "shared.jmx")
	for _, path := range []string{owned, shared} {
		if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
			t.Fatal(err)
		}
	}
	callProjectTool(t, client, "import_test_plan", map[string]any{"projectId": "project-two", "filePath": owned}, true)
	callProjectTool(t, client, "import_test_plan", map[string]any{"projectId": "project-two", "filePath": shared}, true)
	alias := filepath.Join(workspace, "imports", "project-two")
	if err := os.Symlink(filepath.Dir(owned), alias); err != nil {
		t.Fatal(err)
	}
	callProjectTool(t, client, "import_test_plan", map[string]any{"projectId": "project-two", "filePath": filepath.Join(alias, "owned.jmx")}, true)
	imported := callProjectTool(t, client, "import_test_plan", map[string]any{"projectId": "project-one", "filePath": owned}, false).(map[string]any)
	callProjectTool(t, client, "get_test_plan", map[string]any{"projectId": "project-two", "planId": imported["planId"]}, true)
	callProjectTool(t, client, "get_test_plan", map[string]any{"projectId": "project-one", "planId": imported["planId"]}, false)
}

func TestLegacyDraftMigrationAndUnindexedDraftsFailClosed(t *testing.T) {
	workspace := t.TempDir()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable")
	}
	script, err := filepath.Abs("../mcp/node_modules/jmeter-mcp-server/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(script); err != nil {
		t.Skip("pinned upstream unavailable")
	}
	createRaw := func(name string) string {
		// Each authoring process gets its own startup/operation budget; project
		// migration work between the two lifetimes must not expire the next one.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		upstream, err := connectUpstream(ctx, node, script, workspace)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := upstreamJSON(ctx, upstream, "create_test_plan", map[string]any{"name": name})
		upstream.Close()
		if err != nil {
			t.Fatal(err)
		}
		return plan["planId"].(string)
	}
	legacy := createRaw("升级前草稿")
	first := scopedProxyFixture(t, workspace, managerClient{})
	callProjectTool(t, first, "get_test_plan", map[string]any{"projectId": "project-default", "planId": legacy}, false)
	callProjectTool(t, first, "get_test_plan", map[string]any{"projectId": "project-one", "planId": legacy}, true)
	first.Close()
	orphan := createRaw("创建后未登记的孤立草稿")
	second := scopedProxyFixture(t, workspace, managerClient{})
	callProjectTool(t, second, "get_test_plan", map[string]any{"projectId": "project-default", "planId": orphan}, true)
	list := callProjectTool(t, second, "list_test_plans", map[string]any{"projectId": "project-default"}, false).([]any)
	if len(list) != 1 || list[0].(map[string]any)["planId"] != legacy {
		t.Fatalf("unindexed draft exposed %v", list)
	}
	info, err := os.Stat(filepath.Join(workspace, "draft-projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("draft index permissions %o", info.Mode().Perm())
	}
}

func TestCorruptDraftProjectIndexCannotFallbackToGlobalDrafts(t *testing.T) {
	for _, content := range []string{`broken`, `{"version":2,"owners":{}}`, `{"version":1,"owners":{"plan_deadbeef":"../escape"}}`} {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "draft-projects.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := openDraftProjects(context.Background(), nil, workspace); err == nil {
			t.Fatal("invalid ownership index accepted")
		}
	}
}
