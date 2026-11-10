package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestDatasetCSVValidation(t *testing.T) {
	cols, rows, err := inspectDatasetCSV(strings.NewReader("id,name\n1,abc\n2,def\n"), ',')
	if err != nil || rows != 2 || strings.Join(cols, ",") != "id,name" {
		t.Fatalf("%v %v %v", cols, rows, err)
	}
	for _, data := range []string{"id,id\n1,2\n", "id,name\n1\n", "id\n\xff\n", "id\n"} {
		if _, _, err := inspectDatasetCSV(strings.NewReader(data), ','); err == nil {
			t.Fatalf("accepted invalid CSV %q", data)
		}
	}
}
func TestProducerNamesExcludeAutomaticEnvironment(t *testing.T) {
	graph := Obj{"environment_variables": []any{"token", "customer", "static"}, "nodes": []any{
		Obj{"id": "json", "properties": Obj{"component": "JSONPostProcessor", "config": Obj{"referenceName": "token"}}},
		Obj{"id": "csv", "properties": Obj{"component": "CSVDataSet", "config": Obj{"variableNames": "customer"}}},
	}}
	names, e := environmentVariableNames(graph)
	if e != nil || strings.Join(names, ",") != "static" {
		t.Fatalf("%v %v", names, e)
	}
}
func TestDatasetGenerationDeterministic(t *testing.T) {
	body := Obj{"rows": 2, "columns": []any{Obj{"name": "id", "generator": "sequence", "start": 5, "prefix": "x"}, Obj{"name": "name", "generator": "constant", "value": "hello,world"}}}
	var output bytes.Buffer
	e := generateDatasetCSVTo(context.Background(), body, ',', &output, 1024)
	got := output.Bytes()
	if e != nil || string(got) != "id,name\nx5,\"hello,world\"\nx6,\"hello,world\"\n" {
		t.Fatalf("%q %v", got, e)
	}
}

func TestDatasetSequenceValidatesWholeRequestedRangeBeforePreview(t *testing.T) {
	for _, tc := range []struct {
		name    string
		start   any
		rows    int
		preview int64
	}{
		{"overflow", int64(9223372036854775807), 2, 100},
		{"overflow beyond preview", int64(9223372036854775797), 100, 10},
		{"text start", "5", 2, 100}, {"fraction start", json.Number("1.5"), 2, 100}, {"null start", nil, 2, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			err := generateDatasetCSVTo(context.Background(), Obj{"rows": tc.rows, "columns": []any{Obj{"name": "id", "generator": "sequence", "start": tc.start}}}, ',', &output, 4096, tc.preview)
			status, code, _ := requestFailure(err)
			if err == nil || status != 400 || code != "invalid_generation" || output.Len() != 0 {
				t.Fatalf("invalid sequence was written: err=%v output=%q", err, output.String())
			}
		})
	}
	for _, start := range []int64{-9223372036854775808, 9223372036854775806} {
		var output bytes.Buffer
		if err := generateDatasetCSVTo(context.Background(), Obj{"rows": 2, "columns": []any{Obj{"name": "id", "generator": "sequence", "start": start}}}, ',', &output, 4096); err != nil {
			t.Fatal("valid boundary sequence rejected", start, err)
		}
	}
}
func TestDatasetCSVBOMCRLFQuotedAndInvalidUTF8(t *testing.T) {
	input := "\xef\xbb\xbf\"id\",name\r\n1,\"a,b\"\r\n2,\"line\r\nb\"\r\n"
	columns, rows, e := inspectDatasetCSV(strings.NewReader(input), ',')
	if e != nil || rows != 2 || strings.Join(columns, ",") != "id,name" {
		t.Fatal(columns, rows, e)
	}
	if _, _, e = inspectDatasetCSV(strings.NewReader("id,name\r\n1,\xff\r\n"), ','); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
}
func TestManagedCSVMarkersRoundTripAndProducerPriority(t *testing.T) {
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"token", "row_id"}
	csv := graphNode("csv-node", "CSVDataSet", Obj{"filename": "", "variableNames": "row_id", "delimiter": ",", "ignoreFirstLine": false, "recycle": true, "stopThread": false})
	obj(csv["properties"])["dataset_columns"] = []any{"id"}
	extract := graphNode("extract", "JSONPostProcessor", Obj{"referenceName": "token", "jsonPathExpr": "$.token", "defaultValue": "missing"})
	header := graphNode("header", "HeaderManager", Obj{"headers": []any{Obj{"name": "Authorization", "value": "Bearer ${token}; ${row_id}"}}})
	second := graphNode("http-next", "HTTPSamplerProxy", nil)
	g["nodes"] = append(arr(g["nodes"]), csv, extract, header, second)
	g["edges"] = append(arr(g["edges"]), graphEdge("group", "csv-node", 1), graphEdge("http", "extract", 0), graphEdge("http-next", "header", 0), graphEdge("group", "http-next", 2))
	props := obj(second["properties"])
	props["api_reference"] = Obj{"collection_id": "collection-one", "revision": 1, "path": "/ping", "method": "get"}
	props["api_environment_variables"] = []any{"token", "row_id"}
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{`name="jmeter.platform.node_id">csv-node`, `name="quotedData">true`, `name="fileEncoding">UTF-8`, `name="ignoreFirstLine">true`, `name="variableNames">row_id`, `Bearer ${token}; ${row_id}`} {
		if !strings.Contains(content, want) {
			t.Fatal("missing", want)
		}
	}
	meta, e := workflowDatasetMetadata(content)
	if e != nil || len(meta) != 1 {
		t.Fatal(meta, e)
	}
	if requires, names := workflowEnvironmentMetadata(content); !requires || len(names) != 0 {
		t.Fatal("producer became required env", names)
	}
	graph, e := importWorkflowGraph(content)
	if e != nil || str(findGraphNode(graph, "CSVDataSet")["id"]) != "csv-node" || strings.Join(stringList(obj(findGraphNode(graph, "CSVDataSet")["properties"])["dataset_columns"]), ",") != "id" {
		t.Fatal("stable CSV marker lost", e)
	}
}
func TestEnvironmentCannotInjectPlatformRuntimeProperties(t *testing.T) {
	for _, key := range []string{"remote_hosts", "system.properties", "jmeterengine.stopfail.system.exit", "beanshell.server.port", "search_paths", "plugin_dependency_paths", "user.classpath", "engine_id", "server_port", "jmeter.env.base_host"} {
		body := Obj{"name": "test", "base_host": "http://fixture", "variables": []any{Obj{"name": key, "value": "bad"}}}
		if _, e := validateEnvironment(body); e == nil {
			t.Fatal("reserved env accepted", key)
		}
		if _, _, e := environmentSnapshot(body); e == nil {
			t.Fatal("historical reserved env injected", key)
		}
	}
}
func TestProducerVisibilityRespectsThreadGroupsAndRequestOrder(t *testing.T) {
	g := basicWorkflowGraph()
	g["environment_binding"] = "base_host"
	g["environment_variables"] = []any{"token", "id"}
	first := findGraphNode(g, "HTTPSamplerProxy")
	obj(first["properties"])["config"] = Obj{"method": "GET", "protocol": "http", "domain": "", "port": "", "path": "/login/${token}", "bodyJson": ""}
	extract := graphNode("extract-token", "JSONPostProcessor", Obj{"referenceName": "token", "jsonPathExpr": "$.token", "defaultValue": "missing"})
	next := graphNode("next", "HTTPSamplerProxy", Obj{"method": "GET", "protocol": "http", "domain": "", "port": "", "path": "/next/${token}/${id}", "bodyJson": ""})
	csvA := graphNode("csv-a", "CSVDataSet", Obj{"filename": "", "variableNames": "id", "delimiter": ","})
	groupB := graphNode("group-b", "ThreadGroup", nil)
	static := graphNode("static", "HTTPSamplerProxy", Obj{"method": "GET", "protocol": "http", "domain": "", "port": "", "path": "/static/${token}/${id}", "bodyJson": ""})
	csvB := graphNode("csv-b", "CSVDataSet", Obj{"filename": "", "variableNames": "id", "delimiter": ","})
	g["nodes"] = append(arr(g["nodes"]), extract, next, csvA, groupB, static, csvB)
	g["edges"] = append(arr(g["edges"]), graphEdge("http", "extract-token", 0), graphEdge("group", "next", 1), graphEdge("group", "csv-a", 2), graphEdge("root", "group-b", 1), graphEdge("group-b", "static", 0), graphEdge("group-b", "csv-b", 1))
	for _, n := range []Obj{first, next, static} {
		props := obj(n["properties"])
		props["api_reference"] = Obj{"collection_id": "collection-one", "revision": 1, "path": "/ping", "method": "get"}
		props["api_environment_variables"] = []any{"token", "id"}
	}
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	bindings, e := workflowCollectionMetadata(content)
	if e != nil {
		t.Fatal(e)
	}
	names := map[string][]string{}
	for _, v := range bindings {
		m := obj(v)
		names[str(m["node_id"])] = stringList(m["names"])
	}
	if strings.Join(names["http"], ",") != "token" {
		t.Fatal("login request lost initial env token", names)
	}
	if len(names["next"]) != 0 {
		t.Fatal("following request overrode dynamic token/id", names)
	}
	if strings.Join(names["static"], ",") != "token" {
		t.Fatal("other group lost static token or rewrote CSV id", names)
	}
	if required, vars := workflowEnvironmentMetadata(content); !required || len(vars) != 0 {
		t.Fatal("collection-only token forced global requirement", vars)
	}
	delete(obj(static["properties"]), "api_reference")
	content, e = compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	_, vars := workflowEnvironmentMetadata(content)
	if strings.Join(vars, ",") != "token" {
		t.Fatal("other threadgroup static token requirement lost", vars)
	}
}
