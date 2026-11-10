# 集合环境集中管理、流程绑定与部署实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在全宽项目环境页统一管理项目与集合环境，接口调试和流程引用只选择集合环境，完成测试、独立审查及既有实例部署。

**Architecture:** 集合环境嵌入集合加密内容，具有稳定 ID 和可选父项目环境，动态合并父配置及自有覆盖。前端共用解析与选择状态，流程节点保存独立环境来源，在任务提交时冻结节点级地址及变量；历史引用不改写。用户已授权详细设计、实施及部署，执行采用一个贯通实现者加独立审查，root 负责最终发布。

**Tech Stack:** Vue 3、Scalar、Go、MySQL、JMeter、Docker Compose。

**Spec:** `docs/superpowers/specs/2026-11-11-collection-environment-resources-design.md`

## Global Constraints

- 自研后端、CLI、MCP 及执行器使用 Go，前端 Vue；不引入业务规则或外部业务测试资产。
- 集合环境管理放在项目环境页；接口管理不保留两类环境配置标签页。
- 持续继承父项目环境，集合自有同名值优先；普通空字符串是明确覆盖，基础地址须为有效 HTTP(S)。
- 密钥、本地验收记录及运行数据仅在 `runtime/`；不打印秘密值，不提交运行数据。
- 当前 Git 无初始提交，全部源码是共享未跟踪文件。使用源码基线快照与逐文件差异审查，不创建空 worktree，不提交或清理他人文件。
- 保留历史原文、精确集合修订、已发布脚本及任务快照；父引用失效可保存但不得执行；未知规则惰性兼容。
- 部署保留 `/home/yanww/pressure/jmeter`、`COMPOSE_PROJECT_NAME=walmart-jmeter`、现有目录、持久卷及原 session-key。从当前冻结源码重建，仅更新必要管理端镜像。
- 构建、迁移及验收不启动真实业务压测；请求测试只用隔离通用夹具。

## Review Focus

- 两个集合使用相同变量名和不同地址时，HTTP/WS 节点的地址、基础路径及变量必须隔离；覆盖测试属于 Task 1。
- 保存环境回执不能覆盖接口管理中的未保存请求草稿；跨页、跨集合和迟到异步响应测试属于 Task 1。
- 旧覆盖首次显式保存后，删除的兼容环境不能因旧字段存在而复活；空新数组为权威，测试属于 Task 1。
- 父环境删除或跨项目引用必须保留编辑能力并阻止新执行；历史任务和幂等重试不被当前状态破坏，测试属于 Task 1。
- 全绑定流程省略项目环境仅限服务端发布元数据证明无依赖；旧/未知插件脚本保持必填，API、MCP、UI 测试属于 Task 1。

---

### Task 1: 贯通实现集合环境资源、集中页面与流程执行

**Files:**
- Create: `controlplane/manager-go/collection_environment_resources.go` 及定向测试。
- Modify: 管理端 `workspace_http.go`、集合校验/存储、`api_operations.go`、`collection_binding.go`、`environments.go`、流程元数据与编译、任务创建/复制/参数展示相关文件。
- Create: `controlplane/frontend/src/projects/CollectionEnvironments.vue`、`controlplane/frontend/src/api-management/collection-environments.js`、集合环境选择器及相应测试。
- Modify: `projects/ProjectEnvironments.vue`、`Workspace.vue`、`projects/view-state.js`、`api-management/ApiManagement.vue`、HTTP/WS/预留协议环境接入、流程 HTTP/WS 引用编辑器、任务创建和参数摘要、相关样式及测试。
- Modify: MCP 工具、转发、中文目录和真实目录 fixture；README 及环境/流程/MCP 使用说明。

**Interfaces:**
- Authored contract: `client_state.collection_environments:[{id,name,parent_environment_id?,base_host?,variables:[{name,value}]}]`；字段存在（含空数组）后为权威，旧覆盖只作兼容。
- Environment reference: 节点 `properties.collection_environment_reference={collection_id,revision,collection_environment_id}`；与 `api_reference` 分开保存，解除接口引用保留环境来源。
- API: `GET/POST /api/api-collections/:collectionId/collection-environments`，`GET/PUT/DELETE /api/api-collections/:collectionId/collection-environments/:environmentId`；写操作要求集合 revision，返回更新集合及环境结果。名称唯一，ID 管理端产生，父环境仅同项目。
- Debug: 显式 `target_source=collection_environment` 和 `collection_environment_id`；与旧 `environment_id`/文档目标互斥。旧目标契约显式兼容。
- Snapshot: 任务保存每个新绑定节点的集合修订、环境 ID、父环境 ID/revision、最终 base_host/variables 及运行属性来源，敏感值沿用现有加密及脱敏。
- Published plan: 新增服务端计算的 `requires_project_environment`，默认/缺失为 true；仅确认所有目标均有新绑定且没有剩余项目环境依赖时为 false。保留旧 `requires_environment` 含义兼容，不把客户端输入作为证明。
- Runtime: HTTP/WS 节点级地址及变量使用哈希隔离的运行属性；模板编译不固定预览值，动态 producer 遵守既有作用域优先规则。
- State: 管理页面集合环境 CRUD 的保存回执只同步对应环境状态和集合 revision，不能覆盖请求草稿；发生请求草稿版本冲突时保留草稿并提示重新读取/合并。
- Produces: 测试通过的产品源码、完整变更文件清单、任务报告；不部署，不修改真实项目配置或运行任务。

- [ ] **Step 1: 核对相关入口并先写失败行为测试。** 覆盖独立 CRUD、继承/同名/空值/地址、删除失效、项目隔离、revision、旧数据投影与删除不复活，记录 RED 命令及实际失败。
- [ ] **Step 2: 实现 Go 资源与解析契约。** 复用 AES-GCM/集合 revision，新增 CRUD 与明确调试目标；未改变的失效父引用按惰性保留，执行拒绝。新增/修改父引用校验有效性和项目归属，导入失效引用可读取修正。
- [ ] **Step 3: 实现集中管理页面与选择器。** 项目环境页全宽并提供两管理分区，集合分区选择集合后编辑；接口管理移除环境选项卡，空状态提供定位管理页入口；保存、切换、归档及异步状态有定向回归。
- [ ] **Step 4: 实现流程引用、编译及运行快照。** 新引用显式选集合环境，静态地址/变量按节点隔离；引用精确修订、父环境在新任务动态读取。解除定义引用保留环境绑定；明确解除后恢复任务环境或手工配置，不自动冻结秘密值。
- [ ] **Step 5: 条件化任务项目环境必填。** 服务端发布元数据证明全绑定才允许省略，创建/复制/API/MCP/UI 一致；验证旧脚本、未知扩展和未绑定节点仍要求项目环境，幂等重试复用快照。
- [ ] **Step 6: 同步 MCP、中文目录、fixture 与操作文档。** 新增 list/get/create/update/delete_api_collection_environment 工具，debug_api_request 增加新目标模式，不改变旧 set_api_environments 的身份含义。
- [ ] **Step 7: GREEN 验证及自查。** 使用 `/tmp/jmeter-go-toolchain/go/bin/go` 或本仓库锁定工具链；前端定向测试及生产构建，manager/MCP 定向测试和三个 Go 模块编译；目录 fixture 变更按仓库要求跑 MCP 和前端全量一次。测试不依赖外部业务资源。保存完整结果、changed-files、未决问题到任务报告。
- [ ] **Step 8: 独立审查与修复。** root 按基线生成完整差异，fresh reviewer 检查规格和质量；实现者修复重要问题并运行覆盖测试，复核后冻结源码。共享未跟踪工作区不创建 Git 提交。

### Task 2: 当前源码重建、部署与页面验收

**Files:**
- Create: `runtime/releases/20261111-collection-environments/` 中的备份、源码清单、产物、发布回执、QA 证据。
- Modify: 仅目标实例必要源码/构建产物及 manager 镜像标识，保留显式配置、目录、卷和原 session-key。

**Interfaces:**
- Consumes: Task 1 审查通过且测试通过的冻结源码与 changed-files。
- Produces: 新构建及源码 SHA256，部署前后健康状态、页面交互验收和可回退备份位置。

- [ ] **Step 1: 发布前核对。** 只读确认目标实例、镜像和 Compose 标识；通过认证读取全局任务队列，只有无活动执行时更新 manager；不终止用户任务。
- [ ] **Step 2: 备份。** 备份数据库逻辑内容、源码/构建产物、镜像引用和配置；原 session-key 安全保留，回执只存指纹/路径，不打印值。
- [ ] **Step 3: 冻结并重建。** 使用当前源码构建前端和 Go Linux/amd64 管理端/MCP；验收源码及产物清单，独立 staging 构建 manager 镜像，不修改执行池及 MinIO。
- [ ] **Step 4: 部署。** 保留 `walmart-jmeter` 与既有卷/目录，更新 manager，确认 healthz/readyz、核心/worker 登记、任务队列和 MCP 目录。
- [ ] **Step 5: UI 与通用配置验收。** 桌面/窄屏验证集中管理、接口页无环境标签、CRUD/继承和流程选择；使用独立验收项目与合成目标，不访问业务地址，不发压；清理只清理由本次创建的临时资源。
- [ ] **Step 6: 发布回执与交付。** 记录设计/计划/报告、镜像和源码指纹、备份位置、验证通过项目和真实剩余限制，向用户报告实际部署结果。
