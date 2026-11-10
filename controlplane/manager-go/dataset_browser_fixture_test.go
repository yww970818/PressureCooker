package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in browser fixture for dataset generation, preview, and immutable edits.
// mysqlFixture isolates its tables and object store; no scheduler or execution
// loop is started, and the supplied graph contains no business HTTP sampler.
func TestDatasetOnlineBrowserFixture(t *testing.T) {
	if os.Getenv("JMETER_DATASET_BROWSER") != "1" {
		t.Skip("set JMETER_DATASET_BROWSER=1 for the local dataset browser fixture")
	}
	record := os.Getenv("JMETER_DATASET_BROWSER_RECORD")
	if !filepath.IsAbs(record) {
		t.Fatal("JMETER_DATASET_BROWSER_RECORD must be an absolute directory")
	}
	data, err := os.ReadFile(filepath.Join(record, "fixture-graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var graph Obj
	if err = json.Unmarshal(data, &graph); err != nil {
		t.Fatalf("read dataset fixture graph: %v", err)
	}
	if graph == nil {
		t.Fatal("dataset fixture graph must be an object")
	}

	// The browser-specific opt-in is sufficient to enable the shared isolated
	// MySQL fixture. Its ordinary provisioned MySQL configuration still applies.
	t.Setenv("JMETER_GO_INTEGRATION", "1")
	p, _ := mysqlFixture(t)
	p.Config.FrontendDir = filepath.Join("..", "frontend", "dist")
	const workflowName = "数据集新增功能验收"
	workflow, err := p.workspaceCreate("workflows", Obj{"name": workflowName, "graph": graph})
	if err != nil {
		t.Fatal(err)
	}
	stopPath := filepath.Join(record, "local-server.stop")
	if err = os.Remove(stopPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	server := httptest.NewServer(Handler{p})
	defer server.Close()

	tokenPath := filepath.Join(record, "local-token")
	tokenFile, err := os.OpenFile(tokenPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = tokenFile.Chmod(0600); err != nil {
		tokenFile.Close()
		t.Fatal(err)
	}
	if _, err = tokenFile.WriteString(p.Config.AdminToken); err != nil {
		tokenFile.Close()
		t.Fatal(err)
	}
	if err = tokenFile.Close(); err != nil {
		t.Fatal(err)
	}
	ready := Obj{
		"base_url":      server.URL,
		"project_id":    defaultProjectID,
		"project_name":  "默认项目",
		"workflow_id":   workflow["id"],
		"node_id":       "dataset-input",
		"workflow_name": workflowName,
	}
	readyPath := filepath.Join(record, "local-server.json")
	if err = os.WriteFile(readyPath+".tmp", encoded(ready), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(readyPath+".tmp", readyPath); err != nil {
		t.Fatal(err)
	}
	t.Log("dataset browser fixture ready")

	deadline := time.NewTimer(10 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			if _, err = os.Stat(stopPath); err == nil {
				return
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
	}
}
