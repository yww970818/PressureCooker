package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

func historyDeleteSpecs() []managerSpec {
	ids := func() map[string]any {
		return map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "uniqueItems": true, "items": projectSchema()}
	}
	confirm := func() map[string]any { return map[string]any{"type": "boolean", "const": true} }
	return []managerSpec{
		{name: "delete_execution", description: "删除本项目单条已结束任务，连同关联报告、执行详情和任务产物；未结束任务拒绝删除，归档项目只读。", schema: scopedSchema(map[string]any{"executionId": projectSchema()}, "executionId")},
		{name: "delete_executions", description: "原子批量删除本项目1至100条不同的已结束任务及关联报告/产物；任一记录不存在、属于其他项目或未结束时全部拒绝。归档项目只读。", schema: scopedSchema(map[string]any{"executionIds": ids()}, "executionIds")},
		{name: "clear_executions", description: "显式confirm=true后清空当前项目所有已结束任务及关联报告/产物，忽略列表筛选和分页；未结束任务保留并计入skipped_count。归档项目只读。", schema: scopedSchema(map[string]any{"confirm": confirm()}, "confirm")},
		{name: "delete_pressure_report", description: "删除本项目一个压测报告版本及其分析/监控/明细，撤销解析作业；保留任务、原始JTL/日志和其他报告。归档项目只读。", schema: scopedSchema(map[string]any{"reportId": projectSchema()}, "reportId")},
		{name: "delete_pressure_reports", description: "原子批量删除本项目1至100个不同报告版本及其分析/监控/明细，保留任务和原始文件；不存在或跨项目记录导致全批拒绝。归档项目只读。", schema: scopedSchema(map[string]any{"reportIds": ids()}, "reportIds")},
		{name: "clear_pressure_reports", description: "显式confirm=true后清空当前项目全部压测报告及其分析/监控/明细，忽略筛选和分页；保留任务、原始JTL/日志和其他项目数据。归档项目只读。", schema: scopedSchema(map[string]any{"confirm": confirm()}, "confirm")},
	}
}

func (m *managerTools) historyDeleteTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	project, _ := args["projectId"].(string)
	method, path, body := http.MethodPost, "", any(nil)
	switch name {
	case "delete_execution":
		id, _ := args["executionId"].(string)
		method, path = http.MethodDelete, "/api/runs/"+url.PathEscape(id)
	case "delete_executions":
		path, body = "/api/runs/delete", map[string]any{"ids": args["executionIds"]}
	case "clear_executions":
		path, body = "/api/runs/clear", map[string]any{"confirm": args["confirm"]}
	case "delete_pressure_report":
		id, _ := args["reportId"].(string)
		method, path = http.MethodDelete, "/api/pressure-reports/"+url.PathEscape(id)
	case "delete_pressure_reports":
		path, body = "/api/pressure-reports/delete", map[string]any{"ids": args["reportIds"]}
	case "clear_pressure_reports":
		path, body = "/api/pressure-reports/clear", map[string]any{"confirm": args["confirm"]}
	default:
		return nil, errors.New("Unknown history deletion tool")
	}
	return m.api.requestProject(ctx, method, path, body, "", project)
}
