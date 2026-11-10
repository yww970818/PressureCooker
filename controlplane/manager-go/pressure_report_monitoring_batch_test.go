package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func monitoringBatch(revision, count int) Obj {
	entries := []any{}
	for i := 0; i < count; i++ {
		entry := monitoringRequest()
		delete(entry, "component")
		delete(entry, "revision")
		entry["instance"] = fmt.Sprintf("模拟-JVM-%03d", i)
		entries = append(entries, entry)
	}
	return Obj{"revision": revision, "entries": entries}
}

func TestPressureMonitoringBatchValidationAndTotalPointBudget(t *testing.T) {
	entries, revision, err := validatePressureMonitoringBatch(monitoringBatch(7, 200), monitoringRun(), time.Now())
	if err != nil || revision != 7 || len(entries) != 200 || obj(entries[0])["component"] != "jvm" {
		t.Fatal(len(entries), revision, err)
	}
	for name, mutate := range map[string]func(Obj){
		"empty":              func(b Obj) { b["entries"] = []any{} },
		"too many":           func(b Obj) { b["entries"] = arr(monitoringBatch(0, 201)["entries"]) },
		"unknown root":       func(b Obj) { b["component"] = "jvm" },
		"entry component":    func(b Obj) { obj(arr(b["entries"])[0])["component"] = "jvm" },
		"entry revision":     func(b Obj) { obj(arr(b["entries"])[0])["revision"] = 0 },
		"entry invalid":      func(b Obj) { obj(arr(b["entries"])[1])["health"] = "unrecognized" },
		"duplicate instance": func(b Obj) { obj(arr(b["entries"])[1])["instance"] = obj(arr(b["entries"])[0])["instance"] },
		"body bytes": func(b Obj) {
			large := monitoringBatch(0, 40)
			for _, value := range arr(large["entries"]) {
				findings := []any{}
				for i := 0; i < 32; i++ {
					findings = append(findings, Obj{"level": "INFO", "title": "模拟发现", "detail": strings.Repeat("x", 4000)})
				}
				obj(value)["findings"] = findings
			}
			b["entries"] = large["entries"]
		},
		"single bytes": func(b Obj) { obj(arr(b["entries"])[0])["summary"] = strings.Repeat("x", 512*1024) },
	} {
		t.Run(name, func(t *testing.T) {
			body := monitoringBatch(0, 2)
			mutate(body)
			if _, _, err := validatePressureMonitoringBatch(body, monitoringRun(), time.Now()); err == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}
	// Every entry has valid old per-entry limits; the aggregate cap is separate.
	for _, count := range []int{30, 31} {
		body := monitoringBatch(0, count)
		for _, value := range arr(body["entries"]) {
			points := []any{}
			for i := 0; i < 2000; i++ {
				points = append(points, Obj{"timestamp_ms": int64(1793926800000 + i), "value": nil})
			}
			obj(value)["series"] = []any{Obj{"key": "heap", "points": points}}
		}
		_, _, err := validatePressureMonitoringBatch(body, monitoringRun(), time.Now())
		if (err == nil) != (count == 30) {
			t.Fatal("batch point budget", count, err)
		}
	}
}

func TestPressureMonitoringBatchAtomicAPIAndLegacyReplay(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	path := "/api/pressure-reports/" + id + "/monitoring"
	request, _ := workspaceHTTP(t, p)
	headers := map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": p.projectID(), "Idempotency-Key": "scale-batch"}
	legacy := monitoringRequest()
	legacy["component"], legacy["instance"] = "redis", "redis-kept"
	old, err := p.writePressureMonitoring(id, "legacy-single", legacy)
	if err != nil {
		t.Fatal(err)
	}
	var original string
	p.DB.QueryRow("SELECT data FROM pressure_reports WHERE id=?", id).Scan(&original)
	body := monitoringBatch(1, 200)
	status, _, raw := request("POST", path+"/jvm-batch", body, headers)
	written, _ := decode(raw)
	if status != 200 || number(written["revision"]) != 2 || number(written["written_count"]) != 200 || number(written["instance_count"]) != 201 || len(arr(written["written_entries"])) != 200 {
		t.Fatal(status, string(raw))
	}
	if strings.Contains(string(raw), "timestamp_ms") || strings.Contains(string(raw), "metrics") {
		t.Fatal("batch response echoes observations")
	}
	for _, value := range arr(written["written_entries"]) {
		if len(obj(value)) != 2 {
			t.Fatal("summary entry shape", obj(value))
		}
	}
	firstRaw := string(raw)
	status, _, raw = request("POST", path+"/jvm-batch", body, headers)
	if status != 200 || string(raw) != firstRaw {
		t.Fatal("batch replay changed", status)
	}
	legacyRetry, err := p.writePressureMonitoring(id, "legacy-single", legacy)
	if err != nil || string(encoded(old)) != string(encoded(legacyRetry)) {
		t.Fatal("legacy single replay changed", err)
	}
	current, err := p.pressureMonitoring(id, 0)
	if err != nil || len(arr(current["entries"])) != 201 || len(arr(current["history"])) != 2 || number(obj(arr(current["history"])[0])["written_count"]) != 200 {
		t.Fatal("batch snapshot/history", err)
	}
	if _, leaked := current["batch_result"]; leaked {
		t.Fatal("internal batch receipt leaked")
	}
	historical, err := p.pressureMonitoring(id, 1)
	if err != nil || len(arr(historical["entries"])) != 1 {
		t.Fatal("history changed", err)
	}
	later := monitoringRequest()
	later["revision"], later["instance"], later["health"] = 2, "模拟-JVM-000", "WARNING"
	if _, err := p.writePressureMonitoring(id, "later-single", later); err != nil {
		t.Fatal(err)
	}
	status, _, raw = request("POST", path+"/jvm-batch", body, headers)
	if status != 200 || string(raw) != firstRaw {
		t.Fatal("later writes changed original batch receipt", status)
	}
	current, err = p.pressureMonitoring(id, 0)
	if err != nil || number(current["revision"]) != 3 {
		t.Fatal(err)
	}
	// One invalid entry cannot alter any of the prior 201 instances or revision.
	bad := monitoringBatch(3, 2)
	obj(arr(bad["entries"])[1])["health"] = "invalid"
	headers["Idempotency-Key"] = "invalid-batch"
	status, _, _ = request("POST", path+"/jvm-batch", bad, headers)
	if status != 400 {
		t.Fatal("invalid batch accepted", status)
	}
	after, err := p.pressureMonitoring(id, 0)
	if err != nil || string(encoded(after)) != string(encoded(current)) {
		t.Fatal("partial batch persisted", err)
	}
	headers["Idempotency-Key"] = "scale-batch"
	changed := monitoringBatch(1, 199)
	status, _, _ = request("POST", path+"/jvm-batch", changed, headers)
	if status != 409 {
		t.Fatal("batch idempotency conflict", status)
	}
	var unchanged string
	p.DB.QueryRow("SELECT data FROM pressure_reports WHERE id=?", id).Scan(&unchanged)
	if unchanged != original {
		t.Fatal("batch changed core report")
	}
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "batch-restored"))
	if err := restored.importState(state); err != nil {
		t.Fatal(err)
	}
	replayed, err := restored.writePressureMonitoringJVMBatch(id, "scale-batch", body)
	if err != nil || string(encoded(replayed)) != firstRaw {
		t.Fatal("batch restore replay changed", err)
	}
}

func TestPressureMonitoringPagingFiltersRevisionAndBoundaries(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	if _, err := p.writePressureMonitoringJVMBatch(id, "paged", monitoringBatch(0, 23)); err != nil {
		t.Fatal(err)
	}
	body := monitoringRequest()
	body["revision"], body["component"] = 1, "redis"
	if _, err := p.writePressureMonitoring(id, "other", body); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query                   string
		total, count, instances int
	}{{"component=jvm", 23, 20, 24}, {"component=jvm&limit=5&offset=20", 23, 3, 24}, {"search=022&limit=5&offset=0", 1, 1, 24}, {"component=redis&search=missing", 0, 0, 24}, {"component=jvm&offset=1000000", 23, 0, 24}, {"revision=1&component=jvm&limit=200", 23, 23, 23}} {
		values, _ := url.ParseQuery(tc.query)
		page, err := p.pressureMonitoringPage(id, values)
		if err != nil || number(page["total"]) != float64(tc.total) || len(arr(page["entries"])) != tc.count || number(page["instance_count"]) != float64(tc.instances) || number(obj(page["component_counts"])["jvm"]) != 23 {
			t.Fatal(tc.query, page, err)
		}
	}
	full, err := p.pressureMonitoringPage(id, url.Values{"revision": {"1"}, "export": {"1"}})
	if err != nil || len(arr(full["entries"])) != 23 {
		t.Fatal(err)
	}
	if _, paged := full["limit"]; paged {
		t.Fatal("revision-only old full read was paged")
	}
	for _, query := range []string{"component=unknown", "limit=0", "limit=201", "offset=-1", "offset=1000001", "search=" + strings.Repeat("x", 201), "component=jvm&component=redis", "limit=5&limit=10"} {
		values, _ := url.ParseQuery(query)
		if _, err := p.pressureMonitoringPage(id, values); err == nil {
			t.Fatal("invalid query accepted", query)
		}
	}
	if _, err := p.forProject("project-foreign").pressureMonitoringPage(id, url.Values{"component": {"jvm"}}); err == nil {
		t.Fatal("paged cross-project read")
	}
	request, _ := workspaceHTTP(t, p)
	status, _, raw := request("GET", "/api/pressure-reports/"+id+"/monitoring?component=jvm&limit=5&offset=20", nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": p.projectID()})
	page, err := decode(raw)
	if status != 200 || err != nil || number(page["total"]) != 23 || len(arr(page["entries"])) != 3 || number(page["offset"]) != 20 {
		t.Fatal("HTTP pagination contract", status, err)
	}
}

func TestPressureMonitoringBatchConcurrentRevisionScopeAndCapacity(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			_, err := p.writePressureMonitoringJVMBatch(id, fmt.Sprintf("race-batch-%d", i), monitoringBatch(0, 2))
			results <- err
		}(i)
	}
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if e, ok := err.(*APIError); ok && e.Status == 409 {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
	for revision := 1; revision <= 5; revision++ {
		count := 200
		if revision == 5 {
			count = 198
		}
		body := monitoringBatch(revision, count)
		for i, value := range arr(body["entries"]) {
			obj(value)["instance"] = fmt.Sprintf("规模-%04d", (revision-1)*200+i)
		}
		_, err := p.writePressureMonitoringJVMBatch(id, fmt.Sprintf("fill-%d", revision), body)
		if err != nil {
			t.Fatal("capacity batch", revision, err)
		}
	}
	tooMany := monitoringBatch(6, 1)
	obj(arr(tooMany["entries"])[0])["instance"] = "模拟-第1001实例"
	if _, err := p.writePressureMonitoringJVMBatch(id, "overflow-1001", tooMany); err == nil {
		t.Fatal("1001st instance accepted")
	}
	foreign, err := p.projectCreate(Obj{"name": "Foreign batch project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.forProject(str(foreign["id"])).writePressureMonitoringJVMBatch(id, "cross-project", monitoringBatch(6, 2)); err == nil {
		t.Fatal("foreign batch accepted")
	} else if failure, ok := err.(*APIError); !ok || failure.Status != 404 {
		t.Fatal("foreign batch must retain scoped 404", err)
	}
	project, err := p.projectGetQ(p.DB, p.projectID(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.projectUpdate(p.projectID(), Obj{"revision": project["revision"]}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := p.writePressureMonitoringJVMBatch(id, "archived-batch", monitoringBatch(5, 2)); err == nil {
		t.Fatal("archived batch accepted")
	}
}

func TestMonitoringEvidenceDeduplicatesAndPagesWithoutOpeningOtherEvidence(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	if _, err := p.writePressureMonitoringJVMBatch(id, "first", monitoringBatch(0, 23)); err != nil {
		t.Fatal(err)
	}
	body := monitoringRequest()
	body["revision"] = 1
	if _, err := p.writePressureMonitoring(id, "second", body); err != nil {
		t.Fatal(err)
	}
	var evidence, members int
	if err := p.DB.QueryRow("SELECT COUNT(*) FROM monitoring_evidence WHERE report_id=?", id).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	p.DB.QueryRow("SELECT COUNT(*) FROM monitoring_memberships WHERE report_id=?", id).Scan(&members)
	if evidence != 24 || members != 47 {
		t.Fatal("unchanged evidence duplicated", evidence, members)
	}
	// Corrupt an unrelated evidence body. Page one must never read/decode it.
	if _, err := p.DB.Exec("UPDATE monitoring_evidence SET data='invalid-json' WHERE id=(SELECT evidence_id FROM monitoring_memberships WHERE report_id=? AND revision=2 AND position=22)", id); err != nil {
		t.Fatal(err)
	}
	p.DB.SetMaxOpenConns(1)
	page, err := p.pressureMonitoringPage(id, url.Values{"component": {"jvm"}, "limit": {"5"}})
	if err != nil || len(arr(page["entries"])) != 5 || number(page["total"]) != 24 {
		t.Fatal("paging opened unrelated evidence", page, err)
	}
}
