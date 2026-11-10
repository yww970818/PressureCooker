package main

import (
	"net/url"
	"testing"
)

func TestPlanArchiveQueryRequiresSingleBoolean(t *testing.T) {
	for _, test := range []struct {
		query string
		value bool
		valid bool
	}{{"", false, true}, {"include_archived=true", true, true}, {"include_archived=false", false, true}, {"include_archived=1", false, false}, {"include_archived=", false, false}, {"include_archived=true&include_archived=true", false, false}} {
		q, _ := url.ParseQuery(test.query)
		value, err := planIncludeArchived(q)
		if (err == nil) != test.valid || value != test.value {
			t.Fatal(test.query, value, err)
		}
	}
}

func TestMySQLPlanArchiveAuditFailureRollsBack(t *testing.T) {
	p, plan := mysqlFixture(t)
	if _, err := p.DB.Exec("DROP TABLE audit"); err != nil {
		t.Fatal(err)
	}
	_, call := workspaceHTTP(t, p)
	if code, value := call("POST", "/api/plans/"+str(plan["id"])+"/archive", Obj{"archived": true}); code != 500 {
		t.Fatal("audit failure ignored", code, value)
	}
	var count int
	if err := p.DB.QueryRow("SELECT COUNT(*) FROM plan_library_state").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed audit committed archive", count, err)
	}
}

func TestMySQLPlanArchiveSummaryFallbackUsesSingleConnection(t *testing.T) {
	p, plan := mysqlFixture(t)
	p.DB.SetMaxOpenConns(1)
	if _, err := p.DB.Exec("UPDATE plans SET summary_payload=NULL WHERE id=?", plan["id"]); err != nil {
		t.Fatal(err)
	}
	_, call := workspaceHTTP(t, p)
	for _, view := range []string{"", "&view=summary"} {
		code, value := call("GET", "/api/plans?include_archived=false&limit=1&offset=0"+view, nil)
		if code != 200 || len(arr(value["items"])) != 1 || obj(arr(value["items"])[0])["id"] != plan["id"] {
			t.Fatal("legacy projection fallback", code, value)
		}
		code, value = call("GET", "/api/plans?limit=1&offset=1"+view, nil)
		if code != 200 || number(value["total"]) != 1 || len(arr(value["items"])) != 0 {
			t.Fatal("empty page count", code, value)
		}
	}
}
