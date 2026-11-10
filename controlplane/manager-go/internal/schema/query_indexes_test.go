package schema

import "testing"

// Missing these prefixes makes project-wide and workflow-only dataset pages,
// or unfiltered report pages, sort rows outside the requested page in memory.
func TestQueryIndexesCoverDirectoryPaging(t *testing.T) {
	catalog, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) < 7 {
		t.Fatal("missing query indexes migration")
	}
	m := catalog[6]
	if m.Version != 7 || len(m.Columns) != 0 || len(m.Statements) != 0 {
		t.Fatal("query optimization must only add indexes")
	}
	want := map[string]string{
		"workflow_datasets.dataset_project_time":  "project_id,deleted,created_at,id",
		"workflow_datasets.dataset_workflow_time": "project_id,workflow_id,deleted,created_at,id",
		"pressure_reports.report_project_time":    "project_id,created_at,version,id",
	}
	for _, index := range m.Indexes {
		key := index.Table + "." + index.Name
		columns, exists := want[key]
		if !exists || index.Columns != columns {
			t.Errorf("unexpected query index %s(%s)", key, index.Columns)
		}
		delete(want, key)
	}
	for key := range want {
		t.Error("missing query index", key)
	}
}
