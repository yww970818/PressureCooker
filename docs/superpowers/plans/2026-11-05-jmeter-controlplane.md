# JMeter管理服务与六节点部署执行计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for integration; independent manager, agent and frontend tasks follow test-driven-development with disjoint ownership.

**Goal:** 六执行节点、双控制端、管理服务主备及真实历史/重建/对比报告，部署home.yanww.top。

**Architecture:** Python标准库服务+SQLite/flock/监督父进程，Java代理管理JMeter子进程，独立报告节点，React管理界面，Nginx回环入口。

**Tech Stack:** JMeter5.6.3、Java21、Python3.10+、React/Vite、Docker Compose。

**Spec:** ../specs/2026-11-05-jmeter-controlplane-design.md

## Global Constraints

- 单机主备，整机故障通过归档恢复，不声明异机自动切换。
- 6个执行节点，一个全局执行批次；容器内自测，不访问业务环境。
- 主备不重放在途压测；控制器故障必须复位执行节点并标中断。
- API与Java代理独立令牌、无Docker socket、无任意Shell接口。
- 脚本快照/JTL/已发布报告不可覆写；报告有版本，跨批次必须提示配置差异。

## Review Focus

- 管理主进程或调度线程卡死时，OS锁能否释放且备用不重复启动。
- 控制器消失后，远端JMeter线程不能继续施压；过时reset不伤及下一批。
- CSV无样本、错误数字、部分末行和大样本分位数不能输出伪健康值。
- 生成新报告、历史导入、恢复时不能覆盖既有原始记录。
- UI/API错误、CSRF、路径与幂等行为需要实际调用验证。

## 执行任务

- [x] 恢复现有部署定义与历史数据；确认单机主备范围；备份当前配置。
- [x] manager：先红后绿验证DB/API/统计/对比/恢复，完成CLI、监督和备份。
- [x] agent：先红后绿验证进程/锁/幂等复位/路径/中断，完成Java代理。
- [x] frontend：概念图、设计令牌、React实现、表单与状态行为测试、构建。
- [x] root：构建Python/Nginx基础镜像，组合六worker、双controller、双manager、reporter及gateway。
- [x] root：备份/迁移原始两节点历史与密钥，部署新服务，配置SSH隧道访问。
- [x] root：API鉴权、六节点自测、停止、主备故障、报告重建/历史与比较端到端验收。
- [x] root：浏览器桌面/移动交互与概念对照；备份空目录恢复检查；文档、资产登记、交付。

- [x] 用户追加 MinIO：发布 19000/19001，按上海事件日期上传不可变 JSON、失败重试，并验证下载 SHA256 与存储故障恢复。
- [x] 最终代码复核修复未接受复位意图与 worker 自重启竞争；新增跨领导切换回归。
