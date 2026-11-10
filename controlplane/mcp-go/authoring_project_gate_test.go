package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAuthoringRejectsMissingAndArchivedProjectsBeforeCreatingDraft(t *testing.T) {
	var projectReads atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectReads.Add(1)
		project := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		if r.Method != http.MethodGet || r.Header.Get("X-Project-ID") != project {
			t.Error("authoring project check was not scoped")
		}
		if project == "project-missing" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"error":{"code":"not_found","message":"Project unavailable"}}`)
			return
		}
		if project == "project-unavailable" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if project == "project-invalid" {
			json.NewEncoder(w).Encode(map[string]any{"id": "project-other", "archived": false})
			return
		}
		if project == "project-unconfirmed" {
			json.NewEncoder(w).Encode(map[string]any{"id": project})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": project, "archived": true})
	}))
	defer api.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	client := scopedProxyFixture(t, workspace, managerClient{baseURL: api.URL, tokenFile: token})
	for _, project := range []string{"project-missing", "project-archived", "project-unavailable", "project-invalid", "project-unconfirmed"} {
		result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_test_plan", Arguments: map[string]any{"projectId": project, "name": "blocked draft"}})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("draft created for unavailable project %s", project)
		}
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "draft-projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored draftProjectIndex
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Owners) != 0 {
		t.Error("denied project mutation persisted draft ownership")
	}
	if projectReads.Load() != 5 {
		t.Error("project existence and archive state were not checked before authoring")
	}
}

func TestAuthoringArchiveRetainsReadsAndRejectsEditsAcrossRestart(t *testing.T) {
	var archived, missing atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/projects" && r.Method == http.MethodGet:
			io.WriteString(w, `{"items":[],"total":0}`)
		case r.URL.Path == "/api/projects" && r.Method == http.MethodPost:
			io.WriteString(w, `{"id":"project-created","archived":false}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/projects/"):
			project := strings.TrimPrefix(r.URL.Path, "/api/projects/")
			if r.Header.Get("X-Project-ID") != project {
				t.Error("project check lost calling scope")
			}
			if missing.Load() && project == "project-one" {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"error":{"code":"not_found"}}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": project, "archived": archived.Load() && project == "project-one"})
		default:
			t.Errorf("unexpected manager request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := managerClient{baseURL: api.URL, tokenFile: token}
	workspace := t.TempDir()
	client := scopedProxyFixture(t, workspace, manager)
	plan := callProjectTool(t, client, "create_test_plan", map[string]any{"projectId": "project-one", "name": "preserved draft"}, false).(map[string]any)
	archived.Store(true)
	callProjectTool(t, client, "rename_element", map[string]any{"projectId": "project-one", "planId": plan["planId"], "nodeId": plan["rootNodeId"], "name": "denied edit"}, true)
	read := callProjectTool(t, client, "get_test_plan", map[string]any{"projectId": "project-one", "planId": plan["planId"]}, false).(map[string]any)
	if read["name"] != "preserved draft" {
		t.Fatal("archive edit changed draft", read["name"])
	}
	own := callProjectTool(t, client, "list_test_plans", map[string]any{"projectId": "project-one"}, false).([]any)
	if len(own) != 1 {
		t.Fatal("archive list lost own draft")
	}
	callProjectTool(t, client, "get_test_plan_xml", map[string]any{"projectId": "project-one", "planId": plan["planId"]}, false)
	callProjectTool(t, client, "get_test_plan", map[string]any{"projectId": "project-two", "planId": plan["planId"]}, true)
	callProjectTool(t, client, "list_projects", nil, false)
	callProjectTool(t, client, "create_project", map[string]any{"name": "new project"}, false)
	client.Close()
	restarted := scopedProxyFixture(t, workspace, manager)
	callProjectTool(t, restarted, "get_test_plan_xml", map[string]any{"projectId": "project-one", "planId": plan["planId"]}, false)
	missing.Store(true)
	callProjectTool(t, restarted, "get_test_plan", map[string]any{"projectId": "project-one", "planId": plan["planId"]}, true)
	callProjectTool(t, restarted, "list_test_plans", map[string]any{"projectId": "project-one"}, true)
}
