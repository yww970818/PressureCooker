package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// These assertions catch treating runtime aliases as physical file columns,
// dropping the BOM, emitting data, or changing the browser's CSV quoting rules.
func TestDatasetTemplateMatchesBrowserHeaderOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		node Obj
		want string
	}{
		{"physical columns", Obj{"columns": []any{"编号", "a;b", "he\"llo"}, "variable_names": []any{"id", "second", "third"}, "delimiter": ";"}, "\uFEFF编号;\"a;b\";\"he\"\"llo\"\r\n"},
		{"runtime fallback", Obj{"columns": []any{}, "variable_names": []any{"user_id", "message"}, "delimiter": "\t"}, "\uFEFFuser_id\tmessage\r\n"},
		{"default delimiter", Obj{"variable_names": []any{" id ", "name"}}, "\uFEFF id ,name\r\n"},
		{"unicode delimiter", Obj{"columns": []string{"a界b", "中文"}, "delimiter": "界"}, "\uFEFF\"a界b\"界中文\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.node["node_name"], tc.node["node_id"] = "用户数据", "csv-a"
			got, err := renderDatasetTemplate(tc.node)
			if err != nil || got["content"] != tc.want || got["filename"] != "用户数据-csv-a-模板.csv" {
				t.Fatalf("template=%v error=%v", got, err)
			}
		})
	}
	got, err := renderDatasetTemplate(Obj{"columns": []any{"id"}, "node_name": "../用户/数据\n", "node_id": "csv\\node"})
	if err != nil || got["filename"] != ".._用户_数据_-csv_node-模板.csv" {
		t.Fatal(got, err)
	}
}

func TestDatasetTemplateRejectsUnsafeAndAmbiguousConfiguration(t *testing.T) {
	cases := []Obj{
		{}, {"columns": []any{"id", "id"}}, {"columns": []any{"id", 1}},
		{"columns": []any{"id", "name"}, "variable_names": []any{"id"}},
		{"columns": []any{"id"}, "delimiter": "||"}, {"columns": []any{"id"}, "delimiter": "\""},
		{"columns": []any{"id"}, "delimiter": "\uFFFD"}, {"columns": []any{"id"}, "delimiter": "\r"},
		{"columns": []any{"id"}, "delimiter": 1},
	}
	for _, column := range []string{"", " \uFEFF", "a\nb", "id,name", "${secret}", "@formula", "\uFEFF +formula", strings.Repeat("中", 67)} {
		cases = append(cases, Obj{"columns": []any{column}})
	}
	many := make([]any, 101)
	for i := range many {
		many[i] = "column"
	}
	cases = append(cases, Obj{"columns": many})
	for i, node := range cases {
		if got, err := renderDatasetTemplate(node); err == nil {
			t.Fatalf("case %d accepted: %v", i, got)
		}
	}
}

func TestDatasetTemplateCurrentGraphAndSavedFilePrecedence(t *testing.T) {
	graph := Obj{"nodes": []any{Obj{"id": "csv-a", "properties": Obj{"component": "CSVDataSet", "name": "当前节点", "enabled": false, "config": Obj{"variableNames": " alias_id , alias_name ", "delimiter": "\t"}}}}}
	saved := []any{Obj{"node_id": "csv-a", "columns": []any{"inferred", "aliases"}, "datasets": []any{Obj{"columns": []any{}}, Obj{"columns": []any{"physical_id", "physical_name"}, "rows": []any{"private-data"}}}}}
	got, err := datasetTemplateNode(graph, "csv-a", saved)
	if err != nil || !reflect.DeepEqual(got["columns"], []any{"physical_id", "physical_name"}) || !reflect.DeepEqual(got["variable_names"], []string{"alias_id", "alias_name"}) || got["delimiter"] != "\t" || got["node_name"] != "当前节点" {
		t.Fatal(got, err)
	}
	props := obj(obj(arr(graph["nodes"])[0])["properties"])
	props["dataset_columns"] = []any{"current_id", "current_name"}
	got, err = datasetTemplateNode(graph, "csv-a", saved)
	if err != nil || !reflect.DeepEqual(got["columns"], props["dataset_columns"]) {
		t.Fatal("current physical schema lost", got, err)
	}
	delete(props, "dataset_columns")
	got, err = datasetTemplateNode(graph, "csv-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	template, err := renderDatasetTemplate(got)
	if err != nil || template["content"] != "\uFEFFalias_id\talias_name\r\n" || strings.Contains(str(template["content"]), "private-data") {
		t.Fatal(template, err)
	}
	if _, err := datasetTemplateNode(graph, "missing", saved); err == nil {
		t.Fatal("missing node accepted")
	}
	props["component"] = "HTTPSamplerProxy"
	if _, err := datasetTemplateNode(graph, "csv-a", saved); err == nil {
		t.Fatal("non-CSV node accepted")
	}
}

func TestDatasetTemplateHTTPProjectScopeSnapshotAndReadOnly(t *testing.T) {
	p, _ := mysqlFixture(t)
	w := managementWorkflow(t, p, "模板流程", "csv-a", true, false)
	var beforeRevision int64
	var beforePayload string
	if err := p.DB.QueryRow("SELECT revision,payload FROM workflows WHERE id=? AND project_id=?", w["id"], p.projectID()).Scan(&beforeRevision, &beforePayload); err != nil {
		t.Fatal(err)
	}
	path := "/api/workflows/" + str(w["id"]) + "/datasets/template"
	request, _ := workspaceHTTP(t, p)
	headers := map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": p.projectID()}
	status, _, raw := request(http.MethodPost, path, Obj{"node_id": "csv-a"}, headers)
	got, err := decode(raw)
	if status != 200 || err != nil || got["content"] != "\uFEFFid;name\r\n" || got["project_id"] != p.projectID() || got["workflow_id"] != w["id"] || got["node_id"] != "csv-a" {
		t.Fatal(status, string(raw), err)
	}
	graph := Obj{"nodes": []any{Obj{"id": "unsaved-csv", "properties": Obj{"component": "CSVDataSet", "name": "未保存", "config": Obj{"variableNames": "current_id,current_name", "delimiter": "|"}}}}, "environment_variables": Obj{"secret": "must-not-appear"}}
	status, _, raw = request(http.MethodPost, path, Obj{"node_id": "unsaved-csv", "graph": graph}, headers)
	got, err = decode(raw)
	if status != 200 || err != nil || got["content"] != "\uFEFFcurrent_id|current_name\r\n" || strings.Contains(string(raw), "must-not-appear") {
		t.Fatal(status, string(raw), err)
	}
	// Compare stored values with stored values: the create result uses int,
	// while decrypted JSON uses json.Number. Exact ciphertext equality also
	// catches a write that re-encrypts an otherwise identical graph.
	var afterRevision int64
	var afterPayload string
	if err := p.DB.QueryRow("SELECT revision,payload FROM workflows WHERE id=? AND project_id=?", w["id"], p.projectID()).Scan(&afterRevision, &afterPayload); err != nil {
		t.Fatal(err)
	}
	if afterRevision != beforeRevision || afterPayload != beforePayload {
		t.Fatal("template mutated persisted workflow")
	}
	other, err := p.projectCreate(Obj{"name": "Other template project"})
	if err != nil {
		t.Fatal(err)
	}
	headers["X-Project-ID"] = str(other["id"])
	status, _, _ = request(http.MethodPost, path, Obj{"node_id": "unsaved-csv", "graph": graph}, headers)
	if status != 404 {
		t.Fatalf("snapshot bypassed workflow project ownership: %d", status)
	}
	headers["X-Project-ID"] = p.projectID()
	for _, body := range []Obj{{}, {"node_id": "missing"}, {"node_id": "csv-a", "graph": []any{}}, {"node_id": "../csv-a"}} {
		status, _, _ = request(http.MethodPost, path, body, headers)
		if status != 400 && status != 404 {
			t.Fatalf("invalid template config status %d", status)
		}
	}
	var files int
	if err := p.DB.QueryRow("SELECT COUNT(*) FROM workflow_datasets WHERE workflow_id=?", w["id"]).Scan(&files); err != nil || files != 0 {
		t.Fatal("template wrote dataset", files, err)
	}
	project, err := p.projectGetQ(p.DB, p.projectID(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.projectUpdate(p.projectID(), Obj{"revision": project["revision"]}, true); err != nil {
		t.Fatal(err)
	}
	status, _, raw = request(http.MethodPost, path, Obj{"node_id": "csv-a"}, headers)
	if status != 200 {
		t.Fatalf("read-only template blocked for archived project: %d %s", status, string(raw))
	}
}
