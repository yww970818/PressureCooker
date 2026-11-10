package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestDatasetOnlineTemplateGeneration(t *testing.T) {
	body := Obj{"rows": 2, "columns": []any{Obj{"name": "id", "generator": "template", "value": "user_{{row}}-{{uuid}}/{{uuid}}"}, Obj{"name": "text", "generator": "template", "value": "000,中文\n\"{{row}}\""}}}
	var output bytes.Buffer
	if e := generateDatasetCSVTo(context.Background(), body, ';', &output, 1024*1024); e != nil {
		t.Fatal("template generator rejected", e)
	}
	r := csv.NewReader(&output)
	r.Comma = ';'
	rows, e := r.ReadAll()
	if e != nil || len(rows) != 3 || rows[1][1] != "000,中文\n\"1\"" || rows[2][1] != "000,中文\n\"2\"" {
		t.Fatal(rows, e)
	}
	pattern := regexp.MustCompile(`^user_[12]-([0-9a-f-]{36})/([0-9a-f-]{36})$`)
	for _, row := range rows[1:] {
		match := pattern.FindStringSubmatch(row[0])
		if len(match) != 3 || match[1] != match[2] {
			t.Fatal("UUID placeholders must share one value per cell", row)
		}
	}
	for _, value := range []string{"{{unknown}}", "x{{row }}}", "{{uuid", strings.Repeat("x", 65537), "bad\x00text"} {
		body["columns"] = []any{Obj{"name": "id", "generator": "template", "value": value}}
		output.Reset()
		if e = generateDatasetCSVTo(context.Background(), body, ',', &output, 1024*1024); e == nil {
			t.Fatalf("accepted invalid template %q", value[:min(len(value), 30)])
		}
	}
	body["columns"] = []any{Obj{"name": "id", "generator": "template", "value": strings.Repeat("x", 65536)}}
	if e = generateDatasetCSVTo(context.Background(), body, ',', &bytes.Buffer{}, 1024*1024); e != nil {
		t.Fatal("64 KiB template must remain supported", e)
	}
}

func onlineDatasetFixture(t *testing.T) (*Platform, Obj, Obj) {
	t.Helper()
	p, _ := mysqlFixture(t)
	w := managementWorkflow(t, p, "Online CSV", "csv-online", true, false)
	d, e := p.datasetCreate(context.Background(), str(w["id"]), "csv-online", "原文件.csv", strings.NewReader("id;name\n0001;\"含;分隔符\"\n0002;\"第一行\n第二行\"\n0003;第三行\n"))
	if e != nil {
		t.Fatal(e)
	}
	return p, w, d
}

func TestDatasetOnlineTablePagingAndIsolation(t *testing.T) {
	p, _, d := onlineDatasetFixture(t)
	request, call := workspaceHTTP(t, p)
	path := "/api/datasets/" + str(d["id"]) + "/table"
	code, first := call("GET", path+"?page_size=2", nil)
	if code != 200 || number(first["total_rows"]) != 3 || number(first["page"]) != 1 || number(first["page_size"]) != 2 || first["sha256"] != d["sha256"] || first["name"] != d["name"] || first["delimiter"] != ";" || number(first["version"]) != 1 {
		t.Fatal("table page metadata", code, first)
	}
	if string(encoded(first["rows"])) != `[["0001","含;分隔符"],["0002","第一行\n第二行"]]` {
		t.Fatal("CSV text was changed", first)
	}
	code, second := call("GET", path+"?page=2&page_size=2", nil)
	if code != 200 || string(encoded(second["rows"])) != `[["0003","第三行"]]` {
		t.Fatal(code, second)
	}
	code, beyond := call("GET", path+"?page=3&page_size=2", nil)
	if code != 200 || string(encoded(beyond["rows"])) != "[]" {
		t.Fatal(code, beyond)
	}
	for _, query := range []string{"page=0", "page=-1", "page=bad", "page_size=201", "page_size=0", "page=9223372036854775807", "page=1&page=2", "page_size=2&page_size=2", "page=%zz"} {
		code, _ = call("GET", path+"?"+query, nil)
		if code != 400 {
			t.Fatalf("accepted %s: %d", query, code)
		}
	}
	project, e := p.projectCreate(Obj{"name": "Other online"})
	if e != nil {
		t.Fatal(e)
	}
	code, _, _ = request("GET", path, nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])})
	if code != 404 {
		t.Fatal("foreign dataset exposed", code)
	}
	code, _ = call("DELETE", "/api/datasets/"+str(d["id"]), nil)
	if code != 200 {
		t.Fatal(code)
	}
	code, _ = call("GET", path, nil)
	if code != 404 {
		t.Fatal("deleted dataset exposed", code)
	}
}

func TestDatasetOnlineEditsCreateImmutableVersions(t *testing.T) {
	p, _, original := onlineDatasetFixture(t)
	request, call := workspaceHTTP(t, p)
	id := str(original["id"])
	body := Obj{"name": "新版.csv", "base_sha256": original["sha256"], "updates": []any{Obj{"row_index": 0, "values": []any{"00009", "逗号,分号;引号\"\n多行"}}}, "delete_rows": []any{1}, "append_rows": []any{[]any{"00004", "追加"}}}
	code, next := call("POST", "/api/datasets/"+id+"/edits", body)
	if code != 201 || next["id"] == original["id"] || next["parent_dataset_id"] != original["id"] || next["root_dataset_id"] != original["id"] || number(next["version"]) != 2 || next["name"] != "新版.csv" || number(next["rows"]) != 3 || next["sha256"] == original["sha256"] || obj(next["object"])["key"] == obj(original["object"])["key"] {
		t.Fatal("edits must create a new immutable dataset", code, next)
	}
	code, table := call("GET", "/api/datasets/"+str(next["id"])+"/table", nil)
	if code != 200 || string(encoded(table["rows"])) != `[["00009","逗号,分号;引号\"\n多行"],["0003","第三行"],["00004","追加"]]` {
		t.Fatal(code, table)
	}
	code, unchanged := call("GET", "/api/datasets/"+id, nil)
	if code != 200 || string(encoded(unchanged)) != string(encoded(clone(original))) {
		t.Fatal("original metadata changed", code, unchanged)
	}
	code, _, content := request("GET", "/api/datasets/"+id+"/content", nil, map[string]string{"Authorization": "Bearer admin-secret"})
	if code != 200 || string(content) != "id;name\n0001;\"含;分隔符\"\n0002;\"第一行\n第二行\"\n0003;第三行\n" {
		t.Fatal("original object changed", code)
	}
	code, third := call("POST", "/api/datasets/"+str(next["id"])+"/edits", Obj{"base_sha256": next["sha256"], "append_rows": []any{[]any{"00005", "第三版"}}})
	if code != 201 || number(third["version"]) != 3 || third["root_dataset_id"] != original["id"] || third["parent_dataset_id"] != next["id"] {
		t.Fatal(code, third)
	}
	code, branch := call("POST", "/api/datasets/"+id+"/edits", Obj{"base_sha256": original["sha256"], "append_rows": []any{[]any{"00006", "分支"}}})
	if code != 201 || number(branch["version"]) != 2 {
		t.Fatal(code, branch)
	}
}

func TestDatasetOnlineEditsRejectInvalidChangesAndSources(t *testing.T) {
	p, workflow, d := onlineDatasetFixture(t)
	request, call := workspaceHTTP(t, p)
	path := "/api/datasets/" + str(d["id"]) + "/edits"
	cases := []Obj{
		{}, {"base_sha256": "stale"}, {"updates": []any{Obj{"row_index": -1, "values": []any{"1", "x"}}}},
		{"updates": []any{Obj{"row_index": 3, "values": []any{"1", "x"}}}}, {"updates": []any{Obj{"row_index": 0.5, "values": []any{"1", "x"}}}},
		{"updates": []any{Obj{"row_index": 0, "values": []any{"1"}}}}, {"append_rows": []any{[]any{1, "x"}}}, {"append_rows": []any{[]any{"1", "x\x00"}}},
		{"updates": []any{Obj{"row_index": 0, "values": []any{"1", "x"}}, Obj{"row_index": 0, "values": []any{"1", "y"}}}},
		{"delete_rows": []any{0, 0}}, {"delete_rows": []any{0, 1, 2}}, {"delete_rows": []any{3}}, {"delete_rows": []any{"1"}},
		{"updates": []any{Obj{"row_index": 0, "values": []any{"1", "x"}}}, "delete_rows": []any{0}},
		{"updates": "wrong"}, {"append_rows": []any{"wrong"}}, {"name": "  ", "append_rows": []any{[]any{"1", "x"}}},
	}
	for i, body := range cases {
		if i > 1 {
			body["base_sha256"] = d["sha256"]
		}
		code, _ := call("POST", path, body)
		if code != 400 && code != 409 {
			t.Fatalf("accepted invalid edit %d: %d", i, code)
		}
	}
	tooMany := []any{}
	for i := 0; i < 10001; i++ {
		tooMany = append(tooMany, []any{"1", "x"})
	}
	code, _ := call("POST", path, Obj{"base_sha256": d["sha256"], "append_rows": tooMany})
	if code != 400 {
		t.Fatal("change bound ignored", code)
	}
	valid := Obj{"base_sha256": d["sha256"], "append_rows": []any{[]any{"1", "x"}}}
	project, e := p.projectCreate(Obj{"name": "Foreign online"})
	if e != nil {
		t.Fatal(e)
	}
	code, _, _ = request("POST", path, valid, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])})
	if code != 404 {
		t.Fatal("foreign edit accepted", code)
	}
	update := clone(workflow)
	obj(findGraphNode(obj(update["graph"]), "CSVDataSet")["properties"])["enabled"] = false
	if _, e = p.workspaceUpdate("workflows", str(workflow["id"]), update); e != nil {
		t.Fatal(e)
	}
	code, _ = call("POST", path, valid)
	if code != 400 && code != 409 {
		t.Fatal("disabled source accepted", code)
	}
	var count int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM workflow_datasets WHERE project_id=?", p.projectID()).Scan(&count); e != nil || count != 1 {
		t.Fatal("invalid edits persisted", count, e)
	}
}

func TestDatasetOnlineGenerationPreviewDoesNotPersist(t *testing.T) {
	p, w, _ := onlineDatasetFixture(t)
	_, call := workspaceHTTP(t, p)
	path := "/api/workflows/" + str(w["id"]) + "/datasets/generate/preview"
	body := Obj{"node_id": "csv-online", "rows": 100000, "columns": []any{Obj{"name": "id", "generator": "template", "value": "user_{{row}}"}, Obj{"name": "name", "generator": "constant", "value": "000,中文"}}}
	// Any object-store access is a test failure. Preview must only use metadata.
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("preview touched object store")
		w.WriteHeader(500)
	}))
	defer peer.Close()
	p.Config.ArchiveEndpoint = peer.URL
	code, preview := call("POST", path, body)
	if code != 200 || number(preview["total_rows"]) != 100000 || len(arr(preview["rows"])) != 10 || preview["delimiter"] != ";" || string(encoded(arr(preview["rows"])[9])) != `["user_10","000,中文"]` {
		t.Fatal(code, preview)
	}
	for _, mutate := range []func(Obj){
		func(b Obj) { b["rows"] = 0 }, func(b Obj) { b["rows"] = 100001 }, func(b Obj) { b["node_id"] = "missing" },
		func(b Obj) {
			b["columns"] = []any{Obj{"name": "wrong", "generator": "sequence"}, Obj{"name": "name", "generator": "constant", "value": "x"}}
		},
		func(b Obj) { b["columns"] = []any{Obj{"name": "id", "generator": "sequence"}} },
	} {
		invalid := clone(body)
		mutate(invalid)
		code, _ = call("POST", path, invalid)
		if code != 400 {
			t.Fatal("invalid preview accepted", code)
		}
	}
	var count int
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM workflow_datasets WHERE project_id=?", p.projectID()).Scan(&count); e != nil || count != 1 {
		t.Fatal("preview persisted", count, e)
	}
}

func TestDatasetOnlineGenerationPreviewPreservesPhysicalAliases(t *testing.T) {
	p, _ := mysqlFixture(t)
	w := managementWorkflow(t, p, "Implicit columns", "csv-alias", true, true)
	_, call := workspaceHTTP(t, p)
	body := Obj{"node_id": "csv-alias", "rows": 1, "columns": []any{Obj{"name": "id", "generator": "template", "value": "{{row}}"}, Obj{"name": "name", "generator": "constant", "value": "x"}}}
	code, result := call("POST", "/api/workflows/"+str(w["id"])+"/datasets/generate/preview", body)
	if code != 200 || string(encoded(result["columns"])) != `["id","name"]` {
		t.Fatal("variable aliases must not impose physical column names", code, result)
	}
}

func TestDatasetOnlineEditsPreservePhysicalAliasesAndArchive(t *testing.T) {
	p, _ := mysqlFixture(t)
	w := managementWorkflow(t, p, "Implicit edits", "csv-alias-edit", true, true)
	d, e := p.datasetCreate(context.Background(), str(w["id"]), "csv-alias-edit", "alias.csv", strings.NewReader("id;name\n0001;first\n"))
	if e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, p)
	body := Obj{"base_sha256": d["sha256"], "append_rows": []any{[]any{"0002", "second"}}}
	path := "/api/datasets/" + str(d["id"]) + "/edits"
	code, result := call("POST", path, body)
	if code != 201 || number(result["version"]) != 2 || string(encoded(result["columns"])) != `["id","name"]` {
		t.Fatal("physical headers must remain separate from variable aliases", code, result)
	}
	project, e := p.projectGetQ(p.DB, p.projectID(), false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.projectUpdate(p.projectID(), Obj{"revision": project["revision"]}, true); e != nil {
		t.Fatal(e)
	}
	code, _ = call("POST", path, body)
	if code != 409 {
		t.Fatal("archived project accepted edit", code)
	}
}

func TestDatasetOnlineEditBodyLimit(t *testing.T) {
	for _, size := range []int{7 * 1024 * 1024, 8*1024*1024 + 1} {
		input := `{"padding":"` + strings.Repeat("x", size-14) + `"}`
		r := httptest.NewRequest("POST", "/api/datasets/dataset-one/edits", strings.NewReader(input))
		_, e := readJSON(r)
		if size < 8*1024*1024 && e != nil {
			t.Fatal("valid edit body below 8 MiB rejected", e)
		}
		if size > 8*1024*1024 && e == nil {
			t.Fatal("edit request limit ignored")
		}
	}
}

func TestDatasetOnlineTableReadsOnlyRequestedPage(t *testing.T) {
	data := "id,name\n0001,first\n" + strings.Repeat("0002,tail\n", 100000)
	hash := sha256.Sum256([]byte(data))
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write([]byte("id,name\n0001,first\n"))
		w.(http.Flusher).Flush()
		// A full-file reader blocks until the caller's bounded context expires.
		<-r.Context().Done()
	}))
	defer peer.Close()
	p := &Platform{Config: Config{ArchiveEndpoint: peer.URL, ArchiveBucket: "datasets", ArchiveAccessKey: "fixture", ArchiveSecretKey: "fixture", ArchiveTimeout: time.Second}}
	metadata := Obj{"id": "dataset-stream", "name": "stream.csv", "rows": 100001, "version": 1, "delimiter": ",", "sha256": hex.EncodeToString(hash[:]), "object": Obj{"bucket": "datasets", "key": "stream.csv", "size": len(data), "sha256": hex.EncodeToString(hash[:])}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, e := p.datasetTable(ctx, metadata, "page_size=1")
	if e != nil || string(encoded(result["rows"])) != `[["0001","first"]]` {
		t.Fatal("table read the remaining CSV rather than the requested page", result, e)
	}
}

func TestDatasetOnlineEditsPreserveEmptySingleColumnCells(t *testing.T) {
	p, _ := mysqlFixture(t)
	w := managementWorkflow(t, p, "Single column", "csv-empty", true, false)
	update := clone(w)
	props := obj(findGraphNode(obj(update["graph"]), "CSVDataSet")["properties"])
	props["dataset_columns"] = []any{"id"}
	obj(props["config"])["variableNames"] = "row_id"
	if _, e := p.workspaceUpdate("workflows", str(w["id"]), update); e != nil {
		t.Fatal(e)
	}
	d, e := p.datasetCreate(context.Background(), str(w["id"]), "csv-empty", "single.csv", strings.NewReader("id\n\"\"\n0001\n"))
	if e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, p)
	code, next := call("POST", "/api/datasets/"+str(d["id"])+"/edits", Obj{"base_sha256": d["sha256"], "append_rows": []any{[]any{""}}})
	if code != 201 || number(next["rows"]) != 3 {
		t.Fatal("single-column empty data rows were dropped", code, next)
	}
	code, table := call("GET", "/api/datasets/"+str(next["id"])+"/table", nil)
	if code != 200 || string(encoded(table["rows"])) != `[[""],["0001"],[""]]` {
		t.Fatal("empty strings did not survive CSV round trip", code, table)
	}
}

func TestDatasetOnlineTemplatePreservesEmptySingleColumnCells(t *testing.T) {
	var output bytes.Buffer
	body := Obj{"rows": 2, "columns": []any{Obj{"name": "id", "generator": "template", "value": ""}}}
	if e := generateDatasetCSVTo(context.Background(), body, ',', &output, 1024); e != nil {
		t.Fatal(e)
	}
	columns, rows, e := inspectDatasetCSV(&output, ',')
	if e != nil || len(columns) != 1 || rows != 2 {
		t.Fatal("empty template cells were dropped", columns, rows, e)
	}
}

func TestDatasetOnlineGenerationRejectsOversizedColumnNames(t *testing.T) {
	body := Obj{"rows": 1, "columns": []any{Obj{"name": strings.Repeat("中", 100), "generator": "template", "value": "{{row}}"}}}
	if e := generateDatasetCSVTo(context.Background(), body, ',', &bytes.Buffer{}, 1024); e == nil {
		t.Fatal("generator accepted a column name that stored CSV validation rejects")
	}
}
