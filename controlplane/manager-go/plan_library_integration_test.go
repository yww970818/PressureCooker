package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func apiItems(t *testing.T, call func(string, string, Obj) (int, Obj), path string) []any {
	t.Helper()
	status, value := call("GET", path, nil)
	if status != 200 {
		t.Fatalf("missing library links endpoint %s: %d %s", path, status, encoded(value))
	}
	return arr(value["items"])
}
func requireLink(t *testing.T, items []any, planID, workflowID string, revision int64, relation string) Obj {
	t.Helper()
	for _, value := range items {
		link := obj(value)
		rev, _ := integer(link["workflow_revision"])
		if link["plan_id"] == planID && link["workflow_id"] == workflowID && rev == revision && link["relation"] == relation {
			return link
		}
	}
	t.Fatalf("missing exact immutable association %s/%s/%d/%s in %s", planID, workflowID, revision, relation, encoded(items))
	return nil
}
func TestMySQLPlanWorkflowLinksAcrossRevisionsAndClones(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	graph := basicWorkflowGraph()
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "原流程", "graph": graph})
	if e != nil {
		t.Fatal(e)
	}
	wid := str(workflow["id"])
	first, e := p.publishWorkflow(wid, Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	firstID := str(first["managerPlanId"])
	links := apiItems(t, call, "/api/plans/"+firstID+"/workflows")
	requireLink(t, links, firstID, wid, 1, "published")
	updated, e := p.workspaceUpdate("workflows", wid, Obj{"name": "原流程", "description": "仅元数据升级，JMX内容相同", "graph": graph, "revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	second, e := p.publishWorkflow(wid, Obj{"revision": updated["revision"]})
	if e != nil {
		t.Fatal(e)
	}
	secondID := str(second["managerPlanId"])
	if firstID == secondID || obj(first["plan"])["sha256"] != obj(second["plan"])["sha256"] {
		t.Fatal("changed the physical-content versus immutable-plan ID contract")
	}
	for _, name := range []string{"精确副本一", "精确副本二"} {
		status, cloned := call("POST", "/api/workflows/import", Obj{"name": name, "plan_id": firstID})
		if status != 201 {
			t.Fatal(status, cloned)
		}
		if cloned["id"] == wid {
			t.Fatal("editing an immutable plan opened a mutable existing draft")
		}
		clonedLinks := apiItems(t, call, "/api/workflows/"+str(cloned["id"])+"/plans")
		link := requireLink(t, clonedLinks, firstID, str(cloned["id"]), 1, "imported")
		if obj(link["plan"])["id"] != firstID || obj(link["plan"])["sha256"] != obj(first["plan"])["sha256"] {
			t.Fatal("workflow links omitted the full immutable plan metadata", link)
		}
	}
	links = apiItems(t, call, "/api/plans/"+firstID+"/workflows")
	if len(links) != 3 {
		t.Fatal("same-content clone origins were overwritten", encoded(links))
	}
	original := requireLink(t, links, firstID, wid, 1, "published")
	if number(original["workflow_current_revision"]) != 2 || boolean(original["workflow_deleted"]) {
		t.Fatal("old publication was confused with current draft", original)
	}
	workflowLinks := apiItems(t, call, "/api/workflows/"+wid+"/plans")
	requireLink(t, workflowLinks, firstID, wid, 1, "published")
	requireLink(t, workflowLinks, secondID, wid, 2, "published")
	if _, e = p.workspaceDelete("workflows", wid); e != nil {
		t.Fatal(e)
	}
	links = apiItems(t, call, "/api/plans/"+firstID+"/workflows")
	if !boolean(requireLink(t, links, firstID, wid, 1, "published")["workflow_deleted"]) {
		t.Fatal("deleted origin disappeared or looked editable")
	}
	deletedHistory := apiItems(t, call, "/api/workflows/"+wid+"/plans")
	if len(deletedHistory) != 2 {
		t.Fatal("soft deletion discarded the publication history")
	}
}
func libraryTableCounts(t *testing.T, p *Platform) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range append(append(append([]string{}, portableTables...), derivedTables...), "sessions", "instances") {
		var count int
		if e := p.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil {
			t.Fatal(e)
		}
		counts[table] = count
	}
	return counts
}
func TestMySQLPlanHistoryPreviewNeverUsesLatestDraftOrWritesRows(t *testing.T) {
	p, _ := mysqlFixture(t)
	request, call := workspaceHTTP(t, p)
	beforeGraph := basicWorkflowGraph()
	obj(obj(findGraphNode(beforeGraph, "HTTPSamplerProxy")["properties"])["config"])["path"] = "/historical-before"
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "历史内容", "graph": beforeGraph})
	if e != nil {
		t.Fatal(e)
	}
	wid := str(workflow["id"])
	first, e := p.publishWorkflow(wid, Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	pid := str(first["managerPlanId"])
	afterGraph := clone(beforeGraph)
	obj(obj(findGraphNode(afterGraph, "HTTPSamplerProxy")["properties"])["config"])["path"] = "/latest-after"
	if _, e = p.workspaceUpdate("workflows", wid, Obj{"name": "当前草稿", "graph": afterGraph, "revision": 1}); e != nil {
		t.Fatal(e)
	}
	otherProject, e := p.projectCreate(Obj{"name": "预览隔离项目"})
	if e != nil {
		t.Fatal(e)
	}
	plan, e := p.get("plans", pid)
	if e != nil {
		t.Fatal(e)
	}
	planSHA := str(plan["sha256"])
	planFile := filepath.Join(p.Config.PlansDir, "snapshots", planSHA+".jmx")
	beforeFile, e := fileHash(planFile)
	if e != nil {
		t.Fatal(e)
	}
	beforeState, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	delete(beforeState, "created_at")
	before := libraryTableCounts(t, p)
	var previous []byte
	cachePayload := ""
	for i := 0; i < 2; i++ {
		status, preview := call("GET", "/api/plans/"+pid+"/graph", nil)
		if status != 200 || preview["read_only"] != true {
			t.Fatal(status, preview)
		}
		node := findGraphNode(obj(preview["graph"]), "HTTPSamplerProxy")
		if obj(obj(node["properties"])["config"])["path"] != "/historical-before" {
			t.Fatal("historical preview displayed a newer workflow")
		}
		raw := encoded(preview)
		if i > 0 && string(raw) != string(previous) {
			t.Fatal("graph IDs were unstable")
		}
		previous = raw
		var projectID, sha, payload string
		var format int
		if e := p.DB.QueryRow("SELECT project_id,sha256,format_version,payload FROM plan_graph_cache WHERE id=?", pid).Scan(&projectID, &sha, &format, &payload); e != nil {
			t.Fatal(e)
		}
		if projectID != p.projectID() || sha != planSHA || format != 1 || !strings.HasPrefix(payload, "gcm1:") || strings.Contains(payload, "/historical-before") {
			t.Fatal("preview cache was not scoped encrypted data")
		}
		header, e := p.openWorkspace("plan-cache/"+planSHA, pid, 1, payload)
		if e != nil {
			t.Fatal(e)
		}
		cached, e := unpackReadModel(str(header["packed"]))
		if e != nil || string(encoded(cached)) != string(raw) {
			t.Fatal("derived cache differs from exact immutable preview", e)
		}
		if i > 0 && payload != cachePayload {
			t.Fatal("cache hit rewrote encrypted derived row")
		}
		cachePayload = payload
	}
	status, _, _ := request("GET", "/api/plans/"+pid+"/graph", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(otherProject["id"])})
	if status != 404 {
		t.Fatal("cross-project preview disclosed a cached graph", status)
	}
	for table, count := range libraryTableCounts(t, p) {
		if table == "plan_graph_cache" {
			if count != before[table]+1 {
				t.Fatal("preview added unrelated derived rows", count, before[table])
			}
			continue
		}
		if count != before[table] {
			t.Fatalf("preview wrote %s: %d -> %d", table, before[table], count)
		}
	}
	afterState, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	delete(afterState, "created_at")
	if string(encoded(afterState)) != string(encoded(beforeState)) {
		t.Fatal("preview changed primary data, versions or audit")
	}
	afterFile, e := fileHash(planFile)
	if e != nil || afterFile != beforeFile {
		t.Fatal("preview changed immutable JMX file", e)
	}
	status, _, source := request("GET", "/api/plans/"+pid+"/content", nil, map[string]string{"Authorization": "Bearer admin-secret"})
	if status != 200 || !strings.Contains(string(source), "/historical-before") || strings.Contains(string(source), "/latest-after") {
		t.Fatal("original JMX content was replaced")
	}
	if status, _, _ = request("GET", "/api/plans/"+pid+"/workflows", nil, nil); status != http.StatusUnauthorized {
		t.Fatal("anonymous origin disclosure", status)
	}
	status, clone := call("POST", "/api/workflows/import", Obj{"name": "从精确历史脚本编辑", "plan_id": pid})
	if status != 201 || clone["id"] == wid || obj(obj(findGraphNode(obj(clone["graph"]), "HTTPSamplerProxy")["properties"])["config"])["path"] != "/historical-before" {
		t.Fatal("draft fork did not start at exact immutable content", status, clone)
	}
}
func TestMySQLPlanLibraryLinksOCCAndBackupCompatibility(t *testing.T) {
	p, _ := mysqlFixture(t)
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "关联备份", "graph": basicWorkflowGraph()})
	if e != nil {
		t.Fatal(e)
	}
	wid := str(workflow["id"])
	publication, e := p.publishWorkflow(wid, Obj{"revision": 1})
	if e != nil {
		t.Fatal(e)
	}
	pid := str(publication["managerPlanId"])
	var linksBefore int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM plan_workflow_links").Scan(&linksBefore); e != nil {
		t.Fatal(e)
	}
	if _, e = p.workspaceUpdate("workflows", wid, Obj{"name": "更新的草稿", "graph": basicWorkflowGraph(), "revision": 1}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.publishWorkflow(wid, Obj{"revision": 1}); e == nil {
		t.Fatal("stale publication accepted")
	}
	var linksAfter int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM plan_workflow_links").Scan(&linksAfter); e != nil || linksBefore != linksAfter {
		t.Fatal("failed OCC publication persisted an association", linksBefore, linksAfter, e)
	}
	archive := filepath.Join(t.TempDir(), "library-archive")
	if _, e = p.backup(archive); e != nil {
		t.Fatal(e)
	}
	state, e := loadJSONFile(filepath.Join(archive, "state", "manager.json"))
	if e != nil || len(arr(state["plan_workflow_links"])) != 1 {
		t.Fatal("backup omitted exact source links", e)
	}
	restoredRoot := filepath.Join(t.TempDir(), "restored")
	if _, e = restoreArchive(archive, restoredRoot); e != nil {
		t.Fatal(e)
	}
	restored := emptyWorkspacePlatform(t, p, restoredRoot)
	restoredState, e := loadJSONFile(filepath.Join(restoredRoot, "state", "manager.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = restored.importState(restoredState); e != nil {
		t.Fatal(e)
	}
	_, call := workspaceHTTP(t, restored)
	requireLink(t, apiItems(t, call, "/api/plans/"+pid+"/workflows"), pid, wid, 1, "published")
	old := clone(state)
	delete(old, "plan_workflow_links")
	oldTarget := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "older-backup"))
	if e = oldTarget.importState(old); e != nil {
		t.Fatal("older backup without source links rejected", e)
	}
	var count int
	if e = oldTarget.DB.QueryRow("SELECT COUNT(*) FROM plan_workflow_links").Scan(&count); e != nil || count != 0 {
		t.Fatal("guessed a historical association", count, e)
	}
}

func TestMySQLPlanLinkFailureRollsBackPublicationAndImportedDraft(t *testing.T) {
	p, _ := mysqlFixture(t)
	content, e := compileWorkflow(basicWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	source, e := p.createPlan(Obj{"name": "精确来源", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	workflow, e := p.workspaceCreate("workflows", Obj{"name": "发布回滚", "graph": basicWorkflowGraph()})
	if e != nil {
		t.Fatal(e)
	}
	tables := []string{"plans", "workflows", "workflow_versions", "audit"}
	counts := map[string]int{}
	for _, table := range tables {
		var count int
		if e = p.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil {
			t.Fatal(e)
		}
		counts[table] = count
	}
	if _, e = p.DB.Exec("DROP TABLE plan_workflow_links"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.publishWorkflow(str(workflow["id"]), Obj{"revision": 1}); e == nil {
		t.Fatal("publishing silently omitted the persistent association")
	}
	if _, e = p.importWorkflow(Obj{"name": "导入回滚", "plan_id": source["id"]}); e == nil {
		t.Fatal("import silently omitted the persistent source")
	}
	for _, table := range tables {
		var count int
		if e = p.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count != counts[table] {
			t.Fatalf("association failure left partial %s rows: %d -> %d", table, counts[table], count)
		}
	}
}
func TestMySQLSourcePlanAssociationIsServerControlled(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	content, e := compileWorkflow(basicWorkflowGraph())
	if e != nil {
		t.Fatal(e)
	}
	source, e := p.createPlan(Obj{"name": "真实来源", "content": content})
	if e != nil {
		t.Fatal(e)
	}
	status, direct := call("POST", "/api/workflows", Obj{"name": "客户端试图伪造来源", "graph": basicWorkflowGraph(), "source_plan_id": source["id"]})
	if status != 201 || direct["source_plan_id"] != nil {
		t.Fatal("client forged a plan source", status, direct)
	}
	if links := apiItems(t, call, "/api/workflows/"+str(direct["id"])+"/plans"); len(links) != 0 {
		t.Fatal("direct create fabricated a source link", links)
	}
	status, imported := call("POST", "/api/workflows/import", Obj{"name": "服务器记录来源", "plan_id": source["id"], "source_plan_id": "plan-forged"})
	if status != 201 || imported["source_plan_id"] != source["id"] {
		t.Fatal("import source not resolved from actual selected plan", status, imported)
	}
	status, updated := call("PUT", "/api/workflows/"+str(imported["id"]), Obj{"name": "新名称", "revision": 1, "graph": imported["graph"], "source_plan_id": "plan-forged"})
	if status != 200 || updated["source_plan_id"] != source["id"] {
		t.Fatal("client update modified the immutable creation origin", status, updated)
	}
	requireLink(t, apiItems(t, call, "/api/workflows/"+str(imported["id"])+"/plans"), str(source["id"]), str(imported["id"]), 1, "imported")
}

func TestMySQLDeferredPlanEditCreatesOnlyTheEditedRevision(t *testing.T) {
	p, _ := mysqlFixture(t)
	_, call := workspaceHTTP(t, p)
	content, err := compileWorkflow(basicWorkflowGraph())
	if err != nil {
		t.Fatal(err)
	}
	source, err := p.createPlan(Obj{"name": "待编辑脚本", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	planID := str(source["id"])
	var before int
	if err := p.DB.QueryRow("SELECT COUNT(*) FROM workflows").Scan(&before); err != nil {
		t.Fatal(err)
	}
	preview, err := p.planGraph(planID)
	if err != nil {
		t.Fatal(err)
	}
	graph := clone(obj(preview["graph"]))
	http := findGraphNode(graph, "HTTPSamplerProxy")
	obj(obj(http["properties"])["config"])["path"] = "/edited-before-first-save"
	http["x"] = 915
	status, saved := call("POST", "/api/workflows/import?view=outline", Obj{"name": "已修改脚本", "description": "首次编辑", "plan_id": planID, "graph": graph, "source_plan_id": "forged"})
	if status != 201 || saved["source_plan_id"] != planID {
		t.Fatal("first save did not retain its verified source", status, saved)
	}
	stored, err := p.workspaceGet("workflows", str(saved["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded(stored["graph"])) != string(encoded(graph)) || number(stored["revision"]) != 1 {
		t.Fatal("first revision omitted edits or created an intermediate copy")
	}
	var after int
	if err := p.DB.QueryRow("SELECT COUNT(*) FROM workflows").Scan(&after); err != nil || after != before+1 {
		t.Fatal("expected exactly one new draft", before, after, err)
	}
	requireLink(t, apiItems(t, call, "/api/workflows/"+str(saved["id"])+"/plans"), planID, str(saved["id"]), 1, "imported")
	// Invalid/partial graphs must leave no draft or origin rows behind.
	partial := clone(graph)
	obj(obj(arr(partial["nodes"])[0])["properties"])["editor_lazy"] = true
	for _, body := range []Obj{
		{"name": "不完整", "plan_id": planID, "graph": partial},
		{"name": "无效", "plan_id": planID, "graph": Obj{}},
		{"name": "无来源", "content": content, "graph": graph},
	} {
		status, _ := call("POST", "/api/workflows/import", body)
		if status < 400 {
			t.Fatal("invalid local edit was accepted", status)
		}
	}
	if err := p.DB.QueryRow("SELECT COUNT(*) FROM workflows").Scan(&after); err != nil || after != before+1 {
		t.Fatal("rejected edit left a draft", after, err)
	}
	status, original := call("GET", "/api/plans/"+planID+"/graph", nil)
	if status != 200 || string(encoded(original["graph"])) != string(encoded(preview["graph"])) {
		t.Fatal("saving local edits changed the immutable source")
	}
	other, err := p.projectCreate(Obj{"name": "隔离项目"})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := workspaceHTTP(t, p)
	status, _, _ = request("POST", "/api/workflows/import", Obj{"name": "越权", "plan_id": planID, "graph": graph}, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(other["id"])})
	if status != 404 {
		t.Fatal("cross-project source was accepted", status)
	}
}
