package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func pressureReportIDSchema() map[string]any    { return strSchema(`^pressure-[a-f0-9]{32}$`, 0, 0) }
func pressureReportRunIDSchema() map[string]any { return strSchema(`^run-[a-f0-9]{32}$`, 0, 0) }
func pressureReportNullable(schema map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
}
func pressureReportTargetSchema() map[string]any {
	props := map[string]any{}
	for _, key := range []string{"min_rps", "min_success_rate", "max_p95_ms", "max_p99_ms", "min_duration_seconds"} {
		maximum := float64(1e15)
		if key == "min_success_rate" {
			maximum = 100
		}
		props[key] = pressureReportNullable(map[string]any{"type": "number", "minimum": 0, "maximum": maximum})
	}
	return pressureReportNullable(objectSchema(props))
}
func pressureReportAnalysisSchema() map[string]any {
	return pressureReportNullable(objectSchema(map[string]any{
		"apdex_satisfied_ms": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 1e9},
		"apdex_tolerated_ms": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 1e9},
		"granularity_ms":     intSchema(1000, 3600000),
	}))
}
func pressureReportScopeProperties(page bool) map[string]any {
	props := map[string]any{"reportId": pressureReportIDSchema(), "nodeId": strSchema("", 0, 4096), "stepKey": strSchema("", 0, 128)}
	if page {
		props["limit"] = intSchema(1, 100)
		props["offset"] = intSchema(0, 1000000)
	}
	return props
}
func makePressureReportSpecs() []managerSpec {
	steps := pressureReportScopeProperties(true)
	steps["failedOnly"] = map[string]any{"type": "boolean"}
	steps["sort"] = enumSchema("latency", "failure_rate", "failures")
	samples := pressureReportScopeProperties(true)
	samples["stepKey"] = strSchema(`\S`, 1, 128)
	specs := []managerSpec{
		{name: "list_pressure_reports", description: "分页查询本项目已有结构化压测报告，可按executionId、报告状态、验收结论和关键词筛选；只读，不隐式生成报告。", schema: scopedSchema(map[string]any{"executionId": pressureReportRunIDSchema(), "status": enumSchema("QUEUED", "PROCESSING", "RETRY", "FAILED", "READY"), "verdict": enumSchema("NOT_SET", "PASS", "FAIL", "INCOMPLETE"), "search": strSchema("", 0, 200), "limit": intSchema(1, 100), "offset": intSchema(0, 1000000)}), readOnly: true},
		{name: "get_pressure_report", description: "读取本项目指定不可变报告的摘要、指标、质量、验收结论和版本；分析曲线、日志和接口正文分别按范围读取。", schema: scopedSchema(map[string]any{"reportId": pressureReportIDSchema()}, "reportId"), readOnly: true},
		{name: "get_latest_pressure_report", description: "读取本项目executionId最新已有报告版本；尚无报告时返回管理端错误，不自动生成。", schema: scopedSchema(map[string]any{"executionId": pressureReportRunIDSchema()}, "executionId"), readOnly: true},
		{name: "generate_pressure_report", description: "从本项目已结束任务的归档证据创建或重新解析报告，不执行压测。必须复用稳定idempotencyKey；rebuild=true创建重解析版本，reportTargets和analysisOptions内部沿用API蛇形字段，省略设置由管理端继承或使用默认值。", schema: scopedSchema(map[string]any{"executionId": pressureReportRunIDSchema(), "idempotencyKey": strSchema(`^[^\x00-\x1f\x7f]*\S[^\x00-\x1f\x7f]*$`, 1, 200), "rebuild": map[string]any{"type": "boolean"}, "reportTargets": pressureReportTargetSchema(), "analysisOptions": pressureReportAnalysisSchema()}, "executionId", "idempotencyKey")},
		{name: "list_pressure_report_steps", description: "分页读取报告已保存的接口/采样标签统计。nodeId可选，省略仅全局汇总；stepKey可选；支持failedOnly及latency、failure_rate、failures排序，失败率是采样比例。", schema: scopedSchema(steps, "reportId"), readOnly: true},
		{name: "list_pressure_report_nodes", description: "读取报告执行节点指标、预期/缺失/未知状态及日志采集状态；旧版本缺明细时应显式重新解析。", schema: scopedSchema(map[string]any{"reportId": pressureReportIDSchema()}, "reportId"), readOnly: true},
		{name: "list_pressure_report_issues", description: "按可选nodeId/stepKey分页读取报告失败原因与异常证据，不继承接口列表的failedOnly或排序筛选。", schema: scopedSchema(pressureReportScopeProperties(true), "reportId"), readOnly: true},
		{name: "list_pressure_report_logs", description: "按可选nodeId/stepKey分页读取脱敏日志和JTL失败摘要，保留采集状态、截断与来源；没有步骤关联的节点日志不会伪装成接口日志。", schema: scopedSchema(pressureReportScopeProperties(true), "reportId"), readOnly: true},
		{name: "list_pressure_report_samples", description: "显式选择stepKey后按可选nodeId分页读取有界实际请求、响应和断言留样；null是未采集、空串是已采集且为空，assertions_captured=false是未知。历史缺失不能补造，采样成功不等于断言通过。", schema: scopedSchema(samples, "reportId", "stepKey"), readOnly: true},
		{name: "get_pressure_report_analytics", description: "读取报告已保存的全局/节点/步骤性能分析、并发观测、APDEX、分布和完整错误统计；nodeId/stepKey选择范围，不重新计算或触发压测。", schema: scopedSchema(pressureReportScopeProperties(false), "reportId"), readOnly: true},
		{name: "compare_pressure_reports", description: "对比本项目两个READY报告的摘要指标与条件差异；统计口径不完整或不可比时保留警告，不虚构差值。", schema: scopedSchema(map[string]any{"leftReportId": pressureReportIDSchema(), "rightReportId": pressureReportIDSchema()}, "leftReportId", "rightReportId"), readOnly: true},
		{name: "export_pressure_report", description: "确认本项目报告READY后返回认证离线HTML下载元数据与路径；必须携带项目上下文并在下载时认证，工具不读取HTML正文，不返回管理令牌。", schema: scopedSchema(map[string]any{"reportId": pressureReportIDSchema()}, "reportId"), readOnly: true},
	}
	for i := range specs {
		specs[i].resolved = resolveSchema(specs[i].schema)
	}
	return specs
}

var pressureReportDefinitions = makePressureReportSpecs()

func pressureReportSpecs() []managerSpec {
	return append([]managerSpec(nil), pressureReportDefinitions...)
}

func pressureReportValidate(name string, args map[string]any) error {
	for _, spec := range pressureReportDefinitions {
		if spec.name != name {
			continue
		}
		raw, e := json.Marshal(args)
		var normalized map[string]any
		if e != nil || json.Unmarshal(raw, &normalized) != nil || spec.resolved.Validate(normalized) != nil {
			return fmt.Errorf("Invalid arguments for %s", name)
		}
		// JSON Schema maxLength counts Unicode characters; these two manager
		// filters are bounded in UTF-8 bytes, so enforce the API boundary too.
		for key, maximum := range map[string]int{"nodeId": 4096, "stepKey": 128} {
			if value, ok := normalized[key].(string); ok && len(value) > maximum {
				return fmt.Errorf("Invalid arguments for %s", name)
			}
		}
		// The API defaults a supplied partial options object before checking the
		// relationship; reject this relationship locally too, before any HTTP call.
		if name == "generate_pressure_report" {
			if options, ok := normalized["analysisOptions"].(map[string]any); ok {
				satisfied, tolerated := float64(500), float64(1500)
				if value, ok := options["apdex_satisfied_ms"].(float64); ok {
					satisfied = value
				}
				if value, ok := options["apdex_tolerated_ms"].(float64); ok {
					tolerated = value
				}
				if satisfied > tolerated {
					return fmt.Errorf("Invalid arguments for %s", name)
				}
			}
		}
		return nil
	}
	return errors.New("Unknown pressure report tool")
}

func (m *managerTools) pressureReportTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	if e := pressureReportValidate(name, args); e != nil {
		return nil, e
	}
	text := func(key string) string { s, _ := args[key].(string); return s }
	project := text("projectId")
	reportPath := "/api/pressure-reports/" + text("reportId")
	method, path, body, key := http.MethodGet, reportPath, any(nil), ""
	aliases := map[string]string{}
	switch name {
	case "list_pressure_reports":
		path = "/api/pressure-reports"
		aliases = map[string]string{"executionId": "run_id", "status": "status", "verdict": "verdict", "search": "search", "limit": "limit", "offset": "offset"}
	case "get_pressure_report":
	case "get_latest_pressure_report":
		path = "/api/runs/" + text("executionId") + "/pressure-report"
	case "generate_pressure_report":
		method = http.MethodPost
		path = "/api/runs/" + text("executionId") + "/pressure-report"
		key = strings.TrimSpace(text("idempotencyKey"))
		payload := map[string]any{}
		for from, to := range map[string]string{"rebuild": "rebuild", "reportTargets": "report_targets", "analysisOptions": "analysis_options"} {
			if value, present := args[from]; present {
				payload[to] = value
			}
		}
		body = payload
	case "list_pressure_report_steps", "list_pressure_report_issues", "list_pressure_report_logs", "list_pressure_report_samples":
		suffix := map[string]string{"list_pressure_report_steps": "steps", "list_pressure_report_issues": "issues", "list_pressure_report_logs": "logs", "list_pressure_report_samples": "samples"}[name]
		path += "/" + suffix
		aliases = map[string]string{"nodeId": "node_id", "stepKey": "step_key", "limit": "limit", "offset": "offset"}
		if name == "list_pressure_report_steps" {
			aliases["failedOnly"] = "failed_only"
			aliases["sort"] = "sort"
		}
	case "list_pressure_report_nodes":
		path += "/nodes"
	case "get_pressure_report_analytics":
		path += "/analytics"
		aliases = map[string]string{"nodeId": "node_id", "stepKey": "step_key"}
	case "compare_pressure_reports":
		path = "/api/pressure-reports/compare"
		aliases = map[string]string{"leftReportId": "left", "rightReportId": "right"}
	case "export_pressure_report":
		report, e := m.api.requestProject(ctx, http.MethodGet, reportPath, nil, "", project)
		if e != nil {
			return nil, e
		}
		if report["status"] != "READY" {
			return nil, errors.New("Report must be READY for export")
		}
		route := reportPath + "/export.html"
		download, e := m.api.scopedDownloadURL(route, project)
		if e != nil {
			return nil, e
		}
		return map[string]any{"report_id": text("reportId"), "project_id": project, "version": report["version"], "status": report["status"], "download_path": route, "download_url": download, "method": http.MethodGet, "filename": "pressure-report.html", "content_type": "text/html; charset=utf-8", "requires_authentication": true, "request_headers": map[string]any{"X-Project-ID": project}}, nil
	}
	query := url.Values{}
	for from, to := range aliases {
		if value, present := args[from]; present {
			query.Set(to, queryValue(value))
		}
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return m.api.requestProject(ctx, method, path, body, key, project)
}
