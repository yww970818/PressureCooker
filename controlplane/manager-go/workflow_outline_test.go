package main

import (
	"errors"
	"testing"
)

func TestWorkflowOutlinePreservesStructureWithoutNodePayloads(t *testing.T) {
	graph := basicWorkflowGraph()
	http := obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])
	http["raw_xml"] = "original XML"
	obj(http["config"])["bodyJson"] = "large request body"
	http["api_reference"] = Obj{"collection_id": "collection", "revision": 2}
	for i, component := range []string{"CSVDataSet", "JSONPostProcessor", "vendor.Plugin"} {
		node := graphNode("extra-"+component, component, Obj{})
		properties := obj(node["properties"])
		properties["raw_xml"] = "original plugin subtree"
		properties["config"] = Obj{"variableNames": "id,token", "delimiter": ";", "referenceName": "result", "script": "large script"}
		properties["dataset_columns"] = []any{"id", "token"}
		graph["nodes"] = append(arr(graph["nodes"]), node)
		graph["edges"] = append(arr(graph["edges"]), graphEdge("group", str(node["id"]), i+1))
	}
	graph["environment_variables"] = []any{"token"}
	value := Obj{"id": "draft", "revision": 7, "source_plan_id": "original-plan", "graph": graph}
	before := string(encoded(value))
	outline := workflowOutline(value)
	if string(encoded(value)) != before {
		t.Fatal("outline mutated persisted source")
	}
	if outline["id"] != value["id"] || outline["revision"] != value["revision"] || outline["source_plan_id"] != value["source_plan_id"] {
		t.Fatal("outline lost revision or provenance")
	}
	shell := obj(outline["graph"])
	if len(arr(shell["nodes"])) != 6 || string(encoded(shell["edges"])) != string(encoded(graph["edges"])) || string(encoded(shell["environment_variables"])) != string(encoded(graph["environment_variables"])) {
		t.Fatal("outline lost graph structure or variable scope")
	}
	for _, item := range arr(shell["nodes"]) {
		properties := obj(obj(item)["properties"])
		if properties["editor_lazy"] != true || properties["raw_xml"] != nil || obj(properties["config"])["script"] != nil || obj(properties["config"])["bodyJson"] != nil {
			t.Fatal("outline contains complete authored parameters")
		}
	}
	if len(obj(obj(findGraphNode(shell, "HTTPSamplerProxy")["properties"])["api_reference"])) != 2 {
		t.Fatal("outline lost API provenance")
	}
	csv := obj(findGraphNode(shell, "CSVDataSet")["properties"])
	if string(encoded(csv["config"])) != string(encoded(Obj{"variableNames": "id,token", "delimiter": ";"})) || len(arr(csv["dataset_columns"])) != 2 {
		t.Fatal("outline lost CSV schema")
	}
	if obj(obj(findGraphNode(shell, "JSONPostProcessor")["properties"])["config"])["referenceName"] != "result" || obj(findGraphNode(shell, "vendor.Plugin")["properties"])["editor_opaque"] != true {
		t.Fatal("outline lost producer or opaque metadata")
	}
}

func TestWorkflowOutlineCannotBeSavedOrCompiled(t *testing.T) {
	value := workflowOutline(Obj{"graph": basicWorkflowGraph()})
	for _, run := range []func() error{
		func() error { _, e := compileWorkflow(obj(value["graph"])); return e },
		func() error {
			_, e := validateWorkspace("workflows", Obj{"name": "partial", "graph": value["graph"]})
			return e
		},
	} {
		e := run()
		var problem *APIError
		if !errors.As(e, &problem) || problem.Code != "incomplete_graph" {
			t.Fatal("partial graph accepted or not identified", e)
		}
	}
}
