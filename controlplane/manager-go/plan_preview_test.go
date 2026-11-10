package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type previewFixture struct {
	plan               Obj
	writes, otherReads atomic.Int32
}

var previewFixtures sync.Map

type previewDriver struct{}
type previewConn struct{ fixture *previewFixture }
type previewRows struct {
	columns []string
	values  []driver.Value
	done    bool
}

func (previewDriver) Open(name string) (driver.Conn, error) {
	value, _ := previewFixtures.Load(name)
	return &previewConn{value.(*previewFixture)}, nil
}
func (c *previewConn) Prepare(string) (driver.Stmt, error) { return nil, io.EOF }
func (c *previewConn) Close() error                        { return nil }
func (c *previewConn) Begin() (driver.Tx, error)           { c.fixture.writes.Add(1); return nil, io.EOF }
func (c *previewConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.fixture.writes.Add(1)
	return nil, io.EOF
}
func (c *previewConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM projects") {
		return &previewRows{columns: []string{"data"}, values: []driver.Value{`{"id":"project-default","archived":false}`}}, nil
	}
	if strings.Contains(query, "FROM sessions") {
		return &previewRows{columns: []string{"csrf", "expires"}}, nil
	}
	if !strings.Contains(query, "FROM plans") {
		c.fixture.otherReads.Add(1)
		return nil, io.EOF
	}
	rows := &previewRows{columns: []string{"data"}}
	if len(args) == 1 && args[0].Value == c.fixture.plan["id"] {
		rows.values = []driver.Value{string(encoded(c.fixture.plan))}
	}
	return rows, nil
}
func (r *previewRows) Columns() []string { return r.columns }
func (r *previewRows) Close() error      { return nil }
func (r *previewRows) Next(out []driver.Value) error {
	if r.done || r.values == nil {
		return io.EOF
	}
	r.done = true
	copy(out, r.values)
	return nil
}
func init() { sql.Register("plan-preview-test", previewDriver{}) }
func planPreviewFixture(t *testing.T, content string) (*Platform, *previewFixture) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	fixture := &previewFixture{plan: Obj{"id": "plan-historical", "name": "历史脚本", "description": "", "sha256": digest([]byte(content)), "size": len(content), "created_at": now(), "workflow_id": "newer-workflow-must-not-be-read"}}
	key := newID("preview-fixture-")
	previewFixtures.Store(key, fixture)
	db, e := sql.Open("plan-preview-test", key)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close(); previewFixtures.Delete(key) })
	plans := filepath.Join(root, "plans")
	os.MkdirAll(filepath.Join(plans, "snapshots"), 0755)
	if e = os.WriteFile(filepath.Join(plans, "snapshots", str(fixture.plan["sha256"])+".jmx"), []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
	return &Platform{Config: Config{PlansDir: plans, AdminToken: "admin-secret"}, DB: &Database{DB: db}}, fixture
}
func previewAPI(t *testing.T, p *Platform, path string, auth bool) (int, Obj, []byte) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("X-Project-ID", defaultProjectID)
	if auth {
		r.Header.Set("Authorization", "Bearer admin-secret")
	}
	w := httptest.NewRecorder()
	Handler{p}.ServeHTTP(w, r)
	raw := w.Body.Bytes()
	m, _ := decode(raw)
	return w.Code, m, raw
}
func TestPlanGraphPreviewUsesImmutableContentAndWritesNothing(t *testing.T) {
	graph := basicWorkflowGraph()
	obj(obj(findGraphNode(graph, "HTTPSamplerProxy")["properties"])["config"])["path"] = "/historical-content"
	content, e := compileWorkflow(graph)
	if e != nil {
		t.Fatal(e)
	}
	p, fixture := planPreviewFixture(t, content)
	path := "/api/plans/plan-historical/graph"
	if code, _, _ := previewAPI(t, p, path, false); code != 401 {
		t.Fatal("anonymous graph preview", code)
	}
	code, first, rawFirst := previewAPI(t, p, path, true)
	if code != 200 {
		t.Fatalf("immutable plan graph preview missing: %d %s", code, rawFirst)
	}
	code, second, rawSecond := previewAPI(t, p, path, true)
	if code != 200 || first["plan_id"] != "plan-historical" || first["sha256"] != fixture.plan["sha256"] || !boolean(first["read_only"]) {
		t.Fatal(code, second)
	}
	if string(rawFirst) != string(rawSecond) {
		t.Fatal("preview node/edge IDs changed between read-only reads")
	}
	sampler := findGraphNode(obj(first["graph"]), "HTTPSamplerProxy")
	if obj(obj(sampler["properties"])["config"])["path"] != "/historical-content" {
		t.Fatal("preview replaced historical JMX with current workflow content")
	}
	if fixture.writes.Load() != 0 || fixture.otherReads.Load() != 0 {
		t.Fatal("preview wrote data or consulted a mutable workflow", fixture.writes.Load(), fixture.otherReads.Load())
	}
}
func TestPlanGraphPreviewConversionFailureLeavesSourceAvailable(t *testing.T) {
	content := `<jmeterTestPlan custom="valid-XML-without-a-workflow"/>`
	p, fixture := planPreviewFixture(t, content)
	status, result, raw := previewAPI(t, p, "/api/plans/plan-historical/graph", true)
	if status != 422 || obj(result["error"])["code"] != "graph_preview_unavailable" {
		t.Fatal("unpreviewable scripts need an explicit source fallback", status, string(raw))
	}
	status, _, raw = previewAPI(t, p, "/api/plans/plan-historical/content", true)
	if status != 200 || string(raw) != content {
		t.Fatal("read-only preview failure changed the original JMX", status, string(raw))
	}
	if fixture.writes.Load() != 0 {
		t.Fatal("failed preview persisted data")
	}
}

func TestPlanPreviewNodeIDsFollowSharedImmutableSnapshot(t *testing.T) {
	content, e := compileWorkflow(basicWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	p, fixture := planPreviewFixture(t, content)
	status, first, _ := previewAPI(t, p, "/api/plans/plan-historical/graph", true)
	if status != 200 {
		t.Fatal(status)
	}
	fixture.plan["id"] = "plan-second-metadata-same-snapshot"
	status, second, _ := previewAPI(t, p, "/api/plans/plan-second-metadata-same-snapshot/graph", true)
	if status != 200 {
		t.Fatal(status)
	}
	if first["plan_id"] == second["plan_id"] || string(encoded(first["graph"])) != string(encoded(second["graph"])) {
		t.Fatal("same SHA snapshot generated different node IDs for two immutable plan records")
	}
}
