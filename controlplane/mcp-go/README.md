# JMeter MCP 接入

master 自研 MCP 适配使用 Go，第三方 `jmeter-mcp-server@0.4.5` 保留官方 Node 实现作为 stdio 子进程。Go 开放项目、流程、脚本、接口、数据集、集群、控制台任务和结构化报告工具；所有执行复用现有 manager 队列、所选集群控制端和关联 worker。上游单机执行及上游报告工具不开放，`execute_test_plan` 无法列出或调用；报告工具使用本平台已经归档的原件与固定版本。

- 地址：`https://pressure.example.test/mcp`，Streamable HTTP、Bearer 认证。
- 容器内监听：`0.0.0.0:8081`，管理 API 为 `127.0.0.1:8080`。
- 凭据：`JMETER_ADMIN_TOKEN_FILE` 指向已有私有令牌文件；管理 API 凭据可由 `JMETER_MANAGER_TOKEN_FILE` 单独指定，工具不返回令牌。
- 草稿：`JMETER_MCP_WORKSPACE=/state/jmeter-mcp`；Node 保持原格式，Go 的 `draft-projects.json` 持久化项目归属。首次升级把已有草稿归入 `project-default`。后续未登记草稿不对任何项目开放；索引损坏时拒绝启动，不能删除索引来恢复访问。备份必须同时保留草稿与索引。
- 文件导入：`import_test_plan` 仅接受工作区 `imports/<projectId>/` 下的绝对路径、普通 JMX 文件和不超过 5 MiB 的内容，拒绝越界符号链接。共享 `/plans` 快照不作为草稿导入目录。远程客户端上传或读取脚本使用文本工具 `upload_script` / `download_script`。
- 生命周期：Go manager 监管 MCP 适配；适配监管上游 Node，退出时清理子进程。上游异常退出会触发适配退出及重启。

除 `list_projects` 和 `create_project` 外，所有工具均要求显式 `projectId`。每次管理 API 请求带 `X-Project-ID`；管理端校验对象归属、版本冲突、归档状态和关联关系。上游草稿列表、读取、编辑、导入与发布也按持久化归属隔离。每次上游编排调用都先向管理端确认项目存在；已归档项目仍可列出、读取及导出自己的草稿，创建、导入和修改在调用官方 Node 前拒绝。管理端不可用或项目状态无效时拒绝该调用，不以本地归属索引代替项目状态；`list_projects` 和 `create_project` 仍可独立调用。

先用 `list_projects` / `create_project` 取得项目标识。项目设置由 `get_project` / `update_project` 管理；`archive_project` 需要当前 `revision`，保留历史并禁止新增执行任务。

项目接口展示插件由 `list_interface_plugins(projectId)` 读取项目自定义插件元数据、安装状态和独立 `revision`（首次0）。目录不含代码正文；`get_interface_plugin(projectId,pluginId)` 读取完整自定义定义。`create_interface_plugin(projectId,revision,definition,installed)` 创建插件并返回 `created_id`，`update_interface_plugin(projectId,pluginId,revision,definition)` 更新完整定义，`delete_interface_plugin(projectId,pluginId,revision)` 删除自定义定义并移除安装状态。`install_interface_plugin` / `uninstall_interface_plugin` 接受 `projectId,pluginId,revision`，只改变安装偏好。每次实际变更增加插件版本，重复保存或安装保持版本，过期版本409；版本独立于项目名称/描述，归档项目只读。删除或卸载不重写集合与历史引用，展示回退原文。新项目目录为空，平台不提供业务插件实现。未知历史安装标识只作为历史数据保留，不出现在可用目录中，读取不会重写项目状态。

自定义定义有 `kind:config|code`、`name`、`version`、可选 `description`、`protocols:["websocket"]` 和可选 `id:custom-...`。配置式定义提供 `parser:{format:json|socketio|text,rootPath,fields:{text,sender,title,type,image,link}}` 以及 `presentation:{layout:chat|cards|compact,accent:#RRGGBB}`；安全字段路径支持 `$`、点字段、数字下标和无转义的引号括号字段，最多256字符/16层，禁止原型字段。代码式定义提供 `code:{language:"javascript",source}`，展示设置可省略。每项目最多100个定义，单定义64KiB，代码60KiB。自定义能力固定为 `parse/render`；定义加密保存，元数据包含 `kind/renderer/content_sha256`，管理端不执行代码、不拉取依赖。浏览器在受限沙箱解析已有消息并接收清洗后的消息结构，不能授予发送、网络或外部运行时权限。

`list_interface_protocols(projectId)` 读取 `GET /api/interface-protocols` 能力目录。HTTP/WebSocket支持现有调试与工作流；RPC、gRPC、Protobuf及二进制消息仅保留描述扩展，不启动对应执行。OpenAPI操作可保存 `x-pressure-protocol:{protocol:"grpc",serialization:"protobuf",rpc:{service:"example.Greeter",method:"SayHello",streaming:"unary"},protobuf:{schema_ref:"schema-local",descriptor_ref:"descriptor-local",message_type:"example.Request"}}`。`rpc`仅适用于`rpc/grpc`，streaming可为`unary/client/server/bidirectional`；`protobuf`仅适用于protobuf序列化，引用是惰性标识或指针，不会读取远程资源。操作元数据覆盖文档默认值；旧 `x-pressure-ws` 操作继续按WebSocket处理。预留协议可保存和原样导出，但HTTP代理、集合调试、流程引用/运行参数及Postman转换均拒绝按HTTP执行。当前适配版本为 `0.4.5-manager-go.11`，实时目录共134个工具。

项目统一环境由以下工具管理，全部要求 `projectId`，通过同一项目请求头调用 `/api/environments`：

| 能力 | 工具与参数 |
| --- | --- |
| 环境列表 | `list_project_environments`；返回 `items`、`total`、`project_id` |
| 创建环境 | `create_project_environment`；`name`、`base_host`、可选 `variables: [{name,value}]` |
| 读取环境 | `get_project_environment`；`environmentId` |
| 更新环境 | `update_project_environment`；`environmentId`、当前 `revision`、`name`、`base_host`、可选 `variables` |
| 删除环境 | `delete_project_environment`；`environmentId`，软删除并保留既有任务快照 |

环境标识为 `environment-<32位十六进制>`。`base_host` 是完整 HTTP(S) 地址，支持端口、IPv6 和基础路径，不允许用户凭据、查询参数或片段。自定义变量列表不包含 `base_host`，变量名称不能重复；变量值为字符串。版本冲突、跨项目、归档项目及已删除环境由管理端统一校验。环境详情返回标识、项目归属、名称、基础地址、变量、版本与创建/更新时间。

项目与集合变量的 `value` 可为固定文本，或完整规则 `{{$random.uuid}}`、`{{$random.hex(32)}}`、`{{$random.alnum(16)}}`、`{{$random.digits(12)}}`、`{{$auto.client_ip}}`、`{{$auto.timestamp_ms}}`。变量名任意，例如 `{"name":"source_addr","value":"{{$auto.client_ip}}"}` 和 `{"name":"issued_at","value":"{{$auto.timestamp_ms}}"}`；平台不会按变量名自动赋值。UUID 为 v4；随机文本长度为1–256的十进制整数，禁止前导零、空格和正负号。自动IP取本次操作的实际调用方请求来源，时间为当前毫秒时间戳字符串；来源不可用时使用IP规则会明确报错。规则必须占满整个字符串，不能嵌入其他文本、脚本或其他规则语法。规则原文持久化，按有效合并结果每次 WS 连接、HTTP 调试或新任务快照解析一次；同次操作重复引用同名规则保持一致，任务重试复用冻结快照。集合覆盖优先于项目变量；不同集合采用不同规则时仍保持各自覆盖作用域。快照内的生成值和 `generation_rule` 可追溯，所有节点/线程共享冻结值，逐线程身份使用数据集。

MCP只采用自身HTTP连接的TCP peer作为调用方IP，忽略用户提供的 `X-Forwarded-For`、`Forwarded` 和内部caller元信息。存在反向代理时，来源仍是代理peer，不能据此宣称已取得终端公网地址。适配通过共享Go协议向管理API逐请求签名传递来源，签名绑定method、完整URI、项目和两分钟时间窗口；它不能替代管理端正常认证。非HTTP编排上下文显式传递“来源不可用”，不会将内部管理API的回环地址填为调用方IP。HTTP与multipart上传路径使用同一私有上下文，不读取可伪造的SDK请求头作为来源。

项目服务地址和其他运行值使用同一环境的自定义 `variables`，例如 `{"name":"ws_host","value":"wss://service.example.test"}`。更新环境时携带当前完整变量列表，避免覆盖其他配置；集合可按环境 ID 覆盖同名变量，`variables:[]` 清空该环境全部集合覆盖并恢复继承项目环境。

WebSocket 的 `socketio` 选项仅处理传统 Socket.IO 动态握手、`{socket_id}` 替换和 `2::` 传输心跳。管理端调试连接在 `state:open` 返回本次可选 `socket_id`，不保存为项目配置。流程运行时保留 `ser`、`now`、`message_id`、`last_ser` 和本次传统 Socket.IO 握手的 `socket_id`（建连生成、关闭清除）；其他名称均为普通项目或线程变量。请求响应保留 `raw` 与通用 JSON `frame`，文本/正则/JSONPath 匹配和通用 JSON 提取器可由项目配置；平台不识别应用命令、不自动发送业务回执、不派生会话字段。可选心跳仅按配置定时发送，业务确认应由显式接收节点和断言验证。诊断可通过 `websocket_trace_events=true` 启用，只记录有界传输阶段、计数与运行时生成标识，不记录消息、匹配条件或用户值。

流程工具直接使用管理端 graph 与修订版本，不需要先创建上游测试草稿：

| 能力 | 工具 |
| --- | --- |
| 保存流程列表、创建、读取、更新、软删除 | `list_workflows`、`create_workflow`、`get_workflow`、`update_workflow`、`delete_workflow` |
| JMX 文本导入、graph 编译预览 | `import_workflow`、`preview_workflow` |
| 发布不可变脚本、读取发布关系 | `publish_workflow`、`list_workflow_plans` |
| 历史修订列表、指定修订内容 | `list_workflow_versions`、`get_workflow_version` |

创建/更新传完整 `graph`，更新和发布传当前 `revision`；冲突后重新读取。预览返回 `xml`，不保存；发布返回不可变脚本关系，两者均不提交压测。流程默认支持5000节点、10000连线，请求体默认32MiB、可配置至64MiB；MCP仅给携带流程的工具放宽传输上限，其余调用保持6MiB。上游草稿导入、脚本上传及CSV文本上传仍各自限制5MiB。

编排草稿后，通过 `publish_test_plan` 发布不可变 JMX，或直接调用 `upload_script` 提供 `name`、`content` 和可选 `description`；返回 `managerPlanId`。`download_script` 必须且只能指定 `managerPlanId` 或 `workflowId`：前者返回 `/api/plans/:id/content` 的精确不可变原文，后者返回流程当前导出；响应 `content` 保留换行与 Unicode。

使用 `submit_console_task` 提交任务，参数完整对应控制台：

```json
{
  "projectId": "project-default",
  "clusterId": "cluster-<从 get_cluster_status 获取的稳定ID>",
  "managerPlanId": "plan-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "environmentId": "environment-cccccccccccccccccccccccccccccccc",
  "name": "接口回归压测",
  "workers": ["worker-1", "worker-2"],
  "properties": {"loops": "5"},
  "notes": "本轮执行说明",
  "idempotencyKey": "stable-request-id"
}
```

该工具只调用相同的 `POST /api/runs`，发布版本映射为 `plan_id`，项目环境标识映射为 `environment_id`，幂等标识映射为 `Idempotency-Key`。所有新任务必须明确提供 `environmentId`，缺少时由 schema 和管理端拒绝。管理端在提交时固定环境快照，任务响应中的 `environment_id` 和 `environment_snapshot` 原样返回。必须传递 `clusterId`，映射为 `cluster_id`；只使用该集群控制器。省略节点时保持 `workers` 字段省略，由管理端在幂等检查之后固定所选集群全部关联 worker，每个节点执行完整 JMX；同一请求重试必须复用标识和参数，并复用原任务快照。全局属性不能覆盖 `base_host` 和平台保留运行参数。`list_executions`、`get_execution_status`、`stop_execution`、`get_execution_log` 和 `list_execution_artifacts` 均要求同一项目，保留有界日志、正常停止/立即终止及原始 JTL/日志归档查询。

复制已有任务先调用 `get_execution_copy_config(executionId)` 读取可编辑配置，再提交新任务；可传 `copySourceExecutionId` 和 `copyEnvironmentSnapshot:true` 选择历史环境快照，后者要求同时提供来源任务。每个新任务使用新的 `idempotencyKey`，仅同一提交重试复用旧键。原始文件清单以管理端实际返回为准，包含 JTL、执行日志及按节点采集的线程观察原件；历史不可变文件回执继续按任务归属读取。

结构化报告工具均要求 `projectId`，详情使用 `reportId` 固定版本，按任务读取最新报告使用 `executionId`：

| 能力 | 工具 |
| --- | --- |
| 搜索报告、固定版本、任务最新报告 | `list_pressure_reports`、`get_pressure_report`、`get_latest_pressure_report` |
| 生成或重新解析 | `generate_pressure_report`（`executionId`、`idempotencyKey`；可选 `rebuild`、`reportTargets`、`analysisOptions`） |
| 节点、步骤、问题、日志、性能分析 | `list_pressure_report_nodes`、`list_pressure_report_steps`、`list_pressure_report_issues`、`list_pressure_report_logs`、`get_pressure_report_analytics` |
| 实际请求/响应/断言留样 | `list_pressure_report_samples`（必须提供 `stepKey`，可选 `nodeId`） |
| 两版本对比、离线HTML下载信息 | `compare_pressure_reports`、`export_pressure_report` |

读取报告不隐式生成。步骤支持 `failedOnly`、`sort:latency|failure_rate|failures`、`limit`/`offset`；节点与步骤筛选使用 `nodeId`/`stepKey`。`reportTargets` 和 `analysisOptions` 内部字段沿用API蛇形字段，完整口径、阈值、留样范围和历史缺失见 [报告契约](../../PRESSURE-REPORTS.md)。留样的 `null`、空内容、未采集断言、无断言项及实际断言结果分开保留。

例如只读取某个接口的失败统计与留样：

```json
{"name":"list_pressure_report_steps","arguments":{"projectId":"project-default","reportId":"pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","failedOnly":true,"sort":"failure_rate","limit":20}}
{"name":"list_pressure_report_samples","arguments":{"projectId":"project-default","reportId":"pressure-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stepKey":"从步骤列表返回的step_key","nodeId":"worker-1","limit":20}}
```

外部中间件与JVM报告数据写入：

- `get_pressure_report_monitoring(projectId,reportId,revision?,component?,search?,limit?,offset?)`：省略版本读取最新监控补充，指定正整数读取历史版本。默认每页20实例、偏移0，最多200实例；按组件和实例名称筛选。返回 `total`（筛选后数量）、`instance_count`（全量实例数）、`component_counts` 及当前页 `entries`；无数据版本为0。
- `write_pressure_report_middleware`：`component` 选择 `es`、`mysql`、`redis` 或 `kafka`。`write_pressure_report_jvm` 固定写入 `jvm`，不用传 `component`。
- 上百个JVM使用 `write_pressure_report_jvm_batch`：必填 `projectId`、`reportId`、当前监控 `revision`、`idempotencyKey`、`entries`。每条entry含 `instance`、`source`、`window` 和监控证据，不传 `component` 或条目级 `revision`。一批1至200实例、4MiB正文、60000总点；每实例仍遵循单写限制。全批成功才产生一个新监控补充版本，任一条无效则整批不写。同批不能重复实例，其他组件和未涉及实例保留。
- 两个写工具均必填 `projectId`、`reportId`、当前监控 `revision`（首次0）、`idempotencyKey`、`instance`、`source:{name,reference?,collected_at?}`、`window:{start,end}`；另传 `metrics`、`series`、`findings` 中至少一类非空证据，可附 `summary` 和 `health:UNKNOWN|NORMAL|WARNING|CRITICAL`。
- 只向本项目READY报告写入。采集时间必须与压测时间相交，缺少运行时间时保留“未确认”警告；先从报告和采集证据取得真实时间，不使用当前时间替代历史采集范围。实例是被测应用/中间件实例，不是默认指压测worker。
- 每次仅替换同组件、同实例条目并产生独立监控补充版本，原始JTL统计、验收结论和报告版本保持不变。并发冲突后重新读取监控revision再用新键提交；网络重试则复用原键、原revision及原参数，返回首次结果。
- 指标 `value:0` 表示实际零值，`null` 表示缺少数据；趋势按实际时间点保存，`null` 保留断点。`health` 和发现来自外部证据，不自动变成压测达标判断。工具不执行采集、脚本或业务压测。

写入示例（标识和采集时间需要替换为真实报告及证据）：

```json
{
  "name": "write_pressure_report_jvm",
  "arguments": {
    "projectId": "project-example",
    "reportId": "pressure-00000000000000000000000000000000",
    "revision": 0,
    "idempotencyKey": "jvm-capture-unique-request",
    "instance": "application-1",
    "source": {"name": "Prometheus", "reference": "monitoring-snapshot-1"},
    "window": {"start": "2026-11-07T01:00:00Z", "end": "2026-11-07T01:01:00Z"},
    "health": "UNKNOWN",
    "metrics": [
      {"key": "heap_used_percent", "label": "堆内存使用率", "value": 71.2, "unit": "%"},
      {"key": "full_gc_count", "label": "Full GC 次数", "value": 0, "unit": "次"}
    ]
  }
}
```

单次单实例正文最多512KiB：最多64指标、12条序列、每条2000点/合计6000点、32条发现；最新快照最多1000实例/8MiB，每报告最多128个监控补充版本，超限明确拒绝。批量响应只返回 `written_count`、`instance_count`、`written_entries`（实例标识）及新版本，不重复回传百个实例的时序。大量实例使用批量工具以减少版本数和快照重复。

例如读取第201至220个JVM：

```json
{"name":"get_pressure_report_monitoring","arguments":{"projectId":"project-example","reportId":"pressure-00000000000000000000000000000000","component":"jvm","limit":20,"offset":200}}
```

来源引用只保存脱敏文字，不访问其地址。在线总览/“中间件与JVM”和离线HTML均按组件搜索与每页5条选择实例，只展开选中的一份详情和趋势；完整数据仍保存在版本快照中，分页不代表缺少数据。离线HTML内嵌导出时的完整快照及版本号，不访问网络；新解析版本不自动继承旧报告的外部数据。

接口工具经 manager 的集合 API 工作：

| 能力 | 工具 |
| --- | --- |
| 集合列表、创建、读取、保存、删除 | `list_api_collections`、`create_api_collection`、`get_api_collection`、`update_api_collection`、`delete_api_collection` |
| cURL、Postman、Apifox、OpenAPI 文本导入 | `import_api_collection`，参数 `format`、`content`、可选 `name`；cURL 只解析文本 |
| OpenAPI 或 Postman 原文导出 | `export_api_collection`，参数 `collectionId`、`format` |
| 新增或修改完整 OpenAPI 请求 | `create_api_request`、`update_api_request`；`revision`、`path`、`method`、`operation`；路径/方法变更时传 `previous_path`、`previous_method` |
| 经管理端调试接口 | `debug_api_request`；`path`、`method`、必填 `target_source`、可选 `environmentId` 和 `variables`；项目环境模式必须提供环境标识，文档服务模式禁止提供环境标识 |
| 集合环境变量覆盖 | `set_api_environments`；`collectionId`、`revision`、稳定的项目 `environmentId`、`variables:[{name,value}]`；仅替换此 ID 的覆盖；`variables:[]` 清空覆盖并恢复继承项目同名变量，自定义变量可为空字符串，`base_host` 被拒绝 |
| 集合设置 | `update_api_collection_settings`；`revision`、可选 `name`、`description`、`servers`、`client_state` |
| 历史版本列表、固定版本 | `list_api_collection_versions`、`get_api_collection_version`（`collectionId`、`revision`） |

创建/保存集合使用完整 `document` 和可选 `client_state`，保存时必须提供当前 `revision`，保留认证、环境、模型、示例及扩展字段。全部业务工具以实时 `tools/list` 为准，前端中文目录使用同一工具表 fixture，不固定工具数量。

MCP 客户端应配置实际部署地址及私有认证。客户端需重连 MCP 或开启新聊天加载新版工具；本说明中的示例地址和版本不表示已经部署。

本地验证：

```sh
go test -race ./...
go vet ./...
go build -o /tmp/jmeter-mcp .
cd ../mcp
JMETER_MCP_TEST_BINARY=/tmp/jmeter-mcp npm test
```

Go 集成测试启动真实官方 Node authoring 进程，校验项目归属、重启持久化、协议工具表和前端 fixture。仅在有意更新实时 schema 时于 `mcp-go` 运行 `go test -run TestLiveToolCatalog -update-mcp-fixture`，随后运行前端 MCP 与中文化测试。相邻 `mcp/` 目录只保存锁定第三方依赖与协议测试，不承载自研运行服务。

集合环境继承项目环境列表和服务器地址。调试必须显式选择 `target_source`：`project_environment` 同时提供 `environmentId`，按项目变量 → 集合对应环境覆盖 → 本次显式 `variables` 合并，地址固定为项目环境地址；`document_server` 使用 OpenAPI 文档服务地址与本次显式变量，禁止同时提供环境标识。旧 `environment` 调试参数已拒绝，导入的历史活动环境不自动选择执行目标。

集合 JSON 的稳定存储契约是 `client_state.project_environment_overrides[environmentId]={variables:[{name,value}]}`，由集合 AES-GCM/revision/历史及导入导出机制保留。其他项目、删除或未知的 ID 被拒绝。项目环境删除同一事务清理当前集合并增加集合版本；旧版本保留且无法重新保存已删 ID。原 `x-scalar-environments` 和 `workspace_environments` 仅作历史兼容，界面不按旧名称自动迁移，需在正确项目环境中手动录入自定义覆盖，不采用旧地址。

Redis任务附加参数：`datasetSelections:[{node_id,dataset_ids:[],distribution:"copy"|"shard"}]` 和 `timeoutSeconds`（10至86400，默认3600）。全部参数仅映射到管理端同一提交接口；不提供直接代理执行绕过。任务响应保留 `datasets`、`dispatch`、`callback_delivery` 和冻结的环境/集群/文件元数据。

数据文件工具均要求 `projectId`：`list_workflow_datasets(workflowId)`、`list_plan_datasets(managerPlanId)`、`upload_dataset(workflowId,nodeId,name,content)`、`generate_dataset(workflowId,nodeId,rows,columns,name?,seed?)`、`get_dataset(datasetId)`、`preview_dataset(datasetId,limit?)`、`download_dataset(datasetId)`、`delete_dataset(datasetId)`。上传CSV文本最多5MiB；大文件用控制台/CLI流式上传。下载工具返回元数据和认证下载URL，不把大CSV内容塞入MCP消息。删除为软删除，既有任务的不可变快照不变。

`list_datasets` 提供全项目分页检索（`page`、`pageSize`、`q`、`workflowId`、`nodeId`；节点筛选须同时选择流程）；`get_dataset_catalog` 返回有效流程和CSV节点目录。`download_dataset_template(workflowId,nodeId,graph?)` 按当前列和分隔符动态返回仅表头的UTF-8 BOM CSV（`filename`、`content`、`columns`、`delimiter`），可使用未保存graph快照；仍校验已保存流程的项目归属，不读取数据内容或环境值。对应只读管理API为 `POST /api/workflows/:id/datasets/template`，JSON字段为 `node_id`、可选 `graph`。

`download_dataset` 与 `export_pressure_report` 返回 `download_path`、`download_url`、`requires_authentication:true` 和项目请求头，不含令牌。默认URL相对于控制台站点，客户端用控制台基础地址解析并携带已有Bearer认证，或在已登录浏览器中打开；不能作为匿名公开链接使用。可通过 `JMETER_MANAGER_PUBLIC_URL` 显式配置外部HTTP(S)基础地址，禁止凭据、查询与片段；不会暴露容器内部管理API地址。

`get_cluster_status` 读取全局集群，`update_cluster(clusterId,revision,name,workerIds?,callbackUrl?)` 编辑同一配置。仍显式传 `projectId` 作为调用上下文，但集群变更影响所有项目。省略可选字段保持原值，空节点数组解除关联，空回调地址恢复默认；管理端拒绝忙碌集群的节点变更。

集群 `callback_url` 是执行器回传控制台结果与文件URL的单一Webhook端点；裸控制台地址补 `/api/runner/callback`，空值使用平台默认。管理端不会再发送第二轮业务通知。JSON提取变量和CSV变量遵循JMeter线程组、父作用域和请求顺序，后续请求 `${token}` 不会被同名集合环境覆盖；显式 `${__P(token,)}` 读取全局属性。

## 删除任务与报告

六个写工具均要求显式 `projectId`，沿用页面的删除接口和保护：

| 工具 | 必填参数（除 projectId） | 范围 |
| --- | --- | --- |
| `delete_execution` | `executionId` | 一条已结束任务及关联报告、产物 |
| `delete_executions` | `executionIds` | 1至100个不同的已结束任务，原子处理 |
| `clear_executions` | `confirm: true` | 当前项目全部已结束任务，未结束任务保留 |
| `delete_pressure_report` | `reportId` | 一个报告版本及其分析/监控/明细 |
| `delete_pressure_reports` | `reportIds` | 1至100个不同报告版本，原子处理 |
| `clear_pressure_reports` | `confirm: true` | 当前项目全部报告 |

清空不受列表筛选或分页限制；不接受跨项目通配参数。单条/批量任务包含未结束记录时整次拒绝，跨项目或不存在记录同样整批拒绝。报告独立删除保留任务、原始 JTL/日志及其他报告，任务删除会级联关联报告与产物。归档项目禁止删除。全部工具标记为 destructive，返回管理端的 `deleted_count`、`skipped_count`、`cleanup_pending`（报告单独删除为0）；物理产物可在后台重试清理。
