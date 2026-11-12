package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"pressurecooker/manager/reporting"
	"strings"
	"unicode/utf8"
)

func pressureTargets(value any) (reporting.Targets, error) {
	var t reporting.Targets
	if value == nil {
		return t, nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return t, api(400, "invalid_report_targets", "验收目标须为数值对象")
	}
	allowed := map[string]bool{"min_rps": true, "min_success_rate": true, "max_p95_ms": true, "max_p99_ms": true, "min_duration_seconds": true}
	for k, v := range m {
		if !allowed[k] {
			return t, api(400, "invalid_report_targets", "存在未知验收目标")
		}
		if v != nil {
			switch v.(type) {
			case float64, int, int64, json.Number:
			default:
				return t, api(400, "invalid_report_targets", "验收目标须为数值")
			}
		}
	}
	if e := json.Unmarshal(encoded(m), &t); e != nil {
		return t, api(400, "invalid_report_targets", "验收目标无效")
	}
	if e := reporting.ValidateTargets(t); e != nil {
		return t, api(400, "invalid_report_targets", "验收目标须为有效范围内的非负数，成功率为0至100")
	}
	return t, nil
}
func safePressureRun(run Obj) Obj {
	redact := pressureRedactor(run)
	out := Obj{}
	for _, k := range []string{"id", "name", "status", "plan_id", "plan_name", "plan_sha256", "cluster_id", "workers", "created_at", "started_at", "finished_at", "attempt_id", "timeout_seconds"} {
		out[k] = run[k]
	}
	workers := []any{}
	for _, w := range arr(run["workers"]) {
		workers = append(workers, reporting.PublicNodeID(str(w), redact))
	}
	out["workers"] = workers
	out["name"] = redact(str(run["name"]))
	out["plan_name"] = redact(str(run["plan_name"]))
	env := obj(run["environment_snapshot"])
	out["environment_name"] = redact(str(env["name"]))
	out["environment_id"] = run["environment_id"]
	out["environment_revision"] = env["revision"]
	load := Obj{}
	for _, k := range []string{"threads", "concurrency", "ramp_up", "rampup", "loops", "duration", "duration_seconds", "rps", "tps"} {
		if v, ok := obj(run["properties"])[k]; ok {
			load[k] = redact(str(v))
		}
	}
	out["load"] = load
	// Never include historical properties, URLs, headers or bodies in report snapshots.
	return out
}
func pressureRedactor(run Obj) func(string) string {
	values := []string{}
	for k := range sensitiveRunProperties(run) {
		if v := str(obj(run["properties"])[k]); v != "" {
			values = append(values, v)
		}
	}
	values = append(values, cookieAccountSecrets(obj(run["properties"]))...)
	return reporting.NewRedactor(values)
}
func pressureSourceIdentity(run Obj) string {
	return digest(encoded(Obj{"attempt": run["attempt_id"], "files": obj(run["raw_archive"])["files"], "node_evidence": run["node_evidence"], "parser": reporting.ParserVersion, "schema": reporting.SchemaVersion}))
}

// Caller holds the run lock. The durable intent commits with completion; it does no I/O.
func (p *Platform) enqueuePressureQ(tx *Tx, run Obj, targets reporting.Targets, origin, nonce string, analysisOptions ...reporting.DashboardOptions) (Obj, error) {
	options := reporting.DefaultDashboardOptions()
	if len(analysisOptions) > 0 {
		options = analysisOptions[0]
	}
	var err error
	options, err = reporting.NormalizeDashboardOptions(options)
	if err != nil {
		return nil, err
	}
	optionsObject := dtoObject(options)
	identity := digest(encoded(Obj{"source": pressureSourceIdentity(run), "targets": targets, "review": nonce, "analysis_options": optionsObject}))
	var raw string
	e := tx.QueryRow("SELECT data FROM pressure_reports WHERE run_id=? AND identity_hash=?", run["id"], identity).Scan(&raw)
	if e == nil {
		return decode([]byte(raw))
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	var version int
	if e = tx.QueryRow("SELECT COALESCE(MAX(version),0)+1 FROM pressure_reports WHERE run_id=?", run["id"]).Scan(&version); e != nil {
		return nil, e
	}
	previous := ""
	e = tx.QueryRow("SELECT id FROM pressure_reports WHERE run_id=? AND status='READY' ORDER BY version DESC LIMIT 1", run["id"]).Scan(&previous)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	safe := safePressureRun(run)
	report := Obj{"id": newID("pressure-"), "project_id": objectProject(run), "run_id": run["id"], "version": version, "status": "QUEUED", "created_at": now(), "finished_at": "", "run": safe, "run_name": safe["name"], "run_status": safe["status"], "environment_name": safe["environment_name"], "plan_name": safe["plan_name"], "targets": targets, "targets_origin": origin, "verdict_status": "NOT_SET", "previous_ready_id": previous, "error": "", "parser_version": reporting.ParserVersion, "schema_version": reporting.SchemaVersion, "source_fingerprint": pressureSourceIdentity(run)}
	report["analysis_options"] = optionsObject
	if _, e = tx.Exec("INSERT INTO pressure_reports(id,project_id,run_id,version,status,created_at,identity_hash,data) VALUES(?,?,?,?,?,?,?,?)", report["id"], report["project_id"], run["id"], version, "QUEUED", report["created_at"], identity, string(encoded(report))); e != nil {
		return nil, e
	}
	_, e = tx.Exec("INSERT INTO pressure_report_jobs(id,project_id,run_id,status,created_at) VALUES(?,?,?,?,?)", report["id"], report["project_id"], run["id"], "QUEUED", report["created_at"])
	return report, e
}
func (p *Platform) generatePressure(id, key string, body Obj) (Obj, error) {
	if strings.TrimSpace(key) == "" || utf8.RuneCountInString(key) > 200 {
		return nil, api(400, "idempotency_required", "须提供稳定的 Idempotency-Key")
	}
	for k := range body {
		if k != "rebuild" && k != "report_targets" && k != "analysis_options" {
			return nil, api(400, "invalid_report_request", "未知报告参数")
		}
	}
	if v, ok := body["rebuild"]; ok {
		if _, ok = v.(bool); !ok {
			return nil, api(400, "invalid_report_request", "rebuild须为布尔值")
		}
	}
	if _, ok := body["analysis_options"]; ok {
		if _, e := pressureAnalysisOptions(body["analysis_options"]); e != nil {
			return nil, e
		}
	}
	if _, e := p.get("runs", id); e != nil {
		return nil, e
	}
	var result Obj
	e := p.transaction(func(tx *Tx) error {
		if e := p.requireActiveQ(tx); e != nil {
			return e
		}
		run, e := get(tx, "runs", id)
		if e != nil {
			return e
		}
		if e = p.checkProject(run); e != nil {
			return e
		}
		run, e = p.openStoredRun(run)
		if e != nil {
			return e
		}
		if !contains(terminal, str(run["status"])) {
			return api(409, "run_active", "任务结束后才可生成报告")
		}
		var targets reporting.Targets
		origin := "TASK"
		if _, ok := body["report_targets"]; ok {
			targets, e = pressureTargets(body["report_targets"])
			origin = "POST_RUN"
		} else {
			targets, e = pressureTargets(run["report_targets"])
		}
		if e != nil {
			return e
		}
		hash := digest(encoded(Obj{"run": id, "body": body}))
		scopedKey := p.projectID() + "\x00" + digest([]byte(key))
		var prior, reportID string
		e = tx.QueryRow("SELECT digest,object_id FROM idempotency WHERE kind='pressure' AND `key`=?", scopedKey).Scan(&prior, &reportID)
		if e == nil {
			if prior != pressureRequestDigest(hash, p.projectID(), id, reportID) {
				return api(409, "idempotency_conflict", "此幂等键已用于另一报告请求")
			}
			var raw string
			if e = tx.QueryRow("SELECT data FROM pressure_reports WHERE project_id=? AND run_id=? AND id=?", p.projectID(), id, reportID).Scan(&raw); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return api(409, "idempotency_conflict", "报告归属不匹配")
				}
				return e
			}
			result, e = decode([]byte(raw))
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		nonce := ""
		if boolean(body["rebuild"]) || origin == "POST_RUN" {
			nonce = scopedKey
		}
		optionsValue := body["analysis_options"]
		if _, provided := body["analysis_options"]; !provided {
			var priorRaw string
			qe := tx.QueryRow("SELECT data FROM pressure_reports WHERE project_id=? AND run_id=? AND status='READY' ORDER BY version DESC LIMIT 1", p.projectID(), id).Scan(&priorRaw)
			if qe == nil {
				priorReport, de := decode([]byte(priorRaw))
				if de != nil {
					return de
				}
				optionsValue = priorReport["analysis_options"]
			} else if !errors.Is(qe, sql.ErrNoRows) {
				return qe
			}
		}
		options, oe := pressureAnalysisOptions(optionsValue)
		if oe != nil {
			return oe
		}
		result, e = p.enqueuePressureQ(tx, run, targets, origin, nonce, options)
		if e != nil {
			return e
		}
		_, e = tx.Exec("INSERT INTO idempotency(kind,`key`,digest,object_id) VALUES('pressure',?,?,?)", scopedKey, pressureRequestDigest(hash, p.projectID(), id, str(result["id"])), result["id"])
		return e
	})
	return result, e
}

// The first 128 bits identify the request, the remaining 128 bind its stored
// association. A restore can verify ownership without storing the original body.
func pressureRequestDigest(hash, project, run, report string) string {
	prefix := hash[:32]
	return prefix + digest(encoded([]string{prefix, project, run, report}))[:32]
}
func validatePressureImport(tx *Tx) error {
	queries := []string{"SELECT COUNT(*) FROM pressure_reports p LEFT JOIN runs r ON r.id=p.run_id WHERE r.id IS NULL OR r.project_id<>p.project_id OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(p.data,'$.id')),'')<>p.id OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(p.data,'$.project_id')),'')<>p.project_id OR COALESCE(JSON_UNQUOTE(JSON_EXTRACT(p.data,'$.run_id')),'')<>p.run_id", "SELECT COUNT(*) FROM pressure_report_jobs j LEFT JOIN pressure_reports p ON p.id=j.id WHERE p.id IS NULL OR j.project_id<>p.project_id OR j.run_id<>p.run_id"}
	for _, t := range append(append(append([]string{}, pressureTables[2:]...), monitoringTables...), "pressure_dashboard_scopes") {
		queries = append(queries, "SELECT COUNT(*) FROM "+t+" c LEFT JOIN pressure_reports p ON p.id=c.report_id WHERE p.id IS NULL OR p.project_id<>c.project_id")
	}
	for _, q := range queries {
		var n int
		if e := tx.QueryRow(q).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return fmt.Errorf("import contains invalid pressure report associations")
		}
	}
	if e := validateDashboardImport(tx); e != nil {
		return e
	}
	rows, e := tx.Query("SELECT i.`key`,i.digest,i.object_id,COALESCE(r.project_id,''),COALESCE(r.run_id,'') FROM idempotency i LEFT JOIN pressure_reports r ON r.id=i.object_id WHERE i.kind='pressure'")
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var key, hash, id, project, run string
		if e = rows.Scan(&key, &hash, &id, &project, &run); e != nil {
			return e
		}
		parts := strings.Split(key, "\x00")
		if len(parts) != 2 || project == "" || run == "" || parts[0] != project || !objectSHA.MatchString(parts[1]) || !objectSHA.MatchString(hash) || hash != pressureRequestDigest(hash, project, run, id) {
			return fmt.Errorf("invalid pressure idempotency ownership")
		}
	}
	return rows.Err()
}
