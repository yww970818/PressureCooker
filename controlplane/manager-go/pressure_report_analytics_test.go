package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPressureAnalysisOptionsValidation(t *testing.T) {
	defaults, err := pressureAnalysisOptions(nil)
	if err != nil || defaults.ApdexSatisfiedMS != 500 || defaults.ApdexToleratedMS != 1500 || defaults.GranularityMS != 1000 {
		t.Fatal(defaults, err)
	}
	for _, v := range []any{"bad", Obj{"unknown": 1.0}, Obj{"apdex_satisfied_ms": "100"}, Obj{"apdex_satisfied_ms": 0.0}, Obj{"apdex_satisfied_ms": 1501.0}, Obj{"granularity_ms": 1.0}, Obj{"granularity_ms": 1000.5}} {
		if _, err := pressureAnalysisOptions(v); err == nil {
			t.Fatal("accepted invalid analysis options", v)
		}
	}
	valid, err := pressureAnalysisOptions(Obj{"apdex_satisfied_ms": 100.0, "apdex_tolerated_ms": 300.0, "granularity_ms": 5000.0})
	if err != nil || valid.ApdexSatisfiedMS != 100 || valid.GranularityMS != 5000 {
		t.Fatal(valid, err)
	}
}

func TestPressureAnalyticsScopeVersionAndExportIsolation(t *testing.T) {
	csv := "timeStamp,elapsed,label,success,responseMessage,responseCode,bytes,sentBytes,engine_id,Latency,Connect,allThreads\n1793926800000,100,login,true,OK,200,20,10,worker-1,40,10,3\n1793926800200,200,query,false,token=fixture-private-value,500,30,10,worker-1,50,20,3\n"
	p, run, cb := pressureFixtureWith(t, map[string]string{"results.jtl": csv})
	r := pressureComplete(t, p, run, cb)
	if r["status"] != "READY" {
		t.Fatal(r["status"], r["error"])
	}
	read := func(id, query string) Obj {
		t.Helper()
		v, e := p.pressureAnalytics(id, httptest.NewRequest("GET", "/analytics?"+query, nil))
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	id := str(r["id"])
	data := read(id, "")
	if number(data["detail_version"]) != 1 || number(obj(obj(data["scope"])["metrics"])["sample_count"]) != 2 {
		t.Fatal(data)
	}
	points := arr(obj(data["scope"])["points"])
	if len(points) != 1 || number(obj(points[0])["active_threads"]) != 3 {
		t.Fatal("per-sample threads inflated", points)
	}
	key := str(obj(arr(data["steps"])[0])["step_key"])
	scoped := read(id, "node_id=worker-1&step_key="+key)
	if number(obj(obj(scoped["scope"])["metrics"])["sample_count"]) != 1 {
		t.Fatal(scoped)
	}
	if read(id, "node_id=worker-other")["scope"] != nil {
		t.Fatal("fabricated unknown node scope")
	}
	if _, e := p.forProject("project-other").pressureAnalytics(id, httptest.NewRequest("GET", "/", nil)); e == nil {
		t.Fatal("cross project analytics readable")
	}
	if _, e := p.forProject("project-other").pressureExport(id); e == nil {
		t.Fatal("cross project export readable")
	}
	html, e := p.pressureExport(id)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(html), "APDEX") || !strings.Contains(string(html), "Latency") || strings.Contains(string(html), "fixture-private-value") || strings.Contains(string(html), "<script src=") {
		t.Fatal("export lost sections or leaked/loaded remote data")
	}
	for _, suffix := range []string{"", "/analytics", "/export.html"} {
		req := httptest.NewRequest("GET", "/api/pressure-reports/"+id+suffix, nil)
		req.Header.Set("Authorization", "Bearer "+p.Config.AdminToken)
		req.Header.Set("X-Project-ID", defaultProjectID)
		rec := httptest.NewRecorder()
		Handler{p}.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatal("HTTP route", suffix, rec.Code, rec.Body.String())
		}
		if suffix == "" && (strings.Contains(rec.Body.String(), `"dashboard"`) || strings.Contains(rec.Body.String(), `"node_logs"`)) {
			t.Fatal("status poll shipped heavy evidence")
		}
		if suffix == "/export.html" && (!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment")) {
			t.Fatal("offline download headers")
		}
		req = httptest.NewRequest("GET", "/api/pressure-reports/"+id+suffix, nil)
		rec = httptest.NewRecorder()
		Handler{p}.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatal("unauthenticated report evidence route", suffix, rec.Code)
		}
	}
	next, e := p.generatePressure(str(run["id"]), "analytics-custom", Obj{"rebuild": true, "analysis_options": Obj{"apdex_satisfied_ms": 50.0, "apdex_tolerated_ms": 100.0, "granularity_ms": 5000.0}})
	if e != nil {
		t.Fatal(e)
	}
	c, ok, e := p.claimPressure()
	if e != nil || !ok {
		t.Fatal(e)
	}
	p.workPressure(context.Background(), c)
	custom := read(str(next["id"]), "")
	if number(obj(custom["options"])["granularity_ms"]) != 5000 || number(obj(obj(custom["scope"])["apdex"])["score"]) >= number(obj(obj(data["scope"])["apdex"])["score"]) {
		t.Fatal("options did not create independently calculated version", custom)
	}
	if _, e := p.generatePressure(str(run["id"]), "analytics-custom", Obj{"analysis_options": Obj{"apdex_satisfied_ms": 1.0}}); e == nil {
		t.Fatal("options idempotency conflict accepted")
	}
	inherit, e := p.generatePressure(str(run["id"]), "analytics-inherit", Obj{"rebuild": true})
	if e != nil || number(obj(inherit["analysis_options"])["granularity_ms"]) != 5000 {
		t.Fatal("reparse lost frozen analysis options", inherit, e)
	}
	legacy := clone(r)
	delete(legacy, "dashboard")
	delete(legacy, "analytics_version")
	if _, e := p.DB.Exec("UPDATE pressure_reports SET data=? WHERE id=?", string(encoded(legacy)), id); e != nil {
		t.Fatal(e)
	}
	if number(read(id, "")["detail_version"]) != 0 {
		t.Fatal("old report missing legacy state")
	}
}

func TestPressureOfflineHTMLCannotExecuteEvidence(t *testing.T) {
	report := Obj{"id": "pressure-1", "run_name": "<script>alert('x')</script>", "metrics": Obj{"sample_count": 1}, "dashboard": Obj{"version": 1, "scopes": []any{Obj{"node_id": "", "step_key": "", "label": "<img src=x onerror=alert(1)>", "metrics": Obj{}, "errors": []any{Obj{"message": "</script><script>alert('payload')</script>"}}}}}, "node_logs": []any{Obj{"content": "<iframe src=https://example.invalid></iframe>"}}}
	data, err := renderPressureExport(report)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	for _, bad := range []string{"<script>alert(", "<img src=x", "<iframe src=https:", "fetch(", "XMLHttpRequest"} {
		if strings.Contains(raw, bad) {
			t.Fatal("unsafe offline output", bad)
		}
	}
	if !strings.Contains(raw, "&lt;script&gt;") {
		t.Fatal("escaped title missing")
	}
}
