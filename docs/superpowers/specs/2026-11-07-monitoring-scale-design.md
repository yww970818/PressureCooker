# 百级JVM监控写入与展示

用户补充：JVM实例可能上百个。此前5类MCP实际写入和页面/离线验证已通过，但当前32实例上限与逐实例全展开不能满足新规模。沿用Go/Vue和当前独立监控版本，不运行真实压测，在内部验收报告用200个JVM模拟实例验证。

选择保留有界快照及原始单写协议，新增JVM批量写入，每批只产生一个revision，页面/离线按实例分页选择。无需修改DB结构，也不引入持续采集。总实例上限1000，单快照编码后（含内部元数据）8MiB；仍最多128补充版本。单写仍512KiB/6000总点；新批量最多200实例、4MiB正文、60000总点，每个entry仍受现有字段/64指标/12序列/每序列2000点/32发现限制。数据超过边界明确拒绝，不能静默截断。批量避免百个单写消耗百份完整快照版本。

API新增POST /api/pressure-reports/:id/monitoring/jvm-batch。body精确{revision,entries}；entries元素是原jvm写入的instance/source/window/summary?/health?/metrics?/series?/findings?，不能传component/revision，服务端固定jvm。同一批次不允许重复原始instance（entry_id同规则），全批校验通过才一次替换所有目标实例并保留其他条目。project/READY/active、时间交集、脱敏、行锁及Idempotency-Key语义与单写一致，原请求重试返回首次结果。返回{schema_version,project_id,report_id,revision,updated_at,written_count,instance_count,written_entries:[{entry_id,instance}]}，不回传所有指标和时序。history仅一条，标明批量JVM数量；single旧回放和返回shape不变。内部batch结果元数据不泄漏到GET快照。

GET /monitoring 保留不传筛选/分页时的完整快照供现有UI和内部导出兼容；可选component(es/mysql/redis/kafka/jvm/host/k8s)、search(实例名称包含匹配<=200字节)、limit(1..200默认20)、offset(0..1000000)。出现任一筛选/分页参数时返回当前页entries及total、limit、offset、instance_count、component_counts（各组件全量计数），revision/updated_at/history语义不变。revision仍可选，按指定历史快照先筛选再分页；project归属校验先于数据读取。完整快照/导出仍有8MiB边界，列表/对比仍不附时序。

MCP新增write_pressure_report_jvm_batch（projectId/reportId/revision/idempotencyKey/entries必填），固定jvm；get_pressure_report_monitoring新增component/search/limit/offset，默认转发limit=20/offset=0，避免把百个实例塞进回复。查询参数精确别名与API一致，传0不丢失。批量操作<=4MiB，MCP普通6MiB envelope边界不扩大；全量读取响应8MiB小于现有16MiB管理响应边界。服务版本.5、真实目录120项，中文说明/示例同步。

在线MonitoringCards仍保留7类卡片，按组件提供实例搜索/每页5个实例选择列表和前后翻页；仅渲染当前选中的一个实例完整指标/来源/发现/趋势。组件只有一个实例时保持原先直接显示。搜索和换页重置选择为该结果页首个，当前页无实例显示明确空态，总计与筛选计数区分。每组件最多一个详情区，避免按实例数倍增SVG/DOM；切换报告仍取消旧请求。完整快照最多1000条/8MiB，gzip沿用管理端，不引入额外重复全量GET。

离线HTML同样按组件搜索、分页5条、单实例选择展示；数据内嵌、无网络。组件总量/当前页/搜索结果真实，0/null/断点及外部结论口径不变，不把分页当成未采集。旧导出数据和无数据卡片继续工作。24MiB离线报告总边界保留并给出明确超限提示。

验证：批量200条一次revision、重复实例/无效项/超量/超字节全批回滚、幂等重试、项目与归档、已有其他组件保留；实例1000与1001边界；带revision的组件/搜索/分页口径；单写旧回放兼容；备份恢复仍含新结果。在线/离线200实例DOM有界（每组件最多5选择项、1份详情），搜索末尾实例、跨页、图表/0/null、手机滚动、无网络；最终实际MCP写入内部报告，保留既有中间件4条。部署从稳定当前源码构建管理端，不覆盖compose/执行器配置，不运行业务压力任务。
