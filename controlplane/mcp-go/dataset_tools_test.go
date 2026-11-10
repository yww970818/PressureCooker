package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDatasetMCPParityAndDownloadKeepsBytesOutOfMessages(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("test-token"), 0600); e != nil {
		t.Fatal(e)
	}
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-Project-ID") != "project-one" {
			t.Error("scope/auth missing")
		}
		switch r.URL.Path {
		case "/api/workflows/workflow-one/datasets":
			if r.Method != "POST" {
				t.Fatal(r.Method)
			}
			if e := r.ParseMultipartForm(1024); e != nil {
				t.Error(e)
			}
			if r.FormValue("node_id") != "csv-one" || r.FormValue("name") != "sample.csv" {
				t.Error("multipart fields lost")
			}
			f, _, e := r.FormFile("file")
			if e != nil {
				t.Error(e)
			} else {
				defer f.Close()
				data, _ := io.ReadAll(f)
				if string(data) != "id\n1\n" {
					t.Error("CSV changed")
				}
			}
		case "/api/workflows/workflow-one/datasets/generate":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["node_id"] != "csv-one" || body["rows"] != float64(2) || body["nodeId"] != nil || body["workflowId"] != nil {
				t.Error("generation mapping", body)
			}
		case "/api/datasets/dataset-one":
			if r.Method != "GET" {
				t.Error(r.Method)
			}
		default:
			t.Error("unexpected route", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"dataset-one","rows":2}`)
	}))
	defer api.Close()
	tools := managerTools{api: managerClient{baseURL: api.URL, tokenFile: token, client: api.Client()}}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{{"upload_dataset", map[string]any{"workflowId": "workflow-one", "nodeId": "csv-one", "name": "sample.csv", "content": "id\n1\n"}}, {"generate_dataset", map[string]any{"workflowId": "workflow-one", "nodeId": "csv-one", "rows": 2, "columns": []any{map[string]any{"name": "id", "generator": "sequence"}}}}, {"download_dataset", map[string]any{"datasetId": "dataset-one"}}} {
		tc.args["projectId"] = "project-one"
		result, e := tools.call(context.Background(), tc.name, tc.args)
		if e != nil {
			t.Fatal(tc.name, e)
		}
		if tc.name == "download_dataset" {
			m := result.(map[string]any)
			if m["content"] != nil || m["download_url"] != "/api/datasets/dataset-one/content?project_id=project-one" || m["requires_authentication"] != true {
				t.Fatal("raw dataset entered MCP", m)
			}
		}
	}
	if requests != 3 {
		t.Fatal(requests)
	}
	if _, e := tools.call(context.Background(), "preview_dataset", map[string]any{"projectId": "project-one", "datasetId": "../escape"}); e == nil {
		t.Fatal("unsafe dataset id accepted")
	}
}
