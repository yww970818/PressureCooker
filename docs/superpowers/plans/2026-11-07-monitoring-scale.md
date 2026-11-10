# 百级监控实施计划

**Goal:** 支持上百JVM实例批量写入及在线/离线有界展示，并完成200实例实际验收。
**Architecture:** 复用版本化快照表，单事务多实例替换，GET可选分页；MCP批量工具，Vue与离线HTML按组件分页选择单实例详情。
**Spec:** docs/superpowers/specs/2026-11-07-monitoring-scale-design.md

- [x] 后端：pressure_report_monitoring.go/test.go扩容1000/8MiB，批量原子写入/只增一个revision/稳定结果回放，GET筛选分页；pressure_report_http.go最小route、http.go OpenAPI；无新迁移，保留旧单写契约。先失败测试后实现，隔离MySQL定向。
- [x] MCP(root)：监控schema/provider新增batch并调整paged read、边界/转发测试，版本.5/真实fixture120项、文档。单写传输边界不变。
- [x] UI/离线：MonitoringCards实例搜索/5条分页/单选详情，实际离线JS同行为，当前5实例历史验收兼容，百级DOM/交互测试，中文MCP说明。
- [x] Root集成：代码审查、定向测试/编译/前端构建、稳定源码Linux产物、空闲/CAS管理端发布；用真实MCP一次写200JVM（替换此前JVM+新增199），保留4中间件，验证总览/监控/离线及手机，记录版本与未改核心结果。

约束：保留共享未提交改动；所有测试数据明确模拟标识，runtime留痕、不打印密钥、不提交运行数据，不触发真实业务压测。历史单写快照与请求回放保持兼容；新增批量允许回传摘要但不回传百条时序。

验收完成：runtime/acceptance/20261107-monitoring-scale/README.md，实际200JVM+4中间件、revision6、总览/监控/离线/手机通过。
