# Monitoring Write Implementation Plan

**Goal:** MCP写入指定报告的中间件/JVM监控，版本化保存并在在线和离线报告展示。
**Architecture:** Append-only monitoring snapshots in reserved pressure_report_metrics, manager owns validation/scope/idempotency, MCP typed forwarding, Vue renders explicit monitoring resource.
**Tech Stack:** Go, MySQL, Vue3, official MCP runtime.
**Spec:** docs/superpowers/specs/2026-11-07-monitoring-write-design.md

## Constraints and Review Focus

Preserve shared uncommitted workspace; no blanket staging/commits or worktree of empty tracked baseline. Runtime artifacts/secrets stay runtime, no real pressure executions. Core report data/verdict stays immutable; snapshot revisions and idempotency are independent per report. Missing/null stays unknown; external health statements do not become JTL verdicts. Writes must reject wrong project/archived/nonREADY report and non-overlapping collection windows. New report reads must not inflate list/compare payloads. Guard strings and offline rendering against credentials and script injection.

- [x] Task1 manager: new pressure_report_monitoring.go/tests; failing pure validation and isolated MySQL API tests first. Implement bounded validation/redaction, append-only revision/idempotency transactions and GET history. Minimal pressure_report_http.go route + detail metadata, openAPI discovery. Verify projection/portable backup handling with existing table.
- [x] Task2 MCP: new monitoring_tools.go/tests; write failing exact HTTP/schema tests then implement two writes plus read provider/handlers. Root registers in manager_tools.go, updates version and regenerates real catalog fixture. No execution tool or transport expansion.
- [x] Task3 UI/offline: MonitoringCards+focused tests, Reports reportId, source/metric/trend/empty/error rendering; offline template section/textContent charts and tests. Update MCP catalog Chinese fields/notes/examples; do not alter backend projection contract.
- [x] Task4 integrate/review (本地代码/测试/审查完成，等待统一发布后线上验证): docs, targeted tests and all-package compile, frontend build, real MCP fixture equality, independent code review and fixes. Use stable current source build and guarded manager-only deployment if idle, verify internal test fixture through MCP and desktop/mobile UI; preserve other containers/config and original business reports.

Interface ledger: manager GET snapshot is consumed by UI/MCP/offline with exact field names from spec; report.monitoring only summary. Snapshot entries each component+instance replace atomically. Root owns backend routes/detail/export attachment and shared MCP registry/version/fixture; UI owner edits only offline HTML constant, not Go export function.

后续实际线上验收已完成：20261107-monitoring-live（5类）与20261107-monitoring-scale（200JVM），详见runtime/acceptance对应记录。
