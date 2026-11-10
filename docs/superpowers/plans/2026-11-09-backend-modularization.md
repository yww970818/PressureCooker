# Backend Modularization Implementation Plan

> **For agentic workers:** Execute independent package boundaries in parallel; integrate and review once at the end. Existing shared edits and historical data remain intact.

**Goal:** Resolve the six identified backend architecture problems through real package boundaries, process dependencies, request cancellation, typed domain/storage models and separated scheduling policy.

**Architecture:** Keep the existing Go modular monolith and HTTP/MCP contracts. Extract independent `internal/persistence`, `internal/nodes`, `internal/tasks`, and `internal/workspace` packages; main composes them. Current encrypted JSON and immutable snapshots remain opaque durable payloads; hot query fields and state transitions become typed.

**Tech Stack:** Existing Go, database/sql, MySQL, Redis and Vue; no added framework or schema migration.

**Spec:** User approved the six-item audit and instructed “开始修复解决上面问题”; existing APIs, Core-only reporting, project isolation and historical formats are binding.

## Global Constraints

- Preserve collaborators' existing changes, including the newly discovered Redis pool/Platform.Close implementation; verify and improve it rather than overwrite it.
- No initial git commit exists. Use runtime source snapshots/hashes and explicit file ownership; do not mass-stage or commit shared files.
- No real business pressure, new protocol execution, data reset or historical snapshot rewrite.
- Core reports remain the only node-state source, 5s reports/20s freshness/24h retention and causal observation epoch retained.
- Internal IDs, existing compose identity, encrypted payloads, canonical serialization and schema7 unchanged.

## Review Focus

- Project-scoped Platform copies share pools but not cancellation or mutable project state.
- Request-handle Close never kills a process pool; process shutdown is idempotent, concurrent access safe, failed/changed dependency config cannot reuse an unrelated connection.
- Cancelled requests waiting for one SQL connection, nested transactions or object downloads terminate promptly and preserve 499/504 error mapping.
- Node stale/queue-down/unreachable selection and epoch evidence survive extraction without manager probes or private map leaks.
- Typed repository boundaries preserve exact opaque payload bytes, project isolation, locks, CAS/history and single-connection list behavior.

## Tasks

### 1. Process infrastructure and request cancellation (root)

Files: `dependencies.go`, `database.go`, `http.go`, `projects.go`, `main.go`, `diagnostics.go`, new `internal/persistence/*`, request context adapter and focused tests.

- [x] Test queue pool reuse/concurrent handle closure/process shutdown/config mismatch and standalone ownership against private Redis.
- [x] Test cancellation of legacy Query/QueryRow/Exec and transaction pool waits through a request-bound database view; scope isolation and existing prefix behavior.
- [x] Move database/transaction context and prefix concerns into persistence, leaving compatibility aliases in main.
- [x] Bind authenticated request Platform to request context. Legacy transaction wrapper inherits that context. Retain explicit deadline methods.
- [x] Complete pooled dependency ownership and object-store cancellation; production shutdown after loops finish.

### 2. Typed node registry domain (node owner)

Files: `node_inventory.go`, `node_registration.go`, new `internal/nodes/*` and package/root tests. Do not edit dependency/database/main/store files.

- [x] Characterize missing, stale, queue disconnected, old worker sample, cross-Core fresh and newest unreachable selection.
- [x] Extract typed inventory projection/service and narrow report-store interface; package must not import main or issue HTTP probes.
- [x] Keep HMAC and HTTP decoding at handler boundary; main adapts typed results to existing API exactly, including absence/null fields and private cluster evidence.
- [x] Run node protocol/root tests and package race tests, record red/green and source hashes.

### 3. Task state and persistence boundary (task owner)

Files: `store.go` task functions, `scheduler.go`, `runner_recovery.go`, new `internal/tasks/*` and tests. Do not alter Platform struct/initialization or dependency/database files.

- [x] Extract typed Status and transition/recovery policy from ad hoc string/map decisions; preserve terminal, stop/recovery/late callback behavior.
- [x] Introduce typed task Record/Repository for recurring row load/list/save queries, passing opaque JSON bytes and project fields unchanged.
- [x] Separate legacy archival polling from active scheduling reconciliation into focused owner code; preserve rate/retry behavior and no node probes.
- [x] Change legacy transaction wrapper to use `p.requestContext()` (root supplies method); deadline-aware recovery stays bounded.
- [x] Run state, outbox, scheduler/recovery and MySQL compatibility regressions without a real engine task.

### 4. Workspace repository boundary (workspace owner)

Files: `workspace_store.go`, workspace read helpers in `page_read_models.go`, new `internal/workspace/*` and tests. Do not alter encryption functions/serialization or other owners' files.

- [x] Add typed Record/Version/Summary storage models and a narrow SQL interface with allowed table mapping, project filtering and bound parameters.
- [x] Move recurring workspace get/list/version/write SQL into repository; keep AES-GCM, AAD, CAS, immutable versions and projections unchanged.
- [x] Return materialized rows after cursor close; preserve summary pagination and single-connection behavior, avoid per-row fallback queries where batch retrieval is possible.
- [x] Run workspace/project/environment/plugin/summary isolation and cancelled-request tests, no production database.

### 5. Integration, contracts and documentation (root)

- [x] Review all package boundaries and remove duplicated/dead compatibility helpers whose callers migrated; do not change APIs to satisfy tests.
- [x] Run manager full suite and changed MySQL cases; all three modules compile; execution/MCP contract suites as affected. Build fresh Linux artifacts; no frontend behavior change.
- [x] Fresh independent whole-change review, resolve actual findings with regression tests.
- [x] Document dependency lifecycle, package ownership, request cancellation and repository query boundaries; report remaining limitations honestly.
- [x] Build current-source release only after tests pass; preserve deployment identity and idle/lease guards if releasing, verify new endpoints/pages without business pressure.

验收说明：本轮完成本地代码重构、定向和全模块验证、独立复核、冻结源码的 Linux 及前端构建。本次用户未要求部署，未触碰服务器。冻结快照含共享工作区其他会话的界面/Compose改动，仅作为可复核构建记录，不视为已发布版本。
