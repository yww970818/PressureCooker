package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestHistoryDeleteToolsRoutesAndResult(t *testing.T) {
	cases := []struct {
		name, method, path string
		args, body         map[string]any
	}{
		{"delete_execution", "DELETE", "/api/runs/legacy-run", map[string]any{"executionId": "legacy-run"}, nil},
		{"delete_executions", "POST", "/api/runs/delete", map[string]any{"executionIds": []any{"run-one", "run-two"}}, map[string]any{"ids": []any{"run-one", "run-two"}}},
		{"clear_executions", "POST", "/api/runs/clear", map[string]any{"confirm": true}, map[string]any{"confirm": true}},
		{"delete_pressure_report", "DELETE", "/api/pressure-reports/report-old", map[string]any{"reportId": "report-old"}, nil},
		{"delete_pressure_reports", "POST", "/api/pressure-reports/delete", map[string]any{"reportIds": []any{"pressure-one", "pressure-two"}}, map[string]any{"ids": []any{"pressure-one", "pressure-two"}}},
		{"clear_pressure_reports", "POST", "/api/pressure-reports/clear", map[string]any{"confirm": true}, map[string]any{"confirm": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path || r.URL.RawQuery != "" {
					t.Errorf("wrong route %s %s", r.Method, r.URL)
				}
				if r.Header.Get("X-Project-ID") != "project-delete" || r.Header.Get("Authorization") != "Bearer fixture-private-token" {
					t.Error("lost explicit project/auth")
				}
				var body map[string]any
				raw, _ := io.ReadAll(r.Body)
				if len(raw) > 0 {
					if e := json.Unmarshal(raw, &body); e != nil {
						t.Fatal(e)
					}
				}
				if !reflect.DeepEqual(body, tc.body) {
					t.Errorf("body=%v want %v", body, tc.body)
				}
				io.WriteString(w, `{"deleted_count":2,"skipped_count":1,"cleanup_pending":2}`)
			})
			tc.args["projectId"] = "project-delete"
			value, e := m.call(context.Background(), tc.name, tc.args)
			if e != nil {
				t.Fatal(e)
			}
			got := value.(map[string]any)
			if calls != 1 || queryValue(got["deleted_count"]) != "2" || queryValue(got["skipped_count"]) != "1" || queryValue(got["cleanup_pending"]) != "2" {
				t.Fatal("result rewritten", value, calls)
			}
		})
	}
}
func TestHistoryDeleteToolsRejectUnsafeArgumentsBeforeNetwork(t *testing.T) {
	calls := 0
	m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	for _, name := range []string{"delete_execution", "delete_executions", "clear_executions", "delete_pressure_report", "delete_pressure_reports", "clear_pressure_reports"} {
		if _, e := m.call(context.Background(), name, map[string]any{}); e == nil {
			t.Fatal("missing scope accepted", name)
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"delete_execution", map[string]any{"executionId": "../escape"}},
		{"delete_execution", map[string]any{"executionId": "valid", "project_id": "other"}},
		{"delete_executions", map[string]any{"executionIds": []any{}}},
		{"delete_executions", map[string]any{"executionIds": []any{"same", "same"}}},
		{"delete_executions", map[string]any{"executionIds": []any{"valid", 2}}},
		{"delete_pressure_reports", map[string]any{"reportIds": []any{"valid", "../escape"}}},
		{"clear_executions", map[string]any{"confirm": false}},
		{"clear_executions", map[string]any{"confirm": "true"}},
		{"clear_executions", map[string]any{"confirm": true, "status": "COMPLETED"}},
		{"clear_pressure_reports", map[string]any{"confirm": true, "search": "one"}},
	} {
		tc.args["projectId"] = "project-delete"
		if _, e := m.call(context.Background(), tc.name, tc.args); e == nil {
			t.Fatal("invalid arguments accepted", tc.name, tc.args)
		}
	}
	ids := []any{}
	for i := 0; i < 101; i++ {
		ids = append(ids, queryValue(i))
	}
	if _, e := m.call(context.Background(), "delete_pressure_reports", map[string]any{"projectId": "project-delete", "reportIds": ids}); e == nil {
		t.Fatal("oversized batch accepted")
	}
	if calls != 0 {
		t.Fatal("invalid selection reached network", calls)
	}
}
func TestHistoryDeleteToolsPropagateManagerProtection(t *testing.T) {
	for _, status := range []int{401, 404, 409} {
		m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"code":"run_active","message":"private-detail"}}`)
		})
		_, e := m.call(context.Background(), "delete_execution", map[string]any{"projectId": "project-delete", "executionId": "run-protected"})
		if e == nil || e.Error() != "Manager API HTTP "+queryValue(status) {
			t.Fatal(status, e)
		}
	}
}
func TestHistoryDeleteToolsDestructiveCatalog(t *testing.T) {
	count := 0
	for _, s := range managerSpecs {
		switch s.name {
		case "delete_execution", "delete_executions", "clear_executions", "delete_pressure_report", "delete_pressure_reports", "clear_pressure_reports":
			count++
			if s.readOnly {
				t.Fatal("destructive tool marked read only", s.name)
			}
			required := s.schema["required"].([]string)
			found := false
			for _, k := range required {
				if k == "projectId" {
					found = true
				}
			}
			if !found {
				t.Fatal("project optional", s.name)
			}
		}
	}
	if count != 6 {
		t.Fatalf("got %d history deletion tools", count)
	}
}
