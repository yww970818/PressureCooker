package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMySQLEditorWireReadPreservesDraftAndPlan(t *testing.T) {
	p, _ := mysqlFixture(t)
	g := basicWorkflowGraph()
	text := strings.Repeat("source\n{{cookie}} 中文 😀", 500)
	obj(obj(obj(arr(g["nodes"])[2])["properties"])["config"])["bodyJson"] = text
	w, e := p.workspaceCreate("workflows", Obj{"name": "读取传输验收", "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	id := str(w["id"])
	var cipher string
	p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", id).Scan(&cipher)
	read := func(path, project string, code int) Obj {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		r.Header.Set("X-Project-ID", project)
		out := httptest.NewRecorder()
		Handler{p}.ServeHTTP(out, r)
		if out.Code != code {
			t.Fatalf("read status %d wanted %d", out.Code, code)
		}
		var value Obj
		if e = json.Unmarshal(out.Body.Bytes(), &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	full := read("/api/workflows/"+id, defaultProjectID, 200)
	packed := read("/api/workflows/"+id+"?view=editor", defaultProjectID, 200)
	if obj(obj(obj(arr(obj(full["graph"])["nodes"])[2])["properties"])["config"])["bodyJson"] != text || full["format"] != nil || packed["format"] != "editor-text-v1" {
		t.Fatal("full and editor contracts mixed")
	}
	read("/api/workflows/"+id+"?view=editor", "project-other", 404)
	var after string
	p.DB.QueryRow("SELECT payload FROM workflows WHERE id=?", id).Scan(&after)
	if after != cipher {
		t.Fatal("editor read changed ciphertext")
	}
	// The immutable preview uses the same opt-in envelope and retains its hash.
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := p.createPlan(Obj{"name": "不可变读取验收", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	pid := str(plan["id"])
	preview := read("/api/plans/"+pid+"/graph", defaultProjectID, 200)
	compact := read("/api/plans/"+pid+"/graph?view=editor", defaultProjectID, 200)
	if preview["sha256"] != plan["sha256"] || compact["format"] != "editor-text-v1" {
		t.Fatal("immutable preview changed")
	}
}

func TestMySQLEditorImportPreservesFullDraftAndOrigin(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	g := basicWorkflowGraph()
	text := strings.Repeat("source\n{{value}} 中文 😀", 500)
	obj(obj(findGraphNode(g, "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] = text
	content, e := compileWorkflow(g)
	if e != nil {
		t.Fatal(e)
	}
	plan, e := p.createPlan(Obj{"name": "精确导入来源", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	status, wire := call("POST", "/api/workflows/import?view=editor", Obj{"name": "精简编辑导入", "plan_id": plan["id"]})
	if status != 201 || wire["format"] != "editor-text-v1" {
		t.Fatal("editor import did not return pooled parameters", status)
	}
	data := obj(wire["data"])
	stored, e := p.workspaceGet("workflows", str(data["id"]))
	if e != nil {
		t.Fatal(e)
	}
	if stored["source_plan_id"] != plan["id"] || stored["format"] != nil || number(stored["revision"]) != 1 || obj(obj(findGraphNode(obj(stored["graph"]), "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] != text {
		t.Fatal("compact response changed the persisted draft or its source")
	}
	requireLink(t, apiItems(t, call, "/api/workflows/"+str(stored["id"])+"/plans"), str(plan["id"]), str(stored["id"]), 1, "imported")
	status, plain := call("POST", "/api/workflows/import", Obj{"name": "普通导入", "plan_id": plan["id"]})
	if status != 201 || plain["format"] != nil || plain["graph"] == nil || plain["id"] == stored["id"] {
		t.Fatal("normal import contract changed")
	}
	status, outline := call("POST", "/api/workflows/import?view=outline", Obj{"name": "按需读取导入", "plan_id": plan["id"]})
	if status != 201 || obj(findGraphNode(obj(outline["graph"]), "HTTPSamplerProxy")["properties"])["editor_lazy"] != true {
		t.Fatal("outline import returned complete parameters", status)
	}
	persisted, e := p.workspaceGet("workflows", str(outline["id"]))
	if e != nil || persisted["source_plan_id"] != plan["id"] || obj(obj(findGraphNode(obj(persisted["graph"]), "HTTPSamplerProxy")["properties"])["config"])["bodyJson"] != text {
		t.Fatal("outline import lost persisted source", e)
	}
}

// Only the changed editor path; no scheduler, business endpoint, million-JTL or
// history fixture. Two servers share data but serve old/new compiled UI assets.
func TestWorkflowEditorLoadFixture(t *testing.T) {
	if os.Getenv("JMETER_WORKFLOW_LOAD_ACCEPTANCE") != "1" {
		t.Skip("opt-in editor fixture")
	}
	p, _ := mysqlFixture(t)
	g := capacityGraph(5000)
	for i, item := range arr(g["nodes"]) {
		n := obj(item)
		n["x"] = 880
		n["y"] = 100 + i*114
		if i < 2 {
			n["x"] = 320 + i*280
			n["y"] = 100
		}
		props := obj(n["properties"])
		if props["component"] == "HTTPSamplerProxy" {
			config := obj(props["config"])
			if config == nil {
				config = Obj{}
				props["config"] = config
			}
			config["bodyJson"] = strings.Repeat("synthetic", 450)
		}
	}
	w, e := p.workspaceCreate("workflows", Obj{"name": "首次加载验收 · 5000节点", "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	p.Config.FrontendDir = filepath.Join("..", "frontend", "dist")
	old := *p
	old.Config.FrontendDir = os.Getenv("JMETER_WORKFLOW_LOAD_BASELINE")
	if old.Config.FrontendDir == "" {
		old.Config.FrontendDir = "/private/tmp/pc-followup-before-dist"
	}
	// Reproduce the previous read chain for a fair old/new backend comparison;
	// both servers use the same saved revision and perform real authentication.
	previousHandler := http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/workflows/"+str(w["id"]) && r.URL.Query().Get("view") == "editor" {
			if _, e := old.authenticate(r, false); e != nil {
				http.Error(out, "fixture authentication failed", 401)
				return
			}
			scoped := old
			scoped.ProjectID = r.Header.Get("X-Project-ID")
			if scoped.ProjectID == "" {
				scoped.ProjectID = defaultProjectID
			}
			value, e := scoped.workspaceGet("workflows", str(w["id"]))
			if e != nil {
				http.Error(out, "fixture read failed", 500)
				return
			}
			sendJSON(out, r, 200, editorReadPayload(value))
			return
		}
		Handler{&old}.ServeHTTP(out, r)
	})
	current, previous := httptest.NewServer(Handler{p}), httptest.NewServer(previousHandler)
	defer current.Close()
	defer previous.Close()
	out := os.Getenv("JMETER_WORKFLOW_LOAD_OUTPUT")
	if e = os.WriteFile(out, encoded(Obj{"after_url": current.URL, "before_url": previous.URL, "workflow_id": w["id"]}), 0600); e != nil {
		t.Fatal(e)
	}
	t.Log("editor fixture ready")
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		if _, e = os.Stat(out + ".stop"); e == nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("fixture stop signal not received")
}

func TestMySQLWorkflowEditorProjectionPreservesSourceAndRefreshes(t *testing.T) {
	p, _ := mysqlFixture(t)
	g := basicWorkflowGraph()
	obj(obj(obj(arr(g["nodes"])[2])["properties"])["config"])["bodyJson"] = strings.Repeat("原文 {{cookie}} 😀", 500)
	w, e := p.workspaceCreate("workflows", Obj{"name": "派生读取验收", "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	id := str(w["id"])
	var cipher, version, projection string
	var rev int64
	if e = p.DB.QueryRow("SELECT payload,editor_payload,editor_revision FROM workflows WHERE id=?", id).Scan(&cipher, &projection, &rev); e != nil {
		t.Fatal(e)
	}
	if rev != 1 || !strings.HasPrefix(projection, "gcm1:") {
		t.Fatal("create did not persist encrypted revision-scoped read projection")
	}
	p.DB.QueryRow("SELECT payload FROM workflow_versions WHERE object_id=? AND revision=1", id).Scan(&version)
	read := func(project string, code int) Obj {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/workflows/"+id+"?view=editor", nil)
		r.Header.Set("Authorization", "Bearer admin-secret")
		r.Header.Set("X-Project-ID", project)
		out := httptest.NewRecorder()
		Handler{p}.ServeHTTP(out, r)
		if out.Code != code {
			t.Fatalf("editor status %d want %d", out.Code, code)
		}
		var value Obj
		json.Unmarshal(out.Body.Bytes(), &value)
		return value
	}
	first := read(defaultProjectID, 200)
	if string(encoded(first)) != string(encoded(editorReadPayload(w))) {
		t.Fatal("projection differs from original editor response")
	}
	read("project-other", 404)
	// A broken optional projection must not hide the original authored document.
	p.DB.Exec("UPDATE workflows SET editor_payload='broken' WHERE id=?", id)
	if string(encoded(read(defaultProjectID, 200))) != string(encoded(first)) {
		t.Fatal("corrupt projection did not fall back to original")
	}
	// Reproduce the actual deployed v4 schema, then upgrade and backfill.
	if _, e = p.DB.Exec("ALTER TABLE workflows DROP COLUMN editor_payload, DROP COLUMN editor_revision"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.DB.Exec("DELETE FROM schema_migrations WHERE version>=5"); e != nil {
		t.Fatal(e)
	}
	restarted, e := newPlatform(p.Config)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	var after, history string
	p.DB.QueryRow("SELECT payload,editor_payload,editor_revision FROM workflows WHERE id=?", id).Scan(&after, &projection, &rev)
	p.DB.QueryRow("SELECT payload FROM workflow_versions WHERE object_id=? AND revision=1", id).Scan(&history)
	if after != cipher || history != version || rev != 1 || !strings.HasPrefix(projection, "gcm1:") {
		t.Fatal("backfill changed authored bytes or did not rebuild the cache")
	}
	w, e = p.workspaceUpdate("workflows", id, Obj{"name": "修订后", "description": "", "revision": 1, "graph": g})
	if e != nil {
		t.Fatal(e)
	}
	p.DB.QueryRow("SELECT editor_revision FROM workflows WHERE id=?", id).Scan(&rev)
	if rev != 2 || string(encoded(read(defaultProjectID, 200))) != string(encoded(editorReadPayload(w))) {
		t.Fatal("save returned stale editor revision")
	}
	// A stale cache cannot be read even if its encrypted payload is otherwise valid.
	p.DB.Exec("UPDATE workflows SET editor_revision=1 WHERE id=?", id)
	if string(encoded(read(defaultProjectID, 200))) != string(encoded(editorReadPayload(w))) {
		t.Fatal("stale projection was served")
	}
	if _, e = p.workspaceDelete("workflows", id); e != nil {
		t.Fatal(e)
	}
	read(defaultProjectID, 404)
}
