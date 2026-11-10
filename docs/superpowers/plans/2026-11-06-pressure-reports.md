# 压测报告入库与中文报告页 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 结束回调后可靠解析原始 JTL/日志到 MySQL，为非技术人员提供可解释、可追溯、按每任务目标判定的中文报告，并将任务详情改为大弹窗。

**Architecture:** manager 的事务登记持久化分析作业；单后台 worker 下载并校验来源，纯 Go 解析器输出结构化指标，数据库在领取代次保护下原子发布报告版本。Vue 页面读取报告 API；原件仍在 MinIO，旧 HTML 报告路径不变。

**Tech Stack:** Go、Vue 3、MySQL、MinIO、Vitest、现有 Chrome/Playwright；不增加服务。图表优先使用轻量 SVG。

**Spec:** `docs/superpowers/specs/2026-11-06-pressure-report-design.md`

## Global Constraints

- 前端 Vue、后端 Go、MySQL 和 MinIO；不生成 JMeter HTML，不新增 reporter。
- 每任务目标可选，未配置不判定。目标不发送给 JMeter，不改原任务实际执行结果。
- 新表先通过表名前缀离线测试，集成测试仅用独立前缀，不读取其他会话的 Cookie/token。
- 当前 home 正在授权 QA 联调，本会话不部署、不重启、不启动业务压测；最终由原主会话统一发布。
- 原始来源不可改写；报告版本、任务目标、解析器版本及证据可追溯。所有接口按项目隔离。

## Review Focus

1. 重复或冲突回调、过期领取者及旧重解析都不能重复或覆盖新报告。
2. 事务/合并采样、节点覆盖不明、坏行、空文件和 FAILED/STOPPED 不得出现错误绿色；百分位目标用精确阈值计数。
3. CSV 引号/换行、毫秒/秒、起止时间和跨节点统计使用一致口径，零时长返回空值。
4. JTL 与日志中的密钥和个人文本先脱敏再限长；错误不回显原行；来源只允许当前任务已登记对象。
5. 备份包含全部关联表，恢复 READY 报告可读；MinIO 原件缺失必须明确说明，不能假装原件已恢复。

## 冻结 API 与结构

以下 URL 都在现有 `/api` 下，沿用当前项目头、会话/CSRF和错误格式。

- `GET /pressure-reports?run_id=&search=&status=&verdict=&limit=20&offset=0` → `{items:[ReportIndex],total,limit,offset}`。
- `GET /pressure-reports/:id` → `ReportDocument`，只返回一个固定版本，不混读 latest。
- `GET /pressure-reports/:id/steps?limit=20&offset=0`、`/issues?...` → `{items,total,limit,offset}`；支持按失败/耗时排序，不返回无限日志。
- `GET /runs/:id/pressure-report` → 最新报告或 404 `pressure_report_missing`。READY 版本单独可读，重解析失败不会删除旧 READY。
- `POST /runs/:id/pressure-report`，必需稳定 `Idempotency-Key`，body `{rebuild?:boolean,report_targets?:Targets}` → `ReportIndex`；历史任务生成、显式重新解析和测后新评审均使用此入口并生成版本。
- `GET /pressure-reports/compare?left=:id&right=:id` → `{left:ReportDocument,right:ReportDocument,differences:[...],warnings:[...]}`；两份均须属于当前项目，条件不同显示警示。
- `POST /runs` 增加可选 `report_targets:Targets`。MCP `submit_console_task` 对应可选 `reportTargets`。

`Targets` 的字段全部可选、JSON 数值：`min_rps`、`min_success_rate`（百分比0–100）、`max_p95_ms`、`max_p99_ms`、`min_duration_seconds`。缺省不等于0。任务创建冻结；测后标准只进新报告评审版本。

`ReportIndex`：`id,project_id,run_id,version,status,created_at,finished_at,run_name,run_status,environment_name,plan_name,verdict_status,targets_origin,previous_ready_id,error`。状态 `QUEUED/PROCESSING/RETRY/READY/FAILED`。

`ReportDocument`：上述字段加 `run`（非敏感历史快照）、`metrics`、`targets`、`verdict`、`quality`、`sources`、`timeline`、`steps_preview`、`issues_preview`、`findings`、`extensions`、`parser_version`、`schema_version`。

`metrics`：`sample_scope,sample_count,success_count,failure_count,success_rate,duration_seconds,rps,mean_ms,min_ms,max_ms,p50_ms,p90_ms,p95_ms,p99_ms,bytes_received,bytes_sent,transaction_count,percentile_method`。不可用字段为 null。

`verdict`：`status` 为 `NOT_SET/PASS/FAIL/INCOMPLETE`，含 `label,explanation,checks:[{key,label,target,actual,unit,status,detail}]`。未设置标准时 NOT_SET；其余需任务 COMPLETED、有效完整 JTL、可靠样本范围/节点覆盖及目标字段可用。日志完整度单独披露，不以日志空/缺失推断“无异常”。

`quality`：`metric_status,log_status,invalid_rows,warnings,expected_nodes,observed_nodes,node_coverage,scope_confirmed`。`node_coverage=COMPLETE/PARTIAL/UNKNOWN`；记录分类可靠时 `sample_scope=ordinary_samples`，事务另计；不可靠时 `sample_scope=sample_records` 且禁止整体 PASS。

`timeline` 每桶：`timestamp_ms,width_ms,sample_count,failure_count,rps,mean_ms,p95_ms`。`steps` 每项：`id,label,sample_kind` 及与整体一致的统计字段。`issues` 每项：`id,source_kind,source_name,category,level,label,message,count,first_at,last_at,line_from,line_to,step_label`。`sources` 含 `id,kind,name,status,bucket,key,sha256,size,valid_rows,invalid_rows,download_url,error`。

`findings` 每项：`origin,level,title,detail,recommendation,evidence_refs`；首版只使用 `origin=rule`。`extensions` 披露 JVM/中间件/K8s/AI 当前是否已有来源，不伪造指标。

## Task 1: 详情大弹窗（独立 UI 调整）

**Files:** `frontend/src/Runs.vue`、`RunDetail.vue`、`Runs.test.js`。

- [x] 原先列表下详情及空占位移除，改为接近视口的大 Modal。
- [x] 概览、文件、日志、停止入口仍可用；嵌套停止弹窗不误关父层、不跳出焦点。
- [x] 先红后绿；主会话复跑 `npm test -- src/Runs.test.js` 6/6 PASS。
- [x] 桌面1440×1000、390×844隔离浏览器验证通过，无写请求与控制台错误。
- [x] 最终与报告入口合并审查，原主会话统一发布并完成只读验证。

## Task 2: 纯 Go 报告解析与判定

**Files:** 新增 `manager-go/reporting/{model.go,targets.go,jtl.go,logs.go,redact.go}` 及其测试；worker独占本包，不改 manager 集成文件。

**Interfaces:** 导出 typed `Targets,Metrics,Quality,Step,Point,Issue,Finding,Analysis,ParseOptions`；`ParseJTL(context.Context,io.Reader,ParseOptions)(Analysis,error)`；`ParseLogs(context.Context,io.Reader,string,func(string)string)([]Issue,error)`；`Evaluate(Metrics,Quality,Targets,string)Verdict`；`NewRedactor([]string)func(string)string`。结构 JSON 标签按上面的冻结契约；目标阈值精确计数可保存在 Analysis 内部/质量字段供 Evaluate 使用，不通过近似值判断。

- [x] 写固定 CSV、引号/多行/BOM/坏列、断言失败、时间边界、跨节点及事务样本测试并观察失败。
- [x] 实现流式解析、有界统计和分位方法标识；限制标签/分桶/聚类规模，超限显式报告，不静默丢样本。
- [x] 按原始样本累加目标耗时阈值计数，测试P95/P99边界与近似展示值不一致时仍正确判定。
- [x] 测试目标缺省/null/0、非有限值/越界拒绝、非完成任务、节点覆盖缺口及无目标状态。
- [x] 测试日志堆栈、相邻事件、多行摘要、敏感值在标签/响应消息/日志/错误中均被脱敏。
- [x] 运行纯包定向测试；交付类型和 API 示例 JSON，冻结后由根会话接入。

## Task 3: 持久化作业、入库与 API

**Files:** 新增 `manager-go/pressure_report_{schema,store,jobs,http}.go` 及测试；修改 `database.go/store.go/runner_http.go/scheduler.go/http.go/projects.go/admin.go` 的必要接入。

- [x] 先补全部新表的前缀重写测试，再加入白名单与schema，不先连接数据库试错。
- [x] 建立 `pressure_reports/jobs/sources/steps/timeline/issues/metrics/findings` 结构化表和索引，备份列/关联同步纳入；固定summary字段有独立列。
- [x] 回调事务只登记作业，不下载；唯一键含执行批次/来源/解析与评审版本，重复回调仍幂等。
- [x] 后台单作业领取与续租，完整下载并验证大小/SHA256，再调用 reporting 包；有界重试，领取代次约束所有写入。
- [x] 子表与READY发布同事务，保留旧版本；新旧任务乱序完成及过期worker无法覆盖。
- [x] 完成报告列表/详情/步骤/问题分页、手动生成/重解析、两报告对比和目标版本接口；项目隔离覆盖所有子表。
- [x] 测试回调重复/冲突、校验失败、缺文件/无样本、失败重试、版本隔离、目标幂等、跨项目访问和备份恢复。
- [x] 数据库测试仅使用独立前缀；不部署或触碰真实任务/服务生命周期。

## Task 4: 中文报告页面与每任务目标

**Files:** 新增 `frontend/src/reports/` 下页面、详情、图表、目标字段及测试；修改 `Workspace.vue`、`RunDetail.vue`、`CreateRun.vue` 和必要 `Runs.vue` 转发事件。由 UI worker 独占，保留 Task1 的 Modal 功能。

- [x] 使用已批准设计和既有可选主题完成视觉方案；首屏结论/关键数字/目标对照，下面为趋势/步骤/问题/证据，术语用可读中文解释。
- [x] 报告列表搜索/筛选/分页、生成中/解析失败/缺数据/无目标状态；两份报告对比显示条件差异。
- [x] 图表显示单位和数据缺口，95%解释不等同95%用户；原始文件可鉴权下载。
- [x] 创建任务加入可选目标区，清空可省略；纳入草稿dirty与幂等重试快照，不放入 JMeter properties。
- [x] 任务详情增加报告页签或入口，转至报告页时关闭详情Modal，按项目重置选择。
- [x] 定向测试所有状态与目标输入；完全mock API浏览器验证桌面和390px，无业务请求。

## Task 5: MCP、文档与整体验证

**Files:** `mcp-go/manager_tools.go` 的 submit schema/payload及定向测试，必要MCP目录夹具；`deploy/jmeter/README.md` 和相关使用文档。

- [x] `submit_console_task` 接受 `reportTargets` 并传给任务 API，沿用相同校验与幂等；更新工具目录测试。
- [x] 文档说明报告数据口径、目标、重新解析、原件保留与未来扩展；不把压测端日志称为被测服务日志。
- [x] 集成审查代码与定向测试，不重跑无关业务用例；验证原任务状态、原始文件及旧报告不受影响。
- [x] 交付精确文件与源码SHA给既有主会话，待当前QA联调结束后统一发布；不自行重启home。
- [x] 原主会话确认发布后资源健康、报告页面/API/MCP正常；根会话复核公共资源与滚轮配置。JVM/中间件/AI仍仅预留扩展。

当前状态：2026-11-06 已完成实现、定向测试和独立复核；见同目录 pressure-report-handoff.md。统一发布与只读验证由原主会话完成，发布回执见2026-11-06-pressure-report-publication.json。
