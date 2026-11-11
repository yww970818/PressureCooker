package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"pressurecooker/execution/protocol"
)

type upstreamCaller interface {
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
}
type managerTools struct {
	api      managerClient
	upstream upstreamCaller
	drafts   *draftProjects
}
type managerSpec struct {
	name, description string
	schema            map[string]any
	readOnly          bool
	resolved          *jsonschema.Resolved
}

func strSchema(pattern string, min, max int) map[string]any {
	s := map[string]any{"type": "string"}
	if pattern != "" {
		s["pattern"] = pattern
	}
	if min > 0 {
		s["minLength"] = min
	}
	if max > 0 {
		s["maxLength"] = max
	}
	return s
}
func objectSchema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func intSchema(min, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": max}
}
func projectSchema() map[string]any { return strSchema(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,100}$`, 1, 101) }
func workerIDsSchema(min int) map[string]any {
	return map[string]any{"type": "array", "minItems": min, "maxItems": protocol.MaxNodeReportWorkers, "uniqueItems": true, "items": strSchema(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`, 1, 128)}
}
func scopedSchema(properties map[string]any, required ...string) map[string]any {
	properties["projectId"] = projectSchema()
	return objectSchema(properties, append([]string{"projectId"}, required...)...)
}
func anyObject() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}
func revisionSchema() map[string]any { return map[string]any{"type": "integer", "minimum": 1} }
func enumSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func collectionProperties() map[string]any {
	return map[string]any{"name": strSchema(`\S`, 1, 200), "description": strSchema("", 0, 10000), "document": anyObject(), "client_state": anyObject()}
}
func requestProperties() map[string]any {
	return map[string]any{"collectionId": projectSchema(), "revision": revisionSchema(), "path": strSchema(`^/`, 1, 10000), "method": enumSchema("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"), "operation": anyObject(), "previous_path": strSchema(`^/`, 1, 10000), "previous_method": enumSchema("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE")}
}
func environmentIDSchema() map[string]any { return strSchema(`^environment-[a-f0-9]{32}$`, 0, 0) }
func environmentProperties() map[string]any {
	name := strSchema("", 1, 200)
	name["not"] = enumSchema("base_host")
	value := strSchema("", 0, 4096)
	value["description"] = "固定文本，或完整随机规则 {{$random.uuid}}、{{$random.hex(N)}}、{{$random.alnum(N)}}、{{$random.digits(N)}}（N为1至256且无前导零），或自动规则 {{$auto.client_ip}}、{{$auto.timestamp_ms}}。变量名任意；client_ip取本次调用的实际HTTP来源IP，不信任XFF，来源不可用时报错；timestamp_ms为当前毫秒时间戳字符串。规则原文保存，合并后每次连接/调试/任务快照解析一次，同次操作共享、任务重试复用冻结快照；禁止嵌入文本、脚本和其他规则语法，逐线程身份使用数据集。"
	return map[string]any{
		"name":      strSchema(`\S`, 1, 200),
		"base_host": strSchema(`^https?://`, 1, 0),
		"variables": map[string]any{"type": "array", "items": objectSchema(map[string]any{"name": name, "value": value}, "name", "value")},
	}
}

var managerSpecs = makeManagerSpecs()

func makeManagerSpecs() []managerSpec {
	runID := strSchema(`^run-[a-f0-9]{32}$`, 0, 0)
	planID := strSchema(`^plan-[a-f0-9]{32}$`, 0, 0)
	environmentUpdate := environmentProperties()
	environmentUpdate["environmentId"] = environmentIDSchema()
	environmentUpdate["revision"] = revisionSchema()
	specs := []managerSpec{
		{name: "list_projects", description: "列出全部项目与归档状态。", schema: objectSchema(map[string]any{}), readOnly: true},
		{name: "create_project", description: "创建项目，返回 projectId 对应的项目标识。", schema: objectSchema(map[string]any{"name": strSchema(`\S`, 1, 200), "description": strSchema("", 0, 10000)}, "name")},
		{name: "get_project", description: "读取指定项目的设置与归档状态。", schema: scopedSchema(map[string]any{}), readOnly: true},
		{name: "update_project", description: "按当前版本修改项目名称和描述。", schema: scopedSchema(map[string]any{"revision": revisionSchema(), "name": strSchema(`\S`, 1, 200), "description": strSchema("", 0, 10000)}, "revision", "name")},
		{name: "archive_project", description: "归档项目并保留历史；归档后不能提交新任务。", schema: scopedSchema(map[string]any{"revision": revisionSchema()}, "revision")},
		{name: "list_project_environments", description: "列出本项目环境的基础地址、自定义变量和版本。", schema: scopedSchema(map[string]any{}), readOnly: true},
		{name: "create_project_environment", description: "创建本项目环境，提供完整 HTTP(S) base_host 和可选自定义变量列表；变量不得包含 base_host。", schema: scopedSchema(environmentProperties(), "name", "base_host")},
		{name: "get_project_environment", description: "读取本项目指定环境的基础地址、自定义变量及当前版本。", schema: scopedSchema(map[string]any{"environmentId": environmentIDSchema()}, "environmentId"), readOnly: true},
		{name: "update_project_environment", description: "按当前 revision 更新本项目环境；版本冲突时先重新读取，已提交任务的环境快照保持不变。", schema: scopedSchema(environmentUpdate, "environmentId", "revision", "name", "base_host")},
		{name: "delete_project_environment", description: "软删除本项目环境，禁止用于新任务并保留既有任务快照。", schema: scopedSchema(map[string]any{"environmentId": environmentIDSchema()}, "environmentId")},
		{name: "publish_test_plan", description: "把本项目草稿发布为不可变 JMX；使用 submit_console_task 提交控制台任务。", schema: scopedSchema(map[string]any{"planId": strSchema(`^plan_[a-f0-9]{8}$`, 0, 0)}, "planId")},
		{name: "upload_script", description: "上传名称和完整 JMX 文本，保存为本项目不可变脚本版本；不会启动任务。", schema: scopedSchema(map[string]any{"name": strSchema(`\S`, 1, 200), "content": strSchema("", 1, 5*1024*1024), "description": strSchema("", 0, 10000)}, "name", "content")},
		{name: "download_script", description: "下载本项目不可变脚本的精确 JMX 原文，或当前流程导出；两个版本标识必须且只能提供一个。", schema: scopedSchema(map[string]any{"managerPlanId": planID, "workflowId": projectSchema()}), readOnly: true},
		{name: "list_manager_plans", description: "列出本项目不可变 JMX 版本；草稿由 list_test_plans 列出。", schema: scopedSchema(summaryPageSchema(nil)), readOnly: true},
		{name: "submit_console_task", description: "使用与控制台相同的管理端队列提交任务。仅当已发布脚本的 requires_project_environment 明确为 false 时可省略 environmentId，旧脚本及未完全绑定流程仍须提供；CSV脚本须提供 datasetSelections，支持copy/shard和每节点多个文件；timeoutSeconds 10至86400。脚本身份使用 pressurecooker.script_identities schema_version=1 声明，字段含name/type/secret/required；properties按script_identity.<身份名>.<声明字段>配置，最多64条身份；认证如何使用由脚本决定，旧Cookie身份脚本须显式转换并重新发布。身份名以字母开头，最多40个字母、数字、下划线或短横线。脚本选择身份，平台不自动轮询。全局属性合计最多100项，每值4096字符且身份字段为单行文本。任务固定环境快照；必须选择 clusterId，只使用所选集群控制器；省略 workers 时由服务端固定该集群全部关联节点，各节点执行完整 JMX，必须提供稳定幂等标识并在重试时复用参数。", schema: scopedSchema(map[string]any{
			"clusterId": strSchema(`^cluster-[a-f0-9]{24}$`, 0, 0), "managerPlanId": planID, "name": strSchema(`\S`, 1, 200), "idempotencyKey": strSchema(`\S`, 1, 200),
			"environmentId":           environmentIDSchema(),
			"datasetSelections":       map[string]any{"type": "array", "items": objectSchema(map[string]any{"node_id": projectSchema(), "dataset_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 50, "items": projectSchema()}, "distribution": enumSchema("copy", "shard")}, "node_id", "dataset_ids", "distribution")},
			"timeoutSeconds":          intSchema(10, 86400),
			"copySourceExecutionId":   runID,
			"copyEnvironmentSnapshot": map[string]any{"type": "boolean"},
			"reportTargets":           objectSchema(map[string]any{"min_rps": map[string]any{"type": "number", "minimum": 0, "maximum": 1e15}, "min_success_rate": map[string]any{"type": "number", "minimum": 0, "maximum": 100}, "max_p95_ms": map[string]any{"type": "number", "minimum": 0, "maximum": 1e15}, "max_p99_ms": map[string]any{"type": "number", "minimum": 0, "maximum": 1e15}, "min_duration_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 1e15}}),
			"workers":                 workerIDsSchema(1),
			"properties":              map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}, "notes": strSchema("", 0, 10000),
		}, "clusterId", "managerPlanId", "name", "idempotencyKey")},
		{name: "get_cluster_status", description: "读取管理端、按集群关联的控制器与执行节点、共享节点和队列状态。", schema: scopedSchema(map[string]any{}), readOnly: true},
		{name: "list_executions", description: "分页读取本项目控制台任务历史，不聚合 JTL。", schema: scopedSchema(map[string]any{"status": strSchema("", 0, 100), "search": strSchema("", 0, 200), "limit": intSchema(1, 200), "offset": map[string]any{"type": "integer", "minimum": 0}}), readOnly: true},
		{name: "get_execution_status", description: "读取本项目任务状态及原始文件归档进度。", schema: scopedSchema(map[string]any{"executionId": runID}, "executionId"), readOnly: true},
		{name: "stop_execution", description: "通过管理端异步停止本项目任务；随后轮询状态确认节点清理。", schema: scopedSchema(map[string]any{"executionId": runID, "mode": enumSchema("graceful", "now")}, "executionId")},
		{name: "get_execution_log", description: "读取本项目任务经过脱敏的有限日志尾部。", schema: scopedSchema(map[string]any{"executionId": runID, "tail": intSchema(1, 2000)}, "executionId"), readOnly: true},
		{name: "list_execution_artifacts", description: "列出本项目任务原始 JTL 和日志的字节数、SHA256 与 MinIO 对象键。", schema: scopedSchema(map[string]any{"executionId": runID}, "executionId"), readOnly: true},
		{name: "list_api_collections", description: "列出本项目接口集合。", schema: scopedSchema(summaryPageSchema(nil)), readOnly: true},
		{name: "create_api_collection", description: "保存本项目完整 OpenAPI 文档与 Scalar 客户端配置。", schema: scopedSchema(collectionProperties(), "name", "document")},
		{name: "get_api_collection", description: "读取本项目接口集合的完整文档、环境及设置。", schema: scopedSchema(map[string]any{"collectionId": projectSchema()}, "collectionId"), readOnly: true},
		{name: "delete_api_collection", description: "软删除本项目接口集合并保留历史。", schema: scopedSchema(map[string]any{"collectionId": projectSchema()}, "collectionId")},
		{name: "import_api_collection", description: "解析 cURL、Postman、Apifox 或 OpenAPI 文本并导入本项目；不会执行 shell。", schema: scopedSchema(map[string]any{"format": enumSchema("auto", "curl", "postman", "apifox", "openapi"), "content": strSchema("", 1, 5*1024*1024), "name": strSchema(`\S`, 1, 200)}, "format", "content")},
		{name: "export_api_collection", description: "将本项目集合导出为 OpenAPI 或 Postman，返回完整原文。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "format": enumSchema("openapi", "postman")}, "collectionId", "format"), readOnly: true},
		{name: "create_api_request", description: "按集合当前版本新增接口，保留完整 OpenAPI 操作对象。", schema: scopedSchema(requestProperties(), "collectionId", "revision", "path", "method", "operation")},
		{name: "update_api_request", description: "按集合当前版本修改接口；重命名路径或方法时提供原路径和原方法。", schema: scopedSchema(requestProperties(), "collectionId", "revision", "path", "method", "operation")},
		{name: "debug_api_request", description: "通过管理端有界 HTTP 代理调试本项目接口；target_source 优先选择 collection_environment 并指定 collection_environment_id；旧项目环境及文档地址保持显式兼容，目标参数互斥。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "path": strSchema(`^/`, 1, 10000), "method": enumSchema("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"), "environmentId": environmentIDSchema(), "collection_environment_id": projectSchema(), "target_source": enumSchema("collection_environment", "project_environment", "document_server"), "variables": anyObject()}, "collectionId", "path", "method", "target_source")},
		{name: "set_api_environments", description: "按稳定项目环境ID保存集合变量覆盖；空字符串有效，base_host由项目锁定。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "revision": revisionSchema(), "environmentId": environmentIDSchema(), "variables": environmentProperties()["variables"]}, "collectionId", "revision", "environmentId", "variables")},
		{name: "update_api_collection_settings", description: "按当前版本修改集合名称、描述、服务地址或 Scalar 客户端配置。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "revision": revisionSchema(), "name": strSchema(`\S`, 1, 200), "description": strSchema("", 0, 10000), "servers": map[string]any{"type": "array", "items": anyObject()}, "client_state": anyObject()}, "collectionId", "revision")},
	}
	update := collectionProperties()
	update["collectionId"] = projectSchema()
	update["revision"] = revisionSchema()
	specs = append(specs, managerSpec{name: "update_api_collection", description: "按集合当前版本保存完整 OpenAPI 文档与客户端配置，版本冲突由管理端拒绝。", schema: scopedSchema(update, "collectionId", "revision", "name", "document")})
	specs = append(specs, collectionEnvironmentSpecs()...)
	specs = append(specs, historyDeleteSpecs()...)
	specs = append(specs, datasetSpecs()...)
	specs = append(specs, pressureReportSpecs()...)
	specs = append(specs, monitoringSpecs()...)
	specs = append(specs, workflowSpecs()...)
	specs = append(specs, platformSpecs()...)
	specs = append(specs, interfacePluginSpecs()...)
	for i := range specs {
		if specs[i].name == "submit_console_task" {
			specs[i].schema["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"copyEnvironmentSnapshot": map[string]any{"const": true}}, "required": []string{"copyEnvironmentSnapshot"}}, "then": map[string]any{"required": []string{"copySourceExecutionId"}}}}
		}
		if specs[i].name == "download_script" {
			specs[i].schema["oneOf"] = []any{map[string]any{"required": []string{"managerPlanId"}, "not": map[string]any{"required": []string{"workflowId"}}}, map[string]any{"required": []string{"workflowId"}, "not": map[string]any{"required": []string{"managerPlanId"}}}}
		}
		specs[i].resolved = resolveSchema(specs[i].schema)
	}
	return specs
}
func resolveSchema(value map[string]any) *jsonschema.Resolved {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var schema jsonschema.Schema
	if err = json.Unmarshal(raw, &schema); err != nil {
		panic(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	return resolved
}
func resultJSON(value any) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("Cannot encode tool result")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil
}
func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}
func (m managerTools) register(server *mcp.Server) {
	for _, spec := range managerSpecs {
		destructive, open := !spec.readOnly, spec.name == "debug_api_request"
		server.AddTool(&mcp.Tool{Name: spec.name, Description: spec.description, InputSchema: spec.schema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: spec.readOnly, DestructiveHint: &destructive, OpenWorldHint: &open}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 && decodeJSON(req.Params.Arguments, &args) != nil {
				return toolError("Invalid tool arguments"), nil
			}
			value, err := m.call(ctx, spec.name, args)
			if err != nil {
				return toolError(err.Error()), nil
			}
			return resultJSON(value)
		})
	}
}
func (m managerTools) call(ctx context.Context, name string, args map[string]any) (any, error) {
	found := false
	for _, spec := range managerSpecs {
		if spec.name == name {
			found = true
			// The validator infers json.Number as string. Normalize its private
			// validation copy while forwarding the original precise numbers.
			raw, err := json.Marshal(args)
			var validation map[string]any
			if err != nil || json.Unmarshal(raw, &validation) != nil {
				return nil, fmt.Errorf("Invalid arguments for %s", name)
			}
			if err := spec.resolved.Validate(validation); err != nil {
				return nil, fmt.Errorf("Invalid arguments for %s", name)
			}
			break
		}
	}
	if !found {
		return nil, errors.New("Unknown manager tool")
	}
	s := func(key, fallback string) string {
		if value, ok := args[key].(string); ok {
			return value
		}
		return fallback
	}
	project := s("projectId", "")
	request := func(method, path string, body any, key string) (map[string]any, error) {
		return m.api.requestProject(ctx, method, path, body, key, project)
	}
	payload := func(exclude ...string) map[string]any {
		body := map[string]any{}
		for k, v := range args {
			if k == "projectId" {
				continue
			}
			skip := false
			for _, excluded := range exclude {
				if k == excluded {
					skip = true
				}
			}
			if !skip {
				body[k] = v
			}
		}
		return body
	}
	collectionPath := "/api/api-collections/" + s("collectionId", "")
	switch name {
	case "delete_execution", "delete_executions", "clear_executions", "delete_pressure_report", "delete_pressure_reports", "clear_pressure_reports":
		return m.historyDeleteTool(ctx, name, args)
	case "list_interface_protocols", "list_interface_plugins", "get_interface_plugin", "create_interface_plugin", "update_interface_plugin", "delete_interface_plugin", "install_interface_plugin", "uninstall_interface_plugin":
		return m.interfacePluginTool(ctx, name, args)
	case "write_pressure_report_middleware", "write_pressure_report_jvm", "write_pressure_report_jvm_batch", "get_pressure_report_monitoring":
		return m.monitoringTool(ctx, name, args)
	case "list_pressure_reports", "get_pressure_report", "get_latest_pressure_report", "generate_pressure_report", "list_pressure_report_steps", "list_pressure_report_nodes", "list_pressure_report_issues", "list_pressure_report_logs", "list_pressure_report_samples", "get_pressure_report_analytics", "compare_pressure_reports", "export_pressure_report":
		return m.pressureReportTool(ctx, name, args)
	case "list_workflows", "create_workflow", "get_workflow", "update_workflow", "delete_workflow", "import_workflow", "preview_workflow", "publish_workflow", "list_workflow_versions", "get_workflow_version", "list_workflow_plans":
		return m.workflowTool(ctx, name, args)
	case "list_datasets", "get_dataset_catalog", "download_dataset_template", "list_api_collection_versions", "get_api_collection_version", "update_cluster", "get_execution_copy_config":
		return m.platformTool(ctx, name, args)
	case "list_workflow_datasets", "list_plan_datasets", "upload_dataset", "generate_dataset", "get_dataset", "preview_dataset", "download_dataset", "delete_dataset":
		return m.datasetTool(ctx, name, args)
	case "list_projects":
		return request(http.MethodGet, "/api/projects", nil, "")
	case "create_project":
		return request(http.MethodPost, "/api/projects", payload(), "")
	case "get_project":
		return request(http.MethodGet, "/api/projects/"+project, nil, "")
	case "update_project":
		return request(http.MethodPut, "/api/projects/"+project, payload(), "")
	case "archive_project":
		return request(http.MethodPost, "/api/projects/"+project+"/archive", payload(), "")
	case "list_project_environments":
		return request(http.MethodGet, "/api/environments", nil, "")
	case "create_project_environment":
		return request(http.MethodPost, "/api/environments", payload(), "")
	case "get_project_environment":
		return request(http.MethodGet, "/api/environments/"+s("environmentId", ""), nil, "")
	case "update_project_environment":
		return request(http.MethodPut, "/api/environments/"+s("environmentId", ""), payload("environmentId"), "")
	case "delete_project_environment":
		return request(http.MethodDelete, "/api/environments/"+s("environmentId", ""), nil, "")
	case "publish_test_plan":
		if m.drafts == nil || !m.drafts.owns(project, s("planId", "")) {
			return nil, errors.New("Draft not found in this project")
		}
		plan, err := upstreamJSON(ctx, m.upstream, "get_test_plan", map[string]any{"planId": s("planId", "")})
		if err != nil {
			return nil, err
		}
		xml, err := upstreamJSON(ctx, m.upstream, "get_test_plan_xml", map[string]any{"planId": s("planId", "")})
		if err != nil {
			return nil, err
		}
		planName, ok := plan["name"].(string)
		if !ok {
			return nil, errors.New("Upstream draft has no name")
		}
		content, ok := xml["xml"].(string)
		if !ok {
			return nil, errors.New("Upstream draft has no JMX")
		}
		published, err := request(http.MethodPost, "/api/plans", map[string]any{"name": planName, "content": content, "description": "Published from jmeter-mcp-server draft " + s("planId", "")}, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"managerPlanId": published["id"], "draftPlanId": s("planId", ""), "plan": published}, nil
	case "upload_script":
		plan, err := request(http.MethodPost, "/api/plans", payload(), "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"managerPlanId": plan["id"], "plan": plan, "project_id": project}, nil
	case "download_script", "export_api_collection":
		path := collectionPath + "/export?format=" + url.QueryEscape(s("format", "openapi"))
		if name == "download_script" {
			if s("managerPlanId", "") != "" {
				path = "/api/plans/" + s("managerPlanId", "") + "/content"
			} else {
				path = "/api/workflows/" + s("workflowId", "") + "/export"
			}
		}
		content, err := m.api.requestRaw(ctx, http.MethodGet, path, nil, "", project)
		if err != nil {
			return nil, err
		}
		value := payload()
		value["content"] = string(content)
		value["project_id"] = project
		return value, nil
	case "list_manager_plans":
		return request(http.MethodGet, "/api/plans"+summaryQuery(args), nil, "")
	case "submit_console_task":
		properties := args["properties"]
		if properties == nil {
			properties = map[string]string{}
		}
		body := map[string]any{"cluster_id": s("clusterId", ""), "plan_id": s("managerPlanId", ""), "name": strings.TrimSpace(s("name", "")), "properties": properties, "notes": s("notes", "")}
		if workers, present := args["workers"]; present {
			body["workers"] = workers
		}
		if environment := s("environmentId", ""); environment != "" {
			body["environment_id"] = environment
		}
		if value, present := args["datasetSelections"]; present {
			body["dataset_selections"] = value
		}
		if value, present := args["timeoutSeconds"]; present {
			body["timeout_seconds"] = value
		}
		if value, present := args["reportTargets"]; present {
			body["report_targets"] = value
		}
		for from, to := range map[string]string{"copySourceExecutionId": "copy_source_run_id", "copyEnvironmentSnapshot": "copy_environment_snapshot"} {
			if value, present := args[from]; present {
				body[to] = value
			}
		}
		run, err := request(http.MethodPost, "/api/runs", body, strings.TrimSpace(s("idempotencyKey", "")))
		if err != nil {
			return nil, err
		}
		run["executionId"] = run["id"]
		return run, nil
	case "get_cluster_status":
		return request(http.MethodGet, "/api/cluster", nil, "")
	case "list_executions":
		query := url.Values{}
		for key, value := range payload() {
			query.Set(key, queryValue(value))
		}
		return request(http.MethodGet, "/api/runs?"+query.Encode(), nil, "")
	case "get_execution_status":
		return request(http.MethodGet, "/api/runs/"+s("executionId", ""), nil, "")
	case "stop_execution":
		return request(http.MethodPost, "/api/runs/"+s("executionId", "")+"/stop", map[string]any{"mode": s("mode", "graceful")}, "")
	case "get_execution_log":
		tail := args["tail"]
		if tail == nil {
			tail = 200
		}
		return request(http.MethodGet, "/api/runs/"+s("executionId", "")+"/log?tail="+queryValue(tail), nil, "")
	case "list_execution_artifacts":
		return request(http.MethodGet, "/api/runs/"+s("executionId", "")+"/artifacts", nil, "")
	case "list_api_collections":
		return request(http.MethodGet, "/api/api-collections"+summaryQuery(args), nil, "")
	case "create_api_collection":
		return request(http.MethodPost, "/api/api-collections", payload(), "")
	case "get_api_collection":
		return request(http.MethodGet, collectionPath, nil, "")
	case "update_api_collection":
		return request(http.MethodPut, collectionPath, payload("collectionId"), "")
	case "delete_api_collection":
		return request(http.MethodDelete, collectionPath, nil, "")
	case "import_api_collection":
		return request(http.MethodPost, "/api/api-collections/import", payload(), "")
	case "create_api_request", "update_api_request":
		return request(http.MethodPut, collectionPath+"/requests", payload("collectionId"), "")
	case "list_api_collection_environments":
		return request(http.MethodGet, collectionPath+"/collection-environments", nil, "")
	case "get_api_collection_environment":
		return request(http.MethodGet, collectionPath+"/collection-environments/"+s("collectionEnvironmentId", ""), nil, "")
	case "create_api_collection_environment":
		return request(http.MethodPost, collectionPath+"/collection-environments", payload("collectionId", "collectionEnvironmentId"), "")
	case "update_api_collection_environment":
		return request(http.MethodPut, collectionPath+"/collection-environments/"+s("collectionEnvironmentId", ""), payload("collectionId", "collectionEnvironmentId"), "")
	case "delete_api_collection_environment":
		return request(http.MethodDelete, collectionPath+"/collection-environments/"+s("collectionEnvironmentId", ""), payload("collectionId", "collectionEnvironmentId"), "")
	case "debug_api_request":
		body := payload("collectionId", "environmentId")
		if environment := s("environmentId", ""); environment != "" {
			body["environment_id"] = environment
		}
		return request(http.MethodPost, collectionPath+"/debug", body, "")
	case "set_api_environments":
		body := payload("collectionId", "environmentId")
		body["environment_id"] = s("environmentId", "")
		return request(http.MethodPut, collectionPath+"/environments", body, "")
	case "update_api_collection_settings":
		return request(http.MethodPut, collectionPath+"/settings", payload("collectionId"), "")
	}
	return nil, errors.New("Unknown manager tool")
}

func queryValue(value any) string {
	if number, ok := value.(json.Number); ok {
		if rational, ok := new(big.Rat).SetString(number.String()); ok && rational.IsInt() {
			return rational.Num().String()
		}
	}
	return fmt.Sprint(value)
}
func upstreamValue(ctx context.Context, caller upstreamCaller, name string, args map[string]any) (any, error) {
	if caller == nil {
		return nil, errors.New("Upstream authoring server is unavailable")
	}
	result, err := caller.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, errors.New("Upstream authoring request failed")
	}
	if result.IsError {
		return nil, errors.New("Upstream draft could not be read")
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			var value any
			if decodeJSON([]byte(text.Text), &value) == nil && value != nil {
				return value, nil
			}
		}
	}
	return nil, errors.New("Upstream authoring server returned invalid JSON")
}
func upstreamJSON(ctx context.Context, caller upstreamCaller, name string, args map[string]any) (map[string]any, error) {
	value, err := upstreamValue(ctx, caller, name, args)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("Upstream authoring server returned invalid JSON")
	}
	return object, nil
}
