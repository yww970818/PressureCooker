package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

func platformSpecs() []managerSpec {
	datasets := scopedSchema(map[string]any{"page": map[string]any{"type": "integer", "minimum": 1}, "pageSize": intSchema(1, 100), "q": strSchema("", 0, 200), "workflowId": projectSchema(), "nodeId": projectSchema()})
	datasets["dependentRequired"] = map[string]any{"nodeId": []string{"workflowId"}}
	return []managerSpec{
		{name: "list_datasets", description: "按项目分页检索数据文件元数据；page/pageSize、字面名称 q、workflowId/nodeId 筛选，nodeId须同时提供workflowId。", schema: datasets, readOnly: true},
		{name: "get_dataset_catalog", description: "读取本项目有效流程及CSV节点目录，仅包含选择器所需元数据。", schema: scopedSchema(map[string]any{}), readOnly: true},
		{name: "download_dataset_template", description: "按本项目流程CSV节点当前列和分隔符生成UTF8 BOM表头模板，不含数据或环境变量值。可传graph未保存快照；返回filename/content，不保存或执行。", schema: scopedSchema(map[string]any{"workflowId": projectSchema(), "nodeId": projectSchema(), "graph": anyObject()}, "workflowId", "nodeId"), readOnly: true},
		{name: "list_api_collection_versions", description: "列出本项目接口集合的历史修订版本，不修改集合或执行接口。", schema: scopedSchema(summaryPageSchema(map[string]any{"collectionId": projectSchema()}), "collectionId"), readOnly: true},
		{name: "get_api_collection_version", description: "读取本项目接口集合指定revision的固定历史内容。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "revision": revisionSchema()}, "collectionId", "revision"), readOnly: true},
		{name: "update_cluster", description: "按当前revision更新全局集群名称、关联workerIds及callbackUrl；初始revision可为0。省略字段保持原值，空workerIds解除关联，空callbackUrl恢复默认。全局配置影响所有项目；管理端拒绝忙碌集群的节点变更。", schema: scopedSchema(map[string]any{"clusterId": strSchema(`^cluster-[a-f0-9]{24}$`, 0, 0), "revision": map[string]any{"type": "integer", "minimum": 0, "maximum": 9007199254740991}, "name": strSchema(`\S`, 1, 100), "workerIds": workerIDsSchema(0), "callbackUrl": strSchema(`^(https?://|$)`, 0, 8192)}, "clusterId", "revision", "name")},
		{name: "get_execution_copy_config", description: "读取本项目已有任务的安全复制配置和可用环境快照选项；只读，不创建或提交任务。", schema: scopedSchema(map[string]any{"executionId": strSchema(`^run-[a-f0-9]{32}$`, 0, 0)}, "executionId"), readOnly: true},
	}
}
func (m *managerTools) platformTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	s := func(key string) string { value, _ := args[key].(string); return value }
	method, path, body := http.MethodGet, "", any(nil)
	switch name {
	case "list_datasets":
		query := url.Values{}
		for from, to := range map[string]string{"page": "page", "pageSize": "page_size", "q": "q", "workflowId": "workflow_id", "nodeId": "node_id"} {
			if value, present := args[from]; present {
				query.Set(to, queryValue(value))
			}
		}
		path = "/api/datasets"
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
	case "get_dataset_catalog":
		path = "/api/datasets/catalog"
	case "download_dataset_template":
		method, path = http.MethodPost, "/api/workflows/"+url.PathEscape(s("workflowId"))+"/datasets/template"
		value := map[string]any{"node_id": s("nodeId")}
		if graph, present := args["graph"]; present {
			value["graph"] = graph
		}
		body = value
	case "list_api_collection_versions", "get_api_collection_version":
		path = "/api/api-collections/" + url.PathEscape(s("collectionId")) + "/versions"
		if name == "get_api_collection_version" {
			path += "/" + url.PathEscape(queryValue(args["revision"]))
		} else {
			path += summaryQuery(args)
		}
	case "update_cluster":
		method, path = http.MethodPut, "/api/clusters/"+url.PathEscape(s("clusterId"))
		value := map[string]any{"revision": args["revision"], "name": s("name")}
		if workers, present := args["workerIds"]; present {
			value["worker_ids"] = workers
		}
		if callback, present := args["callbackUrl"]; present {
			value["callback_url"] = callback
		}
		body = value
	case "get_execution_copy_config":
		path = "/api/runs/" + url.PathEscape(s("executionId")) + "/copy-config"
	default:
		return nil, errors.New("Unknown platform tool")
	}
	return m.api.requestProject(ctx, method, path, body, "", s("projectId"))
}
