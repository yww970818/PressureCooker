package main

import (
	"encoding/json"
	"math"
	"net/http"

	"pressurecooker/manager/reporting"
)

func pressureAnalysisOptions(value any) (reporting.DashboardOptions, error) {
	options := reporting.DefaultDashboardOptions()
	if value == nil {
		return options, nil
	}
	m, ok := value.(map[string]any)
	bad := func() (reporting.DashboardOptions, error) {
		return options, api(400, "invalid_analysis_options", "分析设置须为正数；APDEX满意阈值不得超过容忍阈值，时间粒度须为1000至3600000毫秒的整数")
	}
	if !ok {
		return bad()
	}
	for key, v := range m {
		switch v.(type) {
		case float64, int, int64, json.Number:
		default:
			return bad()
		}
		n := number(v)
		if math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			return bad()
		}
		switch key {
		case "apdex_satisfied_ms":
			options.ApdexSatisfiedMS = n
		case "apdex_tolerated_ms":
			options.ApdexToleratedMS = n
		case "granularity_ms":
			if n != math.Trunc(n) || n > 3600000 {
				return bad()
			}
			options.GranularityMS = int64(n)
		default:
			return bad()
		}
	}
	result, err := reporting.NormalizeDashboardOptions(options)
	if err != nil {
		return bad()
	}
	return result, nil
}

// A separate endpoint avoids shipping every scoped time series with the report's
// five-second status poll. Filters select persisted aggregates, not new estimates.
func (p *Platform) pressureAnalytics(id string, r *http.Request) (Obj, error) {
	report, e := p.pressureSelect(id, "summary_data")
	if e != nil {
		return nil, e
	}
	node, step := r.URL.Query().Get("node_id"), r.URL.Query().Get("step_key")
	if len(node) > 4096 || len(step) > 128 {
		return nil, api(400, "invalid_scope", "节点或步骤筛选无效")
	}
	dashboard := obj(report["dashboard"])
	if dashboard["external_scopes"] == true || dashboard["scopes_projected"] == true {
		return p.normalizedAnalytics(report, r)
	}
	// Legacy reports retain their original embedded scopes until rebuilt.
	report, e = p.pressureSelect(id, "JSON_REMOVE(data,'$.request_samples','$.node_logs')")
	if e != nil {
		return nil, e
	}
	dashboard = obj(report["dashboard"])
	steps, series := []any{}, []any{}
	seen := map[string]bool{}
	var scope any
	for _, v := range arr(dashboard["scopes"]) {
		row := obj(v)
		rid, key := str(row["node_id"]), str(row["step_key"])
		if rid == "" && key != "" && !seen[key] {
			steps = append(steps, Obj{"step_key": key, "label": row["label"], "sample_kind": row["sample_kind"]})
			seen[key] = true
		}
		if rid != node {
			continue
		}
		if key == step {
			scope = row
		}
		if key != "" && (step == "" || key == step) {
			series = append(series, row)
		}
	}
	version := number(dashboard["version"])
	capabilities := obj(dashboard["capabilities"])
	warnings := arr(dashboard["warnings"])
	if warnings == nil {
		warnings = []any{}
	}
	for _, w := range arr(obj(report["quality"])["warnings"]) {
		warnings = append(warnings, w)
	}
	nodes := arr(report["nodes"])
	if nodes == nil {
		nodes = []any{}
	}
	return Obj{"detail_version": version, "options": dashboard["options"], "capabilities": capabilities, "warnings": warnings, "nodes": nodes, "steps": steps, "scope": scope, "series": series}, nil
}

func pressurePublicReport(report Obj) Obj {
	if report == nil {
		return nil
	}
	out := Obj{}
	for key, value := range report {
		if key != "node_logs" && key != "dashboard" && key != "request_samples" {
			out[key] = value
		}
	}
	return out
}
