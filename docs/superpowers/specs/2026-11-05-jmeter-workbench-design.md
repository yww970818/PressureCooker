# JMeter 流程画布与接口管理

用户指定：流程编辑用 LogicFlow，接口客户端用 @scalar/api-client；master 自研后端/存储逻辑为 Go，页面 Vue，全中文。沿用现有 Go/MySQL master 与六节点执行池、原始文件归档，不改变已有任务接口。

## 流程画布

使用 @logicflow/core 2.2.5。画布表达 JMeter 父子作用域树，连线 order 明确同级执行顺序，限制单根、单父、无环、无孤立节点。支持增加/删除/移动/连线、组件参数、撤销重做、缩放/适应画布、保存/重新加载、导入/导出 JMX，以及发布不可变脚本供现有任务页执行。

草稿记录为 {id,name,description,revision,graph,created_at,updated_at}。graph.nodes 使用 LogicFlow 坐标与节点结构，properties 为 {component,name,enabled,config,raw_xml?}；edges 的 properties.order 为非负整数。提供 TestPlan、ThreadGroup、HTTP 请求、循环/条件/事务控制器、请求头/变量/CSV、等待、响应断言、JSON 提取器等12类。数字和表达式参数保持字符串，允许 JMeter ${...}。导入已知组件保留原 XML 的额外属性，未知插件及子流程保留为不可拆分的透明传递节点。

Go /api/workflows 提供列表/创建/读取/更新/软删除，导入可用已有 plan_id 或 JMX 内容；/:id/export 导出 JMX，/:id/publish 基于 revision 发布不可变版本。保存使用 revision 乐观锁，冲突保留本地编辑供重新加载，发布不发起压力。

## 接口管理

使用 @scalar/api-client 3.23.6 的真实请求编辑/调试组件，启用官方 zh-CN。集合以完整 OpenAPI 文档存储，保留 Scalar 示例/认证/服务地址扩展；client_state 保留文档之外的认证、环境及请求模式。集合支持创建/编辑/保存/恢复查看、JSON/YAML 导入导出和版本记录。

Go /api/api-collections 提供集合 CRUD、revision 乐观锁及版本读取。记录为 {id,name,description,document,client_state,revision,created_at,updated_at}。完整文档与客户端认证配置一起加密保存到 MySQL，不记录凭据到日志；备份只带密文，异机恢复另行保留用于解密的 session-key。

用户选择请求模式可切换：浏览器直连或 master 转发。两种模式均禁用第三方代理。master 转发使用 /api/api-proxy?scalar_url=绝对目标地址，按 Scalar 原生协议保留方法/业务认证/请求体。仅通过平台会话+X-JMeter-CSRF-Token认证，平台Cookie/认证头不发送给目标，目标自己的X-CSRF-Token保留；业务 Cookie 通过 X-Scalar-Cookie 显式传递，响应 Set-Cookie 转为 X-Scalar-Set-Cookie，不污染平台会话。目标只允许 HTTP(S)，限制请求/响应和超时，解析与拨号固定已验证地址，拒绝元数据/链路本地/回环/未指定/组播地址及重定向；允许用户明确需要的内网与 Docker 目标。

## 验收

Go 单元/并发/真实MySQL隔离表测试验证 CRUD/OCC/加密/备份、画布校验/JMX无损语义、代理认证隔离/地址边界。前端测试验证编辑、保存重载、冲突、原生组件与模式选择。实际浏览器验证 LogicFlow 编辑及 Scalar 中文客户端；端到端只使用本项目内部测试目标，不访问业务系统。新表与快照兼容旧数据，旧备份缺新域数组按空集合处理。

代理平台认证专用头为 X-JMeter-CSRF-Token，普通 X-CSRF-Token 属于目标业务请求并原样传递。已导入变量/请求头行保留隐藏 _source_id，编辑/删除/重排时保持扩展属性的条目归属。
