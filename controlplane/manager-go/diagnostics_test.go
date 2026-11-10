package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestReadinessDiffersFromLivenessWithoutDependencies(t *testing.T) {
	h, _ := frontendFileFixture(t)
	alive := frontendRequest(h, "GET", "/healthz", nil)
	ready := frontendRequest(h, "GET", "/readyz", nil)
	if alive.Code != 200 || ready.Code != 503 || ready.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("liveness=%d readiness=%d headers=%v", alive.Code, ready.Code, ready.Header())
	}
	value, err := decode(ready.Body.Bytes())
	if err != nil || value["ok"] != false || obj(value["checks"])["mysql"] != "unavailable" {
		t.Fatal("readiness omitted dependency state")
	}
}

func TestRequestCorrelationIsGeneratedAndLogsAreRedacted(t *testing.T) {
	h, _ := frontendFileFixture(t)
	var output bytes.Buffer
	original := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(original)
	w := frontendRequest(h, "GET", "/api/session?api_key=sensitive-query-token", map[string]string{"Authorization": "Bearer sensitive-auth-value", "X-Request-ID": "sensitive-client-request-id"})
	id := w.Header().Get("X-Request-ID")
	if w.Code != 401 || !regexp.MustCompile(`^req-[a-f0-9]{32}$`).MatchString(id) {
		t.Fatalf("missing generated correlation ID: status=%d id=%q", w.Code, id)
	}
	if !strings.Contains(output.String(), "request_id="+id) || !strings.Contains(output.String(), "code=unauthorized") || strings.Contains(output.String(), "sensitive-") {
		t.Fatal("error correlation missing or sensitive input logged")
	}
}

func TestReadinessTracksDependencyAndControlLoopProgress(t *testing.T) {
	p, _ := mysqlFixture(t)
	nodeRedisFixture(t, p)
	ctx := context.Background()
	if value := p.readiness(ctx); value["ok"] != false || obj(value["checks"])["scheduler"] != "starting" {
		t.Fatal("unstarted control loop reported ready")
	}
	p.recordLoop("scheduler", nil)
	p.recordLoop("dispatcher", nil)
	if value := p.forProject(defaultProjectID).readiness(ctx); value["ok"] != true {
		t.Fatalf("healthy dependencies not ready: %v", value)
	}
	p.runtime.mu.Lock()
	p.runtime.loops["dispatcher"] = loopProgress{At: time.Now().Add(-time.Minute)}
	p.runtime.mu.Unlock()
	if value := p.readiness(ctx); value["ok"] != false || obj(value["checks"])["dispatcher"] != "stale" {
		t.Fatal("stalled dispatcher reported ready")
	}
	p.recordLoop("dispatcher", errors.New("sensitive-failure-reason"))
	if value := p.readiness(ctx); value["ok"] != false || obj(value["checks"])["dispatcher"] != "failed" || strings.Contains(string(encoded(value)), "sensitive-") {
		t.Fatal("failed loop readiness or redaction")
	}
	p.recordLoop("dispatcher", nil)
	realRedis := p.Config.RedisAddr
	p.Config.RedisAddr = "127.0.0.1:1"
	if value := p.readiness(ctx); value["ok"] != false || obj(value["checks"])["redis"] != "unavailable" || strings.Contains(string(encoded(value)), "127.0.0.1") {
		t.Fatal("failed dependency readiness or redaction")
	}
	p.Config.RedisAddr = realRedis
	p.DB.SetMaxOpenConns(1)
	conn, err := p.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	timer := time.AfterFunc(500*time.Millisecond, func() { conn.Close() })
	defer timer.Stop()
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	value := p.readiness(bounded)
	if time.Since(start) > 350*time.Millisecond || value["ok"] != false || obj(value["checks"])["mysql"] != "unavailable" {
		t.Fatalf("readiness ignored request deadline: %v", value)
	}
}

func TestDiagnosticsRequireAuthenticationAndScopeBacklogsToProject(t *testing.T) {
	p, plan, environment := newRunnerFixture(t)
	_, _, err := p.createRun(Obj{"name": "diagnostics", "plan_id": plan["id"], "environment_id": environment["id"], "cluster_id": clusterID("controller-a")}, "diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	project, err := p.projectCreate(Obj{"name": "second project"})
	if err != nil {
		t.Fatal(err)
	}
	second := str(project["id"])
	if _, err = p.DB.Exec("INSERT INTO task_dispatches(id,project_id,status,created_at,payload,last_error) VALUES(?,?,?,?,?,?)", newID("job-"), second, "FAILED", now(), "sensitive-payload", "sensitive-error"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ project, status string }{{defaultProjectID, "PROCESSING"}, {second, "FAILED"}} {
		if _, err = p.DB.Exec("INSERT INTO pressure_report_jobs(id,project_id,run_id,status,created_at) VALUES(?,?,?,?,?)", newID("pr-"), item.project, newID("job-"), item.status, now()); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler{p}
	denied := frontendRequest(h, "GET", "/api/diagnostics", map[string]string{"Authorization": "Bearer invalid"})
	if denied.Code != 401 {
		t.Fatalf("unauthorized diagnostics=%d", denied.Code)
	}
	for _, projectID := range []string{defaultProjectID, second} {
		w := frontendRequest(h, "GET", "/api/diagnostics", map[string]string{"Authorization": "Bearer admin-secret", "X-Project-ID": projectID})
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("diagnostics=%d headers=%v", w.Code, w.Header())
		}
		value, e := decode(w.Body.Bytes())
		if e != nil {
			t.Fatal(e)
		}
		dispatch, reports := obj(value["dispatch"]), obj(value["pressure_reports"])
		if value["project_id"] != projectID {
			t.Fatal("missing project scope")
		}
		if projectID == defaultProjectID {
			if number(dispatch["pending"]) != 1 || number(dispatch["failed"]) != 0 || number(reports["processing"]) != 1 || number(reports["failed"]) != 0 {
				t.Fatal("other project leaked into default diagnostics")
			}
		} else if number(dispatch["pending"]) != 0 || number(dispatch["failed"]) != 1 || number(reports["processing"]) != 0 || number(reports["failed"]) != 1 {
			t.Fatal("default project leaked into second diagnostics")
		}
		if strings.Contains(w.Body.String(), "sensitive-") || strings.Contains(w.Body.String(), "admin-secret") || strings.Contains(w.Body.String(), p.Config.RedisAddr) {
			t.Fatal("diagnostics leaked payload, credentials or address")
		}
	}
	// Runtime counters and pool observations describe this process across projects.
	p.recordDispatch("")
	p.recordDispatch("redis_unavailable")
	p.recordDispatch("dispatch_payload_invalid")
	value, err := p.forProject(second).diagnostics(context.Background())
	if err != nil || obj(value["process"])["dispatch_published"] != uint64(1) || obj(value["process"])["dispatch_retried"] != uint64(1) || obj(value["process"])["dispatch_failed"] != uint64(1) {
		t.Fatal("process observations not shared", err)
	}
}

func TestDiagnosticIndexesMigrateExistingTablesIdempotently(t *testing.T) {
	p, _ := mysqlFixture(t)
	forgetSchemaHistory(t, p)
	for table, index := range map[string]string{"task_dispatches": "dispatch_project_status", "pressure_report_jobs": "report_job_project_status"} {
		if _, err := p.DB.Exec("DROP INDEX " + index + " ON " + table); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := p.migrateSchema(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for table, index := range map[string]string{"task_dispatches": "dispatch_project_status", "pressure_report_jobs": "report_job_project_status"} {
		var count int
		if err := p.DB.QueryRow("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", p.DB.Prefix+table, index).Scan(&count); err != nil || count == 0 {
			t.Fatalf("missing prefixed diagnostic index: %s %v", index, err)
		}
	}
}
