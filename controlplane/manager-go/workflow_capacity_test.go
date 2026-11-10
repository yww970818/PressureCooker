package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func capacityGraph(nodes int) Obj {
	g := basicWorkflowGraph()
	for i := 3; i < nodes; i++ {
		id := fmt.Sprintf("large-%d", i)
		g["nodes"] = append(arr(g["nodes"]), graphNode(id, "HTTPSamplerProxy", nil))
		g["edges"] = append(arr(g["edges"]), graphEdge("group", id, i-2))
	}
	return g
}
func TestWorkflowCapacityDefaultsAndConfiguredBoundaries(t *testing.T) {
	if _, e := validateWorkflowGraph(capacityGraph(1511)); e != nil {
		t.Fatal("full-system graph rejected", e)
	}
	if _, e := validateWorkflowGraph(capacityGraph(5001)); e == nil {
		t.Fatal("unbounded workflow")
	}
	t.Setenv("JMETER_WORKFLOW_MAX_NODES", "1200")
	if _, e := validateWorkflowGraph(capacityGraph(1200)); e != nil {
		t.Fatal(e)
	}
	if _, e := validateWorkflowGraph(capacityGraph(1201)); e == nil {
		t.Fatal("ignored configured node limit")
	}
	t.Setenv("JMETER_WORKFLOW_MAX_EDGES", "2")
	if _, e := validateWorkflowGraph(capacityGraph(4)); e == nil {
		t.Fatal("ignored configured edge limit")
	}
}
func TestWorkflowJSONCapacityDoesNotExpandOtherAPIs(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", 7*1024*1024) + `"}`
	request := func(path string) error {
		_, e := readJSON(httptest.NewRequest("POST", path, strings.NewReader(body)))
		return e
	}
	if e := request("/api/workflows"); e != nil {
		t.Fatal("large workflow body rejected", e)
	}
	if e := request("/api/workflows/import"); e != nil {
		t.Fatal("large JMX import rejected", e)
	}
	if e := request("/api/api-collections"); e == nil {
		t.Fatal("expanded unrelated API request limit")
	}
	if e := request("/api/workflows-other"); e == nil {
		t.Fatal("path-prefix limit bypass")
	}
	t.Setenv("JMETER_WORKFLOW_BODY_MIB", "6")
	if e := request("/api/workflows"); e == nil {
		t.Fatal("ignored configured body limit")
	}
}
func TestWorkflowCapacityRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []string{"0", "-1", "not-a-number", "100001"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("JMETER_WORKFLOW_MAX_NODES", value)
			if _, e := workflowCapacity(); e == nil {
				t.Fatal("invalid node limit accepted")
			}
		})
	}
}

func TestWorkflowCapacityImportsJMXBeyondOldWorkspaceLimit(t *testing.T) {
	raw, e := compileWorkflow(basicWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	raw = strings.Replace(raw, "<hashTree>", "<!--"+strings.Repeat("x", 6*1024*1024)+"--><hashTree>", 1)
	if _, e = importWorkflowGraph(raw); e != nil {
		t.Fatal("workflow source still capped at old 5MiB workspace limit", e)
	}
}
