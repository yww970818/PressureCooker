# JMeter 项目、接口复用与 MCP 扩展设计

## 目标和约束

实现本轮 11 项需求：项目管理；接口集合、流程、发布脚本、任务与历史按项目隔离；流程引用接口；品牌下项目切换；HTTP 参数使用原生 Scalar 编辑；脚本库更名流程编排；MCP 脚本上传下载；停用原执行工具并完整接入控制台任务；MCP 接口和环境管理；集合设置；cURL、Apifox、Postman 导入。

沿用 Go 后端、Vue 前端、MySQL、LogicFlow 与 @scalar/api-client。保留六执行节点、双控制端、单管理端及 MinIO；仅归档原始 JTL 和日志。既有历史 ID、密文、源文件、精确脚本版本不变。

## 方案与实现决定

采用单实例内逻辑项目，所有数据 API 在服务端校验项目归属；前端过滤无法保护按 ID 的读写，因此不采用。暂不引入用户角色或多租户部署。已有数据迁入 `project-default`（默认项目）。项目支持创建、修改和归档；归档保留历史，禁止新增运行，不做级联物理删除。

项目请求以 `X-Project-ID` 表示，缺省兼容默认项目；MCP 所有业务工具显式带 `projectId`。服务端关联对象校验同一项目。项目切换销毁前一项目页面实例、清空缓存，请求保持发起时项目上下文；有未保存修改时提示保存或放弃。项目不是权限边界替代品，仍使用现有登录与 CSRF。

流程引用保存集合 ID、集合版本、请求路径/方法及请求快照。HTTP 参数通过现有原生 Scalar Operation 组件修改，确认后映射 JMeter HTTP、头、查询和请求体。引用可显式刷新；既有已发布 JMX 永不随集合更新。不能可靠映射的请求给出明确错误，不静默丢参数。跨项目引用由后端拒绝。

接口统一保存为现有 OpenAPI + Scalar 客户端状态，继续加密存储。cURL 仅解析文本，禁止执行 shell；Postman 集合 v2/v2.1、Apifox 导出的 Postman/OpenAPI JSON 导入；原生 Apifox 项目导出若支持须明确识别结构与拒绝不支持内容。环境与认证转换保留，不能转换字段提示，不静默损失。导出提供 OpenAPI JSON 和 Postman；版本冲突使用现有 revision。

## HTTP 契约

- `GET/POST /api/projects`；`GET/PUT /api/projects/:id`；`POST /api/projects/:id/archive`。项目对象 `{id,name,description,revision,archived,created_at,updated_at}`。
- 现有 plans、runs、reports、workflows、api-collections API 全部接受 `X-Project-ID`，响应含 `project_id`。缺省默认项目兼容旧客户端；跨项目对象返回 404。列表先筛选再分页，幂等键按项目隔离。
- `POST /api/api-collections/import` `{format:'auto'|'curl'|'postman'|'apifox'|'openapi',content,name?}` -> 新集合；`GET /api/api-collections/:id/export?format=openapi|postman` -> 文本或 JSON 文档。
- `PUT /api/api-collections/:id/requests` `{revision,path,method,operation,previous_path?,previous_method?}` -> 更新集合；`POST .../debug` `{path,method,environment?,variables?}` -> 有界响应 `{status,headers,body,duration_ms,...}`。
- `PUT /api/api-collections/:id/environments` `{revision,environments,active_environment?}` -> 更新集合；`PUT .../settings` `{revision,name?,description?,servers?,client_state?}` -> 更新集合。
- 引用元数据为 HTTP 节点 `properties.api_reference={collection_id,revision,path,method}`，当前 config 即执行快照。后端校验集合归属，发布按快照编译。

## MCP 契约

移除 `execute_test_plan` 且禁止上游直接运行工具。新增 `submit_console_task`，参数与控制台一致（projectId、managerPlanId、name、workers、properties、notes、idempotencyKey），调用相同 `/api/runs`；保留任务查询、停止、日志与归档工具并按项目校验。脚本上传接收 name/content，下载返回精确不可变 JMX 或流程当前导出。项目列表/创建/设置；集合列表/创建/修改/删除/导入/导出；接口新增/修改/调试；环境设置全部经管理端 API，禁止独立旁路执行。

## 验收

真实 MySQL 隔离前缀测试迁移、默认项目、跨项目按 ID 读写、先过滤后分页、跨项目关联、幂等、归档保留历史、备份恢复。导入测试含引号、重复查询、认证、变量、禁 shell 注入、错误结构。MCP 校验真实工具表不含旧执行工具，任务参数完整转发。浏览器验证项目切换、未保存保护、三种导入、流程引用和 Scalar 编辑、任务历史、中文与主题。只对内部自测服务运行六节点验证；部署前新备份，部署后十容器健康。
