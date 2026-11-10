# JMeter MCP 实施计划

**Goal:** 将用户选定的 npm jmeter-mcp-server 接入现有 manager 并配置本机 Codex。

**Architecture:** 上游编排注册器 + manager API 工具；同容器 Streamable HTTP，home.yanww.top:18091/mcp 供 Codex 使用。

**Tech Stack:** Node22、jmeter-mcp-server0.4.5、MCP SDK、Python manager、Docker Compose。

**Spec:** ../specs/2026-11-05-jmeter-mcp-design.md

## 约束与审查

- 仅改 deploy/jmeter 与关联说明及本机新增 MCP 配置；当前部署资产尚未纳入 git，保留现有工作目录，不移动其他未提交内容，不自动提交。
- 不新增统计产物或常驻容器；用户已允许独立 MCP 端口；不发业务压测。
- 提交必须显式幂等键，脚本发布与任务执行分开，重试不会因再次发布产生不同任务请求。
- MCP 错误不泄露凭据；导入限制目录，计划 ID 不可穿越路径；HTTP 请求必须 Bearer 认证。
- 原始文件通过 manager 查询；安装过程不修改现有运行批次。

## 任务

- [x] 写真实 HTTP MCP 测试，覆盖工具发现、编排发布、六节点提交、幂等键、错误与草稿持久化；运行并确认失败。
- [x] 实现 controlplane/mcp 的依赖锁和入口、manager API 工具；测试通过。
- [x] 固定 Node 发行版与校验和，更新 Dockerfile.manager、Compose 环境、进程监督和文档；构建并仅替换 manager。
- [x] 备份 Codex 配置，新增 jmeter MCP；通过实际域名 HTTP 验证发现、查询与内部六节点执行/MinIO 原始文件归档。
- [x] 更新源码校验清单和部署说明，确认常驻容器/端口及无在途任务，记录验收。
- [x] 用户追加：master 页面迁移 Vue3，绑定 0.0.0.0；保留原功能，测试/构建及实际浏览器核验。

## 最新技术边界

用户最终明确：master 内自研代码前端 Vue，其他 Go。master 外 Java 执行代理沿用；JMeter 和 npm MCP 为官方第三方依赖。Go manager 用 MySQL 唯一在线存储，旧 SQLite 仅用于一次迁移。自研 MCP 适配从先期 JS 验证实现改为 Go，代理上游 Node stdio 编排工具。

- [x] Go manager、Go CLI、调度、归档与旧数据迁移：manager-go 工作包。
- [x] Go MCP HTTP 适配与第三方 Node 进程生命周期：mcp-go 工作包。
- [x] Vue3 全页面迁移，27项测试与构建通过；待真实部署浏览器验收。
- [x] UFW放通18090/18091，内网172.25.0.0/16到3306；监听0.0.0.0。
- [x] 本机Codex新增jmeter HTTP MCP配置及认证，配置权限600；待服务端上线连接验收。
- [x] MySQL jmeter账号已验证本机可用；容器来源授权待用户处理（1130错误，已询问）。
- [x] 所有Go产物部署、历史迁移、六节点/故障/归档及Vue浏览器验收。

用户追加页面：新增只读 MCP 工具信息页，实时读取已认证 /api/mcp/tools，展示54工具的中文功能及参数；全站界面文案中文化，技术标识与原始日志保持原值。

- [x] Go MCP目录查询API、认证/连接故障/响应边界测试。
- [x] Vue MCP工具页、54工具中文目录及全站中文化，测试和构建。
- [x] 联合部署并验证域名/API、目录一致性、页面交互及浏览器控制台。

## 最终验收

- Vue 36项测试及构建通过；Go manager与Go MCP race/vet通过，真实MySQL隔离表集成通过。
- 迁移7个已发布计划、31条任务、28份历史报告、57条幂等记录；原3694个历史报告文件SHA256保持一致。
- 真实域名MCP发现54工具；编排/发布/六节点执行及幂等重试通过，30条样本、6个来源均成功。
- 六节点/参数传递/manager重启/停止清理/控制端中断恢复验收通过；7批次21个MinIO原始对象与磁盘SHA256一致，无新报告目录。
- Go备份3808文件校验及v2恢复准备通过，归档收据置PENDING；异机实际导入需空MySQL目标。
- /api/mcp/tools认证与54工具目录实测通过；中文页面桌面和390×844交互通过，控制台无异常。
- 页面/API18090、MCP18091均绑定0.0.0.0并经home.yanww.top可达；10常驻容器，自测目标关闭。
- 旧Python master及JS适配验证代码已归档/移出活跃部署；master自研进程只有Go，Node只运行指定上游npm包。
