package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func mustMonitoringJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func monitoringWriteArgs() map[string]any {
	return map[string]any{
		"projectId": "project-one", "reportId": "pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "revision": 0, "idempotencyKey": "monitoring-request-1",
		"instance": "service-1", "source": map[string]any{"name": "Prometheus", "reference": "snapshot:collector-1"},
		"window": map[string]any{"start": "2026-11-07T01:00:00Z", "end": "2026-11-07T01:01:00Z"}, "health": "UNKNOWN",
		"metrics":  []any{map[string]any{"key": "gc_count", "label": "GC 次数", "value": 0, "unit": "次"}, map[string]any{"key": "heap_used", "label": "堆使用率", "value": nil, "unit": "%"}},
		"series":   []any{map[string]any{"key": "heap_used", "label": "堆使用率", "unit": "%", "points": []any{map[string]any{"timestamp_ms": 1794013200000, "value": nil}, map[string]any{"timestamp_ms": 1794013201000, "value": 0}}}},
		"findings": []any{map[string]any{"level": "INFO", "title": "采集说明", "detail": "未知与零值分别保留"}},
	}
}

func TestMonitoringToolsWriteScopedVersionedDataWithoutExecuting(t *testing.T) {
	for _, component := range []string{"jvm", "mysql", "es", "redis", "kafka"} {
		t.Run(component, func(t *testing.T) {
			args := monitoringWriteArgs()
			name := "write_pressure_report_jvm"
			if component != "jvm" {
				name = "write_pressure_report_middleware"
				args["component"] = component
			}
			calls := 0
			m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.RequestURI() != "/api/pressure-reports/pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/monitoring" || r.Header.Get("Idempotency-Key") != "monitoring-request-1" {
					t.Fatalf("unexpected manager request: %s %s key=%q", r.Method, r.URL, r.Header.Get("Idempotency-Key"))
				}
				var body map[string]any
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				want := map[string]any{}
				for k, v := range args {
					if k != "projectId" && k != "reportId" && k != "idempotencyKey" {
						want[k] = v
					}
				}
				want["component"] = component
				raw, _ := json.Marshal(want)
				var normalized map[string]any
				json.Unmarshal(raw, &normalized)
				if !reflect.DeepEqual(body, normalized) {
					t.Fatalf("payload differs: %s", mustMonitoringJSON(body))
				}
				io.WriteString(w, `{"schema_version":"1","revision":1,"entry":{"component":"`+component+`","metrics":[{"value":0},{"value":null}]}}`)
			})
			result, e := m.call(context.Background(), name, args)
			if e != nil || calls != 1 {
				t.Fatalf("calls=%d error=%v", calls, e)
			}
			if !strings.Contains(string(mustMonitoringJSON(result)), `"value":null`) || !strings.Contains(string(mustMonitoringJSON(result)), `"value":0`) {
				t.Fatal("unknown/zero values lost", result)
			}
		})
	}
}

func TestMonitoringReadSelectsExplicitRevision(t *testing.T) {
	for _, historical := range []bool{false, true} {
		args := map[string]any{"projectId": "project-one", "reportId": "pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
		want := "/api/pressure-reports/pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/monitoring?limit=20&offset=0"
		if historical {
			args["revision"] = 1
			want += "&revision=1"
		}
		m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.RequestURI() != want || r.Header.Get("Idempotency-Key") != "" {
				t.Fatalf("unexpected read %s %s", r.Method, r.URL)
			}
			io.WriteString(w, `{"revision":0,"entries":[],"history":[]}`)
		})
		if _, e := m.call(context.Background(), "get_pressure_report_monitoring", args); e != nil {
			t.Fatal(e)
		}
	}
}

func TestMonitoringInvalidArgumentsNeverReachManager(t *testing.T) {
	calls := 0
	m := parityToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing project", func(a map[string]any) { delete(a, "projectId") }},
		{"report path traversal", func(a map[string]any) { a["reportId"] = "../pressure-one" }},
		{"missing expected revision", func(a map[string]any) { delete(a, "revision") }},
		{"negative revision", func(a map[string]any) { a["revision"] = -1 }},
		{"missing stable key", func(a map[string]any) { delete(a, "idempotencyKey") }},
		{"header injection", func(a map[string]any) { a["idempotencyKey"] = "one\nInjected: bad" }},
		{"blank instance", func(a map[string]any) { a["instance"] = " " }},
		{"unknown field", func(a map[string]any) { a["token"] = "private" }},
		{"jvm component override", func(a map[string]any) { a["component"] = "mysql" }},
		{"unknown source field", func(a map[string]any) { a["source"].(map[string]any)["token"] = "private" }},
		{"text numeric value", func(a map[string]any) { a["metrics"].([]any)[0].(map[string]any)["value"] = "0" }},
		{"invalid health", func(a map[string]any) { a["health"] = "PASS" }},
		{"missing points", func(a map[string]any) { delete(a["series"].([]any)[0].(map[string]any), "points") }},
		{"no evidence", func(a map[string]any) { a["metrics"] = []any{}; a["series"] = []any{}; a["findings"] = []any{} }},
		{"oversized UTF8 text", func(a map[string]any) { a["instance"] = strings.Repeat("中", 100) }},
		{"too many total points", func(a map[string]any) {
			series := []any{}
			for i := 0; i < 4; i++ {
				points := []any{}
				for j := 0; j < 1501; j++ {
					points = append(points, map[string]any{"timestamp_ms": 1794013200000 + j, "value": 0})
				}
				series = append(series, map[string]any{"key": []string{"one", "two", "three", "four"}[i], "label": "series", "points": points})
			}
			a["series"] = series
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := monitoringWriteArgs()
			tc.mutate(args)
			if _, e := m.call(context.Background(), "write_pressure_report_jvm", args); e == nil {
				t.Fatal("invalid write accepted")
			}
		})
	}
	args := monitoringWriteArgs()
	args["component"] = "jvm"
	if _, e := m.call(context.Background(), "write_pressure_report_middleware", args); e == nil {
		t.Fatal("middleware accepts jvm")
	}
	if calls != 0 {
		t.Fatalf("invalid requests performed %d HTTP calls", calls)
	}
}
