package schema

import (
	"strings"
	"testing"
)

func TestPerformanceMigrationAddsReadModelsWithoutDroppingLegacyData(t *testing.T) {
	catalog, e := Catalog()
	if e != nil {
		t.Fatal(e)
	}
	if len(catalog) < 4 {
		t.Fatal("missing additive page read models migration")
	}
	m := catalog[3]
	fields := map[string]bool{}
	indexes := map[string]bool{}
	for _, c := range m.Columns {
		fields[c.Table+"."+c.Name] = true
	}
	for _, i := range m.Indexes {
		indexes[i.Table+"."+i.Name] = true
	}
	for _, name := range []string{"workflows.summary_payload", "api_collections.summary_payload", "workflow_versions.summary_payload", "api_collection_versions.summary_payload", "plans.summary_payload", "pressure_reports.summary_data", "pressure_report_steps.node_hash", "pressure_report_steps.step_key", "pressure_report_issues.node_hash"} {
		if !fields[name] {
			t.Error("missing projected field", name)
		}
	}
	if !indexes["workflows.workspace_project_time"] {
		t.Error("workflow list not indexed by project and time")
	}
	sql := strings.Join(m.Statements, "\n")
	if !strings.Contains(sql, "workflow_csv_nodes") || !strings.Contains(sql, "pressure_dashboard_scopes") {
		t.Fatal("missing query scope models")
	}
	for _, line := range m.Statements {
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "DROP ") || strings.Contains(upper, "TRUNCATE ") {
			t.Fatal("destructive migration")
		}
	}
}
