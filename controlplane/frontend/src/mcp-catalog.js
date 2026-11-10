// Chinese reference text is keyed by the actual MCP identifiers. Live schemas
// remain authoritative for required fields, types, enums and validation limits.
export const TOOL_CATEGORIES = ['项目管理', '草稿与发布', '流程管理', '接口与环境', '线程与流程', '请求采样', '配置与数据', '提取与脚本', '断言校验', '节奏控制', '元素编辑', '任务与产物', '压测报告'];
const catalog = {
  delete_execution: ["任务与产物", "删除任务历史", "删除当前项目的一条已结束任务及关联报告和产物；未结束任务禁止删除，归档项目只读。"],
  delete_executions: ["任务与产物", "批量删除任务历史", "原子删除当前项目1至100条不同的已结束任务；任一跨项目、缺失或未结束记录都会拒绝整批。"],
  clear_executions: ["任务与产物", "清空任务历史", "显式确认后清空当前项目全部已结束任务，保留未结束任务；范围不受筛选和分页限制。"],
  delete_pressure_report: ["压测报告", "删除压测报告", "删除当前项目一个报告版本及其分析、监控和明细，保留任务、原始文件及其他报告。"],
  delete_pressure_reports: ["压测报告", "批量删除压测报告", "原子删除当前项目1至100个不同报告版本；跨项目或缺失记录会拒绝整批，保留任务与原始文件。"],
  clear_pressure_reports: ["压测报告", "清空压测报告", "显式确认后清空当前项目全部压测报告，不受筛选和分页限制；保留任务、原始文件与其他项目数据。"],

  list_projects: ['项目管理', '列出项目', '查看服务中的项目名称、描述、版本和归档情况，取得后续工具所需的项目标识。'],
  create_project: ['项目管理', '创建项目', '新建独立项目，用于管理接口集合、流程、不可变脚本和任务历史。'],
  get_project: ['项目管理', '读取项目设置', '按项目标识查看名称、描述、版本及归档状态。'],
  update_project: ['项目管理', '修改项目设置', '提供当前版本号，更新项目名称与描述；版本冲突时先重新读取项目。'],
  archive_project: ['项目管理', '归档项目', '将项目标记为归档，保留脚本、接口和任务历史，并禁止新增执行任务。'],
  list_interface_protocols: ['接口与环境', '读取协议能力目录', '查看 HTTP、WebSocket 与预留 RPC、gRPC、Protobuf 的能力边界；读取不会建立连接或执行请求。'],
  get_interface_plugin: ['接口与环境', '读取接口插件定义', '读取当前项目自定义插件的完整配置或 JavaScript 源码、内容哈希和共享目录版本；代码仅作为数据返回。'],
  create_interface_plugin: ['接口与环境', '创建接口插件', '按当前目录版本保存项目配置型或 JavaScript 消息展示插件，并明确是否安装；只解析展示已有帧，不发送消息。'],
  update_interface_plugin: ['接口与环境', '更新接口插件定义', '按当前目录版本替换本项目插件完整定义，保留安装状态；内容哈希改变后刷新消息展示。'],
  delete_interface_plugin: ['接口与环境', '删除接口插件', '按当前目录版本删除本项目自定义插件及安装状态，或清理失效安装 ID；保留集合和历史引用，不可用引用回退原文。'],
  list_interface_plugins: ['接口与环境', '列出接口插件', '查看当前项目可用的消息展示插件、安装状态和独立安装版本；插件仅解析并展示既有收发记录。'],
  install_interface_plugin: ['接口与环境', '安装接口插件', '携带当前安装版本，为指定项目安装已创建的解析展示插件，随后可在 WebSocket 接口选择；不会自动安装其他项目的业务逻辑。'],
  uninstall_interface_plugin: ['接口与环境', '卸载接口插件', '携带当前安装版本，从指定项目卸载消息展示插件；已有接口回退原文展示。'],
  list_project_environments: ['接口与环境', '列出项目环境', '查看本项目统一管理的环境名称、基础地址、自定义变量和版本，供接口调试、流程预览与任务选择共用。'],
  create_project_environment: ['接口与环境', '创建项目环境', '为本项目保存完整 HTTP(S) 基础地址和可选自定义变量；基础地址独立保存，变量列表不包含 base_host。'],
  get_project_environment: ['接口与环境', '读取项目环境', '按环境标识读取本项目环境的基础地址、变量、当前版本和时间信息。'],
  update_project_environment: ['接口与环境', '修改项目环境', '提供当前版本号，更新本项目环境名称、基础地址和变量；版本冲突时重新读取，已提交任务保留原环境快照。'],
  delete_project_environment: ['接口与环境', '删除项目环境', '软删除本项目环境，使其不能用于新任务；已提交任务及其环境快照继续保留。'],
  upload_script: ['草稿与发布', '上传不可变脚本', '提供脚本名称和完整 JMX 内容，在本项目保存不可变发布版本；返回的发布版本标识可用于提交任务。'],
  download_script: ['草稿与发布', '下载脚本原文', '使用发布版本标识下载精确不可变 JMX，或使用流程标识导出当前流程；两个标识只能选一个。'],
  list_api_collections: ['接口与环境', '列出接口集合', '查看本项目保存的接口集合及其当前版本。'],
  create_api_collection: ['接口与环境', '创建接口集合', '保存完整 OpenAPI 文档及可选的 Scalar 客户端配置，保留认证、扩展字段和示例。'],
  get_api_collection: ['接口与环境', '读取接口集合', '按集合标识读取本项目的完整接口文档、客户端配置和版本号。'],
  update_api_collection: ['接口与环境', '保存接口集合', '提供当前集合版本号，保存完整 OpenAPI 文档与客户端配置；冲突时重新读取后再修改。'],
  delete_api_collection: ['接口与环境', '删除接口集合', '软删除本项目接口集合，保留既有历史版本和已发布脚本。'],
  import_api_collection: ['接口与环境', '导入接口集合', '提供 cURL、Postman、Apifox 或 OpenAPI 文本，在本项目创建集合；cURL 只解析文本，不执行命令。'],
  export_api_collection: ['接口与环境', '导出接口集合', '读取本项目集合，返回 OpenAPI 或 Postman 格式的完整导出文本。'],
  create_api_request: ['接口与环境', '新增接口请求', '按集合当前版本保存路径、方法与完整 OpenAPI 操作对象，包括参数、请求体、认证和响应。'],
  update_api_request: ['接口与环境', '修改接口请求', '按集合当前版本更新接口；变更路径或方法时传入原路径和原方法以替换原请求。'],
  debug_api_request: ['接口与环境', '调试接口请求', '由管理端代理执行所选集合请求，须明确选择项目环境或文档服务地址及变量；返回有大小和超时限制的状态码、响应头、正文与耗时。'],
  list_api_collection_environments: ['接口与环境','列出集合环境','读取指定集合的独立环境列表及父环境可用状态。'],
  get_api_collection_environment: ['接口与环境','读取集合环境','读取集合环境配置与合并后的当前生效配置。'],
  create_api_collection_environment: ['接口与环境','创建集合环境','按集合版本创建独立环境或持续继承父项目环境，管理端生成稳定ID。'],
  update_api_collection_environment: ['接口与环境','更新集合环境','按集合版本替换自有配置；省略基础地址恢复继承，空字符串变量保留覆盖。'],
  delete_api_collection_environment: ['接口与环境','删除集合环境','按集合版本删除环境，不改写历史引用和任务快照。'],
  set_api_environments: ['接口与环境', '保存集合环境覆盖', '按稳定项目环境ID和集合版本保存自定义变量覆盖；继承项目变量，集合优先，空字符串有效，variables空数组清空覆盖，base_host不可设置。'],
  update_api_collection_settings: ['接口与环境', '修改集合设置', '按集合当前版本更新名称、描述、服务地址或 Scalar 客户端状态。'],
  create_test_plan: ['草稿与发布', '创建测试草稿', '新建 JMeter 测试计划草稿，返回草稿标识和根节点标识，供后续添加组件。'],
  list_test_plans: ['草稿与发布', '列出测试草稿', '查看当前项目 MCP 工作区中的测试计划草稿；已发布的脚本版本请使用发布版本列表工具。'],
  get_test_plan: ['草稿与发布', '读取草稿结构', '读取草稿的完整组件树，查看每个组件的节点标识、类型和属性。'],
  get_test_plan_xml: ['草稿与发布', '读取草稿 XML', '将当前草稿序列化为 JMX XML 内容，供检查或发布前核对。'],
  import_test_plan: ['草稿与发布', '导入 JMX 草稿', '从 MCP 容器工作区 imports/项目标识 目录导入 JMX 文件；必须使用该项目目录内的绝对路径，文件不超过 5 MiB。'],
  publish_test_plan: ['草稿与发布', '发布不可变脚本', '把草稿保存为管理服务中的不可变 JMX 版本，返回发布版本标识；发布本身不会启动任务。'],
  list_manager_plans: ['草稿与发布', '列出已发布脚本', '查看管理服务中已发布的不可变 JMX 版本，选择供执行任务使用的版本标识。'],
  add_thread_group: ['线程与流程', '添加线程组', '定义并发虚拟用户、爬坡时间与循环次数；也可设置持续时间和启动延迟。'],
  add_setup_thread_group: ['线程与流程', '添加前置线程组', '在普通线程组之前执行准备工作，可设置独立的线程数、循环次数和调度时间。'],
  add_teardown_thread_group: ['线程与流程', '添加后置线程组', '在普通线程组之后执行收尾工作，可设置独立的线程数与调度参数。'],
  add_transaction_controller: ['线程与流程', '添加事务控制器', '把一组请求组织成一个事务，可选择将定时器耗时计入事务时间。'],
  add_loop_controller: ['线程与流程', '添加循环控制器', '让子组件按指定次数重复执行，用于构造局部循环。'],
  add_if_controller: ['线程与流程', '添加条件控制器', '仅在条件成立时执行子组件，可选择在每次子组件执行前重新判断。'],
  add_while_controller: ['线程与流程', '添加条件循环控制器', '根据条件持续执行子组件；条件控制循环的继续与退出。'],
  add_random_controller: ['线程与流程', '添加随机控制器', '每次运行时从子组件中随机选择一个执行。'],
  add_interleave_controller: ['线程与流程', '添加交替控制器', '在不同迭代中轮流执行子组件，可设置对子控制器块的处理方式。'],
  add_once_only_controller: ['线程与流程', '添加仅一次控制器', '让每个线程仅在首次迭代时执行所包含的子组件。'],
  add_http_sampler: ['请求采样', '添加 HTTP 请求', '配置请求方法、路径和请求体。省略协议、域名或端口时继承作用域内的 HTTP 默认配置；显式值会覆盖默认配置。'],
  add_jdbc_request: ['请求采样', '添加数据库请求', '通过已配置的 JDBC 数据源执行 SQL 查询或更新；数据源名称必须与连接配置一致。'],
  add_jsr223_sampler: ['请求采样', '添加脚本采样器', '使用 JSR223 脚本完成自定义采样。默认脚本语言为 Groovy，需确认执行环境的 Java 兼容性；随机或唯一值可优先使用内置函数。'],
  add_ftp_request: ['请求采样', '添加 FTP 请求', '配置文件上传或下载，包括远端文件、可选本地文件、登录信息和传输模式。'],
  add_tcp_sampler: ['请求采样', '添加 TCP 请求', '向指定主机和端口发送 TCP 数据，可配置客户端实现、连接复用和超时。'],
  add_header_manager: ['配置与数据', '添加请求头配置', '在请求或线程组作用域内统一配置 HTTP 请求头。'],
  list_workflow_datasets: ['配置与数据', '列出流程数据集', '按 CSV 节点稳定标识列出文件、列名、行数与不可变 MinIO 对象。'],
  list_plan_datasets: ['配置与数据', '读取脚本数据集要求', '读取已发布脚本的 CSV 节点要求和兼容数据文件，创建任务前逐节点选择。'],
  upload_dataset: ['配置与数据', '上传 CSV 数据集', '提供 UTF-8 CSV 文本与流程、节点标识，校验表头并保存为不可变文件；MCP 文本上传最多 5 MiB。'],
  generate_dataset: ['配置与数据', '生成 CSV 数据集', '为流程 CSV 节点按序列、UUID、固定值或随机整数生成数据，默认 100 行，最多 100000 行。'],
  get_dataset: ['配置与数据', '读取数据集信息', '读取数据集的列、行数、来源节点和不可变文件收据。'],
  preview_dataset: ['配置与数据', '预览数据集', '读取有限行数的 CSV 数据，完整文件通过下载工具取得。'],
  download_dataset: ['配置与数据', '下载 CSV 数据集', '读取数据集元数据与鉴权下载地址，大文件通过下载地址取得。'],
  delete_dataset: ['配置与数据', '删除数据集', '软删除文件，使新任务不再可选；已创建任务保留冻结文件快照。'],
  add_csv_data_set: ['配置与数据', '添加 CSV 数据集', '每个线程按迭代读取 CSV 行并赋值给变量；托管数据集按节点选择，Runner 在执行前准备所有选中节点的数据文件。'],
  add_user_defined_variables: ['配置与数据', '添加用户变量', '在测试启动前设置作用域内共享的变量值；各线程使用同一组初始值。'],
  add_jdbc_connection_configuration: ['配置与数据', '添加数据库连接池', '配置 JDBC 数据源、数据库地址、驱动、账号和连接池；数据库请求通过数据源名称引用。'],
  add_http_request_defaults: ['配置与数据', '添加 HTTP 默认配置', '统一设置作用域内 HTTP 请求的协议、域名、端口、路径与超时。'],
  add_cookie_manager: ['配置与数据', '添加会话管理', '管理 HTTP 会话数据，可配置每次迭代是否清空以及会话处理策略。'],
  add_user_parameters: ['配置与数据', '添加分线程参数', '按线程编号分配不同的变量值组；可选择每次循环重新赋值，每组值的数量须与变量名称数量一致。'],
  add_json_extractor: ['提取与脚本', '添加 JSON 提取器', '使用 JSONPath 从响应提取值并保存为 JMeter 变量，作为 HTTP1 子节点，输出名称 token、路径 $.data.token；后续同级 HTTP2 用 Authorization: Bearer ${token}，支持未匹配默认值。'],
  add_regex_extractor: ['提取与脚本', '添加正则提取器', '通过正则表达式和模板从响应中提取数据，配置匹配序号及默认值。'],
  add_xpath_extractor: ['提取与脚本', '添加 XPath 提取器', '使用 XPath 从 XML 或 HTML 响应中提取变量，处理非严格 HTML 时可启用宽容解析。'],
  add_jsr223_preprocessor: ['提取与脚本', '添加脚本前置处理', '在采样前运行 JSR223 脚本，用于准备请求数据；默认 Groovy，需确认 Java 兼容性。'],
  add_jsr223_postprocessor: ['提取与脚本', '添加脚本后置处理', '在采样后运行 JSR223 脚本，用于处理响应；默认 Groovy，需确认 Java 兼容性。'],
  add_response_assertion: ['断言校验', '添加响应断言', '根据指定字段和匹配规则校验响应，可反转断言结果。'],
  add_json_assertion: ['断言校验', '添加 JSON 断言', '校验 JSONPath 是否存在，并可比较预期值、判断空值或反转结果。'],
  add_duration_assertion: ['断言校验', '添加响应耗时断言', '采样耗时超过指定毫秒阈值时，将该次采样判定为失败。'],
  add_size_assertion: ['断言校验', '添加响应大小断言', '将响应字段的字节数与阈值进行比较，按所选比较运算判定结果。'],
  add_constant_timer: ['节奏控制', '添加固定等待', '在作用域内每次采样前加入固定毫秒数的延迟。'],
  add_uniform_random_timer: ['节奏控制', '添加均匀随机等待', '在基础延迟之上加入指定范围的随机延迟，每次采样重新计算。'],
  add_constant_throughput_timer: ['节奏控制', '添加目标吞吐定时器', '按每分钟目标采样数调节请求节奏，可选择按线程或线程组计算。'],
  remove_element: ['元素编辑', '删除组件', '删除指定组件及其子树，测试计划根节点不可删除。'],
  update_element: ['元素编辑', '更新组件属性', '以合并或替换方式更新组件属性；属性值为 null 时删除该键，更新结果按组件类型校验。'],
  rename_element: ['元素编辑', '重命名组件', '修改指定组件的显示名称，保留组件标识及结构。'],
  move_element: ['元素编辑', '移动组件', '将组件移至新的父节点下，可指定插入位置。'],
  reorder_children: ['元素编辑', '调整子组件顺序', '按提供的节点标识顺序重排父节点下的子组件。'],
  set_element_enabled: ['元素编辑', '启用或停用组件', '修改组件的启用状态，保留组件内容以便后续恢复。'],
  submit_console_task: ['任务与产物', '提交控制台任务', '明确选择执行集群后，把本项目已发布脚本提交到控制台相同的任务队列，仅使用该集群的控制器和节点。任务保存集群快照。所有新任务必须明确选择项目环境，环境值进入全局属性并保存快照；逐 CSV 节点选择一份或多份数据集及复制或分片方式，可设置执行节点、属性、超时和备注，并提供稳定幂等标识，同一请求重试时复用标识和参数。'],
  get_cluster_status: ['任务与产物', '读取集群状态', '查看管理实例、各集群的控制器与关联执行节点、共享节点及全平台任务队列状态。'],
  list_executions: ['任务与产物', '查询任务历史', '分页查询管理服务中的任务，支持按状态和名称或标识搜索，不汇总 JTL 样本。'],
  get_execution_status: ['任务与产物', '读取任务状态', '查询任务执行状态和原始文件归档进度，执行标识为管理服务中的任务标识。'],
  stop_execution: ['任务与产物', '停止执行任务', '向管理服务提交正常停止或立即终止请求，随后轮询任务状态，等待真实停止和节点清理完成。'],
  get_execution_log: ['任务与产物', '读取执行日志', '读取有限行数、已脱敏的执行日志尾部，默认 200 行，最多 2000 行。'],
  list_execution_artifacts: ['任务与产物', '查询原始文件', '按归档回执列出控制器及节点原始文件的字节数、SHA256 和 MinIO 对象键，包括已归档的 JTL、执行日志和节点采集证据；结构化统计和分析通过压测报告工具另行读取。'],
  list_pressure_reports: ['压测报告', '列出压测报告', '分页查询本项目已保存的报告版本；读取列表不会生成报告。'],
  get_pressure_report: ['压测报告', '读取压测报告', '按报告标识读取本项目固定版本的摘要、验收目标与采集信息，不自动重新解析。'],
  get_latest_pressure_report: ['压测报告', '读取任务最新报告', '按执行任务标识读取已存在的最新报告；没有报告时不会隐式生成。'],
  generate_pressure_report: ['压测报告', '生成或重新解析报告', '从任务已归档的原始记录生成报告；使用稳定幂等标识，重新解析创建新版本，不启动压测任务。'],
  list_pressure_report_steps: ['压测报告', '查询接口执行统计', '按执行节点和接口筛选已保存的步骤统计，支持只看失败、排序及服务端分页。'],
  list_pressure_report_nodes: ['压测报告', '查询节点执行统计', '读取报告内各执行节点的统计、计划负载及日志采集状态，缺失指标保留缺失。'],
  list_pressure_report_issues: ['压测报告', '查询失败原因', '按执行节点和接口范围读取失败次数、时间及原因；不把缺少证据解释为执行成功。'],
  list_pressure_report_logs: ['压测报告', '查询报告日志证据', '分页读取报告内已保存的节点日志或接口 JTL 失败证据，保留来源、作用域及采集状态。'],
  list_pressure_report_samples: ['压测报告', '查询接口请求留样', '明确选择接口后分页读取有限的脱敏请求、响应和断言留样；可进一步限定执行节点，不代表每次请求。'],
  get_pressure_report_analytics: ['压测报告', '读取报告性能分析', '按执行节点和接口读取固定报告版本的范围统计、图表序列、能力与警告，不触发重新解析。'],
  compare_pressure_reports: ['压测报告', '比较压测报告', '比较本项目选定报告的已保存指标与验收结果，缺失指标不补为零。'],
  export_pressure_report: ['压测报告', '导出离线报告', '取得本项目报告的鉴权下载信息，用于下载可离线查看的 HTML 文件；工具回复不嵌入完整 HTML。'],
  list_workflows: ['流程管理', '列出流程', '查看本项目保存的可编辑流程及当前版本。'],
  create_workflow: ['流程管理', '创建流程', '保存流程名称、描述和完整图结构，创建可编辑流程，不发布或执行。'],
  get_workflow: ['流程管理', '读取流程', '读取本项目流程当前的完整图结构和版本，供编辑、预览或导出。'],
  update_workflow: ['流程管理', '保存流程', '提供当前版本号保存完整图结构；版本冲突时重新读取，避免覆盖他人更新。'],
  delete_workflow: ['流程管理', '删除流程', '删除本项目流程；已发布的不可变脚本和历史任务按管理端规则保留。'],
  import_workflow: ['流程管理', '导入流程', '提供流程名称和完整 JMX 文本，在本项目导入流程，不执行其中请求。'],
  preview_workflow: ['流程管理', '流程预览', '将提供的当前图结构编译为 JMX XML，仅供检查，不保存、发布或执行。'],
  publish_workflow: ['流程管理', '发布流程脚本', '按当前流程版本发布不可变 JMX 脚本；发布完成后仍需单独提交控制台任务才会执行。'],
  list_workflow_versions: ['流程管理', '列出流程历史版本', '读取本项目流程的历史保存版本，供核对与追溯。'],
  get_workflow_version: ['流程管理', '读取流程历史版本', '按流程标识和版本号读取历史图结构，不覆盖当前流程。'],
  list_workflow_plans: ['流程管理', '列出流程发布记录', '查看本项目流程关联的不可变脚本发布版本，供后续选择任务脚本。'],
  list_datasets: ['配置与数据', '检索项目数据集', '按关键词、流程或 CSV 节点分页检索本项目的数据文件及元数据。'],
  get_dataset_catalog: ['配置与数据', '读取数据集目录', '查看本项目数据集目录信息，辅助选择流程、CSV 节点和数据文件。'],
  download_dataset_template: ['配置与数据', '下载动态 CSV 模板', '按流程当前 CSV 节点或未保存图结构生成仅含表头的模板，优先采用物理列，否则使用运行变量名；不包含数据行或环境值。'],
  list_api_collection_versions: ['接口与环境', '列出接口集合历史版本', '查看本项目接口集合的历史版本与保存信息。'],
  get_api_collection_version: ['接口与环境', '读取接口集合历史版本', '按集合标识和版本号读取已保存的历史文档与客户端配置，不修改当前集合。'],
  update_cluster: ['任务与产物', '修改执行集群', '按当前版本更新全局集群名称、关联节点或回调地址，影响使用该集群的所有项目；不下发执行任务。'],
  get_execution_copy_config: ['任务与产物', '读取任务复制配置', '读取本项目历史任务可编辑的复制配置，供确认后另行提交新任务；不会创建或执行任务。'],
  write_pressure_report_middleware: ['压测报告', '写入中间件监控补充', '为指定 READY 报告保存 ES、MySQL、Redis 或 Kafka 实例的外部监控证据，生成独立监控补充版本，不重新执行压测。'],
  write_pressure_report_jvm: ['压测报告', '写入 JVM 监控补充', '为指定 READY 报告保存 JVM 实例的指标、真实时序和外部发现；组件固定为 JVM，监控补充与报告版本独立。'],
  get_pressure_report_monitoring: ['压测报告', '分页读取监控补充', '按组件和实例名称分页读取本项目报告的监控补充，默认每页20个实例；可选择历史补充版本。'],
  write_pressure_report_jvm_batch: ['压测报告', '批量写入 JVM 监控补充', '一次原子写入最多200个 JVM 实例，只生成一个监控补充版本；保留其他组件和未替换实例，不执行采集或压测。'],
};
export function toolInfo(name) {
  const value = catalog[name];
  return value ? { category: value[0], title: value[1], description: value[2], known: true } : { category: '其他工具', title: '新增工具', description: '此工具尚无中文说明，请联系管理员更新工具目录。', known: false };
}

const reportScope = '结果仅属于 projectId 指定项目与所选报告版本；nodeId 限定执行节点，stepKey 限定接口步骤，省略筛选时按工具支持的全部范围查询。';
const detailVersion = 'detail_version 表示已保存的明细能力版本；0 表示历史版本没有相应明细，需显式重新解析，不能据此判断没有请求或异常。';
const offsetPagination = 'items 为本页记录，total 为筛选后总数；使用 limit / offset 翻页，空页不代表其他页没有数据。';
const returnNotes = {
  delete_execution: ["返回 deleted_count（本次已删除数）、skipped_count（保留的未结束任务数）、cleanup_pending（本次排入后台产物清理的任务数）；报告独立删除的 cleanup_pending 为 0。", "删除当前项目的一条已结束任务及关联报告和产物；未结束任务禁止删除，归档项目只读。", "所有删除仅限显式 projectId；归档项目只读。"],
  delete_executions: ["返回 deleted_count（本次已删除数）、skipped_count（保留的未结束任务数）、cleanup_pending（本次排入后台产物清理的任务数）；报告独立删除的 cleanup_pending 为 0。", "原子删除当前项目1至100条不同的已结束任务；任一跨项目、缺失或未结束记录都会拒绝整批。", "所有删除仅限显式 projectId；归档项目只读。"],
  clear_executions: ["返回 deleted_count（本次已删除数）、skipped_count（保留的未结束任务数）、cleanup_pending（本次排入后台产物清理的任务数）；报告独立删除的 cleanup_pending 为 0。", "显式确认后清空当前项目全部已结束任务，保留未结束任务；范围不受筛选和分页限制。", "所有删除仅限显式 projectId；归档项目只读。"],
  delete_pressure_report: ["返回 deleted_count（本次已删除数）、skipped_count（保留的未结束任务数）、cleanup_pending（本次排入后台产物清理的任务数）；报告独立删除的 cleanup_pending 为 0。", "删除当前项目一个报告版本及其分析、监控和明细，保留任务、原始文件及其他报告。", "所有删除仅限显式 projectId；归档项目只读。"],
  delete_pressure_reports: ["返回 deleted_count（本次已删除数）、skipped_count（保留的未结束任务数）、cleanup_pending（本次排入后台产物清理的任务数）；报告独立删除的 cleanup_pending 为 0。", "原子删除当前项目1至100个不同报告版本；跨项目或缺失记录会拒绝整批，保留任务与原始文件。", "所有删除仅限显式 projectId；归档项目只读。"],
  clear_pressure_reports: ["返回 deleted_count（本次已删除数）、skipped_count（保留的未结束任务数）、cleanup_pending（本次排入后台产物清理的任务数）；报告独立删除的 cleanup_pending 为 0。", "显式确认后清空当前项目全部压测报告，不受筛选和分页限制；保留任务、原始文件与其他项目数据。", "所有删除仅限显式 projectId；归档项目只读。"],

  list_interface_protocols: ['返回 protocols、serializations、metadata_extension 等能力描述；reserved 表示可保存描述，不能在线调试或作为 HTTP 执行。'],
  get_interface_plugin: ['返回当前项目的完整 definition、共享 revision 和 content_sha256；读取不安装、不解析执行代码，也不包含收发记录。'],
  create_interface_plugin: ['返回完整项目元数据目录、新 revision 和 created_id；定义和安装状态共用目录版本，首次为 0。', '定义最多64KiB、JavaScript源码最多60KiB，仅支持parse/render；预览样本与消息记录不进入定义。'],
  update_interface_plugin: ['返回更新后的项目目录及 revision；保留原安装状态，content_sha256 用于使旧展示缓存失效。', '发生409冲突时重新读取目标定义和目录版本，核对并合并后重试，不覆盖其他成员修改。'],
  delete_interface_plugin: ['返回删除后的项目目录及 revision；集合和历史版本不重写，原绑定回退原文。归档项目不能删除。'],
  list_interface_plugins: ['返回 project_id、独立 revision、插件目录 items 和失效安装 ID 列表 unavailable，installed 表示本项目安装状态。失效 ID 可显式卸载或删除，不能安装或执行。目录仅包含本项目创建的配置型或代码型插件，新项目默认为空；列表不包含完整源码。'],
  install_interface_plugin: ['返回更新后的完整插件目录与安装 revision；真实安装变化增加版本，同状态重试仍需当前 revision。冲突时先重新列出。'],
  uninstall_interface_plugin: ['返回更新后的完整插件目录与安装 revision；已有接口绑定可保留，卸载后以原文展示，不改变消息发送。'],
  list_pressure_reports: ['返回已保存报告的分页摘要和版本信息；不会触发生成或重新解析。'],
  get_pressure_report: ['返回固定报告版本、任务上下文、统计摘要和验收信息；null 或缺失字段表示无法确认，不补为 0。'],
  get_latest_pressure_report: ['返回该任务已存在的最新报告；没有报告时需显式调用生成工具，读取不会自动创建。'],
  generate_pressure_report: ['返回报告生成结果；重新解析创建新版本并冻结验收目标与分析设置，原版本保留。相同幂等标识和参数用于重试。'],
  list_pressure_report_steps: [reportScope, '省略 nodeId 时读取接口的全局汇总记录；提供 nodeId 时读取该执行节点的接口统计。', offsetPagination, detailVersion, 'failure_rate 为已知有效计数计算的百分比；未知计数保持 null，P95 与失败率不可按各节点简单平均。'],
  list_pressure_report_nodes: ['返回所选报告全部已知执行节点，不分页，也不接受接口筛选。', detailVersion, 'items 包含各执行节点的计划与统计，指标可能为 null；log_status 单独描述日志完整、部分、缺失或空状态。'],
  list_pressure_report_issues: [reportScope, offsetPagination, detailVersion, '失败原因、次数与时间来自已保存证据；缺少原因不能解释为无失败。'],
  list_pressure_report_logs: [reportScope, offsetPagination, detailVersion, '每条记录保留 source_kind / source_name、scope、status 和 truncated；MISSING 表示未取得，EMPTY 表示已取得但为空。', '按 stepKey 筛选仅返回实际关联的接口证据，不把整节点 worker 日志归属给某个接口；查看节点日志时省略 stepKey。'],
  list_pressure_report_samples: [reportScope, offsetPagination, detailVersion, '只返回有限留样；truncated 表示数量或内容受限，不代表全部请求。method、url、request_params、response_body 为 null 时未采集，空文本表示已采集为空。', 'assertions_captured=false 表示断言未采集；true 且列表为空表示未记录断言项或未配置断言，不能称为通过。只有显式断言项的 failure / error 结果可判定通过或失败。'],
  get_pressure_report_analytics: [reportScope, detailVersion, '省略 nodeId 时读取全局汇总范围，可同时用 stepKey 限定某个接口；提供 nodeId 时读取该节点范围。', '返回 scope 范围统计、series 图表、capabilities 采集能力、warnings 及冻结的 options；缺失序列保持缺失，0 是已保存的零值。', '范围摘要不随图表缩放重算；吞吐、事务采样与分位数沿用报告保存口径，不能跨范围简单平均。'],
  compare_pressure_reports: ['返回选定报告的比较结果，仅比较本项目已保存版本；未知或缺失指标保留缺失，不把零值与缺失混同。'],
  export_pressure_report: ['返回文件名、内容类型和鉴权下载地址等元数据，不返回完整 HTML；下载需登录态或 Bearer 认证，并保留项目上下文，URL 不含令牌。'],
  list_workflows: ['返回本项目流程列表、名称和当前版本信息；后续保存前读取完整流程与 revision。'],
  create_workflow: ['返回新流程标识、图结构与当前 revision；保存不会发布脚本或执行任务。'],
  get_workflow: ['返回当前完整图结构与 revision；保存编辑时携带该版本号，避免覆盖并发更新。'],
  update_workflow: ['返回保存后的流程与新 revision；版本冲突时先重新读取并合并，不盲目覆盖。'],
  delete_workflow: ['返回删除结果；删除不会停止或发起执行，已发布脚本与历史任务遵循管理端保留规则。'],
  import_workflow: ['返回导入后的流程标识和版本；导入仅解析并保存流程，不执行请求。'],
  preview_workflow: ['返回编译后的 JMX XML 及管理端预览信息；仅预览，不保存、发布或执行。'],
  publish_workflow: ['返回不可变脚本发布记录；发布本身不会创建执行任务，后续显式调用 submit_console_task。'],
  list_workflow_versions: ['返回流程历史版本列表；历史 revision 用于 get_workflow_version，不等同于不可变脚本标识。'],
  get_workflow_version: ['返回指定历史版本图结构和 revision，不改变当前流程。'],
  list_workflow_plans: ['返回流程关联的不可变脚本发布记录；任务使用其中的 managerPlanId 标识，当前流程后续编辑不会改变既有脚本。'],
  list_datasets: ['返回本项目数据文件分页结果及页码信息；page 从 1 开始，pageSize 控制每页数量，可按 q、workflowId、CSV nodeId 筛选；指定 nodeId 时必须同时提供 workflowId。'],
  get_dataset_catalog: ['返回本项目数据集目录及流程、节点关联信息，不返回或执行完整数据文件。'],
  download_dataset_template: ['返回 filename、content、columns、delimiter 及项目、流程和节点信息；content 是 UTF-8 BOM + CSV 表头，仅含列名，不含数据行或环境变量值。', '可传 graph 使用尚未保存的当前草稿；物理列优先，其次运行变量名，并按当前分隔符正确转义。缺少有效列时返回校验错误。'],
  list_api_collection_versions: ['返回本项目集合历史版本及保存信息；可按返回的 revision 读取指定版本。'],
  get_api_collection_version: ['返回指定版本的完整集合文档与客户端配置，不修改当前集合。'],
  update_cluster: ['返回更新后的全局集群配置与 revision，影响使用该集群的所有项目；版本冲突、节点归属冲突或忙碌集群节点变更由管理端校验，不执行任务。', '省略 workerIds / callbackUrl 保留原值；空 workerIds 解除节点关联，空 callbackUrl 恢复默认回调地址。'],
  get_execution_copy_config: ['返回本项目任务可编辑的复制配置与来源信息，不会创建或执行任务；确认脚本、集群、节点、数据集和环境后单独提交。', '提交复制任务使用 copySourceExecutionId，并明确 copyEnvironmentSnapshot 是否沿用冻结环境；每个新任务使用新的 idempotencyKey，只有同一请求重试才复用。'],
  write_pressure_report_middleware: ['返回 schema_version、project_id、report_id、revision、updated_at 和本次 entry；不改写 JTL 指标、验收结论或原报告版本。', '相同组件和实例替换为本次完整条目，其他实例保留；首次 revision=0，后续携带最新监控补充 revision。版本冲突先读取并核对。', '同一幂等标识和完全相同请求重试返回原补充版本；不同请求使用新标识。文字会脱敏，source.reference 仅存文本，不请求外部地址。', 'metrics / series / findings 至少一项非空，单次正文最多512KiB；当前完整快照最多8MiB、1000实例和128补充版本。零值保留，null 表示缺少数据；外部 health / findings 不作为平台自动健康判定。窗口需与任务时间相交，无法确认时标记 time_alignment=UNCONFIRMED。'],
  write_pressure_report_jvm: ['返回本次 JVM entry 与新的独立监控补充 revision；组件固定为 jvm，不改写 JTL 指标和验收结论。', '写入为同一实例的完整替换；首次 revision=0，后续使用最新监控补充 revision 和新的幂等标识。同一请求重试保留原标识与参数。', 'metrics / series / findings 至少一项非空；0 是实际零值，null 表示缺失。外部发现、时间对齐警告和来源一并保存。单次正文最多512KiB，当前完整快照最多8MiB、1000实例和128补充版本。'],
  get_pressure_report_monitoring: ['返回 schema_version、project_id、report_id、revision、updated_at、当前页 entries、history，以及 total、limit、offset、instance_count、component_counts。total 是筛选后实例数，instance_count 和 component_counts 是该快照全量实例计数。', '默认转发 limit=20 / offset=0；可按 component 和实例名称 search 筛选，limit 最多200。空页不代表其他页或其他组件没有数据。', '省略 revision 读取最新，提供正整数读取对应历史版本后再筛选分页；history 最多128条版本元数据，不重复历史正文。', '报告 monitoring 仅为摘要，完整指标和时序按本工具逐页读取。重新解析的新报告不会自动继承旧监控补充；离线导出内嵌完整快照，在线与离线均按组件选择实例展示。'],
  write_pressure_report_jvm_batch: ['全批校验并在一次事务中替换目标 JVM 实例，只增加一个 revision；任一无效条目导致全批失败，其他组件和未替换实例保留。每批最多200实例、4MiB正文、60000总点；当前完整快照最多1000实例、8MiB、128补充版本。', '返回 schema_version、project_id、report_id、revision、updated_at、written_count、instance_count 和 written_entries（仅 entry_id / instance），不回传百条实例时序。history 只新增一条批量记录。', 'entries 每项为原 JVM 实例字段，不传 component 或 revision；同一批不允许重复实例。首次 revision=0，后续使用最新监控补充版本；完全相同请求重试复用原键和全部参数，返回首次摘要。', '各实例仍受原有指标、序列与发现限制；0 保留，null 为缺失，外部结论不改写 JTL 验收，不启动采集或压测。'],
};
const pluginExampleDefinition = { kind: 'config', name: 'JSON 消息', version: '1.0.0', protocols: ['websocket'], parser: { format: 'json', rootPath: '$', fields: { text: '$.text', sender: '$.sender' } }, presentation: { layout: 'chat', accent: '#3b82f6' } };
const examples = {
  delete_execution: {"projectId": "project-example", "executionId": "run-00000000000000000000000000000000"},
  delete_executions: {"projectId": "project-example", "executionIds": ["run-00000000000000000000000000000000"]},
  clear_executions: {"projectId": "project-example", "confirm": true},
  delete_pressure_report: {"projectId": "project-example", "reportId": "pressure-00000000000000000000000000000000"},
  delete_pressure_reports: {"projectId": "project-example", "reportIds": ["pressure-00000000000000000000000000000000"]},
  clear_pressure_reports: {"projectId": "project-example", "confirm": true},

  list_interface_protocols: { projectId: 'project-example' },
  get_interface_plugin: { projectId: 'project-example', pluginId: 'custom-json-messages' },
  create_interface_plugin: { projectId: 'project-example', revision: 0, installed: true, definition: pluginExampleDefinition },
  update_interface_plugin: { projectId: 'project-example', pluginId: 'custom-json-messages', revision: 1, definition: { ...pluginExampleDefinition, version: '1.0.1' } },
  delete_interface_plugin: { projectId: 'project-example', pluginId: 'custom-json-messages', revision: 2 },
  list_interface_plugins: { projectId: 'project-example' },
  install_interface_plugin: { projectId: 'project-example', pluginId: 'custom-json-messages', revision: 0 },
  uninstall_interface_plugin: { projectId: 'project-example', pluginId: 'custom-json-messages', revision: 1 },
  list_pressure_reports: { projectId: 'project-example', limit: 20, offset: 0 },
  generate_pressure_report: { projectId: 'project-example', executionId: 'run-00000000000000000000000000000000', idempotencyKey: 'report-generation-example-1', rebuild: false },
  list_pressure_report_steps: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', failedOnly: true, sort: 'failure_rate', limit: 20, offset: 0 },
  list_pressure_report_samples: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', stepKey: 'step-example', limit: 20, offset: 0 },
  get_pressure_report_analytics: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000' },
  export_pressure_report: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000' },
  get_workflow: { projectId: 'project-example', workflowId: 'workflow-example' },
  get_workflow_version: { projectId: 'project-example', workflowId: 'workflow-example', revision: 1 },
  list_datasets: { projectId: 'project-example', page: 1, pageSize: 20, workflowId: 'workflow-example' },
  download_dataset_template: { projectId: 'project-example', workflowId: 'workflow-example', nodeId: 'csv-node-example' },
  get_api_collection_version: { projectId: 'project-example', collectionId: 'collection-example', revision: 1 },
  update_cluster: { projectId: 'project-example', clusterId: 'cluster-000000000000000000000000', revision: 0, name: '示例集群' },
  get_execution_copy_config: { projectId: 'project-example', executionId: 'run-00000000000000000000000000000000' },
  create_project_environment: { projectId: 'project-example', name: '示例环境', base_host: 'https://api.example.invalid', variables: [{ name: 'service_region', value: 'test' }, { name: 'ws_url', value: 'wss://echo.example.invalid/socket' }] },
  write_pressure_report_middleware: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', revision: 0, idempotencyKey: 'monitoring-example-1', component: 'mysql', instance: 'mysql-example', source: { name: '外部监控示例' }, window: { start: '2026-11-07T00:00:00Z', end: '2026-11-07T00:05:00Z' }, metrics: [{ key: 'connections', value: 0, unit: '个' }] },
  write_pressure_report_jvm: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', revision: 0, idempotencyKey: 'jvm-monitoring-example-1', instance: 'app-example', source: { name: 'JVM 监控示例' }, window: { start: '2026-11-07T00:00:00Z', end: '2026-11-07T00:05:00Z' }, metrics: [{ key: 'heap_percent', value: 0, unit: '%' }] },
  get_pressure_report_monitoring: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', component: 'jvm', limit: 20, offset: 0 },
  write_pressure_report_jvm_batch: { projectId: 'project-example', reportId: 'pressure-00000000000000000000000000000000', revision: 0, idempotencyKey: 'jvm-batch-example-1', entries: [{ instance: 'app-example-1', source: { name: '模拟 JVM 采集' }, window: { start: '2026-11-07T00:00:00Z', end: '2026-11-07T00:05:00Z' }, metrics: [{ key: 'heap_percent', value: 0, unit: '%' }] }, { instance: 'app-example-2', source: { name: '模拟 JVM 采集' }, window: { start: '2026-11-07T00:00:00Z', end: '2026-11-07T00:05:00Z' }, metrics: [{ key: 'heap_percent', value: null, unit: '%' }] }] },
};
export function toolReturnInfo(name) {
  return { notes: returnNotes[name] || [], example: examples[name] ? { name, arguments: examples[name] } : null };
}

const parameters = {
  executionIds: ['执行任务标识列表', '本项目1至100个不同的任务标识；任何未结束、缺失或跨项目记录导致整批拒绝。'],
  reportIds: ['报告标识列表', '本项目1至100个不同的压测报告版本标识；缺失或跨项目记录导致整批拒绝。'],
  confirm: ['确认清空', '必须显式传入布尔值 true，确认清空当前项目的全部对应历史，忽略筛选和分页。'],
  pluginId: ['接口插件标识', '从当前项目目录取得的自定义插件标识；安装状态和定义均属于指定项目。'],
  datasetId: ['数据集标识', '当前项目的数据文件标识，来自列表、上传或生成结果。'],
  datasetSelections: ['节点数据集选择', '每个 CSV 节点必须选择一份或多份文件，按 dataset_ids 顺序合并。'],
  node_id: ['节点标识', '发布版本要求的 CSV 节点稳定标识。'], dataset_ids: ['数据文件列表', '按顺序合并的兼容数据集标识列表。'], distribution: ['分发方式', 'copy 为每节点完整复制，shard 为按执行节点分片。'],
  timeoutSeconds: ['任务超时秒数', '默认 3600 秒，允许 10 至 86400 秒。'],
  reportTargets: ['报告验收目标', '压测前冻结的验收标准，仅用于报告判定，不改变实际负载；各项均可选。'],
  reportId: ['报告标识', '本项目已保存的固定报告版本标识，来自报告列表或生成结果。'],
  leftReportId: ['左侧报告标识', '本项目内作为比较左侧的已保存报告版本标识。'], rightReportId: ['右侧报告标识', '本项目内作为比较右侧的已保存报告版本标识。'],
  verdict: ['报告验收结论', '按报告已有的验收结论筛选，具体取值以当前服务定义为准。'],
  stepKey: ['接口步骤标识', '报告内稳定的步骤标识；从接口统计结果取得，不使用显示名称猜测。请求留样必须指定此值。'],
  failedOnly: ['仅显示失败接口', '为 true 时只返回有失败记录的接口；false 保留全部接口，筛选在服务端分页之前完成。'],
  sort: ['排序方式', '按工具当前枚举选择服务端排序，排序作用于完整筛选结果，再进行分页。'],
  rebuild: ['重新解析', '为 true 时从原始记录重新解析并创建新的报告版本，原报告保留；不重新执行压测。'],
  analysisOptions: ['报告分析设置', '生成时冻结的分析参数对象，内部字段沿用蛇形命名；省略时继承，显式 null 重置为服务默认设置，读取既有报告不会修改。'],
  apdex_satisfied_ms: ['APDEX 满意阈值', '满意响应时间的毫秒阈值，失败采样始终计为受挫。'],
  apdex_tolerated_ms: ['APDEX 容忍阈值', '容忍响应时间的毫秒阈值，不小于满意阈值。'],
  granularity_ms: ['时间桶粒度', '报告时间序列的毫秒粒度，冻结在新报告版本中。'],
  graph: ['完整流程图', '流程的完整图结构对象，保留节点、连线与配置；默认支持 5000 个节点、10000 条连线，容量默认 32 MiB、可配置至 64 MiB。'],
  component: ['监控组件', '中间件为 es / mysql / redis / kafka；JVM 写工具的组件固定为 jvm。'], instance: ['监控实例标识', '组件内稳定且非空的实例标识；同组件同实例写入替换完整条目，其他实例保留。'],
  source: ['采集来源', '外部证据来源名称、可选文本引用和采集时间。'], reference: ['来源引用', '可选来源标识或地址，仅按脱敏文本保存，不拉取URL。'], collected_at: ['采集时间', 'RFC3339 时间；省略时由管理端记录当前时间。'],
  window: ['采集时间窗口', 'start / end 使用 RFC3339，开始不晚于结束，须与本次任务运行时间有交集。'], summary: ['外部摘要', '本次实例监控的外部说明，不改写平台验收结论。'], health: ['外部健康结论', 'UNKNOWN / NORMAL / WARNING / CRITICAL，默认未知；仅作外部证据展示。'],
  metrics: ['实例指标', '指标列表，value 支持数值或 null；最多64项，标签省略时使用key。'], key: ['指标标识', '稳定且唯一的指标或序列标识，字母开头，仅含字母、数字、下划线、点或短横线。'], label: ['显示名称', '可选指标或序列名称；省略或空文本时使用key。'], unit: ['计量单位', '可选指标或时序单位，例如 ms、% 或 次/秒。'],
  series: ['实际时序', '最多12条序列，每条最多2000点，总点数最多6000；不合成未采集数据。'], points: ['观测点列表', '按时间严格递增的实际观测点，时间须在采集窗口内。'], timestamp_ms: ['观测时间戳', 'Unix 毫秒时间戳，须落在采集窗口内。'],
  findings: ['外部发现', '最多32条外部发现，保留级别、标题和详情，不自动推断服务故障。'], level: ['发现级别', 'INFO 为信息，WARNING 为警告，CRITICAL 为严重。'], title: ['发现标题', '非空的外部发现标题。'], detail: ['发现详情', '可选的外部证据和说明，按脱敏纯文本保存。'],
  entries: ['JVM 实例批次', '1至200个实例的完整 JVM 监控条目，每项不含 component 或 revision，仍受512KiB和6000点限制；同一批实例不可重复，总正文最多4MiB、总点数最多60000。'],
  page: ['页码', '从第 1 页开始的数据集分页页码。'], pageSize: ['每页数量', '每页数据集记录数，具体范围以当前服务定义为准。'], q: ['检索关键词', '按数据集名称等已支持字段检索本项目数据文件。'],
  workerIds: ['集群关联节点', '要关联到当前全局集群的节点标识列表；省略保留，空列表解除关联，可用节点和冲突由管理端校验。'],
  callbackUrl: ['集群回调地址', '全局集群使用的 HTTP(S) 回调地址；省略保留，空文本恢复默认地址，不在地址中放入令牌或密码。'],
  copySourceExecutionId: ['复制来源任务', '本项目历史任务标识，用于追溯本次复制来源；提交仍以确认后的新任务配置为准。'],
  copyEnvironmentSnapshot: ['沿用来源环境快照', 'true 沿用来源任务的可用冻结环境快照，须同时提供 copySourceExecutionId；false 使用当前配置。仅全绑定脚本可省略 environmentId。'],
  min_rps: ['最低每秒请求数', '普通采样每秒请求数的非负下限。'],
  min_success_rate: ['最低请求成功率', '请求成功率下限，取值为 0 至 100。'],
  max_p95_ms: ['P95 耗时上限', '至少 95% 请求的耗时应不超过此毫秒数。'],
  max_p99_ms: ['P99 耗时上限', '至少 99% 请求的耗时应不超过此毫秒数。'],
  min_duration_seconds: ['最短持续时间', '有效采样持续时间的非负秒数下限。'],
  columns: ['生成列', '每列指定名称和生成规则，列名与节点定义保持一致。'], rows: ['生成行数', '默认 100 行，最多 100000 行。'], generator: ['生成规则', 'sequence、uuid、constant 或 random_int。'], start: ['序列起点', '递增序列的初始整数。'], prefix: ['序列前缀', '加在每个序列值之前的文本。'], min: ['最小值', '随机整数范围的最小值。'], max: ['最大值', '随机整数范围的最大值。'], seed: ['随机种子', '指定种子以复现随机生成的数据。'],
  projectId: ['项目标识', '当前操作所属的项目；从项目列表或创建项目结果取得，业务数据及草稿均按此标识隔离。'],
  revision: ['当前版本号', '读取对象时返回的版本号；保存前提供该值，防止覆盖他人的更新。'],
  content: ['完整内容', '上传脚本时为完整 JMX 原文，导入接口时为原始 cURL、Postman、Apifox 或 OpenAPI 文本。'],
  description: ['描述', '项目、脚本或接口集合的补充说明。'],
  workflowId: ['流程标识', '当前项目内的流程标识；用于导出当前流程，与不可变脚本版本标识互斥。'],
  collectionId: ['接口集合标识', '当前项目内的接口集合标识，由集合创建、导入或列表结果取得。'],
  collectionEnvironmentId: ['集合环境标识','从当前集合的环境列表或创建结果取得的稳定ID。'],
  collection_environment_id: ['集合环境标识','target_source=collection_environment 时必填，与旧项目环境/文档目标互斥。'],
  parent_environment_id: ['父项目环境标识','可选；须属于当前项目。未覆盖的地址和变量持续继承父环境的当前配置。'],
  environmentId: ['项目环境标识', '当前项目内的统一环境标识，从项目环境列表或创建结果取得。'],
  base_host: ['基础地址', '完整 HTTP(S) 地址，可包含端口及基础路径；不可包含用户凭据、查询参数或片段。'],
  document: ['接口文档', '完整 OpenAPI 文档对象，保留接口、认证、示例、模型与扩展字段。'],
  client_state: ['客户端配置', 'Scalar 客户端状态对象，包括认证、环境及请求模式等已有设置。'],
  format: ['导入或导出格式', '导入可自动识别或指定 cURL、Postman、Apifox、OpenAPI；导出支持 OpenAPI 和 Postman。'],
  operation: ['接口操作定义', '完整 OpenAPI 操作对象，包含参数、请求体、认证、响应和扩展字段。'],
  previous_path: ['原请求路径', '接口路径变化前的路径，与原请求方法一起定位需要替换的请求。'],
  previous_method: ['原请求方法', '接口方法变化前的 HTTP 方法，与原请求路径一起定位需要替换的请求。'],
  environments: ['环境配置', '以环境名称为键的 Scalar 环境对象；各环境保留颜色和变量列表，变量包含名称与值。'],
  active_environment: ['当前环境', '要设为当前环境的标识；传空文本可清除选择。'],
  servers: ['服务地址列表', 'OpenAPI 服务地址对象列表，可保留地址、描述与服务器变量。'],
  planId: ['草稿标识', '测试计划草稿的标识，由创建或导入草稿工具返回。'],
  parentId: ['父节点标识', '要添加组件或调整子组件的父节点标识，可先读取草稿结构确认。'],
  nodeId: ['组件标识', '草稿结构中的目标组件节点标识。'],
  name: ['名称', '为测试计划、组件或执行任务设置便于识别的名称。'],
  numThreads: ['并发用户数', '当前线程组的并发虚拟用户数量，每个执行节点分别执行。'],
  rampTimeSeconds: ['爬坡时间', '达到完整线程数所需的秒数。'], loops: ['循环次数', '每个线程或控制器重复执行的次数，支持时可用 -1 表示无限循环。'],
  durationSeconds: ['持续时间', '按指定秒数调度运行，替代固定循环次数。'], delaySeconds: ['启动延迟', '线程组开始前等待的秒数，用于分阶段启动。'],
  method: ['请求方法', 'HTTP 请求使用的方法。'], protocol: ['请求协议', 'HTTP 请求协议；可省略以继承默认配置。'],
  domain: ['目标主机', '请求的主机名；可省略以继承默认配置。'], port: ['目标端口', '请求目标的端口号。'], path: ['请求路径', '请求的资源路径，例如 /v1/users。'],
  bodyJson: ['请求体', '原始 JSON 请求体文本。'], referenceName: ['输出变量名称', '保存提取结果的 JMeter 变量名。'], jsonPathExpr: ['提取路径', '用于提取响应值的 JSONPath 表达式。'],
  defaultValue: ['未匹配默认值', '提取不到匹配结果时使用的值。'], headers: ['请求头列表', '由名称和值组成的 HTTP 请求头列表。'], value: ['值', '对应名称的变量值或请求头内容。'],
  testField: ['校验字段', '选择需要校验的响应内容、响应头、状态码或消息等字段。'], matchType: ['匹配方式', '响应内容与模式之间的匹配规则。'], patterns: ['匹配模式列表', '用于校验响应字段的模式集合。'], not: ['反向校验', '启用后要求模式不匹配。'],
  filename: ['文件路径', '组件使用的文件路径，含义取决于当前工具。'], variableNames: ['变量名称', '输入数据对应的变量名；按工具要求传入逗号分隔文本或名称列表。'], delimiter: ['分隔符', 'CSV 文件的列分隔符。'],
  ignoreFirstLine: ['跳过首行', '首行为表头时可启用，避免将其作为数据行。'], recycle: ['循环读取文件', '文件耗尽后重新从首行读取。'], stopThread: ['耗尽时停止线程', '文件内容读完后停止当前线程。'],
  variables: ['变量列表', '测试启动前设置的变量名称和值。'], delayMs: ['基础延迟', '每次采样前等待的基础毫秒数。'], regex: ['正则表达式', '用于提取响应内容的正则表达式。'],
  template: ['提取模板', '根据匹配组生成提取值的模板。'], matchNumber: ['匹配序号', '选择使用第几个匹配结果，1 表示第一个。'], includeTimers: ['计入等待时间', '将定时器耗时计入事务时间。'],
  condition: ['执行条件', '判断是否执行子组件或继续循环的条件表达式。'], evaluateAll: ['逐次判断条件', '每个子组件执行前都重新计算条件。'],
  dataSource: ['数据源名称', '连接池名称；数据库请求与连接配置必须使用相同名称。'], dbUrl: ['数据库地址', '数据库连接使用的 JDBC 地址。'], driver: ['数据库驱动类', '执行节点中可用的 JDBC 驱动完整类名。'],
  username: ['登录账号', '访问目标服务或数据库的用户名。'], password: ['登录密码', '访问目标服务或数据库的密码，调用时按实际权限提供。'], poolMax: ['最大连接数', '连接池的最大连接数，0 表示不限制。'],
  connectionAge: ['连接存活时间', '空闲连接的保留时长，单位为毫秒。'], timeout: ['连接等待超时', '等待获得连接的最长毫秒数。'], trimInterval: ['连接池清理间隔', '检查并清理空闲连接的间隔，单位为毫秒。'], checkQuery: ['连接校验语句', '用于检查数据库连接有效性的查询语句。'],
  query: ['查询语句', '需要执行的 SQL 语句。'], queryType: ['查询类型', '选择查询、更新、预编译查询或事务控制等模式。'], resultVariable: ['结果变量', '保存查询结果的变量名称。'],
  scriptLanguage: ['脚本语言', 'JSR223 脚本使用的语言，执行环境需具备兼容支持。'], script: ['脚本内容', '采样或前后置处理时运行的脚本。'], parameters: ['脚本参数', '传递给脚本的参数文本。'],
  server: ['目标服务器', 'FTP 或 TCP 请求的目标主机。'], localFilename: ['本地文件路径', '下载目标或上传源文件的本地路径。'], inputData: ['上传内容', '直接提供的文件上传内容。'],
  binaryMode: ['二进制传输', '使用二进制方式传输 FTP 文件。'], saveResponse: ['保留响应内容', '将 FTP 响应内容写入采样响应。'], upload: ['上传文件', '启用为上传，关闭为下载。'],
  request: ['发送内容', 'TCP 连接中发送的原始请求文本。'], classname: ['客户端实现类', 'TCP 客户端处理器的类名。'], reUseConnection: ['复用连接', '后续请求复用同一 TCP 连接。'], closeConnection: ['结束后关闭连接', '当前采样完成后关闭 TCP 连接。'],
  noDelay: ['禁用发送延迟', '关闭 TCP 小包合并延迟。'], connectTimeoutMs: ['连接超时', '建立连接允许的最长毫秒数。'], timeoutMs: ['响应超时', '读取响应允许的最长毫秒数。'], responseTimeoutMs: ['响应超时', 'HTTP 请求等待响应的最长毫秒数。'],
  clearEachIteration: ['每轮清空会话', '每次线程迭代开始时清空会话数据。'], policy: ['会话处理策略', '会话数据的解析与兼容策略。'], ignoreSubControllerBlocks: ['忽略子控制器块', '调整交替执行时对子控制器内部组件的处理方式。'],
  xpathQuery: ['提取表达式', '定位 XML 或 HTML 内容的 XPath 表达式。'], tolerant: ['宽容解析', '使用宽容的 HTML 解析方式处理非严格内容。'], valueSets: ['分线程值组', '每个内部列表对应一个线程槽位，值的顺序和数量须与变量名列表一致。'], perIteration: ['每轮重新赋值', '启用时每次迭代重新分配变量值，否则仅在线程启动时分配。'],
  jsonPath: ['校验路径', '需要验证是否存在的 JSONPath 表达式。'], expectedValue: ['预期值', '启用值校验时需要匹配的值。'], jsonValidation: ['校验预期值', '除路径存在性外，也检查预期值是否匹配。'], expectNull: ['预期为空值', '要求目标值为空。'], invert: ['反转结果', '将断言结果反转。'], isRegex: ['使用正则预期值', '将预期值解释为正则表达式。'],
  maxDurationMs: ['最大响应耗时', '允许的采样耗时上限，单位为毫秒。'], size: ['目标字节数', '用于比较的响应字段字节数阈值。'], operator: ['比较运算', '响应大小与阈值的比较方式。'], rangeMs: ['随机延迟范围', '在基础延迟上额外增加的随机毫秒数上限。'],
  targetSamplesPerMinute: ['每分钟目标采样数', '希望达到的每分钟采样数量。'], calcMode: ['吞吐计算范围', '决定目标速率按当前线程、全部线程或当前线程组计算。'],
  props: ['组件属性', '组件属性补丁或完整对象；值为 null 的键会被删除。'], mode: ['操作方式', '选择当前工具采用的操作方式。'], newParentId: ['新父节点标识', '组件移动后的父节点标识。'], index: ['插入位置', '组件在新父节点下的插入序号。'], orderedChildIds: ['子节点顺序', '父节点下子组件的完整有序标识列表。'], enabled: ['是否启用', '是否在执行测试时启用此组件。'],
  filePath: ['导入文件路径', 'MCP 容器工作区 imports/项目标识 目录内的 JMX 绝对路径；文件不超过 5 MiB，不接受共享脚本目录或其他项目目录。'],
  managerPlanId: ['发布版本标识', '管理服务保存的不可变脚本版本标识，来自发布结果或已发布脚本列表。'], executionId: ['执行任务标识', '管理服务中的任务标识，用于查询、停止或读取日志及产物。'],
  idempotencyKey: ['幂等请求标识', '每个新任务使用新的标识；重试同一请求时复用相同标识和参数，避免重复执行。'],
  clusterId: ['执行集群标识', '先读取集群状态并明确选择集群；任务仅使用该集群的控制器和节点，提交时固定集群快照。'],
  workers: ['执行节点列表', '所选集群内的非空节点子集，不可重复，最多 64 个；节点ID来自部署清单，省略时由服务端选择该集群的全部关联节点。'], properties: ['全局执行属性', '发送到每个执行节点的 -G 属性键值对象；命名身份使用 script_identity.<身份名>.<声明字段>，由脚本的版本化声明约束字段类型及必填项；认证使用方式由脚本负责。旧身份脚本须显式转换后重新发布。最多20条身份，值为单行文本；每个节点执行完整脚本。'], notes: ['任务备注', '记录执行目标、环境和本轮变化。'],
  status: ['任务状态', '按执行状态筛选任务。'], search: ['搜索关键词', '按任务名称或标识搜索。'], limit: ['每页数量', '单页最多返回的记录数。'], offset: ['跳过记录数', '分页查询时从列表开头跳过的记录数量。'], tail: ['日志行数', '读取日志最后若干行，默认 200 行，允许 1 至 2000 行。'],
};
const pluginParameters = {
  definition: ['插件完整定义', '项目自定义插件的完整配置；kind=config 使用 parser，kind=code 使用 code，两者互斥。'],
  'definition.id': ['自定义插件标识', '可选 custom- 前缀标识；创建时省略由服务端生成，更新时须与目标插件一致。'],
  'definition.kind': ['插件类型', 'config 为字段映射插件，code 为 JavaScript 代码解析插件。'],
  'definition.name': ['插件名称', '当前项目用于识别此消息展示插件的非空名称。'],
  'definition.description': ['插件说明', '说明本项目消息格式和展示方式，不包含凭据或消息记录。'],
  'definition.version': ['插件显示版本', '插件作者维护的版本文本，与目录并发控制 revision 分开。'],
  'definition.protocols': ['适用协议', '当前只允许包含 websocket 的列表，不增加 RPC 或二进制执行能力。'],
  'definition.code': ['JavaScript 解析定义', '代码型插件配置，仅用于隔离浏览器沙箱解析已有消息帧。'],
  'definition.code.language': ['解析语言', '当前仅支持 javascript，不加载外部依赖。'],
  'definition.code.source': ['JavaScript 解析源码', '定义 function parse(frame)，返回消息对象或数组；最多60KiB，无网络、发送或环境写入权限。'],
  'definition.parser': ['字段映射解析器', '配置型插件的报文格式、消息根路径和展示字段映射。'],
  'definition.parser.format': ['消息格式', 'json 解析 JSON，socketio 去除传统3:::前缀后解析，text 使用原始文本。'],
  'definition.parser.rootPath': ['消息根路径', '从 $ 开始的受限字段路径，选中数组时按消息逐项映射，最多256字节和16层。'],
  'definition.parser.fields': ['展示字段映射', '相对于每条消息的字段路径；留空或省略的展示字段不输出。'],
  'definition.parser.fields.text': ['正文路径', '消息正文的字段路径；纯文本格式可使用 $。'],
  'definition.parser.fields.sender': ['发送人路径', '消息发送人显示名称的字段路径。'],
  'definition.parser.fields.title': ['标题路径', '消息标题的字段路径，不作为可执行HTML。'],
  'definition.parser.fields.type': ['消息类型路径', '用于展示的消息类型字段路径，不触发应用命令。'],
  'definition.parser.fields.image': ['图片链接路径', '图片HTTP(S)地址的字段路径，只展示手动打开的链接。'],
  'definition.parser.fields.link': ['链接路径', '不含嵌入凭据的HTTP(S)地址字段路径。'],
  'definition.presentation': ['展示设置', '消息布局与主题色，不能声明发送或其他运行时权限。'],
  'definition.presentation.layout': ['消息布局', 'chat 对话、cards 卡片或 compact 紧凑列表。'],
  'definition.presentation.accent': ['主题色', '完整 #RRGGBB 十六进制颜色。'],
  installed: ['创建后安装', '显式设置新插件是否安装到当前项目；不会影响其他项目。'],
};
const pluginTools = new Set(['list_interface_plugins', 'get_interface_plugin', 'create_interface_plugin', 'update_interface_plugin', 'delete_interface_plugin', 'install_interface_plugin', 'uninstall_interface_plugin']);
export function parameterInfo(toolName, path) {
  const key = path.split('.').at(-1).replace(/\[\]/g, '');
  const entry = (pluginTools.has(toolName) && pluginParameters[path]) || parameters[key];
  const info = entry ? { label: entry[0], description: entry[1], known: true } : { label: '扩展参数', description: '此参数尚无中文说明，请联系管理员确认。', known: false };
  if (pluginTools.has(toolName) && key === 'revision') Object.assign(info, { label: '插件目录版本', description: '当前项目插件定义与安装状态共享的目录 revision，初始为 0；发生冲突后重新读取目录和目标定义，再使用最新版本。' });
  const monitoring = ['write_pressure_report_middleware', 'write_pressure_report_jvm', 'write_pressure_report_jvm_batch', 'get_pressure_report_monitoring'].includes(toolName);
  const entryPath = path.replace(/^entries\[\]\./, '');
  if (monitoring && key === 'revision') Object.assign(info, { label: '监控补充版本', description: toolName === 'get_pressure_report_monitoring' ? '省略读取最新快照，指定正整数读取该历史补充版本；不是报告版本号。' : '当前监控补充版本，首次为 0；后续使用最新快照 revision，冲突时重新读取核对，不是报告版本号。' });
  if (monitoring && entryPath === 'source.name') Object.assign(info, { label: '来源名称', description: '非空的外部采集来源名称。' });
  if (monitoring && entryPath === 'window.start') Object.assign(info, { label: '窗口开始时间', description: 'RFC3339 格式，不能晚于窗口结束时间。' });
  if (monitoring && entryPath === 'window.end') Object.assign(info, { label: '窗口结束时间', description: 'RFC3339 格式，不能早于窗口开始时间。', known: true });
  if (toolName === 'get_pressure_report_monitoring' && key === 'component') info.description = '按组件 es / mysql / redis / kafka / jvm / host / k8s 筛选；省略查询所有组件。';
  if (toolName === 'get_pressure_report_monitoring' && key === 'search') info.description = '按当前快照实例名称包含匹配，先筛选再分页。';
  if (toolName === 'get_pressure_report_monitoring' && key === 'limit') info.description = '默认每页20个实例，最多200；与 offset 配合逐页读取。';
  if (monitoring && key === 'value') Object.assign(info, { label: '观测值', description: '有限数值或 null；0 保留为实际零值，null 表示缺少数据，不能用0代替。' });
  if (monitoring && key === 'idempotencyKey') info.description = '每次新监控补充使用新标识；重试完全相同请求时复用原标识与全部参数，避免重复新增版本。';
  if (toolInfo(toolName).category === '压测报告' && key === 'nodeId') Object.assign(info, { label: '执行节点标识', description: '报告内执行节点的标识；省略时查询全部执行节点。' });
  if (['list_pressure_report_steps', 'get_pressure_report_analytics'].includes(toolName) && key === 'nodeId') info.description = '报告内执行节点的标识；省略时读取全局汇总范围，提供时限定该执行节点。';
  if (['list_datasets', 'download_dataset_template', 'upload_dataset', 'generate_dataset'].includes(toolName) && key === 'nodeId') Object.assign(info, { label: 'CSV 节点标识', description: '当前流程中的 CSVDataSet 节点稳定标识。' });
  if (key === 'workflowId' && toolName !== 'download_script') info.description = '当前项目内的流程标识，来自流程列表或创建结果。';
  if (toolInfo(toolName).category === '流程管理' && key === 'name') Object.assign(info, { label: '流程名称', description: '当前流程便于识别的名称。' });
  if (toolInfo(toolName).category === '流程管理' && key === 'description') info.description = '当前流程的补充说明。';
  if (key === 'content' && toolName === 'import_workflow') info.description = '完整 JMX XML 文本；按当前部署的流程容量校验（默认 32 MiB，可配置至 64 MiB），导入为流程，不执行其中请求。';
  if (key === 'revision' && ['get_workflow_version', 'get_api_collection_version'].includes(toolName)) Object.assign(info, { label: '历史版本号', description: '要读取的已保存历史版本号，来自对应版本列表。' });
  if (key === 'idempotencyKey' && toolName === 'generate_pressure_report') info.description = '为一次报告生成或重新解析提供新的幂等标识；重试同一请求时复用标识和参数，避免重复创建报告版本。';
  if (key === 'reportTargets' && toolName === 'generate_pressure_report') info.description = '本次报告的验收标准；省略时继承，显式 null 清空验收目标。只影响报告判定，不修改原执行任务或实际负载。';
  if (key === 'status' && toolName === 'list_pressure_reports') Object.assign(info, { label: '报告状态', description: '按报告的生成状态筛选，不是执行任务状态。' });
  if (key === 'search' && toolName === 'list_pressure_reports') info.description = '按报告列表支持的任务名称或标识搜索本项目报告。';
  if (key === 'sort' && toolName === 'list_pressure_report_steps') info.description = 'latency 按 P95 耗时、failure_rate 按失败率、failures 按失败次数降序；排序先于分页，未知计数保持未知。';
  if (key === 'clusterId' && toolName === 'update_cluster') info.description = '本次要修改的全局执行集群标识，先读取集群状态确认当前版本；修改影响使用该集群的所有项目。';
  if (path.includes('columns[]') && key === 'name') Object.assign(info, { label: '列名', description: 'CSV 表头中的唯一列名称。' });
  if (key === 'mode') info.description = toolName === 'stop_execution' ? '选择正常停止或立即终止；提交后仍需等待节点清理。' : '合并保留未指定属性，替换会用新对象覆盖原属性。';
  if (key === 'filename' && toolName === 'add_csv_data_set') info.description = 'CSV 数据文件的绝对路径，需在各执行节点上可读。';
  if (key === 'filename' && toolName === 'add_ftp_request') info.description = 'FTP 服务器上的远端文件路径。';
  const projectEnvironment = toolName.endsWith('_project_environment') || toolName === 'list_project_environments';
  if (key === 'environmentId' && toolName === 'submit_console_task') info.description = '默认必填；仅脚本 requires_project_environment=false 时可省略。集合环境绑定节点使用各自目标并固定快照。';
  if (key === 'environmentId' && toolName === 'debug_api_request') info.description = 'target_source 为 project_environment 时必填，使用该项目环境的基础地址和变量。';
  if (key === 'target_source' && toolName === 'debug_api_request') Object.assign(info, { known: true, label: '目标来源', description: 'collection_environment 使用集合环境及其父环境；project_environment 和 document_server 为旧显式兼容目标，三者互斥。' });
  if (key === 'variables' && toolName === 'debug_api_request') Object.assign(info, { label: '调试变量', description: '本次接口调试的变量覆盖值对象。' });
  if (projectEnvironment && key === 'variables') Object.assign(info, { label: '自定义变量列表', description: '此环境的自定义变量名称和值列表，不包含 base_host；不允许重复名称。服务地址、客户端标识等按实际需要配置，不预置业务变量；集合同名覆盖值优先。' });
  if (projectEnvironment && key === 'name') Object.assign(info, path.includes('variables[]') ? { label: '变量名称', description: '自定义变量的名称；变量列表不包含 base_host，名称不可重复。' } : { label: '环境名称', description: '便于识别的项目环境名称，例如测试或正式。' });
  return info;
}

export function schemaParameters(schema = {}, prefix = '', depth = 0) {
  if (depth > 8) return [];
  const object = schema.properties ? schema : (schema.anyOf || schema.oneOf || []).find(option => option.properties) || schema;
  return Object.entries(object.properties || {}).flatMap(([name, value]) => {
    const path = prefix ? `${prefix}.${name}` : name;
    const row = { path, schema: value, required: (object.required || []).includes(name), nested: depth > 0 };
    const nested = value.type === 'array' ? schemaParameters(value.items, `${path}[]`, depth + 1) : schemaParameters(value, path, depth + 1);
    return [row, ...nested];
  });
}
const types = { string: '文本', number: '数值', integer: '整数', boolean: '布尔值', array: '列表', object: '对象', null: '空值' };
export function schemaType(schema = {}) {
  if (Array.isArray(schema.type)) return schema.type.map(type => types[type] || '任意值').join(' / ');
  if (schema.anyOf || schema.oneOf) return (schema.anyOf || schema.oneOf).map(schemaType).join(' / ');
  return types[schema.type] || '任意值';
}
const enumLabels = {
  copy: '每节点完整复制', shard: '按节点分片', sequence: '递增序列', uuid: 'UUID', constant: '固定值', random_int: '随机整数', auto: '自动识别', curl: 'cURL 命令文本', postman: 'Postman 集合', apifox: 'Apifox 导出', openapi: 'OpenAPI 文档',
  latency: 'P95 耗时降序', failure_rate: '失败率降序', failures: '失败次数降序',
  QUEUED: '等待处理', PROCESSING: '解析中', RETRY: '等待重试', FAILED: '失败', READY: '报告就绪', NOT_SET: '未设置验收目标', PASS: '通过', FAIL: '未通过', INCOMPLETE: '信息不完整',
  es: 'Elasticsearch', mysql: 'MySQL', redis: 'Redis', kafka: 'Kafka', jvm: 'JVM', host: '主机资源', k8s: 'Kubernetes', UNKNOWN: '未知', NORMAL: '正常', WARNING: '警告', CRITICAL: '严重', INFO: '信息',
  true: '是', false: '否', graceful: '正常停止', now: '立即终止', merge: '合并', replace: '替换',
  contains: '包含匹配', matches: '正则完全匹配', equals: '相等', substring: '子串', equal: '等于', notequal: '不等于', greaterthan: '大于', lessthan: '小于', greaterthanequal: '大于等于', lessthanequal: '小于等于',
  response_data: '响应正文', response_code: '响应状态码', response_headers: '响应头', response_message: '响应消息', response_network_size: '响应总字节数',
  this_thread_only: '仅当前线程', all_active_threads: '全部活动线程', all_active_threads_in_current_thread_group: '当前线程组的活动线程', all_active_threads_shared: '全部活动线程共享', all_active_threads_in_current_thread_group_shared: '当前线程组的活动线程共享',
  'Select Statement': '查询', 'Update Statement': '更新', 'Callable Statement': '存储过程', 'Prepared Select Statement': '预编译查询', 'Prepared Update Statement': '预编译更新', Commit: '提交事务', Rollback: '回滚事务', 'AutoCommit(false)': '关闭自动提交', 'AutoCommit(true)': '开启自动提交',
  standard: '标准策略', compatibility: '兼容策略', netscape: '兼容旧版会话', default: '默认策略', ignoreCookies: '忽略会话',
  GET: '读取', POST: '提交', PUT: '替换', PATCH: '局部更新', DELETE: '删除', HEAD: '读取响应头', OPTIONS: '查询支持方法', TRACE: '回显诊断',
  http: '普通 HTTP', https: '加密 HTTPS', groovy: 'Groovy 脚本', beanshell: 'BeanShell 脚本', javascript: 'JavaScript 脚本', jexl3: '表达式脚本',
};
export function enumLabel(value) { return enumLabels[String(value)] || '参数值'; }
export function schemaLimits(schema = {}) {
  const limits = [];
  for (const [key, label] of [['minimum', '最小值'], ['maximum', '最大值'], ['exclusiveMinimum', '须大于'], ['exclusiveMaximum', '须小于'], ['minLength', '最少字符'], ['maxLength', '最多字符'], ['minItems', '最少项数'], ['maxItems', '最多项数']]) {
    if (typeof schema[key] === 'number') limits.push(`${label} ${schema[key]}`);
  }
  if (schema.uniqueItems) limits.push('列表项不可重复');
  return limits.join('；');
}
