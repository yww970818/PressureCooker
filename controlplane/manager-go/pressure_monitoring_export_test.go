package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPressureExportIncludesLatestMonitoringWithoutChangingCoreVersion(t *testing.T) {
	p, run, cb := pressureFixture(t)
	report := pressureComplete(t, p, run, cb)
	before, err := p.pressureSelect(str(report["id"]), "JSON_REMOVE(data,'$.request_samples')")
	if err != nil {
		t.Fatal(err)
	}
	request, _ := workspaceHTTP(t, p)
	status, _, raw := request(http.MethodPost, "/api/pressure-reports/"+str(report["id"])+"/monitoring", Obj{
		"revision": 0, "component": "jvm", "instance": "app-one", "source": Obj{"name": "外部GC观测"}, "window": Obj{"start": "2026-11-06T01:00:00Z", "end": "2026-11-06T01:00:02Z"},
		"metrics": []any{Obj{"key": "gc_count", "label": "GC次数", "value": 0, "unit": "次"}},
	}, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": p.projectID(), "Idempotency-Key": "export-monitoring-1"})
	if status != 200 && status != 201 {
		t.Fatalf("write status=%d %s", status, raw)
	}
	data, err := p.pressureExport(str(report["id"]))
	if err != nil {
		t.Fatal(err)
	}
	marker := `<script id="report-data" type="application/json">`
	parts := strings.SplitN(string(data), marker, 2)
	if len(parts) != 2 {
		t.Fatal("export data absent")
	}
	core, err := decode([]byte(strings.SplitN(parts[1], "</script>", 2)[0]))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := obj(core["monitoring_snapshot"])
	if number(snapshot["revision"]) != 1 || len(arr(snapshot["entries"])) != 1 || core["id"] != report["id"] || number(core["version"]) != number(report["version"]) {
		t.Fatalf("missing monitoring or changed core version: %v", snapshot)
	}
	if string(encoded(core["metrics"])) != string(encoded(before["metrics"])) {
		t.Fatal("monitoring changed exported JTL metrics")
	}
	if output := os.Getenv("JMETER_MONITORING_EXPORT_TEST_PATH"); output != "" {
		if err := os.WriteFile(output, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
