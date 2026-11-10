# History Deletion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 在两个列表完成单条、批量和当前项目清空并部署定向验收。
**Architecture:** Go事务负责项目隔离和关联删除，持久化清理意图负责有限范围的产物重试；Vue复用删除确认与选择状态。
**Tech Stack:** Go, MySQL, Vue, Vitest。
**Spec:** docs/superpowers/specs/2026-11-09-history-delete-design.md

## Global Constraints

- 只删除当前项目；归档项目只读；保留共享资源、密钥及发布记录。
- 请求/响应契约遵循设计；不启动真实业务压测。
- 工作区无HEAD且多人共享，不能提交整个未跟踪目录；保留改动前副本，发布从冻结的当前源码新构建。

## Review Focus

- 活动任务、跨项目混合批量必须原子拒绝。
- 报告worker和旧回调不能复活删除记录。
- 清空忽略筛选分页，保留活动任务，UI清楚告知。
- 对象清理失败可重试且不能触碰共享plans/datasets或越界路径。
- 选择状态、取消、失败和项目切换不能产生多余删除请求。

### Task 1: Backend deletion
**Files:** manager-go history deletion files, schema migration, HTTP discovery, scoped integration tests; execution/objectstore only if required for task-prefix cleanup.
**Interfaces:** DELETE /api/{runs|pressure-reports}/{id}; POST /api/{runs|pressure-reports}/{delete|clear}; body/response as spec.
- [ ] Write failing focused HTTP/store tests for scope, state, cascade, worker race, cleanup retry/path protection.
- [ ] Implement transactional deletion and durable artifact cleanup, wire background lifecycle and API discovery.
- [ ] Run new targeted tests, compile affected modules, record evidence.

### Task 2: Frontend controls
**Files:** Runs.vue, reports/Reports.vue, reusable history deletion composable/component and tests.
**Interfaces:** Consumes Task1 APIs. Current-page select, labels 删除/批量删除/一键清空; confirmation scoped current project.
- [ ] Write failing UI tests for six actions, cancellation, activity/archived guards, filter/pagination and errors.
- [ ] Implement accessible row controls, selection/confirmation and list refresh/reset.
- [ ] Run new tests and build; inspect deployed page using Playwright fallback.

### Task 3: Review, deployment and focused acceptance
- [ ] Review combined delta with baseline copies, fix material findings.
- [ ] Freeze current source, rebuild manager and frontend, preserve explicit target compose IDs and all secrets/volumes.
- [ ] Deploy under existing manager lock and execution lease after idle check.
- [ ] Create isolated synthetic history fixtures without task submission and exercise six actions on live pages; verify APIs/cascade, cleanup and resource preservation.
