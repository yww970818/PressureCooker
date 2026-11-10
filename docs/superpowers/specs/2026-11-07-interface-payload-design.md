# 接口请求、响应与断言详情

用户要求点击报告接口可查看请求地址、请求参数、响应内容及断言结果，部署后验证。沿用独立“接口执行详情”标签卡与原有点击面板、失败筛选及列表上下文。

现有 CSV JTL 不含请求和响应正文，不能从脚本配置推断历史真实报文。采用实际执行留样：官方 JMeter 的 JSR223 Listener（Groovy）在节点采集每个采样标签的首个成功和首个失败样本，经过节点身份、任务、尝试与代际绑定回传。保留 CSV 的聚合统计，只增加独立的有界证据。历史 JTL 已有 URL、参数、响应或断言失败列可以解析；缺字段明确未采集，成功采样不等于断言通过。

已考虑只展示脚本模板（无法代表实际请求）和全量正文归档（明显增加压力与存储），本实现采用有界实际留样。执行期只修改下载后的运行副本，不改不可变 JMX 原件。

单节点最多64条留样、编码后128KiB；URL最多4096字节、参数4096、响应8192、断言最多16条，单个名称128、消息512。超过边界明确截断/采集范围不完整。回调继续采用2MiB节点证据、4MiB回调总体界限。敏感键、已知认证值、URL认证信息与敏感查询必须脱敏；响应按纯文本展示，不执行HTML。

接口点击后请求 `/api/pressure-reports/:id/samples?step_key=...&node_id=...&limit=20&offset=0`，仅详情选择后读取。返回固定报告保存的证据，按时间分页；无step_key不返回全量正文。响应 `{detail_version:1|0,items,total,limit,offset,truncated}`，旧报告无留样为0。报告主详情、列表、总览、对比不携带留样正文。请求/响应面板与失败原因/日志使用同一明确接口/节点范围；快速切换取消旧请求。

公共留样字段：`id,node_id,step_key,label,sample_kind,timestamp_ms,elapsed_ms,success,method,url,request_params,response_code,response_body,assertions_captured,assertions,truncated,source_kind,source_name`。可选字符串为null表示未采集，空字符串为已采集且为空。断言项 `{name,failure,error,message}`，`assertions_captured=false` 表示未知，true且空数组为未配置/未产生断言结果，均不能冒充通过。

采集协议 `protocol.RequestSample` 使用上述字段中除id/node_id/step_key/source外的字段；`protocol.NodeEvidence` 增加 `request_samples`、`request_samples_truncated`、`request_samples_captured`，旧执行器不带则为未采集。reporting.RequestSample 增加完整展示字段。管理端绑定StepKey后脱敏落库；ParserVersion升1.4.0，新生成报告只读保存。历史重解析创建新版本，旧版本原样保留。

验收使用隔离夹具、官方JMeter合成采样/内部自测目标及历史报告，不启动真实业务压测；保留其他协作者修改，不整库提交。部署当前源码重新构建，空闲队列且持有执行租约时升级，保留回滚与指纹回执。
