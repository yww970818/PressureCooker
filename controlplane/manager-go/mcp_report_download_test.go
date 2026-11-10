package main

import (
	"net/http/httptest"
	"testing"
)

func TestReportExportAcceptsNativeProjectScopeOnlyForFile(t *testing.T) {
	file := "/api/pressure-reports/pressure-one/export.html?project_id=project-one"
	project, e := requestProjectID(httptest.NewRequest("GET", file, nil))
	if e != nil || project != "project-one" {
		t.Fatal("report file project scope rejected", project, e)
	}
	for _, path := range []string{"/api/pressure-reports/pressure-one/samples?project_id=project-one", "/api/pressure-reports?project_id=project-one", "/api/pressure-reports/pressure-one/export.html/other?project_id=project-one"} {
		if _, e := requestProjectID(httptest.NewRequest("GET", path, nil)); e == nil {
			t.Fatal("query scope expanded beyond file", path)
		}
	}
	r := httptest.NewRequest("GET", file, nil)
	r.Header.Set("X-Project-ID", "project-two")
	if _, e := requestProjectID(r); e == nil {
		t.Fatal("conflicting project header accepted")
	}
	if _, e := requestProjectID(httptest.NewRequest("POST", file, nil)); e == nil {
		t.Fatal("query scope used on write")
	}
}
