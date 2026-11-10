# 压测中间件与JVM监控数据写入

用户要求通过MCP写入压测中间件报告及JVM报告数据。现有报告只有7类占位卡片。沿用Go管理端/MCP、Vue前端，写入后在总览和监控页展示，离线导出包含所选报告的最新监控补充快照；不自动执行采集或压测。

方案选择：直接改写解析报告会破坏固定版本；克隆全部报告与子表会把每次外部数据补充变成昂贵重解析版本。采用独立、只追加的监控补充版本，复用预留pressure_report_metrics子表，不改变JTL指标、验收结论及原报告data。报告版本和monitoring revision独立标明。重新解析的新报告不隐式继承旧报告外部数据。

API：GET/POST /api/pressure-reports/:id/monitoring。GET可选revision查询，返回{schema_version:"1",project_id,report_id,revision,updated_at,entries,history}；无数据revision=0、entries/history为空。history最多128项元数据{revision,created_at,component,instance}，不重复历史正文。POST必须Idempotency-Key及body.revision（首次0），只向READY报告写入；归档项目只读。每次替换相同component+instance条目，保留其他实例/组件，生成新revision；乐观冲突409。完全相同键/请求重试返回原revision，不被后续写入改变；同键不同请求409。报告行锁串行化，存储kind=monitoring_snapshot，metric_key为幂等键SHA256，label为请求SHA256；正文data仅存脱敏快照。现有备份恢复保留该子表。

POST正文精确字段：revision、component(es/mysql/redis/kafka/jvm)、instance(非空实例标识<=200字节)、source:{name(必填<=200),reference?(<=2000),collected_at?(RFC3339，默认当前时间)}、window:{start,end}(RFC3339，start<=end)、summary?(<=4000)、health?(UNKNOWN/NORMAL/WARNING/CRITICAL，默认UNKNOWN)、metrics?:[{key,label,value(number|null),unit?}]、series?:[{key,label,unit?,points:[{timestamp_ms,value(number|null)}]}]、findings?:[{level(INFO/WARNING/CRITICAL),title,detail?}]。metrics/series/findings至少一项非空。指标key使用^[A-Za-z][A-Za-z0-9_.-]{0,99}$，label<=200、unit<=32、title<=500、detail<=4000字节；拒绝未知字段、重复key、非有限/超出±1e15数值。最多64指标/12序列/6000总点（单序列2000）/32发现；点时间递增且落在window内。每次编码正文<=512KiB；当前完整快照<=2MiB、最多32实例、最多128监控补充版本，超限明确拒绝。

采集window须与run.started_at/finished_at有交集（允许预热/收尾）；缺有效运行时间时接受并标记time_alignment=UNCONFIRMED与警告。所有文字写入前用既有压测脱敏器处理已知凭据及URL/日志敏感内容；source.reference仅作文本，不拉取URL；正文和结论仅作外部证据展示，不自动断言服务健康或改写JTL判定。零值保留、null为缺少数据。

MCP新增write_pressure_report_middleware（component限es/mysql/redis/kafka）、write_pressure_report_jvm（固定jvm）和get_pressure_report_monitoring。显式projectId/reportId；写工具额外revision/idempotencyKey，原对象entry字段与API一致，参数使用source/window/metrics/series/findings，工具只转发管理端。读取可选revision。管理端报告详情返回monitoring摘要{revision,updated_at,instance_count,components}及已写组件extensions状态，不将监控时序塞进列表/对比；完整数据显式读取monitoring。固定报告正文不变。

页面MonitoringCards接收reportId，读取新端点，显示补充版本/刷新、各实例的指标/单位/零值/null、外部结论/发现、来源与时间范围、真实趋势。没有数据的卡片继续保留；主机/K8s暂无写工具，保持空态。请求切换报告清理旧数据；错误显示可重试。复用InteractiveReportChart，不按未知值插0。MCP目录增加中文字段、返回说明、示例与版本信息。离线导出使用同一快照，标明补充版本，纯文本安全渲染，不发网络请求。

验证使用隔离MySQL/本地夹具，验证并发revision、重试/冲突、跨项目、归档、脱敏、时间范围/边界、旧报告data字节不变；MCP精确HTTP映射与非法参数不下发；页面有数据/空态/失败/切换/桌面手机。部署用当前源码，保留现有compose/执行器配置，不运行真实业务压测、不把测试监控写入用户业务报告。

实现补充：POST返回{schema_version,project_id,report_id,revision,updated_at,entry}，重试返回该次原始版本。entry含稳定entry_id（component与原始instance的SHA256）、time_alignment（CONFIRMED/UNCONFIRMED）及warnings数组；原始实例标识不另存，entry_id避免脱敏后名称碰撞。GET的history按新到旧排列；内部revision=0表示最新，HTTP显式revision只接受正整数。metrics/series的label可省略或为空并回退key，unit可省略或为空。window与运行时间按闭区间相交校验。
