# MCP history deletion

授权：用户明确要求补齐 MCP 六种删除能力、部署定向验收，以及清空数据集；不启动真实业务压测。

采用现有 Go MCP 与已有 history delete API。新增 delete_execution / delete_executions / clear_executions 和 delete_pressure_report / delete_pressure_reports / clear_pressure_reports。projectId 必填；批量 1 至 100 个不同的合法 ID；清空必须 confirm=true，不接受筛选或跨项目范围。删除复用管理端原子保护、归档只读、未结束任务保留和产物清理。工具标记 destructive，返回原有 deleted_count/skipped_count/cleanup_pending。

- [ ] 先加六工具路由/校验/错误透传的失败定向测试，再实现独立 history_delete_tools.go 与注册。
- [ ] 更新中文目录、示例、参数、返回说明及实时工具夹具（128→134），只运行新增/目录相关契约验证及构建。
- [ ] 数据集先备份，按已有软删除语义清空各项目的当前文件，保留流程 CSV 节点与模板定义，验证列表为空。
- [ ] 独立代码复核，从当前源码冻结后重建 manager/MCP/frontend，保留服务器显式 Compose 标识、密钥和节点配置。
- [ ] 在线真实 MCP initialize/tools/list/tools/call 验收六操作、跨项目/归档/活动保护（合成终态数据；活动保护用隔离测试验证），不得调用 submit_console_task 或运行 JMeter；清理验收夹具。
