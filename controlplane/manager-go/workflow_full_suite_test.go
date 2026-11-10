package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Build the capacity fixture from platform components, so compilation and
// publication never depend on a customer's scripts or business generators.
func syntheticEditableSuite() Obj {
	graph := Obj{"nodes": []any{graphNode("root", "TestPlan", nil)}, "edges": []any{}}
	add := func(id, component, parent string, order int, config Obj) {
		graph["nodes"] = append(arr(graph["nodes"]), graphNode(id, component, config))
		graph["edges"] = append(arr(graph["edges"]), graphEdge(parent, id, order))
	}
	payload := strings.Repeat("x", 4096)
	for group := 0; group < 31; group++ {
		groupID := fmt.Sprintf("group-%02d", group)
		add(groupID, "ThreadGroup", "root", group, nil)
		for request := 0; request < 47; request++ {
			id := fmt.Sprintf("request-%02d-%02d", group, request)
			add(id, "HTTPSamplerProxy", groupID, request, Obj{"method": "POST", "protocol": "https", "domain": "example.invalid", "path": "/echo", "bodyJson": fmt.Sprintf(`{"record":%q,"payload":%q}`, id, payload)})
		}
	}
	for csv := 0; csv < 33; csv++ {
		add(fmt.Sprintf("csv-%02d", csv), "CSVDataSet", fmt.Sprintf("group-%02d", csv%31), 47+csv/31, Obj{"filename": "", "variableNames": fmt.Sprintf("case_%02d,payload_%02d", csv, csv)})
	}
	return Obj{"name": "Synthetic editable capacity suite", "graph": graph}
}

func TestWorkflowCapacityCompilesFullEditableSuite(t *testing.T) {
	document := syntheticEditableSuite()
	graph := obj(document["graph"])
	tree, e := validateWorkflowGraph(graph)
	if e != nil {
		t.Fatal(e)
	}
	xml, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	if len(tree.Nodes) < 1500 || len(xml) < 5*1024*1024 || len(xml) > workflowByteLimit() {
		t.Fatal("missing capacity coverage or excessive compiled size", len(tree.Nodes), len(xml))
	}
	if strings.Count(xml, `testclass="ThreadGroup"`) != 31 {
		t.Fatal("lost parallel thread groups")
	}
	if strings.Count(xml, datasetNodeMarker) != 33 {
		t.Fatal("lost per-node CSV bindings")
	}
	if _, e = validateWorkspace("workflows", document); e != nil {
		t.Fatal("large workflow persistence rejected", e)
	}
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	p := &Platform{Config: Config{PlansDir: dir}}
	if e = os.Mkdir(filepath.Join(dir, "snapshots"), 0755); e != nil {
		t.Fatal(e)
	}
	if _, e = p.preparePlanWithLimit(Obj{"name": "full-suite", "content": xml}, workflowByteLimit()); e != nil {
		t.Fatal("full workflow cannot publish immutable plan", e)
	}
	if _, e = p.preparePlan(Obj{"name": "direct-upload", "content": xml}); e == nil {
		t.Fatal("changed unrelated direct upload budget")
	}
	t.Logf("synthetic graph %d nodes / %d edges, input %d bytes, JMX %d bytes", len(tree.Nodes), len(arr(graph["edges"])), len(encoded(document)), len(xml))
}
