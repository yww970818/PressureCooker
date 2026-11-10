package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const pressureToolReportID = "pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const pressureToolOtherReportID = "pressure-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const pressureToolRunID = "run-cccccccccccccccccccccccccccccccc"

func pressureToolsFixture(t *testing.T, handler http.HandlerFunc) *managerTools {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("fixture-private-token"), 0600); e != nil {
		t.Fatal(e)
	}
	return &managerTools{api: managerClient{baseURL: server.URL, tokenFile: token, client: server.Client()}}
}
func TestPressureReportSpecsExposeExactCatalogAndProjectBoundary(t *testing.T) {
	expected := strings.Fields("list_pressure_reports get_pressure_report get_latest_pressure_report generate_pressure_report list_pressure_report_steps list_pressure_report_nodes list_pressure_report_issues list_pressure_report_logs list_pressure_report_samples get_pressure_report_analytics compare_pressure_reports export_pressure_report")
	specs := pressureReportSpecs()
	if len(specs) != len(expected) {
		t.Fatalf("report tools absent: got%d want%d", len(specs), len(expected))
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		seen[spec.name] = true
		if spec.description == "" || spec.resolved == nil {
			t.Fatalf("missing schema or description: %s", spec.name)
		}
		required := spec.schema["required"].([]string)
		found := false
		for _, key := range required {
			if key == "projectId" {
				found = true
			}
		}
		if !found {
			t.Fatalf("implicit project scope allowed: %s", spec.name)
		}
		if spec.readOnly != (spec.name != "generate_pressure_report") {
			t.Fatalf("wrong mutation annotation: %s", spec.name)
		}
	}
	for _, name := range expected {
		if !seen[name] {
			t.Fatalf("missing tool %s", name)
		}
	}
}
func TestPressureReportReadToolsUseExactScopedAPIRoutesAndQueries(t *testing.T) {
	node := "worker & other=/.."
	step := "step+query&filter=x"
	for _, tc := range []struct {
		name, path string
		args       map[string]any
		query      url.Values
	}{
		{"list_pressure_reports", "/api/pressure-reports", map[string]any{"executionId": pressureToolRunID, "status": "READY", "verdict": "PASS", "search": "接口 & login?next=true", "limit": 100, "offset": json.Number("0")}, url.Values{"run_id": {pressureToolRunID}, "status": {"READY"}, "verdict": {"PASS"}, "search": {"接口 & login?next=true"}, "limit": {"100"}, "offset": {"0"}}},
		{"get_pressure_report", "/api/pressure-reports/" + pressureToolReportID, map[string]any{"reportId": pressureToolReportID}, url.Values{}},
		{"get_latest_pressure_report", "/api/runs/" + pressureToolRunID + "/pressure-report", map[string]any{"executionId": pressureToolRunID}, url.Values{}},
		{"list_pressure_report_steps", "/api/pressure-reports/" + pressureToolReportID + "/steps", map[string]any{"reportId": pressureToolReportID, "nodeId": node, "stepKey": step, "failedOnly": false, "sort": "failure_rate", "limit": 20, "offset": 0}, url.Values{"node_id": {node}, "step_key": {step}, "failed_only": {"false"}, "sort": {"failure_rate"}, "limit": {"20"}, "offset": {"0"}}},
		{"list_pressure_report_nodes", "/api/pressure-reports/" + pressureToolReportID + "/nodes", map[string]any{"reportId": pressureToolReportID}, url.Values{}},
		{"list_pressure_report_issues", "/api/pressure-reports/" + pressureToolReportID + "/issues", map[string]any{"reportId": pressureToolReportID, "nodeId": node, "stepKey": step, "limit": 1, "offset": 1000000}, url.Values{"node_id": {node}, "step_key": {step}, "limit": {"1"}, "offset": {"1000000"}}},
		{"list_pressure_report_logs", "/api/pressure-reports/" + pressureToolReportID + "/logs", map[string]any{"reportId": pressureToolReportID, "nodeId": node, "stepKey": step, "limit": 20, "offset": 0}, url.Values{"node_id": {node}, "step_key": {step}, "limit": {"20"}, "offset": {"0"}}},
		{"list_pressure_report_samples", "/api/pressure-reports/" + pressureToolReportID + "/samples", map[string]any{"reportId": pressureToolReportID, "nodeId": node, "stepKey": step, "limit": 20, "offset": 0}, url.Values{"node_id": {node}, "step_key": {step}, "limit": {"20"}, "offset": {"0"}}},
		{"get_pressure_report_analytics", "/api/pressure-reports/" + pressureToolReportID + "/analytics", map[string]any{"reportId": pressureToolReportID, "nodeId": node, "stepKey": step}, url.Values{"node_id": {node}, "step_key": {step}}},
		{"compare_pressure_reports", "/api/pressure-reports/compare", map[string]any{"leftReportId": pressureToolReportID, "rightReportId": pressureToolOtherReportID}, url.Values{"left": {pressureToolReportID}, "right": {pressureToolOtherReportID}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != tc.path || !reflect.DeepEqual(r.URL.Query(), tc.query) {
					t.Errorf("wrong request: %s %s, want GET %s %v", r.Method, r.URL, tc.path, tc.query)
				}
				if r.Header.Get("X-Project-ID") != "project-one" || r.Header.Get("Authorization") != "Bearer fixture-private-token" || r.Header.Get("Idempotency-Key") != "" {
					t.Error("read lost project/auth boundary or added mutation key")
				}
				b, _ := io.ReadAll(r.Body)
				if len(b) != 0 {
					t.Error("read request sent body")
				}
				io.WriteString(w, `{"items":[{"response_body":null,"request_params":"","assertions_captured":false,"assertions":[]}],"total":1}`)
			})
			tc.args["projectId"] = "project-one"
			result, e := m.pressureReportTool(context.Background(), tc.name, tc.args)
			if e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatalf("wrong call count%d", calls)
			}
			item := result["items"].([]any)[0].(map[string]any)
			if item["response_body"] != nil || item["request_params"] != "" || item["assertions_captured"] != false || len(item["assertions"].([]any)) != 0 {
				t.Fatal("sample truth was rewritten", item)
			}
		})
	}
}
func TestPressureReportGeneratePreservesZerosFalseNullAndStableIdempotency(t *testing.T) {
	var bodies []map[string]any
	keys := []string{}
	m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/runs/"+pressureToolRunID+"/pressure-report" || r.Header.Get("X-Project-ID") != "project-one" {
			t.Error("wrong report generation route/project")
		}
		var body map[string]any
		if e := decodeJSONFromRequest(r, &body); e != nil {
			t.Error(e)
		}
		bodies = append(bodies, body)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		io.WriteString(w, `{"id":"`+pressureToolReportID+`","status":"QUEUED"}`)
	})
	args := map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": " stable-key ", "rebuild": false, "reportTargets": map[string]any{"min_rps": json.Number("0"), "min_success_rate": 0, "max_p99_ms": nil}, "analysisOptions": map[string]any{"apdex_satisfied_ms": 250, "apdex_tolerated_ms": 1000, "granularity_ms": 10000}}
	for i := 0; i < 2; i++ {
		if _, e := m.pressureReportTool(context.Background(), "generate_pressure_report", args); e != nil {
			t.Fatal(e)
		}
	}
	if len(bodies) != 2 || !reflect.DeepEqual(bodies[0], bodies[1]) || keys[0] != "stable-key" || keys[1] != "stable-key" || bodies[0]["rebuild"] != false {
		t.Fatalf("generation changed retry or dropped explicitfalse: %+v %+v", bodies, keys)
	}
	targets := bodies[0]["report_targets"].(map[string]any)
	if targets["min_rps"] != json.Number("0") || targets["min_success_rate"] != json.Number("0") || targets["max_p99_ms"] != nil || bodies[0]["analysis_options"] == nil {
		t.Fatalf("snake options/zero/null lost: %+v", bodies[0])
	}
	for _, key := range []string{"projectId", "executionId", "idempotencyKey", "reportTargets", "analysisOptions"} {
		if _, exists := bodies[0][key]; exists {
			t.Fatalf("transport-only/camel field leaked toAPI: %s", key)
		}
	}
}
func decodeJSONFromRequest(r *http.Request, target any) error {
	b, e := io.ReadAll(r.Body)
	if e != nil {
		return e
	}
	return decodeJSON(b, target)
}

func TestPressureReportInvalidArgumentsNeverReachManager(t *testing.T) {
	calls := 0
	m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, `{}`) })
	for index, tc := range []struct {
		name string
		args map[string]any
	}{
		{"get_pressure_report", map[string]any{"projectId": "project-one", "reportId": "../../private"}},
		{"get_pressure_report", map[string]any{"reportId": pressureToolReportID}},
		{"get_pressure_report", map[string]any{"projectId": "project-one", "reportId": pressureToolRunID}},
		{"get_latest_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolReportID}},
		{"list_pressure_reports", map[string]any{"projectId": "project-one", "limit": 101}},
		{"list_pressure_reports", map[string]any{"projectId": "project-one", "limit": 0}},
		{"list_pressure_reports", map[string]any{"projectId": "project-one", "offset": 1000001}},
		{"list_pressure_reports", map[string]any{"projectId": "project-one", "offset": -1}},
		{"list_pressure_reports", map[string]any{"projectId": "project-one", "run_id": pressureToolRunID}},
		{"list_pressure_report_steps", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "sort": "secret_sort"}},
		{"list_pressure_report_steps", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "failedOnly": "false"}},
		{"list_pressure_report_steps", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "nodeId": strings.Repeat("界", 2000)}},
		{"list_pressure_report_samples", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "stepKey": strings.Repeat("界", 50)}},
		{"list_pressure_report_logs", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "failedOnly": false}},
		{"list_pressure_report_issues", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "sort": "failure_rate"}},
		{"list_pressure_report_samples", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID}},
		{"list_pressure_report_samples", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "stepKey": ""}},
		{"list_pressure_report_samples", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "stepKey": " "}},
		{"list_pressure_report_samples", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "stepKey": "key", "sort": "failures"}},
		{"get_pressure_report_analytics", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "limit": 20}},
		{"list_pressure_report_nodes", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "offset": 0}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": " "}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "x\r\nother"}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "key", "rebuild": "false"}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "key", "reportTargets": map[string]any{"minRps": 0}}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "key", "reportTargets": map[string]any{"min_success_rate": 101}}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "key", "analysisOptions": map[string]any{"apdex_satisfied_ms": 0}}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "key", "analysisOptions": map[string]any{"apdex_satisfied_ms": 2000}}},
		{"generate_pressure_report", map[string]any{"projectId": "project-one", "executionId": pressureToolRunID, "idempotencyKey": "key", "analysisOptions": map[string]any{"granularity_ms": 1000.5}}},
		{"unknown_pressure_report", map[string]any{"projectId": "project-one"}},
	} {
		if _, e := m.pressureReportTool(context.Background(), tc.name, tc.args); e == nil {
			t.Fatalf("invalid input accepted: case %d %s", index, tc.name)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid calls reached manager: %d", calls)
	}
}
func TestPressureReportExportReturnsScopedAuthenticatedMetadataWithoutHTML(t *testing.T) {
	calls := 0
	m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/pressure-reports/"+pressureToolReportID || r.Header.Get("X-Project-ID") != "project-one" {
			t.Errorf("export fetched content or skipped scoped ownership: %s", r.URL.Path)
		}
		io.WriteString(w, `{"id":"`+pressureToolReportID+`","status":"READY","version":2,"dashboard":{"private":"private-analytics"},"node_logs":["private-log"],"html":"private-html"}`)
	})
	result, e := m.pressureReportTool(context.Background(), "export_pressure_report", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID})
	if e != nil {
		t.Fatal(e)
	}
	if calls != 1 || result["report_id"] != pressureToolReportID || result["project_id"] != "project-one" || result["status"] != "READY" || result["requires_authentication"] != true || result["method"] != "GET" || result["filename"] != "pressure-report.html" {
		t.Fatalf("missing authenticated metadata: %+v", result)
	}
	route := "/api/pressure-reports/" + pressureToolReportID + "/export.html"
	if result["download_path"] != route {
		t.Fatalf("wrong relative path: %+v", result)
	}
	download, e := url.Parse(result["download_url"].(string))
	if e != nil || download.Path != route || download.Query().Get("project_id") != "project-one" || download.User != nil || download.Host != "" {
		t.Fatalf("download must be authenticated relative path by default: %+v %v", result, e)
	}
	headers := result["request_headers"].(map[string]any)
	if headers["X-Project-ID"] != "project-one" || len(headers) != 1 {
		t.Fatalf("missing project header or embedded credentials: %+v", headers)
	}
	encoded, _ := json.Marshal(result)
	for _, private := range []string{"fixture-private-token", "Authorization", "private-html", "private-analytics", "private-log"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("export exposed private/large payload field: %s", private)
		}
	}
}
func TestPressureReportExportRejectsUnreadyReportsAndPreservesManagerErrors(t *testing.T) {
	for _, status := range []string{"QUEUED", "PROCESSING", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			calls := 0
			m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				io.WriteString(w, `{"id":"`+pressureToolReportID+`","status":"`+status+`"}`)
			})
			if _, e := m.pressureReportTool(context.Background(), "export_pressure_report", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID}); e == nil || calls != 1 {
				t.Fatalf("unready export accepted or fetched HTML: %v calls%d", e, calls)
			}
		})
	}
	m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"error":"private-manager-body"}`)
	})
	_, e := m.pressureReportTool(context.Background(), "get_pressure_report", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID})
	if e == nil || !strings.Contains(e.Error(), "404") || strings.Contains(e.Error(), "private-manager-body") {
		t.Fatalf("manager error boundary changed: %v", e)
	}
}

func TestPressureReportScopeUsesManagerUTF8ByteBounds(t *testing.T) {
	node := strings.Repeat("界", 1365) + "a"
	step := strings.Repeat("界", 42) + "ab"
	m := pressureToolsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("node_id") != node || r.URL.Query().Get("step_key") != step {
			t.Error("scope changed at exact manager byte bounds")
		}
		io.WriteString(w, `{"items":[],"total":0}`)
	})
	if _, e := m.pressureReportTool(context.Background(), "list_pressure_report_samples", map[string]any{"projectId": "project-one", "reportId": pressureToolReportID, "nodeId": node, "stepKey": step}); e != nil {
		t.Fatalf("exact API byte bounds rejected: %v", e)
	}
}
