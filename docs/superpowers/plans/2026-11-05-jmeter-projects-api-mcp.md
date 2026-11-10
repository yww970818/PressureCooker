# JMeter 项目与接口复用 Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development or superpowers:executing-plans to implement task by task.

**Goal:** 完整实现本轮项目、流程接口引用、接口导入及 MCP 11 项需求。

**Architecture:** Go 服务端执行项目归属校验，Vue 共享项目上下文。复用现有 Scalar 编辑器和控制台执行队列，不增加服务。

**Tech Stack:** Go 1.25、Vue 3、MySQL 8、LogicFlow 2.2.5、Scalar 3.23.6。

**Spec:** [设计](../specs/2026-11-05-jmeter-projects-api-mcp-design.md)

## Global Constraints

- 六 worker、双 controller、单 manager、MinIO；仅原始 JTL 和日志。
- 历史脚本精确不可变，业务数据保持原 ID；密文保留会话密钥。
- 后端 Go，前端 Vue，原生 LogicFlow 与 Scalar，全量中文。
- 项目请求 X-Project-ID；缺省 project-default；MCP 项目参数 projectId。

## Review Focus

- 按 ID 绕过列表过滤：跨项目详细、版本、下载、停止与引用应拒绝。
- 页面切换时异步返回：旧项目响应不得污染新项目。
- 引用集合修改：已发布 JMX 不变，流程显式同步。
- 导入原文的 shell 表达式：只解析，不执行。
- 旧执行工具别名：MCP 列表及调用路径均禁止直接运行。

### Task 1: 项目及管理端契约

Files: manager-go/projects*.go、store.go、workspace*.go、http.go、admin.go、api_import*.go、api_operations*.go、对应测试。

Produces: 设计文档中的项目、导入导出、请求编辑/调试、环境、集合设置 API；响应 project_id。

- [x] 写迁移/跨项目/幂等/归档/接口转换回归并确认先失败。
- [x] 实现服务端项目隔离和既有数据迁移，保证备份恢复。
- [x] 实现导入导出及接口、环境、设置、调试 API；继续使用有界安全 HTTP 传输。
- [x] 运行 Go 测试、race、vet 和真实 MySQL 前缀隔离测试。

### Task 2: 原生 Scalar 流程复用及接口导入页面

Files: frontend/src/api-management/**、workflow/**、script-library/**。

Consumes: Task 1 接口；X-Project-ID 由共享 api.js 注入。

- [x] 写引用映射、参数保真、导入和失效引用回归并确认先失败。
- [x] HTTP 节点嵌入原生 Scalar 编辑器，支持选择集合请求、显式同步、取消及应用快照。
- [x] 扩展接口导入及集合设置入口，保持已有草稿保护和 Scalar 环境功能。
- [x] 局部及完整前端测试、生产构建。

### Task 3: MCP 控制台能力

Files: mcp-go/**、frontend/src/mcp-catalog.js、相关 fixture/tests。

Consumes: Task 1 API。

- [x] 写旧工具不可用、项目参数、上传下载、导入导出、调试、环境、集合设置、任务字段转发测试并确认先失败。
- [x] 新增项目与接口工具，移除 execute_test_plan，submit_console_task 接入同一控制台队列。
- [x] 更新中文工具目录和文档，运行 Go 测试、race、vet。

### Task 4: 项目切换与管理界面

Files: frontend/src/projects/**、Workspace.vue、api.js、hooks.js、共享标签与测试。

- [x] 写项目请求捕获、切换防串数据与未保存保护回归并确认先失败。
- [x] 品牌下项目选择、项目管理、项目归档；切换重建页面实例并保护草稿。
- [x] 将用户可见“脚本库”改为“流程编排”。

### Task 5: 集成、复核及部署

- [x] 独立复核全部 11 项、备份恢复、项目隔离和原始归档不变。
- [x] 真实数据库及 MCP 联调，备份线上，构建和部署 manager。
- [x] 浏览器验收项目切换、导入、引用、编辑与中文；内部六节点任务验证并关闭自测服务。
- [x] 更新操作文档与任务完成证据，交付运行地址。

## 完成证据（2026-11-05）

- 前端 21 文件、131 测试通过；生产构建通过。Go 单元、race、vet 及真实 MySQL 前缀隔离测试通过。独立复核提出的路径变量、JSON 字节保真、项目刷新草稿保护及原生环境变量兼容问题均已修复并复核。
- 实时 MCP 73 个工具；页面与真实协议均确认旧 `execute_test_plan` 无法调用，新控制台提交工具完整转发字段。
- 浏览器验证：项目设置保存、切换和草稿保护；cURL 导入；master 转发返回 200；真实 Scalar 引用接口、应用、保存、发布；390×844 项目入口无横向溢出。最终一轮浏览器控制台无新增错误或警告。
- 内部任务 `run-5f16f15824c6eb8b4f694a6ca37e4d24` 与页面引用流程任务 `run-0c6ce9ce3566752c0109e51f4728e46a` 均为 6 节点、30 条请求、0 错误；6 个 MinIO 原始对象通过直接下载和 SHA256 校验。没有生成新分析报告，原始不可变脚本未被引用编辑改写。
- 更新前备份 `pre-projects-api-mcp-20261105`；更新后备份及恢复校验 `projects-api-mcp-20261105`；MCP 草稿和归属索引单独备份 `projects-api-mcp-extra-20261105/mcp-state.tar.gz`。恢复包含 3 项目，33 行旧密文原样保留，3825 个快照文件校验通过。
- 仅访问 Docker 内部自测目标，自测服务已关闭。管理平台 `http://home.yanww.top:18090/`，MCP 地址不变；客户端重连后加载新版工具。
