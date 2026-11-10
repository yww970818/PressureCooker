package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func onlineTableObjectFixture(t *testing.T, contents string, rows int) (*Platform, Obj) {
	t.Helper()
	hash := sha256.Sum256([]byte(contents))
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(contents)))
		_, _ = w.Write([]byte(contents))
	}))
	t.Cleanup(peer.Close)
	p := &Platform{Config: Config{ArchiveEndpoint: peer.URL, ArchiveBucket: "datasets", ArchiveAccessKey: "fixture", ArchiveSecretKey: "fixture", ArchiveTimeout: time.Second}}
	sha := hex.EncodeToString(hash[:])
	metadata := Obj{"id": "dataset-table", "name": "table.csv", "rows": rows, "version": 1, "delimiter": ",", "sha256": sha, "object": Obj{"bucket": "datasets", "key": "table.csv", "size": len(contents), "sha256": sha}}
	return p, metadata
}

func TestDatasetOnlineTableBoundsRecordsAndJSON(t *testing.T) {
	for _, tc := range []struct {
		name, contents, query string
		rows, status          int
	}{
		{"large record", "id,name\n1," + strings.Repeat("x", 4*1024*1024+8192) + "\n", "page_size=1", 1, 413},
		{"skipped large record", "id,name\n1," + strings.Repeat("x", 4*1024*1024+8192) + "\n2,last\n", "page=2&page_size=1", 2, 413},
		{"JSON escaped cell", "id,name\n1," + strings.Repeat("\x01", 720*1024) + "\n", "page_size=1", 1, 413},
		{"gzip HTML escaped cell", "id,name\n1," + strings.Repeat("<", 720*1024) + "\n", "page_size=1", 1, 413},
		{"gzip Unicode escaped cell", "id,name\n1," + strings.Repeat("\u2028", 720*1024) + "\n", "page_size=1", 1, 413},
		{"accumulated page", "id,name\n" + strings.Repeat("1,"+strings.Repeat("x", 600*1024)+"\n", 8), "page_size=8", 8, 413},
		{"bounded valid page", "id,name\n1," + strings.Repeat("x", 3*1024*1024) + "\n", "page_size=1", 1, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, metadata := onlineTableObjectFixture(t, tc.contents, tc.rows)
			result, e := p.datasetTable(context.Background(), metadata, tc.query)
			if tc.status == 413 {
				status, code, _ := requestFailure(e)
				if e == nil || status != 413 || code != "dataset_table_too_large" {
					t.Fatalf("expected explicit table limit 413, got %d %s", status, code)
				}
			} else if e != nil || len(encoded(result)) > 4*1024*1024 {
				t.Fatalf("valid bounded page failed: %v", e)
			}
		})
	}
}

func TestDatasetOnlineTablePreservesQuotedCRLF(t *testing.T) {
	p, metadata := onlineTableObjectFixture(t, "id,name\r\n0001,\"a\r\nb \"\"quote\"\"\r\nc\"\r\n", 1)
	result, e := p.datasetTable(context.Background(), metadata, "")
	if e != nil || string(encoded(result["rows"])) != `[["0001","a\r\nb \"quote\"\r\nc"]]` {
		t.Fatal("quoted CRLF was normalized", result["rows"], e)
	}
}

func TestDatasetOnlineTableRejectsOriginalNUL(t *testing.T) {
	p, metadata := onlineTableObjectFixture(t, "id,name\n1,\"a\x00b\"\n", 1)
	_, e := p.datasetTable(context.Background(), metadata, "")
	status, _, _ := requestFailure(e)
	if e == nil || status != 400 {
		t.Fatalf("original NUL must not collide with CR sentinel: %v", e)
	}
}

func TestDatasetOnlineEditsPreserveUnchangedQuotedCRLF(t *testing.T) {
	p, workflow, _ := onlineDatasetFixture(t)
	originalText := "a\r\nb \"quote\"\r\nc"
	d, e := p.datasetCreate(context.Background(), str(workflow["id"]), "csv-online", "crlf.csv", strings.NewReader("id;name\r\n0001;\"a\r\nb \"\"quote\"\"\r\nc\"\r\n"))
	if e != nil {
		t.Fatal(e)
	}
	request, call := workspaceHTTP(t, p)
	code, next := call("POST", "/api/datasets/"+str(d["id"])+"/edits", Obj{"base_sha256": d["sha256"], "append_rows": []any{[]any{"0002", "appended"}}})
	if code != 201 {
		t.Fatal(code, next)
	}
	code, _, contents := request("GET", "/api/datasets/"+str(next["id"])+"/content", nil, map[string]string{"Authorization": "Bearer admin-secret"})
	if code != 200 || !strings.Contains(string(contents), "\"a\r\nb \"\"quote\"\"\r\nc\"") {
		t.Fatal("untouched cell bytes changed during append", code)
	}
	code, table := call("GET", "/api/datasets/"+str(next["id"])+"/table", nil)
	if code != 200 || str(arr(arr(table["rows"])[0])[1]) != originalText {
		t.Fatal("table changed quoted CRLF", code)
	}
}

func TestDatasetOnlineGenerationPreviewBoundsAndPreservesCRLF(t *testing.T) {
	p, workflow, _ := onlineDatasetFixture(t)
	_, call := workspaceHTTP(t, p)
	path := "/api/workflows/" + str(workflow["id"]) + "/datasets/generate/preview"
	for _, tc := range []struct{ name, value, second string }{
		{"CSV budget", strings.Repeat("x", 64*1024), strings.Repeat("x", 64*1024)},
		{"JSON escaped budget", strings.Repeat("\x01", 64*1024), "ok"},
		{"HTML escaped budget", strings.Repeat("<", 64*1024), "ok"},
		{"Unicode escaped budget", strings.Repeat("\u2028", 20*1024), "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := Obj{"node_id": "csv-online", "rows": 100000, "columns": []any{Obj{"name": "id", "generator": "template", "value": tc.value}, Obj{"name": "name", "generator": "template", "value": tc.second}}}
			code, result := call("POST", path, body)
			if code != 413 || obj(result["error"])["code"] != "dataset_preview_too_large" {
				t.Fatal("preview must enforce its own 1 MiB limit", code, obj(result["error"]))
			}
		})
	}
	body := Obj{"node_id": "csv-online", "rows": 2, "columns": []any{Obj{"name": "id", "generator": "template", "value": "{{row}}"}, Obj{"name": "name", "generator": "template", "value": "a\r\nb \"quote\""}}}
	code, preview := call("POST", path, body)
	if code != 200 || str(arr(arr(preview["rows"])[0])[1]) != "a\r\nb \"quote\"" {
		t.Fatal("preview changed quoted CRLF", code)
	}
}

func TestDatasetOnlinePreviewBodyLimit(t *testing.T) {
	for _, size := range []int{7 * 1024 * 1024, 8*1024*1024 + 1} {
		input := `{"padding":"` + strings.Repeat("x", size-14) + `"}`
		r := httptest.NewRequest("POST", "/api/workflows/workflow-one/datasets/generate/preview", strings.NewReader(input))
		_, e := readJSON(r)
		if size < 8*1024*1024 && e != nil {
			t.Fatal("valid preview body below 8 MiB rejected", e)
		}
		if size > 8*1024*1024 {
			status, code, _ := requestFailure(e)
			if e == nil || status != 413 || code != "body_too_large" {
				t.Fatalf("preview body must be bounded at 8 MiB: %v", e)
			}
		}
	}
}

func TestDatasetOnlineEditsBoundOriginalRecords(t *testing.T) {
	p, workflow, _ := onlineDatasetFixture(t)
	contents := "id;name\n0001;" + strings.Repeat("x", 4*1024*1024+8192) + "\n"
	d, e := p.datasetCreate(context.Background(), str(workflow["id"]), "csv-online", "large-record.csv", strings.NewReader(contents))
	if e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, p)
	code, result := call("POST", "/api/datasets/"+str(d["id"])+"/edits", Obj{"base_sha256": d["sha256"], "append_rows": []any{[]any{"0002", "appended"}}})
	if code != 413 || obj(result["error"])["code"] != "dataset_edit_too_large" {
		t.Fatal("edits must bound original CSV records before materializing them", code, obj(result["error"]))
	}
	var count int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM workflow_datasets WHERE project_id=?", p.projectID()).Scan(&count); e != nil || count != 2 {
		t.Fatal("oversized edit persisted", count, e)
	}
}

func TestDatasetOnlineEditsRejectConcurrentDelimiterChange(t *testing.T) {
	p, workflow, _ := onlineDatasetFixture(t)
	update := clone(workflow)
	props := obj(findGraphNode(obj(update["graph"]), "CSVDataSet")["properties"])
	props["dataset_columns"] = []any{"id"}
	obj(props["config"])["variableNames"] = "row_id"
	workflow, e := p.workspaceUpdate("workflows", str(workflow["id"]), update)
	if e != nil {
		t.Fatal(e)
	}
	d, e := p.datasetCreate(context.Background(), str(workflow["id"]), "csv-online", "single.csv", strings.NewReader("id\n0001\n"))
	if e != nil {
		t.Fatal(e)
	}
	target, e := url.Parse(p.Config.ArchiveEndpoint)
	if e != nil {
		t.Fatal(e)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	started, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	defer unblock()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			close(started)
			select {
			case <-resume:
			case <-r.Context().Done():
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer peer.Close()
	p.Config.ArchiveEndpoint = peer.URL
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := p.datasetEdits(ctx, d, Obj{"base_sha256": d["sha256"], "append_rows": []any{[]any{"0002"}}})
		finished <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("source object read did not start")
	}
	update = clone(workflow)
	obj(obj(findGraphNode(obj(update["graph"]), "CSVDataSet")["properties"])["config"])["delimiter"] = ","
	if _, e = p.workspaceUpdate("workflows", str(workflow["id"]), update); e != nil {
		t.Fatal(e)
	}
	unblock()
	select {
	case e = <-finished:
	case <-ctx.Done():
		t.Fatal("edit did not finish")
	}
	status, _, _ := requestFailure(e)
	if e == nil || status != 409 {
		t.Fatalf("edited version silently adopted a new delimiter: %v", e)
	}
}
