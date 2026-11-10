# JMeter 原始文件归档实施计划

> **For agentic workers:** Use test-driven-development for behavior changes and verification-before-completion before delivery. Independent ownership: manager, frontend, root deployment.

**Goal:** 原始 JTL/日志直传 MinIO，取消统计/HTML 生成，移除 reporter/gateway，manager 单实例，自测靶服务按需启停。

**Architecture:** 复用一个持久化 manager 和控制/执行池；manager 后台线程负责流式原始文件归档，跨进程锁保护归档重试与进程重启，界面展示归档和原始下载。

**Tech Stack:** Python stdlib、React/Vite、Java21/JMeter5.6.3、Docker Compose、MinIO。

**Spec:** ../specs/2026-11-05-jmeter-raw-artifacts-design.md

## Global Constraints

- 不生成新的统计 JSON、HTML 或分析 Markdown；不删除历史数据。
- 原始文件字节不变；按北京时间事件日期、run_id、raw 路径归档。
- 不连接业务系统发压；只使用内部自测目标。
- manager 不挂 Docker socket；采集/HQP/JVM 汇总仍为后续需求。

## Review Focus

- 大 JTL 上传是否全量进内存、阻塞管理心跳，哈希与上传是否一致。
- 主备交接、重试是否覆盖不同内容或丢失已上传收据。
- 无 JTL/部分日志时是否误报完整归档成功。
- 历史数据是否被新模式扫描、重算或删除。
- 停止/中断后是否仍然清理执行池，而不是只改文件归档状态。

## 任务

- [x] manager：先写失败测试，再实现原始文件元数据/流式归档/API、取消新报告生成、移除 reporter 健康依赖、适配单 manager、备份恢复收据；跑全套测试。
- [x] frontend：先写行为测试，再精简统计/对比/报告入口，提供原始文件与归档状态；跑测试与构建。
- [x] root：Compose 移除 reporter/gateway，合并为一个 manager 直接提供18090入口，smoke-target 加 selftest profile，管理/验收脚本改为原始文件校验；配置解析及 shell 检查。
- [x] root：备份当前源码与状态，确认无在途压测，部署并执行内部六节点/原始文件上传下载/故障恢复验收。
- [x] root：浏览器核验精简界面，更新需求与部署说明，资产审计，记录最终容器和原始对象收据。

## 验收记录（2026-11-05）

- 实际 Python 3.10 容器内 manager 测试 41 项通过；前端测试 19 项及构建通过。
- 内部验收覆盖六节点、参数传递、manager 重启续管、停止清理、控制端中断与下一批恢复；新任务不生成统计报告。
- 六批次的 18 个 MinIO 原始对象与磁盘文件、归档收据 SHA256 一致；新报告目录为 0。
- 备份校验 3786 个文件，恢复归档状态为 PENDING；3694 个历史报告文件校验不变。
- 10 个常驻容器健康，自测 HTTP 容器已停止；页面/API 保持本机 18090，MinIO 端口为 19000/19001。
- 浏览器桌面与 390×844 布局检查通过，页面无框架错误或控制台异常；未向业务环境发压。
