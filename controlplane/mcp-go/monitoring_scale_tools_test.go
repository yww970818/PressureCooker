package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func monitoringBatchArgs(count int) map[string]any {
	args := monitoringWriteArgs()
	entry := map[string]any{}
	for k, v := range args {
		if k != "projectId" && k != "reportId" && k != "revision" && k != "idempotencyKey" {
			entry[k] = v
		}
	}
	entries := []any{}
	for i := 0; i < count; i++ {
		var copy map[string]any
		json.Unmarshal(mustMonitoringJSON(entry), &copy)
		copy["instance"] = fmt.Sprintf("app-%03d", i)
		entries = append(entries, copy)
	}
	return map[string]any{"projectId": args["projectId"], "reportId": args["reportId"], "revision": 0, "idempotencyKey": "jvm-batch-200", "entries": entries}
}

func TestMonitoringBatchWrites200InstancesWithOneScopedRequest(t *testing.T) {
	args := monitoringBatchArgs(200)
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.RequestURI() != "/api/pressure-reports/pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/monitoring/jvm-batch" || r.Header.Get("Idempotency-Key") != "jvm-batch-200" {
			t.Fatalf("unexpected batch request %s %s", r.Method, r.URL)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		want := map[string]any{"revision": float64(0), "entries": args["entries"]}
		if !reflect.DeepEqual(body, want) {
			t.Fatal("batch body changed or context leaked into entries")
		}
		io.WriteString(w, `{"revision":1,"written_count":200,"instance_count":204,"written_entries":[{"entry_id":"entry-1","instance":"app-000"}]}`)
	})
	result, err := m.call(context.Background(), "write_pressure_report_jvm_batch", args)
	if err != nil || calls != 1 {
		t.Fatal("batch unavailable", err, calls)
	}
	if !strings.Contains(string(mustMonitoringJSON(result)), `"written_count":200`) {
		t.Fatal(result)
	}
}

func TestMonitoringPagedReadFiltersMatchAPIAndPreserveZero(t *testing.T) {
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RequestURI() != "/api/pressure-reports/pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/monitoring?component=jvm&limit=5&offset=0&revision=6&search=app%26%E4%B8%AD" {
			t.Fatalf("bad paged request %s %s", r.Method, r.URL)
		}
		io.WriteString(w, `{"revision":6,"total":200,"limit":5,"offset":0,"entries":[],"component_counts":{"jvm":200}}`)
	})
	_, err := m.call(context.Background(), "get_pressure_report_monitoring", map[string]any{"projectId": "project-one", "reportId": "pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "component": "jvm", "limit": 5, "offset": 0, "revision": 6, "search": "app&中"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMonitoringBatchAndReadBoundsRejectBeforeHTTP(t *testing.T) {
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"empty batch", func(a map[string]any) { a["entries"] = []any{} }},
		{"over200", func(a map[string]any) { a["entries"] = monitoringBatchArgs(201)["entries"] }},
		{"missing revision", func(a map[string]any) { delete(a, "revision") }},
		{"entry project injection", func(a map[string]any) { a["entries"].([]any)[0].(map[string]any)["projectId"] = "project-other" }},
		{"entry component override", func(a map[string]any) { a["entries"].([]any)[0].(map[string]any)["component"] = "mysql" }},
		{"entry revision override", func(a map[string]any) { a["entries"].([]any)[0].(map[string]any)["revision"] = 0 }},
		{"duplicate instance", func(a map[string]any) {
			entries := a["entries"].([]any)
			entries[1].(map[string]any)["instance"] = entries[0].(map[string]any)["instance"]
		}},
		{"UTF8 size", func(a map[string]any) {
			a["entries"].([]any)[0].(map[string]any)["instance"] = strings.Repeat("中", 100)
		}},
		{"over4MiB", func(a map[string]any) {
			entries := monitoringBatchArgs(200)["entries"].([]any)
			for _, entry := range entries {
				e := entry.(map[string]any)
				e["summary"] = strings.Repeat("x", 4000)
				findings := []any{}
				for i := 0; i < 6; i++ {
					findings = append(findings, map[string]any{"level": "INFO", "title": "evidence", "detail": strings.Repeat("x", 4000)})
				}
				e["findings"] = findings
			}
			a["entries"] = entries
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := monitoringBatchArgs(2)
			tc.mutate(args)
			if _, err := m.call(context.Background(), "write_pressure_report_jvm_batch", args); err == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}
	for _, patch := range []map[string]any{{"limit": 201}, {"offset": -1}, {"component": "unknown"}, {"search": strings.Repeat("中", 100)}} {
		a := map[string]any{"projectId": "project-one", "reportId": "pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		for k, v := range patch {
			a[k] = v
		}
		if _, err := m.call(context.Background(), "get_pressure_report_monitoring", a); err == nil {
			t.Fatal("invalid page accepted", patch)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached manager %d times", calls)
	}
}

func TestMonitoringBatchTotalPointBoundary(t *testing.T) {
	args := monitoringBatchArgs(10)
	for _, value := range args["entries"].([]any) {
		entry := value.(map[string]any)
		series := []any{}
		for i := 0; i < 3; i++ {
			points := []any{}
			for j := 0; j < 2000; j++ {
				points = append(points, map[string]any{"timestamp_ms": 1794013200000 + j, "value": 0})
			}
			series = append(series, map[string]any{"key": fmt.Sprintf("s%d", i), "points": points})
		}
		entry["series"] = series
	}
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"revision":1,"written_count":10}`)
	})
	if _, err := m.call(context.Background(), "write_pressure_report_jvm_batch", args); err != nil || calls != 1 {
		t.Fatal("60000 valid points rejected", err, calls)
	}
	extra := monitoringBatchArgs(1)["entries"].([]any)[0].(map[string]any)
	extra["instance"] = "extra"
	extra["series"] = []any{map[string]any{"key": "one", "points": []any{map[string]any{"timestamp_ms": 1794013200000, "value": 0}}}}
	args["entries"] = append(args["entries"].([]any), extra)
	if _, err := m.call(context.Background(), "write_pressure_report_jvm_batch", args); err == nil {
		t.Fatal("60001 total points accepted")
	}
	if calls != 1 {
		t.Fatal("oversized batch reached manager", calls)
	}
}
