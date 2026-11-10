package main

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func connectUpstream(ctx context.Context, node, script, workspace string) (*mcp.ClientSession, error) {
	command := exec.CommandContext(ctx, node, script)
	// The upstream only needs authoring/runtime paths; manager credentials stay in Go.
	for _, key := range []string{"PATH", "HOME", "LANG", "LC_ALL", "TZ", "JAVA_HOME", "JMETER_HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	command.Env = append(command.Env, "JMETER_MCP_WORKSPACE="+workspace)
	command.Stderr = io.Discard
	command.WaitDelay = time.Second
	client := mcp.NewClient(&mcp.Implementation{Name: "jmeter-manager-upstream", Version: "1.0.0"}, nil)
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	session, err := client.Connect(startup, &mcp.CommandTransport{Command: command, TerminateDuration: 500 * time.Millisecond}, nil)
	if err != nil {
		return nil, errors.New("Cannot connect to the official JMeter authoring server")
	}
	return session, nil
}

const mcpServerVersion = "0.4.5-manager-go.11"
const instructions = "所有业务工具必须显式指定 projectId。草稿按项目隔离；每次编排操作先确认管理端项目存在，归档项目仅可列出、读取和导出草稿，禁止创建、导入及修改；通过 publish_test_plan、upload_script 或 publish_workflow 保存不可变 JMX，再使用 submit_console_task 与稳定 idempotencyKey 提交控制台任务。任务必须选择 clusterId；environmentId 默认必填，仅管理端发布元数据 requires_project_environment=false 的全目标集合环境绑定流程可省略；省略 workers 由服务端固定该集群节点，各节点执行完整 JMX，平台仍全局一次一批。get_execution_copy_config 只读取可编辑配置；提交副本使用新幂等标识，重试才复用原标识与参数。流程、接口集合、环境、数据集与历史版本经管理端 API 管理；集合环境通过 list/get/create/update/delete_api_collection_environment 系列工具维护，采用 collection_environment_id 独立身份并可持续继承父项目环境；接口调试使用 target_source=collection_environment，旧 set_api_environments 仍是项目环境ID的历史覆盖契约。项目与集合变量可用完整自动规则{{$auto.client_ip}}和{{$auto.timestamp_ms}}，变量名任意；前者取实际调用方HTTP来源IP，后者取当前毫秒时间戳字符串，同次操作固定、任务重试复用快照；来源不可用时IP规则报错，不使用内部回环地址或未受信转发头。接口协议由list_interface_protocols查询；RPC/gRPC/Protobuf仅保留描述扩展，不执行或回退HTTP。接口展示插件目录默认为空，由list_interface_plugins读取本项目自定义元数据，get/create/update/delete_interface_plugin管理项目自定义配置或JavaScript惰性定义，install_interface_plugin/uninstall_interface_plugin按独立插件revision管理安装状态（首次0）。自定义插件只解析和展示已有WebSocket消息，在浏览器受限沙箱运行，不能发送或访问网络，管理端不执行代码。WebSocket保持原始帧及通用JSON解析，只有ser/now/message_id/last_ser及本次传统Socket.IO握手的socket_id为流程运行时变量，业务解析和JSON提取规则由项目显式定义。结构化报告使用 pressure_report 系列工具查询、生成或对比，读取报告不会隐式生成报告。任务与报告支持delete_execution/delete_executions/clear_executions及delete_pressure_report/delete_pressure_reports/clear_pressure_reports，均限显式projectId；批量1至100个不同ID，任一跨项目/无效记录原子拒绝；任务单条/批量只接受已结束任务，清空保留未结束任务并返回skipped_count。清空须confirm=true且忽略筛选和分页；报告独立删除保留任务与原始文件，任务删除级联关联报告和产物，物理清理可异步重试。归档项目禁止删除。接口留样必须明确 stepKey，可按 nodeId 限定；历史缺失、空内容和断言未知不等于成功。write_pressure_report_middleware 和 write_pressure_report_jvm 将外部中间件/JVM证据保存为独立监控补充版本；写入须带当前revision和稳定idempotencyKey，同请求重试复用原revision/键/参数，其他更新使用新键。上百JVM实例使用write_pressure_report_jvm_batch批量提交（每批最多200实例，仅新增一个监控revision），get_pressure_report_monitoring默认每页20实例并支持component/search/limit/offset，读取最新或指定监控补充版本，数据时间需与压测重叠，不改变JTL指标或验收结论。报告导出和数据下载需要已有认证，下载URL不含令牌，默认相对管理站点地址。禁止上游本地执行和本地报告生成。仅执行用户授权的目标和负载。所有文件路径均为管理端容器路径。"

func newProxyServer(ctx context.Context, upstream *mcp.ClientSession, api managerClient, roots []string) (*mcp.Server, error) {
	if len(roots) == 0 {
		return nil, errors.New("MCP workspace is required")
	}
	drafts, err := openDraftProjects(ctx, upstream, roots[0])
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "jmeter-mcp-server", Version: mcpServerVersion}, &mcp.ServerOptions{Instructions: instructions})
	for upstreamTool, err := range upstream.Tools(ctx, nil) {
		if err != nil {
			return nil, errors.New("Cannot discover upstream authoring tools")
		}
		if !allowedAuthoring(upstreamTool.Name) {
			continue
		}
		tool := *upstreamTool
		schema, err := scopedAuthoringSchema(&tool)
		if err != nil {
			return nil, err
		}
		tool.InputSchema = schema
		// Some upstream descriptions refer to local execution/report tools that are omitted here.
		tool.Description = strings.ReplaceAll(tool.Description, "get_execution_report", "raw manager artifacts")
		tool.Description = strings.ReplaceAll(tool.Description, "execute_test_plan", "submit_console_task")
		server.AddTool(&tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 && decodeJSON(req.Params.Arguments, &args) != nil {
				return toolError("Invalid tool arguments"), nil
			}
			if args == nil {
				return toolError("Tool arguments must be an object"), nil
			}
			if err := api.requireAuthoringProject(ctx, tool.Name, args); err != nil {
				return toolError(err.Error()), nil
			}
			result, err := drafts.call(ctx, upstream, tool.Name, args, roots)
			if err != nil {
				return toolError(err.Error()), nil
			}
			return resultJSON(result)
		})
	}
	managerTools{api: api, upstream: upstream, drafts: drafts}.register(server)
	return server, nil
}
