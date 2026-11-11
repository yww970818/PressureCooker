# Worker 独占与多批执行 Implementation Plan

> **For agentic workers:** Use superpowers:test-driven-development for each implementation task. Independent queue and executor changes can be delegated using superpowers:dispatching-parallel-agents; the primary agent integrates manager/frontend behavior and verifies the complete change.

**Goal:** 支持不同 worker 上的多批并行任务，并在提交时拒绝已占用的 worker。

**Architecture:** 复用管理端已有集群配置事务锁保护全局 worker 预占。Redis 对完整 worker 集合原子加租约；Core 分任务并行调度，保留进程、停止与恢复隔离。

**Tech Stack:** Go、MySQL、Redis Lua、Vue、Vitest。

**Spec:** `docs/superpowers/specs/2026-11-11-worker-exclusive-runs-design.md`

## Global Constraints

- 不修改线上对象，不启动真实业务压测，保留其他协作修改。
- worker 在排队、执行和清理阶段独占，上传与回调阶段可释放。
- 幂等重放先于占用校验；跨项目只公开 worker 标识。
- 旧全局租约与新 worker 租约互斥，迟到续租/释放不得影响新持有人。

## Review Focus

- 同时提交重叠 worker 只能成功一次，非重叠集合应均成功。
- worker 只接受覆盖自身的有效租约；无法部分取得一组 worker。
- 同一 Core 并行运行、停止和清理必须相互隔离。
- 故障恢复等待当前 epoch 上报，不受无关任务占用阻塞。
- 已选节点在界面打开后被占用时阻止提交，服务端仍为最终判定。

### Task 1: Redis worker 集合租约

**Files:** `controlplane/execution/queue/queue.go`、`node_reports.go` 及测试。

**Interfaces:** `Acquire(ctx, token, workers ...string)`；保持 `LeaseOwned/Renew/Release`；新增 `OwnsWorkers(ctx, token, workers ...string)`，验证租约覆盖所请求 worker。

- [x] 先写并运行失败测试：独立集合并行、重叠拒绝、原子失败无残余、续租/释放 fencing、全局兼容、epoch。
- [x] 用原子 Redis 操作实现租约并验证定向测试。

### Task 2: Core 并行与 JVM 隔离

**Files:** `controlplane/execution/cmd/jmeter-executor` 的 runner、process、isolation、worker 及测试。

**Interfaces:** 使用 Task 1 的 Acquire/OwnsWorkers；按任务维护执行状态，分配独立停止端口与隔离身份。

- [x] 先写并运行失败测试：同 Core 多任务调度、重复消息抑制、独立停止/清理、worker 拒绝外部租约。
- [x] 实现并发调度、逐任务隔离、租约资源校验，运行 executor 测试与 Linux 编译验证。

### Task 3: 提交预占、领取与恢复

**Files:** manager-go 的 store、runner_http、runner_recovery、clusters、node_inventory 及相关测试。

**Interfaces:** 全局占用查询与 `worker_busy`；cluster worker 投影增加 occupied；领取验证资源租约；恢复取得自身 worker 集合租约。

- [x] 先写并运行失败测试：跨项目/集群重叠提交、非重叠并行、幂等重放、并发提交、各占用状态与释放。
- [x] 实现事务校验、状态投影与资源级恢复，调整既有夹具以显式反映新提交契约。

### Task 4: 创建界面与交付验证

**Files:** `controlplane/frontend/src/CreateRun.vue`、`CreateRun.test.js`、README 和部署文档。

- [x] 先写并运行失败测试：忙碌节点不可新增勾选，已选择忙节点不可提交，可取消选择后提交。
- [x] 更新界面与全局串行说明，运行前端测试及构建、Go 定向测试与编译。
- [x] 对最终差异进行独立代码复核，修复实质问题并重新验证。

## 验证记录

- 管理端默认全量 Go 测试、三个 Go 模块的编译检查通过。
- 管理端 worker/集群/投递/恢复整组隔离测试通过；跨项目并发提交、领取、恢复的 race 检查通过。
- 环境/集合相关 56 项和项目/幂等/复制/回调相关 36 项隔离测试通过，旧夹具显式停止排队任务或改用独立 worker。
- 执行器完整 Redis/MinIO 隔离集成测试通过；并行生命周期与租约 race 测试通过。Linux amd64 测试二进制可编译；本机未运行 Linux PID 1 隔离测试。
- 独立代码复核发现的旧重叠队列互阻和失联任务阻塞其他恢复问题均已用失败回归复现并修复。
- 前端生产构建通过，124 个测试文件 / 1019 项测试通过；全量前端测试使用单 worker 和 15 秒用例期限，避免本机默认 5 秒期限下接口管理已有慢用例超时。
