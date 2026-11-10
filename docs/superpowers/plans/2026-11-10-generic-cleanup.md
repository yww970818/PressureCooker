# 通用字段与失效配置清理 Implementation Plan

> **For agentic workers:** Use test-driven-development; independent authentication and plugin work may run in parallel. Preserve all existing working-tree content outside this scope.

**Goal:** 修复审计发现的认证字段硬编码、失效插件无法删除和未知变量阻塞保存，并清理相关历史兼容逻辑与业务文案。

**Architecture:** 沿用 Go/Vue 及现有声明、项目插件 CAS、变量生成器。身份字段按脚本声明生成；失效配置只作为不可执行数据展示，用户明确删除。删除隐式补 Cookie 必填、未声明身份的特殊认证和固定业务字段，不迁移历史数据。

**Spec:** AGENTS.md、本聊天的审计结果及用户“解决问题”“清理掉历史不用的，兼容性的东西也改掉”。

## Global Constraints

- 仓库没有 HEAD，原地修改，先保存 runtime/acceptance/20261110-generic-cleanup/before；不提交、不部署、不修改历史与数据库。
- 脚本 wire marker/prefix 无需改名；字段/必填项来自声明，默认空，不内置 staff_id 或业务角色。
- 未知规则、失效插件不能执行；保留原文并允许显式删除，不增加旧业务解释或读时数据迁移。
- 不启动真实业务压测；凭据不打印。测试以本机通用夹具为准。

## Review Focus

- 自定义凭据字段的 UI、传输、必填校验和脱敏一致，不能丢值或泄露。
- 切换脚本时只按声明复用匹配凭据，未声明字段/身份不能被执行。
- 失效插件删除仍经过项目隔离、归档只读、CAS；不得改变集合或历史引用。
- 保存未知变量不等于支持执行；创建、编辑无效新规则的错误不能伪装成成功执行。
- 清理只作用于用户明确选择的条目；不批量改写历史或隐式删除未知数据。

### Task 1: 声明驱动认证字段

**Files:** manager-go/{plan_cookie_accounts,run_cookie_accounts}*.go，frontend/src/{task-cookie-accounts,TaskCookieAccountsEditor,CreateRun}*，MCP 工具说明与测试夹具。

- [x] 写并运行失败用例：cookie+user_id 声明、可选字段、脱敏、没有字段声明时不隐式要求 Cookie、未声明身份拒绝。
- [x] 以声明字段列表驱动 UI/序列化/后端校验，删除固定三字段与历史 Cookie 补全；保留 wire 名称。
- [x] 运行相关前后端测试，更新 MCP 文档/目录夹具。

### Task 2: 失效插件显式清理

**Files:** manager-go/interface_plugins*.go，frontend/src/api-management/interface-plugins/{project-plugins,InterfacePluginManager}*。

- [x] 写并运行失败用例：失效 installed ID 可见且可删除/卸载，不可安装/读取定义。
- [x] 目录单独返回 unavailable 安装 ID；仅允许显式删除，不伪造可执行插件，保留现有 CAS/项目边界。
- [x] 前端显示失效记录及删除按钮；清理“访客”“客服消息展示”预览文案。
- [x] 运行定向测试。

### Task 3: 变量保存与执行边界

**Files:** manager-go/{collection_environments,environment_random,environments}*.go，frontend/src/api-management/{environments,variable-rules,EnvironmentVariableValue,CollectionEnvironmentEditor}*。

- [x] 写并运行失败用例：未知已存规则可保存、原文不变、执行拒绝；编辑器提示不支持并允许明确清除。
- [x] 分开配置结构校验与执行解析；保存不解释未知规则，执行保持失败关闭；移除为兼容而触发的隐式规则解释。
- [x] 更新操作说明和测试；不新增业务规则。

### Task 4: 整合验收

- [x] 运行完整基线：Go及平台工具通过；前端默认超时失败后，以显式Node24、单worker、30秒命令行期限复核，117文件/991测试全过，生产构建通过。
- [x] 检查新旧文件差异、通用字段、脱敏、未知清理与历史不变。审查代理消息传输为空，根代理完成逐项复核，未取得独立审查结论。
- [x] 记录结果与未覆盖项到 runtime/acceptance/20261110-generic-cleanup/report.md。
