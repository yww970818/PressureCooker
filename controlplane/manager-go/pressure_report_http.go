package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

func (p *Platform) pressureGet(id string) (Obj, error) {
	// Internal report workers may persist this object again. Dynamic monitoring
	// projections belong only to public reads, never to the fixed report body.
	return p.pressureSelect(id, "data")
}
func (p *Platform) pressureRead(id string, summary bool) (Obj, error) {
	column := "data"
	if summary {
		column = "summary_data"
	}
	report, e := p.pressureSelect(id, column)
	if e != nil {
		return nil, e
	}
	var previous string
	e = p.DB.QueryRow("SELECT id FROM pressure_reports WHERE project_id=? AND run_id=? AND status='READY' AND version<? ORDER BY version DESC LIMIT 1", p.projectID(), report["run_id"], report["version"]).Scan(&previous)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	report["previous_ready_id"] = previous
	monitoring, e := p.pressureMonitoringSummary(id)
	if e != nil {
		return nil, e
	}
	report["monitoring"] = monitoring
	pressureMonitoringExtensions(report, monitoring)
	return report, nil
}
func (p *Platform) pressureSelect(id, column string) (Obj, error) {
	if e := validID(id); e != nil {
		return nil, e
	}
	var raw string
	e := p.DB.QueryRow("SELECT "+column+" FROM pressure_reports WHERE id=? AND project_id=?", id, p.projectID()).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "pressure_report_missing", "报告不存在或尚未生成")
	}
	if e != nil {
		return nil, e
	}
	return decode([]byte(raw))
}
func pressureIndex(m Obj) Obj {
	out := Obj{}
	for _, k := range []string{"id", "project_id", "run_id", "version", "status", "created_at", "finished_at", "run_name", "run_status", "environment_name", "plan_name", "verdict_status", "targets_origin", "previous_ready_id", "error"} {
		out[k] = m[k]
	}
	return out
}
func (p *Platform) pressureLatest(runID string, summary ...bool) (Obj, error) {
	if _, e := p.get("runs", runID); e != nil {
		return nil, e
	}
	var id string
	e := p.DB.QueryRow("SELECT id FROM pressure_reports WHERE project_id=? AND run_id=? ORDER BY version DESC LIMIT 1", p.projectID(), runID).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, api(404, "pressure_report_missing", "报告尚未生成，可从原始文件生成")
	}
	if e != nil {
		return nil, e
	}
	return p.pressureRead(id, len(summary) > 0 && summary[0])
}
func pressurePage(r *http.Request) (int, int, error) {
	limit, e := parseInt(r.URL.Query().Get("limit"), 20)
	if e != nil || limit < 1 || limit > 100 {
		return 0, 0, api(400, "invalid_pagination", "每页条数须为1至100")
	}
	offset, e := parseInt(r.URL.Query().Get("offset"), 0)
	if e != nil || offset < 0 || offset > 1000000 {
		return 0, 0, api(400, "invalid_pagination", "分页偏移无效")
	}
	return limit, offset, nil
}
func (p *Platform) pressureList(r *http.Request) (Obj, error) {
	limit, offset, e := pressurePage(r)
	if e != nil {
		return nil, e
	}
	q := r.URL.Query()
	where := "project_id=?"
	args := []any{p.projectID()}
	for _, item := range [][2]string{{"run_id", "run_id"}, {"status", "status"}, {"verdict", "verdict_status"}} {
		if v := q.Get(item[0]); v != "" {
			where += " AND " + item[1] + "=?"
			args = append(args, v)
		}
	}
	if search := q.Get("search"); search != "" {
		where += " AND (run_name LIKE ? OR run_id=?)"
		args = append(args, "%"+search+"%", search)
	}
	var total int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM pressure_reports WHERE "+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	rows, e := p.DB.Query("SELECT summary_data,COALESCE((SELECT prior.id FROM pressure_reports prior WHERE prior.project_id=current_report.project_id AND prior.run_id=current_report.run_id AND prior.version<current_report.version AND prior.status='READY' ORDER BY prior.version DESC LIMIT 1),'') FROM pressure_reports current_report WHERE "+where+" ORDER BY created_at DESC,version DESC,id DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var raw, previous string
		if e = rows.Scan(&raw, &previous); e != nil {
			return nil, e
		}
		m, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		m["previous_ready_id"] = previous
		items = append(items, pressureIndex(m))
	}
	return Obj{"items": items, "total": total, "limit": limit, "offset": offset}, rows.Err()
}
func (p *Platform) pressureChildren(id, kind string, r *http.Request) (Obj, error) {
	if _, e := p.pressureRead(id, true); e != nil {
		return nil, e
	}
	limit, offset, e := pressurePage(r)
	if e != nil {
		return nil, e
	}
	table := "pressure_report_" + kind
	if kind != "steps" && kind != "issues" {
		return nil, api(400, "invalid_kind", "无效报告明细")
	}
	where := "report_id=? AND project_id=?"
	args := []any{id, p.projectID()}
	node, step := r.URL.Query().Get("node_id"), r.URL.Query().Get("step_key")
	if len(node) > 4096 || len(step) > 128 {
		return nil, api(400, "invalid_scope", "节点或步骤筛选无效")
	}
	if node != "" {
		where += " AND node_hash=? AND JSON_UNQUOTE(JSON_EXTRACT(data,'$.node_id'))=?"
		args = append(args, digest([]byte(node)))
		args = append(args, node)
	} else if kind == "steps" {
		where += " AND node_hash=?"
		args = append(args, digest(nil))
	}
	if step != "" {
		where += " AND step_key=?"
		args = append(args, step)
	}
	if kind == "steps" {
		failedOnly := r.URL.Query().Get("failed_only")
		if failedOnly != "" && failedOnly != "true" && failedOnly != "false" {
			return nil, api(400, "invalid_filter", "只看失败筛选须为true或false")
		}
		if failedOnly == "true" {
			where += " AND failure_count>0"
		}
	}
	var total int
	if e = p.DB.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	order := "position"
	if r.URL.Query().Get("sort") == "failures" {
		order = "count_value DESC,position"
	}
	if r.URL.Query().Get("sort") == "latency" {
		order = "number_value DESC,position"
	}
	if kind == "steps" && r.URL.Query().Get("sort") == "failure_rate" {
		// Cast before division so close rates do not tie after integer-division
		// precision. Unknown zero-sample rates sort last; position is stable.
		order = "(CAST(failure_count AS DECIMAL(30,12))/NULLIF(sample_count,0)) DESC,number_value DESC,position"
	}
	rows, e := p.DB.Query("SELECT data FROM "+table+" WHERE "+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		m, e := decode([]byte(raw))
		if e != nil {
			return nil, e
		}
		if kind == "steps" {
			m["failure_rate"] = nil
			if n := number(m["sample_count"]); n > 0 && m["failure_count"] != nil && number(m["failure_count"]) >= 0 && number(m["failure_count"]) <= n {
				m["failure_rate"] = number(m["failure_count"]) * 100 / n
			}
		}
		items = append(items, m)
	}
	return Obj{"items": items, "total": total, "limit": limit, "offset": offset}, rows.Err()
}
func (p *Platform) pressureCompare(left, right string) (Obj, error) {
	l, e := p.pressureRead(left, true)
	if e != nil {
		return nil, e
	}
	r, e := p.pressureRead(right, true)
	if e != nil {
		return nil, e
	}
	if l["status"] != "READY" || r["status"] != "READY" {
		return nil, api(409, "report_not_ready", "只能对比已生成的报告")
	}
	differences, warnings := []any{}, []any{}
	for _, k := range []string{"plan_sha256", "environment_id", "cluster_id", "workers", "timeout_seconds", "environment_revision", "load"} {
		lv, rv := obj(l["run"])[k], obj(r["run"])[k]
		if string(encoded(lv)) != string(encoded(rv)) {
			warnings = append(warnings, Obj{"key": k, "left": lv, "right": rv, "message": "执行条件不同，对比仅供参考"})
		}
	}
	if string(encoded(l["analysis_options"])) != string(encoded(r["analysis_options"])) {
		warnings = append(warnings, Obj{"key": "analysis_options", "left": l["analysis_options"], "right": r["analysis_options"], "message": "分析阈值或时间粒度不同，图表和APDEX不可直接比较"})
	}
	ls, rs := str(obj(l["metrics"])["sample_scope"]), str(obj(r["metrics"])["sample_scope"])
	lq, rq := str(obj(l["quality"])["metric_status"]), str(obj(r["quality"])["metric_status"])
	ln, rn := str(obj(l["quality"])["node_coverage"]), str(obj(r["quality"])["node_coverage"])
	comparable := ls == "ordinary_samples" && ls == rs && boolean(obj(l["quality"])["scope_confirmed"]) && boolean(obj(r["quality"])["scope_confirmed"]) && lq == "COMPLETE" && rq == "COMPLETE" && ln == "COMPLETE" && rn == "COMPLETE"
	if !comparable {
		warnings = append(warnings, Obj{"key": "sample_scope", "left": ls, "right": rs, "message": "统计口径或数据覆盖不可比，不计算指标差值"}, Obj{"key": "metric_status", "left": lq, "right": rq, "message": "指标完整度"}, Obj{"key": "node_coverage", "left": ln, "right": rn, "message": "执行节点覆盖"})
	}
	for _, k := range []string{"sample_count", "failure_count", "success_rate", "rps", "p95_ms", "p99_ms", "duration_seconds"} {
		lv, rv := obj(l["metrics"])[k], obj(r["metrics"])[k]
		var delta any
		if comparable && lv != nil && rv != nil {
			delta = number(rv) - number(lv)
		}
		differences = append(differences, Obj{"key": k, "left": lv, "right": rv, "delta": delta})
	}
	if string(encoded(l["targets"])) != string(encoded(r["targets"])) {
		warnings = append(warnings, Obj{"key": "targets", "left": l["targets"], "right": r["targets"], "message": "两次报告的验收目标不同"})
	}
	return Obj{"left": l, "right": r, "differences": differences, "warnings": warnings, "comparison_basis": Obj{"comparable": comparable, "left": Obj{"sample_scope": ls, "metric_status": lq, "node_coverage": ln}, "right": Obj{"sample_scope": rs, "metric_status": rq, "node_coverage": rn}}}, nil
}
func (h Handler) pressureRoute(w http.ResponseWriter, r *http.Request, method string, body Obj) (bool, error) {
	path := r.URL.Path
	parts := strings.Split(strings.Trim(path, "/"), "/")
	send := func(v any, e error) (bool, error) {
		if e == nil {
			sendJSON(w, r, 200, v)
		}
		return true, e
	}
	if path == "/api/pressure-reports" && method == "GET" {
		v, e := h.P.pressureList(r)
		return send(v, e)
	}
	if path == "/api/pressure-reports/compare" && method == "GET" {
		v, e := h.P.pressureCompare(r.URL.Query().Get("left"), r.URL.Query().Get("right"))
		if e == nil {
			v["left"] = pressurePublicReport(obj(v["left"]))
			v["right"] = pressurePublicReport(obj(v["right"]))
		}
		return send(v, e)
	}
	if len(parts) >= 3 && parts[1] == "pressure-reports" {
		if len(parts) == 5 && parts[3] == "monitoring" && parts[4] == "jvm-batch" {
			if method != "POST" {
				return true, api(405, "method_not_allowed", "JVM批量监控接口仅支持POST")
			}
			v, e := h.P.writePressureMonitoringJVMBatch(parts[2], r.Header.Get("Idempotency-Key"), body)
			return send(v, e)
		}
		if len(parts) == 4 && parts[3] == "monitoring" {
			if method == "GET" {
				values, e := url.ParseQuery(r.URL.RawQuery)
				if e != nil {
					return true, monitoringInvalid()
				}
				v, e := h.P.pressureMonitoringPage(parts[2], values)
				return send(v, e)
			}
			if method == "POST" {
				v, e := h.P.writePressureMonitoring(parts[2], r.Header.Get("Idempotency-Key"), body)
				return send(v, e)
			}
			return true, api(405, "method_not_allowed", "监控补充接口仅支持GET和POST")
		}
		if method != "GET" {
			return true, api(405, "method_not_allowed", "只读报告")
		}
		if len(parts) == 3 {
			v, e := h.P.pressureRead(parts[2], true)
			return send(pressurePublicReport(v), e)
		}
		if len(parts) == 4 && parts[3] == "analytics" {
			v, e := h.P.pressureAnalytics(parts[2], r)
			return send(v, e)
		}
		if len(parts) == 4 && parts[3] == "samples" {
			v, e := h.P.pressureRequestSamples(parts[2], r)
			return send(v, e)
		}
		if len(parts) == 4 && parts[3] == "export.html" {
			data, e := h.P.pressureExport(parts[2])
			if e != nil {
				return true, e
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="pressure-report.html"`)
			w.WriteHeader(http.StatusOK)
			_, e = w.Write(data)
			return true, e
		}
		if len(parts) == 4 && (parts[3] == "nodes" || parts[3] == "logs") {
			v, e := h.P.pressureNodeChildren(parts[2], parts[3], r)
			return send(v, e)
		}
		if len(parts) == 4 && (parts[3] == "steps" || parts[3] == "issues") {
			v, e := h.P.pressureChildren(parts[2], parts[3], r)
			return send(v, e)
		}
	}
	if len(parts) == 4 && parts[1] == "runs" && parts[3] == "pressure-report" {
		if method == "GET" {
			v, e := h.P.pressureLatest(parts[2], true)
			return send(pressurePublicReport(v), e)
		}
		if method == "POST" {
			v, e := h.P.generatePressure(parts[2], r.Header.Get("Idempotency-Key"), body)
			if e == nil {
				v = pressureIndex(v)
			}
			return send(v, e)
		}
	}
	if strings.HasPrefix(path, "/api/pressure-reports") {
		return true, api(404, "not_found", "报告接口不存在")
	}
	return false, nil
}
