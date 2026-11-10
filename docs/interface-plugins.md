# 接口消息展示插件

接口插件由项目自行创建、安装和选择，新项目的插件目录为空。在“接口管理 → 接口插件”中维护配置型或 JavaScript 代码型插件，安装后可在 WebSocket 收发记录中选择；选择结果随接口配置保存。连接、模板、手动发送、定时消息和 JSON 提取由通用调试器处理。

插件只解析和展示当前页面已有的消息帧。项目消息的结构与含义可在插件中定制；插件不持有连接，不发送报文，不回填业务草稿，也不修改环境或提取结果。需要发送的内容由用户编写为消息或模板，再点击原“发送消息”按钮。

原始帧最多保留 300 条，展示失败、代码超时、定义不可用或原引用未安装时回退原文，连接与发送不受展示解析失败影响。卸载或删除插件不改写集合及历史版本中的引用。目录单独列出没有可用定义的失效安装 ID，可明确删除失效引用或卸载；不能重新安装或执行这些 ID，读取目录也不会自动清除它们。可随时切换原文查看实际收发内容。

项目和接口复用见[项目与接口复用](项目与接口复用.md)，连接与流程操作见[流程画布与接口管理](流程画布与接口管理.md)。

## 创建与管理

点击“新增插件”，选择配置型或代码型，填写名称、版本、说明和展示样式。布局包括对话、卡片和紧凑列表，主题色使用 `#RRGGBB`。新建时可选择“创建后安装到当前项目”，也可先保存定义再安装。插件支持编辑、导入、导出、删除和安装/卸载；归档项目只能读取和导出。

“本地预览”使用一条样本及收发方向展示结果，不发送消息，也不保存预览样本和结果。导入 `.json` 或 `.js` 只建立未保存草稿：JSON 可为完整定义或导出包，原 ID 会被移除；JavaScript 文件形成代码型草稿。保存只提交插件定义，安装状态与定义均由项目成员共享。

### 配置型插件

`parser.format` 支持 `json`、`socketio`、`text`。`socketio` 仅对传统协议的 `3:::` 文本前缀去除后解析 JSON；消息根路径支持 `$`、`$.messages` 等形式，根结果是数组时逐条映射。字段路径相对于每条消息，支持字段、数组下标和引号括号字段，不支持通配符、表达式、脚本或原型字段。

留空字段不展示；可映射正文、发送人、标题、消息类型、图片和链接。`text` 模式可将正文路径设为 `$`。以下完整定义可直接导入：

```json
{
  "kind": "config",
  "name": "JSON 消息",
  "version": "1.0.0",
  "description": "展示 messages 数组中的正文与发送人",
  "protocols": ["websocket"],
  "parser": {
    "format": "json",
    "rootPath": "$.messages",
    "fields": {
      "text": "$.text",
      "sender": "$.sender",
      "title": "",
      "type": "",
      "image": "",
      "link": ""
    }
  },
  "presentation": {"layout": "chat", "accent": "#3b82f6"}
}
```

### JavaScript 代码型插件

定义 `function parse(frame)`，返回一个消息对象或消息数组。输入是已有帧的 `data`、`direction`（`sent` / `received`）、`timestamp_ms` 和 `binary`；当前只解析文本帧。输出支持 `text`、`sender`、`title`、可选 `kind`（`message` / `system` / `raw`）以及 `blocks`：

```javascript
function parse(frame) {
  const data = JSON.parse(frame.data);
  return [{
    text: String(data.message ?? ''),
    sender: frame.direction === 'sent' ? '发送方' : String(data.sender ?? '接收方'),
    blocks: []
  }];
}
```

完整定义使用 `kind: "code"`、`code: {"language":"javascript","source":"..."}`，不包含 `parser`；名称、版本、协议和展示设置与配置型相同。内容块支持 `text`、`image`、`link`、`file`。文本块可添加 `role: "details"`，将账号、标识或诊断信息放入默认折叠的“消息详情”，正文仍使用 `text` 或普通文本块。URL 仅接受不含内嵌凭据的 HTTP(S) 地址，图片和文件以可手动打开的链接显示，不自动加载远程内容。

收发记录可按全部、对话、系统通知和未解析筛选，搜索同时匹配原文与插件输出。系统通知采用紧凑行展示；插件返回 `kind: "raw"` 或解析失败时自动展开原文。同一帧含多种消息时保留整帧，含未解析消息的帧归入“未解析”。

代码在隔离 iframe 和 Worker 中运行，主页面和 Go 管理端不直接执行源码。沙箱禁止网络访问，不提供发送、环境写入或浏览器凭据接口，不下载 npm 包。输出经结构化清洗后展示，不执行插件返回的 HTML；语法错误、解析异常、超时、二进制输入或超限结果回退原文。

| 范围 | 限制 |
| --- | --- |
| 每项目自定义插件 | 最多 100 个 |
| 完整定义 / JavaScript 源码 | UTF-8 最多 64 KiB / 60 KiB |
| 导入文件 | 最多 128 KiB，定义和代码仍满足各自限制 |
| 配置字段路径 | 最多 256 字节、16 层 |
| 单条输入文本帧 | 最多 256 KiB |
| 每帧输出 | 最多 64 条消息，每条最多 64 个内容块，完整输出最多 64 KiB |
| 代码解析预算 | 每次 Worker 执行默认 500 ms，另有初始化和响应超时保护 |

## 项目接口与版本

接口使用平台认证、CSRF 和 `X-Project-ID` 项目作用域。定义按项目上下文加密保存，列表只返回元数据，完整定义单独读取；备份恢复须保留原 `session-key`。

| 方法与路径 | 正文或响应 |
| --- | --- |
| `GET /api/interface-plugins` | `project_id/revision/items/unavailable`；`unavailable` 列出失效安装 ID，`items` 仅包含有定义的项目插件元数据和安装状态，`kind` 为 `config/code`，列表不含源码 |
| `POST /api/interface-plugins` | `{ "revision": 0, "definition": {...}, "installed": true }`；创建并返回 `created_id`，省略 ID 时生成 `custom-...` |
| `GET /api/interface-plugins/{id}/definition` | `definition/project_id/revision/content_sha256` |
| `PUT /api/interface-plugins/{id}/definition` | `{ "revision": 1, "definition": {...} }`；替换定义，保留安装状态 |
| `PUT /api/interface-plugins/{id}` | `{ "revision": 1, "installed": true }`；安装，`false` 为卸载 |
| `DELETE /api/interface-plugins/{id}` | `{ "revision": 1 }`；删除项目定义及安装状态，也可显式删除失效安装 ID |

定义和安装状态共享独立修订号，首次为 `0`，不改变项目名称/描述的修订号。写入需提供当前插件 `revision`，实际变更后递增；过期版本返回 `409`。冲突后刷新目录并重新打开目标定义，核对其他成员修改后再保存。卸载和删除不重写 `x-pressure-ws.presentation_plugin_id`。

MCP 提供 `list_interface_plugins`、`get_interface_plugin`、`create_interface_plugin`、`update_interface_plugin`、`delete_interface_plugin`、`install_interface_plugin`、`uninstall_interface_plugin`。全部显式传入 `projectId`，写入携带当前 `revision`；完整参数见[MCP 接入](../controlplane/mcp-go/README.md)。导出包格式为：

```json
{
  "format": "pressurecooker.interface-plugin",
  "version": 1,
  "definition": {
    "kind": "code",
    "name": "文本消息",
    "version": "1.0.0",
    "protocols": ["websocket"],
    "code": {
      "language": "javascript",
      "source": "function parse(frame) { return [{ text: frame.data }]; }"
    },
    "presentation": {"layout": "compact", "accent": "#3b82f6"}
  }
}
```

包裹层中的 `definition` 为完整有效定义。导出不包含预览样本、运行时提取值或收发记录。

## 响应提取规则

WebSocket 编辑器支持新增、编辑、删除、启停规则及本地测试。规则保存在 `x-pressure-ws.extraction_rules`；未配置时与显式 `[]` 均表示没有规则。新增和编辑只支持 `type=json`、`source=packet` 的普通 JSON 路径规则：

```json
{
  "id": "result",
  "name": "结果编号",
  "type": "json",
  "enabled": true,
  "source": "packet",
  "path": "$.data.id",
  "variable": "result_id"
}
```

来源为完整普通 JSON 报文，或去除传统 Socket.IO `3:::` 前缀后的 JSON。路径支持字段和数组下标，例如 `$.data[0]["id"]`；不支持通配符、表达式、脚本或原型字段。路径选中数组时，每个非 null 元素成为候选，多项时必须显式选择；标量转为字符串，对象序列化为 JSON，大整数标识保留原值。

输出可在消息模板中用 `{{result_id}}` 引用。调试器保留 `ser`、`now`、`message_id` 和握手生成的 `socket_id`，规则不能覆盖这些运行时名称；其他应用字段没有自动赋值或状态含义。流程执行器另提供最近一次用户发送的 `last_ser`，其余值通过环境、CSV 或显式 JSON 提取器提供。

结果仅当前连接有效，不保存到集合或项目环境。断开、重新连接、修改或停用规则会清除旧结果。默认在新的有效 JSON 响应找不到路径时清除对应旧值；显式配置 `on_unmatched: "keep"` 后，没有来源候选通过筛选时保留旧结果，匹配来源缺失输出字段时仍清除旧值。修改规则后等待下一条响应，不把历史环境值当作当前规则的提取结果。本地测试使用独立预览状态，可填写测试变量 JSON，不发送消息、不更新当前连接，也不保存样本或测试变量。

“来源筛选与嵌套 JSON”提供以下通用选项，均须显式配置，不增加业务默认规则：

- `decode_json: true`：沿路径读取时解码嵌套 JSON 字符串，保持大整数 ID 的精确文本；单次解码最多 16 层，每帧累计解码最多 1 MiB。
- `selectors`：按顺序从上一层候选读取 `path`，数组逐项展开，每层最多 256 项。每层可配置最多 3 个 `alternate_paths` 同时读取其他路径，共用该层匹配条件和父项上下文。`match` 的所有条件必须满足，支持 `equals`、`in`、`exists`（存在且非 null）、`starts_with`。匹配值为 JSON 标量或 `in` 使用的标量数组；字符串中的 `{{name}}` 显式引用当前连接变量，缺少变量时不匹配。
- `outputs`：为同一候选添加输出字段；主输出仍使用 `path/variable`。任一输出缺失时，该候选无效；多项候选须选择后整体回填，避免字段错配。
- 主输出和附加输出可使用 `from: "candidate"`（默认最终候选）、`"parent"`（上一层候选）、`"packet"`（完整报文）。例如子项的问题文本可与父项的消息 ID 一起提取。

```json
{
  "id": "reply", "name": "回复选择", "type": "json", "source": "packet",
  "decode_json": true, "on_unmatched": "keep",
  "selectors": [
    {"path": "$.events", "match": [{"path": "$.kind", "operator": "equals", "value": "reply"}]},
    {"path": "$.choices"}
  ],
  "from": "parent", "path": "$.id", "variable": "reply_id",
  "outputs": [{"path": "$.label", "variable": "choice_label"}]
}
```

| 范围 | 限制 |
| --- | --- |
| 有效提取规则 | 最多 64 条，ID 与输出变量名分别唯一 |
| JSON 路径 | 最多 512 字符、32 层 |
| 输入 JSON | 字符串长度最多 256 × 1024 |
| 数组候选 | 最多 256 项 |
| 单项提取输出 | 字符串长度最多 64 × 1024 |
| 来源筛选 / 每层条件 | 最多 8 层 / 8 个条件 |
| 每条规则输出 | 主输出 1 个、附加输出最多 7 个 |
| 每帧新增提取输出总量 / 路径读取预算 | 512 KiB 字符 / 32,768 次 |

旧集合中的未知类型、来源或无效记录保持原样，界面标明“不支持 · 不执行”，可查看原始配置、随集合导出或显式删除。它们不拦截连接、保存、切换集合及其他有效规则，也不会在读取时自动转换。不可用的旧插件引用回退原文。MCP 可用现有集合/请求更新工具保存相同通用规则，仍需项目和集合当前 revision。

## 协议能力与执行边界

“接口管理 → 协议能力”读取 `GET /api/interface-protocols`，MCP 对应 `list_interface_protocols(projectId)`。协议和消息编码分别建模：HTTP、WebSocket 支持现有调试及流程执行；RPC、gRPC、Protobuf 和二进制编码当前为预留描述，可保存和原样导出，不代表已有执行器或编解码器。

文档或操作使用 `x-pressure-protocol` 保存描述，操作级配置覆盖文档默认值；既有 `x-pressure-ws` 请求继续识别为 WebSocket：

```json
{
  "protocol": "grpc",
  "serialization": "protobuf",
  "rpc": {"service": "example.Greeter", "method": "SayHello", "streaming": "unary"},
  "protobuf": {"schema_ref": "schema-local", "descriptor_ref": "descriptor-local", "message_type": "example.Request"}
}
```

`rpc` 仅适用于 `rpc/grpc`，流模式为 `unary/client/server/bidirectional`；`protobuf` 仅适用于 Protobuf 编码。引用仅为惰性描述，不读取远程 schema 或 descriptor。缺少执行适配时，页面、HTTP 代理、流程引用和 Postman 转换拒绝把预留定义作为 HTTP 执行；含预留项的集合可用原始 OpenAPI 格式导出。消息展示插件不增加传输或编码执行能力。

协议校验见[interfaceprotocol](../controlplane/manager-go/internal/interfaceprotocol/registry.go)，数据库升级见[schema 模块](../controlplane/manager-go/internal/schema/README.md)。源码能力和远端部署验收分别记录，部署与备份见[部署与使用](部署与使用.md)。
