package main

import (
	"net/http/httptest"
	"testing"
)

func TestProjectResourceRequiresExplicitScope(t *testing.T) {
	for _, path := range []string{"/api/plans", "/api/runs", "/api/datasets/catalog", "/api/api-collections", "/api/environment-runtime", "/api/diagnostics"} {
		if _, err := requestProjectID(httptest.NewRequest("GET", path, nil)); err == nil {
			t.Errorf("implicit project accepted: %s", path)
		}
	}
	for _, path := range []string{"/api/projects", "/api/session", "/api/cluster", "/api/clusters/id", "/api/mcp/tools", "/api/openapi.json"} {
		id, err := requestProjectID(httptest.NewRequest("GET", path, nil))
		if err != nil || id != "" {
			t.Errorf("global endpoint scoped: %s %s %v", path, id, err)
		}
	}
}

func TestMySQLUnmappedClusterHasNoMembers(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.Config.DefaultClusterWorkers = nil
	group, err := p.findClusterQ(p.DB, clusterID("controller-a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(arr(group["worker_ids"])) != 0 || group["registration_source"] != "pending_configuration" {
		t.Fatal("unmapped core inherits shared pool", group)
	}
	if _, _, err = p.createRun(Obj{"cluster_id": group["id"], "plan_id": plan["id"], "environment_id": fixtureEnvironmentID(t, p), "name": "unmapped", "properties": Obj{}}, "unmapped"); err == nil {
		t.Fatal("unmapped cluster accepted execution")
	}
}
