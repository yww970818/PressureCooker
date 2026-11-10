# 节点主动注册与 PressureCooker-Core 集群

用户要求检查容器运行但状态未变化的问题，增加Core连接队列后的主动节点注册，将控制器展示为PressureCooker-Core，并将两个集群调整为2/4 worker。当前实现只有管理端10秒HTTP拉取/status及执行期间任务心跳，没有空闲节点注册、机器指标或上报时间；现场8节点均idle、两集群各默认共享6worker。

## 实施边界

- 保留内部controller-a/controller-b及cluster ID、RMI名、密文与历史任务快照。页面显示PressureCooker-Core-1/2，当前集群配置显式改为worker-1/2与worker-3/4/5/6。新部署默认相同分配。
- **最新用户约束：管理平台不主动查询Core或worker的节点状态；只接受Core汇总上报。**
- Core代理确认Redis PING及消费组连接成功后立即POST `/api/nodes/report`，仅Core汇总上报自身与worker；worker不直接POST管理端；每5秒重报，初次连接失败不冒充注册，重连后恢复。HTTP失败重试，不影响任务及回调。首次连通后MQ断开仍上报queue_connected=false。
- 沿用agent-token的HMAC请求签名；管理端仅接受配置清单中匹配身份/角色的节点，不接收可改变调度地址或集群关系的URL。节点报告不得取得执行权限。
- Redis保存临时状态，不增加数据库迁移。接收时间由服务端生成；20秒判陈旧，保留最近记录24小时供诊断。原任务状态/租约/回调协议不变。
- Core聚合报告有界64KiB（单节点32KiB，最多64worker），version=1，字段id/role/display_name/boot_id/boot_started_at_ms/sequence/observed_at/queue_connected/status/healthy/generation/job/reset_operation/machine。machine为结构化通用机器信息，缺失值保留缺失；优先容器cgroup CPU/内存限额，不把宿主总量当容器配额。
- protocol.NodeReport为单节点快照；CoreNodeReport匿名嵌入NodeReport并有Workers []NodeReport。Core注册回包由manager配置返回Workers全池探测清单和ClusterWorkerIDs本簇调度成员。所有Core汇总全池worker供故障恢复观测，但各簇执行成员保持2/4，不因观测共享而共享执行分配。Core主动并发读取worker/status，管理端完全不读取节点/status。queue.StoredNodeReport含CoreNodeReport、ReceivedAtMS。queue.StoreNodeReport(ctx,report,receivedAtMS) (bool,error)做原子启动代次+序号防乱序，queue.NodeReports(ctx,ids)返回map[string]StoredNodeReport；保留24h。较旧boot、同boot旧/重复序号不刷新接收时间。
- /api/cluster保留现有结构，节点新增display_name、registration_status(registered/stale/unregistered/unknown)、last_reported_at、queue_connected、machine、status_source（core_report/none）、reported_by。新鲜报告优先；陈旧报告标记不可用且保留最后信息；没有报告的节点明确未注册且不可用，管理端禁止HTTP探测补拉，不把配置关系当真实注册。故障显示状态来源和时间。配置registered字段仍表示在清单中，不改变旧选择协议。
- 前端5秒刷新，区分空闲/运行/恢复/MQ断开/上报超时，显示机器与最近上报。名称不改写内部ID，历史原始标识仍可见。

## 任务及验证

- [x] execution/protocol/queue：协议和Redis原子存储；代理循环、机器采集、配置；定向红→绿，断连/重连/取消/失序/超时等本机测试。
- [x] manager：独立HMAC Core上报入口、清单校验与Redis读取，集群默认分组及显示名；认证/边界/陈旧/兼容测试。
- [x] frontend：Core名称、节点状态/上报与机器信息、5秒刷新；定向组件测试与构建。
- [x] 整合、独立审查；冻结重构建并发布，空闲时以CAS保存两个集群2/4配置。线上只验证注册心跳和页面、服务状态，不运行真实业务压测；任务数与历史快照保持。

## 失联恢复约束

调度和恢复也不得直接/status探测。恢复取得全局独占租约后，必须等待Core收到本次恢复租约观测epoch后重新采集的worker空闲证据，不能把旧缓存当作本次复位确认。读取缓存有界约12秒，失联或没有有效新证据时保守保持待恢复状态，不启动业务请求。

恢复新增观测epoch栅栏：管理端ACK返回当前全局租约SHA256，Core收到epoch后重新采集worker并带epoch上报。恢复必须匹配本次恢复租约的epoch、服务器接收新鲜度及空闲代次，不仅依赖跨机时间戳；原租约内容不对外暴露。
