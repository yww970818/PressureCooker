package main

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func monitoringRequest() Obj {
	return Obj{"revision": 0, "component": "jvm", "instance": "jvm-a", "source": Obj{"name": "隔离采集器"}, "window": Obj{"start": "2026-11-06T01:00:00Z", "end": "2026-11-06T01:00:02Z"}, "metrics": []any{Obj{"key": "heap.used", "label": "堆使用", "value": 0, "unit": "bytes"}, Obj{"key": "gc.pause", "value": nil}}, "series": []any{Obj{"key": "heap", "points": []any{Obj{"timestamp_ms": int64(1793926800000), "value": 0}, Obj{"timestamp_ms": int64(1793926802000), "value": nil}}}}, "findings": []any{Obj{"level": "INFO", "title": "外部观察"}}}
}
func monitoringRun() Obj {
	return Obj{"started_at": "2026-11-06T01:00:00Z", "finished_at": "2026-11-06T01:00:02Z", "properties": Obj{"token": "fixture-known-secret"}}
}

func TestPressureMonitoringValidationNullZeroAlignmentAndRedaction(t *testing.T) {
	body := monitoringRequest()
	body["summary"] = "token=unlisted-value fixture-known-secret https://user:pass@host/path?q=private"
	obj(body["source"])["reference"] = "https://user:pass@host/path?private=1"
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	entry, revision, err := validatePressureMonitoring(body, monitoringRun(), now)
	if err != nil || revision != 0 || entry["time_alignment"] != "CONFIRMED" || entry["health"] != "UNKNOWN" || len(arr(entry["warnings"])) != 0 || obj(entry["source"])["collected_at"] != now.Format(time.RFC3339Nano) {
		t.Fatal(entry, revision, err)
	}
	metrics := arr(entry["metrics"])
	if number(obj(metrics[0])["value"]) != 0 || obj(metrics[1])["value"] != nil || obj(metrics[1])["label"] != "gc.pause" || obj(arr(entry["series"])[0])["label"] != "heap" {
		t.Fatal("zero/null/default label changed", metrics)
	}
	for _, secret := range []string{"fixture-known-secret", "unlisted-value", "user:pass", "q=private", "private=1"} {
		if strings.Contains(string(encoded(entry)), secret) {
			t.Fatalf("redaction missed %q", secret)
		}
	}
	unknown, _, err := validatePressureMonitoring(monitoringRequest(), Obj{}, now)
	if err != nil || unknown["time_alignment"] != "UNCONFIRMED" || len(arr(unknown["warnings"])) == 0 {
		t.Fatal(unknown, err)
	}
	out := monitoringRequest()
	out["window"] = Obj{"start": "2026-11-05T01:00:00Z", "end": "2026-11-05T01:00:02Z"}
	out["series"] = []any{}
	if _, _, err := validatePressureMonitoring(out, monitoringRun(), now); err == nil {
		t.Fatal("nonoverlapping window accepted")
	}
	// Adjacent closed interval endpoints are valid evidence windows.
	out["window"] = Obj{"start": "2026-11-06T00:59:00Z", "end": "2026-11-06T01:00:00Z"}
	if _, _, err := validatePressureMonitoring(out, monitoringRun(), now); err != nil {
		t.Fatal(err)
	}
}

func TestPressureMonitoringValidationRejectsMalformedAndBoundedInput(t *testing.T) {
	cases := map[string]func(Obj){
		"unknown root":         func(b Obj) { b["extra"] = true },
		"missing revision":     func(b Obj) { delete(b, "revision") },
		"negative revision":    func(b Obj) { b["revision"] = -1 },
		"component":            func(b Obj) { b["component"] = "host" },
		"empty instance":       func(b Obj) { b["instance"] = " " },
		"instance bytes":       func(b Obj) { b["instance"] = strings.Repeat("中", 67) },
		"source unknown":       func(b Obj) { obj(b["source"])["token"] = "ignored" },
		"source missing":       func(b Obj) { delete(obj(b["source"]), "name") },
		"source timestamp":     func(b Obj) { obj(b["source"])["collected_at"] = "yesterday" },
		"window unknown":       func(b Obj) { obj(b["window"])["extra"] = 1 },
		"window reversed":      func(b Obj) { obj(b["window"])["end"] = "2026-11-06T00:00:00Z" },
		"summary bytes":        func(b Obj) { b["summary"] = strings.Repeat("x", 4001) },
		"health":               func(b Obj) { b["health"] = "HEALTHY" },
		"no observations":      func(b Obj) { delete(b, "metrics"); delete(b, "series"); delete(b, "findings") },
		"metric unknown":       func(b Obj) { obj(arr(b["metrics"])[0])["extra"] = 1 },
		"metric missing value": func(b Obj) { delete(obj(arr(b["metrics"])[0]), "value") },
		"metric key":           func(b Obj) { obj(arr(b["metrics"])[0])["key"] = "../key" },
		"metric duplicate":     func(b Obj) { b["metrics"] = []any{Obj{"key": "same", "value": 0}, Obj{"key": "same", "value": nil}} },
		"metric string":        func(b Obj) { obj(arr(b["metrics"])[0])["value"] = "0" },
		"metric bool":          func(b Obj) { obj(arr(b["metrics"])[0])["value"] = false },
		"metric huge":          func(b Obj) { obj(arr(b["metrics"])[0])["value"] = 1e15 + 1 },
		"metric infinity":      func(b Obj) { obj(arr(b["metrics"])[0])["value"] = math.Inf(1) },
		"metric nan":           func(b Obj) { obj(arr(b["metrics"])[0])["value"] = math.NaN() },
		"metric unit":          func(b Obj) { obj(arr(b["metrics"])[0])["unit"] = strings.Repeat("x", 33) },
		"series duplicate":     func(b Obj) { b["series"] = append(arr(b["series"]), arr(b["series"])[0]) },
		"point unknown":        func(b Obj) { obj(arr(obj(arr(b["series"])[0])["points"])[0])["extra"] = 0 },
		"point duplicate timestamp": func(b Obj) {
			points := arr(obj(arr(b["series"])[0])["points"])
			obj(points[1])["timestamp_ms"] = obj(points[0])["timestamp_ms"]
		},
		"point outside window": func(b Obj) { obj(arr(obj(arr(b["series"])[0])["points"])[0])["timestamp_ms"] = int64(1793926799999) },
		"point noninteger":     func(b Obj) { obj(arr(obj(arr(b["series"])[0])["points"])[0])["timestamp_ms"] = 1.5 },
		"finding level":        func(b Obj) { obj(arr(b["findings"])[0])["level"] = "OK" },
		"finding empty title":  func(b Obj) { obj(arr(b["findings"])[0])["title"] = "" },
		"finding unknown":      func(b Obj) { obj(arr(b["findings"])[0])["url"] = "https://ignored" },
		"encoded body":         func(b Obj) { b["summary"] = strings.Repeat("x", 512*1024) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := monitoringRequest()
			mutate(body)
			if _, _, err := validatePressureMonitoring(body, monitoringRun(), time.Now()); err == nil {
				t.Fatal("invalid monitoring accepted")
			}
		})
	}
	for _, tc := range []struct {
		field string
		count int
	}{{"metrics", 65}, {"series", 13}, {"findings", 33}} {
		body := monitoringRequest()
		body[tc.field] = make([]any, tc.count)
		if _, _, err := validatePressureMonitoring(body, monitoringRun(), time.Now()); err == nil {
			t.Fatal("item cap ignored", tc.field)
		}
	}
	for _, count := range []int{2000, 2001} {
		body := monitoringRequest()
		points := []any{}
		for i := 0; i < count; i++ {
			points = append(points, Obj{"timestamp_ms": int64(1793926800000 + i), "value": json.Number("0")})
		}
		obj(arr(body["series"])[0])["points"] = points
		_, _, err := validatePressureMonitoring(body, monitoringRun(), time.Now())
		if (err == nil) != (count == 2000) {
			t.Fatal("series point boundary", count, err)
		}
	}
	body := monitoringRequest()
	series := []any{}
	for i := 0; i < 4; i++ {
		points := []any{}
		for j := 0; j < 1600; j++ {
			points = append(points, Obj{"timestamp_ms": int64(1793926800000 + j), "value": nil})
		}
		series = append(series, Obj{"key": fmt.Sprintf("s%d", i), "points": points})
	}
	body["series"] = series
	if _, _, err := validatePressureMonitoring(body, monitoringRun(), time.Now()); err == nil {
		t.Fatal("total point cap ignored")
	}
}

func monitoringFixture(t *testing.T) (*Platform, Obj, Obj) {
	t.Helper()
	p, plan := mysqlFixture(t)
	run := monitoringRun()
	run["id"], run["project_id"], run["status"], run["plan_id"] = newID("run-"), p.projectID(), "COMPLETED", plan["id"]
	run["created_at"] = now()
	if err := p.transaction(func(tx *Tx) error { return p.saveRun(tx, run) }); err != nil {
		t.Fatal(err)
	}
	report := Obj{"id": newID("pressure-"), "project_id": p.projectID(), "run_id": run["id"], "version": 1, "status": "READY", "created_at": now(), "run": safePressureRun(run), "metrics": Obj{"sample_count": 2, "rps": 1}, "verdict_status": "PASS", "extensions": Obj{"jvm": Obj{"status": "NOT_CONNECTED"}, "middleware": Obj{"status": "NOT_CONNECTED"}, "k8s": Obj{"status": "NOT_CONNECTED"}}}
	if _, err := p.DB.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,?,?,?,?,?)", report["id"], p.projectID(), run["id"], 1, "READY", report["created_at"], digest(encoded(report)), string(encoded(report))); err != nil {
		t.Fatal(err)
	}
	return p, run, report
}

func TestPressureMonitoringRejectsEmptySeriesAndOversizeTimestamps(t *testing.T) {
	for _, field := range []string{"points", "collected_at", "start", "end"} {
		t.Run(field, func(t *testing.T) {
			body := monitoringRequest()
			longTime := "2026-11-06T01:00:00." + strings.Repeat("0", 70) + "Z"
			switch field {
			case "points":
				obj(arr(body["series"])[0])["points"] = []any{}
			case "collected_at":
				obj(body["source"])[field] = longTime
			default:
				if field == "end" {
					longTime = "2026-11-06T01:00:02." + strings.Repeat("0", 70) + "Z"
				}
				obj(body["window"])[field] = longTime
			}
			if _, _, err := validatePressureMonitoring(body, monitoringRun(), time.Now()); err == nil {
				t.Fatal("empty series or timestamp byte cap accepted")
			}
		})
	}
}

func TestPressureMonitoringAPIRevisionsIdempotencyScopeAndImmutability(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	path := "/api/pressure-reports/" + id + "/monitoring"
	request, _ := workspaceHTTP(t, p)
	headers := map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": p.projectID(), "Idempotency-Key": "private-idempotency-key"}
	var original string
	if err := p.DB.QueryRow("SELECT data FROM pressure_reports WHERE id=?", id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	status, _, raw := request("GET", path, nil, headers)
	empty, _ := decode(raw)
	if status != 200 || number(empty["revision"]) != 0 || len(arr(empty["entries"])) != 0 || len(arr(empty["history"])) != 0 {
		t.Fatal(status, string(raw))
	}
	body := monitoringRequest()
	body["summary"] = "fixture-known-secret token=private-monitor-token"
	status, _, raw = request("POST", path, body, headers)
	first, _ := decode(raw)
	if status != 200 || number(first["revision"]) != 1 || obj(first["entry"])["component"] != "jvm" {
		t.Fatal(status, string(raw))
	}
	firstRaw := string(raw)
	second := monitoringRequest()
	second["revision"], second["component"], second["instance"] = 1, "redis", "redis-a"
	headers["Idempotency-Key"] = "second"
	status, _, raw = request("POST", path, second, headers)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	headers["Idempotency-Key"] = "private-idempotency-key"
	status, _, raw = request("POST", path, body, headers)
	if status != 200 || string(raw) != firstRaw {
		t.Fatal("retry did not return original revision/entry", status, string(raw))
	}
	changed := clone(body)
	changed["summary"] = "different"
	status, _, _ = request("POST", path, changed, headers)
	if status != 409 {
		t.Fatal("same-key conflict", status)
	}
	headers["Idempotency-Key"] = "stale"
	status, _, _ = request("POST", path, body, headers)
	if status != 409 {
		t.Fatal("revision conflict", status)
	}
	status, _, raw = request("GET", path, nil, headers)
	current, _ := decode(raw)
	if status != 200 || number(current["revision"]) != 2 || len(arr(current["entries"])) != 2 || number(obj(arr(current["history"])[0])["revision"]) != 2 {
		t.Fatal(status, string(raw))
	}
	status, _, raw = request("GET", path+"?revision=1", nil, headers)
	historical, _ := decode(raw)
	if status != 200 || number(historical["revision"]) != 1 || len(arr(historical["entries"])) != 1 {
		t.Fatal("historical snapshot", status, string(raw))
	}
	for _, query := range []string{"?revision=0", "?revision=-1", "?revision=1&revision=2", "?revision=bad", "?revision=129"} {
		status, _, _ = request("GET", path+query, nil, headers)
		if status != 400 && status != 404 {
			t.Fatal(query, status)
		}
	}
	status, _, raw = request("GET", "/api/pressure-reports/"+id, nil, headers)
	detail, _ := decode(raw)
	if status != 200 || number(obj(detail["monitoring"])["revision"]) != 2 || number(obj(detail["monitoring"])["instance_count"]) != 2 || obj(obj(detail["extensions"])["jvm"])["status"] != "AVAILABLE" || obj(obj(detail["extensions"])["middleware"])["status"] != "AVAILABLE" || obj(obj(detail["extensions"])["k8s"])["status"] != "NOT_CONNECTED" {
		t.Fatal("detail summary", status, string(raw))
	}
	var after string
	p.DB.QueryRow("SELECT data FROM pressure_reports WHERE id=?", id).Scan(&after)
	if after != original {
		t.Fatal("fixed report data overwritten")
	}
	internal, err := p.pressureGet(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, decorated := internal["monitoring"]; decorated || obj(obj(internal["extensions"])["jvm"])["status"] != "NOT_CONNECTED" {
		t.Fatal("internal report read includes dynamic monitoring overlay")
	}
	rows, err := p.DB.Query("SELECT metric_key,label,data FROM pressure_report_metrics WHERE report_id=? AND kind='monitoring_snapshot'", id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key, hash, data string
		rows.Scan(&key, &hash, &data)
		if len(key) != 64 || len(hash) != 64 || strings.Contains(data, "fixture-known-secret") || strings.Contains(data, "private-monitor-token") || strings.Contains(key, "private-idempotency-key") {
			t.Fatal("unsafe stored snapshot")
		}
	}
	rows.Close()
	project, err := p.projectCreate(Obj{"name": "Foreign monitoring"})
	if err != nil {
		t.Fatal(err)
	}
	headers["X-Project-ID"] = str(project["id"])
	for _, method := range []string{"GET", "POST"} {
		status, _, _ = request(method, path, body, headers)
		if status != 404 {
			t.Fatal("cross-project", method, status)
		}
	}
	headers["X-Project-ID"] = p.projectID()
	delete(headers, "Idempotency-Key")
	status, _, _ = request("POST", path, body, headers)
	if status != 400 {
		t.Fatal("missing idempotency", status)
	}
	headers["Idempotency-Key"] = "archive"
	project, err = p.projectGetQ(p.DB, p.projectID(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.projectUpdate(p.projectID(), Obj{"revision": project["revision"]}, true); err != nil {
		t.Fatal(err)
	}
	status, _, _ = request("POST", path, second, headers)
	if status != 409 {
		t.Fatal("archived write", status)
	}
	status, _, _ = request("GET", path, nil, headers)
	if status != 200 {
		t.Fatal("archived read", status)
	}
}

func TestPressureMonitoringConcurrentRevisionAndReplacement(t *testing.T) {
	p, _, report := monitoringFixture(t)
	id := str(report["id"])
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := p.writePressureMonitoring(id, fmt.Sprintf("race-%d", i), monitoringRequest())
			results <- err
		}(i)
	}
	group.Wait()
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
	body := monitoringRequest()
	body["revision"] = 1
	body["health"] = "WARNING"
	if _, err := p.writePressureMonitoring(id, "replace", body); err != nil {
		t.Fatal(err)
	}
	snapshot, err := p.pressureMonitoring(id, 0)
	if err != nil || len(arr(snapshot["entries"])) != 1 || obj(arr(snapshot["entries"])[0])["health"] != "WARNING" {
		t.Fatal(snapshot, err)
	}
	if _, err := p.DB.Exec("UPDATE pressure_reports SET status='PROCESSING' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	body["revision"] = 2
	if _, err := p.writePressureMonitoring(id, "not-ready", body); err == nil {
		t.Fatal("nonREADY write accepted")
	}
}

func TestPressureMonitoringSnapshotCapacityAndDiscovery(t *testing.T) {
	entry, _, err := validatePressureMonitoring(monitoringRequest(), monitoringRun(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{999, 1000} {
		entries := []any{}
		for i := 0; i < count; i++ {
			e := clone(entry)
			e["entry_id"] = fmt.Sprintf("instance-%d", i)
			entries = append(entries, e)
		}
		_, err := mergePressureMonitoringEntries(Obj{"revision": 1, "entries": entries, "history": []any{}}, []any{entry}, now(), false)
		if (err == nil) != (count == 999) {
			t.Fatal("instance limit", count, err)
		}
	}
	if _, err := mergePressureMonitoringEntries(Obj{"revision": 128, "entries": []any{}, "history": []any{}}, []any{entry}, now(), false); err == nil {
		t.Fatal("revision limit ignored")
	}
	huge := clone(entry)
	huge["summary"] = strings.Repeat("x", 8*1024*1024)
	if _, err := mergePressureMonitoringEntries(Obj{"revision": 0, "entries": []any{}, "history": []any{}}, []any{huge}, now(), false); err == nil {
		t.Fatal("snapshot bytes ignored")
	}
	path := obj(obj(openAPI()["paths"])["/api/pressure-reports/{id}/monitoring"])
	if len(obj(path["get"])) == 0 || len(obj(path["post"])) == 0 {
		t.Fatal("monitoring undiscoverable")
	}
	for method, name := range map[string]string{"get": "revision", "post": "Idempotency-Key"} {
		found := false
		for _, value := range arr(obj(path[method])["parameters"]) {
			parameter := obj(value)
			if parameter["name"] == name && (method != "post" || parameter["required"] == true) {
				found = true
			}
		}
		if !found {
			t.Fatalf("monitoring %s parameter %s undiscoverable", method, name)
		}
	}
}

func TestPressureMonitoringBackupRestoreAndReportVersionIsolation(t *testing.T) {
	p, run, report := monitoringFixture(t)
	id := str(report["id"])
	written, err := p.writePressureMonitoring(id, "portable", monitoringRequest())
	if err != nil {
		t.Fatal(err)
	}
	state, err := p.exportState()
	if err != nil {
		t.Fatal(err)
	}
	restored := emptyWorkspacePlatform(t, p, filepath.Join(t.TempDir(), "restored-monitoring"))
	if err := restored.importState(state); err != nil {
		t.Fatal(err)
	}
	current, err := restored.pressureMonitoring(id, 0)
	if err != nil || number(current["revision"]) != 1 || len(arr(current["entries"])) != 1 || obj(arr(current["entries"])[0])["entry_id"] != obj(written["entry"])["entry_id"] {
		t.Fatal("monitoring snapshot lost on restore", err)
	}
	retry, err := restored.writePressureMonitoring(id, "portable", monitoringRequest())
	if err != nil || string(encoded(retry)) != string(encoded(written)) {
		t.Fatal("restored idempotency changed", err)
	}
	next, err := p.generatePressure(str(run["id"]), "separate-report-version", Obj{"rebuild": true})
	if err != nil {
		t.Fatal(err)
	}
	independent, err := p.pressureMonitoring(str(next["id"]), 0)
	if err != nil || number(independent["revision"]) != 0 || len(arr(independent["entries"])) != 0 {
		t.Fatal("new parsed report inherited external evidence", err)
	}
}
