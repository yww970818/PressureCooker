# JMeter 项目环境与运行时切换实施计划

> For agentic workers: use superpowers:subagent-driven-development or superpowers:executing-plans. Execute the independent backend, frontend and MCP tasks against the shared interfaces below.

**Goal:** 同一流程、同一不可变脚本版本在任务提交时选择测试或正式环境，接口调试与编排共用项目环境。

**Architecture:** 项目环境为加密、带版本、可软删除的项目资源。可切换流程只保存环境绑定，编译成 JMeter 属性引用；任务提交事务内解析所选环境并固定快照和下发属性。编排中的环境选择只控制预览/调试，不能隐式决定任务环境。

**Tech Stack:** Go / Vue 3 / MySQL / LogicFlow / Scalar / JMeter 5.6.3。

**Spec:** 本会话已获用户批准的五点方案：项目统一环境、base_host 绑定、编排预览选择、任务明确选择、历史快照。

## Global Constraints

- 自研前端 Vue，其余自研代码 Go；不新增运行服务、不生成 HTML 或统计报告。
- 项目隔离、归档项目只读、现有鉴权与 CSRF、加密密钥保持一致。
- 不改已有不可变 JMX；既有固定地址脚本继续按原方式运行。
- 新流程默认启用项目环境绑定，既有流程可显式启用后重新发布。
- 绑定脚本必须明确选择 environment_id；队列和历史不能重新读取可变配置。
- 任务属性不能覆盖平台注入的 jmeter.env.*；幂等重试复用原任务快照，即使环境已被更新或删除。
- 保留现有画布缩放、同一行接口标题以及原始文件归档行为。
- 只测试相关变更及集成链路；不向业务环境发压。
- 当前 deploy 工作区为未跟踪资产，不进行 Git reset、清理、提交或无关文件重构。

## Shared Interfaces

- GET /api/environments => {items:[Environment],total,project_id}；POST 新建；GET/PUT/DELETE /api/environments/:id。
- Environment = {id,project_id,name,base_host,variables:[{name,value}],revision,created_at,updated_at}；base_host 为完整 HTTP(S) 地址，variables 为自定义变量（不再包含 base_host）。PUT 须 revision。
- 流程 graph 可选 environment_binding:"base_host"；缺省/空表示固定地址。环境绑定流程全部 HTTP 节点继承 base_host。
- 绑定 JMX 使用 jmeter.env.protocol / host / port / base_path / base_host 运行属性；HTTP 路径保持请求自身路径，避免环境基础路径重复拼接。
- 发布/上传后的 Plan 提供 requires_environment:boolean。识别保留属性引用，精确 JMX 导入/预览/再次编辑保留绑定。
- POST /api/runs 新增 environment_id；run 返回 environment_snapshot（id,name,revision,base_host,variables及解析地址）和 environment_id；详情/列表/MCP 共用。
- 运行时环境自定义变量也应采用一致的属性绑定规则，不把预览环境的值固化进发布脚本；后端与前端在实现前确认具体属性名约定。
- MCP submit_console_task 对外 environmentId 映射后端 environment_id；环境 CRUD 工具均携带 projectId。
- API debug 支持 environment_id；指定项目环境时服务器和变量取该环境，与 UI 行为一致。

## Review Focus

1. 跨项目、归档及已删除环境必须拒绝新任务。
2. 幂等重试在环境变更后仍返回原任务，不能改变快照或悄悄创建另一任务。
3. 编排预览、实际调试、JMX 生成和六个执行节点地址必须一致。
4. 旧集合环境保留，可显式导入项目环境；不能批量覆盖同名但地址不同的集合配置。
5. base_host 包含协议、端口、IPv6、基础路径时，HTTP 路径及查询不能重复或丢失。

## Tasks

### 1. 后端环境、绑定编译与任务快照
- [x] 加密环境表及项目作用域 CRUD/OCC/软删除，并接入备份恢复及测试表前缀。
- [x] 先写定向回归，再实现绑定编译、导入保留、Plan requires_environment。
- [x] 任务事务内解析环境、注入运行属性、固定环境快照，保护属性覆盖及幂等重试。
- [x] API debug 接入项目环境；增加相关 Go 单元与隔离 MySQL 集成验证。

### 2. 页面统一环境、流程预览与运行选择
- [x] 项目环境管理页面/面板和共享选择数据，不在 LocalStorage 保存变量值。
- [x] API 与编排使用同一项目环境，兼容既有集合环境的显式导入。
- [x] 流程绑定开关、新流程默认绑定、预览切换不固化 host 或基础路径。
- [x] 创建任务明确选择环境，历史/详情显示实际环境快照，按相关文件定向测试。

### 3. MCP 与目录
- [x] 环境 CRUD 工具及 submit_console_task/debug_api_request 环境参数。
- [x] 更新管理端工具目录、中文说明及前端测试 fixture，验证隔离和参数转发。

### 4. 集成与发布
- [x] 独立复核新变更，修复重要问题；运行相关 Go/Vue/MCP 测试。
- [x] 备份数据库、状态和当前镜像；构建 Go/Vue，校验清单，仅更新 manager。
- [x] 浏览器验证环境管理、预览、任务显式选择和历史快照。
- [x] 用 Docker 内部自测目标验证同一脚本两套环境及原始 JTL；测试后关闭自测目标。
- [x] 保留用户已有环境和任务，交付实际验证结果。


## 实际验收（2026-11-06）

- 本会话统一集成发布，保留另一会话交付的节点名称与两项布局调整。
- Vue 与 Go linux/amd64 生产构建通过；只更新 manager。发布前已备份数据库、源码与原镜像。
- 环境相关前端 8 文件 89 项、空接口引用 2 文件 25 项、环境选择刷新 1 文件 12 项定向回归通过；交接布局 2 文件 10 项通过。后端去重 15 项 MySQL 专项、空接口引用专项及相关 race 通过。
- 独立审查发现的保存并发/重试、变量绑定范围、同名环境、有效环境导入、运行参数预算、敏感日志与快照静态加密问题均闭环。浏览器发现的空接口引用和选择刷新竞态已修复并复测。
- 同一不可变脚本 SHA256 `7ae0a4716d23af48c3a0719bf55896deff7b900945cb5b6cd3011963d4b79ee8`，两个内部环境各执行 18 条请求，均来自 6 个节点且零失败；JTL URL 和变量、原始三文件大小/SHA256、MinIO归档及幂等重试均一致。任务提交后更新环境，仍使用原快照。
- 浏览器验证环境新增/保存、接口管理与流程 Scalar 服务器切换、Master 转发 200、节点编辑保存、JMX 仅保留运行属性引用、任务环境不预选/缺省拦截、历史快照。页面身份、非空、无框架错误及 console 检查通过；环境表单在 900px 无页面横向溢出。
- 另一会话两项布局在部署后复核：左右请求/响应标题均 44px 且顶部相同，流程查看/编辑名称与版本垂直中心一致。
- 线上 10 个常驻容器健康、6 worker/2 controller、队列空闲；MCP 78 个工具，旧直接执行工具缺席。自测目标已关闭，未向业务地址发压。
- 最终源清单 SHA256：`676c2b2eae4b17545ca04978919dd8c9ad504eb51f71abc0850b971205adbd29`。
- 发布与运行验证日志：`/tmp/jmeter-project-env-release-verified.log`；双环境执行：`/tmp/jmeter-project-env-live-smoke.log`；页面证据：`/tmp/jmeter-project-env-qa/`。
