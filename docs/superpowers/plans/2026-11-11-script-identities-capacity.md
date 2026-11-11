# 通用脚本身份容量扩展实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新版脚本支持最多64个具名身份，保留既有字段、属性及秘密保护，使外部业务可以声明50个客服身份而不引入平台业务规则。

**Architecture:** 扩展`pressurecooker.script_identities`的数量校验以及前端和MCP的同一契约；旧声明继续只读并拒绝执行。沿用已有编译、导入、不可变发布、环境冻结、分发、复制与脱敏路径。

**Tech Stack:** Go、Vue、Vitest、现有JMeter XML编译器及MCP适配；不增加依赖。

**Spec:** [机器人与排队完整设计，第6.9、18.5及18.11节](../../../../walmart/pressurecooker-test-assets/docs/superpowers/specs/2026-11-11-robot-pressure-complete-design.md)。

当前状态：计划已编写，产品源码尚未修改。64是设计取值，当前实现仍为20；发布/部署与真实业务压测不属于本计划的离线执行。

## Global Constraints

- 新版身份数量≤64；每身份字段≤20；单值≤4096字符，身份值为单行文本。
- 根身份JSON仍受64KiB总量限制；任务显式properties≤100项、环境变量≤100项，分别校验，不扩大这些上限。
- 身份名称、用途、必填和类型只读脚本声明；不内置staff、Cookie必填、账号顺序或沃尔玛分支。
- `schema_version=1`、`script_identity.<name>.<field>`保持不变；旧`jmeter.cookie_accounts`/`cookie_account.*`继续拒绝执行，不自动迁移历史凭据。
- 不改变历史发布字节、任务快照、备份session-key、运行目录和持久卷。保留其他协作改动，提交只包含本计划实际修改。
- 全部平台测试使用仓库内合成fixture，不读取业务资产、凭据或生产，不启动真实JMeter施压。

## Review Focus

1. 64身份并不允许64×20属性同时提交：100/101属性边界独立生效，测试归任务3。
2. 可选身份整组空缺仍可保留64声明；出现一个字段后只校验该身份必填项，测试归任务1、2。
3. 复制旧任务及切换版本不得把旧秘密自动放入新字段，测试归任务3。
4. 编译能生成却导入不了的超64KiB声明必须在同一入口提前拒绝，测试归任务1。
5. 50–64条秘密全部参与分发和脱敏，不能只覆盖前20条或日志末尾，测试归任务3。

## 文件与边界

修改已有`controlplane/manager-go/script_identities.go`、`run_cookie_accounts.go`及对应测试；`plan_cookie_accounts.go`旧声明的20条限制保留。前端改`task-cookie-accounts.js`及身份编辑、创建任务测试；MCP更新`manager_tools.go`和前端目录说明。新增测试文件承载边界矩阵，避免扩大现有混合测试。

executor不新增业务逻辑；仅通过已有签名属性与输入fixture核对数量无截断。CLI转换继续复用`convertScriptIdentitySource`，保留O_EXCL新文件语义。若当前源码因他人改动已实现部分要求，先核差异并复用，不覆盖。

## Task 1: 管理端新版声明、编译及执行属性的数量边界

**Files:**

- Modify: `controlplane/manager-go/script_identities.go`
- Modify: `controlplane/manager-go/run_cookie_accounts.go`
- Test: create `controlplane/manager-go/script_identity_limits_test.go`
- Reference: `controlplane/manager-go/workflow_compile.go`、`plan_cookie_accounts.go`、`store.go`

**Interfaces:**

- Consumes: 既有`Obj`、`normalizeScriptIdentities(raw any) (Obj,error)`、`scriptIdentityMetadata(content string) (Obj,error)`、`validateCookieAccounts(properties Obj) error`。
- Produces: 同一文件内通用常量`maxScriptIdentities=64`、`maxScriptIdentityFields=20`、`maxScriptIdentitiesBytes=64<<10`；上述函数签名不变。
- 新版声明数量扩至64；旧声明解析数量仍20。身份属性结构校验支持64个新版名称，旧命名空间仍由执行门禁拒绝。

- [ ] **Step 1: 增加失败测试。** `TestScriptIdentityCountAndFieldBoundaries`构造中性名称`actor_01`等，断言20/21/50/64条可规范化、65条拒绝；20字段可用、21字段拒绝；重复名称/字段及非法类型仍拒绝。
- [ ] **Step 2: 增加总量与可选身份测试。** `TestScriptIdentityOptionalActivationAndMetadataSize`断言64个可选空身份不要求凭据；仅激活第64项时执行其必填校验；在合法字段/文本长度内构造根JSON恰≤65536字节与>65536字节，规范化、编译、导入均一致拒绝超限。计字节，不计字符。
- [ ] **Step 3: 先运行失败测试。** 在`controlplane/manager-go`运行`go test -count=1 -run 'TestScriptIdentity(CountAndFieldBoundaries|OptionalActivationAndMetadataSize)$' .`；当前21身份用例应失败，确保不是工具链/导入故障。
- [ ] **Step 4: 修改数量校验。** 新版声明、规范化后的编码总量与JMX读取使用同一常量；给`validateCookieAccounts`的新版身份计数使用64，保留旧执行拒绝和旧声明校验，不改变错误的秘密保护。不要全局把20替换成64。
- [ ] **Step 5: 验证回读。** 新增`TestScriptIdentity64WorkflowRoundTrip`经`compileWorkflow`、`importWorkflowGraph`、再编译、`scriptIdentityMetadata`确认64项及类型/required/secret完全保留；运行`go test -count=1 -run 'TestScriptIdentity|TestVersionedScriptIdentities|TestPlanCookieAccount' .`，应全通过。
- [ ] **Step 6: 提交本任务。** 只暂存上述实际差异，提交`feat: support 64 declared script identities`；未执行不得将勾选标为完成。

## Task 2: 前端身份表单和工具目录同步

**Files:**

- Modify: `controlplane/frontend/src/task-cookie-accounts.js`
- Test: `controlplane/frontend/src/task-cookie-accounts.test.js`
- Test: `controlplane/frontend/src/TaskCookieAccountsEditor.test.js`
- Test: `controlplane/frontend/src/CreateRun.test.js`
- Modify: `controlplane/mcp-go/manager_tools.go`
- Modify: `controlplane/frontend/src/mcp-catalog.js`
- Test: create `controlplane/mcp-go/script_identity_limits_test.go`
- Reference: `controlplane/frontend/src/TaskCookieAccountsEditor.vue`

**Interfaces:**

- Consumes: 任务1的声明、属性和总量契约，既有`scriptIdentityRows`、`applyScriptCookieAccounts`、`buildCookieAccountProperties`。
- Produces: 保持已有导出函数与`maxCookieAccounts`导出名，值改64；UI字段仍严格来自声明，秘密默认隐藏，100属性提示保留。MCP描述同步64，不新增业务参数。

- [ ] **Step 1: 写前端边界测试并确认红灯。** 合成64项可选声明，50项有值输出50个属性、14项为空不输出；65声明构建拒绝，未激活槽不索取必填字段。编辑器能显示末尾第64项，其秘密默认隐藏。
- [ ] **Step 2: 写任务切换测试。** `CreateRun.test.js`选择64声明版本、切换无身份/旧声明版本、再显式选择新版；旧前缀不会自动改名，保留既有历史提示。测试不使用staff名称和业务URL。
- [ ] **Step 3: 实现前端上限及说明同步。** 修改通用常量、MCP后端描述、前端MCP目录说明；不将64写成最多可提交属性数。若前端工具fixture含原描述，按现有生成方式更新，不编辑上游无关工具。
- [ ] **Step 4: 验证。** 前端运行`npm test -- src/task-cookie-accounts.test.js src/TaskCookieAccountsEditor.test.js src/CreateRun.test.js`；MCP模块运行`go test -count=1 -run 'TestScriptIdentityLimitCatalog|TestLiveToolCatalogMatchesFrontendFixture' .`。新增`TestScriptIdentityLimitCatalog`检查提交工具的64身份、100项属性和旧身份显式转换说明。
- [ ] **Step 5: 提交。** `feat: align script identity forms and tool contracts`，只提交本任务改动。

## Task 3: 100属性、复制迁移及秘密保护的完整链路回归

**Files:**

- Test: create `controlplane/manager-go/script_identity_capacity_flow_test.go`
- Test: `controlplane/manager-go/run_cookie_accounts_test.go`
- Test: `controlplane/manager-go/cookie_redaction_test.go`
- Test: `controlplane/manager-go/run_copy_test.go`
- Test: `controlplane/manager-go/script_identities_test.go`
- Test: create `controlplane/mcp-go/script_identity_submission_test.go`
- Reference: `controlplane/manager-go/cli.go`、`run_copy.go`、`runner_dispatch.go`
- Reference: `controlplane/execution/cmd/jmeter-executor/inputs.go`

**Interfaces:**

- Consumes: 任务1、2的64声明和表单，既有任务提交/复制、环境解析、派发与CLI转换入口。
- Produces: 自足合成fixture验证50–64身份可提交与安全传输，保留原公共接口；只有实际发现数量截断时才修对应通用路径。

- [ ] **Step 1: 写100/101测试并运行。** `TestIdentityCapacityPropertyBudget`使用50个一字段身份+50普通参数通过结构/任务校验；加第101项拒绝。另用64个身份+36普通属性通过，64×2字段提交因128项拒绝。各值4096/4097及单行限制单独断言。
- [ ] **Step 2: 写末尾身份隐私与分发测试。** `TestIdentityCapacityFreezeDispatchAndRedaction`核64项全部进入已加密/签名的派发属性，第21/50/64个合成秘密及Cookie子值不出现于公开任务/日志/复制响应；跨字节边界日志沿用已有脱敏测试模式。不要断言秘密从内部签名载荷中消失，它必须可供执行脚本使用。
- [ ] **Step 3: 写旧版与CLI文件测试。** `TestIdentityCapacityLegacyConversionIsolation`保持源JMX哈希、历史任务及旧凭据不变；转换器只创建新文件，目标存在则拒绝；旧版可读/导出且执行409，显式新版不接受旧前缀，空新版必填不能从旧属性补值。执行CLI转换不连接业务或数据库。
- [ ] **Step 4: 写MCP提交转发测试。** 用本地httptest管理端接收50个身份的properties，确认无删项/改名、原project/environment/cluster及幂等键不变，响应不回秘密；MCP不能替管理端绕过100属性限制。
- [ ] **Step 5: 最小修复后验证。** manager运行`go test -count=1 -run 'TestIdentityCapacity|TestCookieAccount|TestScriptIdentity|TestVersionedScriptIdentities|TestRunCopy' .`；MCP运行`go test -count=1 -run 'TestIdentityCapacitySubmission|TestCopiedExecutionSubmission|TestTaskEnvironmentForwarding' .`。涉及MySQL的现有测试若无隔离库只能明确跳过，不伪报已运行。
- [ ] **Step 6: 提交。** `test: cover identity capacity privacy and migration`。

## Task 4: 文档、构建和离线交付

**Files:**

- Modify: `docs/流程画布与接口管理.md`
- Modify: `docs/项目与接口复用.md`（仅存在相关限额/迁移描述时）
- Modify: `controlplane/mcp-go/README.md`（仅相关契约）
- Create: `runtime/acceptance/<实际日期>-script-identities-capacity/verification.md`，私有且不提交

**Interfaces:**

- Consumes: 任务1–3的已通过输出。
- Produces: 通用限额说明、实际验证命令/结果及待部署边界；业务统一脚本可据此开发，不能声称已有50客服实测。

- [ ] **Step 1: 更新文档。** 写清64身份、20字段、64KiB总声明、100属性、4096字符及可选激活关系；保留旧版显式转换、重新发布和重新填凭据说明。
- [ ] **Step 2: 按仓库基线验收。** 使用实际Go绝对路径执行`GO=/实际/go bin/test-controlplane.sh`；前端构建及三个Go模块的编译均以真实结果登记。若全量基线因无关已有失败受阻，保留失败及定向结果，不修无关协作差异。
- [ ] **Step 3: 检查交付边界。** 核平台未新增业务名称/协议/外部依赖，运行数据未暂存；列实际变更、构建版本、未部署及未执行业务压测。若后续部署，另按目标实例显式Compose标识/目录/卷及session-key兼容契约准备。
- [ ] **Step 4: 提交文档及实际源码。** `docs: document generic script identity capacity`；不提交runtime回执，不修改历史发布。

## 自检与执行边界

设计中通用身份相关需求覆盖：6.9→任务1–3，18.5→任务1–2及业务计划，18.11→任务3–4。Review Focus五项均有对应断言。所有命令是实施时的验证指令，本次没有运行这些产品测试，也没有宣称通过。

本计划与业务计划分别评审和实施；平台测试不依赖业务计划产物。产品实现前须完成计划审阅，后续真实发布/压测仍按确定的项目、资源与执行范围进行。
