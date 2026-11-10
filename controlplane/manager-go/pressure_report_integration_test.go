package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"pressurecooker/execution/protocol"
	"strings"
	"testing"
	"time"
)

func pressureFixture(t *testing.T) (*Platform, Obj, Obj) {
	t.Helper()
	return pressureFixtureWith(t, nil)
}
func pressureFixtureWith(t *testing.T, overrides map[string]string) (*Platform, Obj, Obj) {
	t.Helper()
	p, plan := mysqlFixture(t)
	peer := runnerObjectPeer(t)
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveBucket = "jmeter-reports"
	p.Config.ArchiveAccessKey = "test"
	p.Config.ArchiveSecretKey = "test-secret"
	p.Config.ArchiveTimeout = time.Second
	run := Obj{"id": newID("run-"), "project_id": defaultProjectID, "name": "隔离报告验证 https://svc/path?address=private-snapshot#private-fragment", "status": "RUNNING", "execution_mode": "redis", "attempt_id": newID("attempt-"), "cluster_snapshot": Obj{"controller_id": "controller-a"}, "plan_id": plan["id"], "plan_sha256": plan["sha256"], "plan_name": plan["name"], "created_at": "2026-11-06T01:00:00.000Z", "started_at": "2026-11-06T01:00:00.000Z", "workers": []any{"worker-1"}, "properties": Obj{"token": "fixture-private-value"}, "report_targets": Obj{"max_p95_ms": 100.0, "min_success_rate": 99.0}}
	if e := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); e != nil {
		t.Fatal(e)
	}
	csv := "timeStamp,elapsed,label,success,responseMessage,bytes,sentBytes,engine_id\n1793926800000,100,健康检查,true,OK,20,10,worker-1\n1793926801000,100,健康检查,true,OK,20,10,worker-1\n"
	prefix, _ := rawPrefix(run)
	store, _ := p.objectStore()
	refs := []protocol.ObjectRef{}
	files := map[string]string{"results.jtl": csv, "jmeter.log": "2026 WARN fixture token=fixture-private-value\n", "console.log": "2026 ERROR fixture Cookie: a=x; sessionid=private-cookie\n"}
	for k, v := range overrides {
		files[k] = v
	}
	for name, body := range files {
		ref, e := store.Put(context.Background(), prefix+name, bytes.NewReader([]byte(body)), int64(len(body)), digest([]byte(body)), "text/plain")
		if e != nil {
			t.Fatal(e)
		}
		ref.Name = name
		refs = append(refs, ref)
	}
	cb := dtoObject(protocol.Callback{EventID: newID("event-"), TaskID: str(run["id"]), AttemptID: str(run["attempt_id"]), RunnerID: "controller-a", Status: "COMPLETED", StartedAt: str(run["started_at"]), FinishedAt: "2026-11-06T01:00:02.000Z", Artifacts: refs})
	return p, run, cb
}
func pressureComplete(t *testing.T, p *Platform, run, cb Obj) Obj {
	t.Helper()
	if _, e := p.runnerCallback(context.Background(), "controller-a", cb); e != nil {
		t.Fatal(e)
	}
	c, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(c, ok, e)
	}
	p.workPressure(context.Background(), c)
	r, e := p.pressureLatest(str(run["id"]))
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestPressureCallbackAnalysisVersionIsolationAndRedaction(t *testing.T) {
	p, run, cb := pressureFixture(t)
	r := pressureComplete(t, p, run, cb)
	if r["status"] != "READY" || r["verdict_status"] != "PASS" || number(obj(r["metrics"])["sample_count"]) != 2 {
		t.Fatalf("unexpected report %s", encoded(r))
	}
	raw := string(encoded(r))
	if strings.Contains(raw, "fixture-private-value") || strings.Contains(raw, "private-cookie") {
		t.Fatal("report leaked secret")
	}
	if _, e := p.runnerCallback(context.Background(), "controller-a", cb); e != nil {
		t.Fatal(e)
	}
	var n int
	p.DB.QueryRow("SELECT COUNT(*) FROM pressure_reports").Scan(&n)
	if n != 1 {
		t.Fatal("duplicate callback created report", n)
	}
	conflict := clone(cb)
	conflict["exit_code"] = 1
	if _, e := p.runnerCallback(context.Background(), "controller-a", conflict); e == nil {
		t.Fatal("conflicting event accepted")
	}
	req := httptest.NewRequest("GET", "/api/pressure-reports/x/issues?limit=1", nil)
	issues, e := p.pressureChildren(str(r["id"]), "issues", req)
	if e != nil || number(issues["total"]) != 2 || len(arr(issues["items"])) != 1 {
		t.Fatal(issues, e)
	}
	if _, e = p.forProject("project-other").pressureGet(str(r["id"])); e == nil {
		t.Fatal("cross project report readable")
	}
	rebuild, e := p.generatePressure(str(run["id"]), "rebuild", Obj{"rebuild": true, "report_targets": Obj{"max_p95_ms": 50.0}})
	if e != nil {
		t.Fatal(e)
	}
	if number(rebuild["version"]) != 2 || rebuild["previous_ready_id"] != r["id"] {
		t.Fatal(rebuild)
	}
	again, e := p.generatePressure(str(run["id"]), "rebuild", Obj{"rebuild": true, "report_targets": Obj{"max_p95_ms": 50.0}})
	if e != nil || again["id"] != rebuild["id"] {
		t.Fatal("rebuild not idempotent", e)
	}
	if _, e = p.generatePressure(str(run["id"]), "rebuild", Obj{"report_targets": Obj{"max_p95_ms": 60.0}}); e == nil {
		t.Fatal("target idempotency conflict accepted")
	}
	c, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	p.workPressure(context.Background(), c)
	newR, e := p.pressureGet(str(rebuild["id"]))
	if e != nil || newR["status"] != "READY" || newR["verdict_status"] != "FAIL" {
		t.Fatal(newR, e)
	}
	original, _ := p.pressureGet(str(r["id"]))
	if original["verdict_status"] != "PASS" {
		t.Fatal("old version overwritten")
	}
	comparison, e := p.pressureCompare(str(r["id"]), str(newR["id"]))
	if e != nil || len(arr(comparison["differences"])) != 7 {
		t.Fatal(comparison, e)
	}
}
func TestPressureLeaseFencesOldWorkersAndPreservesReady(t *testing.T) {
	p, run, cb := pressureFixture(t)
	ready := pressureComplete(t, p, run, cb)
	r, e := p.generatePressure(str(run["id"]), "rebuild", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	old, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	p.DB.Exec("UPDATE pressure_report_jobs SET lease_until=0 WHERE id=?", old.ID)
	fresh, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	p.failPressure(old, "stale must not replace")
	current, _ := p.pressureGet(str(r["id"]))
	if current["status"] != "PROCESSING" || str(current["error"]) != "" {
		t.Fatal("stale lease wrote", current)
	}
	p.workPressure(context.Background(), fresh)
	p.workPressure(context.Background(), old)
	current, _ = p.pressureGet(str(r["id"]))
	if current["status"] != "READY" {
		t.Fatal(current)
	}
	original, _ := p.pressureGet(str(ready["id"]))
	if original["status"] != "READY" {
		t.Fatal("original lost")
	}
}
func TestPressureChecksumFailureRetryAndMissingSource(t *testing.T) {
	p, run, cb := pressureFixture(t)
	if _, e := p.runnerCallback(context.Background(), "controller-a", cb); e != nil {
		t.Fatal(e)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "3")
		w.Write([]byte("bad"))
	}))
	defer broken.Close()
	p.Config.ArchiveEndpoint = broken.URL
	for i := 0; i < 3; i++ {
		c, ok, e := p.claimPressure()
		if e != nil || !ok {
			t.Fatal(e, ok)
		}
		p.workPressure(context.Background(), c)
		p.DB.Exec("UPDATE pressure_report_jobs SET next_attempt_at=0 WHERE id=?", c.ID)
	}
	r, _ := p.pressureLatest(str(run["id"]))
	if r["status"] != "FAILED" || r["metrics"] != nil {
		t.Fatal("checksum published data", r)
	}
	var count int
	p.DB.QueryRow("SELECT COUNT(*) FROM pressure_report_steps").Scan(&count)
	if count != 0 {
		t.Fatal("partial data persisted")
	}
	// A terminal run with no JTL is a readable incomplete report, never a pass.
	run["id"] = newID("run-")
	run["status"] = "STOPPED"
	run["raw_archive"] = Obj{"files": []any{}}
	p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) })
	queued, e := p.generatePressure(str(run["id"]), "missing", Obj{})
	if e != nil {
		t.Fatal(e)
	}
	c, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	p.workPressure(context.Background(), c)
	r, _ = p.pressureGet(str(queued["id"]))
	if r["status"] != "READY" || r["verdict_status"] != "INCOMPLETE" {
		t.Fatal(r)
	}
}
func TestPressureBackupRestoreAndOriginalUnavailable(t *testing.T) {
	p, run, cb := pressureFixture(t)
	ready := pressureComplete(t, p, run, cb)
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "restored"))
	if e = restored.importState(state); e != nil {
		t.Fatal(e)
	}
	r, e := restored.pressureGet(str(ready["id"]))
	if e != nil || r["status"] != "READY" {
		t.Fatal(r, e)
	}
	unavailable := httptest.NewServer(http.NotFoundHandler())
	defer unavailable.Close()
	restored.Config.ArchiveEndpoint = unavailable.URL
	next, e := restored.generatePressure(str(run["id"]), "rebuild-no-objects", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	c, ok, e := restored.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	restored.workPressure(context.Background(), c)
	failed, _ := restored.pressureGet(str(next["id"]))
	if failed["status"] != "RETRY" {
		t.Fatal(failed)
	}
	r, _ = restored.pressureGet(str(ready["id"]))
	if r["status"] != "READY" {
		t.Fatal("restore lost ready")
	}
}
func TestPressureTargetsValidationAndTaskSnapshot(t *testing.T) {
	for _, v := range []any{"x", Obj{"unknown": 1.0}, Obj{"max_p95_ms": "10"}, Obj{"min_success_rate": 101.0}, Obj{"min_rps": -1.0}} {
		if _, e := pressureTargets(v); e == nil {
			t.Fatal("invalid targets accepted", v)
		}
	}
	p, plan := mysqlFixture(t)
	peer := runnerObjectPeer(t)
	p.Config.ArchiveEndpoint = peer.URL
	p.Config.ArchiveBucket = "jmeter-reports"
	p.Config.ArchiveAccessKey = "test"
	p.Config.ArchiveSecretKey = "secret"
	env, e := p.workspaceCreate("environments", Obj{"name": "隔离环境", "base_host": "http://fixture"})
	if e != nil {
		t.Fatal(e)
	}
	body := Obj{"cluster_id": clusterID("controller-a"), "name": "targets", "plan_id": plan["id"], "environment_id": env["id"], "workers": []any{"worker-1"}, "report_targets": Obj{"min_rps": 0.0, "max_p99_ms": 200.0}}
	r, created, e := p.createRun(body, "targets")
	if e != nil || !created {
		t.Fatal(r, e)
	}
	saved, _ := p.get("runs", str(r["id"]))
	if number(obj(saved["report_targets"])["max_p99_ms"]) != 200 {
		t.Fatal("targets missing")
	}
	if _, exists := obj(saved["properties"])["max_p99_ms"]; exists {
		t.Fatal("goal sent to engine")
	}
	body["report_targets"] = Obj{"max_p99_ms": 100.0}
	if _, _, e = p.createRun(body, "targets"); e == nil {
		t.Fatal("changed goal not idempotency conflict")
	}
}

func TestPressureHTTPProjectScopeAndNormalizedColumns(t *testing.T) {
	p, run, cb := pressureFixture(t)
	report := pressureComplete(t, p, run, cb)
	rawRequest, request := workspaceHTTP(t, p)
	base := "/api/pressure-reports/" + str(report["id"])
	for _, path := range []string{"/api/pressure-reports?limit=1", base, base + "/steps?limit=1&sort=latency", base + "/issues?limit=1&sort=failures", "/api/runs/" + str(run["id"]) + "/pressure-report", "/api/pressure-reports/compare?left=" + str(report["id"]) + "&right=" + str(report["id"])} {
		status, v := request("GET", path, nil)
		if status != 200 {
			t.Fatalf("%s: %d %v", path, status, v)
		}
	}
	if status, _ := request("GET", "/api/pressure-reports?limit=999", nil); status != 400 {
		t.Fatal("unbounded list", status)
	}
	if status, _ := request("POST", "/api/runs/"+str(run["id"])+"/pressure-report", Obj{}); status != 400 {
		t.Fatal("missing idempotency accepted", status)
	}
	project, e := p.projectCreate(Obj{"name": "其他隔离项目"})
	if e != nil {
		t.Fatal(e)
	}
	if status, _, _ := rawRequest("GET", base, nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": str(project["id"])}); status != 404 {
		t.Fatal("crossproject HTTP allowed", status)
	}
	var samples, success, received int64
	var p95, mean float64
	if e = p.DB.QueryRow("SELECT sample_count,success_count,bytes_received,p95_ms,mean_ms FROM pressure_reports WHERE id=?", report["id"]).Scan(&samples, &success, &received, &p95, &mean); e != nil || samples != 2 || success != 2 || received != 40 || p95 != 100 || mean != 100 {
		t.Fatal("normalized metrics", samples, success, received, p95, mean, e)
	}
	var points int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM pressure_report_timeline WHERE timestamp_ms>0 AND width_ms=1000 AND sample_count=1").Scan(&points); e != nil || points != 2 {
		t.Fatal("normalized timeline", points, e)
	}
	var rps float64
	if e = p.DB.QueryRow("SELECT number_value FROM pressure_report_metrics WHERE metric_key='rps'").Scan(&rps); e != nil || rps <= 0 {
		t.Fatal("normalized metric extension", rps, e)
	}
}
func TestPressureBackupClearsProcessingLease(t *testing.T) {
	p, run, cb := pressureFixture(t)
	if _, e := p.runnerCallback(context.Background(), "controller-a", cb); e != nil {
		t.Fatal(e)
	}
	c, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "restored"))
	if e = restored.importState(state); e != nil {
		t.Fatal(e)
	}
	var status, token string
	var until float64
	if e = restored.DB.QueryRow("SELECT status,lease_token,lease_until FROM pressure_report_jobs WHERE id=?", c.ID).Scan(&status, &token, &until); e != nil || status != "RETRY" || token != "" || until != 0 {
		t.Fatal(status, token, until, e)
	}
	r, e := restored.pressureLatest(str(run["id"]))
	if e != nil || r["status"] != "RETRY" || str(r["originals_notice"]) == "" {
		t.Fatal(r, e)
	}
}

func TestPressureReviewLostLeases(t *testing.T) {
	p, run, cb := pressureFixture(t)
	if _, e := p.runnerCallback(context.Background(), "controller-a", cb); e != nil {
		t.Fatal(e)
	}
	var c pressureClaim
	for i := 1; i <= 3; i++ {
		var ok bool
		var e error
		c, ok, e = p.claimPressure()
		if e != nil || !ok || c.Attempts != i {
			t.Fatal(c, e)
		}
		if _, e = p.DB.Exec("UPDATE pressure_report_jobs SET lease_until=0,created_at='0' WHERE id=?", c.ID); e != nil {
			t.Fatal(e)
		}
	}
	next, e := p.generatePressure(str(run["id"]), "next", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	fresh, ok, e := p.claimPressure()
	if e != nil || !ok || fresh.ID != next["id"] {
		t.Fatal("queue starved", fresh, e)
	}
	failed, e := p.pressureGet(c.ID)
	if e != nil || failed["status"] != "FAILED" {
		t.Fatal(failed, e)
	}
}

func TestPressureReviewSpool(t *testing.T) {
	f, e := pressureSpool(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = os.Stat(f.Name()); !os.IsNotExist(e) {
		t.Fatal("spool linked", e)
	}
	if _, e = f.Write([]byte("ok")); e != nil {
		t.Fatal(e)
	}
}
func TestPressureReviewEvidencePersistence(t *testing.T) {
	p, run, cb := pressureFixtureWith(t, map[string]string{"results.jtl": "timeStamp,elapsed,label,success,engine_id,responseMessage\n1,1,/step?who=private-label,false,worker-1,https://svc/a#private-message\n", "jmeter.log": "\n  \n", "console.log": "WARN GET https://svc/path?email=private-log&other=private-value#private-fragment\n"})
	report := pressureComplete(t, p, run, cb)
	if report["status"] != "READY" || obj(report["quality"])["log_status"] != "PARTIAL" || report["verdict_status"] != "FAIL" {
		t.Fatal(report)
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range pressureTables {
		raw := string(encoded(state[name]))
		for _, secret := range []string{"private-label", "private-log", "private-value", "private-fragment", "private-snapshot", "private-message"} {
			if strings.Contains(raw, secret) {
				t.Fatalf("%s leaked %s", name, secret)
			}
		}
	}
	var seen bool
	for _, v := range arr(report["sources"]) {
		m := obj(v)
		if m["name"] == "jmeter.log" {
			seen = m["status"] == "EMPTY" && m["download_status"] == "VERIFIED"
		}
	}
	if !seen {
		t.Fatal("empty source incorrectly described", report)
	}
	for _, row := range arr(state["pressure_report_metrics"]) {
		m := obj(row)
		data, e := decode([]byte(str(m["data"])))
		if e != nil || str(data["unit"]) == "" || m["number_value"] == nil || m["metric_key"] == "sample_scope" || m["metric_key"] == "percentile_method" {
			t.Fatal(m, e)
		}
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "refs"))
	if e = restored.importState(state); e != nil {
		t.Fatal(e)
	}
	recovered, e := restored.pressureGet(str(report["id"]))
	if e != nil || len(arr(recovered["findings"])) != 2 {
		t.Fatal(recovered, e)
	}
	for _, f := range arr(recovered["findings"]) {
		refs := arr(obj(f)["evidence_refs"])
		if len(refs) == 0 {
			t.Fatal("missing refs", f)
		}
		table := "pressure_report_sources"
		if strings.Contains(str(obj(f)["title"]), "失败采样") {
			table = "pressure_report_issues"
		}
		for _, ref := range refs {
			var n int
			if e = restored.DB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id=? AND report_id=?", ref, report["id"]).Scan(&n); e != nil || n != 1 {
				t.Fatal(f, n, e)
			}
		}
	}
	// Same values, different population or evidence quality: neither can yield a delta.
	right, e := p.generatePressure(str(run["id"]), "contrast", Obj{"rebuild": true})
	if e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2]string{{"sample_records", "COMPLETE"}, {"ordinary_samples", "PARTIAL"}, {"ordinary_samples", "COMPLETE"}} {
		other := clone(report)
		obj(other["metrics"])["sample_scope"] = pair[0]
		obj(other["quality"])["metric_status"] = pair[1]
		other["id"] = right["id"]
		if _, e = p.DB.Exec("UPDATE pressure_reports SET data=? WHERE id=?", string(encoded(other)), right["id"]); e != nil {
			t.Fatal(e)
		}
		cmp, e := p.pressureCompare(str(report["id"]), str(right["id"]))
		if e != nil {
			t.Fatal(e)
		}
		same := pair[0] == "ordinary_samples" && pair[1] == "COMPLETE"
		for _, v := range arr(cmp["differences"]) {
			if (obj(v)["delta"] != nil) != same {
				t.Fatal(cmp)
			}
		}
		if !same && len(arr(cmp["warnings"])) < 2 {
			t.Fatal(cmp)
		}
	}
}

func TestPressureReviewBothEmptyLogs(t *testing.T) {
	p, run, cb := pressureFixtureWith(t, map[string]string{"jmeter.log": "", "console.log": ""})
	report := pressureComplete(t, p, run, cb)
	if obj(report["quality"])["log_status"] != "EMPTY" || report["verdict_status"] != "PASS" {
		t.Fatal(report)
	}
}
func TestPressureReviewImportReplayOwnership(t *testing.T) {
	p, run, cb := pressureFixture(t)
	first := pressureComplete(t, p, run, cb)
	_, e := p.generatePressure(str(run["id"]), "owner", Obj{})
	if e != nil {
		t.Fatal(e)
	}
	project, e := p.projectCreate(Obj{"name": "第二项目"})
	if e != nil {
		t.Fatal(e)
	}
	// Both a true second project and a second task in the same project.
	for _, projectID := range []string{str(project["id"]), defaultProjectID} {
		scoped := p.forProject(projectID)
		other := clone(run)
		other["id"] = newID("run-")
		other["project_id"] = projectID
		other["plan_id"] = ""
		other["metadata_incomplete"] = true
		other["status"] = "COMPLETED"
		if e = p.transaction(func(tx *Tx) error { return scoped.saveRun(tx, other) }); e != nil {
			t.Fatal(e)
		}
		foreign, e := scoped.generatePressure(str(other["id"]), newID("other-"), Obj{})
		if e != nil {
			t.Fatal(e)
		}
		key := defaultProjectID + "\x00" + digest([]byte("owner"))
		if _, e = p.DB.Exec("UPDATE idempotency SET object_id=? WHERE kind='pressure' AND `key`=?", foreign["id"], key); e != nil {
			t.Fatal(e)
		}
		if replay, e := p.generatePressure(str(run["id"]), "owner", Obj{}); e == nil || replay != nil {
			t.Fatal("foreign replay", replay, e)
		}
		state, e := p.exportState()
		if e != nil {
			t.Fatal(e)
		}
		restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "bad"))
		if e = restored.importState(state); e == nil || !strings.Contains(e.Error(), "pressure idempotency ownership") {
			t.Fatal("invalid import not rejected for association", e)
		}
		if _, e = p.DB.Exec("UPDATE idempotency SET object_id=? WHERE kind='pressure' AND `key`=?", first["id"], key); e != nil {
			t.Fatal(e)
		}
	}
	state, e := p.exportState()
	if e != nil {
		t.Fatal(e)
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "good"))
	if e = restored.importState(state); e != nil {
		t.Fatal(e)
	}
	replay, e := restored.generatePressure(str(run["id"]), "owner", Obj{})
	if e != nil || replay["id"] != first["id"] {
		t.Fatal(replay, e)
	}
}
