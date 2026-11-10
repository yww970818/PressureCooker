package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

func workflowSpecs() []managerSpec {
	metadata := func() map[string]any {
		return map[string]any{"name": strSchema(`\S`, 1, 200), "description": strSchema("", 0, 10000), "graph": anyObject()}
	}
	update := metadata()
	update["workflowId"] = projectSchema()
	update["revision"] = revisionSchema()
	return []managerSpec{
		{name: "list_workflows", description: "分页列出本项目流程摘要及修订号；完整内容使用get_workflow读取。", schema: scopedSchema(summaryPageSchema(nil)), readOnly: true},
		{name: "create_workflow", description: "保存本项目流程 graph 原对象；默认容量5000节点、10000连线；流程请求体默认32MiB，可配置至64MiB，不启动压测。", schema: scopedSchema(metadata(), "name", "graph")},
		{name: "get_workflow", description: "读取本项目流程当前 graph、名称、描述与 revision。", schema: scopedSchema(map[string]any{"workflowId": projectSchema()}, "workflowId"), readOnly: true},
		{name: "update_workflow", description: "携带当前 revision 保存完整流程 graph 原对象；冲突时先重新读取，不覆盖新版本。流程请求体默认32MiB，可配置至64MiB。", schema: scopedSchema(update, "workflowId", "revision", "name", "graph")},
		{name: "delete_workflow", description: "软删除本项目流程，保留既有历史和发布关系，不停止或执行任务。", schema: scopedSchema(map[string]any{"workflowId": projectSchema()}, "workflowId")},
		{name: "import_workflow", description: "从完整 JMX content 新建本项目流程，传入 name 和可选 description；流程请求体默认32MiB，可配置至64MiB，不执行请求。", schema: scopedSchema(map[string]any{"name": strSchema(`\S`, 1, 200), "description": strSchema("", 0, 10000), "content": strSchema("", 1, 64*1024*1024)}, "name", "content")},
		{name: "preview_workflow", description: "只编译提供的 graph 并返回 XML 原文，不保存、不发布、不执行；流程请求体默认32MiB，可配置至64MiB。", schema: scopedSchema(map[string]any{"graph": anyObject()}, "graph"), readOnly: true},
		{name: "publish_workflow", description: "按当前 revision 将已保存流程发布为不可变 JMX，返回发布关系；不会提交或执行压测。", schema: scopedSchema(map[string]any{"workflowId": projectSchema(), "revision": revisionSchema()}, "workflowId", "revision")},
		{name: "list_workflow_versions", description: "列出本项目流程已保存的历史修订版本，不改变当前草稿。", schema: scopedSchema(summaryPageSchema(map[string]any{"workflowId": projectSchema()}), "workflowId"), readOnly: true},
		{name: "get_workflow_version", description: "读取本项目流程指定 revision 的固定历史内容，不覆盖当前草稿。", schema: scopedSchema(map[string]any{"workflowId": projectSchema(), "revision": revisionSchema()}, "workflowId", "revision"), readOnly: true},
		{name: "list_workflow_plans", description: "列出本项目流程各修订号关联的不可变发布脚本；download_script(workflowId)用于当前流程导出。", schema: scopedSchema(map[string]any{"workflowId": projectSchema()}, "workflowId"), readOnly: true},
	}
}
func (m *managerTools) workflowTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	s := func(key string) string { value, _ := args[key].(string); return value }
	method, path, body := http.MethodGet, "/api/workflows", any(nil)
	workflow := path + "/" + url.PathEscape(s("workflowId"))
	payload := func(keys ...string) map[string]any {
		out := map[string]any{}
		for _, key := range keys {
			if value, present := args[key]; present {
				out[key] = value
			}
		}
		return out
	}
	switch name {
	case "list_workflows":
		path += summaryQuery(args)
	case "create_workflow":
		method, body = http.MethodPost, payload("name", "description", "graph")
	case "get_workflow":
		path = workflow
	case "update_workflow":
		method, path, body = http.MethodPut, workflow, payload("revision", "name", "description", "graph")
	case "delete_workflow":
		method, path = http.MethodDelete, workflow
	case "import_workflow":
		method, path, body = http.MethodPost, path+"/import", payload("name", "description", "content")
	case "preview_workflow":
		raw, err := m.api.requestRaw(ctx, http.MethodPost, path+"/preview", payload("graph"), "", s("projectId"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"xml": string(raw), "project_id": s("projectId")}, nil
	case "publish_workflow":
		method, path, body = http.MethodPost, workflow+"/publish", payload("revision")
	case "list_workflow_versions":
		path = workflow + "/versions" + summaryQuery(args)
	case "get_workflow_version":
		path = workflow + "/versions/" + url.PathEscape(queryValue(args["revision"]))
	case "list_workflow_plans":
		path = workflow + "/plans"
	default:
		return nil, errors.New("Unknown workflow tool")
	}
	return m.api.requestProject(ctx, method, path, body, "", s("projectId"))
}
