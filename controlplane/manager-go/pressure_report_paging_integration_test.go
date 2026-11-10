package main

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

// Report timestamps have millisecond precision and versions are local to a run.
// Removing the unique ID tie breaker, or filtering after pagination, breaks
// these literal page expectations for simultaneous reports from multiple runs.
func TestMySQLPressureReportListOrdersTiesBeforePaging(t *testing.T) {
	p, _ := mysqlFixture(t)
	project, err := p.projectCreate(Obj{"name": "Other report project"})
	if err != nil {
		t.Fatal(err)
	}
	const sameTime = "2026-11-09T00:00:00.000Z"
	for _, row := range []struct {
		id, project, status, created string
		version                      int
	}{
		{"pressure-20", p.projectID(), "READY", sameTime, 1},
		{"pressure-60", p.projectID(), "FAILED", sameTime, 1},
		{"pressure-80", p.projectID(), "READY", sameTime, 1},
		{"pressure-40", p.projectID(), "READY", sameTime, 1},
		{"pressure-02", p.projectID(), "READY", sameTime, 2},
		{"pressure-99", p.projectID(), "READY", "2026-11-08T23:59:59.999Z", 10},
		{"pressure-foreign", str(project["id"]), "READY", sameTime, 20},
	} {
		report := Obj{"id": row.id, "project_id": row.project, "run_id": "run-" + row.id, "version": row.version, "status": row.status, "created_at": row.created, "verdict_status": "NOT_SET"}
		if _, err = p.DB.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,?,?,?,?,?)", row.id, row.project, report["run_id"], row.version, row.status, row.created, digest([]byte(row.id)), string(encoded(report))); err != nil {
			t.Fatal(err)
		}
	}
	request, _ := workspaceHTTP(t, p)
	checkPages := func(t *testing.T) {
		for _, tc := range []struct {
			name, project, filter string
			want                  []string
		}{
			{"project", p.projectID(), "", []string{"pressure-02", "pressure-80", "pressure-60", "pressure-40", "pressure-20", "pressure-99"}},
			{"ready", p.projectID(), "&status=READY", []string{"pressure-02", "pressure-80", "pressure-40", "pressure-20", "pressure-99"}},
			{"failed", p.projectID(), "&status=FAILED", []string{"pressure-60"}},
			{"foreign", str(project["id"]), "&status=READY", []string{"pressure-foreign"}},
			{"empty", p.projectID(), "&status=QUEUED", []string{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, size := range []int{1, 2, 3} {
					got := []string{}
					for offset := 0; offset <= len(tc.want); offset += size {
						path := fmt.Sprintf("/api/pressure-reports?limit=%d&offset=%d%s", size, offset, tc.filter)
						code, _, raw := request(http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": tc.project})
						page, err := decode(raw)
						if err != nil || code != http.StatusOK || int(number(page["total"])) != len(tc.want) {
							t.Fatalf("size=%d offset=%d: code=%d total=%v error=%v", size, offset, code, page["total"], err)
						}
						for _, value := range arr(page["items"]) {
							item := obj(value)
							if item["project_id"] != tc.project {
								t.Fatal("report from another project", item["id"])
							}
							got = append(got, str(item["id"]))
						}
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("page size %d: report IDs=%v, want %v", size, got, tc.want)
					}
				}
			})
		}
	}
	t.Run("indexed", checkPages)
	// An alternate optimizer plan must preserve the same order. Removing only
	// this disposable fixture's new index prevents its incidental ID ordering
	// from hiding a missing ORDER BY tie breaker in the application query.
	if _, err = p.DB.Exec("ALTER TABLE pressure_reports DROP INDEX report_project_time"); err != nil {
		t.Fatal(err)
	}
	t.Run("fallback", checkPages)
}
