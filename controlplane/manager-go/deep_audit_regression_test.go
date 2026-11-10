package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentDurationRejectsNonFiniteOverflowAndSubNanosecond(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "Inf", "-Inf", "1e20", "9223372037", "0.0000000001", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("PC_AUDIT_DURATION", value)
			if duration, err := envDuration("PC_AUDIT_DURATION", 3); err == nil {
				t.Fatalf("invalid duration %q accepted as %s", value, duration)
			}
		})
	}
	for _, value := range []string{"0.000000001", "0.125", "15", "9223372036"} {
		t.Run("valid_"+value, func(t *testing.T) {
			t.Setenv("PC_AUDIT_DURATION", value)
			if duration, err := envDuration("PC_AUDIT_DURATION", 3); err != nil || duration <= 0 {
				t.Fatalf("valid duration %q rejected: %s %v", value, duration, err)
			}
		})
	}
}

func TestWorkflowImportRejectsCorruptWebSocketDeclaration(t *testing.T) {
	content, err := compileWorkflow(websocketWorkflowGraph())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "%%%", base64.StdEncoding.EncodeToString([]byte("not-json")), base64.StdEncoding.EncodeToString([]byte("null")), base64.StdEncoding.EncodeToString([]byte("[]")), base64.StdEncoding.EncodeToString([]byte(`{"action":"unsupported"}`))} {
		t.Run(value, func(t *testing.T) {
			roots, err := parseXMLFragment(content)
			if err != nil {
				t.Fatal(err)
			}
			var damage func(*xmlElement)
			damage = func(e *xmlElement) {
				if websocketComponent(e) {
					e.setProp("stringProp", websocketAuthoredProp, value)
				}
				for _, child := range e.children() {
					damage(child)
				}
			}
			damage(roots[0])
			if _, err := importWorkflowGraph(elementXML(roots[0])); err == nil {
				t.Fatal("corrupt authored declaration silently replaced with default configuration")
			}
		})
	}
}

func TestWorkflowWebSocketDeclarationWithoutAuthoredPropertyUsesExecutableConfig(t *testing.T) {
	content, err := compileWorkflow(websocketWorkflowGraph())
	if err != nil {
		t.Fatal(err)
	}
	roots, err := parseXMLFragment(content)
	if err != nil {
		t.Fatal(err)
	}
	var removeAuthored func(*xmlElement)
	removeAuthored = func(e *xmlElement) {
		if websocketComponent(e) {
			e.removeProp(websocketAuthoredProp)
		}
		for _, child := range e.children() {
			removeAuthored(child)
		}
	}
	removeAuthored(roots[0])
	graph, err := importWorkflowGraph(elementXML(roots[0]))
	if err != nil {
		t.Fatal("a valid declared executable configuration must remain readable", err)
	}
	if _, err = compileWorkflow(graph); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowCompileRejectsCorruptWebSocketRawDeclaration(t *testing.T) {
	graph := websocketWorkflowGraph()
	content, err := compileWorkflow(graph)
	if err != nil {
		t.Fatal(err)
	}
	graph, err = importWorkflowGraph(content)
	if err != nil {
		t.Fatal(err)
	}
	node := findGraphNode(graph, "WebSocketSampler")
	props := obj(node["properties"])
	roots, err := parseXMLFragment(str(props["raw_xml"]))
	if err != nil {
		t.Fatal(err)
	}
	roots[0].setProp("stringProp", websocketAuthoredProp, "%%%")
	props["raw_xml"] = elementXML(roots[0])
	if _, err := compileWorkflow(graph); err == nil {
		t.Fatal("compilation silently overwrote a corrupt WebSocket declaration")
	}
}

type usageAuditRecord struct {
	id, payload string
}
type usageAuditConnector struct{ records []usageAuditRecord }
type usageAuditDriver struct{ fixture *usageAuditConnector }
type usageAuditConn struct{ fixture *usageAuditConnector }
type usageAuditRows struct{ records []usageAuditRecord }

func (c *usageAuditConnector) Connect(context.Context) (driver.Conn, error) {
	return &usageAuditConn{c}, nil
}
func (c *usageAuditConnector) Driver() driver.Driver { return usageAuditDriver{c} }
func (d usageAuditDriver) Open(string) (driver.Conn, error) {
	return &usageAuditConn{d.fixture}, nil
}
func (*usageAuditConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*usageAuditConn) Close() error                        { return nil }
func (*usageAuditConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *usageAuditConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !strings.Contains(query, "FROM workflows") || len(args) == 0 || args[0].Value != "project-audit" {
		return nil, fmt.Errorf("usage scan lost its project scope")
	}
	records := c.fixture.records
	if strings.Contains(query, "ORDER BY updated_at") {
		// The old ordinary list omits the oldest five workflows.
		return &usageAuditRows{records: append([]usageAuditRecord{}, records[5:]...)}, nil
	}
	if len(args) < 2 {
		return nil, fmt.Errorf("usage scan must use a bounded ID cursor")
	}
	cursor, ok := args[1].Value.(string)
	if !ok {
		return nil, fmt.Errorf("invalid usage cursor")
	}
	batch := []usageAuditRecord{}
	for _, record := range records {
		if record.id > cursor {
			batch = append(batch, record)
			if len(batch) == 32 {
				break
			}
		}
	}
	return &usageAuditRows{records: batch}, nil
}
func (*usageAuditRows) Columns() []string { return []string{"id", "revision", "payload"} }
func (*usageAuditRows) Close() error      { return nil }
func (r *usageAuditRows) Next(out []driver.Value) error {
	if len(r.records) == 0 {
		return io.EOF
	}
	record := r.records[0]
	r.records = r.records[1:]
	out[0], out[1], out[2] = record.id, int64(1), record.payload
	return nil
}

func TestWorkflowExtensionUsageIncludesOlderThanFirst200Workflows(t *testing.T) {
	fixture := &usageAuditConnector{}
	p := &Platform{ProjectID: "project-audit", Config: Config{SessionKey: "synthetic-audit-key"}}
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("workflow-%03d", i)
		component := "HTTPSamplerProxy"
		if i == 0 {
			component = "fixture.OnlyOlder"
		}
		value := Obj{"id": id, "name": id, "revision": 1, "graph": Obj{"nodes": []any{Obj{"properties": Obj{"component": component}}}}}
		payload, err := p.sealWorkspace("workflows", id, 1, value)
		if err != nil {
			t.Fatal(err)
		}
		fixture.records = append(fixture.records, usageAuditRecord{id, payload})
	}
	db := sql.OpenDB(fixture)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	p.DB = &Database{DB: db}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	usage, err := p.workflowExtensionUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	total, older := 0, false
	for _, raw := range arr(usage["items"]) {
		item := obj(raw)
		total += int(number(item["node_count"]))
		older = older || item["component"] == "fixture.OnlyOlder"
	}
	if total != 205 || !older {
		t.Fatalf("usage omitted persisted workflows: counted=%d older=%v", total, older)
	}
}

func TestWorkflowExtensionUsageRespectsCancelledContext(t *testing.T) {
	db := sql.OpenDB(&usageAuditConnector{})
	t.Cleanup(func() { db.Close() })
	p := &Platform{DB: &Database{DB: db}, ProjectID: "project-audit"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.workflowExtensionUsage(ctx); err != context.Canceled {
		t.Fatal("usage scan ignored cancellation", err)
	}
}

func TestReportedRedisRecoveryDoesNotRequireDefaultProject(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	project, err := p.projectCreate(Obj{"name": "Only project"})
	if err != nil {
		t.Fatal(err)
	}
	// A restored installation may contain only explicitly owned projects. Remove
	// this fixture's empty bootstrap data without leaving orphan plan ownership.
	if _, err = p.DB.Exec("DELETE FROM plans WHERE project_id=?", defaultProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DB.Exec("DELETE FROM projects WHERE id=?", defaultProjectID); err != nil {
		t.Fatal(err)
	}
	p = p.forProject(str(project["id"]))
	plan, err := p.createPlan(Obj{"name": "synthetic recovery", "content": "<jmeterTestPlan/>"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := p.workspaceCreate("environments", Obj{"name": "local", "base_host": "http://fixture.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	run := registeredRecoveryRun(t, p, plan, environment)
	q, err := p.runnerQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	stop := startRecoveryEpochPublisher(t, q, recoveryWorkerReport)
	defer stop()
	scheduler := newScheduler(p, p.Config.ManagerID)
	defer scheduler.close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = scheduler.resolveRedisOwnerContext(ctx, run); err != nil {
		t.Fatal("recovery depended on the default project instead of platform execution lock", err)
	}
	stored, err := p.get("runs", str(run["id"]))
	if err != nil || stored["status"] != "INTERRUPTED" || stored["runner_reconciled"] != true {
		t.Fatal("stale owner was not resolved", stored, err)
	}
}

func TestClusterMembersUseRegisteredGenericNodeIDs(t *testing.T) {
	known := map[string]string{}
	members := []any{}
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("load-node-%02d", i)
		known[id] = "http://fixture.invalid"
		members = append(members, id)
	}
	if actual, err := clusterMembers(members, known); err != nil || len(actual) != 64 {
		t.Fatal("registered nodes are constrained by the default deployment", len(actual), err)
	}
	if err := validateNodeInventories(nil, known); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]any{{"unregistered"}, {"load-node-00", "load-node-00"}, append(append([]any{}, members...), "extra")} {
		if _, err := clusterMembers(invalid, known); err == nil {
			t.Fatal("invalid membership accepted")
		}
	}
	known["one-more-node"] = "http://fixture.invalid"
	if err := validateNodeInventories(nil, known); err == nil {
		t.Fatal("Core cannot acknowledge an inventory larger than its registration protocol")
	}
	if err := validateNodeInventories(map[string]string{"../invalid": "http://fixture.invalid"}, nil); err == nil {
		t.Fatal("invalid node identity accepted")
	}
}

func TestDefaultClusterMappingDoesNotGuessDeploymentTopology(t *testing.T) {
	controllers := map[string]string{"core-a": "http://a", "core-b": "http://b"}
	workers := map[string]string{}
	for i := 0; i < 6; i++ {
		workers[fmt.Sprintf("load-%d", i)] = "http://fixture.invalid"
	}
	groups, err := parseDefaultClusterWorkers("", false, controllers, workers)
	if err != nil || len(groups) != 0 {
		t.Fatal("inventory size must not imply a deployment partition", groups, err)
	}
}

func TestMySQLClusterGenericNodesFreezeAtSubmission(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.ControllerURLs = map[string]string{"core-custom": "http://127.0.0.1:1"}
	p.Config.WorkerURLs = map[string]string{}
	workers := []any{}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("load-region-%02d", i)
		p.Config.WorkerURLs[id] = "http://127.0.0.1:1"
		workers = append(workers, id)
	}
	cluster := clusterID("core-custom")
	if _, err := p.updateCluster(cluster, Obj{"revision": 0, "name": "generic", "worker_ids": workers}); err != nil {
		t.Fatal(err)
	}
	run, _, err := p.createRun(Obj{"name": "generic nodes", "plan_id": plan["id"], "environment_id": fixtureEnvironmentID(t, p), "cluster_id": cluster}, "generic-nodes")
	if err != nil || len(arr(run["workers"])) != 7 || len(arr(obj(run["cluster_snapshot"])["worker_ids"])) != 7 {
		t.Fatal("generic membership did not reach the frozen execution snapshot", run, err)
	}
	if _, err := p.updateCluster(cluster, Obj{"revision": 1, "name": "generic", "worker_ids": []any{workers[0]}}); err == nil {
		t.Fatal("queued task no longer protects its cluster membership")
	}
	if _, _, err := p.createRun(Obj{"name": "unregistered", "plan_id": plan["id"], "environment_id": fixtureEnvironmentID(t, p), "cluster_id": cluster, "workers": []any{"unregistered"}}, "generic-rejected"); err == nil {
		t.Fatal("unregistered node accepted")
	}
}
