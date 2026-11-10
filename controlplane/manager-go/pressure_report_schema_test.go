package main

import (
	"strings"
	"testing"
)

func TestPressureReportTablesAreIsolatedAndPortable(t *testing.T) {
	for _, table := range []string{"pressure_reports", "pressure_report_jobs", "pressure_report_sources", "pressure_report_steps", "pressure_report_timeline", "pressure_report_issues", "pressure_report_metrics", "pressure_report_findings"} {
		if got := rewriteTables("SELECT * FROM "+table, "isolated_"); got != "SELECT * FROM isolated_"+table {
			t.Errorf("unisolated %s: %s", table, got)
		}
		if !contains(portableTables, table) || len(tableColumns[table]) == 0 {
			t.Errorf("not portable %s", table)
		}
	}
	if got := rewriteTables("SELECT * FROM pressure_reports JOIN runs ON 1=1", "test_"); !strings.Contains(got, "test_pressure_reports JOIN test_runs") {
		t.Error(got)
	}
}
