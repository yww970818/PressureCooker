# JMeter 统一工作区 Implementation Plan

> **For agentic workers:** 使用已划定文件责任并行实现，分别完成有意义的回归与范围复核；root统一集成/部署/浏览器验证。

**Goal:** 将脚本与流程融合，提供精确双预览和年轻轻快的统一Vue界面。

**Architecture:** 保留不可变JMX、可编辑加密草稿和显式版本关联。前端单一脚本工作区组合真实LogicFlow预览/编辑与代码预览；接口管理保持真实Scalar与双请求模式。

**Tech Stack:** Vue3、Go、MySQL、LogicFlow2.2.5、Scalar3.23.6。

**Spec:** ../specs/2026-11-05-jmeter-unified-workspace-design.md

## Global Constraints

- 全部自研master后端Go，前端Vue；五项中文导航；不新增常驻容器或报告生成。
- 历史脚本预览与编辑必须来自精确plan快照，未保存输入/OCC保护保留。
- 真正的LogicFlow和Scalar；现有加密、会话与代理隔离保留。

## Review Focus

- 同名旧版本/草稿已前进：预览和基于脚本编辑不能取错内容。
- 只读预览与页面切换：不能创建数据或丢失未保存编辑。
- 多来源多版本关系：按实体版本保留，来源删除仍能看脚本。
- 大/未知JMX：源码仍可用，不执行内容，未知插件保持原始XML。
- Scalar环境输入并发与异常：不丢扩展字段，不死锁；响应文本不改变数字精度。

## Task 1 Go预览与关联

Owner: jmeter_go_manager，manager-go/**。先写预览无副作用/稳定图、多关系/OCC/备份回归并确认失败；按spec实现graph及双向links接口、import/publish关系事务、新表/备份兼容；race/vet与随机前缀真实MySQL。接口和字段以spec为准；输出linux binary/tests及SHA，不自行部署。

## Task 2 融合脚本工作区

Owner: jmeter_vue_ui，frontend/src/script-library/**、frontend/src/workflow/**、Plans.vue。消费spec API，输出 <Plans :notify> 或同名封装，内部完成统一列表/选择/只读结构树、流程图、源码预览/版本/原位草稿编辑/发布联动。以列表选中精确版本、无副作用预览、切换与未保存保护写回归，真实LogicFlow与代码编辑器。不能更改共享Workspace、package、全局CSS。

## Task 3 Scalar视觉

Owner: jmeter_scalar_ui，frontend/src/api-management/**。保留当前已通过的环境/response修复，仅按新参考重做局部布局与样式。缩减头部、扩大编辑区、主题token与全局一致；原有业务测试保留，变更交互添加针对性验证。不改共享文件与第三方源码。

## Task 4 共享视觉与集成

Owner: root，Workspace.vue、styles.css、ui/**、package/lock、相关共享tests。按概念提取token与字体/间距，五导航、Plans懒加载/KeepAlive、同页编辑区高度/移动端滚动、主题浅/深/系统及三主色选择；修改共享导航测试，保留任务/集群/MCP功能。概念与浏览器截图必须逐一view_image对照。

## Task 5 验证与部署

Root统一运行前端测试构建、Go真实MySQL，独立增量复核；新表切换前备份并构建镜像，核源码与二进制SHA，仅重建manager。浏览器验证融合/两类预览/历史版本精确编辑与发布/Scalar两模式/窄屏；需要执行时仅请求内部fixture，归档原文件并核验。最终fixture关闭、10容器健康、队列空。更新文档、清单、checksum。无commit/push要求。

- [x] 后端实现与验证
- [x] 融合工作区实现与验证
- [x] Scalar与共享视觉实现
- [x] 独立复核、真实MySQL、构建与部署
- [x] 浏览器/参考图对照、文档/校验与最终状态


## 当前完整交付清单（按用户最新要求重新编排）

### 1. 统一脚本工作区
- [x] 删除独立画布导航，五个入口；脚本列表与画布在同页。
- [x] 单一脚本列表可检索全部脚本与草稿；草稿/已发布仅作状态和版本选项。
- [x] 选中、筛选、加载/空/失败状态明确；切换主导航保留工作上下文。

### 2. 四种查看方式
- [x] 结构预览：JMeter父子树，按order排序，展开/折叠、查看组件参数。
- [x] 流程预览：真实只读LogicFlow，可缩放与适应画布。
- [x] JMX源码：精确原文、只读代码、复制/换行；未保存画布走无写入编译预览。
- [x] 版本记录：切换精确版本，四视图不取错内容、不产生新草稿。

### 3. 原位编辑与发布
- [x] 同一详情区开启画布编辑，不独立跳页或返回预览页。
- [x] 上传、导入、新建、节点/参数/顺序编辑、保存、发布及来源链接可用。
- [x] 历史脚本编辑显式建立精确快照的新草稿，保存不覆盖期间新增输入。
- [x] 未保存切换确认/OCC冲突本地保留/发布刷新列表，导入未知XML属性保留。

### 4. Go与MySQL
- [x] 纯只读graph端点与临时编译端点，认证/CSRF/解析边界及零写入回归。
- [x] plan_workflow_links双向关联与事务、多源多版本、删除来源、旧脚本无来源兼容。
- [x] 新表纳入backup/import-state/restore与随机前缀测试；旧备份无数组可恢复。

### 5. 年轻轻快视觉和可选主题
- [x] 白色表面、近白淡紫背景、轻圆角、轻阴影、紧凑工具栏及充足编辑区。
- [x] 浅色/深色/跟随系统与紫蓝/薄荷/天空蓝主色，键盘可操作、本机记忆。
- [x] 全局、画布、结构、代码、Scalar/传送弹窗同步主题与可读对比。

### 6. 接口管理
- [x] 集合头/次级菜单/请求操作简化布局，扩大真实Scalar请求与响应区。
- [x] 保留集合CRUD、auth/env、版本恢复、OpenAPI导入导出及direct/master双模式。
- [x] 保留事件并发/异常/销毁队列保护、完整元数据、文本解码与大整数原文。

### 7. 其他页面统一
- [x] 任务、集群、MCP、登录、弹窗及原始日志展示同步主题，既有功能与中文保留。
- [x] 桌面及390px布局无页面横向溢出；编辑器各栏按需滚动，尊重减少动效。

### 8. 验证、部署及交付
- [x] scoped回归、完整npm test/build、Go race/vet、真实MySQL、独立增量复核。
- [x] 备份后构建/部署manager，源码和binary校验，保留旧镜像/前端回退点。
- [x] 浏览器四视图/精确版本原位编辑与发布/主题/Scalar双模式/错误控制台验证。
- [x] 自有fixture六节点执行与MinIO原始文件核验；结束关闭fixture、10常驻健康、队列空。
- [x] view_image逐一对照概念与浏览器截图，修正漂移；更新使用文档/registry/源清单。
