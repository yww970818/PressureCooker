package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
)

var updateMCPFixture = flag.Bool("update-mcp-fixture", false, "refresh the frontend fixture from the real Go/official Node tool table")

func TestLiveToolCatalogMatchesFrontendFixture(t *testing.T) {
	client := scopedProxyFixture(t, t.TempDir(), managerClient{})
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	const path = "../frontend/test/fixtures/mcp-tools.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	toolsRaw, err := json.Marshal(listed.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(toolsRaw), "execute_test_plan") {
		t.Fatal("tool catalog still refers to retired execution tool")
	}
	var actual []any
	if err = json.Unmarshal(toolsRaw, &actual); err != nil {
		t.Fatal(err)
	}
	if *updateMCPFixture {
		fixture["tools"] = actual
		fixture["total"] = float64(len(actual))
		fixture["server"] = map[string]any{"name": "jmeter-mcp-server", "version": mcpServerVersion}
		raw, err = json.MarshalIndent(fixture, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(fixture["tools"], actual) || int(fixture["total"].(float64)) != len(actual) {
		t.Fatalf("frontend fixture differs from the actual %d-tool table; regenerate with -update-mcp-fixture", len(actual))
	}
}
