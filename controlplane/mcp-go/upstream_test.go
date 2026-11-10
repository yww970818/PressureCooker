package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOfficialUpstreamAuthorsDraftAndShutsDown(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node runtime unavailable")
	}
	script, err := filepath.Abs("../mcp/node_modules/jmeter-mcp-server/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(script); err != nil {
		t.Skip("install the pinned upstream npm dependency to run the integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := connectUpstream(ctx, node, script, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if session == nil {
		t.Fatal("upstream connection missing")
	}
	plan, err := upstreamJSON(ctx, session, "create_test_plan", map[string]any{"name": "Go integration"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plan["planId"].(string), "plan_") {
		t.Fatalf("invalid draft %v", plan)
	}
	xml, err := upstreamJSON(ctx, session, "get_test_plan_xml", map[string]any{"planId": plan["planId"]})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(xml["xml"].(string), "Go integration") {
		t.Fatalf("missing draft in XML: %v", xml)
	}
	var names []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if allowedAuthoring(tool.Name) {
			names = append(names, tool.Name)
		}
	}
	if len(names) < 40 {
		t.Fatalf("authoring tools missing: %v", names)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { session.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stdio child did not shut down")
	}
}

func TestProxyManagerSchemaValidationThroughMCP(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	managerTools{}.register(server)
	ct, st := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "submit_console_task", Arguments: map[string]any{"clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "projectId": "project-one", "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "name": "missing key"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("MCP accepted a missing idempotency key")
	}
}
