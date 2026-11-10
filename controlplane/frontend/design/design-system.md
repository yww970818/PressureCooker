# Current scope: original artifacts only

The user-authorized raw-artifacts simplification supersedes feature/copy references below. Retain existing navy/white/teal tokens, typography, spacing, table surfaces and responsive behavior. No new concept generation or restyling is needed.

- Navigation: 任务与历史 / 脚本库 / 集群状态.
- Task table: 任务名称 / 状态 / 执行节点 / 控制器 / 原始文件归档 / 创建时间 / 操作. No comparison selection or statistics columns.
- Detail tabs: 概览 / 执行日志 / 原始文件. Raw metadata includes MinIO prefix/object key, exact byte size, SHA256 receipt, archive status, missing files, errors and automatic retry information.
- Only terminal whitelisted JTL/log files have authenticated download links; running tasks use log tails. Existing READY report links, when present, are labelled 历史产物（只读）.
- Cluster: one healthy manager, two controllers, six workers. No reporter, standby or gateway requirement in the UI.
- Original concept and prior feature notes below are historical style references, not current product requirements.

---

# JMeter Console design contract

Primary screen: `task-history-concept.png` (1488 × 1058, built-in Image Gen). A full native app screen, not an app raster background. All screen text, icons, state, tables and controls are code-native. No CDN or image dependency is required by the runtime.

- Color lock: dark navy sidebar `#0f1b2d`; cool light gray workspace `#f5f7fb`; true white surfaces `#ffffff`; text `#172033`; muted text `#667085`; dividers `#dce2eb`; teal `#009b9b`; blue links `#1765c1`.
- Layout: 224px fixed sidebar, 56px topbar, 28–32px main gutters, 24px section gaps, 72–76px task rows. Table surface has 8px radius and 1px border. No cards nested in cards and no marketing hero.
- Type: system Chinese font (`-apple-system`, `BlinkMacSystemFont`, `Segoe UI`, `PingFang SC`, `Microsoft YaHei`, sans-serif), 28px/700 page heading, 18px section title, 14px body/controls, 12px metadata; tabular numerals for all data, monospace for IDs/logs/hashes.
- Buttons: teal filled primary, white bordered secondary, blue text tertiary; 38px regular control height, 6px radius; clear disabled/focus/hover states.
- Navigation: 任务与历史 / 脚本库 / 集群状态 / 结果对比. Active nav has translucent teal background. Side footer contains API connectivity, 管理员, 退出.
- First viewport exact copy: 任务与历史; 管理执行批次，查看原始结果与报告版本。; 新建任务; 全部任务 / 执行中 / 已完成 / 已中断; 搜索任务名称或 ID; 刷新; 任务名称 / 状态 / 执行节点 / P95 / P99 / RPS / 错误率 / 创建时间 / 操作; 任务详情; 概览 / 执行日志 / 报告版本; 完成仅表示框架执行结束，业务正确性与 SLO 需单独验收。
- Icon family: inline SVG outlines, 20px, 1.7px stroke, rounded ends. Nav: clipboard, code, server, compare arrows; controls: plus, refresh, search, chevrons; simple pulse-square brand. No third-party icon font.
- Healthy strip is a single horizontal band; task table and selected-run tray are the primary components. Green semantic status for completed/healthy; amber for stopped/interrupted/recovering; red for failed/offline; teal for active.
- Extend required secondary screens using the same rail, toolbar, table, form and status primitives. Create task is a 640px modal with labelled fields, immutable script dropdown, worker checkboxes, multiline global properties, notes, and visible validation. Report versions stay in a table. Comparison uses a baseline dropdown, warning band and metrics table rather than approximate chart encodings.
- Responsive: sidebar becomes a compact top navigation; main gutter 16px; table scroll stays inside its surface; modal max-height with body scrolling; no horizontal page overflow at 390px. Detail metrics wrap into two columns. Controls remain 38px minimum.
- Motion: 140ms color/background transition; subtle loading spinner only while real fetches occur; respect reduced-motion.

## Data and interaction constraints

Only API values populate the page; the concept's row names are layout examples, never shipped runtime data. Missing metrics are `—`, never healthy zeros. Exact API numbers appear without aggregation or percentile averaging. Submission and report regeneration use stable idempotency keys per attempt. Bearer tokens never enter storage. Cookie session writes carry X-CSRF-Token. Compare selection is 2–6 historical runs with latest READY reports. Baseline, context warnings, partial status and server analysis remain visible. Reports open protected same-origin HTML routes. Any missing or offline state is explicit.

## Visual QA ownership

Root agent owns real-backend browser workflow/viewport testing and screenshot capture; frontend agent compares that returned screenshot with the persisted concept via view_image before handoff. Compare sidebar dimensions/nav color, cool workspace and white surface color, heading hierarchy/gutters, table typography/row spacing/statuses, toolbar/selection tray and detail panel. Intentional differences may only arise from real API data and empty/error states.
