export const GUIDE_TOPICS = [
 { id:'start', title:'快速上手', icon:'pulse', description:'先调试，再运行。按下面的顺序完成一次测试。', articles:[
  { id:'start-environment', title:'选择项目，准备测试环境', text:'在左侧选择所属项目，再在项目环境中配置服务器地址和业务变量。接口、流程、数据集和任务都按项目隔离。', destination:'environments', action:'配置项目环境' },
  {"id": "start-api", "title": "先调通单个接口", "text": "创建或导入接口集合，选择项目环境后检查实际地址、请求参数、认证和断言。长连接先核对握手方式、消息格式和接收条件。", "destination": "apis", "action": "打开接口管理"},
  { id:'start-workflow', title:'组织流程并发布脚本', text:'在流程编排中串联接口、参数和数据节点。确认执行顺序及变量传递后，保存草稿并发布可执行脚本版本。', destination:'plans', action:'打开流程编排' },
  { id:'start-dataset', title:'准备本次使用的数据', text:'有 CSV 节点时，下载动态模板，或按字段规则生成数据集；用表格预览和编辑后保存版本。', destination:'datasets', action:'准备 CSV 数据集' },
  { id:'start-cluster', title:'确认集群和执行节点', text:'查看 PressureCooker-Core 汇总上报的节点健康、MQ 连接和当前任务。选中节点会分别执行完整脚本，需结合节点数量核对总体负载。', destination:'cluster', action:'查看集群状态' },
  { id:'start-run', title:'创建并检查执行任务', text:'选择发布后的脚本、执行环境、集群、执行节点和数据版本，核对并发、循环、时长与验收目标后再提交。', destination:'runs', action:'打开任务与历史' },
  { id:'start-report', title:'查看报告和执行证据', text:'执行结束后打开报告。先看总览和关键曲线，再查看接口失败、节点日志和监控数据，需要分享时导出离线 HTML。', destination:'reports', action:'打开压测报告' },
 ] },
 { id:'environments', title:'项目与环境', icon:'settings', description:'先明确项目和环境，再复用同一套接口与流程。', articles:[
  { id:'environment-project', title:'项目选择与页面记忆', text:'左侧项目选择器切换项目，旁边设置入口管理项目。项目、当前页面和选中的项目环境会按项目记住，刷新后恢复；归档项目可查看历史，写操作由各模块限制。' },
  { id:'environment-inherit', title:'项目环境与集合覆盖', steps:['在项目环境中创建测试、正式等环境，填写服务器地址和业务变量。','接口或流程中选择环境，使用 {{base_host}} 或 {{变量名}} 引用。','集合环境继承项目配置；同名集合变量优先，空字符串也会覆盖项目值。需要统一继承时删除集合中的同名覆盖。'], note:'调试时选择的环境用于预览和单次调试；提交任务时仍需明确选择执行环境。', destination:'environments', action:'管理项目环境' },
  {"id": "environment-connections", "title": "服务地址与自动变量来源", "text": "项目环境的服务地址集中配置：服务器地址使用 base_host，Socket 地址使用 ws_url，Socket.IO 握手地址使用 socketio_handshake_url。三个名称固定，两个 Socket 地址可选；接口使用 {{ws_url}} / {{socketio_handshake_url}} 引用，其他变量名由项目自行定义。项目环境与集合覆盖支持固定值、随机值、调用方来源 IP 和当前毫秒时间戳。自动来源分别保存为 {{$auto.client_ip}} 与 {{$auto.timestamp_ms}}，不增加业务专用字段。", "example": "request_key = {{$random.uuid}}\nsample_key = {{$random.hex(32)}}\nrequest_source = {{$auto.client_ip}}\nrequest_time = {{$auto.timestamp_ms}}\n请求参数：{{request_source}} / {{request_time}}", "note": "只有本次有效变量使用自动来源时，才读取一次管理端上下文；同次请求、连接或任务快照复用，新操作重新获取。来源 IP 由管理端识别调用方连接，不查询外网 IP 服务；来源不可用时 IP 规则会阻止发送，单独时间戳规则仍可使用。长连接在连接时获取并复用，预览不会提前请求。生成值不写回配置；随机值不能替代有效账号或凭据。", "keywords": "自动来源 client_ip timestamp_ms IP 毫秒 时间戳 随机生成 UUID hex alnum digits 环境变量"},
  { id:'environment-snapshot', title:'环境修改与历史任务', text:'提交任务会冻结环境版本、地址和变量快照。之后修改环境不会改变已提交任务；要使用新配置，请创建新任务或使用任务副本。' },
 ] },
 { id:'apis', title:'接口调试', icon:'folder', description:'在单接口验证通过后，再把请求用于流程和压测。', articles:[
  { id:'api-http', title:'HTTP 集合与调试', steps:['新建集合或导入 OpenAPI、cURL、Postman、Apifox 导出内容。','选择环境并检查解析后的地址，填写请求参数、请求体和认证。','按实际需要选择浏览器直连或 master 转发，查看响应和断言。','保存集合；有未保存修改时，先保存或明确放弃后再切换。'], note:'浏览器直连受目标 CORS 和浏览器请求头限制；需要转发时切换 master 模式。', destination:'apis', action:'调试接口' },
  {"id": "api-websocket", "title": "WebSocket 与 Socket.IO 传统协议", "steps": ["选择环境，填写 WS 地址、子协议和需要的请求头。", "使用传统 Socket.IO 时配置握手地址；{socket_id} 由本次握手产生。", "点击连接，在消息模板或待发送框中填写接口文档要求的内容，再手动发送。", "按需配置定时消息；平台只自动处理传输层心跳，不解释业务认证和状态码。", "通过收发记录查看原文，或选择当前项目安装的解析展示插件。"], "note": "登录、订阅、确认、消息回执及会话标识等由具体应用定义，可通过项目配置和插件扩展；平台没有默认业务流程。", "keywords": "WS 长连接 websocket socket.io 握手 定时消息"},
  {"id": "api-extraction", "title": "消息模板与通用响应提取", "text": "点击“保存为模板”会保存模板及当前集合的修改，刷新后仍可使用；删除模板后需保存集合。响应提取规则默认空白，按实际 JSON 结构配置路径和输出变量；结果仅当前连接有效。", "steps": ["点击新增规则，填写名称、JSON 路径和输出变量名。", "粘贴一条响应进行本地测试，检查实际值；路径可使用字段和数组下标。", "应用规则并保存集合，等待符合路径的响应；路径返回多个候选时手动选择。", "在待发送消息中使用 {{变量名}}。断开、重新连接或修改规则后，旧提取结果失效。"], "note": "平台不预置任何项目的提取规则。不支持的旧规则会停用并保留原定义，可明确删除后重新配置。"},
  { id:'api-reserved', title:'RPC、gRPC 与 Protobuf', text:'在新建或编辑请求中可选择预留协议，填写服务、方法、流模式和 Protobuf 描述引用；协议能力页可查看当前支持状态。', note:'目前可编辑、保存和查看定义，尚未提供在线执行适配器；不会把预留协议自动按 HTTP 发送。', destination:'apis', action:'查看接口协议能力', keywords:'RPC gRPC Protobuf Protocol Buffers 二进制 schema descriptor' },
 ] },
 { id:'plans', title:'流程编排', icon:'flow', description:'把已验证的请求、变量和数据连接成可复用的执行流程。', articles:[
  { id:'workflow-library', title:'脚本库与统一工作区', text:'在流程编排中选择脚本或流程，使用结构、流程、源码、版本视图查看和编辑。JMX 源码与导入原文提供格式化预览、行号、高亮和复制。', destination:'plans', action:'打开流程工作区' },
  { id:'workflow-variables', title:'节点顺序与变量传递', text:'按业务先后连接节点，配置请求、断言和提取；后续节点使用前置步骤输出的变量。先核对登录、会话和清理步骤，再调整并发或循环参数。', note:'使用项目环境绑定时，流程保存变量引用；执行时由所选环境提供实际地址和变量。已有固定地址脚本仍按自身定义运行。' },
  { id:'workflow-version', title:'草稿、版本与发布', steps:['修改流程后保存草稿。','核对结构和 JMX 源码，确认需要的数据节点和参数。','发布脚本，选择发布后的版本创建任务。','需要修改时继续编辑并发布新版本；旧版本和任务快照保留。'], note:'保存草稿与发布可执行脚本是不同步骤。任务使用固定版本，不会随之后的草稿编辑自动更新。' },
  { id:'workflow-csv', title:'动态数据集模板', text:'在流程中的 CSV 数据节点下载模板，模板根据当前节点需要的列动态生成。节点列变化后重新下载，准备或生成对应数据文件。', destination:'datasets', action:'管理流程数据集' },
 ] },
 { id:'datasets', title:'CSV 数据集', icon:'file', description:'从流程节点出发准备数据，并明确选择本次执行的数据版本。', articles:[
  { id:'dataset-generate', title:'上传或一键生成 CSV', steps:['选择来源流程和 CSV 节点，确认所需列和运行时变量。','上传带表头的 UTF-8 CSV，或点击“一键生成 CSV”。','按字段规则和所需行数生成预览，核对后保存数据集。','有业务校验的账号、凭据和关联 ID 应使用有效测试数据，不能只靠随机生成。'], destination:'datasets', action:'上传或生成数据集', keywords:'参数 模板 生成 数据文件' },
  { id:'dataset-table', title:'表格预览与在线编辑', text:'数据集列表提供表格预览、下载和在线编辑。编辑后保存为新版本；核对列名、必填值和样本行，再用于任务。', note:'编辑不会重写原文件，也不会替换已有任务中的数据快照。来源流程或节点不可用时，需先处理来源配置。' },
  { id:'dataset-task', title:'为任务绑定正确版本', text:'创建任务时为对应的 CSV 节点选择数据文件与版本。若来源有多个节点，逐一确认绑定；仅上传文件不会自动替换已创建任务的数据。', destination:'runs', action:'查看任务数据选择' },
 ] },
 { id:'runs', title:'任务与历史', icon:'tasks', description:'把脚本、环境、集群和数据固定为一次可追溯的执行。', articles:[
  { id:'run-submit', title:'创建任务前核对', steps:['选择当前项目下已发布的脚本版本。','明确选择执行环境和集群，并核对执行节点。','选择 CSV 数据版本，填写脚本需要的属性和负载参数。','设置需要评估的验收目标，核对总体并发与时长后提交。'], note:'每个选中的执行节点运行完整脚本，平台不会自动把脚本线程数均分。', destination:'runs', action:'查看任务与历史' },
  { id:'run-state', title:'排队、执行与停止', text:'任务会经历排队、准备、运行、原件上传和回调等阶段。全平台共享执行租约，同一时间只执行一个批次；排队不代表任务丢失。需要停止时在任务详情操作，并等待停止和归档状态完成。', note:'停止或失败的任务仍归档实际取得的文件；缺失的原件会明确标记，不能据此判定成功。' },
  { id:'run-copy', title:'使用任务副本复测', text:'从历史任务复制配置，检查当前环境、节点和数据是否仍然有效，再创建新任务。原任务及其环境、脚本和数据快照保持原样。', keywords:'复制 复用 快照 历史版本' },
 ] },
 { id:'reports', title:'压测报告', icon:'pulse', description:'先看结果和趋势，再沿执行节点与流程步骤追踪证据。', articles:[
  { id:'report-overview', title:'总览与关键曲线', text:'总览展示关键指标、验收目标、实际并发、耗时和错误分析等内容。平均值用于总体观察，P95/P99 帮助关注慢请求；同时核对采样范围、记录类型和测试时间。', note:'曲线默认以线展示，悬停查看数据点和值。真实活跃线程每 1 秒独立采集；缺失区间保留空缺，不补成 0。', destination:'reports', action:'查看压测报告', keywords:'Latency Connect 连接耗时 并发 成功率 吞吐量 RPS TPS 分步骤 趋势' },
  { id:'report-interface', title:'接口执行详情与失败定位', steps:['打开“接口执行详情”，按执行节点和流程步骤查看结果。','使用“只看失败”缩小范围，按耗时或失败率排序。','点击接口查看失败原因、日志、请求地址、请求参数、响应内容和断言结果。','结合失败时间与节点日志核对问题；未留样或缺失的内容不能当作执行成功。'], keywords:'错误统计 请求响应 断言 日志 节点 步骤' },
  { id:'report-monitoring', title:'中间件与 JVM 监控', text:'“中间件与 JVM”查看 ES、MySQL、Redis、Kafka 和 JVM 实例。可按组件、实例名称搜索并分页查看大量 JVM；没有数据的卡片会保留状态提示。监控需要外部采集并通过 MCP 写入，与本次压测时间对齐。', note:'监控补充是独立版本，不会改变 JTL 指标或原验收结论。无数据不等于服务健康。', destination:'mcp', action:'查看监控写入工具', keywords:'Elasticsearch JVM 中间件 ES mysql redis kafka 内存 GC 线程' },
  { id:'report-export', title:'离线报告、对比与原始文件', text:'报告就绪后可导出离线 HTML，或选择两份报告进行对比；数据来源页可核对原件和解析范围。任务原件包括实际归档的 results.jtl、jmeter.log、console.log 及节点证据。下载需要当前登录认证。', note:'历史任务没有结构化报告时，可从任务详情生成；重新解析会生成报告版本。原件缺失时先检查归档状态。', keywords:'HTML 导出 对比 下载 JTL 原件 重新解析 版本' },
 ] },
 { id:'cluster', title:'集群与节点', icon:'cluster', description:'集群组织 PressureCooker-Core 和执行节点，由 Core 汇总上报节点状态。', articles:[
  { id:'cluster-select', title:'选择和维护集群', text:'集群管理中查看 PressureCooker-Core、关联节点、健康及当前任务。标准初始配置将两个集群分配为 2 / 4 个执行节点，已保存的集群配置优先。任务选择所选集群的执行节点子集；省略子集时使用该集群的节点配置。', destination:'cluster', action:'打开集群管理' },
  { id:'cluster-recovery', title:'节点恢复与结果回调', text:'PressureCooker-Core 每 5 秒汇总上报自身与 worker 状态，管理平台只读取上报，不主动探测节点；页面每 5 秒刷新。上报或 worker 观测超过 20 秒时标记为不可用，未收到上报时明确显示未知或缺失。节点启动或恢复后，应等待健康、MQ 已连接、空闲且上报新鲜后再使用。任务结束后还可能处于上传或回调阶段，需结合任务详情检查归档和回调结果。', note:'不要把“暂无业务请求”当成节点一定空闲。全局执行租约仍会协调共享节点。', keywords:'控制器 worker runner 健康 恢复 Webhook 回调' },
 ] },
 { id:'extensions', title:'JMeter 扩展', icon:'code', description:'查看组件来源，维护项目画布适配器。', articles:[
  { id:'extension-origin', title:'原生与自定义组件', text:'扩展管理列出 JMeter 5.6.3 的 117 种原生组件、平台通用组件与项目适配器，支持按来源搜索和筛选。读取当前流程引用可识别保留子流程中的外部组件。', destination:'extensions', action:'打开扩展管理' },
  { id:'extension-adapter', title:'用 JavaScript 适配自定义组件', steps:['新增适配器，填写与 JMX 一致的组件类型和来源位置。','定义 adapt(component)，返回 title 和 fields；使用样本属性 XML 预览。','保存并安装后，在节点中明确选择画布适配器。','可选提供组件 XML 模板，从画布组件栏新建。'], note:'适配代码只生成画布表单。JMeter 执行仍需要对应 Java JAR。卸载或删除适配器保留已有流程 XML。', destination:'extensions', action:'管理画布适配器', keywords:'JavaScript JS JMeter 自定义 原生 来源 扩展' },
 ] },
 { id:'plugins', title:'接口消息插件', icon:'code', description:'给现有收发记录增加解析和展示，消息仍由原调试面板发送。', articles:[
  { id:'plugin-create', title:'新增、导入与安装插件', steps:['进入接口管理，点击“接口插件”，确认当前项目。','点击“新增插件”，选择配置型或代码型；也可导入 JSON 或 JavaScript 文件。','配置消息格式、字段路径或 parse(frame) 解析函数，选择展示布局与主题色。','使用本地样本预览，保存后安装到当前项目。','在 WS 收发记录中选择已安装插件，可随时切换原文。'], note:'本地预览不会建立连接或发送消息。插件目录默认空白；按项目显式创建或导入配置型、代码型插件，再安装和选择。自定义插件支持编辑、导出、删除和安装/卸载。', destination:'apis', action:'管理接口插件', keywords:'JavaScript JS 配置型 代码型 解析 消息格式' },
  {"id": "plugin-mapping", "title": "按项目扩展消息格式", "text": "配置型插件用字段路径映射消息正文、发送人和附件链接；代码型插件可解析自定义文本或嵌套消息。用本地样本验证后安装到当前项目，其他项目不会自动获得这些业务规则。", "note": "平台不预装业务插件。业务解析、格式和展示通过项目插件维护，连接和发送仍使用通用调试面板。"},
  {"id": "plugin-boundary", "title": "展示边界与冲突处理", "text": "自定义插件只解析和展示已有帧，代码在隔离环境运行，不持有连接或发送入口。解析失败、超时或插件不可用时回退原文；图片和文件以可手动打开的链接展示。", "note": "多人编辑发生冲突后重新加载目录；若目标定义已变化，先核对草稿并重新打开最新定义。插件仅解析和展示，不持有连接、自动发送或环境写入能力。"},
 ] },
 { id:'mcp', title:'MCP 工具', icon:'code', description:'使用同一套项目和版本契约，通过工具管理系统功能。', articles:[
  { id:'mcp-catalog', title:'接入与查看工具参数', text:'打开 MCP 工具页，查看接入说明、实时工具目录、中文参数、返回信息和示例。客户端配置管理服务的 MCP 地址与认证，工具升级后重新连接以加载目录；认证信息应由接入配置提供。', destination:'mcp', action:'打开 MCP 工具目录' },
  { id:'mcp-project', title:'项目、修订号与幂等标识', steps:['每个业务调用显式提供 projectId，先读取目标对象及当前修订号。','写操作使用当前 revision；冲突时重新读取并核对，不重复覆盖。','提交任务明确指定 clusterId 和 environmentId，选择正确脚本与数据。','同一请求重试复用幂等标识和原参数，新任务使用新的标识。'], example:'先 list_interface_plugins(projectId)\n再 create_interface_plugin(projectId, revision, definition, installed)', note:'页面跳转或阅读工具说明不会调用执行工具。草稿需要发布后才能作为任务脚本。' },
  { id:'mcp-monitoring', title:'补充报告监控与插件定义', text:'中间件与 JVM 报告工具用于写入外部采集证据；大量 JVM 使用批量写入，并按实例分页读取。接口插件工具可管理配置型和代码型定义及安装状态，协议工具读取实际能力目录。具体字段、批量上限与返回值以实时目录为准。', keywords:'write_pressure_report_middleware write_pressure_report_jvm_batch list_interface_protocols create_interface_plugin' },
 ] },
 { id:'faq', title:'常见问题', icon:'book', description:'从界面提示出发，找到下一步需要检查的配置或执行证据。', articles:[
  {"id": "faq-url", "faq": true, "title": "选择环境后，地址仍有变量怎么办？", "text": "先确认当前项目、环境和变量名，再查看集合是否有同名覆盖；修改后刷新环境。传统 Socket.IO 的 {socket_id} 来自本次握手，其余业务字段需要由用户配置变量或通用响应提取，平台不会预填。", "destination": "apis", "action": "检查接口环境"},
  {"id": "faq-status", "faq": true, "title": "响应里有错误码，连接为什么仍然保持？", "text": "应用消息中的 code、status 等字段由具体协议定义。平台保留原始响应，不会默认把某个业务码当作登录失败并断开连接。请根据接口文档检查消息与认证参数，使用提取规则或项目插件解析应用状态。"},
  {"id": "faq-extraction", "faq": true, "title": "为什么响应变量一直没有提取？", "text": "检查规则是否启用、响应是否为有效 JSON、路径是否与实际层级及数组下标一致。新建连接或修改规则后要等待新的响应；路径没有匹配到值时不会凭空生成结果。可在规则编辑器用原文进行本地测试。"},
  {"id": "faq-assertions", "faq": true, "title": "收到一条回复就算请求成功吗？", "text": "连接建立、消息收到和业务成功是不同条件。按接口约定配置接收匹配和断言，并查看实际响应与日志。平台不会根据某个项目的确认报文或结束通知自动判定所有业务成功。"},
  { id:'faq-data', faq:true, title:'修改了环境或数据集，旧任务为什么没变化？', text:'任务保存的是提交时的环境、脚本和数据快照。修改当前配置或保存数据集新版本不会重写历史任务；要使用新值，请创建新任务或任务副本并重新确认选择。', destination:'runs', action:'查看历史任务' },
  { id:'faq-report', faq:true, title:'图表断开、监控没数据，是否说明没有问题？', text:'不是。断线可能表示该区间缺少真实观测，监控卡片也可能尚未写入外部证据。先查看数据来源、归档状态和采集时间；请求留样、日志或断言结果缺失时，都不能直接判断成功。', destination:'reports', action:'核对报告数据来源' },
  { id:'faq-plugin', faq:true, title:'为什么插件没有可用选项，或解析后又显示原文？', text:'先在当前项目安装插件，再在 WS 收发记录选择。项目不一致、插件被卸载/删除、输入不匹配、代码超时或解析异常都会回退原文。检查插件本地预览与错误提示，原始帧仍会保留。', destination:'apis', action:'检查插件配置' },
 ] },
];

const normalized = value => String(value || '').normalize('NFKC').toLocaleLowerCase().trim();
const termsFor = value => normalized(value).split(/\s+/u).filter(Boolean);
export function searchGuide(query, topicId = 'start') {
 const terms = termsFor(query);
 return GUIDE_TOPICS.flatMap(topic => topic.articles.map(article => ({ ...article, topicId:topic.id, topicTitle:topic.title, topicIcon:topic.icon })))
  .filter(article => terms.length ? terms.every(term => normalized([article.topicTitle,article.title,article.text,...(article.steps || []),article.note,article.example,article.keywords].join(' ')).includes(term)) : article.topicId === topicId);
}
