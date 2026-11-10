package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPressureInterfaceFailureFilterAndWholeReportSort(t *testing.T) {
	var csv strings.Builder
	csv.WriteString("timeStamp,elapsed,label,success,responseMessage,engine_id\n")
	for _, c := range []struct {
		label                  string
		total, failed, elapsed int
	}{{"slow-success", 10, 0, 900}, {"rare-failure", 100, 10, 300}, {"high-failure", 10, 9, 200}, {"all-failed", 2, 2, 100}, {"zero-rate", 5, 0, 400}} {
		for i := 0; i < c.total; i++ {
			fmt.Fprintf(&csv, "1793926800000,%d,%s,%t,fixture,worker-1\n", c.elapsed, c.label, i >= c.failed)
		}
	}
	p, run, cb := pressureFixtureWith(t, map[string]string{"results.jtl": csv.String()})
	r := pressureComplete(t, p, run, cb)
	if r["status"] != "READY" {
		t.Fatal(r["status"], r["error"])
	}
	id := str(r["id"])
	read := func(query string) Obj {
		t.Helper()
		v, e := p.pressureChildren(id, "steps", httptest.NewRequest("GET", "/steps?"+query, nil))
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	first := read("sort=failure_rate&limit=1")
	if number(first["total"]) != 5 || obj(arr(first["items"])[0])["label"] != "all-failed" {
		t.Fatal("failure rates must sort all interfaces before pagination", first)
	}
	second := read("sort=failure_rate&limit=1&offset=1")
	if obj(arr(second["items"])[0])["label"] != "high-failure" || number(obj(arr(second["items"])[0])["failure_rate"]) != 90 {
		t.Fatal(second)
	}
	failures := read("failed_only=true&sort=failure_rate&limit=2")
	if number(failures["total"]) != 3 || len(arr(failures["items"])) != 2 {
		t.Fatal("failure filtering must precede count/pagination", failures)
	}
	row := obj(arr(failures["items"])[1])
	if number(row["sample_count"]) != 10 || number(row["success_count"]) != 1 || number(row["failure_count"]) != 9 {
		t.Fatal("failed_only changed sampling denominator", row)
	}
	tail := read("failed_only=true&sort=failure_rate&limit=2&offset=2")
	if len(arr(tail["items"])) != 1 || obj(arr(tail["items"])[0])["label"] != "rare-failure" {
		t.Fatal(tail)
	}
	latency := read("failed_only=true&sort=latency&limit=1")
	if obj(arr(latency["items"])[0])["label"] != "rare-failure" {
		t.Fatal(latency)
	}
	if number(read("node_id=worker-1&failed_only=true")["total"]) != 3 || number(read("node_id=worker-other&failed_only=true")["total"]) != 0 {
		t.Fatal("node scope lost")
	}
	if number(read("failed_only=false")["total"]) != 5 {
		t.Fatal("false filtered rows")
	}
	if _, e := p.pressureChildren(id, "steps", httptest.NewRequest("GET", "/steps?failed_only=invalid", nil)); e == nil {
		t.Fatal("invalid failure flag accepted")
	}
	if _, e := p.forProject("project-other").pressureChildren(id, "steps", httptest.NewRequest("GET", "/steps?failed_only=true&sort=failure_rate", nil)); e == nil {
		t.Fatal("cross project steps readable")
	}
	// A legacy empty step has an unknown rate; it belongs after a measured 0%.
	if _, e := p.DB.Exec("UPDATE pressure_report_steps SET sample_count=0,failure_count=0,count_value=0,data=JSON_SET(data,'$.sample_count',0,'$.failure_count',0,'$.success_rate',NULL) WHERE report_id=? AND label='zero-rate'", id); e != nil {
		t.Fatal(e)
	}
	all := arr(read("sort=failure_rate&limit=100")["items"])
	if obj(all[len(all)-1])["label"] != "zero-rate" || obj(all[len(all)-1])["failure_rate"] != nil {
		t.Fatal("unknown failure rate was treated as zero", all)
	}
	zero := obj(all[len(all)-2])
	if zero["label"] != "slow-success" || number(zero["failure_rate"]) != 0 {
		t.Fatal("measured zero rate lost", zero)
	}
	// Very close rates still sort by the ratio rather than integer/rounded percent.
	for label, total := range map[string]int{"rare-failure": 1000001, "high-failure": 1000000} {
		if _, e := p.DB.Exec("UPDATE pressure_report_steps SET sample_count=?,failure_count=1,count_value=1,data=JSON_SET(data,'$.sample_count',?,'$.failure_count',1) WHERE report_id=? AND label=?", total, total, id, label); e != nil {
			t.Fatal(e)
		}
	}
	closeRates := read("failed_only=true&sort=failure_rate&limit=1&offset=1")
	if obj(arr(closeRates["items"])[0])["label"] != "high-failure" {
		t.Fatal("close rate sorting rounded early", closeRates)
	}
}
