package main

import (
	"bytes"
	"database/sql"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workflowPreviewRequest(p *Platform, graph Obj, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/workflows/preview", bytes.NewReader(encoded(Obj{"graph": graph})))
	r.Header.Set("X-Project-ID", defaultProjectID)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	return w
}
func TestUnsavedWorkflowSourcePreviewCompilesWithoutPersistence(t *testing.T) {
	old, e := compileWorkflow(basicWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	p, fixture := planPreviewFixture(t, old)
	graph := basicWorkflowGraph()
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["path"] = "/unsaved-local-only"
	before, e := os.ReadDir(filepath.Join(p.Config.PlansDir, "snapshots"))
	if e != nil {
		t.Fatal(e)
	}
	response := workflowPreviewRequest(p, graph, map[string]string{"Authorization": "Bearer admin-secret"})
	if response.Code != 200 {
		t.Fatalf("unsaved graph XML preview missing: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Content-Type"), "application/xml") || !strings.Contains(response.Body.String(), "/unsaved-local-only") {
		t.Fatal("preview did not compile current unsaved graph", response.Header(), response.Body.String())
	}
	after, _ := os.ReadDir(filepath.Join(p.Config.PlansDir, "snapshots"))
	if fixture.writes.Load() != 0 || fixture.otherReads.Load() != 0 || len(before) != len(after) {
		t.Fatal("preview persisted a draft, plan or file")
	}
	source, _ := os.ReadFile(filepath.Join(p.Config.PlansDir, "snapshots", str(fixture.plan["sha256"])+".jmx"))
	if string(source) != old {
		t.Fatal("unsaved preview replaced an immutable source file")
	}
	invalid := workflowPreviewRequest(p, Obj{"nodes": []any{}, "edges": []any{}}, map[string]string{"Authorization": "Bearer admin-secret"})
	errorBody, _ := decode(invalid.Body.Bytes())
	if invalid.Code != 400 || obj(errorBody["error"])["code"] != "invalid_graph" || fixture.writes.Load() != 0 {
		t.Fatal("invalid graph preview must validate without persisting", invalid.Code, invalid.Body.String())
	}
	if obj(openAPI()["paths"])["/api/workflows/preview"] == nil {
		t.Fatal("draft preview missing from API discovery")
	}
}
func TestWorkflowSourcePreviewUsesStandardSessionCSRF(t *testing.T) {
	db, e := sql.Open("proxy-test-session", "")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	p := &Platform{Config: Config{AdminToken: "admin-secret", SessionKey: "proxy-session-key"}, DB: &Database{DB: db}}
	graph := basicWorkflowGraph()
	for _, test := range []struct {
		headers map[string]string
		status  int
	}{{map[string]string{}, 401}, {map[string]string{"Cookie": "jmeter_session=valid-cookie"}, 403}, {map[string]string{"Cookie": "jmeter_session=valid-cookie", "X-JMeter-CSRF-Token": "csrf-secret"}, 403}, {map[string]string{"Cookie": "jmeter_session=valid-cookie", "X-CSRF-Token": "csrf-secret"}, 200}} {
		response := workflowPreviewRequest(p, graph, test.headers)
		if response.Code != test.status {
			t.Fatal("changed standard workspace authentication/CSRF", response.Code, response.Body.String())
		}
	}
}
