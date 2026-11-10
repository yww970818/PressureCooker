# 报告执行节点与流程步骤详情

用户要求补齐报告节点详情，按执行节点和流程步骤查看耗时、成功率、失败原因、日志，部署并验收。沿用 Go/Vue，不运行真实业务压测；当前源码均未跟踪，限定文件改动并保留协作内容，不做整库提交。

契约（供并行实现各模块）：

- reporting.Analysis 增加 Nodes []Node 和 NodeSteps []Step。Node: node_id,label,expected,status(OBSERVED/MISSING/UNASSIGNED/UNEXPECTED),metrics,first_at,last_at,step_count。Step 增加 node_id,omitempty、step_key（采样标签+类型的稳定SHA键，所有节点一致）；Issue 增加 node_id、step_key。未知节点固定 __unassigned__，不猜测或均分。缺少预期节点仍显示，metrics 的可选耗时/成功率 null。节点/步骤范围显式为采样标签，无法区分同名流程组件时说明。
- 解析保留现有总量与全局步骤/趋势，补充每节点普通采样统计、节点×步骤，事务另外统计，失败明细带关联；未知/外来节点与坏行不获得达标结论。限制节点与节点×步骤聚合，超限明确失败。ParserVersion 升为 1.2.0。
- protocol.Callback 可选 node_evidence []NodeEvidence。NodeEvidence: node_id,task_id,attempt_id,generation,prepared_at,finished_at,status(COLLECTED/MISSING),logs []NodeLog。NodeLog: name(jmeter.log/console.log),content,status(COLLECTED/MISSING),truncated,line_from,line_to,size。单日志上限32KiB，缺少/截断明确标记。身份/代次约束下，在 worker 进程重启清空日志前保存该任务证据；reset 重试返回持久化证据，runner journal 持久化，回调带回。所有缺失不得阻塞安全清理，不把 reset 完成当成业务执行成功。总回调受既有4MiB上限约束。
- manager 保持三个原始文件归档契约，在签名回调校验 expected worker/task/attempt、数量与大小后先脱敏，再存入run。源指纹纳入证据；重新解析创建新报告版本。report JSON 增加 nodes（上述结构，另外含log_status）、node_logs（脱敏、限长、来源范围）和 node_detail_version:1，无新SQL表。每节点步骤复用 pressure_report_steps 的 JSON 数据；默认 /steps 只返回全局步骤。
- GET /pressure-reports/:id/nodes → {items,total,detail_version:1}；/steps 支持 node_id 和 step_key，/issues 支持同样筛选；新增 /logs 支持 node_id/step_key 分页。/issues 不指定 node_id 保留全部原行为，/steps 不指定 node_id 仅全局汇总，保留旧排序和分页。流程步骤日志采用有明确关联的JTL失败摘要；worker日志没有step_key时不假装能归属步骤。旧报告 detail_version 缺省0，提示重解析；日志缺失明确提示。
- 前端新增“节点执行详情”页签及独立组件，节点/步骤筛选、指标、节点对比/步骤表、失败明细、日志内容与采集状态；总览指标保留。可筛选所有节点/具体节点、所有步骤/具体步骤（step_key）。接口返回数据要保留真实零与缺失区分；日志使用文本插值。兼容旧报告并提示重新解析。

工作分工：解析包（reporting/）；执行器和协议（execution/）；前端节点详情组件（frontend/src/reports/）；根会话独占 manager 集成/API/验证/发布，避免共享文件改动冲突。

- [ ] 解析包失败回归及实现；多节点、同名步骤、事务、未归属、缺失、边界/脱敏。
- [ ] 执行器身份隔离、重试、日志采集回调测试及实现，不执行真实JMeter业务。
- [ ] 前端筛选、指标、错误日志、旧版与窄屏验证。
- [ ] manager 回调校验/脱敏、持久化与新API，覆盖项目隔离、过滤、版本、空日志。
- [ ] 定向测试、编译、隔离集成、审查，从当前源码构建manager/执行器并部署，历史报告新版本重解析及线上验证。
