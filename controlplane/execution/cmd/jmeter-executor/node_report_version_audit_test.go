package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pressurecooker/execution/protocol"
)

func TestWorkerProbeRequiresDeclaredCurrentReportVersion(t *testing.T) {
	for _, version := range []any{nil, 0, 1, 2} {
		t.Run(stringJSON(version), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				report := map[string]any{"id": "worker-1", "role": "worker", "healthy": true, "status": "idle", "generation": 1, "job": nil}
				if version != nil {
					report["version"] = version
				}
				json.NewEncoder(w).Encode(report)
			}))
			defer server.Close()
			reporter := reporterFixture(t, "http://unused.invalid", &reportingQueue{})
			report := reporter.probe(context.Background(), protocol.Worker{ID: "worker-1", AgentURL: server.URL}, 1)
			if version == 1 {
				if !report.Healthy || report.Status != "idle" {
					t.Fatal("current worker rejected", report)
				}
			} else if report.Healthy || report.Status != "unreachable" {
				t.Fatal("undeclared or unsupported report promoted to current healthy evidence", report)
			}
		})
	}
}

func stringJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
