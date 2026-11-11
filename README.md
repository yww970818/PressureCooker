# PressureCooker

PressureCooker 是独立的通用压测管理平台，提供项目、环境、接口集合、流程编排、JMeter 执行、数据集与结构化报告。业务地址、认证、请求、断言和消息格式由使用者在项目资源中配置；长连接消息可通过项目自定义插件解析展示。

## 使用文档

| 文档 | 内容 |
| --- | --- |
| [部署与使用](docs/部署与使用.md) | 运行依赖、构建、部署、任务、归档与恢复 |
| [流程画布与接口管理](docs/流程画布与接口管理.md) | 流程、接口调试、环境及变量作用域 |
| [项目与接口复用](docs/项目与接口复用.md) | 项目边界、导入导出、精确版本引用、MCP 与 CLI |
| [JMeter 扩展管理](docs/流程扩展管理.md) | 组件来源、117 种原生组件画布适配及自定义 JavaScript 表单 |
| [接口插件](docs/interface-plugins.md) | 配置型与 JavaScript 消息展示插件、JSON 响应提取、协议预留边界 |
| [报告契约](PRESSURE-REPORTS.md) | 原始文件、结构化报告、版本、对比及验收口径 |
| [测试基线与验收](docs/测试基线与验收.md) | 本地测试、隔离依赖和通用夹具 |

## 能力与架构

接口管理使用 Scalar，流程画布使用 LogicFlow，前端使用 Vue。自研管理端、CLI、MCP 适配和执行代理使用 Go；JMeter、Scalar、LogicFlow 和锁定的官方 npm MCP 依赖使用各自运行时。

| 目录 | 职责 |
| --- | --- |
| [controlplane/frontend](controlplane/frontend) | 页面、接口调试、流程编辑和报告展示 |
| [controlplane/manager-go](controlplane/manager-go) | API、项目资源、CLI、调度、编译、归档登记和报告 |
| [controlplane/execution](controlplane/execution) | 控制器与执行节点代理、JMeter 生命周期和结果回传 |
| [controlplane/mcp-go](controlplane/mcp-go) | 项目作用域的 MCP 工具与官方创作运行时适配 |
| [tools/platform-ops](tools/platform-ops/README.md) | 通用主机运维与离线文件校验 |

MySQL 保存元数据及加密的项目内容，Redis Streams 传递任务，MinIO 保存发布脚本、数据集和原始结果。Compose 默认拓扑为两个 PressureCooker-Core、六个 worker，显式初始分配为 2/4；平台接受部署清单中的通用节点ID，单批最多64个节点。平台支持多批任务并行执行，每个所选节点执行完整 JMX。worker 从任务提交至执行与清理完成期间独占；若所选 worker 已被排队或运行中的任务占用，提交返回 `409/worker_busy`，须改选空闲节点或等待释放。

PressureCooker-Core 连接 Redis 后主动注册，每5秒汇总自身和 worker 的状态、机器信息及队列连通性上报。管理端只读取上报缓存，20秒未更新即标记超时，不主动探测节点。内部 controller ID 保持兼容，页面使用 PressureCooker-Core 名称。注册与故障恢复链路见[节点注册与状态](docs/节点注册与状态.md)。

项目统一管理环境的 `base_host` 和自定义变量，集合可按环境 ID 覆盖自定义值。流程引用集合的精确修订及请求快照，发布产生不可变脚本。任务提交时明确选择项目、脚本、环境、集群和数据集，并固定执行快照；后续编辑不改写历史任务。

HTTP 与 WebSocket 支持调试及流程执行。WebSocket 提供标准连接、传统 Socket.IO 握手与传输心跳、手动发送、消息模板、定时消息和通用 JSON 提取。提取规则默认空列表；项目插件目录默认空，只安装和选择用户创建的配置型或 JavaScript 展示插件。插件解析已有帧，发送仍由用户操作原发送按钮。

RPC、gRPC、Protobuf 和二进制编码当前保留描述配置，可保存和原样导出；实际能力以[协议目录与执行边界](docs/interface-plugins.md#协议能力与执行边界)为准。旧的未知规则保留原配置并停止执行，不拦截集合保存或连接；不可用插件回退原文，历史引用保留。

CSV 数据集支持上传、生成、预览、版本化编辑及 copy/shard 分配。JTL、日志和实际采集的执行证据按任务归档并校验大小与 SHA256；结构化报告支持固定版本、重新解析、筛选、对比和离线 HTML 导出。归档成功与脚本断言通过分别判断，口径见[报告契约](PRESSURE-REPORTS.md)。

## 开发与验证

依赖版本和构建准备见[部署与使用](docs/部署与使用.md)。前端可单独安装依赖、测试和构建：

```bash
cd controlplane/frontend
npm ci
npm test
npm run build
```

在仓库根目录运行统一本地基线：

```bash
GO=/path/to/go bin/test-controlplane.sh
```

基线使用三个 Go 模块、平台工具、前端测试及生产构建，不启动真实业务压测。隔离 MySQL、Redis 和本机 WebSocket 夹具的用法见[测试基线与验收](docs/测试基线与验收.md)。仅做 Go 导入与编译检查时，在 `controlplane/manager-go`、`controlplane/execution`、`controlplane/mcp-go` 各自运行 `go test -run '^$' ./...`；实际变更另运行对应定向测试。

准备完整发布产物时，在仓库根目录执行：

```bash
bin/prepare-mcp-cache.sh
GO=/path/to/go JAVA_HOME=/path/to/jdk21 JMETER_HOME=/path/to/apache-jmeter-5.6.3 bin/build-master.sh
```

构建生成 Go 二进制、JMeter 线程观测扩展及源码/产物校验清单。新发布必须从当前源码重建，历史清单和旧二进制不能代表当前内容已验收。

## 部署与数据

新部署的 Compose 项目默认使用 `pressurecooker`，镜像使用 PressureCooker 命名前缀；配置入口为 [cluster.env.example](cluster.env.example)、[compose.yaml](compose.yaml) 和 [manage.sh](manage.sh)。现存实例按[部署兼容说明](docs/部署与使用.md#架构与部署配置)保留显式标识及持久数据，源码命名调整不自动迁移运行资源。

密钥、缓存和本地验收记录位于 `runtime/`，不提交运行数据或输出密钥正文。备份须安全保留原 `session-key`，以及数据库、对象存储、MCP 草稿归属和执行器待确认状态；恢复及数据库升级见[部署与使用](docs/部署与使用.md)与[schema 模块](controlplane/manager-go/internal/schema/README.md)。

历史业务测试资产由仓库外的业务工作区维护，不作为平台构建、测试或功能入口。历史发布回执保留当时的路径和指纹；平台源码能力、构建结果及远端部署验收分别记录。

开发维护：[后端架构与模块边界](docs/后端架构与模块边界.md)。

项目环境页集中管理项目环境和集合环境。接口调试只选择当前集合环境；流程可把每个 HTTP/WS 节点绑定到指定集合环境，父项目环境持续继承，任务固定节点快照。详见[集合环境集中管理](docs/流程画布与接口管理.md#集合环境集中管理)。
