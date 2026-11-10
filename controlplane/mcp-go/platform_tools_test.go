package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPlatformParityToolsUseActualAPIContracts(t *testing.T) {
	graph := map[string]any{"nodes": []any{}}
	run := "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name, method, path string
		args, want         map[string]any
	}{
		{"list_datasets", "GET", "/api/datasets?node_id=csv-one&page=2&page_size=3&q=a%26b+%E4%B8%AD&workflow_id=workflow-one", map[string]any{"page": 2, "pageSize": 3, "q": "a&b 中", "workflowId": "workflow-one", "nodeId": "csv-one"}, nil},
		{"get_dataset_catalog", "GET", "/api/datasets/catalog", nil, nil},
		{"download_dataset_template", "POST", "/api/workflows/workflow-one/datasets/template", map[string]any{"workflowId": "workflow-one", "nodeId": "csv-one", "graph": graph}, map[string]any{"node_id": "csv-one", "graph": graph}},
		{"list_api_collection_versions", "GET", "/api/api-collections/collection-one/versions?limit=200&offset=0&view=summary", map[string]any{"collectionId": "collection-one"}, nil},
		{"get_api_collection_version", "GET", "/api/api-collections/collection-one/versions/7", map[string]any{"collectionId": "collection-one", "revision": 7}, nil},
		{"update_cluster", "PUT", "/api/clusters/cluster-aaaaaaaaaaaaaaaaaaaaaaaa", map[string]any{"clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "revision": 0, "name": "cluster", "workerIds": []any{}, "callbackUrl": ""}, map[string]any{"revision": 0, "name": "cluster", "worker_ids": []any{}, "callback_url": ""}},
		{"get_execution_copy_config", "GET", "/api/runs/" + run + "/copy-config", map[string]any{"executionId": run}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if r.ContentLength > 0 {
					json.NewDecoder(r.Body).Decode(&body)
				}
				raw, _ := json.Marshal(tc.want)
				var want map[string]any
				json.Unmarshal(raw, &want)
				if !reflect.DeepEqual(body, want) {
					t.Errorf("payload mismatch: %#v != %#v", body, want)
				}
				io.WriteString(w, `{"ok":true,"content":"\ufeffid\r\n","project_id":"project-one"}`)
			})
			args := map[string]any{"projectId": "project-one"}
			for k, v := range tc.args {
				args[k] = v
			}
			if _, err := m.platformTool(context.Background(), tc.name, args); err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
func TestPlatformSchemasRejectCrossScopeAndInvalidArguments(t *testing.T) {
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_datasets", map[string]any{"projectId": "project-one", "nodeId": "csv-one"}},
		{"list_datasets", map[string]any{"projectId": "project-one", "page": 0}},
		{"list_datasets", map[string]any{"projectId": "project-one", "pageSize": 101}},
		{"download_dataset_template", map[string]any{"projectId": "project-one", "workflowId": "../foreign", "nodeId": "csv-one"}},
		{"download_dataset_template", map[string]any{"projectId": "project-one", "workflowId": "workflow-one", "nodeId": "csv-one", "graph": []any{}}},
		{"get_api_collection_version", map[string]any{"projectId": "project-one", "collectionId": "collection-one", "revision": 0}},
		{"update_cluster", map[string]any{"projectId": "project-one", "clusterId": "cluster-aaaaaaaaaaaaaaaaaaaaaaaa", "revision": -1, "name": "x"}},
		{"get_execution_copy_config", map[string]any{"executionId": "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	} {
		_, err := m.call(context.Background(), tc.name, tc.args)
		if err == nil || !strings.HasPrefix(err.Error(), "Invalid arguments for ") {
			t.Fatalf("invalid %s not rejected: %v", tc.name, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid platform args reached HTTP")
	}
	if len(platformSpecs()) != 7 {
		t.Fatal("platform capability count mismatch")
	}
}
