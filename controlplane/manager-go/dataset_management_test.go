package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Removing SQL project scope, treating search text as LIKE syntax, or paging
// before filtering breaks this API contract. Fixtures only insert metadata.
func TestDatasetManagementListIsolationFiltersAndPaging(t *testing.T) {
	p, _ := mysqlFixture(t)
	workflow := managementWorkflow(t, p, "Workflow A", "csv-a", true, false)
	otherWorkflow := managementWorkflow(t, p, "Workflow B", "csv-b", true, false)
	managementDataset(t, p, workflow, "csv-a", "file-a", "literal%_\\中.csv", 0)
	managementDataset(t, p, workflow, "csv-a", "file-b", "regular.csv", 0)
	managementDataset(t, p, otherWorkflow, "csv-b", "file-c", "third.csv", 0)
	managementDataset(t, p, workflow, "csv-a", "file-deleted", "deleted.csv", 1)
	project, e := p.projectCreate(Obj{"name": "Other"})
	if e != nil {
		t.Fatal(e)
	}
	other := p.forProject(str(project["id"]))
	foreignWorkflow := managementWorkflow(t, other, "Foreign workflow", "csv-a", true, false)
	managementDataset(t, other, foreignWorkflow, "csv-a", "file-foreign", "secret.csv", 0)
	_, call := workspaceHTTP(t, p)
	code, first := call("GET", "/api/datasets?page=1&page_size=2", nil)
	if code != 200 || number(first["total"]) != 3 || number(first["page"]) != 1 || number(first["page_size"]) != 2 || len(arr(first["items"])) != 2 {
		t.Fatalf("first page: %d %v", code, first)
	}
	code, second := call("GET", "/api/datasets?page=2&page_size=2", nil)
	if code != 200 || number(second["total"]) != 3 || len(arr(second["items"])) != 1 {
		t.Fatalf("second page: %d %v", code, second)
	}
	ids := map[string]bool{}
	for _, page := range []Obj{first, second} {
		for _, v := range arr(page["items"]) {
			item := obj(v)
			id := str(item["id"])
			if ids[id] || id == "file-deleted" || id == "file-foreign" {
				t.Fatal("leaked or duplicate", item)
			}
			ids[id] = true
			if item["source_available"] != true || item["source_status"] != "available" || str(item["workflow_name"]) == "" || number(item["rows"]) != 2 {
				t.Fatal("lost metadata or source", item)
			}
		}
	}
	for _, tc := range []struct {
		query string
		total int
		id    string
	}{
		{"q=" + url.QueryEscape("%_\\中"), 1, "file-a"},
		{"workflow_id=" + str(workflow["id"]), 2, ""},
		{"workflow_id=" + str(workflow["id"]) + "&node_id=csv-a", 2, ""},
		{"workflow_id=" + str(workflow["id"]) + "&node_id=missing", 0, ""},
		{"q=not-present", 0, ""},
		{"q=%25", 1, "file-a"},
		{"q=secret", 0, ""},
	} {
		code, result := call("GET", "/api/datasets?"+tc.query, nil)
		if code != 200 || number(result["total"]) != float64(tc.total) || len(arr(result["items"])) != tc.total {
			t.Fatalf("filter %s: %d %v", tc.query, code, result)
		}
		if tc.id != "" && obj(arr(result["items"])[0])["id"] != tc.id {
			t.Fatal(result)
		}
	}
	code, defaults := call("GET", "/api/datasets", nil)
	if code != 200 || number(defaults["page_size"]) != 20 {
		t.Fatal(code, defaults)
	}
	code, beyond := call("GET", "/api/datasets?page=4&page_size=2", nil)
	if code != 200 || number(beyond["total"]) != 3 || len(arr(beyond["items"])) != 0 {
		t.Fatal(code, beyond)
	}
	for _, query := range []string{"page=0", "page=-1", "page=abc", "page=9223372036854775807", "page_size=0", "page_size=101", "page_size=1.5", "page=1&page=2", "page_size=", "workflow_id=missing", "workflow_id=" + str(foreignWorkflow["id"]), "workflow_id=bad%2Fid", "node_id=csv-a", "workflow_id=" + str(workflow["id"]) + "&node_id=bad%2Fid"} {
		code, result := call("GET", "/api/datasets?"+query, nil)
		if code != 400 {
			t.Fatalf("accepted invalid %s: %d %v", query, code, result)
		}
	}
	code, filteredPage := call("GET", "/api/datasets?workflow_id="+str(workflow["id"])+"&page_size=1&page=2", nil)
	if code != 200 || number(filteredPage["total"]) != 2 || len(arr(filteredPage["items"])) != 1 {
		t.Fatal(code, filteredPage)
	}
	code, catalog := call("GET", "/api/datasets/catalog", nil)
	if code != 200 || len(arr(catalog["workflows"])) != 2 {
		t.Fatal("catalog project isolation", code, catalog)
	}
	for _, value := range arr(catalog["workflows"]) {
		if obj(value)["id"] == foreignWorkflow["id"] {
			t.Fatal("foreign catalog source", value)
		}
	}
	// Handler project scope comes from request header, not the fixture's Platform copy.
	request, _ := workspaceHTTP(t, p)
	code, _, raw := request("GET", "/api/datasets", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])})
	scoped, _ := decode(raw)
	if code != 200 || number(scoped["total"]) != 1 || obj(arr(scoped["items"])[0])["id"] != "file-foreign" {
		t.Fatal(code, scoped)
	}
	code, _, raw = request("GET", "/api/datasets/catalog", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])})
	scoped, _ = decode(raw)
	if code != 200 || len(arr(scoped["workflows"])) != 1 || obj(arr(scoped["workflows"])[0])["id"] != foreignWorkflow["id"] {
		t.Fatal("scoped catalog", code, scoped)
	}
	emptyProject, e := p.projectCreate(Obj{"name": "Empty project"})
	if e != nil {
		t.Fatal(e)
	}
	code, _, raw = request("GET", "/api/datasets/catalog", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(emptyProject["id"])})
	scoped, _ = decode(raw)
	if code != 200 || string(encoded(scoped["workflows"])) != "[]" {
		t.Fatal("empty catalog", code, scoped)
	}
}

// Source deletion must not hide files or expose complete workflow configuration.
func TestDatasetManagementUnavailableSourcesAndCatalog(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	p.DB.SetMaxIdleConns(1)
	live := managementWorkflow(t, p, "Live workflow", "csv-live", true, true)
	removed := managementWorkflow(t, p, "Removed node", "csv-removed", true, false)
	disabled := managementWorkflow(t, p, "Disabled node", "csv-disabled", false, false)
	deleted := managementWorkflow(t, p, "Deleted workflow", "csv-deleted", true, false)
	for _, entry := range []struct {
		w        Obj
		node, id string
	}{{live, "csv-live", "live-file"}, {removed, "csv-removed", "removed-file"}, {disabled, "csv-disabled", "disabled-file"}, {deleted, "csv-deleted", "deleted-file"}} {
		managementDataset(t, p, entry.w, entry.node, entry.id, entry.id+".csv", 0)
	}
	if _, e := p.DB.Exec("UPDATE workflows SET deleted=1 WHERE id=?", deleted["id"]); e != nil {
		t.Fatal(e)
	}
	update := clone(removed)
	update["graph"] = basicWorkflowGraph()
	if _, e := p.workspaceUpdate("workflows", str(removed["id"]), update); e != nil {
		t.Fatal(e)
	}
	// More than one picker window of non-CSV workflows must neither obscure
	// the useful CSV source nor appear in the compact catalog response.
	if e := p.transaction(func(tx *Tx) error {
		for i := 0; i < 201; i++ {
			id := fmt.Sprintf("non-csv-source-%03d", i)
			graph := basicWorkflowGraph()
			if i == 0 {
				obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = strings.Repeat("non-csv-content", 65536)
			}
			data := Obj{"id": id, "name": "No CSV source", "revision": 1, "project_id": p.projectID(), "graph": graph}
			payload, e := p.sealWorkspace("workflows", id, 1, data)
			if e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO workflows(id,revision,deleted,created_at,updated_at,payload,project_id) VALUES(?,1,0,?,?,?,?)", id, "9999-12-31", "9999-12-31", payload, p.projectID()); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, p)
	code, result := call("GET", "/api/datasets", nil)
	if code != 200 || number(result["total"]) != 4 {
		t.Fatal(code, result)
	}
	want := map[string]string{"live-file": "available", "removed-file": "node_removed", "disabled-file": "node_removed", "deleted-file": "workflow_deleted"}
	for _, v := range arr(result["items"]) {
		item := obj(v)
		status := want[str(item["id"])]
		if item["source_status"] != status || item["source_available"] != (status == "available") || str(item["workflow_name"]) == "" {
			t.Fatal(item)
		}
	}
	code, result = call("GET", "/api/datasets?workflow_id="+str(deleted["id"]), nil)
	if code != 200 || number(result["total"]) != 1 {
		t.Fatal(code, result)
	}
	code, catalog := call("GET", "/api/datasets/catalog", nil)
	if code != 200 || len(arr(catalog["workflows"])) != 1 {
		t.Fatal(code, catalog)
	}
	w := obj(arr(catalog["workflows"])[0])
	nodes := arr(w["nodes"])
	if w["id"] != live["id"] || w["name"] != "Live workflow" || len(nodes) != 1 {
		t.Fatal(w)
	}
	node := obj(nodes[0])
	if node["node_id"] != "csv-live" || strings.Join(stringList(node["columns"]), ",") != "id,name" || strings.Join(stringList(node["variable_names"]), ",") != "row_id,row_name" || node["delimiter"] != ";" {
		t.Fatal(node)
	}
	for _, forbidden := range []string{"graph", "environment_variables", "private-host"} {
		if strings.Contains(string(encoded(catalog)), forbidden) {
			t.Fatal("catalog leaked", catalog)
		}
	}
	code, file := call("GET", "/api/datasets/deleted-file", nil)
	if code != 200 || file["id"] != "deleted-file" {
		t.Fatal("unavailable source metadata unreadable", code, file)
	}
	if _, e := p.DB.Exec("UPDATE workflow_datasets SET deleted=1 WHERE project_id=?", p.projectID()); e != nil {
		t.Fatal(e)
	}
	code, empty := call("GET", "/api/datasets", nil)
	if code != 200 || string(encoded(empty["items"])) != "[]" || number(empty["total"]) != 0 {
		t.Fatal(code, empty)
	}
}

// The transfer budget catches fetching an encrypted megabyte-sized graph per
// file instead of once per distinct source. MySQL's own connection counter
// measures the real query boundary without a query mock or timing threshold.
func TestDatasetManagementLargeWorkflowTransferBudget(t *testing.T) {
	p, _ := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	p.DB.SetMaxIdleConns(1)
	workflow := managementWorkflow(t, p, "Large source", "csv-large", true, false)
	update := clone(workflow)
	graph := obj(update["graph"])
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = strings.Repeat("x", 1024*1024)
	var e error
	workflow, e = p.workspaceUpdate("workflows", str(workflow["id"]), update)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 24; i++ {
		managementDataset(t, p, workflow, "csv-large", fmt.Sprintf("large-file-%02d", i), "large.csv", 0)
	}
	bytesSent := func() int64 {
		t.Helper()
		var name string
		var value int64
		if e := p.DB.QueryRow("SHOW SESSION STATUS LIKE 'Bytes_sent'").Scan(&name, &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	_, call := workspaceHTTP(t, p)
	before := bytesSent()
	code, result := call("GET", "/api/datasets?page_size=100", nil)
	transferred := bytesSent() - before
	if code != 200 || number(result["total"]) != 24 || len(arr(result["items"])) != 24 {
		t.Fatal(code, result)
	}
	if transferred > 4*1024*1024 {
		t.Fatalf("file metadata page transferred %d MySQL bytes for one 1MiB workflow; source graph must be fetched once", transferred)
	}
	t.Logf("24-file metadata page MySQL bytes: %d", transferred)
}

func TestDatasetManagementRejectsMalformedQuery(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	for _, path := range []string{"/api/datasets?workflow_id=%ZZ", "/api/datasets?page=1;bad", "/api/datasets/catalog?workflow_id=%ZZ"} {
		code, result := call("GET", path, nil)
		if code != 400 || obj(result["error"])["code"] != "invalid_dataset_filter" {
			t.Fatalf("malformed query accepted: %s: %d %v", path, code, result)
		}
	}
}

// Disabling a CSV node while its object upload is in flight must fail the final
// locked ownership check; the immutable object need not be removed on failure.
func TestDatasetManagementUploadRejectsConcurrentlyDisabledNode(t *testing.T) {
	p, _ := mysqlFixture(t)
	workflow := managementWorkflow(t, p, "Concurrent disable", "csv-disable", true, false)
	peer := runnerObjectPeer(t)
	putStarted, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			close(putStarted)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		peer.Config.Handler.ServeHTTP(w, r)
	}))
	defer blocked.Close()
	// Unblock before Close even if a fixture update fails.
	defer unblock()
	p.Config.ArchiveEndpoint = blocked.URL
	p.Config.ArchiveBucket = "test-datasets"
	p.Config.ArchiveAccessKey = "test-access"
	p.Config.ArchiveSecretKey = "test-secret"
	p.Config.ArchiveTimeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, e := p.datasetCreate(ctx, str(workflow["id"]), "csv-disable", "disable.csv", strings.NewReader("id;name\n1;user\n"))
		finished <- e
	}()
	select {
	case <-putStarted:
	case e := <-finished:
		t.Fatalf("upload failed before fixture PUT: %v", e)
	case <-ctx.Done():
		t.Fatal("PUT did not start")
	}
	update := clone(workflow)
	obj(findGraphNode(obj(update["graph"]), "CSVDataSet")["properties"])["enabled"] = false
	if _, e := p.workspaceUpdate("workflows", str(workflow["id"]), update); e != nil {
		t.Fatal(e)
	}
	unblock()
	var uploadError error
	select {
	case uploadError = <-finished:
	case <-ctx.Done():
		t.Fatal("upload did not finish")
	}
	var apiError *APIError
	if !errors.As(uploadError, &apiError) || apiError.Status != 409 || apiError.Code != "dataset_node_changed" {
		t.Fatalf("disabled source upload accepted: %v", uploadError)
	}
	var count int
	if e := p.DB.QueryRow("SELECT COUNT(*) FROM workflow_datasets WHERE project_id=? AND workflow_id=?", p.projectID(), workflow["id"]).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatalf("disabled source persisted %d dataset records", count)
	}
}

func managementWorkflow(t *testing.T, p *Platform, name, nodeID string, enabled, implicit bool) Obj {
	t.Helper()
	graph := basicWorkflowGraph()
	node := graphNode(nodeID, "CSVDataSet", Obj{"variableNames": "row_id,row_name", "delimiter": ";"})
	props := obj(node["properties"])
	props["enabled"] = enabled
	if !implicit {
		props["dataset_columns"] = []any{"id", "name"}
	}
	graph["nodes"] = append(arr(graph["nodes"]), node)
	graph["edges"] = append(arr(graph["edges"]), graphEdge("group", nodeID, 1))
	graph["environment_binding"] = "base_host"
	graph["environment_variables"] = []any{"private-host"}
	w, e := p.workspaceCreate("workflows", Obj{"name": name, "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func managementDataset(t *testing.T, p *Platform, w Obj, nodeID, id, name string, deleted int) {
	t.Helper()
	data := Obj{"id": id, "project_id": p.projectID(), "workflow_id": w["id"], "node_id": nodeID, "node_name": "CSV node", "name": name, "columns": []any{"id", "name"}, "rows": 2, "size": 20, "delimiter": ";", "created_at": "2026-11-06T00:00:00Z"}
	if _, e := p.DB.Exec("INSERT INTO workflow_datasets(id,project_id,workflow_id,node_id,deleted,created_at,data) VALUES(?,?,?,?,?,?,?)", id, p.projectID(), w["id"], nodeID, deleted, data["created_at"], string(encoded(data))); e != nil {
		t.Fatal(e)
	}
}

func TestDatasetCatalogMissingProjectionOnlyReadsMissingPayloads(t *testing.T) {
	p, _ := mysqlFixture(t)
	projected := managementWorkflow(t, p, "Projected", "csv-good", true, false)
	missing := managementWorkflow(t, p, "Missing", "csv-missing", true, false)
	if _, err := p.DB.Exec("UPDATE workflows SET payload='unreadable-current-payload' WHERE id=?", projected["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DB.Exec("UPDATE workflows SET summary_payload=NULL WHERE id=?", missing["id"]); err != nil {
		t.Fatal(err)
	}
	p.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := p.forRequest(ctx, p.projectID()).datasetManagementCatalog()
	if err != nil || len(arr(result["workflows"])) != 2 {
		t.Fatal("missing projection opened an unrelated source", result, err)
	}
	cancel()
	if _, err = p.forRequest(ctx, p.projectID()).datasetManagementCatalog(); err == nil {
		t.Fatal("catalog ignored request cancellation")
	}
}

func TestDatasetCatalogOldProjectionPreservesAuthoredColumnProvenance(t *testing.T) {
	p, _ := mysqlFixture(t)
	unrelated := managementWorkflow(t, p, "Current projection", "csv-current", true, false)
	old := managementWorkflow(t, p, "Old projection", "csv-old", true, true)
	managementDataset(t, p, old, "csv-old", "dataset-old", "Uploaded", 0)
	var raw string
	var revision int64
	if err := p.DB.QueryRow("SELECT revision,payload FROM workflow_csv_nodes WHERE workflow_id=?", old["id"]).Scan(&revision, &raw); err != nil {
		t.Fatal(err)
	}
	node, err := p.openWorkspace("csv-node/"+str(old["id"]), "csv-old", revision, raw)
	if err != nil {
		t.Fatal(err)
	}
	delete(node, "columns_declared")
	raw, err = p.sealWorkspace("csv-node/"+str(old["id"]), "csv-old", revision, node)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("UPDATE workflow_csv_nodes SET payload=? WHERE workflow_id=?", raw, old["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("UPDATE workflows SET payload='unreadable-unrelated-source' WHERE id=?", unrelated["id"]); err != nil {
		t.Fatal(err)
	}
	p.DB.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := p.forRequest(ctx, p.projectID()).datasetManagementCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range arr(result["workflows"]) {
		w := obj(value)
		if w["id"] == old["id"] {
			columns := stringList(obj(arr(w["nodes"])[0])["columns"])
			if strings.Join(columns, ",") != "id,name" {
				t.Fatal("old implicit columns ignored uploaded headers", columns)
			}
			if _, err = p.rebuildPageReadModels(1); err != nil {
				t.Fatal(err)
			}
			var rebuilt string
			if err = p.DB.QueryRow("SELECT payload FROM workflow_csv_nodes WHERE workflow_id=?", old["id"]).Scan(&rebuilt); err != nil {
				t.Fatal(err)
			}
			projection, err := p.openWorkspace("csv-node/"+str(old["id"]), "csv-old", revision, rebuilt)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := projection["columns_declared"].(bool); !ok {
				t.Fatal("explicit rebuild left outdated column provenance")
			}
			return
		}
	}
	t.Fatal("old projected workflow missing")
}
