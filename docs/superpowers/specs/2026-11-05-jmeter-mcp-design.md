# Go master、Vue 与 JMeter MCP 接入设计

用户要求：master 自研前端使用 Vue，其他自研运行代码使用 Go，接入已有 MySQL，并将指定 npm juliodelimas/jmeter-mcp-server 集成进 master、配置到本机 Codex。master 外执行代理沿用原实现。

## 组成与接口

- Go manager：认证/会话、脚本不可变版本、幂等任务队列、调度、停止与故障清理、流式原始文件归档及管理 CLI。
- MySQL：已有 jmeter 库与本库账号，密码文件只读挂载；旧 SQLite 一次性导出并事务导入，旧会话不迁移。
- Vue3：任务与历史、脚本库、集群状态、MCP 工具；保留原版布局和 API，界面中文，技术标识和原始日志保持原值。
- Go MCP 适配：Streamable HTTP、Bearer 认证；代理官方 npm jmeter-mcp-server0.4.5 的45个编排工具，提供9个 manager API工具，共54个。
- Go父进程监督管理服务和Go MCP子进程；Go MCP监督第三方Node stdio进程，退出清理，异常退出触发重启。自研服务不再调用旧Python或自研JS服务。

## 访问与操作

master 页面/API绑定0.0.0.0:18090，MCP绑定0.0.0.0:18091。通过 home.yanww.top 域名访问，UFW放通两个TCP端口。Codex配置 mcp_servers.jmeter URL为 http://home.yanww.top:18091/mcp，使用已有令牌，私有配置权限600。

MCP流程：编排草稿→publish_test_plan发布不可变JMX→execute_test_plan提交managerPlanId和显式稳定幂等键→状态/日志/原始文件查询。默认六个worker，每个worker执行完整JMX。计划发布与执行分开，任务重试不会产生不同请求。

MCP工具页通过认证GET /api/mcp/tools读取实际目录。服务端只连接容器内MCP地址，限制总耗时和响应大小，失败返回安全中文状态，不返回认证凭据。页面展示连接状态、地址、协议、认证方式、工具数量、中文分类/用途/参数；属于信息页，不提供直接执行入口。

## 约束和验收

仅上传原始results.jtl、jmeter.log、console.log到MinIO；不新增统计JSON/HTML分析。旧READY历史只读，原始文件按北京时间开始日期、run_id和raw路径归档。流式上传使用无进展超时和不可变对象校验，持续传输不受总耗时截断。

幂等键使用精确UTF8字节存储，避免MySQL尾空格比较折叠。迁移保留任务/脚本/报告和幂等记录，备份不含会话和凭据；恢复导入空MySQL目标。

验收覆盖认证、协议发现、编排/发布、真实六节点内部自测、任务重试、停止、管理服务重启、控制端故障、对象校验、备份恢复及Vue桌面/移动布局。只请求Docker内部自测目标，不向业务环境施压。
