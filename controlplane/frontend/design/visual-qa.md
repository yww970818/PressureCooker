# Current raw-artifact scope

The prior report/comparison verification below is historical. The user-authorized simplification retains its visual system while removing comparison, statistics, report generation and reporter dependence. Current navigation has three items, task rows show raw_archive status, and details offer overview/log/raw files with authenticated original downloads. Manager deployment is a single instance.

Current automated verification: five expected feature failures were observed before implementation, then the full suite passed **19 tests**; production `npm run build` passed. Coverage includes single-manager/no-reporter readiness, absence of old navigation/metric selection, raw download metadata/path safety, missing/failed/empty archives, active-run download suppression, historical read-only links, session/CSRF, creation validation, polling focus and search.

Latest raw-artifact deployed desktop/mobile screenshots and original-byte download validation remain root's integrated QA responsibility. The historical screenshots below do not verify the current simplified source. No new visual concept was generated because the user requested the existing style be retained.

---

# Visual QA ledger

Accepted concept: `task-history-concept.png` (1487 × 1058), built-in Image Gen, generated before implementation and accepted by root. Tokens/layout rules: `design-system.md`. Concept examples never populate runtime UI.

## Final inspection

Frontend agent used `view_image` on the accepted concept and both latest real-backend Browser/IAB full-page captures:

- `outputs/jmeter-controlplane-2026-11-05/visual/task-history-desktop-final.png`: DOM viewport 1488 × 1058, PNG 1473 × 1226 (full-page content, scrollbar excluded).
- `outputs/jmeter-controlplane-2026-11-05/visual/task-history-mobile-final.png`: DOM viewport width 390, PNG 375 × 1514 (full-page content, scrollbar excluded). Root confirmed no page overflow.

No Playwright fallback was required. Earlier clipped and pre-fix captures were used only to find issues, not for final sign-off.

Inspected points:

1. **Shell:** restrained navy rail, outline pulse brand, four exact navigation labels, teal selected item and quiet account/connection footer follow the concept. Desktop rail is 224px per the explicit implementation requirement.
2. **Palette and surfaces:** cool gray workspace, true white tables/details, light 1px borders, small radii, teal primary actions and blue text links match the design system; no decorative charts or card grid.
3. **Hierarchy and density:** 28px heading, 30px desktop gutters, slim health band, 74px task rows, subdued IDs, tabular numeric values and semantic statuses preserve the table-led composition.
4. **Controls:** filter tabs, single-border search, now-visible 搜索 submit, refresh, checkboxes and pagination have consistent geometry. Root confirmed a pointer click narrows 17 rows to the two 界面验收 matches.
5. **Details and reports:** overview/log/report tabs, immutable version rows, compact report state, protected report/download actions and independent expandable MinIO archive detail use the same surface/typography family.
6. **Mobile:** navigation becomes a compact top rail, controls remain readable, main page stays within the viewport, and wide tables scroll internally. Expanded archive details determine their report row's height, including when those columns are horizontally offscreen.

## Copy diff and intentional differences

Main navigation, heading, subtitle, primary action, filter labels, table headings and execution/SLO disclaimer retain accepted copy. The visible 搜索 action was explicitly requested after browser QA. All names, IDs, dates, metrics, leader, queue and counts come from the API rather than concept examples. The connection footer uses a plain status dot and 管理服务已连接 instead of the concept's English ONLINE pill. Real selected-detail/report state replaces the concept's empty placeholder; the selection tray appears only when runs are selected. RPS in list/details uses two decimals with exact raw values in a tooltip; comparison retains exact values. MinIO archive detail is a requested extension of the report table.

Material issues found and fixed: overflowing long RPS values, toolbar search double border, hidden search hit area, polling stealing dialog focus, and insufficient width for six exact comparison columns. No material visual mismatches remain in the supplied current desktop/mobile captures. The implementation was faithfully verified against the accepted design with the intentional differences above.

## Behavior evidence

Root exercised the real integrated browser: token login; creation/start (18 samples, zero errors); graceful stop (STOPPED, 756 partial samples); two immutable report versions archived to MinIO; two-run comparison and baseline switch; visible search submission. Source has no demo dataset or browser token storage. Browser workflows use real same-origin API/session/CSRF routes.

Frontend verification: `npm test` — 17 tests pass; `npm run build` — passes. API compatibility adjustments required: none. Error rate ratio units, report archive states and ISO retry timestamps match manager responses.
